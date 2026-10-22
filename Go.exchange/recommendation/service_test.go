package recommendation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type serviceTestVersionProvider struct {
	version string
	err     error
	calls   *[]string
}

func (provider serviceTestVersionProvider) LoadServingVersion(context.Context) (string, error) {
	if provider.calls != nil {
		*provider.calls = append(*provider.calls, "version")
	}
	return provider.version, provider.err
}

type serviceTestCandidateRepository struct {
	calls                    *[]string
	semantic                 []Candidate
	following                []Candidate
	recent                   []Candidate
	trending                 []Candidate
	publicRecent             []Candidate
	publicTrend              []Candidate
	softRecent               []Candidate
	posts                    map[uint]models.Post
	embeddings               map[uint][]float32
	loadErr                  error
	hydrateErr               error
	publicCalls              int
	softQueries              int
	softHistoricalQueries    int
	recentQueries            []CandidateQuery
	hydrateQueries           []CandidateQuery
	snapshotEligibilityCalls int
	snapshotCandidateIDsRead int
	embeddingReadCalls       int
	snapshotEmbeddingIDsRead int
	snapshotDatabaseQueries  int
	snapshotPageCandidateIDs []int
	snapshotQueries          []CandidateQuery
	snapshotIneligibleIDs    map[uint]struct{}
}

func (repository *serviceTestCandidateRepository) addCall(value string) {
	if repository.calls != nil {
		*repository.calls = append(*repository.calls, value)
	}
}

