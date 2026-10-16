package recommendation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type generationCaptureMetrics struct {
	NoopMetrics
	mu       sync.Mutex
	duration time.Duration
	observed bool
}

func (metrics *generationCaptureMetrics) ObserveGenerationDuration(_ string, duration time.Duration) {
	metrics.mu.Lock()
	metrics.duration = duration
	metrics.observed = true
	metrics.mu.Unlock()
}

func (metrics *generationCaptureMetrics) snapshot() (time.Duration, bool) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	return metrics.duration, metrics.observed
}

type serviceDelayHistoryStore struct {
	*serviceTestHistoryStore
	onRecord func()
	delay    time.Duration
}

func (store *serviceDelayHistoryStore) RecordUserServed(ctx context.Context, userID uint, postIDs []uint, window HistoryWindow) error {
	if store.onRecord != nil {
		store.onRecord()
	}
	if store.delay > 0 {
		time.Sleep(store.delay)
	}
	return store.serviceTestHistoryStore.RecordUserServed(ctx, userID, postIDs, window)
}

type serviceCancelHistoryStore struct {
	*serviceTestHistoryStore
	cancel context.CancelFunc
}

func (store *serviceCancelHistoryStore) RecordUserServed(ctx context.Context, userID uint, postIDs []uint, window HistoryWindow) error {
	store.cancel()
	return store.serviceTestHistoryStore.RecordUserServed(ctx, userID, postIDs, window)
}

func TestRecommendationServeReturnsWhileAsyncTracePersistenceIsBlocked(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	metrics := &generationCaptureMetrics{}
	var repositoryContext context.Context
	dispatcher := newTraceDispatcherForTest(t, traceRepositoryFunc(func(ctx context.Context, _ models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		repositoryContext = ctx
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}), metrics, traceDispatcherConfig(1, 1, 5*time.Second))
	requestID := uuid.NewString()
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: &serviceTestCandidateRepository{
				recent: []Candidate{testCandidate(1, CandidateSourceRecent)},
				posts:  map[uint]models.Post{1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Minute)}, AuthorID: 2}},
			},
			Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
		},
		ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		TraceEnqueuer:   dispatcher,
		Metrics:         metrics,
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: requestID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalized := make(chan error, 1)
	go func() {
		finalizer, ok := service.(ServingFinalizer)
		if !ok {
			finalized <- errors.New("recommendation service does not implement ServingFinalizer")
			return
		}
		finalized <- finalizer.FinalizeServing(context.Background(), result, result.Selected)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("trace worker did not enter blocked persistence")
	}
	select {
	case finalizeErr := <-finalized:
		if finalizeErr != nil {
			t.Fatalf("finalize response: %v", finalizeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("FinalizeServing waited for blocked trace repository persistence")
	}
	if generation, observed := metrics.snapshot(); !observed || generation < 0 {
		t.Fatalf("generation duration not recorded before Serve returned: %v observed=%v", generation, observed)
	}
	if repositoryContext == nil || repositoryContext.Err() != nil {
		t.Fatalf("trace persistence context was canceled with the completed request: %v", repositoryContext)
	}
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Shutdown(ctx); err != nil {
		t.Fatalf("dispatcher shutdown: %v", err)
	}
}

func TestRecommendationGenerationDurationIsFrozenBeforeHistoryAndReusedByTrace(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	metrics := &generationCaptureMetrics{}
	var observedBeforeHistory atomic.Bool
	history := &serviceDelayHistoryStore{
		serviceTestHistoryStore: &serviceTestHistoryStore{},
		delay:                   60 * time.Millisecond,
		onRecord: func() {
			_, observed := metrics.snapshot()
			observedBeforeHistory.Store(observed)
		},
	}
	enqueuer := &serviceTestTraceEnqueuer{}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: &serviceTestCandidateRepository{
				recent: []Candidate{testCandidate(1, CandidateSourceRecent)},
				posts:  map[uint]models.Post{1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Minute)}, AuthorID: 2}},
			},
			Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
			History:  history,
		},
		ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		TraceEnqueuer:   enqueuer,
		Metrics:         metrics,
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizeServiceTestResult(t, service, result)
	observedDuration, observed := metrics.snapshot()
	if !observed || !observedBeforeHistory.Load() {
		t.Fatalf("generation metric was not frozen before synchronous history: observed=%v beforeHistory=%v", observed, observedBeforeHistory.Load())
	}
	if got, want := enqueuer.job.Request.GenerationLatencyMS, observedDuration.Milliseconds(); got != want {
		t.Fatalf("trace generation latency=%dms, metric duration=%dms", got, want)
	}
}

func TestRecommendationServeSkipsTraceEnqueueWhenHistoryExpiresServingContext(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	enqueuer := &serviceTestTraceEnqueuer{}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: &serviceTestCandidateRepository{
				recent: []Candidate{testCandidate(1, CandidateSourceRecent)},
				posts:  map[uint]models.Post{1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Minute)}, AuthorID: 2}},
			},
			Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
			History:  &serviceCancelHistoryStore{serviceTestHistoryStore: &serviceTestHistoryStore{}, cancel: cancel},
		},
		ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		TraceEnqueuer:   enqueuer,
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(ctx, ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatalf("best-effort history cancellation changed serving response: %v", err)
	}
	finalizer, ok := service.(ServingFinalizer)
	if !ok {
		t.Fatal("recommendation service does not implement ServingFinalizer")
	}
	if err := finalizer.FinalizeServing(ctx, result, result.Selected); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if enqueuer.enqueueCalls != 0 {
		t.Fatalf("trace enqueue calls=%d after serving context expired", enqueuer.enqueueCalls)
	}
}
