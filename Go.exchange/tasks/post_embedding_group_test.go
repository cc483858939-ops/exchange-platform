package tasks

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	appmetrics "Go.exchange/metrics"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type postEmbeddingGroupTestGeneration struct {
	id          int32
	groupID     string
	memberID    string
	assignments map[string][]kafka.PartitionAssignment
	startCtx    context.Context
	startCalls  atomic.Int32
	startDone   chan struct{}
	commitMu    sync.Mutex
	commits     []map[string]map[int]int64
	commitFn    func(map[string]map[int]int64) error
}

type postEmbeddingEmbedderFunc func(context.Context, []string) (embeddings.EmbedResult, error)

func (f postEmbeddingEmbedderFunc) Embed(ctx context.Context, texts []string) (embeddings.EmbedResult, error) {
	return f(ctx, texts)
}

func newPostEmbeddingGroupTestGeneration(assignments map[string][]kafka.PartitionAssignment) *postEmbeddingGroupTestGeneration {
	return &postEmbeddingGroupTestGeneration{
		id: 1, groupID: "post-embedding-test-group", memberID: "member-test",
		assignments: assignments, startDone: make(chan struct{}),
	}
}

func (g *postEmbeddingGroupTestGeneration) ID() int32        { return g.id }
func (g *postEmbeddingGroupTestGeneration) GroupID() string  { return g.groupID }
func (g *postEmbeddingGroupTestGeneration) MemberID() string { return g.memberID }
func (g *postEmbeddingGroupTestGeneration) Assignments() map[string][]kafka.PartitionAssignment {
	return g.assignments
}
func (g *postEmbeddingGroupTestGeneration) Start(fn func(context.Context)) {
	g.startCalls.Add(1)
	go func() {
		defer close(g.startDone)
		ctx := g.startCtx
		if ctx == nil {
			ctx = context.Background()
		}
		fn(ctx)
	}()
}
func (g *postEmbeddingGroupTestGeneration) CommitOffsets(offsets map[string]map[int]int64) error {
	copyOffsets := make(map[string]map[int]int64, len(offsets))
	for topic, partitions := range offsets {
		copyOffsets[topic] = make(map[int]int64, len(partitions))
		for partition, offset := range partitions {
			copyOffsets[topic][partition] = offset
		}
	}
	g.commitMu.Lock()
	g.commits = append(g.commits, copyOffsets)
	commitFn := g.commitFn
	g.commitMu.Unlock()
	if commitFn != nil {
		return commitFn(copyOffsets)
	}
	return nil
}

type postEmbeddingGroupTestConsumerGroup struct {
	generation *postEmbeddingGroupTestGeneration
	returned   bool
	nextCalls  atomic.Int32
}

func (g *postEmbeddingGroupTestConsumerGroup) Next(ctx context.Context) (postEmbeddingGeneration, error) {
	g.nextCalls.Add(1)
	if !g.returned {
		g.returned = true
		g.generation.startCtx = ctx
		return g.generation, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.generation.startDone:
		return nil, ctx.Err()
	}
}
func (*postEmbeddingGroupTestConsumerGroup) Close() error { return nil }

func TestPostEmbeddingGenerationUsesOneSupervisorAndAllowsEmptyAssignments(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{})
	group := &postEmbeddingGroupTestConsumerGroup{generation: generation}
	runtimeConfig := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-a", batchSize: 1,
		processBatch: func(context.Context, []kafka.Message) error { return nil },
	}
	done := make(chan error, 1)
	go func() { done <- runPostEmbeddingKafkaGroup(ctx, group, runtimeConfig) }()
	deadline := time.After(2 * time.Second)
	for generation.startCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("Generation.Start was not called")
		default:
			runtime.Gosched()
		}
	}
	if generation.startCalls.Load() != 1 {
		t.Fatalf("Generation.Start calls=%d want 1", generation.startCalls.Load())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runPostEmbeddingKafkaGroup error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("empty-assignment Supervisor did not exit after cancellation")
	}
}

func TestPostEmbeddingGenerationSessionRegistersZeroPartitionMemberByClientID(t *testing.T) {
	resetPipelineStates(t)
	now := time.Now().UTC()
	registerPipelineAt(PipelinePostEmbedding, 1, now)
	KafkaConsumerSlotStarted(PipelinePostEmbedding, 0)
	identity, unregister := registerKafkaConsumerGenerationSession(
		PipelinePostEmbedding, 0, "post-embedding", "post-embedding-group", "stable-client-id", 17, nil,
	)
	if !identity.valid() {
		t.Fatal("zero-partition Generation did not receive a health identity")
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.sessionActive || !slot.sessionRecoveryEligible || slot.session.clientID != "stable-client-id" ||
		slot.session.generationID != 17 || !slot.session.generationScoped || slot.session.partitionCount != 0 {
		t.Fatalf("zero-partition Generation session was not registered as a live member: %+v", slot)
	}
	if len(slot.postEmbeddingPartitions) != 0 {
		t.Fatalf("zero-partition Generation created partition health: %+v", slot.postEmbeddingPartitions)
	}
	unregister()
	workerPipelines.RLock()
	slot = workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.sessionActive || slot.sessionRecoveryEligible {
		t.Fatalf("Generation session remained active after cancellation: %+v", slot)
	}
}

func TestPostEmbeddingProvider503KeepsGenerationAndGroupMemberActive(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	ctx, cancel := context.WithCancel(context.Background())
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 0, Offset: 0}},
	})
	group := &postEmbeddingGroupTestConsumerGroup{generation: generation}
	gate := newPostEmbeddingProviderGate(providerRetryRuntime{})
	reader := &postEmbeddingPartitionTestReader{message: kafka.Message{Topic: "post-embedding", Partition: 0, Offset: 0}}
	failureSeen := make(chan struct{})
	runtimeConfig := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-503", batchSize: 1,
		retryPolicy:  kafkaRetryPolicy{MaxAttempts: 1},
		retryRuntime: providerRetryRuntime{Sleep: waitForPostEmbeddingProviderDelay},
		providerGate: gate,
		readerFactory: func(string, int, int64, string) (postEmbeddingPartitionReader, error) {
			return reader, nil
		},
		processBatch: func(requestCtx context.Context, _ []kafka.Message) error {
			permit, err := gate.acquire(requestCtx)
			if err != nil {
				return err
			}
			decision := ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}
			gate.providerRequestFailed(permit, decision, postEmbeddingFailureScopeSharedOrUnknown, true)
			select {
			case <-failureSeen:
			default:
				close(failureSeen)
			}
			failure := &postEmbeddingProviderFailure{decision: decision, cause: errors.New("HTTP 503")}
			return retryableKafkaError(kafkaFailureCodeProviderRetryable, failure)
		},
	}
	done := make(chan error, 1)
	go func() { done <- runPostEmbeddingKafkaGroup(ctx, group, runtimeConfig) }()
	select {
	case <-failureSeen:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("partition did not observe the simulated provider 503")
	}
	deadline := time.After(2 * time.Second)
	for group.nextCalls.Load() < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("ConsumerGroup.Next was not waiting on the active generation")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-generation.startDone:
		t.Fatal("provider 503 ended Generation.Start Supervisor")
	case <-time.After(100 * time.Millisecond):
	}
	if group.nextCalls.Load() != 2 || len(generation.commits) != 0 {
		t.Fatalf("Next calls=%d commits=%v; provider pause must retain group without committing", group.nextCalls.Load(), generation.commits)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("group runner exit error=%v", err)
	}
	select {
	case <-generation.startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor did not exit after Generation cancellation")
	}
}

