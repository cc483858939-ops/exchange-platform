package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/gorm"
)

type fakeRecommendationService struct {
	requests    []recommendation.ServeRequest
	contexts    []context.Context
	result      recommendation.ServeResult
	err         error
	finalizeErr error
	finalized   [][]recommendation.SelectedCandidate
}

func (fake *fakeRecommendationService) Serve(ctx context.Context, request recommendation.ServeRequest) (recommendation.ServeResult, error) {
	fake.requests = append(fake.requests, request)
	fake.contexts = append(fake.contexts, ctx)
	return fake.result, fake.err
}

func (fake *fakeRecommendationService) FinalizeServing(_ context.Context, _ recommendation.ServeResult, selected []recommendation.SelectedCandidate) error {
	fake.finalized = append(fake.finalized, append([]recommendation.SelectedCandidate(nil), selected...))
	return fake.finalizeErr
}

type fakeRecommendationResponseMapper struct {
	contexts      []context.Context
	selectedCalls [][]recommendation.SelectedCandidate
	result        []RecommendedPostResponse
	err           error
}

type traceDropEnqueuer struct {
	result recommendation.TraceEnqueueResult
	calls  int
}

func (enqueuer *traceDropEnqueuer) TryEnqueue(recommendation.TracePersistJob) recommendation.TraceEnqueueResult {
	enqueuer.calls++
	return enqueuer.result
}

type controllerRecommendationCandidates struct {
	items                []recommendation.Candidate
	posts                map[uint]models.Post
	calls                map[string]int
	snapshotPageContexts [][]uint
}

func (repo *controllerRecommendationCandidates) recordCall(name string) {
	if repo.calls == nil {
		repo.calls = make(map[string]int)
	}
	repo.calls[name]++
}

func (repo *controllerRecommendationCandidates) LoadSemanticCandidates(context.Context, recommendation.CandidateQuery) ([]recommendation.Candidate, error) {
	repo.recordCall("semantic")
	return nil, nil
}
func (repo *controllerRecommendationCandidates) LoadFollowingCandidates(context.Context, recommendation.CandidateQuery) ([]recommendation.Candidate, error) {
	repo.recordCall("following")
	return nil, nil
}
func (repo *controllerRecommendationCandidates) LoadRecentCandidates(context.Context, recommendation.CandidateQuery) ([]recommendation.Candidate, error) {
	repo.recordCall("recent")
	return append([]recommendation.Candidate(nil), repo.items...), nil
}
func (repo *controllerRecommendationCandidates) LoadTrendingCandidates(context.Context, recommendation.CandidateQuery) ([]recommendation.Candidate, error) {
	repo.recordCall("trending")
	return nil, nil
}
func (repo *controllerRecommendationCandidates) LoadPublicRecentCandidates(context.Context, recommendation.PublicCandidateQuery) ([]recommendation.Candidate, error) {
	return nil, nil
}
func (repo *controllerRecommendationCandidates) LoadPublicTrendingCandidates(context.Context, recommendation.PublicCandidateQuery) ([]recommendation.Candidate, error) {
	return nil, nil
}
func (repo *controllerRecommendationCandidates) HydrateCandidates(_ context.Context, _ recommendation.CandidateQuery, candidates []recommendation.Candidate) ([]recommendation.RankedCandidate, error) {
	items := make([]recommendation.RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, recommendation.RankedCandidate{
			Candidate: candidate, Post: repo.posts[candidate.PostID],
			Breakdown: recommendation.ScoreBreakdown{BaseScore: 1, FinalScore: 1},
		})
	}
	return items, nil
}
func (*controllerRecommendationCandidates) LoadPostEmbeddings(context.Context, []uint, string) (map[uint][]float32, error) {
	return map[uint][]float32{}, nil
}
func (repo *controllerRecommendationCandidates) LoadEligibleSnapshotCandidates(ctx context.Context, query recommendation.CandidateQuery, candidates []recommendation.Candidate) ([]recommendation.RankedCandidate, error) {
	eligible, _, err := repo.LoadSnapshotPage(ctx, query, candidates, nil)
	return eligible, err
}
func (repo *controllerRecommendationCandidates) LoadSnapshotPage(_ context.Context, _ recommendation.CandidateQuery, candidates []recommendation.Candidate, contextPostIDs []uint) ([]recommendation.RankedCandidate, map[uint][]float32, error) {
	repo.snapshotPageContexts = append(repo.snapshotPageContexts, append([]uint(nil), contextPostIDs...))
	items := make([]recommendation.RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, exists := repo.posts[candidate.PostID]
		if !exists || post.ID == 0 || post.AuthorID == 0 {
			continue
		}
		items = append(items, recommendation.RankedCandidate{Candidate: candidate, Post: post})
	}
	return items, map[uint][]float32{}, nil
}

type controllerRecommendationProfiles struct{ now time.Time }

func (repo controllerRecommendationProfiles) Load(context.Context, recommendation.ProfileLoadQuery) (recommendation.ProfileLoadResult, error) {
	return recommendation.ProfileLoadResult{Profile: recommendation.Profile{
		ProfileStatus: recommendation.ProfileStatusMiss, ComputedAt: repo.now,
		AuthorAffinity: map[uint]float64{}, FollowingAuthorIDs: map[uint]struct{}{},
	}}, nil
}
func (controllerRecommendationProfiles) LoadAuthorContext(context.Context, recommendation.AuthorContextQuery) (recommendation.AuthorContext, error) {
	return recommendation.AuthorContext{Affinity: map[uint]float64{}, FollowingAuthorIDs: map[uint]struct{}{}}, nil
}
func (controllerRecommendationProfiles) LoadPostAffinityInputs(context.Context, []uint) ([]recommendation.PostAffinityInput, error) {
	return nil, nil
}

type controllerRecommendationHistory struct{}

func (controllerRecommendationHistory) LoadUserHistory(context.Context, uint, recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	return recommendation.ServedHistory{}, nil
}
func (controllerRecommendationHistory) RecordUserServed(context.Context, uint, []uint, recommendation.HistoryWindow) error {
	return nil
}
func (controllerRecommendationHistory) LoadGuestHistory(context.Context, string, recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	return recommendation.ServedHistory{}, nil
}
func (controllerRecommendationHistory) RecordGuestServed(context.Context, string, []uint, recommendation.HistoryWindow) error {
	return nil
}

