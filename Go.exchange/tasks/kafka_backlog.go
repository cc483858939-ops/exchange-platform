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
	clientID         string
	topic            string
	groupID          string
	generationID     int32
	generationScoped bool
	partitionCount   int
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
	if pipeline == "" || session.clientID == "" || session.topic == "" || session.groupID == "" || workerShuttingDown.Load() {
		return func() {}
	}

	workerPipelines.Lock()
	state, found := workerPipelines.states[pipeline]
	if workerShuttingDown.Load() || !found || !isKafkaPipeline(pipeline) {
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
	slotState.membership = kafkaConsumerMembershipState{}
	slotState.postEmbeddingPartitions = nil
	registeredSessionVersion := slotState.sessionVersion
	state.membershipSampleVersion++
	state.kafkaSlots[slotID] = slotState
	workerPipelines.states[pipeline] = state
	workerPipelines.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			workerPipelines.Lock()
			if state, found := workerPipelines.states[pipeline]; found {
				if slotState, found := state.kafkaSlots[slotID]; found && slotState.sessionActive && slotState.session == session &&
					(slotState.sessionVersion == registeredSessionVersion ||
						(slotState.sessionVersion == registeredSessionVersion+1 && !slotState.sessionRecoveryEligible)) {
					slotState.session = kafkaConsumerSession{}
					slotState.sessionActive = false
					slotState.sessionRecoveryEligible = false
					slotState.sessionVersion++
					slotState.membership = kafkaConsumerMembershipState{}
					slotState.postEmbeddingPartitions = nil
					state.membershipSampleVersion++
					state.kafkaSlots[slotID] = slotState
					workerPipelines.states[pipeline] = state
				}
			}
			workerPipelines.Unlock()
		})
	}
}

type kafkaRecoveryCandidate struct {
	pipeline                string
	sampleVersion           uint64
	membershipSampleVersion uint64
	expectedWorkers         int
	activeWorkers           int
	slots                   []kafkaRecoverySlot
	potentialRecovery       bool
}

type kafkaRecoverySlot struct {
	slotID                  int
	exists                  bool
	workerStarted           bool
	sessionActive           bool
	sessionRecoveryEligible bool
	failureVersion          uint64
	sessionVersion          uint64
	session                 kafkaConsumerSession
	failureKinds            pipelineFailureKinds
	failures                int
}

func beginKafkaBacklogSample(name string) (uint64, kafkaRecoveryCandidate) {
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[name]
	if !found {
		return 0, kafkaRecoveryCandidate{}
	}
	state.backlogSampleVersion++
	state.membershipSampleVersion++
	sampleVersion := state.backlogSampleVersion
	candidate := kafkaRecoveryCandidate{
		pipeline:                name,
		sampleVersion:           sampleVersion,
		membershipSampleVersion: state.membershipSampleVersion,
		expectedWorkers:         state.expectedWorkers,
		activeWorkers:           state.activeWorkers,
		potentialRecovery:       state.consecutiveFailures > 0 && state.failureKinds == pipelineFailureTransportOnly && state.unattributedFailures == 0,
	}
	for slotID := 0; slotID < state.expectedWorkers; slotID++ {
		slot, found := state.kafkaSlots[slotID]
		candidate.slots = append(candidate.slots, kafkaRecoverySlot{
			slotID: slotID, exists: found,
			workerStarted: slot.workerStarted, sessionActive: slot.sessionActive,
			sessionRecoveryEligible: slot.sessionRecoveryEligible,
			failureVersion:          slot.failureVersion, sessionVersion: slot.sessionVersion,
			session: slot.session, failureKinds: slot.failureKinds,
			failures: slot.consecutiveFailures,
		})
		if !found || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible {
			candidate.potentialRecovery = false
		}
	}
	workerPipelines.states[name] = state
	return sampleVersion, candidate
}

