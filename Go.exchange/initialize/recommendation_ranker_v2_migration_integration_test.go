package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationRankerV2IndexesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.RecommendationEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationRankerV2Indexes(db); err != nil {
		t.Fatal(err)
	}

	var indexes []struct {
		Name       string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'recommendation_events'
  AND indexname IN ('idx_recommendation_events_user_feedback_order', 'idx_recommendation_events_user_article_negative')
ORDER BY indexname
`).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Fatalf("rules_v2 indexes=%d want=2", len(indexes))
	}

	definitions := make(map[string]string, len(indexes))
	for _, index := range indexes {
		definitions[index.Name] = strings.ToLower(index.Definition)
	}
	assertIndexDefinitionContains(t, definitions["idx_recommendation_events_user_feedback_order"],
		"user_id", "occurred_at desc", "received_at desc", "event_id desc", "where")
	assertIndexDefinitionContains(t, definitions["idx_recommendation_events_user_article_negative"],
		"user_id", "article_id", "occurred_at desc", "not_interested", "where")
}

func assertIndexDefinitionContains(t *testing.T, definition string, fragments ...string) {
	t.Helper()
	if definition == "" {
		t.Fatal("index definition is missing")
	}
	for _, fragment := range fragments {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("index definition %q does not contain %q", definition, fragment)
		}
	}
}