func TestPostEmbeddingOffsetCommitCoordinatorRetriesUncertainTargetMonotonically(t *testing.T) {
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 2, Offset: 100}},
	})
	var attempts atomic.Int32
	brokerAcceptedNextOffset := int64(100)
	generation.commitFn = func(offsets map[string]map[int]int64) error {
		if got := offsets["post-embedding"][2]; got != 103 {
			t.Errorf("CommitOffsets target=%d want 103", got)
		}
		if target := offsets["post-embedding"][2]; target < brokerAcceptedNextOffset {
			t.Errorf("broker observed offset regression: target=%d accepted=%d", target, brokerAcceptedNextOffset)
		} else {
			brokerAcceptedNextOffset = target
		}
		if attempts.Add(1) == 1 {
			return errors.New("commit timed out after broker acceptance")
		}
		return nil
	}
	coordinator := newPostEmbeddingOffsetCommitCoordinator(context.Background(), generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	if err := coordinator.Commit(context.Background(), "post-embedding", 2, 103); err == nil {
		t.Fatal("first ambiguous CommitOffsets call unexpectedly succeeded")
	}
	if got := coordinator.LastConfirmedNextOffset(2, -1); got != 100 {
		t.Fatalf("confirmed after uncertain commit=%d want initial 100", got)
	}
	if brokerAcceptedNextOffset != 103 {
		t.Fatalf("fake broker accepted offset=%d want 103 despite uncertain response", brokerAcceptedNextOffset)
	}
	if err := coordinator.Commit(context.Background(), "post-embedding", 2, 103); err != nil {
		t.Fatalf("retry same pending target: %v", err)
	}
	if got := coordinator.LastConfirmedNextOffset(2, -1); got != 103 {
		t.Fatalf("confirmed next offset=%d want 103", got)
	}
	if err := coordinator.Commit(context.Background(), "post-embedding", 2, 102); !errors.Is(err, errPostEmbeddingOffsetRegression) {
		t.Fatalf("backwards commit error=%v", err)
	}
	if len(generation.commits) != 2 || generation.commits[0]["post-embedding"][2] != 103 || generation.commits[1]["post-embedding"][2] != 103 {
		t.Fatalf("CommitOffsets calls=%v; expected the same target twice", generation.commits)
	}
	coordinator.Close()
	if err := coordinator.Commit(context.Background(), "post-embedding", 2, 104); !errors.Is(err, errPostEmbeddingCommitGateClosed) {
		t.Fatalf("commit after Close error=%v", err)
	}
}

func TestPostEmbeddingCommitOutcomeSeparatesRejectedFromUncertain(t *testing.T) {
	if postEmbeddingCommitOutcomeUncertain(nil) {
		t.Fatal("nil commit result classified as uncertain")
	}
	if postEmbeddingCommitOutcomeUncertain(fmt.Errorf("offset commit rejected: %w", kafka.IllegalGeneration)) {
		t.Fatal("known invalid generation classified as uncertain")
	}
	if !postEmbeddingCommitOutcomeUncertain(fmt.Errorf("offset commit timeout: %w", kafka.RequestTimedOut)) {
		t.Fatal("broker timeout not classified as uncertain")
	}
	if !postEmbeddingCommitOutcomeUncertain(errors.New("response lost after broker acceptance")) {
		t.Fatal("unclassified client error not conservatively classified as uncertain")
	}
}

func TestPostEmbeddingOffsetCommitGateClosesAroundInFlightRPC(t *testing.T) {
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 0, Offset: 1}},
	})
	entered, release := make(chan struct{}), make(chan struct{})
	generation.commitFn = func(map[string]map[int]int64) error {
		close(entered)
		<-release
		return nil
	}
	generationCtx, cancelGeneration := context.WithCancel(context.Background())
	coordinator := newPostEmbeddingOffsetCommitCoordinator(generationCtx, generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	firstDone := make(chan error, 1)
	go func() { firstDone <- coordinator.Commit(context.Background(), "post-embedding", 0, 2) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("CommitOffsets did not start")
	}
	cancelGeneration()
	closeDone := make(chan struct{})
	go func() { coordinator.Close(); close(closeDone) }()
	deadline := time.After(2 * time.Second)
	for !coordinator.closing.Load() {
		select {
		case <-deadline:
			t.Fatal("Commit Gate did not enter Closing")
		default:
			runtime.Gosched()
		}
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- coordinator.Commit(context.Background(), "post-embedding", 0, 3) }()
	select {
	case err := <-secondDone:
		t.Fatalf("new commit returned before admitted RPC ended: %v", err)
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("in-flight admitted commit: %v", err)
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Commit Gate Close did not wait for and finish the in-flight RPC")
	}
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("commit after generation cancellation error=%v", err)
	}
	if len(generation.commits) != 1 {
		t.Fatalf("CommitOffsets calls=%d want 1", len(generation.commits))
	}
}

type postEmbeddingPartitionTestReader struct {
	mu         sync.Mutex
	message    kafka.Message
	fetchCalls int
	closed     atomic.Bool
}

func (r *postEmbeddingPartitionTestReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	r.fetchCalls++
	call := r.fetchCalls
	r.mu.Unlock()
	if call == 1 {
		return r.message, nil
	}
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}
func (r *postEmbeddingPartitionTestReader) Close() error { r.closed.Store(true); return nil }

func TestPostEmbeddingPartitionRetainsBatchUntilSuccessAndCommitsNextOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 1, Offset: 10}},
	})
	coordinator := newPostEmbeddingOffsetCommitCoordinator(ctx, generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	reader := &postEmbeddingPartitionTestReader{message: kafka.Message{Topic: "post-embedding", Partition: 1, Offset: 11, Value: []byte("pending")}}
	firstFailure, allowRetry, processed, committed := make(chan struct{}), make(chan struct{}), make(chan kafka.Message, 2), make(chan int64, 1)
	generation.commitFn = func(offsets map[string]map[int]int64) error {
		committed <- offsets["post-embedding"][1]
		return nil
	}
	var processCalls atomic.Int32
	retryRuntime := providerRetryRuntime{
		Now:    time.Now,
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Sleep: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-allowRetry:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	runtimeConfig := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-a", batchSize: 1,
		retryPolicy: kafkaRetryPolicy{MaxAttempts: 1}, retryRuntime: retryRuntime,
		readerFactory: func(string, int, int64, string) (postEmbeddingPartitionReader, error) {
			return reader, nil
		},
		processBatch: func(_ context.Context, messages []kafka.Message) error {
			processed <- messages[0]
			if processCalls.Add(1) == 1 {
				close(firstFailure)
				return retryableKafkaError(kafkaFailureCodeProviderRetryable, &postEmbeddingProviderFailure{
					decision: ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer},
				})
			}
			return nil
		},
	}
	type partitionResult struct {
		nextOffset int64
		err        error
	}
	done := make(chan partitionResult, 1)
	go func() {
		nextOffset, err := runPostEmbeddingPartitionReaderAttempt(
			ctx,
			kafka.PartitionAssignment{ID: 1, Offset: 10},
			10,
			runtimeConfig,
			"worker-a-p1",
			coordinator,
			&postEmbeddingPartitionHold{},
		)
		done <- partitionResult{nextOffset: nextOffset, err: err}
	}()
	select {
	case <-firstFailure:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("partition did not attempt its original message")
	}
	select {
	case offset := <-committed:
		t.Fatalf("uncompleted batch committed offset %d", offset)
	default:
	}
	close(allowRetry)
	first := <-processed
	second := <-processed
	if first.Offset != 11 || second.Offset != 11 || processCalls.Load() != 2 {
		t.Fatalf("processed offsets=%d,%d calls=%d; wanted the same held offset twice", first.Offset, second.Offset, processCalls.Load())
	}
	select {
	case target := <-committed:
		if target != 12 {
			t.Fatalf("committed next offset=%d want 12", target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("successful held batch was not committed")
	}
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("partition reader exit error=%v", result.err)
	}
	if result.nextOffset != 12 {
		t.Fatalf("partition next offset=%d want 12", result.nextOffset)
	}
	if !reader.closed.Load() {
		t.Fatal("partition reader was not closed")
	}
}