func (repository *serviceTestCandidateRepository) LoadSemanticCandidates(context.Context, CandidateQuery) ([]Candidate, error) {
	repository.addCall("semantic")
	return append([]Candidate(nil), repository.semantic...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) LoadFollowingCandidates(context.Context, CandidateQuery) ([]Candidate, error) {
	repository.addCall("following")
	return append([]Candidate(nil), repository.following...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) LoadRecentCandidates(_ context.Context, query CandidateQuery) ([]Candidate, error) {
	repository.addCall("recent")
	repository.recentQueries = append(repository.recentQueries, query)
	if query.SoftOnly {
		repository.softQueries++
		soft := make([]Candidate, 0, len(repository.softRecent))
		for _, candidate := range repository.softRecent {
			served, exists := query.Served[candidate.PostID]
			if exists && served.Soft && !served.Hard {
				soft = append(soft, candidate)
			}
		}
		return soft, repository.loadErr
	}
	return append([]Candidate(nil), repository.recent...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) LoadTrendingCandidates(context.Context, CandidateQuery) ([]Candidate, error) {
	repository.addCall("trending")
	return append([]Candidate(nil), repository.trending...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) LoadPublicRecentCandidates(context.Context, PublicCandidateQuery) ([]Candidate, error) {
	repository.addCall("public_recent")
	repository.publicCalls++
	return append([]Candidate(nil), repository.publicRecent...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) LoadPublicTrendingCandidates(context.Context, PublicCandidateQuery) ([]Candidate, error) {
	repository.addCall("public_trending")
	repository.publicCalls++
	return append([]Candidate(nil), repository.publicTrend...), repository.loadErr
}
func (repository *serviceTestCandidateRepository) HydrateCandidates(_ context.Context, query CandidateQuery, candidates []Candidate) ([]RankedCandidate, error) {
	repository.addCall("hydrate")
	repository.hydrateQueries = append(repository.hydrateQueries, query)
	if repository.hydrateErr != nil {
		return nil, repository.hydrateErr
	}
	items := make([]RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, exists := repository.posts[candidate.PostID]
		if !exists {
			post = models.Post{Model: gorm.Model{ID: candidate.PostID, CreatedAt: time.Now().UTC()}, AuthorID: candidate.PostID + 1000}
		}
		items = append(items, RankedCandidate{Candidate: candidate, Post: post})
	}
	return items, nil
}
func (repository *serviceTestCandidateRepository) LoadPostEmbeddings(_ context.Context, postIDs []uint, _ string) (map[uint][]float32, error) {
	repository.embeddingReadCalls++
	embeddings := make(map[uint][]float32, len(postIDs))
	for _, postID := range postIDs {
		if embedding, exists := repository.embeddings[postID]; exists {
			embeddings[postID] = append([]float32(nil), embedding...)
		}
	}
	return embeddings, nil
}

func (repository *serviceTestCandidateRepository) LoadHistoricalCandidates(_ context.Context, query CandidateQuery) (HistoricalCandidatePage, error) {
	page := HistoricalCandidatePage{Exhausted: true}
	if !query.SoftOnly {
		return page, nil
	}
	repository.softHistoricalQueries++
	for _, candidate := range repository.softRecent {
		served, exists := query.Served[candidate.PostID]
		if !exists || !served.Soft || served.Hard {
			continue
		}
		candidate.RecallTier = RecallTierSoftRevisit
		candidate.WasSoftServed = true
		candidate.LastServedAt = served.LastServedAt
		page.Candidates = append(page.Candidates, candidate)
		if post, exists := repository.posts[candidate.PostID]; exists {
			page.Cursor = HistoricalCursor{CreatedAt: post.CreatedAt.UTC(), PostID: candidate.PostID}
		}
	}
	page.Scanned = len(page.Candidates)
	return page, nil
}

type serviceTestProfileRepository struct {
	calls   *[]string
	loaded  ProfileLoadResult
	loadErr error
}

func (repository *serviceTestProfileRepository) Load(context.Context, ProfileLoadQuery) (ProfileLoadResult, error) {
	if repository.calls != nil {
		*repository.calls = append(*repository.calls, "profile")
	}
	return repository.loaded, repository.loadErr
}
func (repository *serviceTestProfileRepository) LoadAuthorContext(_ context.Context, _ AuthorContextQuery) (AuthorContext, error) {
	if repository.calls != nil {
		*repository.calls = append(*repository.calls, "author_context")
	}
	return AuthorContext{Affinity: map[uint]float64{}, FollowingAuthorIDs: map[uint]struct{}{}}, nil
}
func (*serviceTestProfileRepository) LoadPostAffinityInputs(context.Context, []uint) ([]PostAffinityInput, error) {
	return nil, nil
}

type serviceTestHistoryStore struct {
	mu                 sync.Mutex
	calls              *[]string
	user               ServedHistory
	guest              ServedHistory
	loadErr            error
	recordErr          error
	lastUserIDs        []uint
	lastGuestIDs       []uint
	lastUserWindow     HistoryWindow
	lastGuestWindow    HistoryWindow
	pageReceipts       map[string]struct{}
	pageRecordAttempts int
}

func (store *serviceTestHistoryStore) LoadUserHistory(_ context.Context, _ uint, window HistoryWindow) (ServedHistory, error) {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_load_user")
	}
	store.lastUserWindow = window
	return store.user, store.loadErr
}
func (store *serviceTestHistoryStore) RecordUserServed(_ context.Context, _ uint, postIDs []uint, window HistoryWindow) error {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_record_user")
	}
	store.lastUserIDs = append([]uint(nil), postIDs...)
	store.lastUserWindow = window
	return store.recordErr
}
func (store *serviceTestHistoryStore) RecordUserServedForPage(ctx context.Context, userID uint, pageKey string, postIDs []uint, window HistoryWindow) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pageRecordAttempts++
	if store.pageReceipts == nil {
		store.pageReceipts = make(map[string]struct{})
	}
	if _, exists := store.pageReceipts[pageKey]; exists {
		return nil
	}
	if err := store.RecordUserServed(ctx, userID, postIDs, window); err != nil {
		return err
	}
	store.pageReceipts[pageKey] = struct{}{}
	return nil
}
func (store *serviceTestHistoryStore) LoadGuestHistory(_ context.Context, _ string, window HistoryWindow) (ServedHistory, error) {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_load_guest")
	}
	store.lastGuestWindow = window
	return store.guest, store.loadErr
}
func (store *serviceTestHistoryStore) RecordGuestServed(_ context.Context, _ string, postIDs []uint, window HistoryWindow) error {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_record_guest")
	}
	store.lastGuestIDs = append([]uint(nil), postIDs...)
	store.lastGuestWindow = window
	return store.recordErr
}
func (store *serviceTestHistoryStore) RecordGuestServedForPage(ctx context.Context, sessionID, pageKey string, postIDs []uint, window HistoryWindow) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pageRecordAttempts++
	if store.pageReceipts == nil {
		store.pageReceipts = make(map[string]struct{})
	}
	if _, exists := store.pageReceipts[pageKey]; exists {
		return nil
	}
	if err := store.RecordGuestServed(ctx, sessionID, postIDs, window); err != nil {
		return err
	}
	store.pageReceipts[pageKey] = struct{}{}
	return nil
}

type serviceTestTraceEnqueuer struct {
	calls        *[]string
	job          TracePersistJob
	result       TraceEnqueueResult
	enqueueCalls int
}

