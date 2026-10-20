package tasks

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/metrics"

	"github.com/segmentio/kafka-go"
)

const kafkaBacklogMaxAge = 30 * time.Second

func isKafkaPipeline(name string) bool {
	switch name {
	case PipelineUserBehaviorProjection, PipelineNotificationProjection, PipelineRecommendationMetrics, PipelineLikeSnapshotProjection, PipelinePostEmbedding:
		return true
	default:
		return false
	}
}

type kafkaConsumerSession struct {
	clientID string
	topic    string
	groupID  string
}

// workerKafkaSessions is guarded by workerPipelines.Mutex so session changes
// and their version increments are atomic with pipeline failure state.
var workerKafkaSessions = make(map[string]map[string]kafkaConsumerSession)

type kafkaConsumerReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type trackedKafkaConsumerReader struct {
	reader      kafkaConsumerReader
	unregister  func()
	sessionOnce sync.Once
	closeOnce   sync.Once
	closeErr    error
}

func trackKafkaConsumerReader(pipeline, topic, groupID, clientID string, reader kafkaConsumerReader) *trackedKafkaConsumerReader {
	return &trackedKafkaConsumerReader{
		reader:     reader,
		unregister: registerKafkaConsumerSession(pipeline, topic, groupID, clientID),
	}
}

func (r *trackedKafkaConsumerReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	message, err := r.reader.FetchMessage(ctx)
	if err != nil && (ctx == nil || ctx.Err() == nil) {
		// A failed live fetch ends this reader's recovery evidence before the
		// pipeline failure is recorded by the consumer.
		r.unregisterSession()
	}
	return message, err
}

func (r *trackedKafkaConsumerReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	return r.reader.CommitMessages(ctx, messages...)
}

func (r *trackedKafkaConsumerReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.unregisterSession()
		if r.reader != nil {
			r.closeErr = r.reader.Close()
		}
	})
	return r.closeErr
}

func (r *trackedKafkaConsumerReader) unregisterSession() {
	if r == nil {
		return
	}
	r.sessionOnce.Do(func() {
		if r.unregister != nil {
			r.unregister()
		}
	})
}

func registerKafkaConsumerSession(pipeline, topic, groupID, clientID string) func() {
	pipeline = strings.TrimSpace(pipeline)
	session := kafkaConsumerSession{
		clientID: strings.TrimSpace(clientID),
		topic:    strings.TrimSpace(topic),
		groupID:  strings.TrimSpace(groupID),
	}
	if pipeline == "" || session.clientID == "" || session.topic == "" || session.groupID == "" {
		return func() {}
	}

	workerPipelines.Lock()
	if _, found := workerPipelines.states[pipeline]; !found {
		workerPipelines.Unlock()
		return func() {}
	}
	sessions := workerKafkaSessions[pipeline]
	if sessions == nil {
		sessions = make(map[string]kafkaConsumerSession)
		workerKafkaSessions[pipeline] = sessions
	}
	sessions[session.clientID] = session
	state := workerPipelines.states[pipeline]
	state.sessionVersion = workerPipelineVersion.Add(1)
	workerPipelines.states[pipeline] = state
	workerPipelines.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			workerPipelines.Lock()
			if sessions := workerKafkaSessions[pipeline]; sessions != nil {
				if _, found := sessions[session.clientID]; found {
					delete(sessions, session.clientID)
					if len(sessions) == 0 {
						delete(workerKafkaSessions, pipeline)
					}
					if state, found := workerPipelines.states[pipeline]; found {
						state.sessionVersion = workerPipelineVersion.Add(1)
						workerPipelines.states[pipeline] = state
					}
				}
			}
			workerPipelines.Unlock()
		})
	}
}

type kafkaRecoveryCandidate struct {
	pipeline          string
	failureVersion    uint64
	sessionVersion    uint64
	sampleVersion     uint64
	expectedWorkers   int
	activeWorkers     int
	sessions          []kafkaConsumerSession
	potentialRecovery bool
}

