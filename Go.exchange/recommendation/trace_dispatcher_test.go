package recommendation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

type traceRepositoryFunc func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error

func (repository traceRepositoryFunc) PersistServing(ctx context.Context, request models.RecommendationRequest, results []models.RecommendationResultTrace) error {
	return repository(ctx, request, results)
}

type traceDispatcherTestMetrics struct {
	NoopMetrics

	mu               sync.Mutex
	enqueueResults   []TraceEnqueueResult
	persistResults   []TracePersistOutcome
	persistDurations []time.Duration
	queueDepth       int
	persistFailures  int
	generation       time.Duration
}

func (metrics *traceDispatcherTestMetrics) RecordTraceEnqueue(result TraceEnqueueResult) {
	metrics.mu.Lock()
	metrics.enqueueResults = append(metrics.enqueueResults, result)
	metrics.mu.Unlock()
}

func (metrics *traceDispatcherTestMetrics) SetTraceQueueDepth(depth int) {
	metrics.mu.Lock()
	metrics.queueDepth = depth
	metrics.mu.Unlock()
}

func (metrics *traceDispatcherTestMetrics) RecordTracePersist(outcome TracePersistOutcome, duration time.Duration) {
	metrics.mu.Lock()
	metrics.persistResults = append(metrics.persistResults, outcome)
	metrics.persistDurations = append(metrics.persistDurations, duration)
	metrics.mu.Unlock()
}

func (metrics *traceDispatcherTestMetrics) RecordTracePersistFailure() {
	metrics.mu.Lock()
	metrics.persistFailures++
	metrics.mu.Unlock()
}

func (metrics *traceDispatcherTestMetrics) ObserveGenerationDuration(_ string, duration time.Duration) {
	metrics.mu.Lock()
	metrics.generation = duration
	metrics.mu.Unlock()
}

func (metrics *traceDispatcherTestMetrics) snapshot() (queueDepth int, enqueue []TraceEnqueueResult, outcomes []TracePersistOutcome, failures int, generation time.Duration) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	return metrics.queueDepth, append([]TraceEnqueueResult(nil), metrics.enqueueResults...), append([]TracePersistOutcome(nil), metrics.persistResults...), metrics.persistFailures, metrics.generation
}

func traceDispatcherConfig(workers, capacity int, persistTimeout time.Duration) config.RecommendationTraceConfig {
	return config.RecommendationTraceConfig{
		PersistTimeoutMS:       int(persistTimeout / time.Millisecond),
		QueueCapacity:          capacity,
		WorkerCount:            workers,
		ShutdownDrainTimeoutMS: 5000,
	}
}

func newTraceDispatcherForTest(t *testing.T, repository TraceRepository, metrics Metrics, cfg config.RecommendationTraceConfig) *AsyncTraceDispatcher {
	t.Helper()
	dispatcher, err := NewAsyncTraceDispatcher(repository, metrics, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(ctx)
	})
	return dispatcher
}

func TestTraceDispatcherEnqueueIsNonBlockingAndFullQueueDropsWithoutFallback(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var persistCalls atomic.Int32
	repository := traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		persistCalls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	metrics := &traceDispatcherTestMetrics{}
	dispatcher := newTraceDispatcherForTest(t, repository, metrics, traceDispatcherConfig(1, 1, 5*time.Second))
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	if got := dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: "first"}}); got != TraceEnqueueQueued {
		t.Fatalf("first enqueue=%q want queued", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start the first persistence")
	}

	queued := make(chan TraceEnqueueResult, 1)
	go func() {
		queued <- dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: "second"}})
	}()
	select {
	case got := <-queued:
		if got != TraceEnqueueQueued {
			t.Fatalf("second enqueue=%q want queued", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("TryEnqueue waited for blocked repository persistence")
	}

	full := make(chan TraceEnqueueResult, 1)
	go func() {
		full <- dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: "dropped"}})
	}()
	select {
	case got := <-full:
		if got != TraceEnqueueDroppedFull {
			t.Fatalf("full queue enqueue=%q want dropped_full", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("full queue admission blocked")
	}
	if got := persistCalls.Load(); got != 1 {
		t.Fatalf("repository calls while worker blocked=%d want 1; synchronous fallback occurred", got)
	}
	depth, _, _, _, _ := metrics.snapshot()
	if depth != 1 {
		t.Fatalf("queue depth while worker is blocked=%d want 1", depth)
	}

	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after releasing repository: %v", err)
	}
	if got := persistCalls.Load(); got != 2 {
		t.Fatalf("persist calls=%d want two accepted jobs", got)
	}
	depth, _, _, _, _ = metrics.snapshot()
	if depth != 0 {
		t.Fatalf("queue depth after drain=%d want 0", depth)
	}
	_, enqueueResults, _, _, _ := metrics.snapshot()
	if fmt.Sprint(enqueueResults) != fmt.Sprint([]TraceEnqueueResult{TraceEnqueueQueued, TraceEnqueueQueued, TraceEnqueueDroppedFull}) {
		t.Fatalf("enqueue metrics=%v", enqueueResults)
	}
}

