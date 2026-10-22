package recommendation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/gorm"
)

type memorySnapshotStore struct {
	mu        sync.Mutex
	snapshots map[string]CandidatePoolSnapshot
}

func equalFloat32Slices(left, right []float32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func newMemorySnapshotStore() *memorySnapshotStore {
	return &memorySnapshotStore{snapshots: make(map[string]CandidatePoolSnapshot)}
}

func (store *memorySnapshotStore) Create(_ context.Context, snapshot CandidatePoolSnapshot, _ time.Duration, _ int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.snapshots[snapshot.ID]; exists {
		return errors.New("snapshot already exists")
	}
	snapshot.Version = candidatePoolSnapshotVersion
	snapshot.Revision = 1
	store.snapshots[snapshot.ID] = cloneSnapshot(snapshot)
	return nil
}

func (store *memorySnapshotStore) Load(_ context.Context, id string) (CandidatePoolSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot, exists := store.snapshots[id]
	if !exists {
		return CandidatePoolSnapshot{}, ErrCursorExpired
	}
	return cloneSnapshot(snapshot), nil
}

func (store *memorySnapshotStore) CompareAndSwap(_ context.Context, id string, expected int64, snapshot CandidatePoolSnapshot, _ int) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.snapshots[id]
	if !exists {
		return false, ErrCursorExpired
	}
	if current.Revision != expected {
		return false, nil
	}
	if snapshot.Revision != expected+1 {
		return false, ErrSnapshotUnavailable
	}
	store.snapshots[id] = cloneSnapshot(snapshot)
	return true, nil
}

func (repository *serviceTestCandidateRepository) LoadEligibleSnapshotCandidates(ctx context.Context, query CandidateQuery, candidates []Candidate) ([]RankedCandidate, error) {
	eligible, _, err := repository.LoadSnapshotPage(ctx, query, candidates, nil)
	return eligible, err
}

func (repository *serviceTestCandidateRepository) LoadSnapshotPage(_ context.Context, query CandidateQuery, candidates []Candidate, contextPostIDs []uint) ([]RankedCandidate, map[uint][]float32, error) {
	repository.snapshotEligibilityCalls++
	repository.snapshotQueries = append(repository.snapshotQueries, query)
	repository.snapshotCandidateIDsRead += len(candidates)
	repository.embeddingReadCalls++
	repository.snapshotEmbeddingIDsRead += len(candidates) + len(contextPostIDs)
	if len(contextPostIDs) > 0 {
		repository.snapshotPageCandidateIDs = append(repository.snapshotPageCandidateIDs, len(candidates))
	}
	if len(candidates) > 0 {
		repository.snapshotDatabaseQueries += 2 // posts plus author preload
	}
	if len(candidates)+len(contextPostIDs) > 0 {
		repository.snapshotDatabaseQueries++ // one batched embedding query
	}
	items := make([]RankedCandidate, 0, len(candidates))
	contextEmbeddings := make(map[uint][]float32, len(contextPostIDs))
	for _, postID := range contextPostIDs {
		if embedding, exists := repository.embeddings[postID]; exists {
			contextEmbeddings[postID] = append([]float32(nil), embedding...)
		}
	}
	for _, candidate := range candidates {
		if _, ineligible := repository.snapshotIneligibleIDs[candidate.PostID]; ineligible {
			continue
		}
		post, exists := repository.posts[candidate.PostID]
		if !exists || post.ID == 0 || post.AuthorID == 0 {
			continue
		}
		if query.Range == CandidateRangeRecent &&
			(post.CreatedAt.Before(query.RecentCutoff) || post.CreatedAt.After(query.Now)) {
			continue
		}
		items = append(items, RankedCandidate{Candidate: candidate, Post: post, Embedding: append([]float32(nil), repository.embeddings[candidate.PostID]...)})
	}
	return items, contextEmbeddings, nil
}

func TestRecommendationSnapshotRevalidatesServingEmbeddingWithoutRecentRangeFilter(t *testing.T) {
	store := newMemorySnapshotStore()
	service, repository, now := newSnapshotTestService(t, store)
	first := firstSnapshotResult(t, service, now)
	snapshot, err := store.Load(context.Background(), first.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, exists := snapshotPageAt(snapshot, 0)
	if !exists || len(page.Items) < 2 {
		t.Fatalf("initial snapshot page=%#v, want multiple eligible posts", page)
	}
	invalidatedPostID := page.Items[0].PostID
	repository.snapshotIneligibleIDs = map[uint]struct{}{invalidatedPostID: {}}
	rematerialized, _, err := service.materializeSnapshotPage(
		context.Background(), first.finalization.request, snapshot, page,
		first.Profile, first.LanguageContext, first.EmbeddingVersion,
	)
	if err != nil {
		t.Fatalf("rematerialize snapshot after serving embedding removal: %v", err)
	}
	if len(rematerialized.Selected) != len(page.Items)-1 || containsPostID(selectedPostIDs(rematerialized.Selected), invalidatedPostID) {
		t.Fatalf("revalidated snapshot returned %v, removed Post %d, page items=%d", selectedPostIDs(rematerialized.Selected), invalidatedPostID, len(page.Items))
	}
	if len(repository.snapshotQueries) != 1 {
		t.Fatalf("snapshot revalidation calls=%d, want one", len(repository.snapshotQueries))
	}
	query := repository.snapshotQueries[0]
	if query.ServingVersion != first.EmbeddingVersion || query.Range != CandidateRangeAny {
		t.Fatalf("snapshot revalidation query=%+v, want serving version %q and CandidateRangeAny", query, first.EmbeddingVersion)
	}
}

func TestRecommendationSnapshotRevalidatesWindowAtCurrentRequestTime(t *testing.T) {
	service, repository, servedAt := newSnapshotTestService(t, newMemorySnapshotStore())
	boundaryCandidate := testCandidate(1, CandidateSourceSemantic)
	validCandidate := testCandidate(2, CandidateSourceSemantic)
	boundaryPost := repository.posts[boundaryCandidate.PostID]
	boundaryPost.CreatedAt = servedAt.AddDate(0, 0, -service.config.Recommendation.PrimaryWindowDays).Add(-time.Second)
	repository.posts[boundaryCandidate.PostID] = boundaryPost
	profile := Profile{PositiveVector: []float32{1}, ProfileStatus: ProfileStatusHit}
	page := CachedPage{
		Index: 0, RequestID: "original-page-request", ServedAt: servedAt,
		Items: []CachedCandidate{
			{Candidate: boundaryCandidate, PostID: boundaryCandidate.PostID, AuthorID: boundaryPost.AuthorID, CreatedAt: boundaryPost.CreatedAt},
			{Candidate: validCandidate, PostID: validCandidate.PostID, AuthorID: repository.posts[validCandidate.PostID].AuthorID, CreatedAt: repository.posts[validCandidate.PostID].CreatedAt},
		},
	}
	snapshot := CandidatePoolSnapshot{ID: "snapshot-window-revalidation", Candidates: append([]CachedCandidate(nil), page.Items...)}
	candidates := []Candidate{boundaryCandidate, validCandidate}

	initial, err := service.loadEligibleSnapshotCandidates(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Now: servedAt,
	}, "embedding-v1", profile, candidates)
	if err != nil {
		t.Fatalf("load candidates at original page time: %v", err)
	}
	if len(initial) != 2 {
		t.Fatalf("original page eligible count=%d, want 2", len(initial))
	}

	currentRequestTime := servedAt.Add(2 * time.Second)
	result, returnedRequest, err := service.materializeSnapshotPage(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20,
		RequestID: "retry-request", Now: currentRequestTime,
	}, snapshot, page, profile, LanguageContext{}, "embedding-v1")
	if err != nil {
		t.Fatalf("revalidate cached page at retry time: %v", err)
	}
	if got := selectedPostIDs(result.Selected); !equalUintSlices(got, []uint{boundaryCandidate.PostID, validCandidate.PostID}) {
		t.Fatalf("revalidated page IDs=%v, want historical and current candidates [%d %d]", got, boundaryCandidate.PostID, validCandidate.PostID)
	}
	if result.RequestID != page.RequestID || returnedRequest.RequestID != page.RequestID || !result.Now.Equal(servedAt) || !returnedRequest.Now.Equal(servedAt) {
		t.Fatalf("page replay metadata changed: result=%+v request=%+v, want original request ID and ServedAt", result, returnedRequest)
	}
	if len(repository.snapshotQueries) != 2 {
		t.Fatalf("snapshot eligibility calls=%d, want original and retry checks", len(repository.snapshotQueries))
	}
	query := repository.snapshotQueries[1]
	if !query.Now.Equal(currentRequestTime) || query.Range != CandidateRangeAny {
		t.Fatalf("retry eligibility query=%+v, want current request time and CandidateRangeAny", query)
	}
}