func TestPostEmbeddingPartitionWorkerRestartsAfterUnexpectedPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 3, Offset: 7}},
	})
	coordinator := newPostEmbeddingOffsetCommitCoordinator(ctx, generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	readerOpened := make(chan struct{})
	var factoryCalls atomic.Int32
	var sleepCalls atomic.Int32
	runtimeConfig := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-restart", batchSize: 1,
		retryPolicy: kafkaRetryPolicy{MaxAttempts: 1},
		retryRuntime: providerRetryRuntime{
			Now:    time.Now,
			Jitter: func(delay time.Duration) time.Duration { return delay },
			Sleep: func(context.Context, time.Duration) error {
				if sleepCalls.Add(1) == 1 {
					panic("injected unexpected retry panic")
				}
				return nil
			},
		},
		readerFactory: func(string, int, int64, string) (postEmbeddingPartitionReader, error) {
			if factoryCalls.Add(1) == 1 {
				return nil, errors.New("injected reader construction failure")
			}
			reader := &postEmbeddingPartitionTestReader{message: kafka.Message{Topic: "post-embedding", Partition: 3, Offset: 7}}
			select {
			case <-readerOpened:
			default:
				close(readerOpened)
			}
			return reader, nil
		},
		processBatch: func(context.Context, []kafka.Message) error { return nil },
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPostEmbeddingPartitionWorker(ctx, kafka.PartitionAssignment{ID: 3, Offset: 7}, runtimeConfig, make(chan struct{}, 1), coordinator, &atomic.Int32{})
	}()
	select {
	case <-readerOpened:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("partition worker did not restart after its unexpected panic")
	}
	if factoryCalls.Load() < 2 || sleepCalls.Load() < 2 {
		cancel()
		t.Fatalf("reader factory calls=%d retry sleeps=%d; worker was not retried", factoryCalls.Load(), sleepCalls.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("restarted partition worker did not exit after cancellation")
	}
	coordinator.Close()
}

type postEmbeddingRestartTestReader struct {
	messages   []kafka.Message
	index      int
	panicAtEnd bool
}

func (r *postEmbeddingRestartTestReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.index < len(r.messages) {
		message := r.messages[r.index]
		r.index++
		return message, nil
	}
	if r.panicAtEnd {
		panic("injected panic after confirmed commit")
	}
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

func (*postEmbeddingRestartTestReader) Close() error { return nil }

func TestPostEmbeddingPartitionWorkerRestartUsesConfirmedOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 0, Offset: 100}},
	})
	coordinator := newPostEmbeddingOffsetCommitCoordinator(ctx, generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	var (
		factoryMu    sync.Mutex
		factoryCalls int
		readerStarts []int64
		sleepCalls   atomic.Int32
	)
	secondReaderStarted := make(chan struct{})
	runtimeConfig := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-offset-restart", batchSize: 3,
		batchMaxWait: time.Millisecond, retryPolicy: kafkaRetryPolicy{MaxAttempts: 1},
		retryRuntime: providerRetryRuntime{
			Now: time.Now, Jitter: func(delay time.Duration) time.Duration { return delay },
			Sleep: func(context.Context, time.Duration) error {
				if sleepCalls.Add(1) == 1 {
					panic("injected Worker Pass panic after commit")
				}
				return nil
			},
		},
		readerFactory: func(_ string, _ int, offset int64, _ string) (postEmbeddingPartitionReader, error) {
			factoryMu.Lock()
			factoryCalls++
			call := factoryCalls
			readerStarts = append(readerStarts, offset)
			factoryMu.Unlock()
			if call == 1 {
				return &postEmbeddingRestartTestReader{
					panicAtEnd: true,
					messages: []kafka.Message{
						{Topic: "post-embedding", Partition: 0, Offset: 100},
						{Topic: "post-embedding", Partition: 0, Offset: 101},
						{Topic: "post-embedding", Partition: 0, Offset: 102},
					},
				}, nil
			}
			if call == 2 {
				close(secondReaderStarted)
			}
			return &postEmbeddingRestartTestReader{}, nil
		},
		processBatch: func(context.Context, []kafka.Message) error { return nil },
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPostEmbeddingPartitionWorker(ctx, kafka.PartitionAssignment{ID: 0, Offset: 100}, runtimeConfig, make(chan struct{}, 1), coordinator, &atomic.Int32{})
	}()
	select {
	case <-secondReaderStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("partition worker did not restart after the controlled panic")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("restarted partition worker did not stop after cancellation")
	}
	factoryMu.Lock()
	starts := append([]int64(nil), readerStarts...)
	factoryMu.Unlock()
	if len(starts) != 2 || starts[0] != 100 || starts[1] != 103 {
		t.Fatalf("reader restart offsets=%v, want [100 103]", starts)
	}
	if got := coordinator.LastConfirmedNextOffset(0, -1); got != 103 {
		t.Fatalf("confirmed restart offset=%d, want 103", got)
	}
	if len(generation.commits) != 1 || generation.commits[0]["post-embedding"][0] != 103 {
		t.Fatalf("commits=%v, want one commit to 103", generation.commits)
	}
	coordinator.Close()
}

func TestPostEmbeddingFailureScopeKeepsAuthUnknownAnd413PartitionLocal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422, 429, 503} {
		decision := classifyPostEmbeddingProviderError(&embeddings.ProviderHTTPError{StatusCode: status}, 1)
		if scope := classifyPostEmbeddingFailureScope(decision, 1); scope != postEmbeddingFailureScopeSharedOrUnknown {
			t.Errorf("status %d failure scope=%v want shared/unknown", status, scope)
		}
		if shouldQuarantinePostEmbeddingFailure(classifyPostEmbeddingFailureScope(decision, 1), false) {
			t.Errorf("status %d without local evidence requested quarantine", status)
		}
	}
	decision := classifyPostEmbeddingProviderError(&embeddings.ProviderHTTPError{StatusCode: 413}, 1)
	if scope := classifyPostEmbeddingFailureScope(decision, 1); scope != postEmbeddingFailureScopePartition {
		t.Fatalf("single-message 413 scope=%v want partition", scope)
	}
	decision = ProviderDecision{Action: ProviderActionSplitBatch, Reason: providerReasonContract}
	if scope := classifyPostEmbeddingFailureScope(decision, 4); scope != postEmbeddingFailureScopePartition {
		t.Fatalf("contract split scope=%v want partition", scope)
	}
	if !shouldQuarantinePostEmbeddingFailure(postEmbeddingFailureScopeInstance, true) ||
		shouldQuarantinePostEmbeddingFailure(postEmbeddingFailureScopeInstance, false) ||
		shouldQuarantinePostEmbeddingFailure(postEmbeddingFailureScopeSharedOrUnknown, true) {
		t.Fatal("quarantine decision did not require instance scope and local evidence")
	}
}

