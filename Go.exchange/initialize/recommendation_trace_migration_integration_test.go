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
		&models.Article{},
		&models.RecommendationRequest{},
		&models.RecommendationResultTrace{},
	); err != nil {
		t.Fatal(err)
	}

	const indexName = "uidx_recommendation_result_trace_request_article"
	if err := db.Exec("DROP INDEX IF EXISTS " + indexName).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX " + indexName + " ON recommendation_result_traces (article_id)").Error; err != nil {
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
		!strings.Contains(indexDefinition, "(request_id,article_id)") ||
		strings.Contains(indexDefinition, "(article_id)") {
		t.Fatalf("index definition=%q", indexDefinition)
	}

	user := models.User{
		Username: "recommendation-trace-migration-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	articleX := models.Article{
		AuthorID:         user.ID,
		Title:            "trace migration x",
		Content:          "body",
		Preview:          "body",
		PublicationState: "published",
	}
	articleY := models.Article{
		AuthorID:         user.ID,
		Title:            "trace migration y",
		Content:          "body",
		Preview:          "body",
		PublicationState: "published",
	}
	if err := db.Create(&articleX).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&articleY).Error; err != nil {
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
		articleIDs := []uint{articleX.ID, articleY.ID}
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("request_id IN ?", requestIDs).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	if err := db.Create(&[]models.RecommendationRequest{requestA, requestB}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	traceAArticleX := models.RecommendationResultTrace{
		RequestID: requestA.RequestID,
		Position:  1,
		ArticleID: articleX.ID,
		AuthorID:  user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	traceBArticleX := traceAArticleX
	traceBArticleX.RequestID = requestB.RequestID
	if err := db.Create(&traceAArticleX).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&traceBArticleX).Error; err != nil {
		t.Fatalf("database rejected same article in another request: %v", err)
	}

	duplicateArticle := traceAArticleX
	duplicateArticle.Position = 2
	if err := db.Create(&duplicateArticle).Error; err == nil {
		t.Fatal("database accepted duplicate article in one request")
	}

	duplicatePosition := traceAArticleX
	duplicatePosition.ArticleID = articleY.ID
	if err := db.Create(&duplicatePosition).Error; err == nil {
		t.Fatal("database accepted duplicate request position")
	}
}