type controllerPageHistory struct {
	mu     sync.Mutex
	values [][]uint
}

func (history *controllerPageHistory) LoadUserHistory(context.Context, uint, recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	return recommendation.ServedHistory{}, nil
}
func (history *controllerPageHistory) RecordUserServed(_ context.Context, _ uint, postIDs []uint, _ recommendation.HistoryWindow) error {
	history.mu.Lock()
	defer history.mu.Unlock()
	history.values = append(history.values, append([]uint(nil), postIDs...))
	return nil
}
func (*controllerPageHistory) LoadGuestHistory(context.Context, string, recommendation.HistoryWindow) (recommendation.ServedHistory, error) {
	return recommendation.ServedHistory{}, nil
}
func (*controllerPageHistory) RecordGuestServed(context.Context, string, []uint, recommendation.HistoryWindow) error {
	return nil
}
func (history *controllerPageHistory) recordedPages() [][]uint {
	history.mu.Lock()
	defer history.mu.Unlock()
	result := make([][]uint, len(history.values))
	for index := range history.values {
		result[index] = append([]uint(nil), history.values[index]...)
	}
	return result
}

type controllerRecommendationVersion struct{}

func (controllerRecommendationVersion) LoadServingVersion(context.Context) (string, error) {
	return "post_embedding_v1", nil
}

type controllerMemorySnapshotStore struct {
	mu        sync.Mutex
	snapshots map[string]recommendation.CandidatePoolSnapshot
}

func newControllerMemorySnapshotStore() *controllerMemorySnapshotStore {
	return &controllerMemorySnapshotStore{snapshots: make(map[string]recommendation.CandidatePoolSnapshot)}
}
func (store *controllerMemorySnapshotStore) Create(_ context.Context, snapshot recommendation.CandidatePoolSnapshot, _ time.Duration, _ int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot.Revision = 1
	store.snapshots[snapshot.ID] = snapshot
	return nil
}
func (store *controllerMemorySnapshotStore) Load(_ context.Context, id string) (recommendation.CandidatePoolSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot, exists := store.snapshots[id]
	if !exists {
		return recommendation.CandidatePoolSnapshot{}, recommendation.ErrCursorExpired
	}
	return cloneControllerSnapshot(snapshot), nil
}
func (store *controllerMemorySnapshotStore) CompareAndSwap(_ context.Context, id string, expected int64, snapshot recommendation.CandidatePoolSnapshot, _ int) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.snapshots[id]
	if !exists {
		return false, recommendation.ErrCursorExpired
	}
	if current.Revision != expected {
		return false, nil
	}
	store.snapshots[id] = cloneControllerSnapshot(snapshot)
	return true, nil
}
func cloneControllerSnapshot(snapshot recommendation.CandidatePoolSnapshot) recommendation.CandidatePoolSnapshot {
	snapshot.Candidates = append([]recommendation.CachedCandidate(nil), snapshot.Candidates...)
	snapshot.UsedPostIDs = append([]uint(nil), snapshot.UsedPostIDs...)
	snapshot.MapperRejectedPostIDs = append([]uint(nil), snapshot.MapperRejectedPostIDs...)
	snapshot.Pages = append([]recommendation.CachedPage(nil), snapshot.Pages...)
	for index := range snapshot.Pages {
		snapshot.Pages[index].Items = append([]recommendation.CachedCandidate(nil), snapshot.Pages[index].Items...)
		snapshot.Pages[index].ReturnedPostIDs = append([]uint(nil), snapshot.Pages[index].ReturnedPostIDs...)
	}
	return snapshot
}

func (store *controllerMemorySnapshotStore) onlySnapshot() (recommendation.CandidatePoolSnapshot, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.snapshots) != 1 {
		return recommendation.CandidatePoolSnapshot{}, false
	}
	for _, snapshot := range store.snapshots {
		return cloneControllerSnapshot(snapshot), true
	}
	return recommendation.CandidatePoolSnapshot{}, false
}

type preselectionHTTPMapper struct {
	store              *controllerMemorySnapshotStore
	filterFirstIndex   int
	reorderFirstPage   bool
	calls              int
	inputIDs           [][]uint
	mappedIDs          [][]uint
	initialSnapshot    recommendation.CandidatePoolSnapshot
	hasInitialSnapshot bool
}

func (mapper *preselectionHTTPMapper) Map(_ context.Context, _ uint, selected []recommendation.SelectedCandidate, _ time.Time) ([]RecommendedPostResponse, error) {
	inputIDs := recommendationSelectedIDs(selected)
	if mapper.calls == 0 && mapper.store != nil {
		mapper.initialSnapshot, mapper.hasInitialSnapshot = mapper.store.onlySnapshot()
	}
	mapper.calls++
	ordered := append([]recommendation.SelectedCandidate(nil), selected...)
	if mapper.calls == 1 {
		if mapper.filterFirstIndex >= 0 && mapper.filterFirstIndex < len(ordered) {
			ordered = append(ordered[:mapper.filterFirstIndex], ordered[mapper.filterFirstIndex+1:]...)
		}
		if mapper.reorderFirstPage && len(ordered) >= 3 {
			ordered[1], ordered[2] = ordered[2], ordered[1]
		}
	}
	mapper.inputIDs = append(mapper.inputIDs, inputIDs)
	mappedIDs := recommendationSelectedIDs(ordered)
	mapper.mappedIDs = append(mapper.mappedIDs, mappedIDs)
	return mapRecommendationPostResponses(ordered), nil
}

func recommendationSelectedIDs(selected []recommendation.SelectedCandidate) []uint {
	ids := make([]uint, 0, len(selected))
	for _, item := range selected {
		ids = append(ids, item.Post.ID)
	}
	return ids
}

func recommendationTestPage(snapshot recommendation.CandidatePoolSnapshot, pageIndex int) (recommendation.CachedPage, bool) {
	for _, page := range snapshot.Pages {
		if page.Index == pageIndex {
			return page, true
		}
	}
	return recommendation.CachedPage{}, false
}

type filteringRecommendationResponseMapper struct {
	filterPosition int
	mappedPageIDs  [][]uint
}

