package initialize

import (
	"fmt"

	"gorm.io/gorm"
)

// applyRecommendationLanguageAffinitySchema owns the small schema extension
// for language-aware recommendation serving. It is deliberately idempotent:
// AutoMigrate may have added the columns already, and deployments can safely
// run the migration more than once.
func applyRecommendationLanguageAffinitySchema(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE user_reco_profiles ADD COLUMN IF NOT EXISTS language_zh_weight DOUBLE PRECISION",
		"ALTER TABLE user_reco_profiles ADD COLUMN IF NOT EXISTS language_ja_weight DOUBLE PRECISION",
		"ALTER TABLE user_reco_profiles ADD COLUMN IF NOT EXISTS language_en_weight DOUBLE PRECISION",
		"ALTER TABLE user_reco_profiles ADD COLUMN IF NOT EXISTS language_evidence DOUBLE PRECISION",
		"UPDATE user_reco_profiles SET language_zh_weight = COALESCE(language_zh_weight, 0), language_ja_weight = COALESCE(language_ja_weight, 0), language_en_weight = COALESCE(language_en_weight, 0), language_evidence = COALESCE(language_evidence, 0)",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_zh_weight SET DEFAULT 0",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_ja_weight SET DEFAULT 0",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_en_weight SET DEFAULT 0",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_evidence SET DEFAULT 0",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_zh_weight SET NOT NULL",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_ja_weight SET NOT NULL",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_en_weight SET NOT NULL",
		"ALTER TABLE user_reco_profiles ALTER COLUMN language_evidence SET NOT NULL",
		"ALTER TABLE user_reco_profiles DROP CONSTRAINT IF EXISTS chk_user_reco_profiles_language_weights",
		"ALTER TABLE user_reco_profiles ADD CONSTRAINT chk_user_reco_profiles_language_weights CHECK (language_zh_weight >= 0 AND language_zh_weight <> 'NaN'::double precision AND language_zh_weight < 'Infinity'::double precision AND language_ja_weight >= 0 AND language_ja_weight <> 'NaN'::double precision AND language_ja_weight < 'Infinity'::double precision AND language_en_weight >= 0 AND language_en_weight <> 'NaN'::double precision AND language_en_weight < 'Infinity'::double precision AND language_evidence >= 0 AND language_evidence <> 'NaN'::double precision AND language_evidence < 'Infinity'::double precision)",

		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS browser_language_primary VARCHAR(8)",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_context_source VARCHAR(16)",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_behavior_evidence DOUBLE PRECISION",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_behavior_share DOUBLE PRECISION",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_affinity_zh DOUBLE PRECISION",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_affinity_ja DOUBLE PRECISION",
		"ALTER TABLE recommendation_requests ADD COLUMN IF NOT EXISTS language_affinity_en DOUBLE PRECISION",
		"UPDATE recommendation_requests SET browser_language_primary = CASE lower(trim(COALESCE(browser_language_primary, ''))) WHEN 'zh' THEN 'zh' WHEN 'ja' THEN 'ja' WHEN 'en' THEN 'en' ELSE '' END, language_context_source = CASE lower(trim(COALESCE(language_context_source, ''))) WHEN 'browser' THEN 'browser' WHEN 'behavior' THEN 'behavior' WHEN 'blended' THEN 'blended' ELSE 'none' END, language_behavior_evidence = CASE WHEN language_behavior_evidence >= 0 AND language_behavior_evidence <> 'NaN'::double precision AND language_behavior_evidence < 'Infinity'::double precision THEN language_behavior_evidence ELSE 0 END, language_behavior_share = CASE WHEN language_behavior_share >= 0 AND language_behavior_share <= 1 AND language_behavior_share <> 'NaN'::double precision THEN language_behavior_share ELSE 0 END, language_affinity_zh = CASE WHEN language_affinity_zh >= 0 AND language_affinity_zh <= 1 AND language_affinity_zh <> 'NaN'::double precision THEN language_affinity_zh ELSE 0 END, language_affinity_ja = CASE WHEN language_affinity_ja >= 0 AND language_affinity_ja <= 1 AND language_affinity_ja <> 'NaN'::double precision THEN language_affinity_ja ELSE 0 END, language_affinity_en = CASE WHEN language_affinity_en >= 0 AND language_affinity_en <= 1 AND language_affinity_en <> 'NaN'::double precision THEN language_affinity_en ELSE 0 END",
		"ALTER TABLE recommendation_requests ALTER COLUMN browser_language_primary SET DEFAULT ''",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_context_source SET DEFAULT 'none'",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_behavior_evidence SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_behavior_share SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_zh SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_ja SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_en SET DEFAULT 0",
		"ALTER TABLE recommendation_requests ALTER COLUMN browser_language_primary SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_context_source SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_behavior_evidence SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_behavior_share SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_zh SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_ja SET NOT NULL",
		"ALTER TABLE recommendation_requests ALTER COLUMN language_affinity_en SET NOT NULL",
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

		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS post_language VARCHAR(8)",
		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS language_affinity DOUBLE PRECISION",
		"ALTER TABLE recommendation_result_traces ADD COLUMN IF NOT EXISTS language_component DOUBLE PRECISION",
		"UPDATE recommendation_result_traces SET post_language = CASE lower(trim(COALESCE(post_language, ''))) WHEN 'zh' THEN 'zh' WHEN 'ja' THEN 'ja' WHEN 'en' THEN 'en' WHEN 'und' THEN 'und' ELSE 'und' END, language_affinity = CASE WHEN language_affinity >= 0 AND language_affinity <= 1 AND language_affinity <> 'NaN'::double precision THEN language_affinity ELSE 0 END, language_component = CASE WHEN language_component >= 0 AND language_component <> 'NaN'::double precision AND language_component < 'Infinity'::double precision THEN language_component ELSE 0 END",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN post_language SET DEFAULT 'und'",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN language_affinity SET DEFAULT 0",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN language_component SET DEFAULT 0",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN post_language SET NOT NULL",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN language_affinity SET NOT NULL",
		"ALTER TABLE recommendation_result_traces ALTER COLUMN language_component SET NOT NULL",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_post_language",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_post_language CHECK (post_language IN ('zh', 'ja', 'en', 'und'))",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_language_affinity",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_language_affinity CHECK (language_affinity >= 0 AND language_affinity <= 1 AND language_affinity <> 'NaN'::double precision)",
		"ALTER TABLE recommendation_result_traces DROP CONSTRAINT IF EXISTS chk_recommendation_result_trace_language_component",
		"ALTER TABLE recommendation_result_traces ADD CONSTRAINT chk_recommendation_result_trace_language_component CHECK (language_component >= 0 AND language_component <> 'NaN'::double precision AND language_component < 'Infinity'::double precision)",

		"UPDATE user_reco_profiles SET next_rebuild_at = LEAST(next_rebuild_at, CURRENT_TIMESTAMP)",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply recommendation language affinity schema: %w", err)
		}
	}
	return nil
}
