package recommendation

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

const (
	defaultTracePersistTimeoutMS       = 5000
	defaultTraceQueueCapacity          = 256
	defaultTraceWorkerCount            = 1
	defaultTraceShutdownDrainTimeoutMS = 5000
)

type TracePersistJob struct {
	Request models.RecommendationRequest
	Results []models.RecommendationResultTrace
}

type TraceEnqueueResult string

const (
	TraceEnqueueQueued          TraceEnqueueResult = "queued"
	TraceEnqueueDroppedFull     TraceEnqueueResult = "dropped_full"
	TraceEnqueueDroppedStopping TraceEnqueueResult = "dropped_stopping"
)

type TracePersistOutcome string

const (
	TracePersistSuccess TracePersistOutcome = "success"
	TracePersistError   TracePersistOutcome = "error"
	TracePersistTimeout TracePersistOutcome = "timeout"
)

type TraceEnqueuer interface {
	TryEnqueue(job TracePersistJob) TraceEnqueueResult
}

type TraceDispatcher interface {
	TraceEnqueuer
	Shutdown(ctx context.Context) error
}

type AsyncTraceDispatcher struct {
	repository     TraceRepository
	metrics        Metrics
	jobs           chan TracePersistJob
	persistTimeout time.Duration

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	mu        sync.RWMutex
	accepting bool

	depthMu    sync.Mutex
	queueDepth int

	workers  sync.WaitGroup
	stopOnce sync.Once
	done     chan struct{}
}

func NewAsyncTraceDispatcher(repository TraceRepository, metrics Metrics, cfg config.RecommendationTraceConfig) (*AsyncTraceDispatcher, error) {
	if repository == nil {
		return nil, errors.New("recommendation trace repository is required")
	}
	if metrics == nil {
		metrics = NoopMetrics{}
	}
	if cfg.PersistTimeoutMS <= 0 {
		cfg.PersistTimeoutMS = defaultTracePersistTimeoutMS
	}
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = defaultTraceQueueCapacity
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaultTraceWorkerCount
	}

	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	dispatcher := &AsyncTraceDispatcher{
		repository:      repository,
		metrics:         metrics,
		jobs:            make(chan TracePersistJob, cfg.QueueCapacity),
		persistTimeout:  time.Duration(cfg.PersistTimeoutMS) * time.Millisecond,
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		accepting:       true,
		done:            make(chan struct{}),
	}
	dispatcher.workers.Add(cfg.WorkerCount)
	go func() {
		dispatcher.workers.Wait()
		close(dispatcher.done)
	}()
	for range cfg.WorkerCount {
		go dispatcher.runWorker()
	}
	return dispatcher, nil
}

func (dispatcher *AsyncTraceDispatcher) TryEnqueue(job TracePersistJob) TraceEnqueueResult {
	if dispatcher == nil {
		return TraceEnqueueDroppedStopping
	}
	job.Results = append([]models.RecommendationResultTrace(nil), job.Results...)

	dispatcher.mu.RLock()
	if !dispatcher.accepting {
		dispatcher.mu.RUnlock()
		dispatcher.metrics.RecordTraceEnqueue(TraceEnqueueDroppedStopping)
		return TraceEnqueueDroppedStopping
	}

	dispatcher.depthMu.Lock()
	result := TraceEnqueueDroppedFull
	select {
	case dispatcher.jobs <- job:
		dispatcher.queueDepth++
		dispatcher.metrics.SetTraceQueueDepth(dispatcher.queueDepth)
		result = TraceEnqueueQueued
	default:
	}
	dispatcher.depthMu.Unlock()
	dispatcher.mu.RUnlock()
	dispatcher.metrics.RecordTraceEnqueue(result)
	return result
}

func (dispatcher *AsyncTraceDispatcher) Shutdown(ctx context.Context) error {
	if dispatcher == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dispatcher.stopOnce.Do(func() {
		dispatcher.mu.Lock()
		dispatcher.accepting = false
		close(dispatcher.jobs)
		dispatcher.mu.Unlock()
	})

	select {
	case <-dispatcher.done:
		dispatcher.lifecycleCancel()
		return nil
	case <-ctx.Done():
		dispatcher.lifecycleCancel()
		dispatcher.discardQueuedJobs()
		return ctx.Err()
	}
}

func (dispatcher *AsyncTraceDispatcher) runWorker() {
	defer dispatcher.workers.Done()
	for {
		select {
		case <-dispatcher.lifecycleCtx.Done():
			return
		case job, ok := <-dispatcher.jobs:
			if !ok {
				return
			}
			dispatcher.adjustQueueDepth(-1)
			if dispatcher.lifecycleCtx.Err() != nil {
				continue
			}
			dispatcher.persist(job)
		}
	}
}

func (dispatcher *AsyncTraceDispatcher) persist(job TracePersistJob) {
	started := time.Now()
	persistCtx, cancel := context.WithTimeout(dispatcher.lifecycleCtx, dispatcher.persistTimeout)
	err := dispatcher.repository.PersistServing(persistCtx, job.Request, job.Results)
	cancel()
	duration := time.Since(started)

	outcome := TracePersistSuccess
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			outcome = TracePersistTimeout
		} else {
			outcome = TracePersistError
		}
		dispatcher.metrics.RecordTracePersistFailure()
		log.Printf("[RecommendationTelemetry] asynchronous trace persistence failed: %v", err)
	}
	dispatcher.metrics.RecordTracePersist(outcome, duration)
}

func (dispatcher *AsyncTraceDispatcher) adjustQueueDepth(delta int) {
	dispatcher.depthMu.Lock()
	defer dispatcher.depthMu.Unlock()
	dispatcher.queueDepth += delta
	if dispatcher.queueDepth < 0 {
		dispatcher.queueDepth = 0
	}
	dispatcher.metrics.SetTraceQueueDepth(dispatcher.queueDepth)
}

func (dispatcher *AsyncTraceDispatcher) discardQueuedJobs() {
	for {
		select {
		case _, ok := <-dispatcher.jobs:
			if !ok {
				dispatcher.setQueueDepth(0)
				return
			}
			dispatcher.adjustQueueDepth(-1)
		default:
			dispatcher.setQueueDepth(0)
			return
		}
	}
}

func (dispatcher *AsyncTraceDispatcher) setQueueDepth(depth int) {
	dispatcher.depthMu.Lock()
	defer dispatcher.depthMu.Unlock()
	if depth < 0 {
		depth = 0
	}
	dispatcher.queueDepth = depth
	dispatcher.metrics.SetTraceQueueDepth(depth)
}

var _ TraceDispatcher = (*AsyncTraceDispatcher)(nil)
