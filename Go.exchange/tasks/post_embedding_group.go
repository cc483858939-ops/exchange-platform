package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	postEmbeddingReaderQueueCapacity = 4
	postEmbeddingMaxPartitionReaders = 6
	postEmbeddingGroupRPCTimeout     = 5 * time.Second
	postEmbeddingPartitionRetryMin   = 250 * time.Millisecond
	postEmbeddingPartitionRetryMax   = 10 * time.Second
	postEmbeddingCommitAttempts      = 3
)

var (
	errPostEmbeddingCommitGateClosed    = errors.New("post embedding generation commit gate is closed")
	errPostEmbeddingOffsetRegression    = errors.New("post embedding partition offset would move backwards")
	errPostEmbeddingCommitTargetChanged = errors.New("post embedding pending commit target changed")
)

type postEmbeddingGeneration interface {
	ID() int32
	GroupID() string
	MemberID() string
	Assignments() map[string][]kafka.PartitionAssignment
	Start(func(context.Context))
	CommitOffsets(map[string]map[int]int64) error
}

type postEmbeddingConsumerGroup interface {
	Next(context.Context) (postEmbeddingGeneration, error)
	Close() error
}

type kafkaPostEmbeddingConsumerGroup struct {
	group *kafka.ConsumerGroup
}

type kafkaPostEmbeddingGeneration struct {
	generation *kafka.Generation
}

func newKafkaPostEmbeddingConsumerGroup(kafkaConfig config.KafkaConfig, clientID string) (postEmbeddingConsumerGroup, error) {
	brokers := normalizedPostEmbeddingBrokers(kafkaConfig.Brokers)
	topic := strings.TrimSpace(kafkaConfig.PostEmbeddingTopic)
	groupID := strings.TrimSpace(kafkaConfig.PostEmbeddingGroupID)
	if len(brokers) == 0 || topic == "" || groupID == "" || strings.TrimSpace(clientID) == "" {
		return nil, &postEmbeddingLocalConfigurationError{cause: errors.New("post embedding Kafka brokers, topic, group id, and client id are required")}
	}
	group, err := kafka.NewConsumerGroup(kafka.ConsumerGroupConfig{
		ID: groupID, Brokers: brokers, Topics: []string{topic},
		Dialer:      &kafka.Dialer{ClientID: clientID},
		StartOffset: kafka.FirstOffset, Timeout: postEmbeddingGroupRPCTimeout,
	})
	if err != nil {
		return nil, &postEmbeddingLocalConfigurationError{cause: err}
	}
	return &kafkaPostEmbeddingConsumerGroup{group: group}, nil
}

func (g *kafkaPostEmbeddingConsumerGroup) Next(ctx context.Context) (postEmbeddingGeneration, error) {
	if g == nil || g.group == nil {
		return nil, errors.New("post embedding Kafka consumer group is nil")
	}
	generation, err := g.group.Next(ctx)
	if err != nil {
		return nil, err
	}
	return &kafkaPostEmbeddingGeneration{generation: generation}, nil
}

func (g *kafkaPostEmbeddingConsumerGroup) Close() error {
	if g == nil || g.group == nil {
		return nil
	}
	return g.group.Close()
}

func (g *kafkaPostEmbeddingGeneration) ID() int32 {
	if g == nil || g.generation == nil {
		return 0
	}
	return g.generation.ID
}

func (g *kafkaPostEmbeddingGeneration) GroupID() string {
	if g == nil || g.generation == nil {
		return ""
	}
	return g.generation.GroupID
}

func (g *kafkaPostEmbeddingGeneration) MemberID() string {
	if g == nil || g.generation == nil {
		return ""
	}
	return g.generation.MemberID
}

func (g *kafkaPostEmbeddingGeneration) Assignments() map[string][]kafka.PartitionAssignment {
	if g == nil || g.generation == nil {
		return nil
	}
	return g.generation.Assignments
}

func (g *kafkaPostEmbeddingGeneration) Start(fn func(context.Context)) {
	if g != nil && g.generation != nil {
		g.generation.Start(fn)
	}
}

