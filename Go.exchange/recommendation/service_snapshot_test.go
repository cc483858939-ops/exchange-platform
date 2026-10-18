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

func (repository *serviceTestCandidateRepository) LoadSnapshotPage(_ context.Context, _ CandidateQuery, candidates []Candidate, contextPostIDs []uint) ([]RankedCandidate, map[uint][]float32, error) {
	repository.snapshotEligibilityCalls++
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
		post, exists := repository.posts[candidate.PostID]
		if !exists || post.ID == 0 || post.AuthorID == 0 {
			continue
		}
		items = append(items, RankedCandidate{Candidate: candidate, Post: post, Embedding: append([]float32(nil), repository.embeddings[candidate.PostID]...)})
	}
	return items, contextEmbeddings, nil
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

func TestRecommendationSnapshotRejectsCursorReuseByAnotherUserAndFallsBackWhenRedisUnavailable(t *testing.T) {
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
	fallback, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 20, Cursor: first.NextCursor, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fallback.snapshotFallback || fallback.NextCursor != "" || fallback.Depleted {
		t.Fatalf("redis fallback state=%#v", fallback)
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