func TestRecommendationSnapshotCursorExpiresAfterServingVersionChanges(t *testing.T) {
	service, _, now := newSnapshotTestService(t, newMemorySnapshotStore())
	first := firstSnapshotResult(t, service, now)
	if first.NextCursor == "" {
		t.Fatal("first snapshot response has no continuation cursor")
	}
	service.dependencies.ServingVersions = serviceTestVersionProvider{version: "embedding-v2"}
	_, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20,
		Cursor: first.NextCursor, RequestID: uuid.NewString(), Now: now.Add(time.Second),
	})
	if !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("cursor after serving version switch error=%v, want %v", err, ErrCursorExpired)
	}
}

type unavailableSnapshotStore struct{ SnapshotStore }

func (unavailableSnapshotStore) Load(context.Context, string) (CandidatePoolSnapshot, error) {
	return CandidatePoolSnapshot{}, fmt.Errorf("%w: redis unavailable", ErrSnapshotUnavailable)
}

type snapshotCASConflictOnce struct {
	SnapshotStore
	mu        sync.Mutex
	conflicts int
}

type snapshotCASRevisionConflictOnce struct {
	SnapshotStore
	once     sync.Once
	mu       sync.Mutex
	proposed []CandidatePoolSnapshot
}

func (store *snapshotCASRevisionConflictOnce) CompareAndSwap(ctx context.Context, id string, revision int64, snapshot CandidatePoolSnapshot, maxBytes int) (bool, error) {
	store.mu.Lock()
	store.proposed = append(store.proposed, cloneSnapshot(snapshot))
	store.mu.Unlock()
	conflicted := false
	store.once.Do(func() { conflicted = true })
	if conflicted {
		current, err := store.SnapshotStore.Load(ctx, id)
		if err != nil {
			return false, err
		}
		updated := cloneSnapshot(current)
		updated.Revision = current.Revision + 1
		if committed, err := store.SnapshotStore.CompareAndSwap(ctx, id, current.Revision, updated, maxBytes); err != nil || !committed {
			if err != nil {
				return false, err
			}
			return false, errors.New("failed to inject snapshot revision conflict")
		}
		return false, nil
	}
	return store.SnapshotStore.CompareAndSwap(ctx, id, revision, snapshot, maxBytes)
}

func (store *snapshotCASConflictOnce) CompareAndSwap(ctx context.Context, id string, revision int64, snapshot CandidatePoolSnapshot, maxBytes int) (bool, error) {
	store.mu.Lock()
	if store.conflicts == 0 {
		store.conflicts++
		store.mu.Unlock()
		return false, nil
	}
	store.mu.Unlock()
	return store.SnapshotStore.CompareAndSwap(ctx, id, revision, snapshot, maxBytes)
}

type snapshotCASFailure struct {
	SnapshotStore
	err error
}

func (store snapshotCASFailure) CompareAndSwap(context.Context, string, int64, CandidatePoolSnapshot, int) (bool, error) {
	return false, store.err
}

func firstSnapshotResult(t *testing.T, service *RecommendationService, now time.Time) ServeResult {
	t.Helper()
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20,
		RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatalf("serve first snapshot page: %v", err)
	}
	return result
}

func TestRecommendationTraceQueueFullDoesNotFailServing(t *testing.T) {
	for _, outcome := range []TraceEnqueueResult{TraceEnqueueDroppedFull, TraceEnqueueDroppedStopping} {
		t.Run(string(outcome), func(t *testing.T) {
			service, _, now := newSnapshotTestService(t, newMemorySnapshotStore())
			enqueuer := &serviceTestTraceEnqueuer{result: outcome}
			service.dependencies.TraceEnqueuer = enqueuer
			result := firstSnapshotResult(t, service, now)
			if len(result.Selected) == 0 {
				t.Fatal("recommendation page was empty")
			}
			facts, err := service.FinalizeMappedServing(context.Background(), result, result.Selected)
			if err != nil {
				t.Fatalf("dropped trace prevented page finalization: %v", err)
			}
			if len(result.Selected) != len(enqueuer.job.Results) || enqueuer.enqueueCalls != 1 {
				t.Fatalf("selected=%d trace results=%d enqueue calls=%d", len(result.Selected), len(enqueuer.job.Results), enqueuer.enqueueCalls)
			}
			if len(facts) != 0 {
				t.Fatalf("tracking facts unexpectedly enabled: %d", len(facts))
			}
		})
	}
}

func TestRecommendationFinalizeCASConflictDoesNotDuplicateSideEffects(t *testing.T) {
	base := newMemorySnapshotStore()
	store := &snapshotCASConflictOnce{SnapshotStore: base}
	service, _, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected); err != nil {
		t.Fatalf("finalize after one CAS conflict: %v", err)
	}
	if history.pageRecordAttempts != 1 || traces.enqueueCalls != 1 {
		t.Fatalf("history page writes=%d trace enqueues=%d, want one each", history.pageRecordAttempts, traces.enqueueCalls)
	}
	snapshot, err := base.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := snapshotPageAt(snapshot, 0)
	if !ok || !snapshotPageCommitted(page) || !equalUintSlices(page.ReturnedPostIDs, selectedPostIDs(result.Selected)) {
		t.Fatalf("committed page=%#v", page)
	}
}

func TestRecommendationFinalizeRedisFailureDoesNotRecordUncommittedPage(t *testing.T) {
	base := newMemorySnapshotStore()
	store := snapshotCASFailure{SnapshotStore: base, err: errors.New("redis write failed")}
	service, _, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected); err == nil {
		t.Fatal("expected Redis CAS error")
	}
	if history.pageRecordAttempts != 0 || traces.enqueueCalls != 0 {
		t.Fatalf("uncommitted page had side effects: history=%d trace=%d", history.pageRecordAttempts, traces.enqueueCalls)
	}
	snapshot, err := base.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := snapshotPageAt(snapshot, 0)
	if snapshotPageCommitted(page) {
		t.Fatalf("page unexpectedly committed after CAS failure: %#v", page)
	}
}

func TestRecommendationFinalizeRetryKeepsStableReturnedIDs(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	firstReturned := result.Selected[:15]
	if _, err := service.FinalizeMappedServing(context.Background(), result, firstReturned); err != nil {
		t.Fatal(err)
	}
	if _, err := service.FinalizeMappedServing(context.Background(), result, firstReturned); err != nil {
		t.Fatalf("same mapped page retry: %v", err)
	}
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected[10:]); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("changed mapped page error=%v, want cursor expired", err)
	}
	snapshot, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := snapshotPageAt(snapshot, 0)
	wantIDs := selectedPostIDs(firstReturned)
	if !equalUintSlices(page.ReturnedPostIDs, wantIDs) || !equalUintSlices(history.lastUserIDs, wantIDs) {
		t.Fatalf("committed IDs=%v history IDs=%v want=%v", page.ReturnedPostIDs, history.lastUserIDs, wantIDs)
	}
	if traces.enqueueCalls != 1 || history.pageRecordAttempts != 1 {
		t.Fatalf("trace enqueues=%d history page writes=%d want one each", traces.enqueueCalls, history.pageRecordAttempts)
	}
}

