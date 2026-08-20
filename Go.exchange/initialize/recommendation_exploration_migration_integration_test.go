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

	user := models.User{Username: "recommendation-exploration-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Article{AuthorID: user.ID, Title: "exploration migration", Content: "body", Preview: "body", PublicationState: "published"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	request := models.RecommendationRequest{
		RequestID: uuid.NewString(), UserID: user.ID, Scene: "recommendation_page", StrategyID: "exploration-migration",
		RankerVersion: "rules_v4", RankerConfigHash: "hash", RequestedLimit: 2, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id = ?", request.RequestID).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	invalidTrace := db.Exec(`
INSERT INTO recommendation_result_traces (request_id, position, article_id, author_id, selection_mode, exploration_reason, exploration_semantic, created_at, expires_at)
VALUES (?, 1, ?, ?, 'exploration', 'recent', 0, ?, ?)
`, request.RequestID, article.ID, user.ID, time.Now().UTC(), time.Now().UTC().Add(time.Hour))
	if invalidTrace.Error == nil {
		t.Fatal("invalid trace provenance write was accepted")
	}
	invalidRequest := request
	invalidRequest.RequestID = uuid.NewString()
	invalidRequest.ExplorationTargetCount = 3
	if err := db.Create(&invalidRequest).Error; err == nil {
		t.Fatal("invalid request exploration count write was accepted")
	}
	invalidMetric := db.Exec(`
INSERT INTO recommendation_daily_metrics (metric_date, scene, ranker_version, ranker_config_hash, strategy_id, exploration_opportunity, selection_mode, exploration_reason, position, article_id, updated_at)
VALUES (CURRENT_DATE, 'recommendation_page', 'rules_v4', 'hash-invalid', 'exploration-migration', FALSE, 'invalid', '', 1, ?, ?)
`, article.ID, time.Now().UTC())
	if invalidMetric.Error == nil {
		t.Fatal("invalid metric selection mode write was accepted")
	}
}