func finalizeServiceTestResult(t testing.TB, service VerificationService, result ServeResult) {
	t.Helper()
	finalizer, ok := service.(ServingFinalizer)
	if !ok {
		t.Fatal("recommendation service does not implement ServingFinalizer")
	}
	if err := finalizer.FinalizeServing(context.Background(), result, result.Selected); err != nil {
		t.Fatal(err)
	}
}

func (enqueuer *serviceTestTraceEnqueuer) TryEnqueue(job TracePersistJob) TraceEnqueueResult {
	enqueuer.enqueueCalls++
	if enqueuer.calls != nil {
		*enqueuer.calls = append(*enqueuer.calls, "trace_enqueue")
	}
	enqueuer.job = job
	enqueuer.job.Results = append([]models.RecommendationResultTrace(nil), job.Results...)
	if enqueuer.result == "" {
		return TraceEnqueueQueued
	}
	return enqueuer.result
}

func serviceTestConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
		PrimaryWindowDays:  7,
		HistoricalBackfill: config.RecommendationHistoricalBackfillConfig{BatchSize: 200, MaxScanPerRequest: 2000},
		Candidates: config.RecommendationCandidatesConfig{
			Personalized: config.RecommendationCandidateCaps{Semantic: 20, Following: 20, Recent: 20, Trending: 20, Merged: 50},
			ColdStart:    config.RecommendationCandidateCaps{Following: 20, Recent: 20, Trending: 20, Merged: 50},
		},
		Fusion:               config.RecommendationFusionConfig{RankConstant: 60},
		Trending:             config.RecommendationTrendingConfig{MaxAgeDays: 30, HalfLifeHours: 24, ReplyFactor: 1},
		Diversity:            config.RecommendationDiversityConfig{Enabled: false},
		OutOfNetworkMinRatio: 0,
		Trace:                config.RecommendationTraceConfig{ResultRetentionDays: 30},
		Exploration:          config.RecommendationExplorationConfig{Ratio: 0},
	}
}

func newServiceTestService(t testing.TB, deps ServiceDependencies, cfg ServiceConfig) VerificationService {
	t.Helper()
	service, err := NewService(deps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testCandidate(id uint, source CandidateSource) Candidate {
	candidate := Candidate{PostID: id, SourceCount: 1}
	switch source {
	case CandidateSourceSemantic:
		candidate.FromSemantic = true
		candidate.PositiveSemanticSimilarity = .5
		candidate.SemanticRank = 1
	case CandidateSourceFollowing:
		candidate.FromFollowing = true
		candidate.FollowingRank = 1
	case CandidateSourceRecent:
		candidate.FromRecent = true
		candidate.RecentRank = 1
	case CandidateSourceTrending:
		candidate.FromTrending = true
		candidate.TrendingRank = 1
	}
	return candidate
}

func TestRecommendationServiceAuthenticatedOwnsServingSequenceAndSideEffects(t *testing.T) {
	calls := make([]string, 0, 16)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	candidates := &serviceTestCandidateRepository{
		calls:  &calls,
		recent: []Candidate{testCandidate(1, CandidateSourceRecent), testCandidate(2, CandidateSourceRecent)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 101, Content: "one"},
			2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 102, Content: "two"},
		},
	}
	profiles := &serviceTestProfileRepository{calls: &calls, loaded: ProfileLoadResult{Profile: Profile{
		ProfileStatus: ProfileStatusHit, PositiveSignalCount: 2, PersonalizedSignalCount: 2,
		PositiveVector: []float32{1, 0}, ProfileVersion: MaterializedProfileVersion,
	}}}
	history := &serviceTestHistoryStore{calls: &calls}
	traces := &serviceTestTraceEnqueuer{calls: &calls}
	deps := ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles, History: history},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1", calls: &calls},
		TraceEnqueuer:    traces,
	}
	service := newServiceTestService(t, deps, ServiceConfig{Recommendation: serviceTestConfig()})
	requestID := uuid.NewString()
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2, RequestID: requestID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizeServiceTestResult(t, service, result)
	wantCalls := []string{"version", "history_load_user", "profile", "semantic", "following", "recent", "trending", "hydrate", "author_context", "history_record_user", "trace_enqueue"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("serving calls=%v want=%v", calls, wantCalls)
	}
	if result.RequestID != requestID || result.EmbeddingVersion != "post_embedding_v1" || len(result.Selected) != 2 {
		t.Fatalf("serve result=%#v", result)
	}
	if !reflect.DeepEqual(history.lastUserIDs, []uint{1, 2}) {
		t.Fatalf("recorded history=%v want [1 2]", history.lastUserIDs)
	}
	if traces.job.Request.RequestID != requestID || traces.job.Request.CandidateCount != 2 || len(traces.job.Results) != 2 {
		t.Fatalf("enqueued request=%#v traces=%#v", traces.job.Request, traces.job.Results)
	}
}