func (mapper *filteringRecommendationResponseMapper) Map(_ context.Context, _ uint, selected []recommendation.SelectedCandidate, _ time.Time) ([]RecommendedPostResponse, error) {
	selectedIDs := make([]uint, 0, len(selected))
	for _, item := range selected {
		selectedIDs = append(selectedIDs, item.Post.ID)
	}
	for _, mappedIDs := range mapper.mappedPageIDs {
		if equalControllerIDs(selectedIDs, mappedIDs) {
			return mapRecommendationPostResponses(selected), nil
		}
	}

	result := make([]RecommendedPostResponse, 0, len(selected))
	for index, item := range selected {
		if index == mapper.filterPosition {
			continue
		}
		result = append(result, RecommendedPostResponse{Post: postResponse{ID: item.Post.ID}})
	}
	mappedIDs := make([]uint, 0, len(result))
	for _, item := range result {
		mappedIDs = append(mappedIDs, item.Post.ID)
	}
	mapper.mappedPageIDs = append(mapper.mappedPageIDs, mappedIDs)
	return result, nil
}

func mapRecommendationPostResponses(selected []recommendation.SelectedCandidate) []RecommendedPostResponse {
	result := make([]RecommendedPostResponse, 0, len(selected))
	for _, item := range selected {
		result = append(result, RecommendedPostResponse{Post: postResponse{ID: item.Post.ID}})
	}
	return result
}

type captureTraceEnqueuer struct {
	calls []recommendation.TracePersistJob
}

func (enqueuer *captureTraceEnqueuer) TryEnqueue(job recommendation.TracePersistJob) recommendation.TraceEnqueueResult {
	job.Results = append([]models.RecommendationResultTrace(nil), job.Results...)
	enqueuer.calls = append(enqueuer.calls, job)
	return recommendation.TraceEnqueueQueued
}

func (fake *fakeRecommendationResponseMapper) Map(ctx context.Context, _ uint, selected []recommendation.SelectedCandidate, _ time.Time) ([]RecommendedPostResponse, error) {
	fake.contexts = append(fake.contexts, ctx)
	fake.selectedCalls = append(fake.selectedCalls, append([]recommendation.SelectedCandidate(nil), selected...))
	return fake.result, fake.err
}

func newRecommendationHandlerForTest(t *testing.T, service recommendation.Service, mapper RecommendationResponseMapper, timeout time.Duration) *RecommendationHandler {
	t.Helper()
	handler, err := NewRecommendationHandler(service, mapper, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newRecommendationHTTPContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	return ctx, recorder
}

func TestRecommendationHandlerUsesInjectedServiceAndMapsRequest(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "serving-request", Now: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC), Depleted: true,
	}}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, time.Second)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts?limit=77&cursor=opaque-cursor")
	ctx.Set("user_id", uint(42))
	ctx.Request.Header.Set("Accept-Language", "ja-JP, en-US;q=0.2")
	handler.GetPostRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 || service.requests[0].Viewer != (recommendation.Viewer{Kind: recommendation.ViewerAuthenticated, UserID: 42}) {
		t.Fatalf("requests=%#v", service.requests)
	}
	if service.requests[0].Limit != 77 {
		t.Fatalf("limit=%d want raw transport value 77 for Service policy", service.requests[0].Limit)
	}
	if service.requests[0].Cursor != "opaque-cursor" {
		t.Fatalf("cursor=%q want opaque-cursor", service.requests[0].Cursor)
	}
	if service.requests[0].BrowserLanguage.BrowserPrimary != "ja" {
		t.Fatalf("browser language=%#v", service.requests[0].BrowserLanguage)
	}
	if len(service.contexts) != 1 {
		t.Fatalf("service calls=%d, want one", len(service.contexts))
	}
	if _, ok := service.contexts[0].Deadline(); !ok {
		t.Fatal("handler did not propagate its configured serving deadline")
	}
	if len(mapper.contexts) != 1 || len(mapper.selectedCalls) != 1 {
		t.Fatalf("mapper calls=%d, want one", len(mapper.contexts))
	}
	if len(service.finalized) != 1 {
		t.Fatalf("finalize calls=%d, want one after successful mapping", len(service.finalized))
	}
	if _, ok := mapper.contexts[0].Deadline(); !ok {
		t.Fatal("handler did not propagate serving deadline to response mapper")
	}
	if !strings.Contains(recorder.Body.String(), `"request_id":"serving-request"`) || !strings.Contains(recorder.Body.String(), `"depleted":true`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestRecommendationTraceQueueDropKeepsHTTPResponseSuccessful(t *testing.T) {
	for _, outcome := range []recommendation.TraceEnqueueResult{
		recommendation.TraceEnqueueDroppedFull,
		recommendation.TraceEnqueueDroppedStopping,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
			candidate := recommendation.Candidate{PostID: 17, FromRecent: true, RecentRank: 1, SourceCount: 1}
			candidates := &controllerRecommendationCandidates{
				items: []recommendation.Candidate{candidate},
				posts: map[uint]models.Post{17: {Model: gorm.Model{ID: 17, CreatedAt: now}, AuthorID: 71, Content: "recommendation"}},
			}
			cfg := recommendation.DefaultConfig()
			cfg.Snapshot.Enabled = false
			enqueuer := &traceDropEnqueuer{result: outcome}
			service, err := recommendation.NewService(recommendation.ServiceDependencies{
				DataDependencies: recommendation.DataDependencies{
					Candidates: candidates, Profiles: controllerRecommendationProfiles{now: now},
					History: controllerRecommendationHistory{},
				},
				ServingVersions: controllerRecommendationVersion{}, TraceEnqueuer: enqueuer,
			}, recommendation.ServiceConfig{Recommendation: cfg})
			if err != nil {
				t.Fatal(err)
			}
			mapper := &fakeRecommendationResponseMapper{result: []RecommendedPostResponse{{Post: postResponse{ID: 17}}}}
			handler := newRecommendationHandlerForTest(t, service, mapper, 0)
			ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
			ctx.Set("user_id", uint(42))
			handler.GetPostRecommendations(ctx)
			if recorder.Code != http.StatusOK {
				t.Fatalf("trace drop %s changed HTTP status=%d body=%s", outcome, recorder.Code, recorder.Body.String())
			}
			if enqueuer.calls != 1 || !strings.Contains(recorder.Body.String(), `"id":17`) {
				t.Fatalf("enqueue calls=%d response=%s", enqueuer.calls, recorder.Body.String())
			}
		})
	}
}

