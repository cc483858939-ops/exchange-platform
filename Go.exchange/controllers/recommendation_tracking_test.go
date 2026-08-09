package controllers

import (
	"testing"
	"time"
)

func TestRecommendationTrackingTokenRoundTripAndTamper(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	claims := recommendationTrackingClaims{
		UserID: 7, RequestID: "550e8400-e29b-41d4-a716-446655440000", ArticleID: 11,
		Position: 2, Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix:  time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC).Unix(),
		ExpiresAtUnix: time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC).Unix(),
	}
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
	if _, err := verifyRecommendationTrackingToken(token+"x", key); err == nil {
		t.Fatal("expected tampered token to fail")
	}
}

func TestAttachRecommendationTrackingUsesFinalPositions(t *testing.T) {
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_ROLLOUT_PERCENT", "100")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("RECOMMENDATION_TELEMETRY_TOKEN_TTL", "24h")

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	recommendations := []recommendedArticleResponse{{ID: 11}, {ID: 12}}
	requestID := "550e8400-e29b-41d4-a716-446655440001"
	trackedCount, err := attachRecommendationTracking(7, requestID, userInterestProfile{}, recommendations, now)
	if err != nil {
		t.Fatal(err)
	}
	if trackedCount != len(recommendations) {
		t.Fatalf("trackedCount=%d", trackedCount)
	}
	if recommendations[0].Tracking == nil || recommendations[1].Tracking == nil {
		t.Fatal("expected tracking metadata")
	}
	if recommendations[0].Tracking.Position != 1 || recommendations[1].Tracking.Position != 2 {
		t.Fatalf("unexpected positions: %#v %#v", recommendations[0].Tracking, recommendations[1].Tracking)
	}
	if recommendations[0].Tracking.RequestID != requestID || recommendations[1].Tracking.RequestID != requestID {
		t.Fatal("expected supplied request id for the returned list")
	}
	claims, err := verifyRecommendationTrackingToken(
		recommendations[1].Tracking.Token,
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ArticleID != 12 || claims.Position != 2 || claims.StrategyID != recommendationColdStartStrategyID {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}
