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

type kafkaConsumerReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type kafkaBatchFetchResult struct {
	Messages         []kafka.Message
	TerminalFetchErr error
	ContextErr       error
}

type kafkaBatchProcessor func(context.Context, []kafka.Message) error
type kafkaBatchFailureClassifier func(error) (string, pipelineFailureKinds, bool)

func fetchKafkaBatch(ctx context.Context, reader interface {
	FetchMessage(context.Context) (kafka.Message, error)
}, batchSize int, window time.Duration) kafkaBatchFetchResult {
	if ctx == nil {
		return kafkaBatchFetchResult{ContextErr: errors.New("Kafka batch context is nil")}
	}
	if reader == nil {
		return kafkaBatchFetchResult{ContextErr: errors.New("Kafka batch reader is nil")}
	}
	if batchSize < 1 {
		return kafkaBatchFetchResult{ContextErr: errors.New("Kafka batch size must be positive")}
	}
	first, err := reader.FetchMessage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return kafkaBatchFetchResult{ContextErr: ctx.Err()}
		}
		invalidateKafkaReaderSession(reader)
		return kafkaBatchFetchResult{TerminalFetchErr: err}
	}
	return collectKafkaBatch(ctx, reader, first, batchSize, window)
}

func collectKafkaBatch(ctx context.Context, reader interface {
	FetchMessage(context.Context) (kafka.Message, error)
}, first kafka.Message, batchSize int, window time.Duration) kafkaBatchFetchResult {
	result := kafkaBatchFetchResult{Messages: []kafka.Message{first}}
	if batchSize <= 1 || len(result.Messages) >= batchSize || window <= 0 {
		return result
	}
	collectCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	for len(result.Messages) < batchSize {
		message, err := reader.FetchMessage(collectCtx)
		if err != nil {
			if ctx.Err() != nil {
				result.ContextErr = ctx.Err()
				return result
			}
			if errors.Is(err, context.DeadlineExceeded) && errors.Is(collectCtx.Err(), context.DeadlineExceeded) {
				return result
			}
			invalidateKafkaReaderSession(reader)
			result.TerminalFetchErr = err
			return result
		}
		result.Messages = append(result.Messages, message)
	}
	return result
}

func finishKafkaBatch(
	ctx context.Context,
	pipeline string,
	reader interface{},
	result kafkaBatchFetchResult,
	process kafkaBatchProcessor,
	commit func(context.Context, ...kafka.Message) error,
	classifyProcessFailure kafkaBatchFailureClassifier,
) (bool, error) {
	if result.TerminalFetchErr != nil {
		invalidateKafkaReaderSession(reader)
	}
	if result.ContextErr != nil {
		return false, result.ContextErr
	}
	if ctx == nil {
		return false, errors.New("Kafka batch context is nil")
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if len(result.Messages) == 0 {
		if result.TerminalFetchErr != nil {
			failKafkaConsumerForReader(reader, pipeline, "kafka_fetch_failed", kafkaFetchFailureKind(result.TerminalFetchErr))
			return false, result.TerminalFetchErr
		}
		return false, nil
	}
	if process != nil {
		if err := process(ctx, result.Messages); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			reason, kind, record := "projection_failed", pipelineFailureProcessing, true
			if classifyProcessFailure != nil {
				reason, kind, record = classifyProcessFailure(err)
			}
			if result.TerminalFetchErr != nil {
				kind |= kafkaFetchFailureKind(result.TerminalFetchErr)
				if !record {
					reason, record = "kafka_fetch_failed", true
				}
			}
			if record {
				failKafkaConsumerForReader(reader, pipeline, reason, kind)
			}
			return false, err
		}
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if commit == nil {
		return false, errors.New("Kafka batch commit function is nil")
	}
	if err := commit(ctx, result.Messages...); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		kind := pipelineFailureCommit
		if result.TerminalFetchErr != nil {
			kind |= kafkaFetchFailureKind(result.TerminalFetchErr)
		}
		failKafkaConsumerForReader(reader, pipeline, "kafka_commit_failed", kind)
		return false, err
	}
	commitKafkaConsumerForReader(reader, pipeline, time.Now().UTC())
	if result.TerminalFetchErr != nil {
		failKafkaConsumerForReader(reader, pipeline, "kafka_fetch_failed", kafkaFetchFailureKind(result.TerminalFetchErr))
		return true, result.TerminalFetchErr
	}
	return true, nil
}

type trackedKafkaConsumerReader struct {
	reader      kafkaConsumerReader
	pipeline    string
	slotID      int
	clientID    string
	unregister  func()
	sessionOnce sync.Once
	closeOnce   sync.Once
	closeErr    error
}

func trackKafkaConsumerReader(pipeline string, slotID int, topic, groupID, clientID string, reader kafkaConsumerReader) *trackedKafkaConsumerReader {
	return &trackedKafkaConsumerReader{
		reader:     reader,
		pipeline:   pipeline,
		slotID:     slotID,
		clientID:   clientID,
		unregister: registerKafkaConsumerSession(pipeline, slotID, topic, groupID, clientID),
	}
}