func beginKafkaBacklogSample(name string) (uint64, kafkaRecoveryCandidate) {
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[name]
	if !found {
		return 0, kafkaRecoveryCandidate{}
	}
	state.backlogSampleVersion++
	sampleVersion := state.backlogSampleVersion
	candidate := kafkaRecoveryCandidate{
		pipeline:        name,
		failureVersion:  state.failureVersion,
		sessionVersion:  state.sessionVersion,
		sampleVersion:   sampleVersion,
		expectedWorkers: state.expectedWorkers,
		activeWorkers:   state.activeWorkers,
	}
	if state.consecutiveFailures > 0 && state.failureKinds == pipelineFailureTransportOnly {
		candidate.potentialRecovery = true
		for _, session := range workerKafkaSessions[name] {
			candidate.sessions = append(candidate.sessions, session)
		}
	}
	workerPipelines.states[name] = state
	return sampleVersion, candidate
}

func recordKafkaBacklogSample(name string, sampleVersion uint64, backlog int64, now time.Time, valid bool) bool {
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[name]
	if !found || sampleVersion == 0 || state.backlogSampleVersion != sampleVersion {
		return false
	}
	state.backlogSampleValid = valid && backlog >= 0
	if state.backlogSampleValid {
		setPipelineBacklog(&state, backlog, now)
		state.backlogSampleAt = now
	}
	workerPipelines.states[name] = state
	return true
}

func pipelineKafkaBacklogSample(name string, backlog int64, now time.Time, valid bool) {
	sampleVersion, _ := beginKafkaBacklogSample(name)
	recordKafkaBacklogSample(name, sampleVersion, backlog, now, valid)
}

func kafkaGroupMatchesLocalSessions(description eventing.KafkaGroupDescription, groupID string, candidate kafkaRecoveryCandidate) bool {
	if description.GroupID != groupID || description.State != "Stable" || len(candidate.sessions) == 0 {
		return false
	}
	members := make(map[string]eventing.KafkaGroupMember, len(description.Members))
	for _, member := range description.Members {
		if member.ClientID != "" {
			members[member.ClientID] = member
		}
	}
	for _, session := range candidate.sessions {
		if session.groupID != groupID {
			return false
		}
		member, found := members[session.clientID]
		if !found || !containsKafkaTopic(member.Topics, session.topic) {
			return false
		}
	}
	return true
}

func containsKafkaTopic(topics []string, topic string) bool {
	for _, candidate := range topics {
		if candidate == topic {
			return true
		}
	}
	return false
}

func kafkaFetchFailureKind(err error) pipelineFailureKinds {
	for _, configurationError := range []error{
		kafka.UnknownTopicOrPartition,
		kafka.InvalidTopic,
		kafka.InvalidGroupId,
		kafka.TopicAuthorizationFailed,
		kafka.GroupAuthorizationFailed,
		kafka.ClusterAuthorizationFailed,
		kafka.BrokerAuthorizationFailed,
		kafka.UnsupportedSASLMechanism,
		kafka.IllegalSASLState,
		kafka.UnsupportedVersion,
		kafka.SecurityDisabled,
		kafka.SASLAuthenticationFailed,
	} {
		if errors.Is(err, configurationError) {
			return pipelineFailureConfiguration
		}
	}
	return pipelineFailureTransport
}

func markKafkaIdleRecovered(candidate kafkaRecoveryCandidate, now time.Time) bool {
	workerPipelines.Lock()
	state, found := workerPipelines.states[candidate.pipeline]
	if workerShuttingDown.Load() || !found || state.failureVersion != candidate.failureVersion || state.sessionVersion != candidate.sessionVersion ||
		state.backlogSampleVersion != candidate.sampleVersion || state.expectedWorkers != candidate.expectedWorkers || state.activeWorkers != candidate.activeWorkers || state.failureKinds != pipelineFailureTransportOnly ||
		state.consecutiveFailures == 0 || !state.registered || state.activeWorkers < state.expectedWorkers ||
		state.backlog != 0 || !state.backlogSampleValid || state.backlogSampleAt.IsZero() || now.Sub(state.backlogSampleAt) > kafkaBacklogMaxAge ||
		len(candidate.sessions) != state.activeWorkers || len(candidate.sessions) < state.expectedWorkers || len(workerKafkaSessions[candidate.pipeline]) != state.activeWorkers {
		workerPipelines.Unlock()
		return false
	}
	for _, session := range candidate.sessions {
		if current, exists := workerKafkaSessions[candidate.pipeline][session.clientID]; !exists || current != session {
			workerPipelines.Unlock()
			return false
		}
	}
	failuresBefore := state.consecutiveFailures
	state.consecutiveFailures = 0
	state.failureKinds = 0
	state.failureVersion = workerPipelineVersion.Add(1)
	state.lastSuccessAt = now
	state.lastProgressAt = now
	state.state = "idle"
	state.reasonCode = ""
	workerPipelines.states[candidate.pipeline] = state
	workerPipelines.Unlock()
	log.Printf("[WorkerPipeline] name=%s recovery_type=kafka_idle_recovered failures_before=%d lag=%d result=success", candidate.pipeline, failuresBefore, state.backlog)
	return true
}