func TestPostEmbeddingProviderGateLimitsConcurrencyAndAllowsOneHalfOpenProbe(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	runtimeConfig := providerRetryRuntime{Now: time.Now, Jitter: func(delay time.Duration) time.Duration { return delay }}
	gate := newPostEmbeddingProviderGate(runtimeConfig)
	first, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	thirdCtx, cancelThird := context.WithCancel(context.Background())
	thirdResult := make(chan *postEmbeddingProviderPermit, 1)
	thirdErr := make(chan error, 1)
	go func() {
		permit, err := gate.acquire(thirdCtx)
		thirdResult <- permit
		thirdErr <- err
	}()
	select {
	case <-thirdResult:
		t.Fatal("third provider request passed the two-request limit")
	default:
	}
	gate.providerRequestSucceeded(first)
	var third *postEmbeddingProviderPermit
	select {
	case third = <-thirdResult:
		if err := <-thirdErr; err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider request did not enter after a permit was released")
	}
	cancelThird()
	gate.providerRequestSucceeded(second)
	gate.providerRequestSucceeded(third)

	permit, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.providerRequestFailed(permit, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
	gate.mu.Lock()
	gate.openUntil = gate.runtime.Now().Add(-time.Millisecond)
	gate.mu.Unlock()
	probe, err := gate.acquire(context.Background())
	if err != nil || !probe.halfOpen {
		t.Fatalf("probe=%+v err=%v", probe, err)
	}
	blockedCtx, cancelBlocked := context.WithCancel(context.Background())
	blocked := make(chan error, 1)
	go func() {
		_, err := gate.acquire(blockedCtx)
		blocked <- err
	}()
	select {
	case err := <-blocked:
		t.Fatalf("second HalfOpen request was not serialized: %v", err)
	default:
	}
	cancelBlocked()
	if err := <-blocked; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked request cancellation=%v", err)
	}
	gate.providerRequestSucceeded(probe)
	gate.mu.Lock()
	state := gate.state
	gate.mu.Unlock()
	if state != postEmbeddingCircuitClosed {
		t.Fatalf("breaker state=%v want closed after validated probe", state)
	}
}

func TestPostEmbeddingHalfOpenPartitionFailureDoesNotReopenGlobalCircuit(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	now := time.Unix(300, 0)
	held := make(chan struct{})
	var heldOnce sync.Once
	runtimeConfig := providerRetryRuntime{
		Now:    func() time.Time { return now },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Sleep: func(ctx context.Context, delay time.Duration) error {
			if delay >= postEmbeddingContractPause {
				heldOnce.Do(func() { close(held) })
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
	}
	gate := newPostEmbeddingProviderGate(runtimeConfig)
	initial, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.providerRequestFailed(initial, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
	gate.mu.Lock()
	gate.openUntil = now.Add(-time.Millisecond)
	gate.mu.Unlock()

	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 1, Offset: 100}},
	})
	coordinator := newPostEmbeddingOffsetCommitCoordinator(context.Background(), generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &postEmbeddingPartitionTestReader{message: kafka.Message{Topic: "post-embedding", Partition: 1, Offset: 100}}
	partitionEmbedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: 413}}
	runtimeConfigForPartition := postEmbeddingGroupRuntime{
		topic: "post-embedding", groupID: generation.groupID, clientID: "worker-half-open", batchSize: 1,
		retryPolicy: kafkaRetryPolicy{MaxAttempts: 1}, retryRuntime: runtimeConfig, providerGate: gate,
		readerFactory: func(string, int, int64, string) (postEmbeddingPartitionReader, error) { return reader, nil },
		processBatch: func(ctx context.Context, _ []kafka.Message) error {
			_, err := generatePostEmbeddingTextsWithRuntimeAndGate(
				ctx, partitionEmbedder, []string{"partition one"}, kafkaRetryPolicy{MaxAttempts: 3}, runtimeConfig, nil, gate,
			)
			return err
		},
	}
	var heldCount atomic.Int32
	hold := &postEmbeddingPartitionHold{count: &heldCount}
	partitionDone := make(chan error, 1)
	go func() {
		_, err := runPostEmbeddingPartitionReaderAttempt(ctx, kafka.PartitionAssignment{ID: 1, Offset: 100}, 100,
			runtimeConfigForPartition, "worker-half-open-p1", coordinator, hold)
		partitionDone <- err
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("partition 1 did not enter its bounded Hold")
	}
	if !hold.active.Load() || partitionEmbedder.calls != 1 {
		t.Fatalf("partition Hold=%t Provider calls=%d, want held and one single-item 413 request", hold.active.Load(), partitionEmbedder.calls)
	}
	gate.mu.Lock()
	state, inFlight := gate.state, gate.inFlight
	gate.mu.Unlock()
	if state != postEmbeddingCircuitHalfOpen || inFlight != 0 {
		t.Fatalf("breaker after partition-scoped probe failure state=%v inFlight=%d, want HalfOpen/0", state, inFlight)
	}
	if len(generation.commits) != 0 || coordinator.LastConfirmedNextOffset(1, 100) != 100 {
		t.Fatalf("partition 1 advanced while held: commits=%v confirmed=%d", generation.commits, coordinator.LastConfirmedNextOffset(1, 100))
	}

	recoveryBefore := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total")
	if _, err := generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), &postEmbeddingTestEmbedder{}, []string{"partition two"},
		kafkaRetryPolicy{MaxAttempts: 3}, runtimeConfig, nil, gate,
	); err != nil {
		t.Fatalf("partition 2 HalfOpen probe: %v", err)
	}
	if _, err := generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), &postEmbeddingTestEmbedder{}, []string{"partition zero"},
		kafkaRetryPolicy{MaxAttempts: 3}, runtimeConfig, nil, gate,
	); err != nil {
		t.Fatalf("partition 0 processing after breaker recovery: %v", err)
	}
	gate.mu.Lock()
	state = gate.state
	gate.mu.Unlock()
	if state != postEmbeddingCircuitClosed {
		t.Fatalf("breaker after partition 2 validated response state=%v, want Closed", state)
	}
	if got := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total"); got != recoveryBefore+1 {
		t.Fatalf("Provider recovery metric=%v want %v", got, recoveryBefore+1)
	}
	cancel()
	if err := <-partitionDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("held partition exit error=%v, want context cancellation", err)
	}
	if coordinator.LastConfirmedNextOffset(1, 100) != 100 || len(generation.commits) != 0 {
		t.Fatalf("partition 1 lost its uncommitted offset: confirmed=%d commits=%v", coordinator.LastConfirmedNextOffset(1, 100), generation.commits)
	}
	coordinator.Close()
}

