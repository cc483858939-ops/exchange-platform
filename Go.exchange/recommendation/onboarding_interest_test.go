package recommendation

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type onboardingInterestTestRepository struct {
	slugs []string
	err   error
	calls int
}

func (repository *onboardingInterestTestRepository) LoadOnboardingInterestSlugs(_ context.Context, _ uint, enabled []string) ([]string, error) {
	repository.calls++
	if len(enabled) == 0 {
		return nil, errors.New("expected enabled topics")
	}
	return append([]string(nil), repository.slugs...), repository.err
}

func TestOnboardingInterestStrength(t *testing.T) {
	tests := []struct {
		signals int
		want    float64
	}{
		{-2, 1}, {0, 1}, {1, .875}, {2, .75}, {4, .5}, {7, .125}, {8, 0}, {20, 0},
	}
	for _, test := range tests {
		got := onboardingInterestStrength(test.signals)
		if got != test.want || math.IsNaN(got) || math.IsInf(got, 0) || got < 0 {
			t.Errorf("onboardingInterestStrength(%d)=%v want=%v", test.signals, got, test.want)
		}
	}
}

func TestOnboardingSourceKeysUsesOnlyEnabledTopicsAndDeduplicates(t *testing.T) {
	sources := map[string][]string{
		"ai":       {"OpenAI", "Anthropic"},
		"space":    {"NASA", "SpaceX"},
		"disabled": {"ignored"},
	}
	got := onboardingSourceKeys([]string{"space", "ai", "disabled", "unknown"}, map[string][]string{
		"ai": sources["ai"], "space": sources["space"],
	})
	want := []string{"Anthropic", "NASA", "OpenAI", "SpaceX"}
	if !equalStrings(got, want) {
		t.Fatalf("source keys=%v want=%v", got, want)
	}
	if got := onboardingSourceKeys(nil, sources); len(got) != 0 {
		t.Fatalf("empty interests produced source keys=%v", got)
	}
}

func TestOnboardingInterestCandidateBudget(t *testing.T) {
	for _, test := range []struct{ limit, want int }{{200, 40}, {150, 37}, {20, 5}, {3, 0}, {0, 0}} {
		if got := onboardingInterestCandidateBudget(test.limit); got != test.want {
			t.Errorf("budget(%d)=%d want=%d", test.limit, got, test.want)
		}
	}
}

func TestMergeRecentInterestCandidatesIsBoundedAndDeduplicates(t *testing.T) {
	got := mergeRecentInterestCandidates(
		[]Candidate{{PostID: 1, FromRecent: true}, {PostID: 2, FromRecent: true}, {PostID: 3, FromRecent: true}, {PostID: 4, FromRecent: true}},
		[]Candidate{{PostID: 2}, {PostID: 5}}, 4,
	)
	if len(got) != 4 || got[0].PostID != 2 || !got[0].FromOnboardingInterest || got[1].PostID != 5 {
		t.Fatalf("merged recent candidates=%#v", got)
	}
	assertUniqueRecommendationPostIDs(t, got)
}

func TestOnboardingInterestMarkerDoesNotCreateAnotherRRFSource(t *testing.T) {
	fused := FuseCandidates(10, FusionConfig{RankConstant: 60},
		CandidateSet{Source: CandidateSourceSemantic, Candidates: []Candidate{{PostID: 7}}},
		CandidateSet{Source: CandidateSourceRecent, Candidates: []Candidate{{PostID: 7, FromOnboardingInterest: true}}},
	)
	if len(fused) != 1 || !fused[0].FromRecent || !fused[0].FromOnboardingInterest || fused[0].SourceCount != 2 {
		t.Fatalf("fused interest candidate=%#v; interest must remain inside Recent", fused)
	}
}