func TestRecommendationServiceGuestOwnsPublicDiversificationFallbackAndHistory(t *testing.T) {
	calls := make([]string, 0, 16)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	history := &serviceTestHistoryStore{calls: &calls, guest: ServedHistory{
		3: {PostID: 3, LastServedAt: now.Add(-time.Hour), Hard: true},
		4: {PostID: 4, LastServedAt: now.Add(-2 * time.Hour), Soft: true},
	}}
	candidates := &serviceTestCandidateRepository{
		calls:        &calls,
		publicRecent: []Candidate{testCandidate(1, CandidateSourceRecent)},
		publicTrend:  []Candidate{testCandidate(2, CandidateSourceTrending)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 101},
			2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 102},
			4: {Model: gorm.Model{ID: 4, CreatedAt: now.Add(-3 * time.Hour)}, AuthorID: 104},
		},
	}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, History: history},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1", calls: &calls},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerGuest, GuestSessionID: "guest-session"}, Limit: 3, RequestID: "public-request", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizeServiceTestResult(t, service, result)
	if candidates.publicCalls != 2 {
		t.Fatalf("public recent/trending candidate calls=%d want two fresh source queries", candidates.publicCalls)
	}
	if candidates.softHistoricalQueries == 0 {
		t.Fatal("guest serving skipped the session history soft-tier query")
	}
	if len(candidates.hydrateQueries) == 0 {
		t.Fatal("guest candidates were not hydrated")
	}
	wantRecentCutoff, _ := config.RecommendationPrimaryWindowBounds(now, serviceTestConfig().PrimaryWindowDays)
	for _, query := range candidates.hydrateQueries {
		if len(query.Served) != 0 {
			t.Fatalf("guest hydration reapplied user served history: %+v", query.Served)
		}
		if query.ServingVersion != "post_embedding_v1" || query.Range != CandidateRangeRecent || !query.RecentCutoff.Equal(wantRecentCutoff) {
			t.Fatalf("guest hydration eligibility=%+v want serving version and recent range", query)
		}
	}
	ids := selectedPostIDs(result.Selected)
	if len(ids) < 2 || containsPostID(ids, 3) {
		t.Fatalf("public selected ids=%v should exclude hard-served post 3", ids)
	}
	if !reflect.DeepEqual(history.lastGuestIDs, ids) {
		t.Fatalf("recorded guest history=%v selected=%v", history.lastGuestIDs, ids)
	}
	if result.Viewer.Kind != ViewerGuest || result.Depleted != (len(result.Selected) < result.Limit) {
		t.Fatalf("guest result=%#v", result)
	}
}