func TestRecommendationPreselectedPageInvalidationIsAtomic(t *testing.T) {
	base := newMemorySnapshotStore()
	store := &snapshotCASRevisionConflictOnce{SnapshotStore: base}
	service, repository, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	before, err := base.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	oldPageOne, ok := snapshotPageAt(before, 1)
	if !ok {
		t.Fatal("expected second page to be preselected")
	}
	returned := append([]SelectedCandidate(nil), result.Selected...)
	filteredID := returned[len(returned)-1].Post.ID
	returned = returned[:len(returned)-1]
	if _, err := service.FinalizeMappedServing(context.Background(), result, returned); err != nil {
		t.Fatalf("finalize filtered page after a concurrent CAS revision: %v", err)
	}

	after, err := base.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	pageZero, ok := snapshotPageAt(after, 0)
	if !ok || !snapshotPageCommitted(pageZero) || !samePostIDs(pageZero.ReturnedPostIDs, selectedPostIDs(returned)) {
		t.Fatalf("committed page 0=%#v returned=%v", pageZero, selectedPostIDs(returned))
	}
	if _, exists := snapshotPageAt(after, 1); exists {
		t.Fatal("dependent page 1 survived the same-CAS invalidation")
	}
	store.mu.Lock()
	proposed := append([]CandidatePoolSnapshot(nil), store.proposed...)
	store.mu.Unlock()
	if len(proposed) < 2 {
		t.Fatalf("CAS attempts=%d, want retry after injected conflict", len(proposed))
	}
	atomicState := proposed[1]
	atomicZero, zeroExists := snapshotPageAt(atomicState, 0)
	if !zeroExists || !snapshotPageCommitted(atomicZero) {
		t.Fatalf("retry CAS did not include committed page 0: %#v", atomicZero)
	}
	if _, exists := snapshotPageAt(atomicState, 1); exists || !samePostIDs(atomicState.UsedPostIDs, selectedPostIDs(returned)) {
		t.Fatalf("retry CAS did not atomically invalidate page 1 and rebuild reservations: pages=%v used=%v", atomicState.Pages, atomicState.UsedPostIDs)
	}
	if !samePostIDs(after.UsedPostIDs, selectedPostIDs(returned)) {
		t.Fatalf("used IDs=%v want committed IDs=%v", after.UsedPostIDs, selectedPostIDs(returned))
	}
	if !samePostIDs(after.MapperRejectedPostIDs, []uint{filteredID}) {
		t.Fatalf("mapper rejected IDs=%v want [%d]", after.MapperRejectedPostIDs, filteredID)
	}
	for _, id := range cachedPagePostIDs(oldPageOne) {
		if containsUint(after.UsedPostIDs, id) {
			t.Fatalf("invalidated page ID %d remains reserved", id)
		}
	}
	if history.pageRecordAttempts != 1 || traces.enqueueCalls != 1 {
		t.Fatalf("invalidation duplicated side effects: history=%d trace=%d", history.pageRecordAttempts, traces.enqueueCalls)
	}

	request := ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: result.NextCursor, Now: now.Add(time.Second)}
	regenerated, err := service.Serve(context.Background(), request)
	if err != nil {
		t.Fatalf("reuse original cursor to regenerate page 1: %v", err)
	}
	for _, id := range selectedPostIDs(regenerated.Selected) {
		if containsUint(selectedPostIDs(returned), id) || id == filteredID {
			t.Fatalf("regenerated page repeated committed or mapper-rejected post %d", id)
		}
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		count := 0
		for _, call := range *repository.calls {
			if call == source {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s recall calls=%d, want one full recall", source, count)
		}
	}
}

func TestRecommendationPreselectionRejectsInvalidationAfterLaterPageCommit(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	result := firstSnapshotResult(t, service, now)
	snapshot, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	pageOne, ok := snapshotPageAt(snapshot, 1)
	if !ok {
		t.Fatal("expected page 1 preselection")
	}
	pageOne.Committed = true
	pageOne.ReturnedPostIDs = cachedPagePostIDs(pageOne)
	updated := cloneSnapshot(snapshot)
	for index := range updated.Pages {
		if updated.Pages[index].Index == 1 {
			updated.Pages[index] = pageOne
		}
	}
	updated.Revision++
	if committed, err := store.CompareAndSwap(context.Background(), snapshot.ID, snapshot.Revision, updated, defaultSnapshotMaxBytes); err != nil || !committed {
		t.Fatalf("inject later committed page: committed=%v err=%v", committed, err)
	}
	returned := append([]SelectedCandidate(nil), result.Selected[:len(result.Selected)-1]...)
	if _, err := service.FinalizeMappedServing(context.Background(), result, returned); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("earlier page changed after later commit: err=%v want expired cursor", err)
	}
	current, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	currentZero, _ := snapshotPageAt(current, 0)
	currentOne, _ := snapshotPageAt(current, 1)
	if snapshotPageCommitted(currentZero) || !snapshotPageCommitted(currentOne) {
		t.Fatalf("conflicting committed state was rewritten: page0=%#v page1=%#v", currentZero, currentOne)
	}
}

func TestRecommendationCommittedPageRecoversHistorySideEffect(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	history.recordErr = errors.New("history temporarily unavailable")
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected); err != nil {
		t.Fatalf("best-effort history failure changed successful response: %v", err)
	}
	snapshot, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := snapshotPageAt(snapshot, 0)
	if !snapshotPageCommitted(page) || page.HistoryRecorded || page.TraceAttempted || traces.enqueueCalls != 0 {
		t.Fatalf("failed history left invalid state page=%#v trace calls=%d", page, traces.enqueueCalls)
	}
	history.recordErr = nil
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected); err != nil {
		t.Fatalf("recover committed page history: %v", err)
	}
	if _, err := service.FinalizeMappedServing(context.Background(), result, result.Selected); err != nil {
		t.Fatalf("repeat recovered page: %v", err)
	}
	snapshot, err = store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = snapshotPageAt(snapshot, 0)
	if !page.HistoryRecorded || !page.TraceAttempted || !page.Finalized || traces.enqueueCalls != 1 {
		t.Fatalf("recovered page=%#v trace calls=%d", page, traces.enqueueCalls)
	}
	if history.pageRecordAttempts != 2 {
		t.Fatalf("history attempts=%d want failed attempt plus recovery once", history.pageRecordAttempts)
	}
}

func TestRecommendationConcurrentFinalizeUsesSingleCommittedPage(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	history := service.dependencies.History.(*serviceTestHistoryStore)
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.FinalizeMappedServing(context.Background(), result, result.Selected)
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent finalize: %v", err)
		}
	}
	if history.pageRecordAttempts < 1 || history.pageRecordAttempts > 2 || traces.enqueueCalls != 1 {
		t.Fatalf("history attempts=%d trace enqueues=%d", history.pageRecordAttempts, traces.enqueueCalls)
	}
	snapshot, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := snapshotPageAt(snapshot, 0)
	if !page.Finalized || !equalUintSlices(page.ReturnedPostIDs, selectedPostIDs(result.Selected)) {
		t.Fatalf("concurrent page=%#v", page)
	}
}