func TestOnboardingInterestBoostDecaysAndIsAppliedOnce(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	base := []RankedCandidate{
		{Candidate: Candidate{PostID: 1, FromOnboardingInterest: true}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 10}},
		{Candidate: Candidate{PostID: 2}, Post: models.Post{Model: gorm.Model{ID: 2, CreatedAt: now}, AuthorID: 20}},
	}
	cfg := RankingConfig{SemanticWeight: 1}
	for _, test := range []struct {
		signals int
		want    float64
	}{
		{0, .4}, {2, .3}, {4, .2}, {7, .05}, {8, 0},
	} {
		ranked := RankCandidates(ProfileFeatures{OnboardingInterestStrength: onboardingInterestStrength(test.signals)}, append([]RankedCandidate(nil), base...), now, cfg)
		byID := map[uint]RankedCandidate{ranked[0].Post.ID: ranked[0], ranked[1].Post.ID: ranked[1]}
		if math.Abs(byID[1].Breakdown.OnboardingInterestComponent-test.want) > 1e-12 {
			t.Errorf("signals=%d interest component=%v want=%v", test.signals, byID[1].Breakdown.OnboardingInterestComponent, test.want)
		}
		if byID[2].Breakdown.OnboardingInterestComponent != 0 || byID[2].Breakdown.BaseScore != 0 {
			t.Errorf("non-matching candidate received interest score: %+v", byID[2].Breakdown)
		}
		if byID[1].Breakdown.BaseScore != byID[1].Breakdown.SemanticComponent+byID[1].Breakdown.OnboardingInterestComponent {
			t.Errorf("base score does not include the stored interest component: %+v", byID[1].Breakdown)
		}
	}
}

func TestBuildResultTracePersistsOnboardingInterestScore(t *testing.T) {
	request := models.RecommendationRequest{RequestID: "request", CreatedAt: time.Now().UTC()}
	selected := []SelectedCandidate{{
		Candidate: Candidate{PostID: 12, FromRecent: true, SourceCount: 1},
		Post:      models.Post{Model: gorm.Model{ID: 12}, AuthorID: 9},
		Breakdown: ScoreBreakdown{SemanticComponent: .7, OnboardingInterestComponent: .3, BaseScore: 1, FinalScore: .8, DiversityPenalty: .2},
	}}
	traces := buildResultTraces(request, selected, request.CreatedAt, config.RecommendationConfig{Trace: config.RecommendationTraceConfig{ResultRetentionDays: 30}})
	if len(traces) != 1 || traces[0].OnboardingInterestComponent != .3 || traces[0].SemanticComponent != .7 || traces[0].BaseScore != 1 || traces[0].FinalScore != .8 {
		t.Fatalf("persisted recommendation score trace=%#v", traces)
	}
}