func TestRecommendationServiceGuestDiversificationIsStablePerRequestAndVariesAcrossRequests(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	publicRecent := make([]Candidate, 80)
	posts := make(map[uint]models.Post, len(publicRecent))
	for index := range publicRecent {
		id := uint(index + 1)
		publicRecent[index] = testCandidate(id, CandidateSourceRecent)
		publicRecent[index].RecentRank = index + 1
		posts[id] = models.Post{Model: gorm.Model{ID: id, CreatedAt: now.Add(-time.Duration(index+1) * time.Minute)}, AuthorID: id + 1000}
	}
	newServing := func() VerificationService {
		return newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{Candidates: &serviceTestCandidateRepository{publicRecent: publicRecent, posts: posts}},
			ServingVersions:  serviceTestVersionProvider{version: "v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
	}
	serve := func(requestID string) []uint {
		result, err := newServing().Serve(context.Background(), ServeRequest{
			Viewer: Viewer{Kind: ViewerGuest, GuestSessionID: requestID}, Limit: 10, RequestID: requestID, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return selectedPostIDs(result.Selected)
	}
	first := serve("guest-request-a")
	repeated := serve("guest-request-a")
	second := serve("guest-request-b")
	if !reflect.DeepEqual(first, repeated) {
		t.Fatalf("same public request changed results: first=%v repeated=%v", first, repeated)
	}
	if reflect.DeepEqual(first, second) {
		t.Fatalf("different public requests reused identical diversification: %v", first)
	}
}

func TestRecommendationServiceFallbackReintroducesSoftButNotHardServedUserPosts(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	history := &serviceTestHistoryStore{user: ServedHistory{
		2: {PostID: 2, LastServedAt: now.Add(-time.Hour), Hard: true},
		3: {PostID: 3, LastServedAt: now.Add(-48 * time.Hour), Soft: true},
	}}
	candidates := &serviceTestCandidateRepository{
		recent:     []Candidate{testCandidate(1, CandidateSourceRecent)},
		softRecent: []Candidate{testCandidate(2, CandidateSourceRecent), testCandidate(3, CandidateSourceRecent)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 101},
			3: {Model: gorm.Model{ID: 3, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 103},
		},
	}
	profiles := &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles, History: history},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 8}, Limit: 2, RequestID: uuid.NewString(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := selectedPostIDs(result.Selected)
	if !reflect.DeepEqual(ids, []uint{1, 3}) {
		t.Fatalf("selected ids=%v want fresh 1 plus soft-served 3", ids)
	}
	if candidates.softHistoricalQueries == 0 || !result.Selected[1].Candidate.WasSoftServed {
		t.Fatalf("soft fallback was not marked: queries=%d selected=%#v", candidates.softHistoricalQueries, result.Selected)
	}
}

func TestRecommendationServicePreservesFailuresAndFailOpenPolicies(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	request := ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 9}, Limit: 1, RequestID: uuid.NewString(), Now: now}
	newDeps := func(candidateErr, profileErr, historyErr error) ServiceDependencies {
		return ServiceDependencies{
			DataDependencies: DataDependencies{
				Candidates: &serviceTestCandidateRepository{recent: []Candidate{testCandidate(1, CandidateSourceRecent)}, loadErr: candidateErr},
				Profiles:   &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}, loadErr: profileErr},
				History:    &serviceTestHistoryStore{loadErr: historyErr, recordErr: errors.New("write unavailable")},
			},
			ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
			TraceEnqueuer:   &serviceTestTraceEnqueuer{result: TraceEnqueueDroppedFull},
		}
	}
	for _, tc := range []struct {
		name         string
		candidateErr error
		profileErr   error
		wantErr      error
	}{
		{name: "candidate failure", candidateErr: errors.New("candidate unavailable"), wantErr: errors.New("candidate unavailable")},
		{name: "profile failure", profileErr: context.Canceled, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newServiceTestService(t, newDeps(tc.candidateErr, tc.profileErr, errors.New("history unavailable")), ServiceConfig{Recommendation: serviceTestConfig()})
			_, err := service.Serve(context.Background(), request)
			if err == nil || err.Error() != tc.wantErr.Error() {
				t.Fatalf("error=%v want=%v", err, tc.wantErr)
			}
			if tc.wantErr == context.Canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was not preserved: %v", err)
			}
		})
	}

	service := newServiceTestService(t, newDeps(nil, nil, errors.New("history unavailable")), ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), request)
	if err != nil {
		t.Fatalf("history and trace failures should be best-effort: %v", err)
	}
	if len(result.Selected) != 1 {
		t.Fatalf("fail-open result=%#v", result)
	}

	service = newServiceTestService(t, newDeps(nil, nil, context.DeadlineExceeded), ServiceConfig{Recommendation: serviceTestConfig()})
	if _, err := service.Serve(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("history deadline was swallowed: %v", err)
	}
}

func TestRecommendationServiceRequestDefaultsAndDeterministicSelection(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	candidates := &serviceTestCandidateRepository{recent: []Candidate{
		testCandidate(1, CandidateSourceRecent), testCandidate(2, CandidateSourceRecent), testCandidate(3, CandidateSourceRecent),
	}}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}}},
		ServingVersions:  serviceTestVersionProvider{version: "v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	request := ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 10}, RequestID: "stable-request", Now: now}
	first, err := service.Serve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Serve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Limit != 20 || first.RequestID != request.RequestID {
		t.Fatalf("normalized request defaults=%#v", first)
	}
	if !reflect.DeepEqual(selectedPostIDs(first.Selected), selectedPostIDs(second.Selected)) {
		t.Fatalf("selection changed for fixed request: first=%v second=%v", selectedPostIDs(first.Selected), selectedPostIDs(second.Selected))
	}
}

func containsPostID(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