func TestRecommendationPreselectedPagePreservesCrossPageSemanticDiversity(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	vectorA := []float32{1, 0}
	vectorB := []float32{0.995, 0.01}
	if similarity := CosineSimilarity(vectorA, vectorB); similarity <= 0.92 {
		t.Fatalf("fixture similarity=%f, want > 0.92", similarity)
	}
	page := CachedPage{Index: 0, Items: []CachedCandidate{{
		Candidate: Candidate{PostID: 100}, PostID: 100, AuthorID: 10, CreatedAt: now,
		Breakdown: ScoreBreakdown{BaseScore: 10},
	}}}
	embeddings := map[uint][]float32{100: vectorA}
	preselectedPrior := selectedContextFromPages([]CachedPage{page}, embeddings)
	dynamicPrior := selectedContextFromPages([]CachedPage{page}, embeddings)
	if len(preselectedPrior) != 1 || len(preselectedPrior[0].Embedding) != len(vectorA) {
		t.Fatalf("preselected prior embedding=%v", preselectedPrior)
	}
	if len(dynamicPrior) != 1 || !equalFloat32Slices(preselectedPrior[0].Embedding, dynamicPrior[0].Embedding) {
		t.Fatalf("preselected and dynamic prior contexts differ: %v vs %v", preselectedPrior, dynamicPrior)
	}
	cfg := testSelectionConfig(DefaultConfig())
	cfg.OutOfNetworkMinRatio = 0
	cfg.Exploration.Ratio = 0
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 8
	cfg.Diversity.SemanticDuplicateThreshold = 0.92
	cfg.Diversity.SemanticDuplicatePenalty = 200
	candidates := []RankedCandidate{
		{Candidate: Candidate{PostID: 101}, Post: models.Post{Model: gorm.Model{ID: 101}, AuthorID: 20}, Embedding: vectorB, Breakdown: ScoreBreakdown{BaseScore: 100}},
		{Candidate: Candidate{PostID: 102}, Post: models.Post{Model: gorm.Model{ID: 102}, AuthorID: 30}, Embedding: []float32{0, 1}, Breakdown: ScoreBreakdown{BaseScore: 1}},
	}
	if penalty := diversityPenalty(candidates[0], preselectedPrior, cfg.Diversity); penalty != cfg.Diversity.SemanticDuplicatePenalty {
		t.Fatalf("cross-page semantic penalty=%v, want %v", penalty, cfg.Diversity.SemanticDuplicatePenalty)
	}
	requestID := "fixed-page-seed"
	preselected := SelectNextPage(SelectionInput{Candidates: candidates, Limit: 1, Now: now, Phase: SelectionPhaseFresh, RequestID: requestID, Config: cfg}, preselectedPrior)
	dynamic := SelectNextPage(SelectionInput{Candidates: candidates, Limit: 1, Now: now, Phase: SelectionPhaseFresh, RequestID: requestID, Config: cfg}, dynamicPrior)
	if len(preselected) != 1 || preselected[0].Post.ID != 102 {
		t.Fatalf("preselected second page=%v, want distinct post 102", selectedPostIDs(preselected))
	}
	if !equalUintSlices(selectedPostIDs(preselected), selectedPostIDs(dynamic)) {
		t.Fatalf("preselected/dynamic results differ with same page seed: %v vs %v", selectedPostIDs(preselected), selectedPostIDs(dynamic))
	}
	withoutEmbedding := selectedContextFromPages([]CachedPage{page}, nil)
	withoutPenalty := SelectNextPage(SelectionInput{Candidates: candidates, Limit: 1, Now: now, Phase: SelectionPhaseFresh, RequestID: requestID, Config: cfg}, withoutEmbedding)
	if len(withoutPenalty) != 1 || withoutPenalty[0].Post.ID != 101 {
		t.Fatalf("missing-embedding control=%v, want high-score duplicate 101", selectedPostIDs(withoutPenalty))
	}
}

func TestRecommendationPreselectionUsesCommittedAuthorAndSemanticContext(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	filteredVector := []float32{1, 0}
	returnedVector := []float32{0, 1}
	page := CachedPage{
		Index: 0, Committed: true,
		Items: []CachedCandidate{
			{Candidate: Candidate{PostID: 100}, PostID: 100, AuthorID: 10, CreatedAt: now},
			{Candidate: Candidate{PostID: 101}, PostID: 101, AuthorID: 20, CreatedAt: now},
		},
		ReturnedPostIDs: []uint{101},
	}
	prior := selectedContextFromPages([]CachedPage{page}, map[uint][]float32{100: filteredVector, 101: returnedVector})
	if len(prior) != 1 || prior[0].Post.ID != 101 || !equalFloat32Slices(prior[0].Embedding, returnedVector) {
		t.Fatalf("committed context retained mapper-rejected content: %#v", prior)
	}
	cfg := testSelectionConfig(DefaultConfig())
	cfg.Diversity.Enabled = true
	cfg.Diversity.AuthorWindowSize = 8
	cfg.Diversity.MaxSameAuthorInWindow = 1
	cfg.Diversity.SemanticDuplicateThreshold = 0.92
	cfg.Diversity.SemanticDuplicatePenalty = 100
	candidate := RankedCandidate{
		Candidate: Candidate{PostID: 102}, Post: models.Post{Model: gorm.Model{ID: 102}, AuthorID: 10},
		Embedding: filteredVector, Breakdown: ScoreBreakdown{BaseScore: 10},
	}
	if !authorWindowAllows(candidate, prior, cfg.Diversity) {
		t.Fatal("filtered page-0 author still constrained the next-page author window")
	}
	if penalty := diversityPenalty(candidate, prior, cfg.Diversity); penalty != 0 {
		t.Fatalf("filtered page-0 embedding still affected semantic diversity: penalty=%v", penalty)
	}
}

func TestRecommendationCommittedPageMaterializationUsesReturnedOrder(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	result := firstSnapshotResult(t, service, now)
	returned := append([]SelectedCandidate(nil), result.Selected...)
	returned[1], returned[2] = returned[2], returned[1]
	wantIDs := selectedPostIDs(returned)
	if _, err := service.FinalizeMappedServing(context.Background(), result, returned); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := snapshotPageAt(snapshot, 0)
	if !ok {
		t.Fatal("committed page 0 missing")
	}
	rematerialized, _, err := service.materializeSnapshotPage(
		context.Background(), result.finalization.request, snapshot, page,
		result.Profile, result.LanguageContext, result.EmbeddingVersion,
	)
	if err != nil {
		t.Fatalf("rematerialize committed page: %v", err)
	}
	if !samePostIDs(selectedPostIDs(rematerialized.Selected), wantIDs) {
		t.Fatalf("rematerialized IDs=%v want committed mapped order=%v", selectedPostIDs(rematerialized.Selected), wantIDs)
	}
}

func containsUint(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestRecommendationTrackingPositionsFollowMappedResponse(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	key := []byte(strings.Repeat("tracking-test-key", 3))
	service.config.Tracking = TrackingConfig{Enabled: true, RolloutPercent: 100, SigningKey: key, TokenTTL: 24 * time.Hour}
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	if len(result.Selected) != 20 {
		t.Fatalf("candidate page size=%d want 20", len(result.Selected))
	}
	filtered := append([]SelectedCandidate(nil), result.Selected[:2]...)
	filtered = append(filtered, result.Selected[3:]...)
	facts, err := service.FinalizeMappedServing(context.Background(), result, filtered)
	if err != nil {
		t.Fatalf("finalize mapped page: %v", err)
	}
	if len(facts) != 19 || len(traces.job.Results) != 19 {
		t.Fatalf("tracking facts=%d trace results=%d, want 19", len(facts), len(traces.job.Results))
	}
	for index, item := range filtered {
		fact := facts[index]
		if fact.PostID != item.Post.ID || fact.Position != index+1 {
			t.Fatalf("tracking[%d]=%#v does not match post=%d position=%d", index, fact, item.Post.ID, index+1)
		}
		claims, err := VerifyTrackingToken(fact.Token, key)
		if err != nil {
			t.Fatalf("verify token for post %d: %v", fact.PostID, err)
		}
		if claims.PostID != item.Post.ID || claims.Position != index+1 || claims.RequestID != result.RequestID {
			t.Fatalf("tracking claims=%#v want post=%d position=%d request=%s", claims, item.Post.ID, index+1, result.RequestID)
		}
		trace := traces.job.Results[index]
		if trace.PostID != item.Post.ID || trace.Position != fact.Position {
			t.Fatalf("trace[%d]=post %d position %d, tracking post %d position %d", index, trace.PostID, trace.Position, fact.PostID, fact.Position)
		}
		if fact.PostID == result.Selected[2].Post.ID {
			t.Fatalf("filtered post %d received tracking", fact.PostID)
		}
	}
}

func TestRecommendationRetryKeepsStableTrackingClaims(t *testing.T) {
	service, _, now := newSnapshotTestService(t, newMemorySnapshotStore())
	key := []byte(strings.Repeat("tracking-retry-key", 3))
	service.config.Tracking = TrackingConfig{Enabled: true, RolloutPercent: 100, SigningKey: key, TokenTTL: 24 * time.Hour}
	traces := &serviceTestTraceEnqueuer{}
	service.dependencies.TraceEnqueuer = traces
	result := firstSnapshotResult(t, service, now)
	first, err := service.FinalizeMappedServing(context.Background(), result, result.Selected)
	if err != nil {
		t.Fatal(err)
	}
	retry := result
	retry.Profile.ProfileAgeMS += int64(time.Minute / time.Millisecond)
	second, err := service.FinalizeMappedServing(context.Background(), retry, retry.Selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) || traces.enqueueCalls != 1 {
		t.Fatalf("first tracking=%d retry tracking=%d trace calls=%d", len(first), len(second), traces.enqueueCalls)
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("tracking claim changed on retry at %d: %#v vs %#v", index, first[index], second[index])
		}
	}
}