func TestPostEmbeddingHalfOpenContractFailureStaysAvailableAndSharedFailureReopens(t *testing.T) {
	tests := []struct {
		name      string
		embedder  *postEmbeddingTestEmbedder
		wantState postEmbeddingCircuitState
		wantCalls int
	}{
		{
			name:      "contract failure remains partition local",
			embedder:  &postEmbeddingTestEmbedder{results: []embeddings.EmbedResult{{Model: "test-model"}}},
			wantState: postEmbeddingCircuitHalfOpen, wantCalls: 1,
		},
		{
			name:      "503 reopens the shared circuit",
			embedder:  &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: 503}},
			wantState: postEmbeddingCircuitOpen, wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleanupPostEmbeddingGateMetrics(t)
			now := time.Unix(400, 0)
			runtimeConfig := providerRetryRuntime{
				Sleep:  func(context.Context, time.Duration) error { return nil },
				Jitter: func(delay time.Duration) time.Duration { return delay },
				Now:    func() time.Time { return now },
			}
			gate := newPostEmbeddingProviderGate(runtimeConfig)
			initial, err := gate.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			gate.providerRequestFailed(initial, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
			gate.mu.Lock()
			gate.openUntil = now.Add(-time.Millisecond)
			gate.mu.Unlock()
			_, err = generatePostEmbeddingTextsWithRuntimeAndGate(
				context.Background(), test.embedder, []string{"half-open probe"},
				kafkaRetryPolicy{MaxAttempts: 4}, runtimeConfig, nil, gate,
			)
			if err == nil || test.embedder.calls != test.wantCalls {
				t.Fatalf("probe err=%v calls=%d, want failure and %d call(s)", err, test.embedder.calls, test.wantCalls)
			}
			gate.mu.Lock()
			state, inFlight := gate.state, gate.inFlight
			gate.mu.Unlock()
			if state != test.wantState || inFlight != 0 {
				t.Fatalf("breaker after probe failure state=%v inFlight=%d, want %v/0", state, inFlight, test.wantState)
			}
			if state == postEmbeddingCircuitHalfOpen {
				nextProbe, err := gate.acquire(context.Background())
				if err != nil || !nextProbe.halfOpen {
					t.Fatalf("next partition could not probe after scoped failure: permit=%+v err=%v", nextProbe, err)
				}
				gate.providerRequestSucceeded(nextProbe)
			}
		})
	}
}

func TestPostEmbeddingProviderPermitReleasedAfterPanic(t *testing.T) {
	for _, halfOpen := range []bool{false, true} {
		name := "closed"
		if halfOpen {
			name = "half-open"
		}
		t.Run(name, func(t *testing.T) {
			cleanupPostEmbeddingGateMetrics(t)
			now := time.Unix(500, 0)
			runtimeConfig := providerRetryRuntime{
				Sleep:  func(context.Context, time.Duration) error { return nil },
				Jitter: func(delay time.Duration) time.Duration { return delay },
				Now:    func() time.Time { return now },
			}
			gate := newPostEmbeddingProviderGate(runtimeConfig)
			if halfOpen {
				initial, err := gate.acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				gate.providerRequestFailed(initial, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
				gate.mu.Lock()
				gate.openUntil = now.Add(-time.Millisecond)
				gate.mu.Unlock()
			}
			panicEmbedder := postEmbeddingEmbedderFunc(func(context.Context, []string) (embeddings.EmbedResult, error) {
				panic("injected Provider panic")
			})
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				_, _ = generatePostEmbeddingTextsWithRuntimeAndGate(
					context.Background(), panicEmbedder, []string{"panic"},
					kafkaRetryPolicy{MaxAttempts: 1}, runtimeConfig, nil, gate,
				)
			}()
			if !panicked {
				t.Fatal("Provider panic was swallowed")
			}
			gate.mu.Lock()
			state, inFlight, openUntil := gate.state, gate.inFlight, gate.openUntil
			gate.mu.Unlock()
			if inFlight != 0 {
				t.Fatalf("Permit leaked after panic: inFlight=%d", inFlight)
			}
			if halfOpen {
				if state != postEmbeddingCircuitOpen || !openUntil.After(now) {
					t.Fatalf("HalfOpen panic state=%v cooldown=%s, want safely Open", state, openUntil.Sub(now))
				}
				gate.mu.Lock()
				gate.openUntil = now.Add(-time.Millisecond)
				gate.mu.Unlock()
			} else if state != postEmbeddingCircuitClosed {
				t.Fatalf("Closed-state panic changed breaker to %v", state)
			}
			if _, err := generatePostEmbeddingTextsWithRuntimeAndGate(
				context.Background(), &postEmbeddingTestEmbedder{}, []string{"next request"},
				kafkaRetryPolicy{MaxAttempts: 1}, runtimeConfig, nil, gate,
			); err != nil {
				t.Fatalf("Provider request after panic did not get a Permit: %v", err)
			}
			first, err := gate.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			second, err := gate.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			gate.providerRequestSucceeded(first)
			gate.providerRequestSucceeded(first)
			gate.mu.Lock()
			inFlight = gate.inFlight
			gate.mu.Unlock()
			if inFlight != 1 {
				t.Fatalf("duplicate Permit release changed inFlight=%d, want 1", inFlight)
			}
			gate.providerRequestSucceeded(second)
			gate.mu.Lock()
			state, inFlight = gate.state, gate.inFlight
			gate.mu.Unlock()
			if inFlight != 0 || state != postEmbeddingCircuitClosed {
				t.Fatalf("Permit cleanup state=%v inFlight=%d, want Closed/0", state, inFlight)
			}
		})
	}
}

func TestPostEmbeddingProviderPermitReleasedOnCancellation(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	gate := newPostEmbeddingProviderGate(providerRetryRuntime{})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	embedder := postEmbeddingEmbedderFunc(func(ctx context.Context, _ []string) (embeddings.EmbedResult, error) {
		close(started)
		<-ctx.Done()
		return embeddings.EmbedResult{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := generatePostEmbeddingTextsWithRuntimeAndGate(
			ctx, embedder, []string{"cancel"}, kafkaRetryPolicy{MaxAttempts: 3}, providerRetryRuntime{}, nil, gate,
		)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Provider request did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Provider request error=%v", err)
	}
	gate.mu.Lock()
	state, inFlight := gate.state, gate.inFlight
	gate.mu.Unlock()
	if state != postEmbeddingCircuitClosed || inFlight != 0 {
		t.Fatalf("cancellation Permit cleanup state=%v inFlight=%d, want Closed/0", state, inFlight)
	}
	if _, err := generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), &postEmbeddingTestEmbedder{}, []string{"after cancellation"},
		kafkaRetryPolicy{MaxAttempts: 1}, providerRetryRuntime{}, nil, gate,
	); err != nil {
		t.Fatalf("Provider request after cancellation could not acquire a Permit: %v", err)
	}
}

func TestPostEmbeddingNoopAndDLQSuccessDoNotCloseOpenBreaker(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	gate := newPostEmbeddingProviderGate(providerRetryRuntime{})
	permit, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.providerRequestFailed(permit, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
	store := &postEmbeddingTestStore{postErr: gorm.ErrRecordNotFound, embeddingErr: gorm.ErrRecordNotFound}
	embedder := &postEmbeddingTestEmbedder{}
	publisher := &fakeRawKafkaMessagePublisher{}
	hooks := postEmbeddingConsumerHooks{providerGate: gate}
	if err := processPostEmbeddingBatchWithHooks(
		context.Background(), []kafka.Message{postEmbeddingTestMessage(t, 52)}, publisher, embedder,
		store, "v1", postEmbeddingRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, hooks,
	); err != nil {
		t.Fatalf("missing Post no-op: %v", err)
	}
	if err := processPostEmbeddingBatchWithHooks(
		context.Background(), []kafka.Message{{Topic: "post-embedding", Partition: 0, Offset: 1, Value: []byte("invalid")}},
		publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, hooks,
	); err != nil {
		t.Fatalf("permanent invalid message DLQ: %v", err)
	}
	gate.mu.Lock()
	state := gate.state
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen {
		t.Fatalf("No-op/DLQ closed Provider breaker: state=%v", state)
	}
	if embedder.calls != 0 || len(publisher.messages) != 1 {
		t.Fatalf("embedder calls=%d DLQ messages=%d, want 0 and 1", embedder.calls, len(publisher.messages))
	}
	generation := newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": {{ID: 0, Offset: 1}},
	})
	coordinator := newPostEmbeddingOffsetCommitCoordinator(context.Background(), generation, "post-embedding", generation.Assignments()["post-embedding"], postEmbeddingGenerationHealthIdentity{})
	if err := coordinator.Commit(context.Background(), "post-embedding", 0, 2); err != nil {
		t.Fatalf("Kafka commit while Provider circuit is open: %v", err)
	}
	coordinator.Close()
	gate.mu.Lock()
	state = gate.state
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen {
		t.Fatalf("Kafka offset commit closed Provider breaker: state=%v", state)
	}
}