func TestRecommendationSnapshotHTTPChainCommitsMappedPageAndStableRetry(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	posts := make(map[uint]models.Post, 40)
	candidates := make([]recommendation.Candidate, 0, 40)
	for id := uint(1); id <= 40; id++ {
		candidates = append(candidates, recommendation.Candidate{PostID: id, FromRecent: true, RecentRank: int(id), SourceCount: 1})
		posts[id] = models.Post{Model: gorm.Model{ID: id, CreatedAt: now.Add(-time.Duration(id) * time.Minute)}, AuthorID: id + 1000, Content: "post"}
	}
	repository := &controllerRecommendationCandidates{items: candidates, posts: posts}
	history := &controllerPageHistory{}
	traces := &captureTraceEnqueuer{}
	snapshots := newControllerMemorySnapshotStore()
	cfg := recommendation.DefaultConfig()
	cfg.Snapshot = config.RecommendationSnapshotConfig{Enabled: true, TTLSeconds: 120, MaxCandidates: 40, PreselectPages: 1, DefaultPageSize: 5, MaxPageSize: 5}
	cfg.OutOfNetworkMinRatio = 0
	cfg.Exploration.Ratio = 0
	cfg.Diversity.Enabled = false
	trackingKey := []byte(strings.Repeat("http-tracking-key", 3))
	service, err := recommendation.NewService(recommendation.ServiceDependencies{
		DataDependencies: recommendation.DataDependencies{
			Candidates: repository, Profiles: controllerRecommendationProfiles{now: now}, History: history,
		},
		ServingVersions: controllerRecommendationVersion{}, Snapshots: snapshots, TraceEnqueuer: traces,
	}, recommendation.ServiceConfig{
		Recommendation: cfg, SnapshotSigningKey: []byte(strings.Repeat("snapshot-signing-key", 2)),
		Tracking: recommendation.TrackingConfig{Enabled: true, RolloutPercent: 100, SigningKey: trackingKey, TokenTTL: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newRecommendationHandlerForTest(t, service, &filteringRecommendationResponseMapper{filterPosition: 2}, 0)
	request := func(cursor string) (postRecommendationPageResponse, string, int) {
		path := "/api/recommendations/posts?limit=5"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		ctx, recorder := newRecommendationHTTPContext(http.MethodGet, path)
		ctx.Set("user_id", uint(42))
		handler.GetPostRecommendations(ctx)
		var response postRecommendationPageResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response %s: %v", recorder.Body.String(), err)
		}
		return response, recorder.Body.String(), recorder.Code
	}
	first, _, status := request("")
	if status != http.StatusOK || len(first.Items) != 4 || first.NextCursor == nil {
		t.Fatalf("first page status=%d items=%d next=%v", status, len(first.Items), first.NextCursor)
	}
	firstIDs := make([]uint, len(first.Items))
	seenIDs := make(map[uint]struct{}, 20)
	for index, item := range first.Items {
		firstIDs[index] = item.Post.ID
		seenIDs[item.Post.ID] = struct{}{}
		if item.Tracking == nil || item.Tracking.Position != index+1 {
			t.Fatalf("first page item %d tracking=%#v", index, item.Tracking)
		}
		claims, err := recommendation.VerifyTrackingToken(item.Tracking.Token, trackingKey)
		if err != nil || claims.PostID != item.Post.ID || claims.Position != index+1 {
			t.Fatalf("first page tracking claims=%#v err=%v", claims, err)
		}
	}
	if len(traces.calls) != 1 || len(traces.calls[0].Results) != len(first.Items) {
		t.Fatalf("trace jobs=%d results=%d want one job and %d results", len(traces.calls), len(traces.calls[0].Results), len(first.Items))
	}
	for index, result := range traces.calls[0].Results {
		if result.PostID != firstIDs[index] || result.Position != index+1 {
			t.Fatalf("trace[%d]=%#v does not match mapped response", index, result)
		}
	}
	recorded := history.recordedPages()
	if len(recorded) != 1 || !equalControllerIDs(recorded[0], firstIDs) {
		t.Fatalf("history=%v want mapped IDs %v", recorded, firstIDs)
	}

	cursor := *first.NextCursor
	second, secondBody, status := request(cursor)
	if status != http.StatusOK || len(second.Items) != 4 {
		t.Fatalf("second page status=%d items=%d body=%s", status, len(second.Items), secondBody)
	}
	for index, item := range second.Items {
		if _, duplicate := seenIDs[item.Post.ID]; duplicate {
			t.Fatalf("post %d repeated across mapped pages", item.Post.ID)
		}
		seenIDs[item.Post.ID] = struct{}{}
		if item.Tracking == nil || item.Tracking.Position != index+1 {
			t.Fatalf("second page item %d tracking=%#v", index, item.Tracking)
		}
	}
	secondRetry, retryBody, status := request(cursor)
	if status != http.StatusOK || secondBody != retryBody || second.RequestID != secondRetry.RequestID {
		t.Fatalf("same cursor retry changed page: status=%d first=%s retry=%s", status, secondBody, retryBody)
	}
	if len(traces.calls) != 2 || len(history.recordedPages()) != 2 {
		t.Fatalf("trace jobs=%d history pages=%d, want one per committed page despite retry", len(traces.calls), len(history.recordedPages()))
	}
	for index, result := range traces.calls[1].Results {
		if result.Position != index+1 || result.PostID != second.Items[index].Post.ID {
			t.Fatalf("retried trace[%d]=%#v response=%#v", index, result, second.Items[index])
		}
	}
	delete(repository.posts, second.Items[0].Post.ID)
	staleRetry, staleBody, status := request(cursor)
	if status != http.StatusGone || len(staleRetry.Items) != 0 {
		t.Fatalf("page with invalidated committed content status=%d items=%d body=%s", status, len(staleRetry.Items), staleBody)
	}
	if len(traces.calls) != 2 || len(history.recordedPages()) != 2 {
		t.Fatalf("invalidated retry created side effects: trace jobs=%d history pages=%d", len(traces.calls), len(history.recordedPages()))
	}
	cursor = *second.NextCursor
	for pageIndex := 2; pageIndex < 5; pageIndex++ {
		page, body, pageStatus := request(cursor)
		if pageStatus != http.StatusOK || len(page.Items) != 4 || page.NextCursor == nil {
			t.Fatalf("page %d status=%d items=%d next=%v body=%s", pageIndex, pageStatus, len(page.Items), page.NextCursor, body)
		}
		for index, item := range page.Items {
			if _, duplicate := seenIDs[item.Post.ID]; duplicate {
				t.Fatalf("post %d repeated across mapped pages", item.Post.ID)
			}
			seenIDs[item.Post.ID] = struct{}{}
			if item.Tracking == nil || item.Tracking.Position != index+1 {
				t.Fatalf("page %d item %d tracking=%#v", pageIndex, index, item.Tracking)
			}
		}
		traceJob := traces.calls[pageIndex]
		if len(traceJob.Results) != len(page.Items) {
			t.Fatalf("page %d trace results=%d response items=%d", pageIndex, len(traceJob.Results), len(page.Items))
		}
		for index, traceResult := range traceJob.Results {
			if traceResult.PostID != page.Items[index].Post.ID || traceResult.Position != index+1 {
				t.Fatalf("page %d trace[%d]=%#v response=%#v", pageIndex, index, traceResult, page.Items[index])
			}
		}
		cursor = *page.NextCursor
	}
	if len(seenIDs) != 20 || len(traces.calls) != 5 || len(history.recordedPages()) != 5 {
		t.Fatalf("five mapped pages: unique posts=%d trace jobs=%d history pages=%d", len(seenIDs), len(traces.calls), len(history.recordedPages()))
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if repository.calls[source] != 1 {
			t.Fatalf("%s recall calls=%d want 1", source, repository.calls[source])
		}
	}
}

type preselectionHTTPFixture struct {
	handler   *RecommendationHandler
	repo      *controllerRecommendationCandidates
	snapshots *controllerMemorySnapshotStore
	history   *controllerPageHistory
	traces    *captureTraceEnqueuer
	mapper    *preselectionHTTPMapper
}

func newPreselectionHTTPFixture(t *testing.T, filterFirstIndex int, reorderFirstPage bool) *preselectionHTTPFixture {
	t.Helper()
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	posts := make(map[uint]models.Post, 40)
	candidates := make([]recommendation.Candidate, 0, 40)
	for id := uint(1); id <= 40; id++ {
		candidates = append(candidates, recommendation.Candidate{PostID: id, FromRecent: true, RecentRank: int(id), SourceCount: 1})
		posts[id] = models.Post{Model: gorm.Model{ID: id, CreatedAt: now.Add(-time.Duration(id) * time.Minute)}, AuthorID: id + 1000, Content: "post"}
	}
	repository := &controllerRecommendationCandidates{items: candidates, posts: posts}
	history := &controllerPageHistory{}
	traces := &captureTraceEnqueuer{}
	snapshots := newControllerMemorySnapshotStore()
	cfg := recommendation.DefaultConfig()
	cfg.Snapshot = config.RecommendationSnapshotConfig{Enabled: true, TTLSeconds: 120, MaxCandidates: 40, PreselectPages: 2, DefaultPageSize: 5, MaxPageSize: 5}
	cfg.OutOfNetworkMinRatio = 0
	cfg.Exploration.Ratio = 0
	cfg.Diversity.Enabled = false
	trackingKey := []byte(strings.Repeat("preselection-http-tracking", 2))
	service, err := recommendation.NewService(recommendation.ServiceDependencies{
		DataDependencies: recommendation.DataDependencies{
			Candidates: repository, Profiles: controllerRecommendationProfiles{now: now}, History: history,
		},
		ServingVersions: controllerRecommendationVersion{}, Snapshots: snapshots, TraceEnqueuer: traces,
	}, recommendation.ServiceConfig{
		Recommendation: cfg, SnapshotSigningKey: []byte(strings.Repeat("preselection-snapshot-signing", 2)),
		Tracking: recommendation.TrackingConfig{Enabled: true, RolloutPercent: 100, SigningKey: trackingKey, TokenTTL: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	mapper := &preselectionHTTPMapper{store: snapshots, filterFirstIndex: filterFirstIndex, reorderFirstPage: reorderFirstPage}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	return &preselectionHTTPFixture{handler: handler, repo: repository, snapshots: snapshots, history: history, traces: traces, mapper: mapper}
}

func (fixture *preselectionHTTPFixture) request(t *testing.T, cursor string) (postRecommendationPageResponse, int, string) {
	t.Helper()
	path := "/api/recommendations/posts?limit=5"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, path)
	ctx.Set("user_id", uint(42))
	fixture.handler.GetPostRecommendations(ctx)
	var response postRecommendationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %s: %v", recorder.Body.String(), err)
	}
	return response, recorder.Code, recorder.Body.String()
}

func TestRecommendationPreselectedPageRemainsValidWhenFirstPageUnchanged(t *testing.T) {
	fixture := newPreselectionHTTPFixture(t, -1, false)
	first, status, body := fixture.request(t, "")
	if status != http.StatusOK || len(first.Items) != 5 || first.NextCursor == nil {
		t.Fatalf("first page status=%d items=%d next=%v body=%s", status, len(first.Items), first.NextCursor, body)
	}
	if !fixture.mapper.hasInitialSnapshot {
		t.Fatal("mapper did not observe the initial preselection snapshot")
	}
	preparedZero, ok := recommendationTestPage(fixture.mapper.initialSnapshot, 0)
	if !ok || !equalControllerIDs(recommendationSelectedIDsFromCached(preparedZero), fixture.mapper.mappedIDs[0]) {
		t.Fatalf("page 0 prepared=%v mapped=%v", recommendationSelectedIDsFromCached(preparedZero), fixture.mapper.mappedIDs[0])
	}
	preparedOne, ok := recommendationTestPage(fixture.mapper.initialSnapshot, 1)
	if !ok || len(preparedOne.Items) != 5 {
		t.Fatalf("page 1 was not preselected: %#v", preparedOne)
	}

	stored, err := fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	pageZero, _ := recommendationTestPage(stored, 0)
	pageOne, ok := recommendationTestPage(stored, 1)
	if !ok || !pageZero.Committed || !equalControllerIDs(pageZero.ReturnedPostIDs, fixture.mapper.mappedIDs[0]) {
		t.Fatalf("stored pages after unchanged first result: page0=%#v page1=%#v", pageZero, pageOne)
	}
	if pageOne.RequestID != preparedOne.RequestID || !equalControllerIDs(recommendationSelectedIDsFromCached(pageOne), recommendationSelectedIDsFromCached(preparedOne)) {
		t.Fatalf("unchanged page 0 did not preserve page 1 preselection: before=%#v after=%#v", preparedOne, pageOne)
	}

	second, status, body := fixture.request(t, *first.NextCursor)
	if status != http.StatusOK || len(second.Items) != 5 {
		t.Fatalf("second page status=%d items=%d body=%s", status, len(second.Items), body)
	}
	stored, err = fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	pageOne, ok = recommendationTestPage(stored, 1)
	if !ok || !pageOne.Committed || pageOne.RequestID != preparedOne.RequestID || !equalControllerIDs(recommendationSelectedIDsFromCached(pageOne), recommendationSelectedIDsFromCached(preparedOne)) {
		t.Fatalf("preselected page 1 was regenerated despite unchanged page 0: %#v", pageOne)
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if fixture.repo.calls[source] != 1 {
			t.Fatalf("%s recall calls=%d, want one full recall", source, fixture.repo.calls[source])
		}
	}
}

func TestRecommendationPreselectedPageInvalidatedAfterMapperFiltering(t *testing.T) {
	fixture := newPreselectionHTTPFixture(t, 2, false)
	first, status, body := fixture.request(t, "")
	if status != http.StatusOK || len(first.Items) != 4 || first.NextCursor == nil {
		t.Fatalf("filtered first page status=%d items=%d next=%v body=%s", status, len(first.Items), first.NextCursor, body)
	}
	preparedOne, ok := recommendationTestPage(fixture.mapper.initialSnapshot, 1)
	if !ok {
		t.Fatal("expected page 1 to be preselected before mapping page 0")
	}
	preparedZero, _ := recommendationTestPage(fixture.mapper.initialSnapshot, 0)
	filteredID := recommendationSelectedIDsFromCached(preparedZero)[2]
	firstIDs := recommendationResponseIDs(first.Items)
	if containsControllerID(firstIDs, filteredID) {
		t.Fatalf("filtered post %d appeared in page 0 response %v", filteredID, firstIDs)
	}
	if len(fixture.history.recordedPages()) != 1 || !equalControllerIDs(fixture.history.recordedPages()[0], firstIDs) {
		t.Fatalf("history=%v want actual mapped page %v", fixture.history.recordedPages(), firstIDs)
	}
	if len(fixture.traces.calls) != 1 || len(fixture.traces.calls[0].Results) != len(firstIDs) {
		t.Fatalf("trace jobs=%d results=%d want only %d mapped posts", len(fixture.traces.calls), len(fixture.traces.calls[0].Results), len(firstIDs))
	}
	for _, traceResult := range fixture.traces.calls[0].Results {
		if traceResult.PostID == filteredID {
			t.Fatalf("mapper-rejected post %d entered trace", filteredID)
		}
	}

	stored, err := fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	pageZero, ok := recommendationTestPage(stored, 0)
	if !ok || !pageZero.Committed || !equalControllerIDs(pageZero.ReturnedPostIDs, firstIDs) {
		t.Fatalf("committed page 0=%#v want IDs %v", pageZero, firstIDs)
	}
	if _, exists := recommendationTestPage(stored, 1); exists {
		t.Fatal("dependent preselected page 1 was not invalidated")
	}
	if !equalControllerIDs(stored.UsedPostIDs, firstIDs) {
		t.Fatalf("used IDs=%v want only committed page IDs=%v", stored.UsedPostIDs, firstIDs)
	}
	if !equalControllerIDs(stored.MapperRejectedPostIDs, []uint{filteredID}) {
		t.Fatalf("mapper rejected IDs=%v want [%d]", stored.MapperRejectedPostIDs, filteredID)
	}
	oldPageOneIDs := recommendationSelectedIDsFromCached(preparedOne)
	for _, id := range oldPageOneIDs {
		if containsControllerID(stored.UsedPostIDs, id) {
			t.Fatalf("invalidated page 1 post %d remains reserved", id)
		}
	}

	second, status, body := fixture.request(t, *first.NextCursor)
	if status != http.StatusOK || len(second.Items) != 5 {
		t.Fatalf("regenerated page 1 status=%d items=%d body=%s", status, len(second.Items), body)
	}
	secondIDs := recommendationResponseIDs(second.Items)
	for _, id := range secondIDs {
		if containsControllerID(firstIDs, id) || id == filteredID {
			t.Fatalf("page 1 repeated committed/rejected post %d; first=%v second=%v", id, firstIDs, secondIDs)
		}
	}
	if fixture.mapper.calls != 2 || len(fixture.repo.snapshotPageContexts) < 1 || !equalControllerIDs(fixture.repo.snapshotPageContexts[0], firstIDs) {
		t.Fatalf("page 1 reselect context=%v mapped calls=%d want committed page context %v", fixture.repo.snapshotPageContexts, fixture.mapper.calls, firstIDs)
	}
	stored, err = fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	newPageOne, ok := recommendationTestPage(stored, 1)
	if !ok || !newPageOne.Committed || newPageOne.RequestID == preparedOne.RequestID {
		t.Fatalf("page 1 was not freshly selected and committed: old=%#v new=%#v", preparedOne, newPageOne)
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if fixture.repo.calls[source] != 1 {
			t.Fatalf("%s recall calls=%d, want one full recall", source, fixture.repo.calls[source])
		}
	}
}

func TestRecommendationPreselectedPageInvalidatedAfterMapperReordering(t *testing.T) {
	fixture := newPreselectionHTTPFixture(t, -1, true)
	first, status, body := fixture.request(t, "")
	if status != http.StatusOK || len(first.Items) != 5 || first.NextCursor == nil {
		t.Fatalf("reordered first page status=%d items=%d next=%v body=%s", status, len(first.Items), first.NextCursor, body)
	}
	preparedZero, _ := recommendationTestPage(fixture.mapper.initialSnapshot, 0)
	preparedZeroIDs := recommendationSelectedIDsFromCached(preparedZero)
	preparedOne, ok := recommendationTestPage(fixture.mapper.initialSnapshot, 1)
	if !ok {
		t.Fatal("expected a preselected page 1")
	}
	firstIDs := recommendationResponseIDs(first.Items)
	if equalControllerIDs(firstIDs, preparedZeroIDs) || firstIDs[1] != preparedZeroIDs[2] || firstIDs[2] != preparedZeroIDs[1] {
		t.Fatalf("mapper did not reorder page 0: prepared=%v returned=%v", preparedZeroIDs, firstIDs)
	}
	for index, item := range first.Items {
		if item.Tracking == nil || item.Tracking.Position != index+1 {
			t.Fatalf("reordered item %d tracking=%#v", index, item.Tracking)
		}
	}
	stored, err := fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	pageZero, _ := recommendationTestPage(stored, 0)
	if !pageZero.Committed || !equalControllerIDs(pageZero.ReturnedPostIDs, firstIDs) {
		t.Fatalf("page 0 order was not committed: %#v want %v", pageZero, firstIDs)
	}
	if _, exists := recommendationTestPage(stored, 1); exists {
		t.Fatal("dependent page 1 survived a page 0 reorder")
	}

	second, status, body := fixture.request(t, *first.NextCursor)
	if status != http.StatusOK || len(second.Items) != 5 {
		t.Fatalf("reselected page 1 status=%d items=%d body=%s", status, len(second.Items), body)
	}
	if len(fixture.repo.snapshotPageContexts) < 1 || !equalControllerIDs(fixture.repo.snapshotPageContexts[0], firstIDs) {
		t.Fatalf("page 1 used stale page 0 order: contexts=%v want %v", fixture.repo.snapshotPageContexts, firstIDs)
	}
	stored, err = fixture.snapshots.Load(context.Background(), fixture.mapper.initialSnapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	newPageOne, ok := recommendationTestPage(stored, 1)
	if !ok || newPageOne.RequestID == preparedOne.RequestID {
		t.Fatalf("page 1 was not regenerated after reorder: old=%#v new=%#v", preparedOne, newPageOne)
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		if fixture.repo.calls[source] != 1 {
			t.Fatalf("%s recall calls=%d, want one full recall", source, fixture.repo.calls[source])
		}
	}
}

func recommendationSelectedIDsFromCached(page recommendation.CachedPage) []uint {
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.PostID)
	}
	return ids
}

func recommendationResponseIDs(items []RecommendedPostResponse) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Post.ID)
	}
	return ids
}

