package initialize

import (
	"os"
	"testing"

	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecommendationLanguageAffinityMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN unavailable")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.UserRecoProfile{}, &models.RecommendationRequest{}, &models.RecommendationResultTrace{}); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationLanguageAffinitySchema(db); err != nil {
		t.Fatal(err)
	}
	if err := applyRecommendationLanguageAffinitySchema(db); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"user_reco_profiles", "recommendation_requests", "recommendation_result_traces"} {
		var count int
		if err := db.Raw(`
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = ?
  AND column_name IN ?`, table, languageAffinityMigrationColumns(table)).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := len(languageAffinityMigrationColumns(table))
		if count != want {
			t.Fatalf("table=%s language columns=%d want=%d", table, count, want)
		}
	}

	constraintNames := []string{
		"chk_user_reco_profiles_language_weights",
		"chk_recommendation_request_browser_language",
		"chk_recommendation_request_language_context_source",
		"chk_recommendation_request_language_evidence",
		"chk_recommendation_request_language_share",
		"chk_recommendation_request_language_affinity",
		"chk_recommendation_result_trace_post_language",
		"chk_recommendation_result_trace_language_affinity",
		"chk_recommendation_result_trace_language_component",
	}
	var constraintCount int
	if err := db.Raw(`
SELECT COUNT(*)
FROM pg_constraint
WHERE connamespace = current_schema()::regnamespace
  AND conname IN ?`, constraintNames).Scan(&constraintCount).Error; err != nil {
		t.Fatal(err)
	}
	if constraintCount != len(constraintNames) {
		t.Fatalf("language constraint count=%d want=%d", constraintCount, len(constraintNames))
	}

}

func languageAffinityMigrationColumns(table string) []string {
	switch table {
	case "user_reco_profiles":
		return []string{"language_zh_weight", "language_ja_weight", "language_en_weight", "language_evidence"}
	case "recommendation_requests":
		return []string{"browser_language_primary", "language_context_source", "language_behavior_evidence", "language_behavior_share", "language_affinity_zh", "language_affinity_ja", "language_affinity_en"}
	case "recommendation_result_traces":
		return []string{"post_language", "language_affinity", "language_component"}
	default:
		return nil
	}
}