func TestPostEmbeddingOpenBreakerDoesNotIssueProviderRequests(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	gate := newPostEmbeddingProviderGate(providerRetryRuntime{})
	permit, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.providerRequestFailed(permit, ProviderDecision{
		Action: ProviderActionRetry, Reason: providerReasonServer,
		RetryAfter: time.Hour, RetryAfterValid: true,
	}, postEmbeddingFailureScopeSharedOrUnknown, true)
	embedder := &postEmbeddingTestEmbedder{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = generatePostEmbeddingTextsWithRuntimeAndGate(
		ctx, embedder, []string{"must wait"}, kafkaRetryPolicy{MaxAttempts: 1}, providerRetryRuntime{}, nil, gate,
	)
	if !errors.Is(err, context.DeadlineExceeded) || embedder.calls != 0 {
		t.Fatalf("open circuit returned err=%v provider calls=%d, want canceled wait and zero calls", err, embedder.calls)
	}
}

func cleanupPostEmbeddingGateMetrics(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		appmetrics.SetPostEmbeddingCircuitBreakerState("closed")
		appmetrics.SetEmbeddingProviderPause(false, "", 0)
	})
}

func TestPostEmbeddingProviderGatePreservesCooldownBackoffAndRetryAfter(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	now := time.Unix(100, 0)
	runtimeConfig := providerRetryRuntime{
		Now:    func() time.Time { return now },
		Jitter: func(delay time.Duration) time.Duration { return delay },
	}
	gate := newPostEmbeddingProviderGate(runtimeConfig)
	decision := ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}
	for want := 5 * time.Second; want <= 10*time.Second; want += 5 * time.Second {
		permit, err := gate.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		gate.providerRequestFailed(permit, decision, postEmbeddingFailureScopeSharedOrUnknown, true)
		gate.mu.Lock()
		got := gate.openUntil.Sub(now)
		gate.openUntil = now.Add(-time.Millisecond)
		gate.mu.Unlock()
		if got != want {
			t.Fatalf("provider cooldown=%s want %s", got, want)
		}
	}

	permit, err := gate.acquire(context.Background())
	if err != nil || !permit.halfOpen {
		t.Fatalf("half-open permit=%+v err=%v", permit, err)
	}
	decision = ProviderDecision{
		Action: ProviderActionRetry, Reason: providerReasonRateLimit,
		RetryAfter: 7 * time.Minute, RetryAfterValid: true,
	}
	gate.providerRequestFailed(permit, decision, postEmbeddingFailureScopeSharedOrUnknown, true)
	gate.mu.Lock()
	got := gate.openUntil.Sub(now)
	gate.mu.Unlock()
	if got != decision.RetryAfter {
		t.Fatalf("cooldown=%s want Retry-After %s", got, decision.RetryAfter)
	}

	authGate := newPostEmbeddingProviderGate(runtimeConfig)
	permit, err = authGate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decision = ProviderDecision{Action: ProviderActionPause, Reason: providerReasonAuth}
	authGate.providerRequestFailed(permit, decision, postEmbeddingFailureScopeSharedOrUnknown, true)
	authGate.mu.Lock()
	got = authGate.openUntil.Sub(now)
	authGate.mu.Unlock()
	if got != postEmbeddingServicePause {
		t.Fatalf("auth pause=%s want %s", got, postEmbeddingServicePause)
	}
}

func TestPostEmbeddingPartitionBatchRequiresOneTopicPartitionAndOrderedOffsets(t *testing.T) {
	valid := []kafka.Message{
		{Topic: "posts", Partition: 2, Offset: 10},
		{Topic: "posts", Partition: 2, Offset: 12},
	}
	if err := validatePostEmbeddingPartitionBatch(valid, "posts", 2); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		messages []kafka.Message
	}{
		{name: "cross partition", messages: []kafka.Message{{Topic: "posts", Partition: 2, Offset: 10}, {Topic: "posts", Partition: 3, Offset: 11}}},
		{name: "out of order", messages: []kafka.Message{{Topic: "posts", Partition: 2, Offset: 11}, {Topic: "posts", Partition: 2, Offset: 10}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePostEmbeddingPartitionBatch(test.messages, "posts", 2); err == nil {
				t.Fatal("invalid partition batch was accepted")
			}
		})
	}
}

