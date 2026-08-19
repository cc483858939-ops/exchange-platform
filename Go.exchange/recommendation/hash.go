package recommendation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"Go.exchange/config"
)

// ProfileConfigHash identifies only the canonical profile inputs. Serving
// rank weights, candidate caps, and materializer timing are intentionally not
// included because they do not change the materialized profile contents.
func ProfileConfigHash(cfg config.RecommendationConfig, embeddingVersion string) string {
	canonical := fmt.Sprintf(
		"profile=%s|canonical=%s|embedding=%s|recent_views=%d|replies=%d|view=%g|like=%g|click=%g|qualified_read=%g|reply=%g|quick_bounce=%g|not_interested=%g|half_life=%g|lookback=%d|coexist=%g|article_cap=%g",
		MaterializedProfileVersion, CanonicalOutcomeVersion, embeddingVersion,
		ProfileRecentViewLimit, ProfileReplyLimit,
		cfg.BehaviorWeights.View, cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Click,
		cfg.BehaviorWeights.QualifiedRead, cfg.BehaviorWeights.Reply,
		cfg.BehaviorWeights.QuickBounce, cfg.BehaviorWeights.NotInterested,
		cfg.SignalHalfLifeDays, cfg.FeedbackLookbackDays,
		cfg.PositiveSignalCoexistBonus, cfg.PositiveArticleWeightCap,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:12]
}

const MaterializedProfileVersion = "materialized_profile_v1"