func (g *kafkaPostEmbeddingGeneration) CommitOffsets(offsets map[string]map[int]int64) error {
	if g == nil || g.generation == nil {
		return errors.New("post embedding Kafka generation is nil")
	}
	return g.generation.CommitOffsets(offsets)
}

type postEmbeddingLocalConfigurationError struct{ cause error }

func (e *postEmbeddingLocalConfigurationError) Error() string {
	if e == nil || e.cause == nil {
		return "post embedding local configuration is invalid"
	}
	return "post embedding local configuration is invalid: " + e.cause.Error()
}

func (e *postEmbeddingLocalConfigurationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func isPostEmbeddingLocalConfigurationError(err error) bool {
	var target *postEmbeddingLocalConfigurationError
	return errors.As(err, &target)
}

type postEmbeddingPartitionReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	Close() error
}

type postEmbeddingPartitionReaderFactory func(topic string, partition int, offset int64, clientID string) (postEmbeddingPartitionReader, error)

type postEmbeddingBatchProcessor func(context.Context, []kafka.Message) error

type postEmbeddingGroupRuntime struct {
	topic             string
	groupID           string
	clientID          string
	slotID            int
	batchSize         int
	batchMaxWait      time.Duration
	maxReaders        int
	brokers           []string
	retryPolicy       kafkaRetryPolicy
	retryRuntime      providerRetryRuntime
	providerGate      *postEmbeddingProviderGate
	readerFactory     postEmbeddingPartitionReaderFactory
	processBatch      postEmbeddingBatchProcessor
	onGenerationStart func(int32, []int)
}

func newProductionPostEmbeddingGroupRuntime(kafkaConfig config.KafkaConfig, clientID string, batchSize int, batchMaxWait time.Duration, processor postEmbeddingBatchProcessor, gate *postEmbeddingProviderGate, retryRuntime providerRetryRuntime) postEmbeddingGroupRuntime {
	return postEmbeddingGroupRuntime{
		topic: strings.TrimSpace(kafkaConfig.PostEmbeddingTopic), groupID: strings.TrimSpace(kafkaConfig.PostEmbeddingGroupID),
		clientID: clientID, batchSize: batchSize, batchMaxWait: batchMaxWait,
		maxReaders: postEmbeddingMaxPartitionReaders, brokers: normalizedPostEmbeddingBrokers(kafkaConfig.Brokers), retryPolicy: defaultKafkaRetryPolicy,
		retryRuntime: normalizedProviderRetryRuntime(retryRuntime), providerGate: gate,
		readerFactory: newPostEmbeddingPartitionReader, processBatch: processor,
	}
}

func newPostEmbeddingPartitionReader(topic string, partition int, offset int64, clientID string) (postEmbeddingPartitionReader, error) {
	var brokers []string
	if config.AppConfig != nil {
		brokers = normalizedPostEmbeddingBrokers(config.AppConfig.Kafka.Brokers)
	}
	return newPostEmbeddingPartitionReaderWithBrokers(brokers, topic, partition, offset, clientID)
}

func newPostEmbeddingPartitionReaderWithBrokers(brokers []string, topic string, partition int, offset int64, clientID string) (postEmbeddingPartitionReader, error) {
	if strings.TrimSpace(topic) == "" || partition < 0 || strings.TrimSpace(clientID) == "" {
		return nil, errors.New("post embedding partition reader topic, partition, and client id are required")
	}
	brokers = normalizedPostEmbeddingBrokers(brokers)
	if len(brokers) == 0 {
		return nil, errors.New("post embedding partition reader has no Kafka brokers")
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, Topic: topic, Partition: partition,
		Dialer:        &kafka.Dialer{ClientID: clientID},
		QueueCapacity: postEmbeddingReaderQueueCapacity,
		MinBytes:      1, MaxBytes: 10e6,
	})
	if err := reader.SetOffset(offset); err != nil {
		_ = reader.Close()
		return nil, err
	}
	return reader, nil
}

func normalizedPostEmbeddingBrokers(brokers []string) []string {
	result := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		if broker = strings.TrimSpace(broker); broker != "" {
			result = append(result, broker)
		}
	}
	return result
}