func TestTraceDispatcherPersistenceDoesNotInheritRequestCancellation(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	repository := traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		started <- ctx
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	dispatcher := newTraceDispatcherForTest(t, repository, NoopMetrics{}, traceDispatcherConfig(1, 1, 5*time.Second))
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	if got := dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: "independent"}}); got != TraceEnqueueQueued {
		t.Fatalf("enqueue=%q want queued", got)
	}
	var persistCtx context.Context
	select {
	case persistCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("persistence did not start")
	}
	cancelRequest()
	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatalf("request context error=%v want canceled", requestCtx.Err())
	}
	select {
	case <-persistCtx.Done():
		t.Fatalf("trace persistence inherited request cancellation: %v", persistCtx.Err())
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTraceDispatcherPersistenceTimeoutRecordsLegacyFailure(t *testing.T) {
	metrics := &traceDispatcherTestMetrics{}
	repository := traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		<-ctx.Done()
		return ctx.Err()
	})
	dispatcher := newTraceDispatcherForTest(t, repository, metrics, traceDispatcherConfig(1, 1, 25*time.Millisecond))
	if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueQueued {
		t.Fatalf("enqueue=%q want queued", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, _, outcomes, failures, _ := metrics.snapshot()
	if fmt.Sprint(outcomes) != fmt.Sprint([]TracePersistOutcome{TracePersistTimeout}) || failures != 1 {
		t.Fatalf("persist outcomes=%v legacy failures=%d", outcomes, failures)
	}
}

func TestTraceDispatcherRepositoryErrorRecordsBoundedErrorOutcome(t *testing.T) {
	metrics := &traceDispatcherTestMetrics{}
	dispatcher := newTraceDispatcherForTest(t, traceRepositoryFunc(func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		return errors.New("database unavailable")
	}), metrics, traceDispatcherConfig(1, 1, time.Second))
	if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueQueued {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, _, outcomes, failures, _ := metrics.snapshot()
	if fmt.Sprint(outcomes) != fmt.Sprint([]TracePersistOutcome{TracePersistError}) || failures != 1 {
		t.Fatalf("persist outcomes=%v legacy failures=%d", outcomes, failures)
	}
}

func TestTraceDispatcherGracefulShutdownDrainsAcceptedJobs(t *testing.T) {
	var persistCalls atomic.Int32
	dispatcher := newTraceDispatcherForTest(t, traceRepositoryFunc(func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		persistCalls.Add(1)
		return nil
	}), NoopMetrics{}, traceDispatcherConfig(1, 4, time.Second))
	for index := 0; index < 4; index++ {
		if got := dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: fmt.Sprint(index)}}); got != TraceEnqueueQueued {
			t.Fatalf("enqueue %d=%q", index, got)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
	if got := persistCalls.Load(); got != 4 {
		t.Fatalf("persist calls=%d want 4", got)
	}
	if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueDroppedStopping {
		t.Fatalf("enqueue after shutdown=%q want dropped_stopping", got)
	}
}

func TestTraceDispatcherShutdownIsBoundedAndAbandonsQueuedJobs(t *testing.T) {
	started := make(chan struct{}, 1)
	metrics := &traceDispatcherTestMetrics{}
	repository := traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	dispatcher := newTraceDispatcherForTest(t, repository, metrics, traceDispatcherConfig(1, 2, 30*time.Second))
	if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueQueued {
		t.Fatal(got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocked persistence did not start")
	}
	for range 2 {
		if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueQueued {
			t.Fatalf("queue enqueue=%q", got)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := dispatcher.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v want deadline exceeded", err)
	}
	secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Second)
	defer secondCancel()
	if err := dispatcher.Shutdown(secondCtx); err != nil {
		t.Fatalf("worker did not exit after lifecycle cancellation: %v", err)
	}
	depth, _, outcomes, failures, _ := metrics.snapshot()
	if depth != 0 {
		t.Fatalf("queue depth after abandonment=%d want 0", depth)
	}
	if fmt.Sprint(outcomes) != fmt.Sprint([]TracePersistOutcome{TracePersistTimeout}) || failures != 1 {
		t.Fatalf("persist outcomes=%v failures=%d", outcomes, failures)
	}
}

func TestTraceDispatcherTryEnqueueShutdownRaceAndIdempotency(t *testing.T) {
	var persistCalls atomic.Int32
	dispatcher := newTraceDispatcherForTest(t, traceRepositoryFunc(func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		persistCalls.Add(1)
		return nil
	}), NoopMetrics{}, traceDispatcherConfig(2, 64, time.Second))
	var producers sync.WaitGroup
	for producer := 0; producer < 8; producer++ {
		producers.Add(1)
		go func(producer int) {
			defer producers.Done()
			for item := 0; item < 500; item++ {
				dispatcher.TryEnqueue(TracePersistJob{Request: models.RecommendationRequest{RequestID: fmt.Sprintf("%d-%d", producer, item)}})
			}
		}(producer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- dispatcher.Shutdown(ctx) }()
	producers.Wait()
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown while racing producers: %v", err)
	}
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatalf("repeated shutdown: %v", err)
	}
	if got := dispatcher.TryEnqueue(TracePersistJob{}); got != TraceEnqueueDroppedStopping {
		t.Fatalf("post-shutdown enqueue=%q", got)
	}
}

func TestTraceDispatcherCopiesResultSliceBeforeAdmission(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var persisted atomic.Int64
	dispatcher := newTraceDispatcherForTest(t, traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, results []models.RecommendationResultTrace) error {
		started <- struct{}{}
		select {
		case <-release:
			persisted.Store(int64(results[0].Position))
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}), NoopMetrics{}, traceDispatcherConfig(1, 1, time.Second))
	results := []models.RecommendationResultTrace{{Position: 1}}
	if got := dispatcher.TryEnqueue(TracePersistJob{Results: results}); got != TraceEnqueueQueued {
		t.Fatal(got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("repository was not invoked")
	}
	results[0].Position = 99
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := persisted.Load(); got != 1 {
		t.Fatalf("persisted mutated result position=%d want copied value 1", got)
	}
}