func TestRecommendationSnapshotNextPageRetainsCandidateWithoutEmbedding(t *testing.T) {
	store := newMemorySnapshotStore()
	service, repository, now := newSnapshotTestService(t, store)
	firstCandidate := testCandidate(1, CandidateSourceRecent)
	secondCandidate := testCandidate(2, CandidateSourceRecent)
	repository.semantic = nil
	repository.following = nil
	repository.recent = []Candidate{firstCandidate, secondCandidate}
	repository.trending = nil
	repository.embeddings = nil
	service.config.Recommendation.Candidates.Personalized = config.RecommendationCandidateCaps{Recent: 2, Merged: 2}
	service.config.Recommendation.Candidates.ColdStart = config.RecommendationCandidateCaps{Recent: 2, Merged: 2}
	service.config.Recommendation.Snapshot.MaxCandidates = 2
	service.config.Recommendation.Snapshot.PreselectPages = 1
	service.config.Recommendation.Snapshot.DefaultPageSize = 1
	service.config.Recommendation.Snapshot.MaxPageSize = 1

	first, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1,
		RequestID: "snapshot-no-vector-page-0", Now: now,
	})
	if err != nil {
		t.Fatalf("serve first snapshot page: %v", err)
	}
	if len(first.Selected) != 1 || first.Selected[0].Embedding != nil {
		t.Fatalf("first page=%+v, want one candidate without a vector", first.Selected)
	}
	var remainingPostID uint
	switch first.Selected[0].Post.ID {
	case firstCandidate.PostID:
		remainingPostID = secondCandidate.PostID
	case secondCandidate.PostID:
		remainingPostID = firstCandidate.PostID
	default:
		t.Fatalf("first page returned unexpected post %d", first.Selected[0].Post.ID)
	}
	if err := service.FinalizeServing(context.Background(), first, first.Selected); err != nil {
		t.Fatalf("finalize first snapshot page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first snapshot page did not return a continuation cursor")
	}

	second, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, Cursor: first.NextCursor,
		RequestID: "snapshot-no-vector-page-1", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("serve second snapshot page: %v", err)
	}
	if !second.CacheHit || len(second.Selected) != 1 {
		t.Fatalf("second page cache hit=%v selected=%v, want one snapshot result", second.CacheHit, selectedPostIDs(second.Selected))
	}
	if second.Selected[0].Post.ID != remainingPostID || second.Selected[0].Embedding != nil {
		t.Fatalf("second page candidate=%+v, want remaining no-vector candidate %d with nil embedding", second.Selected[0], remainingPostID)
	}
	if len(repository.snapshotQueries) != 2 {
		t.Fatalf("snapshot page revalidation queries=%d, want one commit check and one dynamic-page query", len(repository.snapshotQueries))
	}
	if !repository.snapshotQueries[1].Now.Equal(now.Add(time.Second)) {
		t.Fatalf("dynamic snapshot page query time=%s, want %s", repository.snapshotQueries[1].Now, now.Add(time.Second))
	}
}

type snapshotScoringCandidateRepository struct {
	*serviceTestCandidateRepository
}

func (repository snapshotScoringCandidateRepository) HydrateCandidates(ctx context.Context, query CandidateQuery, candidates []Candidate) ([]RankedCandidate, error) {
	items, err := repository.serviceTestCandidateRepository.HydrateCandidates(ctx, query, candidates)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Embedding = append([]float32(nil), repository.embeddings[items[index].Candidate.PostID]...)
	}
	return items, nil
}

func configureSnapshotScoringFixture(
	service *RecommendationService,
	repository *serviceTestCandidateRepository,
	candidates []Candidate,
	embeddings map[uint][]float32,
	positiveVector, negativeVector []float32,
	negativeConfidence float64,
	pageSize, preselectPages int,
) {
	service.dependencies.Candidates = snapshotScoringCandidateRepository{serviceTestCandidateRepository: repository}
	repository.semantic = nil
	repository.following = nil
	repository.recent = append([]Candidate(nil), candidates...)
	repository.trending = nil
	repository.embeddings = embeddings

	cfg := service.config.Recommendation
	cfg.SemanticWeight = 2
	cfg.NegativeSemanticWeight = 1.5
	cfg.Candidates.Personalized = config.RecommendationCandidateCaps{Recent: len(candidates), Merged: len(candidates)}
	cfg.Candidates.ColdStart = config.RecommendationCandidateCaps{Recent: len(candidates), Merged: len(candidates)}
	cfg.Snapshot.MaxCandidates = len(candidates)
	cfg.Snapshot.PreselectPages = preselectPages
	cfg.Snapshot.DefaultPageSize = pageSize
	cfg.Snapshot.MaxPageSize = pageSize
	service.config.Recommendation = cfg

	profiles := service.dependencies.Profiles.(*serviceTestProfileRepository)
	profile := profiles.loaded.Profile
	profile.PositiveVector = append([]float32(nil), positiveVector...)
	profile.NegativeVector = append([]float32(nil), negativeVector...)
	profile.NegativeConfidence = negativeConfidence
	profiles.loaded.Profile = profile
}

func TestRecommendationSnapshotDynamicNextPageRefreshesSemanticScoresAndKeepsMissingVectorCandidate(t *testing.T) {
	store := newMemorySnapshotStore()
	service, repository, now := newSnapshotTestService(t, store)
	candidates := []Candidate{
		testCandidate(1, CandidateSourceRecent),
		testCandidate(2, CandidateSourceRecent),
		testCandidate(3, CandidateSourceRecent),
	}
	configureSnapshotScoringFixture(service, repository, candidates, map[uint][]float32{
		1: {1, 0}, 2: {.8, .6}, 3: {0, 1},
	}, []float32{1, 0}, nil, 0, 1, 1)

	first, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1,
		RequestID: "snapshot-semantic-refresh-page-0", Now: now,
	})
	if err != nil {
		t.Fatalf("serve initial snapshot page: %v", err)
	}
	if got := selectedPostIDs(first.Selected); !equalUintSlices(got, []uint{1}) {
		t.Fatalf("initial page IDs=%v, want highest-scoring post 1", got)
	}
	snapshot, err := store.Load(context.Background(), first.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	var cachedSecond CachedCandidate
	for _, cached := range snapshot.Candidates {
		if cached.PostID == 2 {
			cachedSecond = cached
			break
		}
	}
	if cachedSecond.PostID != 2 || cachedSecond.Breakdown.PositiveSemantic <= 0 {
		t.Fatalf("cached post 2=%+v, want a positive semantic score before its vector is removed", cachedSecond)
	}

	delete(repository.embeddings, 2)
	if err := service.FinalizeServing(context.Background(), first, first.Selected); err != nil {
		t.Fatalf("finalize initial snapshot page: %v", err)
	}
	second, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, Cursor: first.NextCursor,
		RequestID: "snapshot-semantic-refresh-page-1", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("serve dynamic snapshot page after vector removal: %v", err)
	}
	if got := selectedPostIDs(second.Selected); !equalUintSlices(got, []uint{3}) {
		t.Fatalf("dynamic page IDs=%v, want post 3 after post 2's cached semantic score is cleared", got)
	}
	if err := service.FinalizeServing(context.Background(), second, second.Selected); err != nil {
		t.Fatalf("finalize dynamic snapshot page: %v", err)
	}
	if second.NextCursor == "" {
		t.Fatal("dynamic snapshot page has no cursor for the remaining no-vector candidate")
	}
	third, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, Cursor: second.NextCursor,
		RequestID: "snapshot-semantic-refresh-page-2", Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("serve remaining snapshot candidate without vector: %v", err)
	}
	if got := selectedPostIDs(third.Selected); !equalUintSlices(got, []uint{2}) || third.Selected[0].Embedding != nil {
		t.Fatalf("remaining page=%+v, want retained post 2 with nil embedding", third.Selected)
	}
	selected := third.Selected[0]
	if selected.Candidate.PositiveSemanticSimilarity != 0 || selected.Breakdown.PositiveSemantic != 0 ||
		selected.Breakdown.NegativeSemantic != 0 || selected.Breakdown.SemanticComponent != 0 || selected.ExplorationSemantic != 0 {
		t.Fatalf("remaining no-vector page retained stale semantic data: %+v", selected)
	}
	if selected.Breakdown.BaseScore != selected.Breakdown.TrendingComponent+selected.Breakdown.AuthorAffinityComponent+
		selected.Breakdown.LanguageComponent+selected.Breakdown.OnboardingInterestComponent {
		t.Fatalf("remaining no-vector BaseScore=%v, want only current non-semantic components", selected.Breakdown.BaseScore)
	}
}

