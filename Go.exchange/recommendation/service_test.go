package recommendation

import (
	"context"
	"errors"
	"reflect"
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
	calls        *[]string
	semantic     []Candidate
	following    []Candidate
	recent       []Candidate
	trending     []Candidate
	publicRecent []Candidate
	publicTrend  []Candidate
	softRecent   []Candidate
	posts        map[uint]models.Post
	loadErr      error
	hydrateErr   error
	publicCalls  int
	softQueries  int
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
func (repository *serviceTestCandidateRepository) HydrateCandidates(_ context.Context, _ string, candidates []Candidate, _ time.Time) ([]RankedCandidate, error) {
	repository.addCall("hydrate")
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
func (*serviceTestCandidateRepository) LoadPostEmbeddings(context.Context, []uint, string) (map[uint][]float32, error) {
	return map[uint][]float32{}, nil
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
	calls        *[]string
	user         ServedHistory
	guest        ServedHistory
	loadErr      error
	recordErr    error
	lastUserIDs  []uint
	lastGuestIDs []uint
}

func (store *serviceTestHistoryStore) LoadUserHistory(context.Context, uint, HistoryWindow) (ServedHistory, error) {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_load_user")
	}
	return store.user, store.loadErr
}
func (store *serviceTestHistoryStore) RecordUserServed(_ context.Context, _ uint, postIDs []uint, _ HistoryWindow) error {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_record_user")
	}
	store.lastUserIDs = append([]uint(nil), postIDs...)
	return store.recordErr
}
func (store *serviceTestHistoryStore) LoadGuestHistory(context.Context, string, HistoryWindow) (ServedHistory, error) {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_load_guest")
	}
	return store.guest, store.loadErr
}
func (store *serviceTestHistoryStore) RecordGuestServed(_ context.Context, _ string, postIDs []uint, _ HistoryWindow) error {
	if store.calls != nil {
		*store.calls = append(*store.calls, "history_record_guest")
	}
	store.lastGuestIDs = append([]uint(nil), postIDs...)
	return store.recordErr
}

type serviceTestTraceRepository struct {
	calls   *[]string
	request models.RecommendationRequest
	results []models.RecommendationResultTrace
	err     error
}

func (repository *serviceTestTraceRepository) PersistServing(_ context.Context, request models.RecommendationRequest, results []models.RecommendationResultTrace) error {
	if repository.calls != nil {
		*repository.calls = append(*repository.calls, "trace")
	}
	repository.request = request
	repository.results = append([]models.RecommendationResultTrace(nil), results...)
	return repository.err
}

func serviceTestConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
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

func newServiceTestService(t *testing.T, deps ServiceDependencies, cfg ServiceConfig) VerificationService {
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
	traces := &serviceTestTraceRepository{calls: &calls}
	deps := ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles, History: history, Traces: traces},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1", calls: &calls},
	}
	service := newServiceTestService(t, deps, ServiceConfig{Recommendation: serviceTestConfig()})
	requestID := uuid.NewString()
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2, RequestID: requestID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"version", "history_load_user", "profile", "semantic", "following", "recent", "trending", "hydrate", "author_context", "history_record_user", "trace"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("serving calls=%v want=%v", calls, wantCalls)
	}
	if result.RequestID != requestID || result.EmbeddingVersion != "post_embedding_v1" || len(result.Selected) != 2 {
		t.Fatalf("serve result=%#v", result)
	}
	if !reflect.DeepEqual(history.lastUserIDs, []uint{1, 2}) {
		t.Fatalf("recorded history=%v want [1 2]", history.lastUserIDs)
	}
	if traces.request.RequestID != requestID || traces.request.CandidateCount != 2 || len(traces.results) != 2 {
		t.Fatalf("persisted request=%#v traces=%#v", traces.request, traces.results)
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
	if candidates.publicCalls < 4 {
		t.Fatalf("public candidate calls=%d want fresh and fallback source calls", candidates.publicCalls)
	}
	if candidates.softQueries != 0 {
		t.Fatalf("guest serving unexpectedly used authenticated soft query count=%d", candidates.softQueries)
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
			Viewer: Viewer{Kind: ViewerGuest}, Limit: 10, RequestID: requestID, Now: now,
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
	if candidates.softQueries == 0 || !result.Selected[1].Candidate.WasSoftServed {
		t.Fatalf("soft fallback was not marked: queries=%d selected=%#v", candidates.softQueries, result.Selected)
	}
}

func TestRecommendationServicePreservesFailuresAndFailOpenPolicies(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	request := ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 9}, Limit: 1, RequestID: uuid.NewString(), Now: now}
	newDeps := func(candidateErr, profileErr, historyErr, traceErr error) ServiceDependencies {
		return ServiceDependencies{
			DataDependencies: DataDependencies{
				Candidates: &serviceTestCandidateRepository{recent: []Candidate{testCandidate(1, CandidateSourceRecent)}, loadErr: candidateErr},
				Profiles:   &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}, loadErr: profileErr},
				History:    &serviceTestHistoryStore{loadErr: historyErr, recordErr: errors.New("write unavailable")},
				Traces:     &serviceTestTraceRepository{err: traceErr},
			},
			ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
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
			service := newServiceTestService(t, newDeps(tc.candidateErr, tc.profileErr, errors.New("history unavailable"), nil), ServiceConfig{Recommendation: serviceTestConfig()})
			_, err := service.Serve(context.Background(), request)
			if err == nil || err.Error() != tc.wantErr.Error() {
				t.Fatalf("error=%v want=%v", err, tc.wantErr)
			}
			if tc.wantErr == context.Canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was not preserved: %v", err)
			}
		})
	}

	service := newServiceTestService(t, newDeps(nil, nil, errors.New("history unavailable"), errors.New("trace unavailable")), ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), request)
	if err != nil {
		t.Fatalf("history and trace failures should be best-effort: %v", err)
	}
	if len(result.Selected) != 1 {
		t.Fatalf("fail-open result=%#v", result)
	}

	service = newServiceTestService(t, newDeps(nil, nil, context.DeadlineExceeded, nil), ServiceConfig{Recommendation: serviceTestConfig()})
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
