package controllers

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"gorm.io/gorm"
)

func testRecommendationTrackingClaims(now time.Time) recommendationTrackingClaims {
	return recommendationTrackingClaims{
		UserID: 7, RequestID: "550e8400-e29b-41d4-a716-446655440000", ArticleID: 11,
		Position: 2, Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		SelectionMode: string(recommendationResultSelectionRanked),
	}
}

func TestRecommendationRankerConfigHashIncludesV4ServingSettings(t *testing.T) {
	base := defaultRecommendationConfig()
	tests := []struct {
		name   string
		mutate func(*config.RecommendationConfig)
	}{
		{name: "semantic recent window", mutate: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentWindowDays++ }},
		{name: "semantic recent ratio", mutate: func(cfg *config.RecommendationConfig) { cfg.SemanticRecall.RecentRatio = 0.75 }},
		{name: "trending weight", mutate: func(cfg *config.RecommendationConfig) { cfg.TrendingWeight++ }},
		{name: "trending max age", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.MaxAgeDays++ }},
		{name: "trending half life", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.HalfLifeHours++ }},
		{name: "trending comment factor", mutate: func(cfg *config.RecommendationConfig) { cfg.Trending.CommentFactor++ }},
		{name: "personalized trending cap", mutate: func(cfg *config.RecommendationConfig) { cfg.Candidates.Personalized.Trending++ }},
		{name: "cold-start trending cap", mutate: func(cfg *config.RecommendationConfig) { cfg.Candidates.ColdStart.Trending++ }},
		{name: "exploration ratio", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.Ratio = 0.20 }},
		{name: "exploration max slots", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.MaxSlots++ }},
		{name: "exploration recent window", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.RecentWindowDays++ }},
		{name: "exploration novel age", mutate: func(cfg *config.RecommendationConfig) { cfg.Exploration.NovelArticleMaxAgeDays++ }},
	}

	baseHash := recommendationRankerConfigHash(base)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mutate(&mutated)
			if got := recommendationRankerConfigHash(mutated); got == baseHash {
				t.Fatalf("hash=%q unchanged from base %q", got, baseHash)
			}
		})
	}
}

func TestRecommendationRankerConfigHashExplorationDoesNotChangeProfileHash(t *testing.T) {
	base := defaultRecommendationConfig()
	mutated := base
	mutated.Exploration.Ratio = 0.20
	mutated.Exploration.MaxSlots++
	mutated.Exploration.RecentWindowDays++
	mutated.Exploration.NovelArticleMaxAgeDays++
	if got, want := recommendation.ProfileConfigHash(mutated, config.ActiveEmbeddingVersion()), recommendation.ProfileConfigHash(base, config.ActiveEmbeddingVersion()); got != want {
		t.Fatalf("profile hash changed with exploration settings: got=%q want=%q", got, want)
	}
}

func tamperRecommendationTrackingClaim(token string, oldValue, newValue string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token + "tampered"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return token + "tampered"
	}
	payload = bytes.Replace(payload, []byte(oldValue), []byte(newValue), 1)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, ".")
}

func TestRecommendationTrackingTokenV3RoundTripAndClaimBinding(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	claims := testRecommendationTrackingClaims(now)
	token, err := signRecommendationTrackingClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := verifyRecommendationTrackingToken(token, key)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != claims {
		t.Fatalf("decoded claims=%#v want %#v", decoded, claims)
	}
	if _, err := verifyRecommendationTrackingToken(tamperRecommendationTrackingClaim(token, "\"estimated_read_time_ms\":3000", "\"estimated_read_time_ms\":4000"), key); err == nil {
		t.Fatal("estimated read time tampering should invalidate signature")
	}
	if _, err := verifyRecommendationTrackingToken(tamperRecommendationTrackingClaim(token, "\"read_policy_version\":\"read_v1\"", "\"read_policy_version\":\"read_v2\""), key); err == nil {
		t.Fatal("read policy tampering should invalidate signature")
	}
	v2 := strings.Replace(token, "v3.", "v2.", 1)
	if _, err := verifyRecommendationTrackingToken(v2, key); err == nil {
		t.Fatal("V2 token should be rejected")
	}
	missing := claims
	missing.EstimatedReadTimeMS = 0
	missingToken, err := signRecommendationTrackingClaims(missing, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRecommendationTrackingToken(missingToken, key); err == nil {
		t.Fatal("missing V3 estimated read time should be rejected")
	}
}

func TestRecommendationTrackingTokenV3RejectsInvalidProvenanceStates(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	for _, mutate := range []func(*recommendationTrackingClaims){
		func(claims *recommendationTrackingClaims) {
			claims.SelectionMode = string(recommendationResultSelectionExploration)
			claims.ExplorationReason = recommendationExplorationReasonRecent
		},
		func(claims *recommendationTrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(recommendationResultSelectionRanked)
			claims.ExplorationReason = recommendationExplorationReasonRecent
		},
		func(claims *recommendationTrackingClaims) {
			claims.ExplorationOpportunity = true
			claims.SelectionMode = string(recommendationResultSelectionExploration)
			claims.ExplorationReason = "unsupported"
		},
	} {
		claims := testRecommendationTrackingClaims(now)
		mutate(&claims)
		token, err := signRecommendationTrackingClaims(claims, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyRecommendationTrackingToken(token, key); err == nil {
			t.Fatalf("invalid provenance state accepted: %#v", claims)
		}
	}
}

func TestAttachRecommendationTrackingUsesFinalPositionsAndReadClaims(t *testing.T) {
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_ROLLOUT_PERCENT", "100")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("RECOMMENDATION_TELEMETRY_TOKEN_TTL", "24h")

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	recommendations := []recommendedArticleResponse{
		{ID: 11, Content: "tiny"},
		{ID: 12, Content: strings.Repeat("word ", 400)},
	}
	requestID := "550e8400-e29b-41d4-a716-446655440001"
	selected := []selectedRecommendation{
		{Article: models.Article{Model: gorm.Model{ID: 11}}, SelectionMode: recommendationResultSelectionRanked},
		{Article: models.Article{Model: gorm.Model{ID: 12}}, ExplorationOpportunity: true, SelectionMode: recommendationResultSelectionExploration, ExplorationReason: recommendationExplorationReasonRecent, ExplorationSemantic: .8},
	}
	trackedCount, err := attachRecommendationTracking(7, requestID, userInterestProfile{}, selected, recommendations, now)
	if err != nil {
		t.Fatal(err)
	}
	if trackedCount != len(recommendations) || recommendations[0].Tracking == nil || recommendations[1].Tracking == nil {
		t.Fatalf("tracking count=%d recommendations=%#v", trackedCount, recommendations)
	}
	if recommendations[0].Tracking.Position != 1 || recommendations[1].Tracking.Position != 2 {
		t.Fatalf("unexpected positions: %#v %#v", recommendations[0].Tracking, recommendations[1].Tracking)
	}
	claims, err := verifyRecommendationTrackingToken(recommendations[1].Tracking.Token, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if claims.ArticleID != 12 || claims.Position != 2 || claims.StrategyID != recommendationColdStartStrategyID ||
		!claims.ExplorationOpportunity || claims.SelectionMode != string(recommendationResultSelectionExploration) || claims.ExplorationReason != recommendationExplorationReasonRecent ||
		claims.EstimatedReadTimeMS <= 0 || claims.ReadPolicyVersion != recommendationReadPolicyVersion {
		t.Fatalf("unexpected V3 claims: %#v", claims)
	}
}