func containsControllerID(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func equalControllerIDs(left, right []uint) bool {
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

func TestPublicRecommendationHandlerNormalizesGuestSessionHeader(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "guest-request", Now: time.Now().UTC(), Depleted: false,
	}}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts?limit=not-a-number")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "  4CA3706B-197E-4F63-8F51-F99176F8B61C ")
	handler.GetPublicPostRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 || service.requests[0].Viewer.Kind != recommendation.ViewerGuest || service.requests[0].Viewer.GuestSessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
		t.Fatalf("requests=%#v", service.requests)
	}
	if service.requests[0].Limit != 0 {
		t.Fatalf("invalid limit=%d want service default sentinel 0", service.requests[0].Limit)
	}
}

func TestPublicRecommendationHandlerKeepsInvalidGuestSessionEmpty(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{RequestID: "guest-request", Now: time.Now().UTC()}}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "not-a-uuid")
	handler.GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || len(service.requests) != 1 || service.requests[0].Viewer != (recommendation.Viewer{Kind: recommendation.ViewerGuest}) {
		t.Fatalf("status=%d requests=%#v body=%s", recorder.Code, service.requests, recorder.Body.String())
	}
}

func TestRecommendationHandlerMapsDeadlineAndRequestCancellation(t *testing.T) {
	mapper := &fakeRecommendationResponseMapper{}
	service := &fakeRecommendationService{err: context.DeadlineExceeded}
	handler := newRecommendationHandlerForTest(t, service, mapper, time.Second)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(mapper.contexts) != 0 {
		t.Fatalf("mapper calls=%d, want none after service failure", len(mapper.contexts))
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, recorder = newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	ctx.Request = ctx.Request.WithContext(cancelled)
	handler.GetPostRecommendations(ctx)
	if ctx.Writer.Written() {
		t.Fatalf("canceled parent request wrote a response: %s", recorder.Body.String())
	}
}

func TestRecommendationHandlerRequiresAuthenticatedViewer(t *testing.T) {
	service := &fakeRecommendationService{}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 0 || len(mapper.contexts) != 0 {
		t.Fatalf("unauthenticated request called dependencies: service=%d mapper=%d", len(service.requests), len(mapper.contexts))
	}
}

func TestRecommendationHandlerMapsServiceFailureToHTTP500(t *testing.T) {
	service := &fakeRecommendationService{err: errors.New("service unavailable")}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "service unavailable") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	requireAPIError(t, recorder, "INTERNAL_ERROR")
	if len(service.requests) != 1 {
		t.Fatalf("service calls=%d, want one", len(service.requests))
	}
	if len(mapper.contexts) != 0 {
		t.Fatalf("mapper calls=%d, want none after service failure", len(mapper.contexts))
	}
}