func TestPostEmbeddingPartitionReaderUsesBoundedManualAssignment(t *testing.T) {
	reader, err := newPostEmbeddingPartitionReaderWithBrokers(
		[]string{"127.0.0.1:9092"}, "post-embedding", 4, 99, "post-embedding-test-p4",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	kafkaReader, ok := reader.(*kafka.Reader)
	if !ok {
		t.Fatalf("reader type=%T want *kafka.Reader", reader)
	}
	readerConfig := kafkaReader.Config()
	if readerConfig.GroupID != "" || readerConfig.Topic != "post-embedding" || readerConfig.Partition != 4 ||
		readerConfig.QueueCapacity != postEmbeddingReaderQueueCapacity || readerConfig.MaxBytes != 10e6 {
		t.Fatalf("partition reader config=%+v", readerConfig)
	}
	if postEmbeddingReaderQueueCapacity != 4 || postEmbeddingMaxPartitionReaders != 6 {
		t.Fatalf("reader bounds queue=%d max_readers=%d", postEmbeddingReaderQueueCapacity, postEmbeddingMaxPartitionReaders)
	}
}

const postEmbeddingHealthTestClientID = "post-embedding-health-client"

func setupPostEmbeddingGenerationHealth(
	t *testing.T,
	generationID int32,
	assignments []kafka.PartitionAssignment,
	now time.Time,
) (postEmbeddingGenerationHealthIdentity, func()) {
	t.Helper()
	resetPipelineStates(t)
	registerPipelineAt(PipelinePostEmbedding, 1, now.Add(-workerActiveGrace-time.Second))
	KafkaConsumerSlotStarted(PipelinePostEmbedding, 0)
	identity, unregister := registerKafkaConsumerGenerationSession(
		PipelinePostEmbedding, 0, "post-embedding", "post-embedding-test-group",
		postEmbeddingHealthTestClientID, generationID, assignments,
	)
	if !identity.valid() {
		t.Fatal("Post Embedding Generation health identity was not registered")
	}
	t.Cleanup(unregister)
	recordPostEmbeddingReadySample(t, now)
	return identity, unregister
}

func recordPostEmbeddingReadySample(t *testing.T, now time.Time) {
	t.Helper()
	sampleVersion, candidate := beginKafkaBacklogSample(PipelinePostEmbedding)
	if !recordKafkaBacklogSample(PipelinePostEmbedding, sampleVersion, 0, now, true) {
		t.Fatal("could not record fresh zero Lag sample")
	}
	description := eventing.KafkaGroupDescription{
		GroupID: "post-embedding-test-group", State: "Stable",
		Members: []eventing.KafkaGroupMember{{ClientID: postEmbeddingHealthTestClientID, Topics: []string{"post-embedding"}}},
	}
	if !recordKafkaMembershipSample(context.Background(), PipelinePostEmbedding, candidate, description.GroupID, description, now, true) {
		t.Fatal("could not record Stable Post Embedding membership")
	}
}

func postEmbeddingHealthTestGeneration(assignments []kafka.PartitionAssignment) *postEmbeddingGroupTestGeneration {
	return newPostEmbeddingGroupTestGeneration(map[string][]kafka.PartitionAssignment{
		"post-embedding": assignments,
	})
}

func postEmbeddingPartitionState(t *testing.T, partition int) postEmbeddingPartitionHealth {
	t.Helper()
	workerPipelines.RLock()
	pipelineState, found := workerPipelines.states[PipelinePostEmbedding]
	if !found {
		workerPipelines.RUnlock()
		t.Fatal("Post Embedding pipeline state is missing")
	}
	partitionState, found := pipelineState.kafkaSlots[0].postEmbeddingPartitions[partition]
	workerPipelines.RUnlock()
	if !found {
		t.Fatalf("partition %d health state is missing", partition)
	}
	return partitionState
}

func TestPostEmbeddingOneStalledPartitionCannotBeMaskedByOtherCommits(t *testing.T) {
	now := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 0, Offset: 10}, {ID: 1, Offset: 20}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 31, assignments, now)
	defer unregister()
	pendingSince := now.Add(-postEmbeddingPartitionStallGrace - time.Second)
	if !recordPostEmbeddingPartitionBatchPending(identity, 0, 12, pendingSince) {
		t.Fatal("partition A pending work was not recorded")
	}
	generation := postEmbeddingHealthTestGeneration(assignments)
	coordinator := newPostEmbeddingOffsetCommitCoordinator(
		context.Background(), generation, "post-embedding", assignments, identity,
	)
	if err := coordinator.Commit(context.Background(), "post-embedding", 1, 21); err != nil {
		t.Fatalf("partition B commit failed: %v", err)
	}
	KafkaConsumerSlotCommitted(PipelinePostEmbedding, 0, time.Now().UTC())
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_post_embedding_partition_stalled") {
		t.Fatalf("partition B progress masked partition A stall: %v", reasons)
	}
	state := pipelineSnapshots()[PipelinePostEmbedding]
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("readiness evaluation accumulated consumer failures: %+v", state)
	}
	coordinator.Close()
}

func TestPostEmbeddingPartitionCommitClearsStall(t *testing.T) {
	now := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 0, Offset: 10}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 32, assignments, now)
	defer unregister()
	if !recordPostEmbeddingPartitionBatchPending(identity, 0, 12, now.Add(-postEmbeddingPartitionStallGrace-time.Second)) {
		t.Fatal("partition pending work was not recorded")
	}
	if reasons := evaluatePipelineHealth(now); !containsWorkerReason(reasons, "worker_post_embedding_partition_stalled") {
		t.Fatalf("aged partition pending state did not stall readiness: %v", reasons)
	}
	generation := postEmbeddingHealthTestGeneration(assignments)
	coordinator := newPostEmbeddingOffsetCommitCoordinator(
		context.Background(), generation, "post-embedding", assignments, identity,
	)
	if err := coordinator.Commit(context.Background(), "post-embedding", 0, 12); err != nil {
		t.Fatalf("confirmed partition commit failed: %v", err)
	}
	KafkaConsumerSlotCommitted(PipelinePostEmbedding, 0, time.Now().UTC())
	if state := postEmbeddingPartitionState(t, 0); state.Pending || state.LastConfirmedOffset != 12 {
		t.Fatalf("confirmed offset did not clear the matching Pending state: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); containsWorkerReason(reasons, "worker_post_embedding_partition_stalled") {
		t.Fatalf("confirmed partition commit did not restore readiness: %v", reasons)
	}
	coordinator.Close()
}

func TestPostEmbeddingUncertainCommitKeepsPending(t *testing.T) {
	now := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 0, Offset: 4}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 33, assignments, now)
	defer unregister()
	if !recordPostEmbeddingPartitionBatchPending(identity, 0, 7, now) {
		t.Fatal("partition pending work was not recorded")
	}
	generation := postEmbeddingHealthTestGeneration(assignments)
	generation.commitFn = func(map[string]map[int]int64) error {
		return errors.New("CommitOffsets response was lost")
	}
	coordinator := newPostEmbeddingOffsetCommitCoordinator(
		context.Background(), generation, "post-embedding", assignments, identity,
	)
	if err := coordinator.Commit(context.Background(), "post-embedding", 0, 7); err == nil {
		t.Fatal("uncertain commit unexpectedly succeeded")
	}
	coordinator.RecordPartitionBatchPending(0, 7, now.Add(30*time.Second))
	state := postEmbeddingPartitionState(t, 0)
	if !state.Pending || !state.PendingSince.Equal(now) || state.PendingNextOffset != 7 || state.LastConfirmedOffset != 4 {
		t.Fatalf("uncertain commit cleared or changed Pending state: %+v", state)
	}
	coordinator.Close()
}

func TestPostEmbeddingRetriesDoNotResetPendingSince(t *testing.T) {
	now := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 2, Offset: 100}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 34, assignments, now)
	defer unregister()
	for attempt, target := range []int64{103, 103, 105} {
		if !recordPostEmbeddingPartitionBatchPending(identity, 2, target, now.Add(time.Duration(attempt)*10*time.Second)) {
			t.Fatalf("retry attempt %d did not retain pending work", attempt+1)
		}
	}
	state := postEmbeddingPartitionState(t, 2)
	if !state.Pending || !state.PendingSince.Equal(now) || state.PendingNextOffset != 105 {
		t.Fatalf("retries reset the pending timer or target: %+v", state)
	}
}

func TestPostEmbeddingIdlePartitionIsNotStalled(t *testing.T) {
	initial := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 0, Offset: 50}}
	_, unregister := setupPostEmbeddingGenerationHealth(t, 35, assignments, initial)
	defer unregister()
	checkAt := initial.Add(postEmbeddingPartitionStallGrace + time.Second)
	recordPostEmbeddingReadySample(t, checkAt)
	if reasons := evaluatePipelineHealth(checkAt); containsWorkerReason(reasons, "worker_post_embedding_partition_stalled") {
		t.Fatalf("idle assigned partition was treated as stalled: %v", reasons)
	}
}

func TestPostEmbeddingZeroAssignmentDoesNotCreateStall(t *testing.T) {
	initial := time.Now().UTC()
	_, unregister := setupPostEmbeddingGenerationHealth(t, 36, nil, initial)
	defer unregister()
	checkAt := initial.Add(postEmbeddingPartitionStallGrace + time.Second)
	recordPostEmbeddingReadySample(t, checkAt)
	if blocked, reason := pipelineBacklogBlocked(PipelinePostEmbedding, checkAt, func() workerPipelineState {
		workerPipelines.RLock()
		defer workerPipelines.RUnlock()
		return workerPipelines.states[PipelinePostEmbedding]
	}()); blocked {
		t.Fatalf("zero-partition assignment created a stall: %s", reason)
	}
}