func newPostEmbeddingInstanceClientID() string {
	return "goexchange-post-embedding-" + uuid.NewString()
}

func runPostEmbeddingKafkaGroup(ctx context.Context, group postEmbeddingConsumerGroup, runtime postEmbeddingGroupRuntime) error {
	if ctx == nil {
		return errors.New("post embedding consumer group context is nil")
	}
	if group == nil {
		return errors.New("post embedding consumer group is nil")
	}
	if runtime.processBatch == nil {
		return errors.New("post embedding group batch processor is nil")
	}
	if runtime.topic = strings.TrimSpace(runtime.topic); runtime.topic == "" {
		return errors.New("post embedding group topic is required")
	}
	if runtime.groupID = strings.TrimSpace(runtime.groupID); runtime.groupID == "" {
		return errors.New("post embedding group id is required")
	}
	if runtime.clientID = strings.TrimSpace(runtime.clientID); runtime.clientID == "" {
		return errors.New("post embedding group client id is required")
	}
	if runtime.batchSize < 1 || runtime.batchSize > config.MaxEmbeddingBatchSize {
		return fmt.Errorf("post embedding batch size must be between 1 and %d", config.MaxEmbeddingBatchSize)
	}
	if runtime.batchSize > 1 && (runtime.batchMaxWait <= 0 || runtime.batchMaxWait > time.Duration(config.MaxEmbeddingBatchMaxWaitMs)*time.Millisecond) {
		return fmt.Errorf("post embedding batch max wait must be between 1ms and %dms", config.MaxEmbeddingBatchMaxWaitMs)
	}
	if runtime.retryPolicy.MaxAttempts < 1 {
		runtime.retryPolicy = normalizedKafkaRetryPolicy(runtime.retryPolicy)
	}
	if runtime.retryRuntime.Sleep == nil || runtime.retryRuntime.Now == nil || runtime.retryRuntime.Jitter == nil {
		runtime.retryRuntime = normalizedProviderRetryRuntime(runtime.retryRuntime)
	}
	if runtime.providerGate == nil {
		runtime.providerGate = newPostEmbeddingProviderGate(runtime.retryRuntime)
	}
	if runtime.readerFactory == nil {
		brokers := normalizedPostEmbeddingBrokers(runtime.brokers)
		runtime.readerFactory = func(topic string, partition int, offset int64, clientID string) (postEmbeddingPartitionReader, error) {
			return newPostEmbeddingPartitionReaderWithBrokers(brokers, topic, partition, offset, clientID)
		}
	}
	if runtime.maxReaders < 1 {
		runtime.maxReaders = postEmbeddingMaxPartitionReaders
	}
	if runtime.maxReaders > postEmbeddingMaxPartitionReaders {
		runtime.maxReaders = postEmbeddingMaxPartitionReaders
	}

	for {
		generation, err := group.Next(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if errors.Is(err, kafka.ErrGroupClosed) {
				return err
			}
			KafkaConsumerSlotFailed(PipelinePostEmbedding, runtime.slotID, "kafka_generation_unavailable", pipelineFailureTransport)
			log.Printf("[PostEmbedding] ConsumerGroup.Next failed; retaining group client id=%s: %v", runtime.clientID, err)
			if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryMin); waitErr != nil {
				return waitErr
			}
			continue
		}
		if generation == nil {
			return errors.New("post embedding ConsumerGroup returned a nil generation")
		}
		partitions := generationPartitionIDs(generation.Assignments(), runtime.topic)
		metrics.RecordPostEmbeddingGenerationChange()
		metrics.SetPostEmbeddingAssignedPartitions(len(partitions))
		metrics.SetPostEmbeddingHeldPartitions(0)
		if runtime.onGenerationStart != nil {
			runtime.onGenerationStart(generation.ID(), partitions)
		}
		healthIdentity, generationContext := registerKafkaConsumerGenerationSession(
			PipelinePostEmbedding, runtime.slotID, runtime.topic, runtime.groupID,
			runtime.clientID, generation.ID(), generation.Assignments()[runtime.topic],
		)
		generation.Start(func(generationCtx context.Context) {
			defer generationContext()
			runPostEmbeddingGenerationSupervisor(generationCtx, generation, runtime, healthIdentity)
		})
	}
}

