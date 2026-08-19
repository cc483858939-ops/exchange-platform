package recommendation

import (
	"testing"

	"Go.exchange/config"
)

func TestProfileConfigHashIncludesCanonicalInputsOnly(t *testing.T) {
	base := config.RecommendationConfig{
		BehaviorWeights:    config.RecommendationBehaviorWeights{View: .5, Like: 6, Click: 1.5, QualifiedRead: 3, Reply: 5, QuickBounce: -3, NotInterested: -6},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90, PositiveSignalCoexistBonus: 1, PositiveArticleWeightCap: 7,
	}
	baseHash := ProfileConfigHash(base, "embedding-v1")
	canonical := base
	canonical.BehaviorWeights.Like++
	if ProfileConfigHash(canonical, "embedding-v1") == baseHash {
		t.Fatal("behavior weight must change profile hash")
	}
	operational := base
	operational.Candidates.Personalized.Merged++
	operational.SemanticWeight++
	operational.ProfileMaterialization.BatchSize = 999
	if ProfileConfigHash(operational, "embedding-v1") != baseHash {
		t.Fatal("serving/materializer operational settings must not change profile hash")
	}
	if ProfileConfigHash(base, "embedding-v2") == baseHash {
		t.Fatal("embedding version must change profile hash")
	}
}