func recordKafkaBacklogSample(name string, sampleVersion uint64, backlog int64, now time.Time, valid bool) bool {
	if workerShuttingDown.Load() {
		return false
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[name]
	if workerShuttingDown.Load() || !found || sampleVersion == 0 || state.backlogSampleVersion != sampleVersion {
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
	if groupID == "" || candidate.expectedWorkers < 1 || candidate.activeWorkers < candidate.expectedWorkers || description.GroupID != groupID || description.State != "Stable" || len(candidate.slots) != candidate.expectedWorkers {
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
		if !slot.exists || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible || session.clientID == "" || session.topic == "" || session.groupID != groupID {
			return false
		}
		member, found := members[session.clientID]
		if !found || !containsKafkaTopic(member.Topics, session.topic) {
			return false
		}
	}
	return true
}

// recordKafkaMembershipSample applies only the newest background Group sample
// and only while every sampled local slot still identifies the same Reader
// session. A failed or non-Stable result revokes the previous proof.
func recordKafkaMembershipSample(
	ctx context.Context,
	pipeline string,
	candidate kafkaRecoveryCandidate,
	groupID string,
	description eventing.KafkaGroupDescription,
	verifiedAt time.Time,
	querySucceeded bool,
) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[pipeline]
	if ctx.Err() != nil || workerShuttingDown.Load() || !found || candidate.pipeline != pipeline || candidate.expectedWorkers != state.expectedWorkers || len(candidate.slots) != candidate.expectedWorkers ||
		candidate.membershipSampleVersion == 0 || candidate.membershipSampleVersion != state.membershipSampleVersion {
		return false
	}
	for _, sampled := range candidate.slots {
		current, exists := state.kafkaSlots[sampled.slotID]
		if exists != sampled.exists {
			return false
		}
		if exists && (current.workerStarted != sampled.workerStarted || current.sessionActive != sampled.sessionActive || current.sessionRecoveryEligible != sampled.sessionRecoveryEligible || current.sessionVersion != sampled.sessionVersion || current.session != sampled.session) {
			return false
		}
	}
	if ctx.Err() != nil || workerShuttingDown.Load() {
		return false
	}
	verified := querySucceeded && kafkaGroupMatchesLocalSessions(description, groupID, candidate)
	if verified && verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}
	for _, sampled := range candidate.slots {
		if !sampled.exists {
			continue
		}
		slot := state.kafkaSlots[sampled.slotID]
		if verified {
			slot.membership = kafkaConsumerMembershipState{
				Verified: true, VerifiedAt: verifiedAt.UTC(), SessionVersion: slot.sessionVersion,
			}
		} else {
			slot.membership = kafkaConsumerMembershipState{}
		}
		state.kafkaSlots[sampled.slotID] = slot
	}
	workerPipelines.states[pipeline] = state
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

func markKafkaIdleRecovered(ctx context.Context, candidate kafkaRecoveryCandidate, now time.Time) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	workerPipelines.Lock()
	state, found := workerPipelines.states[candidate.pipeline]
	if ctx.Err() != nil || workerShuttingDown.Load() || !found ||
		state.backlogSampleVersion != candidate.sampleVersion || state.membershipSampleVersion != candidate.membershipSampleVersion || state.expectedWorkers != candidate.expectedWorkers || state.activeWorkers != candidate.activeWorkers || state.failureKinds != pipelineFailureTransportOnly || state.unattributedFailures != 0 ||
		state.consecutiveFailures == 0 || !state.registered || state.activeWorkers < state.expectedWorkers ||
		state.backlog != 0 || !state.backlogSampleValid || state.backlogSampleAt.IsZero() || now.Sub(state.backlogSampleAt) > kafkaBacklogMaxAge ||
		len(candidate.slots) != candidate.expectedWorkers {
		workerPipelines.Unlock()
		return false
	}
	for _, candidateSlot := range candidate.slots {
		slot, exists := state.kafkaSlots[candidateSlot.slotID]
		if !exists || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible || !slot.membership.Verified || slot.membership.SessionVersion != slot.sessionVersion || slot.membership.VerifiedAt.IsZero() || now.Sub(slot.membership.VerifiedAt) > kafkaMembershipMaxAge || slot.failureVersion != candidateSlot.failureVersion || slot.sessionVersion != candidateSlot.sessionVersion || slot.session != candidateSlot.session ||
			slot.failureKinds != candidateSlot.failureKinds || slot.consecutiveFailures != candidateSlot.failures {
			workerPipelines.Unlock()
			return false
		}
	}
	if ctx.Err() != nil || workerShuttingDown.Load() {
		workerPipelines.Unlock()
		return false
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

type kafkaLagSampleResult struct {
	lag         int64
	err         error
	completedAt time.Time
}

type kafkaGroupSampleResult struct {
	description eventing.KafkaGroupDescription
	err         error
	completedAt time.Time
}

func kafkaSampleCompletedBeforeDeadline(ctx context.Context, completedAt time.Time) bool {
	if ctx == nil || ctx.Err() != nil || completedAt.IsZero() {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok && completedAt.After(deadline) {
		return false
	}
	return ctx.Err() == nil
}

func sampleKafkaPipelineBacklog(ctx context.Context, pipeline, topic, groupID string, lagQuery kafkaLagQuery, groupQuery kafkaGroupQuery) (int64, time.Time, bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return sampleKafkaPipelineBacklogWithGroupContext(ctx, ctx, pipeline, topic, groupID, lagQuery, groupQuery)
}

func sampleKafkaPipelineBacklogWithGroupContext(
	ctx context.Context,
	groupCtx context.Context,
	pipeline, topic, groupID string,
	lagQuery kafkaLagQuery,
	groupQuery kafkaGroupQuery,
) (int64, time.Time, bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if groupCtx == nil {
		groupCtx = ctx
	}
	sampleVersion, candidate := beginKafkaBacklogSample(pipeline)
	lagResults := make(chan kafkaLagSampleResult, 1)
	groupResults := make(chan kafkaGroupSampleResult, 1)
	go func() {
		if lagQuery == nil {
			lagResults <- kafkaLagSampleResult{err: errors.New("Kafka lag query is nil"), completedAt: time.Now().UTC()}
			return
		}
		lag, err := lagQuery(ctx, topic, groupID)
		lagResults <- kafkaLagSampleResult{lag: lag, err: err, completedAt: time.Now().UTC()}
	}()
	go func() {
		if groupQuery == nil {
			groupResults <- kafkaGroupSampleResult{err: errors.New("Kafka consumer group query is nil"), completedAt: time.Now().UTC()}
			return
		}
		description, err := groupQuery(groupCtx, groupID)
		groupResults <- kafkaGroupSampleResult{description: description, err: err, completedAt: time.Now().UTC()}
	}()

	var lagResult kafkaLagSampleResult
	var groupResult kafkaGroupSampleResult
	lagDone, groupDone := false, false
	groupDoneCh := groupCtx.Done()
collect:
	for !lagDone || !groupDone {
		select {
		case lagResult = <-lagResults:
			lagDone = true
		case groupResult = <-groupResults:
			groupDone = true
			groupDoneCh = nil
		case <-groupDoneCh:
			groupResult = kafkaGroupSampleResult{err: groupCtx.Err(), completedAt: time.Now().UTC()}
			groupDone = true
			groupDoneCh = nil
		case <-ctx.Done():
			break collect
		}
	}
	if !lagDone {
		select {
		case lagResult = <-lagResults:
			lagDone = true
		default:
		}
	}
	if !groupDone {
		select {
		case groupResult = <-groupResults:
			groupDone = true
		default:
		}
	}

	now := time.Now().UTC()
	err := lagResult.err
	if !lagDone {
		err = ctx.Err()
		if err == nil {
			err = context.DeadlineExceeded
		}
	}
	valid := lagDone && err == nil && lagResult.lag >= 0 && kafkaSampleCompletedBeforeDeadline(ctx, lagResult.completedAt)
	if lagDone && !valid && err == nil {
		err = context.DeadlineExceeded
	}
	sampleAccepted := recordKafkaBacklogSample(pipeline, sampleVersion, lagResult.lag, now, valid)

	groupSucceeded := groupDone && groupResult.err == nil && kafkaSampleCompletedBeforeDeadline(groupCtx, groupResult.completedAt)
	membershipAccepted := recordKafkaMembershipSample(
		groupCtx, pipeline, candidate, groupID, groupResult.description, groupResult.completedAt, groupSucceeded,
	)
	groupMatched := groupSucceeded && kafkaGroupMatchesLocalSessions(groupResult.description, groupID, candidate)
	if sampleAccepted && valid && lagResult.lag == 0 && candidate.potentialRecovery && membershipAccepted && groupMatched {
		markKafkaIdleRecovered(groupCtx, candidate, now)
	}
	return lagResult.lag, now, valid, sampleAccepted, err
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
	if len(groups) == 0 || overallBudget <= 0 || perGroupBudget <= 0 || maxConcurrent < 1 {
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
				lag, sampledAt, valid, accepted, err := sampleKafkaPipelineBacklogWithGroupContext(
					overallCtx, groupCtx, target.pipeline, target.topic, target.group, lagQuery, groupQuery,
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