func generationPartitionIDs(assignments map[string][]kafka.PartitionAssignment, topic string) []int {
	ids := make([]int, 0, len(assignments[topic]))
	for _, assignment := range assignments[topic] {
		ids = append(ids, assignment.ID)
	}
	sort.Ints(ids)
	return ids
}

func registerKafkaConsumerGenerationSession(
	pipeline string,
	slotID int,
	topic, groupID, clientID string,
	generationID int32,
	assignments []kafka.PartitionAssignment,
) (postEmbeddingGenerationHealthIdentity, func()) {
	pipeline = strings.TrimSpace(pipeline)
	session := kafkaConsumerSession{
		clientID: strings.TrimSpace(clientID), topic: strings.TrimSpace(topic), groupID: strings.TrimSpace(groupID),
		generationID: generationID, generationScoped: true, partitionCount: len(assignments),
	}
	if pipeline == "" || session.clientID == "" || session.topic == "" || session.groupID == "" || workerShuttingDown.Load() {
		return postEmbeddingGenerationHealthIdentity{}, func() {}
	}
	workerPipelines.Lock()
	state, found := workerPipelines.states[pipeline]
	if workerShuttingDown.Load() || !found || !isKafkaPipeline(pipeline) {
		workerPipelines.Unlock()
		return postEmbeddingGenerationHealthIdentity{}, func() {}
	}
	slot, found := state.kafkaSlots[slotID]
	if !found || !slot.workerStarted {
		workerPipelines.Unlock()
		return postEmbeddingGenerationHealthIdentity{}, func() {}
	}
	slot.session = session
	slot.sessionActive = true
	slot.sessionRecoveryEligible = true
	slot.sessionVersion++
	slot.membership = kafkaConsumerMembershipState{}
	slot.postEmbeddingPartitions = make(map[int]postEmbeddingPartitionHealth, len(assignments))
	for _, assignment := range assignments {
		slot.postEmbeddingPartitions[assignment.ID] = postEmbeddingPartitionHealth{
			LastConfirmedOffset: assignment.Offset,
		}
	}
	identity := postEmbeddingGenerationHealthIdentity{
		pipeline: pipeline, slotID: slotID, generationID: generationID, sessionVersion: slot.sessionVersion,
	}
	state.kafkaSlots[slotID] = slot
	state.membershipSampleVersion++
	workerPipelines.states[pipeline] = state
	workerPipelines.Unlock()

	var once sync.Once
	return identity, func() {
		once.Do(func() {
			workerPipelines.Lock()
			if state, found := workerPipelines.states[pipeline]; found {
				if slot, found := state.kafkaSlots[slotID]; found && slot.sessionActive && slot.sessionVersion == identity.sessionVersion && slot.session == session {
					slot.session = kafkaConsumerSession{}
					slot.sessionActive = false
					slot.sessionRecoveryEligible = false
					slot.sessionVersion++
					slot.membership = kafkaConsumerMembershipState{}
					slot.postEmbeddingPartitions = nil
					state.kafkaSlots[slotID] = slot
					state.membershipSampleVersion++
					workerPipelines.states[pipeline] = state
				}
			}
			workerPipelines.Unlock()
		})
	}
}

