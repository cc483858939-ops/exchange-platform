package initialize

import (
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationTraceMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Post{},
		&models.RecommendationRequest{},
		&models.RecommendationResultTrace{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS popular_candidate_count INTEGER NOT NULL DEFAULT 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_popular_candidates").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_popular_candidates CHECK (popular_candidate_count >= 0)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS from_popular BOOLEAN NOT NULL DEFAULT FALSE").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS popularity_component DOUBLE PRECISION NOT NULL DEFAULT 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationTrendingSchemaCleanup(db); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationTrendingSchemaCleanup(db); err != nil {
		t.Fatal(err)
	}
	var legacyColumnCount, newColumnCount int
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'recommendation_requests'
  AND column_name = 'popular_candidate_count'`).Scan(&legacyColumnCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'recommendation_requests'
  AND column_name = 'trending_candidate_count'`).Scan(&newColumnCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyColumnCount != 0 || newColumnCount != 1 {
		t.Fatalf("request columns legacy=%d new=%d", legacyColumnCount, newColumnCount)
	}
	var legacyRequestConstraintCount, newRequestConstraintCount int
	if err := db.Raw(`
SELECT COUNT(*)
FROM pg_constraint
WHERE conrelid = 'recommendation_requests'::regclass
  AND conname = 'chk_recommendation_request_popular_candidates'`).Scan(&legacyRequestConstraintCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`
SELECT COUNT(*)
FROM pg_constraint
WHERE conrelid = 'recommendation_requests'::regclass
  AND conname = 'chk_recommendation_request_trending_candidates'`).Scan(&newRequestConstraintCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyRequestConstraintCount != 0 || newRequestConstraintCount != 1 {
		t.Fatalf("request constraints legacy=%d new=%d", legacyRequestConstraintCount, newRequestConstraintCount)
	}
	var legacyTraceColumns, newTraceColumns int
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'recommendation_result_traces'
  AND column_name IN ('from_popular', 'popularity_component')`).Scan(&legacyTraceColumns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'recommendation_result_traces'
  AND column_name IN ('from_trending', 'trending_component')`).Scan(&newTraceColumns).Error; err != nil {
		t.Fatal(err)
	}
	if legacyTraceColumns != 0 || newTraceColumns != 2 {
		t.Fatalf("trace columns legacy=%d new=%d", legacyTraceColumns, newTraceColumns)
	}
	if err := applyRecommendationRetrievalV3Indexes(db); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationRetrievalV3Indexes(db); err != nil {
		t.Fatal(err)
	}
	indexRows, err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'posts'
  AND indexname IN (?, ?)
`, "idx_posts_recommendation_popular", "idx_posts_recommendation_trending").Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer indexRows.Close()
	postIndexes := map[string]string{}
	for indexRows.Next() {
		var name, definition string
		if err := indexRows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		postIndexes[name] = strings.ToLower(strings.Join(strings.Fields(definition), ""))
	}
	if err := indexRows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, exists := postIndexes["idx_posts_recommendation_popular"]; exists {
		t.Fatal("legacy popular retrieval index still exists")
	}
	trendingIndex, exists := postIndexes["idx_posts_recommendation_trending"]
	if !exists || !strings.Contains(trendingIndex, "published_at") || !strings.Contains(trendingIndex, "like_count>0") || !strings.Contains(trendingIndex, "comment_count>0") {
		t.Fatalf("trending index=%q", trendingIndex)
	}

	const indexName = "uidx_recommendation_result_trace_request_article"
	if err := db.Exec("DROP INDEX IF EXISTS " + indexName).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX " + indexName + " ON recommendation_result_traces (post_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationTraceConstraints(db); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationTraceConstraints(db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Raw("SELECT indexname, indexdef\nFROM pg_indexes\nWHERE schemaname = current_schema()\n  AND tablename = 'recommendation_result_traces'\n  AND indexname = ?", indexName).Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	indexByName := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		indexByName[name] = strings.ToLower(strings.Join(strings.Fields(definition), ""))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	indexDefinition := indexByName[indexName]
	if !strings.Contains(indexDefinition, "unique") ||
		!strings.Contains(indexDefinition, "(request_id,post_id)") ||
		strings.Contains(indexDefinition, "(post_id)") {
		t.Fatalf("index definition=%q", indexDefinition)
	}

	user := models.User{
		Username: "recommendation-trace-migration-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	postX := models.Post{
		AuthorID: user.ID, Content: "trace migration x", Visibility: "public",
	}
	postY := models.Post{
		AuthorID: user.ID, Content: "trace migration y", Visibility: "public",
	}
	if err := db.Create(&postX).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&postY).Error; err != nil {
		t.Fatal(err)
	}

	requestA := models.RecommendationRequest{
		RequestID:        uuid.NewString(),
		UserID:           user.ID,
		Scene:            "for_you",
		StrategyID:       "trace-migration",
		RankerVersion:    "test",
		RankerConfigHash: "test",
		RequestedLimit:   2,
		CreatedAt:        time.Now().UTC(),
	}
	requestB := requestA
	requestB.RequestID = uuid.NewString()
	t.Cleanup(func() {
		requestIDs := []string{requestA.RequestID, requestB.RequestID}
		postIDs := []uint{postX.ID, postY.ID}
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	if err := db.Create(&[]models.RecommendationRequest{requestA, requestB}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	traceAPostX := models.RecommendationResultTrace{
		RequestID: requestA.RequestID,
		Position:  1,
		PostID:    postX.ID,
		AuthorID:  user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	traceBPostX := traceAPostX
	traceBPostX.RequestID = requestB.RequestID
	if err := db.Create(&traceAPostX).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&traceBPostX).Error; err != nil {
		t.Fatalf("database rejected same article in another request: %v", err)
	}

	duplicateArticle := traceAPostX
	duplicateArticle.Position = 2
	if err := db.Create(&duplicateArticle).Error; err == nil {
		t.Fatal("database accepted duplicate article in one request")
	}

	duplicatePosition := traceAPostX
	duplicatePosition.PostID = postY.ID
	if err := db.Create(&duplicatePosition).Error; err == nil {
		t.Fatal("database accepted duplicate request position")
	}
}