func TestRecommendationSnapshotPreselectedPageReplayRefreshesSemanticScores(t *testing.T) {
	store := newMemorySnapshotStore()
	service, repository, now := newSnapshotTestService(t, store)
	candidates := []Candidate{
		testCandidate(1, CandidateSourceRecent),
		testCandidate(2, CandidateSourceRecent),
		testCandidate(3, CandidateSourceRecent),
		testCandidate(4, CandidateSourceRecent),
	}
	configureSnapshotScoringFixture(service, repository, candidates, map[uint][]float32{
		1: {1, 0}, 2: {.9, .1}, 3: {.8, .2}, 4: {.7, .3},
	}, []float32{1, 0}, []float32{0, 1}, .4, 2, 2)

	first, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2,
		RequestID: "snapshot-preselected-replay-page-0", Now: now,
	})
	if err != nil {
		t.Fatalf("serve initial snapshot page: %v", err)
	}
	if got := selectedPostIDs(first.Selected); !equalUintSlices(got, []uint{1, 2}) {
		t.Fatalf("initial page IDs=%v, want [1 2]", got)
	}
	snapshot, err := store.Load(context.Background(), first.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	page, exists := snapshotPageAt(snapshot, 1)
	if !exists || len(page.Items) != 2 || !equalUintSlices(cachedPagePostIDs(page), []uint{3, 4}) {
		t.Fatalf("preselected page=%+v, want cached order [3 4]", page)
	}
	if page.Items[0].Breakdown.PositiveSemantic <= 0 || page.Items[1].Breakdown.PositiveSemantic <= 0 {
		t.Fatalf("preselected page lacks cached semantic scores: %+v", page.Items)
	}

	// Remove one vector and replace the other with a new valid vector under the
	// same serving version. Materializing the page must keep its stored order.
	delete(repository.embeddings, 3)
	repository.embeddings[4] = []float32{1, 0}
	serveReplay := func(label string, requestTime time.Time) ServeResult {
		t.Helper()
		result, err := service.Serve(context.Background(), ServeRequest{
			Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2, Cursor: first.NextCursor,
			RequestID: "snapshot-preselected-retry-" + label, Now: requestTime,
		})
		if err != nil {
			t.Fatalf("replay preselected snapshot page (%s): %v", label, err)
		}
		return result
	}
	firstReplay := serveReplay("one", now.Add(time.Second))
	secondReplay := serveReplay("two", now.Add(2*time.Second))
	for label, result := range map[string]ServeResult{"first": firstReplay, "retry": secondReplay} {
		if !result.CacheHit || !equalUintSlices(selectedPostIDs(result.Selected), []uint{3, 4}) {
			t.Fatalf("%s replay cache=%v selected=%v, want cached order [3 4]", label, result.CacheHit, selectedPostIDs(result.Selected))
		}
		if result.RequestID != page.RequestID || !result.Now.Equal(page.ServedAt) {
			t.Fatalf("%s replay metadata request=%q now=%s, want page request=%q served_at=%s", label, result.RequestID, result.Now, page.RequestID, page.ServedAt)
		}
	}
	if firstReplay.RequestID != secondReplay.RequestID || !equalUintSlices(selectedPostIDs(firstReplay.Selected), selectedPostIDs(secondReplay.Selected)) {
		t.Fatalf("same-cursor retry changed page identity: first=%+v retry=%+v", firstReplay, secondReplay)
	}
	missingVector := firstReplay.Selected[0]
	if missingVector.Post.ID != 3 || missingVector.Embedding != nil || missingVector.Candidate.PositiveSemanticSimilarity != 0 ||
		missingVector.Breakdown.PositiveSemantic != 0 || missingVector.Breakdown.NegativeSemantic != 0 ||
		missingVector.Breakdown.SemanticComponent != 0 || missingVector.ExplorationSemantic != 0 {
		t.Fatalf("preselected missing-vector candidate retained stale semantic data: %+v", missingVector)
	}
	changedVector := firstReplay.Selected[1]
	if changedVector.Post.ID != 4 || !equalFloat32Slices(changedVector.Embedding, []float32{1, 0}) ||
		changedVector.Candidate.PositiveSemanticSimilarity != 1 || changedVector.Breakdown.PositiveSemantic != 1 ||
		changedVector.Breakdown.SemanticComponent <= page.Items[1].Breakdown.SemanticComponent {
		t.Fatalf("same-version updated vector did not refresh cached semantic score: %+v; cached=%+v", changedVector, page.Items[1])
	}
}

func TestRefreshSnapshotSemanticScoresUsesCurrentEmbeddingAndPreservesNonSemanticBreakdown(t *testing.T) {
	profile := ProfileFeatures{
		PositiveVector: []float32{1, 0}, NegativeVector: []float32{0, 1}, NegativeConfidence: .5,
	}
	cfg := RankingConfig{SemanticWeight: 2, NegativeSemanticWeight: 1.5}
	base := RankedCandidate{
		Candidate: Candidate{PostID: 1, PositiveSemanticSimilarity: .99},
		Breakdown: ScoreBreakdown{
			PositiveSemantic: .99, NegativeSemantic: .88, NegativeConfidence: .2,
			InteractionAffinity: .6, FollowingBonusApplied: .1, SemanticComponent: 9,
			TrendingComponent: .2, AuthorAffinityComponent: .3, LanguageAffinity: .7,
			LanguageComponent: .4, OnboardingInterestComponent: .5, DiversityPenalty: .15,
			BaseScore: 99, FinalScore: 98,
		},
		ExplorationSemantic: .99,
	}
	for _, test := range []struct {
		name                string
		embedding           []float32
		positive, negative  float64
		component, explore  float64
		candidateSimilarity float64
	}{
		{name: "missing vector"},
		{name: "dimension mismatch", embedding: []float32{1, 0, 0}},
		{name: "current changed vector", embedding: []float32{.8, .6}, positive: .8, negative: .6, component: .7, explore: .35, candidateSimilarity: .8},
		{name: "negative vector", embedding: []float32{0, 1}, negative: 1, component: -1.5, candidateSimilarity: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Embedding = append([]float32(nil), test.embedding...)
			got := refreshSnapshotSemanticScores(candidate, profile, cfg)
			if math.Abs(got.Breakdown.PositiveSemantic-test.positive) > 1e-6 ||
				math.Abs(got.Breakdown.NegativeSemantic-test.negative) > 1e-6 ||
				math.Abs(got.Breakdown.NegativeConfidence-profile.NegativeConfidence) > 1e-6 ||
				math.Abs(got.Breakdown.SemanticComponent-test.component) > 1e-6 ||
				math.Abs(got.ExplorationSemantic-test.explore) > 1e-6 ||
				math.Abs(got.Candidate.PositiveSemanticSimilarity-test.candidateSimilarity) > 1e-6 {
				t.Fatalf("refreshed semantic scores=%+v candidate=%+v, want positive=%v negative=%v component=%v exploration=%v similarity=%v",
					got.Breakdown, got.Candidate, test.positive, test.negative, test.component, test.explore, test.candidateSimilarity)
			}
			wantBase := test.component + .2 + .3 + .4 + .5
			if math.Abs(got.Breakdown.BaseScore-wantBase) > 1e-6 || math.Abs(got.Breakdown.FinalScore-(wantBase-.15)) > 1e-6 {
				t.Fatalf("refreshed total scores base=%v final=%v, want %v and %v", got.Breakdown.BaseScore, got.Breakdown.FinalScore, wantBase, wantBase-.15)
			}
			if got.Breakdown.TrendingComponent != base.Breakdown.TrendingComponent ||
				got.Breakdown.AuthorAffinityComponent != base.Breakdown.AuthorAffinityComponent ||
				got.Breakdown.LanguageComponent != base.Breakdown.LanguageComponent ||
				got.Breakdown.OnboardingInterestComponent != base.Breakdown.OnboardingInterestComponent ||
				got.Breakdown.DiversityPenalty != base.Breakdown.DiversityPenalty ||
				got.Breakdown.InteractionAffinity != base.Breakdown.InteractionAffinity ||
				got.Breakdown.FollowingBonusApplied != base.Breakdown.FollowingBonusApplied ||
				got.Breakdown.LanguageAffinity != base.Breakdown.LanguageAffinity {
				t.Fatalf("refresh changed non-semantic breakdown fields: got=%+v want=%+v", got.Breakdown, base.Breakdown)
			}
		})
	}
}