func runPostEmbeddingGenerationSupervisor(
	ctx context.Context,
	generation postEmbeddingGeneration,
	runtime postEmbeddingGroupRuntime,
	healthIdentity postEmbeddingGenerationHealthIdentity,
) {
	if ctx == nil || generation == nil {
		return
	}
	assignments := append([]kafka.PartitionAssignment(nil), generation.Assignments()[runtime.topic]...)
	metrics.SetPostEmbeddingAssignedPartitions(len(assignments))
	var heldPartitions atomic.Int32
	commitGate := newPostEmbeddingOffsetCommitCoordinator(ctx, generation, runtime.topic, assignments, healthIdentity)
	partitionCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		commitGate.Close()
		workers.Wait()
		metrics.SetPostEmbeddingAssignedPartitions(0)
		metrics.SetPostEmbeddingHeldPartitions(0)
	}()

	readerSlots := make(chan struct{}, runtime.maxReaders)
	for _, assignment := range assignments {
		assignment := assignment
		workers.Add(1)
		go func() {
			defer workers.Done()
			runPostEmbeddingPartitionWorker(partitionCtx, assignment, runtime, readerSlots, commitGate, &heldPartitions)
		}()
	}
	<-ctx.Done()
}

type postEmbeddingPartitionHold struct {
	active atomic.Bool
	count  *atomic.Int32
}

func (h *postEmbeddingPartitionHold) set(active bool) {
	if h == nil || h.count == nil {
		return
	}
	previous := h.active.Swap(active)
	if previous == active {
		return
	}
	if active {
		h.count.Add(1)
	} else {
		h.count.Add(-1)
	}
	metrics.SetPostEmbeddingHeldPartitions(int(h.count.Load()))
}

func runPostEmbeddingPartitionWorker(
	ctx context.Context,
	assignment kafka.PartitionAssignment,
	runtime postEmbeddingGroupRuntime,
	readerSlots chan struct{},
	commitGate *postEmbeddingOffsetCommitCoordinator,
	heldPartitions *atomic.Int32,
) {
	for ctx.Err() == nil {
		err := runPostEmbeddingPartitionWorkerPass(ctx, assignment, runtime, readerSlots, commitGate, heldPartitions)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("post embedding partition worker returned unexpectedly")
		}
		log.Printf("[PostEmbedding] partition worker restarting topic=%s partition=%d offset=%d err=%v", runtime.topic, assignment.ID, commitGate.LastConfirmedNextOffset(assignment.ID, assignment.Offset), err)
		metrics.RecordPostEmbeddingFailure("partition_worker_exit")
		if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryMin); waitErr != nil {
			return
		}
	}
}

func runPostEmbeddingPartitionWorkerPass(
	ctx context.Context,
	assignment kafka.PartitionAssignment,
	runtime postEmbeddingGroupRuntime,
	readerSlots chan struct{},
	commitGate *postEmbeddingOffsetCommitCoordinator,
	heldPartitions *atomic.Int32,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("post embedding partition worker panic: %v", recovered)
		}
	}()
	partition := assignment.ID
	nextOffset := assignment.Offset
	failureCount := 0
	hold := &postEmbeddingPartitionHold{count: heldPartitions}
	defer hold.set(false)
	for ctx.Err() == nil {
		select {
		case readerSlots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if ctx.Err() != nil {
			<-readerSlots
			return ctx.Err()
		}
		readerClientID := fmt.Sprintf("%s-p%d", runtime.clientID, partition)
		attemptOffset, attemptErr := runPostEmbeddingPartitionReaderAttempt(ctx, assignment, nextOffset, runtime, readerClientID, commitGate, hold)
		<-readerSlots
		if attemptOffset >= nextOffset {
			nextOffset = attemptOffset
		}
		nextOffset = commitGate.LastConfirmedNextOffset(partition, nextOffset)
		if ctx.Err() != nil {
			return
		}
		if attemptErr == nil || errors.Is(attemptErr, io.EOF) {
			failureCount = 0
		} else {
			failureCount++
			log.Printf("[PostEmbedding] partition reader restarting topic=%s partition=%d offset=%d err=%v", runtime.topic, partition, nextOffset, attemptErr)
		}
		if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryDelay(failureCount, attemptErr, runtime.retryPolicy, runtime.retryRuntime)); waitErr != nil {
			return waitErr
		}
	}
	return ctx.Err()
}