type kafkaLagQuery func(context.Context, string, string) (int64, error)
type kafkaGroupQuery func(context.Context, string) (eventing.KafkaGroupDescription, error)

func sampleKafkaPipelineBacklog(ctx context.Context, pipeline, topic, groupID string, lagQuery kafkaLagQuery, groupQuery kafkaGroupQuery) (int64, time.Time, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sampleVersion, candidate := beginKafkaBacklogSample(pipeline)
	lag, err := lagQuery(ctx, topic, groupID)
	now := time.Now().UTC()
	valid := err == nil && lag >= 0
	sampleAccepted := recordKafkaBacklogSample(pipeline, sampleVersion, lag, now, valid)
	if !valid || lag != 0 || !sampleAccepted || !candidate.potentialRecovery || workerShuttingDown.Load() || ctx.Err() != nil {
		return lag, now, valid, err
	}
	if len(candidate.sessions) != candidate.activeWorkers || candidate.expectedWorkers < 1 || candidate.activeWorkers < candidate.expectedWorkers {
		return lag, now, valid, err
	}
	description, groupErr := groupQuery(ctx, groupID)
	if groupErr != nil || ctx.Err() != nil || !kafkaGroupMatchesLocalSessions(description, groupID, candidate) {
		return lag, now, valid, err
	}
	markKafkaIdleRecovered(candidate, time.Now().UTC())
	return lag, now, valid, err
}

func refreshKafkaCommittedBacklogs(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	sampleCtx, cancel := context.WithTimeout(ctx, pipelineMetricsSampleTimeout)
	defer cancel()
	if config.AppConfig == nil {
		return
	}
	cfg := config.AppConfig.Kafka
	groups := []struct{ pipeline, topic, group string }{
		{PipelineUserBehaviorProjection, cfg.UserBehaviorTopic, cfg.UserBehaviorGroupID},
		{PipelineLikeSnapshotProjection, cfg.LikeSnapshotTopic, cfg.LikeSnapshotGroupID},
		{PipelineRecommendationMetrics, cfg.RecommendationEventsTopic, cfg.RecommendationMetricsGroupID},
	}
	if notificationConsumerConfigured() {
		groups = append(groups, struct{ pipeline, topic, group string }{PipelineNotificationProjection, cfg.ActivityEventsTopic, cfg.NotificationGroupID})
	}
	if config.AppConfig.Embedding.Enabled {
		groups = append(groups, struct{ pipeline, topic, group string }{PipelinePostEmbedding, cfg.PostEmbeddingTopic, cfg.PostEmbeddingGroupID})
	}
	for _, entry := range groups {
		lag, sampledAt, valid, err := sampleKafkaPipelineBacklog(
			sampleCtx,
			entry.pipeline,
			entry.topic,
			entry.group,
			func(ctx context.Context, topic, group string) (int64, error) {
				return eventing.KafkaCommittedLag(ctx, cfg, topic, group)
			},
			func(ctx context.Context, group string) (eventing.KafkaGroupDescription, error) {
				return eventing.KafkaConsumerGroup(ctx, cfg, group)
			},
		)
		metrics.RecordKafkaCommittedLagSample(entry.group, entry.topic, lag, sampledAt, valid)
		if !valid {
			if sampleCtx.Err() == nil {
				if err != nil {
					log.Printf("[Metrics] committed Kafka lag group=%s topic=%s: %v", entry.group, entry.topic, err)
				} else {
					log.Printf("[Metrics] committed Kafka lag group=%s topic=%s: invalid sample", entry.group, entry.topic)
				}
			}
		} else if entry.pipeline == PipelineNotificationProjection {
			metrics.SetNotificationConsumerLag(float64(lag))
		}
		if sampleCtx.Err() != nil {
			return
		}
	}
}