func TestRecommendationSnapshotRefillsHistoricalCandidatesAfterCachedPoolIsConsumed(t *testing.T) {
	store := newMemorySnapshotStore()
	service, repository, now := newSnapshotTestService(t, store)
	historicalIDs := make([]uint, 20)
	posts := make(map[uint]models.Post, len(historicalIDs))
	for index := range historicalIDs {
		id := uint(1001 + index)
		historicalIDs[index] = id
		posts[id] = models.Post{Model: gorm.Model{ID: id, CreatedAt: now.AddDate(0, 0, -400).Add(-time.Duration(index) * time.Second)}, AuthorID: id + 10000}
	}
	for id, post := range posts {
		repository.posts[id] = post
	}
	historicalRepository := &scriptedHistoricalRepository{
		serviceTestCandidateRepository: repository,
		pages:                          []HistoricalCandidatePage{historicalTestPage(now, historicalIDs, false)},
	}
	service.dependencies.Candidates = historicalRepository

	pool := make([]CachedCandidate, 0, 500)
	used := make([]uint, 0, 500)
	for id := uint(1); id <= 500; id++ {
		post := repository.posts[id]
		candidate := testCandidate(id, CandidateSourceRecent)
		candidate.RecallTier = RecallTierRecentUnseen
		pool = append(pool, CachedCandidate{
			Candidate: candidate, PostID: id, AuthorID: post.AuthorID, CreatedAt: post.CreatedAt,
		})
		used = append(used, id)
	}
	snapshot := CandidatePoolSnapshot{
		ID: "consumed-pool", PageSize: 20, Candidates: pool, UsedPostIDs: used,
		RecentCutoff: now.AddDate(0, 0, -7), SoftExhausted: true,
	}
	page, err := service.selectSnapshotPage(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, RequestID: "historical-refill", Now: now,
	}, "embedding-v1", Profile{PositiveVector: []float32{1}, ProfileStatus: ProfileStatusHit}, &snapshot, 0, "historical-refill", ServedHistory{})
	if err != nil {
		t.Fatalf("select page after cached pool exhaustion: %v", err)
	}
	if got := cachedPagePostIDs(page); !equalUintSlices(got, historicalIDs) {
		t.Fatalf("refilled page IDs=%v, want historical IDs %v", got, historicalIDs)
	}
	if snapshot.HistoricalExhausted || snapshot.Exhausted || snapshot.HistoricalCursor == nil || snapshot.HistoricalCursor.PostID != historicalIDs[len(historicalIDs)-1] {
		t.Fatalf("snapshot after historical refill=%+v, want a live history cursor and non-exhausted pool", snapshot)
	}
	if len(snapshot.Candidates) != len(historicalIDs) {
		t.Fatalf("refilled snapshot candidates=%d, want %d", len(snapshot.Candidates), len(historicalIDs))
	}
	if len(historicalRepository.queries) != 1 || historicalRepository.queries[0].Range != CandidateRangeHistorical ||
		historicalRepository.queries[0].HistoricalCursor != nil || len(historicalRepository.queries[0].ExcludedPostIDs) != 500 {
		t.Fatalf("historical refill query=%+v, want initial historical keyset query excluding all 500 consumed IDs", historicalRepository.queries)
	}
}

func TestRecommendationSnapshotReuses500CandidatePoolAcrossFivePages(t *testing.T) {
	store := newMemorySnapshotStore()
	service, candidates, now := newSnapshotTestService(t, store)
	exporter, provider := newRecommendationTracingTestProvider(t)
	previousTracing := TracingEnabled()
	SetTracingEnabled(true)
	t.Cleanup(func() {
		SetTracingEnabled(previousTracing)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	var allIDs []uint
	var snapshotPageDurations []time.Duration
	cursor := ""
	var snapshotID string
	for pageIndex := 0; pageIndex < 5; pageIndex++ {
		pageCtx, root := provider.Tracer("snapshot-test").Start(context.Background(), "page")
		pageStarted := time.Now()
		result, err := service.Serve(pageCtx, ServeRequest{
			Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: cursor,
			RequestID: uuid.NewString(), Now: now.Add(time.Duration(pageIndex) * time.Second),
		})
		root.End()
		if err != nil {
			t.Fatalf("page %d: %v", pageIndex, err)
		}
		snapshotPageDurations = append(snapshotPageDurations, time.Since(pageStarted))
		if pageIndex == 0 && len(result.CandidateSummary.Candidates) != 500 {
			t.Fatalf("snapshot candidate count=%d want 500", len(result.CandidateSummary.Candidates))
		}
		if pageIndex == 0 {
			snapshotID = result.PoolID
		}
		if pageIndex > 0 && !result.CacheHit {
			t.Fatalf("page %d was not served from its candidate snapshot", pageIndex)
		}
		if len(result.Selected) != 20 {
			t.Fatalf("page %d returned %d posts, want 20", pageIndex, len(result.Selected))
		}
		allIDs = append(allIDs, selectedPostIDs(result.Selected)...)
		cursor = result.NextCursor
		if cursor == "" && pageIndex < 4 {
			t.Fatalf("page %d did not return a continuation cursor", pageIndex)
		}
	}
	seen := make(map[uint]struct{}, len(allIDs))
	for _, id := range allIDs {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("post %d repeated across pages", id)
		}
		seen[id] = struct{}{}
	}
	if len(allIDs) != 100 {
		t.Fatalf("unique posts=%d want 100", len(allIDs))
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if countString(*candidates.calls, source) != 1 {
			t.Fatalf("%s recall calls=%d want 1; calls=%v", source, countString(*candidates.calls, source), *candidates.calls)
		}
	}
	if got := countRecommendationSpans(exporter.GetSpans(), "recommendation.recall.fusion"); got != 1 {
		t.Fatalf("RRF fusion spans=%d want 1", got)
	}
	if got := countRecommendationSpans(exporter.GetSpans(), "recommendation.rank"); got != 1 {
		t.Fatalf("full ranking spans=%d want 1", got)
	}
	if candidates.snapshotEligibilityCalls != 7 || candidates.snapshotCandidateIDsRead == 0 || candidates.embeddingReadCalls == 0 {
		t.Fatalf("snapshot page revalidation calls=%d candidate IDs=%d embedding batches=%d", candidates.snapshotEligibilityCalls, candidates.snapshotCandidateIDsRead, candidates.embeddingReadCalls)
	}

	legacyService, legacyCandidates, _ := newSnapshotTestService(t, newMemorySnapshotStore())
	legacyService.config.Recommendation.Snapshot.Enabled = false
	legacyService.dependencies.Snapshots = nil
	var legacyPageDurations []time.Duration
	for pageIndex := 0; pageIndex < 5; pageIndex++ {
		pageCtx, root := provider.Tracer("snapshot-test").Start(context.Background(), "legacy-page")
		pageStarted := time.Now()
		if _, err := legacyService.Serve(pageCtx, ServeRequest{
			Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20,
			RequestID: uuid.NewString(), Now: now.Add(time.Duration(pageIndex) * time.Second),
		}); err != nil {
			root.End()
			t.Fatalf("legacy page %d: %v", pageIndex, err)
		}
		root.End()
		legacyPageDurations = append(legacyPageDurations, time.Since(pageStarted))
	}
	if got := countRecommendationSpans(exporter.GetSpans(), "recommendation.rank"); got != 6 {
		t.Fatalf("snapshot plus legacy full ranking spans=%d want 6", got)
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if countString(*legacyCandidates.calls, source) != 5 {
			t.Fatalf("legacy %s recall calls=%d want 5", source, countString(*legacyCandidates.calls, source))
		}
	}
	snapshot, err := store.Load(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("load measured snapshot: %v", err)
	}
	snapshotBytes, err := marshalSnapshot(snapshot, defaultSnapshotMaxBytes)
	if err != nil {
		t.Fatalf("marshal measured snapshot: %v", err)
	}
	encodedSnapshotBytes := len(snapshotBytes)
	t.Logf("simulated 5-page latency: candidate-pool p50=%s p95=%s total=%s; full-recompute p50=%s p95=%s total=%s; dynamic-page eligible candidate IDs=%v total-read=%d; embedding rows=%d in %d batches; modeled DB queries=%d; Redis snapshot bytes=%d",
		medianDuration(snapshotPageDurations), percentileDuration(snapshotPageDurations, .95), sumDurations(snapshotPageDurations),
		medianDuration(legacyPageDurations), percentileDuration(legacyPageDurations, .95), sumDurations(legacyPageDurations),
		candidates.snapshotPageCandidateIDs, candidates.snapshotCandidateIDsRead, candidates.snapshotEmbeddingIDsRead,
		candidates.embeddingReadCalls, candidates.snapshotDatabaseQueries, encodedSnapshotBytes)
}

func BenchmarkRecommendationSnapshotDynamicNextPages(b *testing.B) {
	var candidateIDs, embeddingIDs, databaseQueries int
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		store := newMemorySnapshotStore()
		service, candidates, now := newSnapshotTestService(b, store)
		cursor := ""
		for pageIndex := 0; pageIndex < 5; pageIndex++ {
			result, err := service.Serve(context.Background(), ServeRequest{
				Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: cursor,
				RequestID: uuid.NewString(), Now: now.Add(time.Duration(pageIndex) * time.Second),
			})
			if err != nil {
				b.Fatalf("page %d: %v", pageIndex, err)
			}
			cursor = result.NextCursor
		}
		candidateIDs += candidates.snapshotCandidateIDsRead
		embeddingIDs += candidates.snapshotEmbeddingIDsRead
		databaseQueries += candidates.snapshotDatabaseQueries
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(candidateIDs)/float64(b.N), "snapshot-candidate-ids/op")
		b.ReportMetric(float64(embeddingIDs)/float64(b.N), "embedding-rows/op")
		b.ReportMetric(float64(databaseQueries)/float64(b.N), "snapshot-db-queries/op")
	}
}