func runPostEmbeddingPartitionReaderAttempt(
	ctx context.Context,
	assignment kafka.PartitionAssignment,
	startOffset int64,
	runtime postEmbeddingGroupRuntime,
	clientID string,
	commitGate *postEmbeddingOffsetCommitCoordinator,
	hold *postEmbeddingPartitionHold,
) (nextOffset int64, err error) {
	nextOffset = startOffset
	var reader postEmbeddingPartitionReader
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("post embedding partition worker panic: %v", recovered)
		}
		if reader != nil {
			if closeErr := reader.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
	}()
	var createErr error
	reader, createErr = runtime.readerFactory(runtime.topic, assignment.ID, startOffset, clientID)
	if createErr != nil {
		return startOffset, createErr
	}
	if reader == nil {
		return startOffset, errors.New("post embedding partition reader factory returned nil")
	}
	return consumePostEmbeddingPartitionReader(ctx, reader, assignment.ID, startOffset, runtime, commitGate, hold)
}

func consumePostEmbeddingPartitionReader(
	ctx context.Context,
	reader postEmbeddingPartitionReader,
	partition int,
	nextOffset int64,
	runtime postEmbeddingGroupRuntime,
	commitGate *postEmbeddingOffsetCommitCoordinator,
	hold *postEmbeddingPartitionHold,
) (int64, error) {
	for ctx.Err() == nil {
		fetch := fetchPostEmbeddingBatch(ctx, reader, runtime.batchSize, runtime.batchMaxWait)
		if fetch.ContextErr != nil {
			return nextOffset, fetch.ContextErr
		}
		if len(fetch.Messages) == 0 {
			if fetch.TerminalFetchErr != nil {
				return nextOffset, fetch.TerminalFetchErr
			}
			continue
		}
		if err := validatePostEmbeddingPartitionBatch(fetch.Messages, runtime.topic, partition); err != nil {
			hold.set(true)
			return nextOffset, err
		}
		targetNextOffset := fetch.Messages[len(fetch.Messages)-1].Offset + 1
		commitGate.RecordPartitionBatchPending(partition, targetNextOffset, time.Now().UTC())
		for ctx.Err() == nil {
			processErr := callPostEmbeddingBatchProcessor(ctx, runtime.processBatch, fetch.Messages)
			if processErr == nil {
				hold.set(false)
				break
			}
			if ctx.Err() != nil {
				return nextOffset, ctx.Err()
			}
			hold.set(true)
			metrics.RecordPostEmbeddingFailure("partition_batch")
			log.Printf("[PostEmbedding] holding uncommitted batch topic=%s partition=%d first_offset=%d last_offset=%d err=%v", runtime.topic, partition, fetch.Messages[0].Offset, fetch.Messages[len(fetch.Messages)-1].Offset, processErr)
			if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryDelay(1, processErr, runtime.retryPolicy, runtime.retryRuntime)); waitErr != nil {
				return nextOffset, waitErr
			}
		}
		if err := ctx.Err(); err != nil {
			return nextOffset, err
		}

		for attempt := 1; attempt <= postEmbeddingCommitAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return nextOffset, err
			}
			commitErr := commitGate.Commit(ctx, runtime.topic, partition, targetNextOffset)
			if commitErr == nil {
				nextOffset = targetNextOffset
				KafkaConsumerSlotCommitted(PipelinePostEmbedding, runtime.slotID, time.Now().UTC())
				break
			}
			if ctx.Err() != nil {
				return nextOffset, ctx.Err()
			}
			log.Printf("[PostEmbedding] offset commit pending topic=%s partition=%d next_offset=%d attempt=%d err=%v", runtime.topic, partition, targetNextOffset, attempt, commitErr)
			if attempt < postEmbeddingCommitAttempts {
				if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryDelay(attempt, commitErr, runtime.retryPolicy, runtime.retryRuntime)); waitErr != nil {
					return nextOffset, waitErr
				}
			}
		}
		if nextOffset != targetNextOffset {
			// Keep the confirmed target pending and do not fetch later messages.
			// The same target is retried after a bounded pause while this generation is active.
			for ctx.Err() == nil && nextOffset != targetNextOffset {
				if waitErr := runtime.retryRuntime.Sleep(ctx, postEmbeddingPartitionRetryMax); waitErr != nil {
					return nextOffset, waitErr
				}
				if commitErr := commitGate.Commit(ctx, runtime.topic, partition, targetNextOffset); commitErr == nil {
					nextOffset = targetNextOffset
					KafkaConsumerSlotCommitted(PipelinePostEmbedding, runtime.slotID, time.Now().UTC())
				}
			}
			if ctx.Err() != nil {
				return nextOffset, ctx.Err()
			}
		}
		if fetch.TerminalFetchErr != nil {
			return nextOffset, fetch.TerminalFetchErr
		}
	}
	return nextOffset, ctx.Err()
}