func TestOnboardingInterestLoadedOnlyForEligibleAuthenticatedColdStarts(t *testing.T) {
	t.Run("empty interest set leaves recent recall unmodified", func(t *testing.T) {
		interestRepo := &onboardingInterestTestRepository{}
		candidateRepo := &serviceTestCandidateRepository{
			recent: []Candidate{{PostID: 1, FromRecent: true}},
			posts:  map[uint]models.Post{1: {Model: gorm.Model{ID: 1}, AuthorID: 10}},
		}
		service := newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{Candidates: candidateRepo, Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{}}}, OnboardingInterests: interestRepo},
			ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		result, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if interestRepo.calls != 1 || len(candidateRepo.recentQueries) != 1 || len(candidateRepo.recentQueries[0].OnboardingInterestSourceKeys) != 0 {
			t.Fatalf("empty interest path calls=%d queries=%+v", interestRepo.calls, candidateRepo.recentQueries)
		}
		if result.OnboardingInterestActive || result.Selected[0].Breakdown.OnboardingInterestComponent != 0 {
			t.Fatalf("empty interest set changed recommendation: active=%v selected=%+v", result.OnboardingInterestActive, result.Selected)
		}
	})

	t.Run("cold start loads a single interest set and passes bounded source keys", func(t *testing.T) {
		interestRepo := &onboardingInterestTestRepository{slugs: []string{"ai", "space"}}
		candidateRepo := &serviceTestCandidateRepository{
			recent: []Candidate{{PostID: 1, FromRecent: true, FromOnboardingInterest: true}, {PostID: 2, FromRecent: true}},
			posts: map[uint]models.Post{
				1: {Model: gorm.Model{ID: 1, CreatedAt: time.Now().Add(-time.Minute)}, AuthorID: 10},
				2: {Model: gorm.Model{ID: 2, CreatedAt: time.Now().Add(-time.Hour)}, AuthorID: 11},
			},
		}
		profileRepo := &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{PersonalizedSignalCount: 2, ProfileStatus: ProfileStatusMiss}}}
		service := newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{Candidates: candidateRepo, Profiles: profileRepo, OnboardingInterests: interestRepo},
			ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		result, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if interestRepo.calls != 1 || len(candidateRepo.recentQueries) != 1 {
			t.Fatalf("interest reads=%d recent calls=%d", interestRepo.calls, len(candidateRepo.recentQueries))
		}
		query := candidateRepo.recentQueries[0]
		if query.OnboardingInterestLimit != 5 || len(query.OnboardingInterestSourceKeys) != 5 {
			t.Fatalf("bounded interest recent query=%+v", query)
		}
		if !result.OnboardingInterestActive || result.OnboardingInterestCandidateCount != 1 {
			t.Fatalf("interest trace summary active=%v candidates=%d", result.OnboardingInterestActive, result.OnboardingInterestCandidateCount)
		}
		if math.Abs(result.Selected[0].Breakdown.OnboardingInterestComponent-.3) > 1e-12 {
			t.Fatalf("signal-decayed interest boost=%v want=.3", result.Selected[0].Breakdown.OnboardingInterestComponent)
		}
	})

	t.Run("mature user does not read interests", func(t *testing.T) {
		interestRepo := &onboardingInterestTestRepository{slugs: []string{"ai"}}
		service := newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{
				Candidates: &serviceTestCandidateRepository{}, Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{PersonalizedSignalCount: 8}}},
				OnboardingInterests: interestRepo,
			}, ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		if _, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1}); err != nil {
			t.Fatal(err)
		}
		if interestRepo.calls != 0 {
			t.Fatalf("mature-user onboarding-interest reads=%d want=0", interestRepo.calls)
		}
	})

	t.Run("visitor does not read interests", func(t *testing.T) {
		interestRepo := &onboardingInterestTestRepository{slugs: []string{"ai"}}
		service := newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{Candidates: &serviceTestCandidateRepository{publicRecent: []Candidate{testCandidate(1, CandidateSourceRecent)}, posts: map[uint]models.Post{1: {Model: gorm.Model{ID: 1}, AuthorID: 10}}}, OnboardingInterests: interestRepo},
			ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		if _, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerGuest}, Limit: 1}); err != nil {
			t.Fatal(err)
		}
		if interestRepo.calls != 0 {
			t.Fatalf("visitor onboarding-interest reads=%d want=0", interestRepo.calls)
		}
	})

	t.Run("interest database failure is returned", func(t *testing.T) {
		wantErr := errors.New("interest database unavailable")
		service := newServiceTestService(t, ServiceDependencies{
			DataDependencies: DataDependencies{
				Candidates: &serviceTestCandidateRepository{}, Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{PersonalizedSignalCount: 0}}},
				OnboardingInterests: &onboardingInterestTestRepository{err: wantErr},
			}, ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		_, err := service.Serve(context.Background(), ServeRequest{Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Serve error=%v want underlying interest read failure", err)
		}
	})
}

func TestRecommendationServiceRejectsMissingOnboardingTopicConfig(t *testing.T) {
	const childModeEnv = "GO_TEST_ONBOARDING_TOPIC_CONFIG_MISSING_CHILD"
	if os.Getenv(childModeEnv) == "1" {
		_, err := NewService(ServiceDependencies{
			DataDependencies: DataDependencies{
				Candidates:          &serviceTestCandidateRepository{},
				OnboardingInterests: &onboardingInterestTestRepository{},
			},
			ServingVersions: serviceTestVersionProvider{version: "post_embedding_v1"},
		}, ServiceConfig{Recommendation: serviceTestConfig()})
		if err == nil || !strings.Contains(err.Error(), "curated topics") {
			t.Fatalf("NewService error=%v, want explicit curated topics configuration error", err)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRecommendationServiceRejectsMissingOnboardingTopicConfig$")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), childModeEnv+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated missing-config child test failed: %v\n%s", err, output)
	}
}

func equalStrings(left, right []string) bool {
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