func TestRecommendationHandlerMapsTrackingFactsIntoResponseJSON(t *testing.T) {
	recommendations := []RecommendedPostResponse{{Post: postResponse{ID: 17}, Score: .75}}
	facts := []recommendation.TrackingFact{{
		PostID: 17, RequestID: "request-17", Position: 1, Scene: "recommendation_page",
		RankerVersion: "rules_v6", RankerConfigHash: "hash-17", StrategyID: "strategy-17",
		Token: "signed-token", ExpiresAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
	}}
	attachRecommendationTrackingFacts(recommendations, facts)
	body, err := json.Marshal(postRecommendationPageResponse{Items: recommendations, RequestID: "request-17"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"tracking"`, `"request_id":"request-17"`, `"position":1`, `"token":"signed-token"`, `"ranker_version":"rules_v6"`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("response=%s missing %s", body, field)
		}
	}
}

func TestRecommendationHandlerRequiresService(t *testing.T) {
	mapper := &fakeRecommendationResponseMapper{}
	if _, err := NewRecommendationHandler(nil, mapper, 0); err == nil {
		t.Fatal("expected constructor to reject nil service")
	}
	if _, err := NewRecommendationHandler(&fakeRecommendationService{}, nil, 0); err == nil {
		t.Fatal("expected constructor to reject nil response mapper")
	}
}

func TestRecommendationHandlerMapsResponseMapperFailureToHTTP500(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{RequestID: "request", Now: time.Now().UTC()}}
	mapper := &fakeRecommendationResponseMapper{err: errors.New("hydrate failed")}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "hydrate failed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	requireAPIError(t, recorder, "INTERNAL_ERROR")
	if len(mapper.contexts) != 1 {
		t.Fatalf("mapper calls=%d, want one", len(mapper.contexts))
	}
	if len(service.finalized) != 0 {
		t.Fatalf("finalize calls=%d after mapping failure, want none", len(service.finalized))
	}
}

func TestRecommendationHandlerFinalizesOnlySuccessfullyMappedPosts(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "request", Now: time.Now().UTC(), Selected: []recommendation.SelectedCandidate{
			{Post: models.Post{Model: gorm.Model{ID: 7}}},
			{Post: models.Post{Model: gorm.Model{ID: 8}}},
		},
	}}
	mapper := &fakeRecommendationResponseMapper{result: []RecommendedPostResponse{{Post: postResponse{ID: 8}}}}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.finalized) != 1 || len(service.finalized[0]) != 1 || service.finalized[0][0].Post.ID != 8 {
		t.Fatalf("finalized selections=%#v, want only successfully mapped post 8", service.finalized)
	}
}

func TestRecommendationHandlerMapsCursorErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
		body string
	}{
		{name: "invalid", err: recommendation.ErrInvalidCursor, code: http.StatusBadRequest, body: "INVALID_CURSOR"},
		{name: "expired", err: recommendation.ErrCursorExpired, code: http.StatusGone, body: "CURSOR_EXPIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeRecommendationService{err: test.err}
			handler := newRecommendationHandlerForTest(t, service, &fakeRecommendationResponseMapper{}, 0)
			ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts?cursor=token")
			ctx.Set("user_id", uint(42))
			handler.GetPostRecommendations(ctx)
			if recorder.Code != test.code || !strings.Contains(recorder.Body.String(), test.body) {
				t.Fatalf("status=%d body=%s want %d containing %q", recorder.Code, recorder.Body.String(), test.code, test.body)
			}
		})
	}
}

func TestRecommendationHTTPSpanLinksRequestIDForSuccessAndFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		requestID  string
		serviceErr error
		mapperErr  error
		wantStatus int
	}{
		{name: "success", requestID: "business-request-17", wantStatus: http.StatusOK},
		{name: "service error with request ID", requestID: "failed-request-18", serviceErr: errors.New("service unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "service error without request ID", serviceErr: errors.New("service unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "mapper error", requestID: "mapper-request-19", mapperErr: errors.New("private SQL parameter and token"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown tracer provider: %v", err)
				}
			})
			requestCtx, serverSpan := provider.Tracer("test").Start(context.Background(), "GET /api/public/recommendations/posts")
			service := &fakeRecommendationService{
				result: recommendation.ServeResult{RequestID: test.requestID, Now: time.Now().UTC()},
				err:    test.serviceErr,
			}
			mapper := &fakeRecommendationResponseMapper{err: test.mapperErr}
			handler := newRecommendationHandlerForTest(t, service, mapper, 0)
			ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts")
			ctx.Request = ctx.Request.WithContext(requestCtx)
			handler.GetPublicPostRecommendations(ctx)
			serverSpan.End()

			spans := exporter.GetSpans()
			var server, mapperSpan *tracetest.SpanStub
			for index := range spans {
				switch spans[index].Name {
				case "GET /api/public/recommendations/posts":
					server = &spans[index]
				case "recommendation.response.map":
					mapperSpan = &spans[index]
				}
			}
			if server == nil {
				t.Fatalf("missing server span: %#v", spans)
			}
			if test.serviceErr == nil && mapperSpan == nil {
				t.Fatalf("missing response map span: %#v", spans)
			}
			if mapperSpan != nil && mapperSpan.Parent.SpanID() != server.SpanContext.SpanID() {
				t.Fatalf("response map parent=%s server span=%s", mapperSpan.Parent.SpanID(), server.SpanContext.SpanID())
			}
			if test.requestID != "" {
				if !hasStringAttribute(server.Attributes, "recommendation.request_id", test.requestID) {
					t.Fatalf("business request ID missing from HTTP span: %#v", server.Attributes)
				}
			} else if hasAttribute(server.Attributes, "recommendation.request_id") {
				t.Fatalf("empty request ID created an HTTP attribute: %#v", server.Attributes)
			}
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.serviceErr != nil && strings.Contains(recorder.Body.String(), test.serviceErr.Error()) {
				t.Fatalf("service error leaked into response: %s", recorder.Body.String())
			}
			if test.serviceErr != nil || test.mapperErr != nil {
				requireAPIError(t, recorder, "INTERNAL_ERROR")
			}
			if test.mapperErr != nil {
				if mapperSpan.Status.Code != codes.Error {
					t.Fatalf("mapper status=%v, want error", mapperSpan.Status.Code)
				}
				for _, event := range mapperSpan.Events {
					for _, item := range event.Attributes {
						if strings.Contains(item.Value.AsString(), "private SQL") || strings.Contains(item.Value.AsString(), "token") {
							t.Fatalf("sensitive mapper error leaked into trace: %#v", event.Attributes)
						}
					}
				}
			}
		})
	}
}

func hasAttribute(attributes []attribute.KeyValue, key string) bool {
	for _, item := range attributes {
		if string(item.Key) == key {
			return true
		}
	}
	return false
}

func hasStringAttribute(attributes []attribute.KeyValue, key, want string) bool {
	for _, item := range attributes {
		if string(item.Key) == key && item.Value.AsString() == want {
			return true
		}
	}
	return false
}
