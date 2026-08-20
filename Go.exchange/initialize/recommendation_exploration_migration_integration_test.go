package initialize

import (
	"os"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationExplorationSchemaMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.RecommendationRequest{}, &models.RecommendationResultTrace{}, &models.RecommendationDailyMetric{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS freshness_component DOUBLE PRECISION NOT NULL DEFAULT 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationExplorationSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationExplorationSchema(db); err != nil {
		t.Fatal(err)
	}

	var freshnessColumns, explorationTraceColumns, explorationRequestColumns, explorationMetricColumns int
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'recommendation_result_traces' AND column_name = 'freshness_component'`).Scan(&freshnessColumns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'recommendation_result_traces' AND column_name IN ('exploration_opportunity', 'selection_mode', 'exploration_reason', 'exploration_semantic')`).Scan(&explorationTraceColumns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'recommendation_requests' AND column_name IN ('exploration_target_count', 'exploration_opportunity_count', 'exploration_result_count')`).Scan(&explorationRequestColumns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'recommendation_daily_metrics' AND column_name IN ('exploration_opportunity', 'selection_mode', 'exploration_reason')`).Scan(&explorationMetricColumns).Error; err != nil {
		t.Fatal(err)
	}
	if freshnessColumns != 0 || explorationTraceColumns != 4 || explorationRequestColumns != 3 || explorationMetricColumns != 3 {
		t.Fatalf("freshness=%d trace=%d request=%d metric=%d", freshnessColumns, explorationTraceColumns, explorationRequestColumns, explorationMetricColumns)
	}

	var primaryKeyColumns string
	if err := db.Raw(`
SELECT string_agg(a.attname, ',' ORDER BY keys.ordinality)
FROM pg_constraint c
JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS keys(attnum, ordinality) ON TRUE
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = keys.attnum
WHERE c.conrelid = 'recommendation_daily_metrics'::regclass AND c.contype = 'p'
`).Scan(&primaryKeyColumns).Error; err != nil {
		t.Fatal(err)
	}
	wantPrimaryKey := "metric_date,scene,ranker_version,ranker_config_hash,strategy_id,exploration_opportunity,selection_mode,exploration_reason,position,article_id"
	if primaryKeyColumns != wantPrimaryKey {
		t.Fatalf("daily metric primary key=%q want=%q", primaryKeyColumns, wantPrimaryKey)
	}

	strategyID := "exploration-migration-" + uuid.NewString()
	user := models.User{Username: "recommendation-exploration-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	articles := []models.Article{
		{AuthorID: user.ID, Title: "exploration migration one", Content: "body", Preview: "body", PublicationState: "published"},
		{AuthorID: user.ID, Title: "exploration migration two", Content: "body", Preview: "body", PublicationState: "published"},
		{AuthorID: user.ID, Title: "exploration migration three", Content: "body", Preview: "body", PublicationState: "published"},
	}
	for index := range articles {
		if err := db.Create(&articles[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := models.RecommendationRequest{
		RequestID: uuid.NewString(), UserID: user.ID, Scene: "recommendation_page", StrategyID: strategyID,
		RankerVersion: "rules_v4", RankerConfigHash: "hash", RequestedLimit: 20, ResultCount: 3,
		ExplorationTargetCount: 2, ExplorationOpportunityCount: 2, ExplorationResultCount: 1, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("strategy_id = ?", strategyID).Delete(&models.RecommendationDailyMetric{})
		db.Unscoped().Where("author_id = ?", user.ID).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	traceStates := []struct {
		position    int
		articleID   uint
		opportunity bool
		mode        string
		reason      string
		semantic    float64
	}{
		{1, articles[0].ID, false, "ranked", "", 0},
		{2, articles[1].ID, true, "ranked", "", 0},
		{3, articles[2].ID, true, "exploration", "recent", .5},
	}
	for _, state := range traceStates {
		trace := models.RecommendationResultTrace{
			RequestID: request.RequestID, Position: state.position, ArticleID: state.articleID, AuthorID: user.ID,
			ExplorationOpportunity: state.opportunity, SelectionMode: state.mode, ExplorationReason: state.reason,
			ExplorationSemantic: state.semantic, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
		}
		if err := db.Create(&trace).Error; err != nil {
			t.Fatalf("valid trace state %#v rejected: %v", state, err)
		}
	}

	invalidTraceStates := []struct {
		name        string
		opportunity bool
		mode        string
		reason      string
		semantic    float64
	}{
		{"false exploration reason", false, "exploration", "recent", 0},
		{"ranked reason", true, "ranked", "recent", 0},
		{"exploration empty reason", true, "exploration", "", .5},
		{"unsupported reason", true, "exploration", "unsupported", .5},
		{"ranked semantic", true, "ranked", "", .5},
		{"negative semantic", true, "exploration", "recent", -.1},
		{"over one semantic", true, "exploration", "recent", 1.1},
	}
	for index, state := range invalidTraceStates {
		result := db.Exec(`
INSERT INTO recommendation_result_traces (request_id, position, article_id, author_id, exploration_opportunity, selection_mode, exploration_reason, exploration_semantic, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, request.RequestID, 10+index, articles[0].ID, user.ID, state.opportunity, state.mode, state.reason, state.semantic, time.Now().UTC(), time.Now().UTC().Add(time.Hour))
		if result.Error == nil {
			t.Fatalf("invalid trace state %q was accepted", state.name)
		}
	}

	invalidRequests := []struct {
		name   string
		mutate func(*models.RecommendationRequest)
	}{
		{"target negative", func(value *models.RecommendationRequest) { value.ExplorationTargetCount = -1 }},
		{"target exceeds limit", func(value *models.RecommendationRequest) { value.ExplorationTargetCount = value.RequestedLimit + 1 }},
		{"opportunity negative", func(value *models.RecommendationRequest) { value.ExplorationOpportunityCount = -1 }},
		{"opportunity exceeds target", func(value *models.RecommendationRequest) {
			value.ExplorationOpportunityCount = value.ExplorationTargetCount + 1
		}},
		{"result negative", func(value *models.RecommendationRequest) { value.ExplorationResultCount = -1 }},
		{"result exceeds opportunity", func(value *models.RecommendationRequest) {
			value.ExplorationResultCount = value.ExplorationOpportunityCount + 1
		}},
		{"result exceeds result count", func(value *models.RecommendationRequest) { value.ResultCount = 0 }},
	}
	for _, invalid := range invalidRequests {
		value := request
		value.RequestID = uuid.NewString()
		invalid.mutate(&value)
		if err := db.Create(&value).Error; err == nil {
			t.Fatalf("invalid request state %q was accepted", invalid.name)
		}
	}

	metricDate := time.Now().UTC()
	metricStates := []struct {
		opportunity bool
		mode        string
		reason      string
	}{
		{false, "ranked", ""},
		{true, "ranked", ""},
		{true, "exploration", "recent"},
	}
	for _, state := range metricStates {
		metric := models.RecommendationDailyMetric{
			MetricDate: metricDate, Scene: request.Scene, RankerVersion: request.RankerVersion,
			RankerConfigHash: request.RankerConfigHash, StrategyID: strategyID,
			ExplorationOpportunity: state.opportunity, SelectionMode: state.mode, ExplorationReason: state.reason,
			Position: 1, ArticleID: articles[0].ID, UpdatedAt: metricDate,
		}
		if err := db.Create(&metric).Error; err != nil {
			t.Fatalf("valid metric state %#v rejected: %v", state, err)
		}
	}
	var metricRows int64
	if err := db.Model(&models.RecommendationDailyMetric{}).Where("strategy_id = ?", strategyID).Count(&metricRows).Error; err != nil {
		t.Fatal(err)
	}
	if metricRows != 3 {
		t.Fatalf("metric rows=%d want=3 distinct provenance rows", metricRows)
	}
	invalidMetricStates := []struct {
		name        string
		opportunity bool
		mode        string
		reason      string
	}{
		{"false exploration reason", false, "exploration", "recent"},
		{"ranked reason", true, "ranked", "recent"},
		{"exploration empty reason", true, "exploration", ""},
		{"unsupported reason", true, "exploration", "unsupported"},
	}
	for index, state := range invalidMetricStates {
		result := db.Exec(`
INSERT INTO recommendation_daily_metrics (metric_date, scene, ranker_version, ranker_config_hash, strategy_id, exploration_opportunity, selection_mode, exploration_reason, position, article_id, updated_at)
VALUES (CURRENT_DATE, ?, 'rules_v4', ?, ?, ?, ?, ?, ?, ?, ?)
`, request.Scene, "hash-invalid-"+uuid.NewString(), strategyID, state.opportunity, state.mode, state.reason, 10+index, articles[0].ID, metricDate)
		if result.Error == nil {
			t.Fatalf("invalid metric state %q was accepted", state.name)
		}
	}
}