func TestPostEmbeddingOldGenerationCannotClearNewPartitionState(t *testing.T) {
	now := time.Now().UTC()
	assignmentsA := []kafka.PartitionAssignment{{ID: 0, Offset: 10}}
	identityA, unregisterA := setupPostEmbeddingGenerationHealth(t, 37, assignmentsA, now)
	if !recordPostEmbeddingPartitionBatchPending(identityA, 0, 12, now) {
		t.Fatal("Generation A pending work was not recorded")
	}
	generationA := postEmbeddingHealthTestGeneration(assignmentsA)
	coordinatorA := newPostEmbeddingOffsetCommitCoordinator(
		context.Background(), generationA, "post-embedding", assignmentsA, identityA,
	)
	unregisterA()
	assignmentsB := []kafka.PartitionAssignment{{ID: 0, Offset: 20}}
	identityB, unregisterB := registerKafkaConsumerGenerationSession(
		PipelinePostEmbedding, 0, "post-embedding", "post-embedding-test-group",
		postEmbeddingHealthTestClientID, 38, assignmentsB,
	)
	defer unregisterB()
	if !identityB.valid() || identityB.sessionVersion == identityA.sessionVersion {
		t.Fatalf("Generation B did not receive a new Session Version: A=%+v B=%+v", identityA, identityB)
	}
	if !recordPostEmbeddingPartitionBatchPending(identityB, 0, 23, now.Add(time.Second)) {
		t.Fatal("Generation B pending work was not recorded")
	}
	if err := coordinatorA.Commit(context.Background(), "post-embedding", 0, 12); err != nil {
		t.Fatalf("late Generation A commit failed unexpectedly: %v", err)
	}
	state := postEmbeddingPartitionState(t, 0)
	if !state.Pending || state.PendingNextOffset != 23 || !state.PendingSince.Equal(now.Add(time.Second)) || state.LastConfirmedOffset != 20 {
		t.Fatalf("old Generation commit changed Generation B partition state: %+v", state)
	}
	coordinatorA.Close()
}

func TestPostEmbeddingReaderRestartDoesNotResetPending(t *testing.T) {
	startedAt := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 3, Offset: 70}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 39, assignments, startedAt)
	defer unregister()
	if !recordPostEmbeddingPartitionBatchPending(identity, 3, 74, startedAt) {
		t.Fatal("initial Reader batch was not marked pending")
	}
	// A replacement partition Reader in the same Generation re-fetches the
	// uncommitted target through the same generation identity.
	if !recordPostEmbeddingPartitionBatchPending(identity, 3, 74, startedAt.Add(40*time.Second)) {
		t.Fatal("replacement Reader batch was not recognized as the same pending work")
	}
	state := postEmbeddingPartitionState(t, 3)
	if !state.Pending || !state.PendingSince.Equal(startedAt) || state.PendingNextOffset != 74 {
		t.Fatalf("Reader restart reset pending timer: %+v", state)
	}
}

func TestPostEmbeddingShutdownClearsAndRejectsPartitionHealth(t *testing.T) {
	now := time.Now().UTC()
	assignments := []kafka.PartitionAssignment{{ID: 1, Offset: 20}}
	identity, unregister := setupPostEmbeddingGenerationHealth(t, 42, assignments, now)
	defer unregister()
	if !recordPostEmbeddingPartitionBatchPending(identity, 1, 22, now) {
		t.Fatal("partition pending work was not recorded before shutdown")
	}
	KafkaConsumerSlotCommitted(PipelinePostEmbedding, 0, now.Add(-time.Second))
	sampleVersion, _ := beginKafkaBacklogSample(PipelinePostEmbedding)
	workerPipelines.RLock()
	beforeShutdown := workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0]
	workerPipelines.RUnlock()
	previousSnapshot := workerSnapshot.Load()
	t.Cleanup(func() { workerSnapshot.Store(previousSnapshot) })
	MarkWorkerShuttingDown()
	if recordPostEmbeddingPartitionBatchPending(identity, 1, 23, now.Add(time.Second)) {
		t.Fatal("shutdown accepted new positive partition health state")
	}
	if confirmPostEmbeddingPartitionCommit(identity, 1, 22) {
		t.Fatal("shutdown accepted a partition health commit event")
	}
	if recordKafkaBacklogSample(PipelinePostEmbedding, sampleVersion, 0, now.Add(time.Second), true) {
		t.Fatal("shutdown accepted a positive Kafka backlog sample")
	}
	KafkaConsumerSlotCommitted(PipelinePostEmbedding, 0, now.Add(time.Second))
	registerKafkaConsumerSession(PipelinePostEmbedding, 0, "post-embedding", "post-embedding-test-group", "replacement-client")()
	workerPipelines.RLock()
	afterShutdown := workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0]
	workerPipelines.RUnlock()
	if len(afterShutdown.postEmbeddingPartitions) != 0 {
		t.Fatalf("shutdown retained partition health state: %+v", afterShutdown.postEmbeddingPartitions)
	}
	if !afterShutdown.lastCommittedOffsetAt.Equal(beforeShutdown.lastCommittedOffsetAt) {
		t.Fatalf("shutdown accepted a Kafka commit event: before=%s after=%s", beforeShutdown.lastCommittedOffsetAt, afterShutdown.lastCommittedOffsetAt)
	}
	if afterShutdown.session != beforeShutdown.session || afterShutdown.sessionVersion != beforeShutdown.sessionVersion+1 {
		t.Fatalf("shutdown replaced the Kafka consumer session: before=%+v after=%+v", beforeShutdown, afterShutdown)
	}
}

func TestPostEmbeddingGenerationMembershipAndIdleRecovery(t *testing.T) {
	now := time.Now().UTC()
	assignmentsA := []kafka.PartitionAssignment{{ID: 0, Offset: 10}}
	_, unregisterA := setupPostEmbeddingGenerationHealth(t, 40, assignmentsA, now)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(PipelinePostEmbedding, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), PipelinePostEmbedding, "post-embedding", "post-embedding-test-group",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{
				GroupID: "post-embedding-test-group", State: "Stable",
				Members: []eventing.KafkaGroupMember{{ClientID: postEmbeddingHealthTestClientID, Topics: []string{"post-embedding"}}},
			}, nil
		},
	)
	if err != nil || !valid || !accepted {
		t.Fatalf("Generation A membership/idle sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	if state := pipelineSnapshots()[PipelinePostEmbedding]; state.ConsecutiveFailures != 0 {
		t.Fatalf("Generation A zero-Lag recovery left failures: %+v", state)
	}
	unregisterA()
	workerPipelines.RLock()
	oldProof := workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if oldProof {
		t.Fatal("Generation stop retained old membership proof")
	}
	identityB, unregisterB := registerKafkaConsumerGenerationSession(
		PipelinePostEmbedding, 0, "post-embedding", "post-embedding-test-group",
		postEmbeddingHealthTestClientID, 41, nil,
	)
	defer unregisterB()
	if !identityB.valid() {
		t.Fatal("Generation B health identity was not registered")
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[PipelinePostEmbedding].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.membership.Verified || slot.sessionVersion != identityB.sessionVersion {
		t.Fatalf("Generation B inherited old Session membership: %+v", slot)
	}
	recordPostEmbeddingReadySample(t, time.Now().UTC())
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("new Stable zero-partition Generation did not become ready: %v", reasons)
	}
}