func (r *trackedKafkaConsumerReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	return r.reader.FetchMessage(ctx)
}

func (r *trackedKafkaConsumerReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	return r.reader.CommitMessages(ctx, messages...)
}

func (r *trackedKafkaConsumerReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.reader != nil {
			r.closeErr = r.reader.Close()
		}
		r.unregisterSession()
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

func registerKafkaConsumerSession(pipeline string, slotID int, topic, groupID, clientID string) func() {
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
	state, found := workerPipelines.states[pipeline]
	if !found || !isKafkaPipeline(pipeline) {
		workerPipelines.Unlock()
		return func() {}
	}
	slotState, found := state.kafkaSlots[slotID]
	if !found || !slotState.workerStarted {
		workerPipelines.Unlock()
		return func() {}
	}
	slotState.session = session
	slotState.sessionActive = true
	slotState.sessionRecoveryEligible = true
	slotState.sessionVersion++
	state.kafkaSlots[slotID] = slotState
	workerPipelines.states[pipeline] = state
	workerPipelines.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			workerPipelines.Lock()
			if state, found := workerPipelines.states[pipeline]; found {
				if slotState, found := state.kafkaSlots[slotID]; found && slotState.sessionActive && slotState.session == session {
					slotState.session = kafkaConsumerSession{}
					slotState.sessionActive = false
					slotState.sessionRecoveryEligible = false
					slotState.sessionVersion++
					state.kafkaSlots[slotID] = slotState
					workerPipelines.states[pipeline] = state
				}
			}
			workerPipelines.Unlock()
		})
	}
}

type kafkaRecoveryCandidate struct {
	pipeline          string
	sampleVersion     uint64
	expectedWorkers   int
	activeWorkers     int
	slots             []kafkaRecoverySlot
	potentialRecovery bool
}