func validatePostEmbeddingPartitionBatch(messages []kafka.Message, topic string, partition int) error {
	if len(messages) == 0 {
		return errors.New("post embedding partition batch is empty")
	}
	previous := int64(-1)
	for index, message := range messages {
		if message.Topic != topic || message.Partition != partition {
			return fmt.Errorf("post embedding batch crossed partition boundary: got %s[%d], want %s[%d]", message.Topic, message.Partition, topic, partition)
		}
		if index > 0 && message.Offset <= previous {
			return fmt.Errorf("post embedding partition offsets are not strictly increasing: previous=%d current=%d", previous, message.Offset)
		}
		previous = message.Offset
	}
	return nil
}

func callPostEmbeddingBatchProcessor(ctx context.Context, process postEmbeddingBatchProcessor, messages []kafka.Message) (err error) {
	if process == nil {
		return errors.New("post embedding partition batch processor is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("post embedding batch processor panic: %v", recovered)
		}
	}()
	return process(ctx, messages)
}

func postEmbeddingPartitionRetryDelay(attempt int, err error, policy kafkaRetryPolicy, runtime providerRetryRuntime) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if decision, providerFailure := providerFailureDecision(err); providerFailure {
		if classifyPostEmbeddingFailureScope(decision, 1) == postEmbeddingFailureScopePartition {
			delay := postEmbeddingContractPause
			if decision.RetryAfterValid && decision.RetryAfter > delay {
				return decision.RetryAfter
			}
			return delay
		}
		// The shared breaker owns the provider cooldown. This short wait avoids
		// repeating local Post reads while the circuit is open.
		return postEmbeddingPartitionRetryMin
	}
	delay := providerRetryBackoff(policy, min(attempt, 20), runtime.Jitter)
	if delay < postEmbeddingPartitionRetryMin {
		delay = postEmbeddingPartitionRetryMin
	}
	if delay > postEmbeddingPartitionRetryMax {
		delay = postEmbeddingPartitionRetryMax
	}
	return delay
}

type postEmbeddingOffsetProgress struct {
	initialNextOffset       int64
	lastConfirmedNextOffset int64
	pendingNextOffset       int64
	pending                 bool
}

type postEmbeddingOffsetCommitCoordinator struct {
	ctx            context.Context
	generation     postEmbeddingGeneration
	topic          string
	healthIdentity postEmbeddingGenerationHealthIdentity
	mu             sync.Mutex
	rpcMu          sync.Mutex
	closing        atomic.Bool
	progress       map[int]postEmbeddingOffsetProgress
}

func newPostEmbeddingOffsetCommitCoordinator(
	ctx context.Context,
	generation postEmbeddingGeneration,
	topic string,
	assignments []kafka.PartitionAssignment,
	healthIdentity postEmbeddingGenerationHealthIdentity,
) *postEmbeddingOffsetCommitCoordinator {
	progress := make(map[int]postEmbeddingOffsetProgress, len(assignments))
	for _, assignment := range assignments {
		progress[assignment.ID] = postEmbeddingOffsetProgress{
			initialNextOffset: assignment.Offset, lastConfirmedNextOffset: assignment.Offset,
		}
	}
	return &postEmbeddingOffsetCommitCoordinator{
		ctx: ctx, generation: generation, topic: topic, healthIdentity: healthIdentity, progress: progress,
	}
}

func (c *postEmbeddingOffsetCommitCoordinator) RecordPartitionBatchPending(partition int, nextOffset int64, now time.Time) bool {
	if c == nil {
		return false
	}
	return recordPostEmbeddingPartitionBatchPending(c.healthIdentity, partition, nextOffset, now)
}

