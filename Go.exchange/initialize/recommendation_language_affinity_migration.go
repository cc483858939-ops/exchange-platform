package initialize

import (
	"fmt"

	"gorm.io/gorm"
)

// applyRecommendationLanguageAffinityConstraints reapplies the current
// language-aware recommendation checks after AutoMigrate owns the columns.
func applyRecommendationLanguageAffinityConstraints(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS chk_user_reco_profiles_language_weights",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT chk_user_reco_profiles_language_weights CHECK (language_zh_weight >= 0 AND language_zh_weight <> 'NaN'::double precision AND language_zh_weight < 'Infinity'::double precision AND language_ja_weight >= 0 AND language_ja_weight <> 'NaN'::double precision AND language_ja_weight < 'Infinity'::double precision AND language_en_weight >= 0 AND language_en_weight <> 'NaN'::double precision AND language_en_weight < 'Infinity'::double precision AND language_evidence >= 0 AND language_evidence <> 'NaN'::double precision AND language_evidence < 'Infinity'::double precision)",

		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_browser_language",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_browser_language CHECK (browser_language_primary IN ('', 'zh', 'ja', 'en'))",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_language_context_source",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_language_context_source CHECK (language_context_source IN ('none', 'browser', 'behavior', 'blended'))",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_language_evidence",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_language_evidence CHECK (language_behavior_evidence >= 0 AND language_behavior_evidence <> 'NaN'::double precision AND language_behavior_evidence < 'Infinity'::double precision)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_language_share",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_language_share CHECK (language_behavior_share >= 0 AND language_behavior_share <= 1 AND language_behavior_share <> 'NaN'::double precision)",
		"ALTER TABLE recommendation_requests DROP CONSTRAINT IF EXISTS chk_recommendation_request_language_affinity",
		"ALTER TABLE recommendation_requests ADD CONSTRAINT chk_recommendation_request_language_affinity CHECK (language_affinity_zh >= 0 AND language_affinity_zh <= 1 AND language_affinity_zh <> 'NaN'::double precision AND language_affinity_ja >= 0 AND language_affinity_ja <= 1 AND language_affinity_ja <> 'NaN'::double precision AND language_affinity_en >= 0 AND language_affinity_en <= 1 AND language_affinity_en <> 'NaN'::double precision)",

		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_post_language",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_post_language CHECK (post_language IN ('zh', 'ja', 'en', 'und'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_language_affinity",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_language_affinity CHECK (language_affinity >= 0 AND language_affinity <= 1 AND language_affinity <> 'NaN'::double precision)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_language_component",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_language_component CHECK (language_component >= 0 AND language_component <> 'NaN'::double precision AND language_component < 'Infinity'::double precision)",
	}
	if err := applyMigrationStatements(tx, "apply recommendation language affinity constraints", statements); err != nil {
		return fmt.Errorf("apply recommendation language affinity constraints: %w", err)
	}
	return nil
}
