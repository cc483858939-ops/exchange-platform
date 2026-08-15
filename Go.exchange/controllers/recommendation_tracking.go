package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

const (
	recommendationScene                  = "recommendation_page"
	recommendationRankerVersion          = "rules_v3"
	recommendationPersonalizedStrategyID = "personalized_rules_v3"
	recommendationColdStartStrategyID    = "cold_start_rules_v3"
	recommendationTrackingTokenVersion   = "v2"
	recommendationSigningKeyMinBytes     = 32
)

type recommendationTrackingResponse struct {
	RequestID        string    `json:"request_id"`
	Position         int       `json:"position"`
	Scene            string    `json:"scene"`
	RankerVersion    string    `json:"ranker_version"`
	RankerConfigHash string    `json:"ranker_config_hash"`
	StrategyID       string    `json:"strategy_id"`
	Token            string    `json:"token"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type recommendationTrackingClaims struct {
	UserID              uint   `json:"user_id"`
	RequestID           string `json:"request_id"`
	ArticleID           uint   `json:"article_id"`
	Position            int    `json:"position"`
	Scene               string `json:"scene"`
	RankerVersion       string `json:"ranker_version"`
	RankerConfigHash    string `json:"ranker_config_hash"`
	StrategyID          string `json:"strategy_id"`
	IssuedAtUnix        int64  `json:"iat"`
	ExpiresAtUnix       int64  `json:"exp"`
	EstimatedReadTimeMS int64  `json:"estimated_read_time_ms"`
	ReadPolicyVersion   string `json:"read_policy_version"`
}

func attachRecommendationTracking(userID uint, requestID string, profile userInterestProfile, recommendations []recommendedArticleResponse, now time.Time) (int, error) {
	if len(recommendations) == 0 || !config.RecommendationTelemetryEnabled() {
		return 0, nil
	}

	if _, err := uuid.Parse(requestID); err != nil {
		return 0, errors.New("recommendation tracking request id must be a UUID")
	}
	if !recommendationTelemetryRequestSelected(userID, requestID, config.RecommendationTelemetryRolloutPercent()) {
		return 0, nil
	}

	key := []byte(config.RecommendationTelemetrySigningKey())
	if len(key) < recommendationSigningKeyMinBytes {
		return 0, errors.New("recommendation telemetry signing key must contain at least 32 bytes")
	}

	issuedAt := now.UTC()
	expiresAt := issuedAt.Add(config.RecommendationTelemetryTokenTTL())
	strategyID := recommendationStrategyID(profile)
	configHash := recommendationRankerConfigHash(normalizedRulesV3RecommendationConfig())

	for index := range recommendations {
		claims := recommendationTrackingClaims{
			UserID: userID, RequestID: requestID, ArticleID: recommendations[index].ID,
			Position: index + 1, Scene: recommendationScene,
			RankerVersion: recommendationRankerVersion, RankerConfigHash: configHash,
			StrategyID: strategyID, IssuedAtUnix: issuedAt.Unix(), ExpiresAtUnix: expiresAt.Unix(),
			EstimatedReadTimeMS: estimateArticleReadTime(recommendations[index].Content).Milliseconds(), ReadPolicyVersion: recommendationReadPolicyVersion,
		}
		token, err := signRecommendationTrackingClaims(claims, key)
		if err != nil {
			return 0, err
		}
		recommendations[index].Tracking = &recommendationTrackingResponse{
			RequestID: requestID, Position: index + 1, Scene: recommendationScene,
			RankerVersion: recommendationRankerVersion, RankerConfigHash: configHash,
			StrategyID: strategyID, Token: token, ExpiresAt: expiresAt,
		}
	}
	return len(recommendations), nil
}

func recommendationStrategyID(profile userInterestProfile) string {
	if profile.PersonalizedSignalCount > 0 {
		return recommendationPersonalizedStrategyID
	}
	return recommendationColdStartStrategyID
}

func recommendationTelemetryRequestSelected(userID uint, requestID string, percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", userID, requestID)))
	bucket := int(sum[0])<<8 | int(sum[1])
	return bucket%100 < percent
}

func recommendationRankerConfigHash(cfg config.RecommendationConfig) string {
	canonical := fmt.Sprintf(
		"view=%g|like=%g|click=%g|qualified_read=%g|quick_bounce=%g|not_interested=%g|half_life=%g|lookback=%d|saturation=%g|category=%g|tag=%g|popularity=%g|freshness=%g|candidate_cap=%d|feedback_article_limit=%d|view_article_limit=%d|read_policy=%s",
		cfg.BehaviorWeights.View, cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Click, cfg.BehaviorWeights.QualifiedRead, cfg.BehaviorWeights.QuickBounce, cfg.BehaviorWeights.NotInterested, cfg.SignalHalfLifeDays, cfg.FeedbackLookbackDays, cfg.InterestSaturationScale, cfg.CategoryWeight, cfg.TagWeight, cfg.PopularityWeight, cfg.FreshnessWeight, recommendationCandidateCap, recommendationFeedbackArticleLimit, recommendationRecentViewArticleLimit, recommendationReadPolicyVersion,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:12]
}
func signRecommendationTrackingClaims(claims recommendationTrackingClaims, key []byte) (string, error) {
	if len(key) < recommendationSigningKeyMinBytes {
		return "", errors.New("recommendation telemetry signing key is too short")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal recommendation tracking claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signedValue := recommendationTrackingTokenVersion + "." + encodedPayload
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signedValue))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signedValue + "." + signature, nil
}

func verifyRecommendationTrackingToken(token string, key []byte) (recommendationTrackingClaims, error) {
	if len(key) < recommendationSigningKeyMinBytes {
		return recommendationTrackingClaims{}, errors.New("recommendation telemetry signing key is too short")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != recommendationTrackingTokenVersion {
		return recommendationTrackingClaims{}, errors.New("invalid tracking token format")
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return recommendationTrackingClaims{}, errors.New("invalid tracking token signature")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return recommendationTrackingClaims{}, errors.New("invalid tracking token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return recommendationTrackingClaims{}, errors.New("invalid tracking token payload")
	}
	var claims recommendationTrackingClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return recommendationTrackingClaims{}, errors.New("invalid tracking token payload")
	}
	if claims.UserID == 0 || claims.ArticleID == 0 || claims.Position <= 0 ||
		claims.Scene == "" || claims.RankerVersion == "" || claims.RankerConfigHash == "" || claims.StrategyID == "" ||
		claims.IssuedAtUnix <= 0 || claims.ExpiresAtUnix <= claims.IssuedAtUnix || claims.EstimatedReadTimeMS <= 0 || strings.TrimSpace(claims.ReadPolicyVersion) == "" {
		return recommendationTrackingClaims{}, errors.New("incomplete tracking token claims")
	}
	if _, err := uuid.Parse(claims.RequestID); err != nil {
		return recommendationTrackingClaims{}, errors.New("invalid tracking request id")
	}
	return claims, nil
}