func (c *postEmbeddingOffsetCommitCoordinator) Commit(ctx context.Context, topic string, partition int, nextOffset int64) error {
	if c == nil {
		return errors.New("post embedding offset commit coordinator is nil")
	}
	if ctx == nil {
		return errors.New("post embedding offset commit context is nil")
	}
	c.rpcMu.Lock()
	defer c.rpcMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.ctx != nil && c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	c.mu.Lock()
	if c.closing.Load() {
		c.mu.Unlock()
		return errPostEmbeddingCommitGateClosed
	}
	if strings.TrimSpace(topic) != c.topic {
		c.mu.Unlock()
		return fmt.Errorf("post embedding commit topic %q is not assigned topic %q", topic, c.topic)
	}
	state, ok := c.progress[partition]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("post embedding commit partition %d is not assigned to generation", partition)
	}
	if nextOffset < state.lastConfirmedNextOffset || (state.pending && nextOffset < state.pendingNextOffset) {
		c.mu.Unlock()
		return errPostEmbeddingOffsetRegression
	}
	if state.pending && nextOffset != state.pendingNextOffset {
		c.mu.Unlock()
		return errPostEmbeddingCommitTargetChanged
	}
	if !state.pending && nextOffset == state.lastConfirmedNextOffset {
		c.mu.Unlock()
		return nil
	}
	state.pendingNextOffset = nextOffset
	state.pending = true
	c.progress[partition] = state
	if c.closing.Load() || ctx.Err() != nil || (c.ctx != nil && c.ctx.Err() != nil) {
		c.mu.Unlock()
		return errPostEmbeddingCommitGateClosed
	}
	c.mu.Unlock()

	offsets := map[string]map[int]int64{c.topic: {partition: nextOffset}}
	if c.generation == nil {
		return errors.New("post embedding commit generation is nil")
	}
	if err := c.generation.CommitOffsets(offsets); err != nil {
		metrics.RecordPostEmbeddingPartitionCommitFailure(postEmbeddingCommitOutcomeUncertain(err))
		return err
	}
	c.mu.Lock()
	state = c.progress[partition]
	if nextOffset > state.lastConfirmedNextOffset {
		state.lastConfirmedNextOffset = nextOffset
	}
	state.pending = false
	state.pendingNextOffset = 0
	c.progress[partition] = state
	c.mu.Unlock()
	confirmPostEmbeddingPartitionCommit(c.healthIdentity, partition, nextOffset)
	return nil
}

func postEmbeddingCommitOutcomeUncertain(err error) bool {
	if err == nil {
		return false
	}
	var kafkaErr kafka.Error
	if errors.As(err, &kafkaErr) {
		switch kafkaErr {
		case kafka.IllegalGeneration, kafka.UnknownMemberId, kafka.RebalanceInProgress,
			kafka.GroupAuthorizationFailed, kafka.TopicAuthorizationFailed, kafka.ClusterAuthorizationFailed,
			kafka.InvalidGroupId, kafka.InvalidCommitOffsetSize, kafka.InvalidTopic, kafka.UnknownTopicOrPartition:
			return false
		default:
			return true
		}
	}
	// A transport or unclassified client error can happen after the broker has
	// accepted the request but before the response reaches this process.
	return true
}

func (c *postEmbeddingOffsetCommitCoordinator) LastConfirmedNextOffset(partition int, fallback int64) int64 {
	if c == nil {
		return fallback
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.progress[partition]
	if !ok {
		return fallback
	}
	if state.lastConfirmedNextOffset < 0 && state.initialNextOffset >= 0 {
		return state.initialNextOffset
	}
	return state.lastConfirmedNextOffset
}

func (c *postEmbeddingOffsetCommitCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closing.Store(true)
	c.mu.Unlock()
	// CommitOffsets has no context parameter. Waiting on rpcMu lets any already
	// admitted request finish under the ConsumerGroup's bounded network timeout.
	c.rpcMu.Lock()
	c.rpcMu.Unlock()
}
