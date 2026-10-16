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

func (repository *serviceTestCandidateRepository) LoadEligibleSnapshotCandidates(_ context.Context, _ CandidateQuery, candidates []Candidate) ([]RankedCandidate, error) {
	repository.snapshotEligibilityCalls++
	repository.snapshotCandidateIDsRead += len(candidates)
	items := make([]RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, exists := repository.posts[candidate.PostID]
		if !exists || post.ID == 0 || post.AuthorID == 0 {
			continue
		}
		items = append(items, RankedCandidate{Candidate: candidate, Post: post})
	}
	return items, nil
}

type unavailableSnapshotStore struct{ SnapshotStore }

func (unavailableSnapshotStore) Load(context.Context, string) (CandidatePoolSnapshot, error) {
	return CandidatePoolSnapshot{}, fmt.Errorf("%w: redis unavailable", ErrSnapshotUnavailable)
}

func TestRecommendationSnapshotReuses500CandidatePoolAcrossFivePages(t *testing.T) {
	service, candidates, now := newSnapshotTestService(t, newMemorySnapshotStore())
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
	t.Logf("simulated 5-page latency: candidate-pool p50=%s p95=%s total=%s; full-recompute p50=%s p95=%s total=%s; batched snapshot eligibility reads=%d candidate IDs=%d",
		medianDuration(snapshotPageDurations), percentileDuration(snapshotPageDurations, .95), sumDurations(snapshotPageDurations),
		medianDuration(legacyPageDurations), percentileDuration(legacyPageDurations, .95), sumDurations(legacyPageDurations),
		candidates.snapshotEligibilityCalls, candidates.snapshotCandidateIDsRead)
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

func newSnapshotTestService(t *testing.T, store SnapshotStore) (*RecommendationService, *serviceTestCandidateRepository, time.Time) {
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