type kafkaRecoverySlot struct {
	slotID         int
	failureVersion uint64
	sessionVersion uint64
	session        kafkaConsumerSession
	failureKinds   pipelineFailureKinds
	failures       int
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
		sampleVersion:   sampleVersion,
		expectedWorkers: state.expectedWorkers,
		activeWorkers:   state.activeWorkers,
	}
	if state.consecutiveFailures > 0 && state.failureKinds == pipelineFailureTransportOnly && state.unattributedFailures == 0 {
		candidate.potentialRecovery = true
		for slotID := 0; slotID < state.expectedWorkers; slotID++ {
			slot, found := state.kafkaSlots[slotID]
			if !found || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible {
				candidate.potentialRecovery = false
				continue
			}
			candidate.slots = append(candidate.slots, kafkaRecoverySlot{
				slotID: slotID, failureVersion: slot.failureVersion,
				sessionVersion: slot.sessionVersion, session: slot.session,
				failureKinds: slot.failureKinds, failures: slot.consecutiveFailures,
			})
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
	if description.GroupID != groupID || description.State != "Stable" || len(candidate.slots) != candidate.expectedWorkers {
		return false
	}
	members := make(map[string]eventing.KafkaGroupMember, len(description.Members))
	for _, member := range description.Members {
		if member.ClientID != "" {
			members[member.ClientID] = member
		}
	}
	for _, slot := range candidate.slots {
		session := slot.session
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
	if workerShuttingDown.Load() || !found ||
		state.backlogSampleVersion != candidate.sampleVersion || state.expectedWorkers != candidate.expectedWorkers || state.activeWorkers != candidate.activeWorkers || state.failureKinds != pipelineFailureTransportOnly || state.unattributedFailures != 0 ||
		state.consecutiveFailures == 0 || !state.registered || state.activeWorkers < state.expectedWorkers ||
		state.backlog != 0 || !state.backlogSampleValid || state.backlogSampleAt.IsZero() || now.Sub(state.backlogSampleAt) > kafkaBacklogMaxAge ||
		len(candidate.slots) != candidate.expectedWorkers {
		workerPipelines.Unlock()
		return false
	}
	for _, candidateSlot := range candidate.slots {
		slot, exists := state.kafkaSlots[candidateSlot.slotID]
		if !exists || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible || slot.failureVersion != candidateSlot.failureVersion || slot.sessionVersion != candidateSlot.sessionVersion || slot.session != candidateSlot.session ||
			slot.failureKinds != candidateSlot.failureKinds || slot.consecutiveFailures != candidateSlot.failures {
			workerPipelines.Unlock()
			return false
		}
	}
	failuresBefore := state.consecutiveFailures
	for _, candidateSlot := range candidate.slots {
		slot := state.kafkaSlots[candidateSlot.slotID]
		if slot.consecutiveFailures > 0 {
			markKafkaConsumerSlotSuccess(&slot, now, "idle")
			state.kafkaSlots[candidateSlot.slotID] = slot
		}
	}
	state.lastProgressAt = now
	recomputeKafkaPipelineAggregate(&state, "idle")
	workerPipelines.states[candidate.pipeline] = state
	workerPipelines.Unlock()
	log.Printf("[WorkerPipeline] name=%s recovery_type=kafka_idle_recovered failures_before=%d lag=%d result=success", candidate.pipeline, failuresBefore, state.backlog)
	return true
}

type kafkaLagQuery func(context.Context, string, string) (int64, error)
type kafkaGroupQuery func(context.Context, string) (eventing.KafkaGroupDescription, error)

type kafkaBacklogTarget struct {
	pipeline string
	topic    string
	group    string
}

func sampleKafkaPipelineBacklog(ctx context.Context, pipeline, topic, groupID string, lagQuery kafkaLagQuery, groupQuery kafkaGroupQuery) (int64, time.Time, bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sampleVersion, candidate := beginKafkaBacklogSample(pipeline)
	lag, err := lagQuery(ctx, topic, groupID)
	now := time.Now().UTC()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	valid := err == nil && lag >= 0
	sampleAccepted := recordKafkaBacklogSample(pipeline, sampleVersion, lag, now, valid)
	if !valid || lag != 0 || !sampleAccepted || !candidate.potentialRecovery || workerShuttingDown.Load() || ctx.Err() != nil {
		return lag, now, valid, sampleAccepted, err
	}
	if len(candidate.slots) != candidate.expectedWorkers || candidate.expectedWorkers < 1 || candidate.activeWorkers < candidate.expectedWorkers {
		return lag, now, valid, sampleAccepted, err
	}
	description, groupErr := groupQuery(ctx, groupID)
	if groupErr != nil || ctx.Err() != nil || !kafkaGroupMatchesLocalSessions(description, groupID, candidate) {
		return lag, now, valid, sampleAccepted, err
	}
	markKafkaIdleRecovered(candidate, time.Now().UTC())
	return lag, now, valid, sampleAccepted, err
}

func sampleKafkaBacklogGroups(
	ctx context.Context,
	groups []kafkaBacklogTarget,
	overallBudget time.Duration,
	perGroupBudget time.Duration,
	maxConcurrent int,
	lagQuery kafkaLagQuery,
	groupQuery kafkaGroupQuery,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(groups) == 0 || overallBudget <= 0 || perGroupBudget <= 0 || maxConcurrent < 1 || lagQuery == nil || groupQuery == nil {
		return
	}
	overallCtx, cancel := context.WithTimeout(ctx, overallBudget)
	defer cancel()
	workerCount := len(groups)
	if workerCount > maxConcurrent {
		workerCount = maxConcurrent
	}
	jobs := make(chan kafkaBacklogTarget, len(groups))
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerID := 0; workerID < workerCount; workerID++ {
		go func() {
			defer workers.Done()
			for target := range jobs {
				groupCtx, groupCancel := context.WithTimeout(overallCtx, perGroupBudget)
				lag, sampledAt, valid, accepted, err := sampleKafkaPipelineBacklog(
					groupCtx, target.pipeline, target.topic, target.group, lagQuery, groupQuery,
				)
				groupTimedOut := groupCtx.Err() != nil
				groupCancel()
				if !accepted {
					continue
				}
				metrics.RecordKafkaCommittedLagSample(target.group, target.topic, lag, sampledAt, valid)
				if !valid {
					if overallCtx.Err() == nil && !groupTimedOut {
						if err != nil {
							log.Printf("[Metrics] committed Kafka lag group=%s topic=%s: %v", target.group, target.topic, err)
						} else {
							log.Printf("[Metrics] committed Kafka lag group=%s topic=%s: invalid sample", target.group, target.topic)
						}
					}
				} else if target.pipeline == PipelineNotificationProjection {
					metrics.SetNotificationConsumerLag(float64(lag))
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-overallCtx.Done():
	}
}

func refreshKafkaCommittedBacklogs(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	if config.AppConfig == nil {
		return
	}
	cfg := config.AppConfig.Kafka
	groups := []kafkaBacklogTarget{
		{PipelineUserBehaviorProjection, cfg.UserBehaviorTopic, cfg.UserBehaviorGroupID},
		{PipelineLikeSnapshotProjection, cfg.LikeSnapshotTopic, cfg.LikeSnapshotGroupID},
		{PipelineRecommendationMetrics, cfg.RecommendationEventsTopic, cfg.RecommendationMetricsGroupID},
	}
	if notificationConsumerConfigured() {
		groups = append(groups, kafkaBacklogTarget{PipelineNotificationProjection, cfg.ActivityEventsTopic, cfg.NotificationGroupID})
	}
	if config.AppConfig.Embedding.Enabled {
		groups = append(groups, kafkaBacklogTarget{PipelinePostEmbedding, cfg.PostEmbeddingTopic, cfg.PostEmbeddingGroupID})
	}
	sampleKafkaBacklogGroups(ctx, groups, pipelineMetricsSampleTimeout, 4*time.Second, 5,
		func(ctx context.Context, topic, group string) (int64, error) {
			return eventing.KafkaCommittedLag(ctx, cfg, topic, group)
		},
		func(ctx context.Context, group string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaConsumerGroup(ctx, cfg, group)
		},
	)
}