func TestRecommendationSnapshotHonorsConfiguredMaximumPageSize(t *testing.T) {
	service, _, now := newSnapshotTestService(t, newMemorySnapshotStore())
	service.config.Recommendation.Snapshot.MaxPageSize = 5

	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Now: now,
	})
	if err != nil {
		t.Fatalf("serve recommendations: %v", err)
	}
	if result.Limit != 5 || len(result.Selected) != 5 {
		t.Fatalf("page limit=%d selected=%d, want 5", result.Limit, len(result.Selected))
	}
	snapshot, err := service.dependencies.Snapshots.Load(context.Background(), result.PoolID)
	if err != nil {
		t.Fatalf("load candidate snapshot: %v", err)
	}
	if snapshot.PageSize != 5 {
		t.Fatalf("snapshot page size=%d, want 5", snapshot.PageSize)
	}
}

func TestRecommendationSnapshotSameCursorConcurrentRequestsUseOneStablePage(t *testing.T) {
	store := newMemorySnapshotStore()
	service, _, now := newSnapshotTestService(t, store)
	first, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: first.NextCursor, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor := second.NextCursor // page 2 is generated on demand
	results := make([]ServeResult, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = service.Serve(context.Background(), ServeRequest{
				Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: cursor, Now: now.Add(2 * time.Second),
			})
		}(index)
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent request %d: %v", index, err)
		}
	}
	if results[0].RequestID == "" || results[0].RequestID != results[1].RequestID {
		t.Fatalf("request IDs differ for the same cursor: %q and %q", results[0].RequestID, results[1].RequestID)
	}
	if !equalUintSlices(selectedPostIDs(results[0].Selected), selectedPostIDs(results[1].Selected)) {
		t.Fatalf("same cursor returned different pages: %v and %v", selectedPostIDs(results[0].Selected), selectedPostIDs(results[1].Selected))
	}
}

func TestRecommendationSnapshotRejectsCursorReuseAndReturnsStoreFailure(t *testing.T) {
	service, _, now := newSnapshotTestService(t, newMemorySnapshotStore())
	first, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 43}, Limit: 20, Cursor: first.NextCursor, Now: now.Add(time.Second),
	}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-user cursor error=%v want invalid cursor", err)
	}
	service.dependencies.Snapshots = unavailableSnapshotStore{SnapshotStore: service.dependencies.Snapshots}
	_, err = service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: first.NextCursor, Now: now.Add(time.Second),
	})
	if err == nil || !strings.Contains(err.Error(), "redis unavailable") {
		t.Fatalf("snapshot store failure=%v, want Redis error", err)
	}
}

func newSnapshotTestService(t testing.TB, store SnapshotStore) (*RecommendationService, *serviceTestCandidateRepository, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	calls := make([]string, 0)
	semantic := make([]Candidate, 0, 200)
	following := make([]Candidate, 0, 150)
	recent := make([]Candidate, 0, 150)
	trending := make([]Candidate, 0, 150)
	posts := make(map[uint]models.Post, 500)
	for id := uint(1); id <= 500; id++ {
		switch {
		case id <= 200:
			semantic = append(semantic, testCandidate(id, CandidateSourceSemantic))
		case id <= 350:
			following = append(following, testCandidate(id, CandidateSourceFollowing))
		case id <= 500:
			recent = append(recent, testCandidate(id, CandidateSourceRecent))
		}
		posts[id] = models.Post{Model: gorm.Model{ID: id, CreatedAt: now.Add(-time.Minute)}, AuthorID: id + 10000}
		if id <= 150 {
			trending = append(trending, testCandidate(id, CandidateSourceTrending))
		}
	}
	candidates := &serviceTestCandidateRepository{
		calls: &calls, semantic: semantic, following: following, recent: recent, trending: trending, posts: posts,
	}
	cfg := serviceTestConfig()
	cfg.Candidates.Personalized = config.RecommendationCandidateCaps{Semantic: 200, Following: 150, Recent: 150, Trending: 150, Merged: 500}
	cfg.Candidates.ColdStart = config.RecommendationCandidateCaps{Semantic: 200, Following: 150, Recent: 150, Trending: 150, Merged: 500}
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity = config.RecommendationDiversityConfig{Enabled: false}
	cfg.Exploration = config.RecommendationExplorationConfig{Ratio: 0}
	cfg.Snapshot = config.RecommendationSnapshotConfig{Enabled: true, TTLSeconds: 120, MaxCandidates: 500, PreselectPages: 2, DefaultPageSize: 20, MaxPageSize: 50}
	profiles := &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{
		PositiveVector: []float32{1}, ProfileStatus: ProfileStatusHit, ProfileVersion: "materialized-v1",
		ComputedAt: now.Add(-time.Hour), AuthorAffinity: make(map[uint]float64), FollowingAuthorIDs: make(map[uint]struct{}),
	}}}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles, History: &serviceTestHistoryStore{user: make(ServedHistory)}},
		ServingVersions:  serviceTestVersionProvider{version: "embedding-v1"}, Snapshots: store,
	}, ServiceConfig{Recommendation: cfg, SnapshotSigningKey: []byte(strings.Repeat("s", 32))}).(*RecommendationService)
	return service, candidates, now
}

func countString(items []string, value string) int {
	count := 0
	for _, item := range items {
		if item == value {
			count++
		}
	}
	return count
}

func countRecommendationSpans(spans []tracetest.SpanStub, name string) int {
	count := 0
	for _, span := range spans {
		if span.Name == name {
			count++
		}
	}
	return count
}

func equalUintSlices(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func medianDuration(values []time.Duration) time.Duration {
	return percentileDuration(values, .5)
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(float64(len(sorted))*percentile)) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

func sumDurations(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total
}
