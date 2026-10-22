package recommendation

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"gorm.io/gorm"
)

type scriptedHistoricalRepository struct {
	*serviceTestCandidateRepository
	pages      []HistoricalCandidatePage
	softPage   HistoricalCandidatePage
	queries    []CandidateQuery
	pageCursor int
}

func (repository *scriptedHistoricalRepository) LoadHistoricalCandidates(_ context.Context, query CandidateQuery) (HistoricalCandidatePage, error) {
	repository.queries = append(repository.queries, query)
	if query.SoftOnly {
		return repository.softPage, nil
	}
	if repository.pageCursor >= len(repository.pages) {
		return HistoricalCandidatePage{Exhausted: true}, nil
	}
	page := repository.pages[repository.pageCursor]
	repository.pageCursor++
	return page, nil
}

func historicalTestPage(now time.Time, ids []uint, exhausted bool) HistoricalCandidatePage {
	page := HistoricalCandidatePage{Candidates: make([]Candidate, 0, len(ids)), Scanned: len(ids), Exhausted: exhausted}
	for index, id := range ids {
		createdAt := now.AddDate(0, 0, -400).Add(-time.Duration(index) * time.Second)
		page.Candidates = append(page.Candidates, Candidate{PostID: id})
		page.Cursor = HistoricalCursor{CreatedAt: createdAt, PostID: id}
	}
	return page
}

func TestRecommendationHistoricalRecallUsesKeysetBatchesAndRanksPostsWithoutVectors(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := &serviceTestCandidateRepository{posts: map[uint]models.Post{
		101: {Model: gorm.Model{ID: 101, CreatedAt: now.AddDate(0, 0, -400)}, AuthorID: 1001},
		102: {Model: gorm.Model{ID: 102, CreatedAt: now.AddDate(0, 0, -401)}, AuthorID: 1002},
		103: {Model: gorm.Model{ID: 103, CreatedAt: now.AddDate(0, 0, -402)}, AuthorID: 1003},
		104: {Model: gorm.Model{ID: 104, CreatedAt: now.AddDate(0, 0, -403)}, AuthorID: 1004},
	}}
	repository := &scriptedHistoricalRepository{
		serviceTestCandidateRepository: base,
		pages: []HistoricalCandidatePage{
			historicalTestPage(now, []uint{101, 102}, false),
			historicalTestPage(now, []uint{103, 104}, true),
		},
	}
	cfg := serviceTestConfig()
	cfg.HistoricalBackfill = config.RecommendationHistoricalBackfillConfig{BatchSize: 2, MaxScanPerRequest: 10}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: repository, Profiles: &serviceTestProfileRepository{}},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v2"},
	}, ServiceConfig{Recommendation: cfg})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 44}, Limit: 3, RequestID: "historical-batched", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer finalizeServiceTestResult(t, service, result)
	if got := selectedPostIDs(result.Selected); len(got) != 3 {
		t.Fatalf("historical selected IDs=%v, want three results", got)
	}
	for _, item := range result.Selected {
		if item.Candidate.RecallTier != RecallTierHistoricalUnseen || !item.Candidate.FromHistorical || item.Embedding != nil {
			t.Fatalf("historical candidate lost tier or required a vector: %+v", item)
		}
	}
	if len(repository.queries) != 2 || repository.queries[0].HistoricalCursor != nil ||
		repository.queries[1].HistoricalCursor == nil || repository.queries[1].HistoricalCursor.PostID != 102 {
		t.Fatalf("keyset queries=%+v, want second query after post 102", repository.queries)
	}
	if result.ContinuationStatus != ContinuationAvailable || result.Depleted {
		t.Fatalf("full historical page continuation=%q depleted=%t", result.ContinuationStatus, result.Depleted)
	}
}

func TestRecommendationHistoricalBudgetKeepsContinuationCursor(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	repository := &scriptedHistoricalRepository{
		serviceTestCandidateRepository: &serviceTestCandidateRepository{posts: map[uint]models.Post{
			201: {Model: gorm.Model{ID: 201, CreatedAt: now.AddDate(0, 0, -500)}, AuthorID: 1201},
		}},
		pages: []HistoricalCandidatePage{historicalTestPage(now, []uint{201}, false)},
	}
	cfg := serviceTestConfig()
	cfg.HistoricalBackfill = config.RecommendationHistoricalBackfillConfig{BatchSize: 1, MaxScanPerRequest: 1}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: repository, Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{PositiveSignalCount: 1}}}},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
	}, ServiceConfig{Recommendation: cfg})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 45}, Limit: 2, RequestID: "historical-budget", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer finalizeServiceTestResult(t, service, result)
	if result.ContinuationStatus != ContinuationBudgetLimited || result.Depleted || len(result.Selected) != 1 {
		t.Fatalf("budget-limited result status=%q depleted=%t selected=%v", result.ContinuationStatus, result.Depleted, selectedPostIDs(result.Selected))
	}
	if result.historicalCursor == nil || result.historicalCursor.PostID != 201 || !result.historicalBudgetLimited {
		t.Fatalf("budget-limited continuation cursor=%+v limited=%t", result.historicalCursor, result.historicalBudgetLimited)
	}
}

func TestRecommendationHistoricalUnseenPrecedesSoftRevisit(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := &serviceTestCandidateRepository{posts: map[uint]models.Post{
		301: {Model: gorm.Model{ID: 301, CreatedAt: now.AddDate(0, 0, -400)}, AuthorID: 1301},
		302: {Model: gorm.Model{ID: 302, CreatedAt: now.AddDate(0, 0, -401)}, AuthorID: 1302},
		309: {Model: gorm.Model{ID: 309, CreatedAt: now.AddDate(0, 0, -402)}, AuthorID: 1309},
	}}
	repository := &scriptedHistoricalRepository{
		serviceTestCandidateRepository: base,
		pages:                          []HistoricalCandidatePage{historicalTestPage(now, []uint{301, 302}, true)},
		softPage:                       historicalTestPage(now, []uint{309}, true),
	}
	history := &serviceTestHistoryStore{user: ServedHistory{309: {PostID: 309, Soft: true, LastServedAt: now.Add(-48 * time.Hour)}}}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: repository, Profiles: &serviceTestProfileRepository{}, History: history},
		ServingVersions:  serviceTestVersionProvider{version: "post_embedding_v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 46}, Limit: 3, RequestID: "historical-before-soft", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer finalizeServiceTestResult(t, service, result)
	if !containsPostID(selectedPostIDs(result.Selected), 301) || !containsPostID(selectedPostIDs(result.Selected), 302) || !containsPostID(selectedPostIDs(result.Selected), 309) {
		t.Fatalf("historical plus soft selected=%v", selectedPostIDs(result.Selected))
	}
	if result.Selected[len(result.Selected)-1].Candidate.RecallTier != RecallTierSoftRevisit || !result.Selected[len(result.Selected)-1].Candidate.WasSoftServed {
		t.Fatalf("soft candidate did not remain last-tier: %+v", result.Selected)
	}
	if len(repository.queries) != 2 || !repository.queries[1].SoftOnly || repository.queries[1].Range != CandidateRangeAny {
		t.Fatalf("historical/soft queries=%+v", repository.queries)
	}
}
