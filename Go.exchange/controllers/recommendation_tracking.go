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
	"Go.exchange/eventing"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
)

const (
	recommendationScene                   = "recommendation_page"
	recommendationRankerVersion           = "rules_v4"
	recommendationPersonalizedStrategyID  = "for_you_materialized_profile_v5"
	recommendationColdStartStrategyID     = "for_you_materialized_profile_v5"
	recommendationTrackingTokenVersion    = "v3"
	recommendationCanonicalOutcomeVersion = recommendation.CanonicalOutcomeVersion
	recommendationPassiveRecencyPolicy    = "read_end_recency_v2"
	recommendationSelectionPolicyVersion  = "network_balance_exploration_v2"
	recommendationSigningKeyMinBytes      = 32
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

	ExplorationOpportunity bool   `json:"exploration_opportunity"`
	SelectionMode          string `json:"selection_mode"`
	ExplorationReason      string `json:"exploration_reason"`
}

func attachRecommendationTracking(userID uint, requestID string, profile userInterestProfile, selected []selectedRecommendation, recommendations []recommendedArticleResponse, now time.Time) (int, error) {
	if len(recommendations) == 0 || !config.RecommendationTelemetryEnabled() {
		return 0, nil
	}
	if len(selected) != len(recommendations) {
		return 0, errors.New("recommendation tracking selection and response lengths differ")
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
	configHash := recommendationRankerConfigHash(normalizedRecommendationConfig())

	for index := range recommendations {
		if selected[index].Article.ID != recommendations[index].ID {
			return 0, errors.New("recommendation tracking selection and response ids differ")
		}
		if err := eventing.ValidateRecommendationProvenance(selected[index].ExplorationOpportunity, string(selected[index].SelectionMode), selected[index].ExplorationReason); err != nil {
			return 0, err
		}
		claims := recommendationTrackingClaims{
			UserID: userID, RequestID: requestID, ArticleID: recommendations[index].ID,
			Position: index + 1, Scene: recommendationScene,
			RankerVersion: recommendationRankerVersion, RankerConfigHash: configHash,
			StrategyID: strategyID, IssuedAtUnix: issuedAt.Unix(), ExpiresAtUnix: expiresAt.Unix(),
			EstimatedReadTimeMS: estimateArticleReadTime(recommendations[index].Content).Milliseconds(), ReadPolicyVersion: recommendationReadPolicyVersion,
			ExplorationOpportunity: selected[index].ExplorationOpportunity,
			SelectionMode:          string(selected[index].SelectionMode), ExplorationReason: selected[index].ExplorationReason,
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
	if len(profile.PositiveVector) == 0 && profile.ProfileStatus == recommendationProfileStatusMiss {
		return recommendationColdStartStrategyID
	}
	return recommendationPersonalizedStrategyID
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
	canonical := recommendationRankerConfigCanonicalString(cfg)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:12]
}

func recommendationRankerConfigCanonicalString(cfg config.RecommendationConfig) string {
	p := cfg.Candidates.Personalized
	c := cfg.Candidates.ColdStart
	return fmt.Sprintf(
		"view=%g|like=%g|click=%g|qualified_read=%g|reply=%g|quick_bounce=%g|not_interested=%g|signal_half_life=%g|lookback=%d|coexist=%g|article_cap=%g|semantic=%g|negative_semantic=%g|negative_confidence_scale=%g|semantic_recent_window_days=%d|semantic_recent_ratio=%g|trending_weight=%g|trending_max_age_days=%d|trending_half_life_hours=%g|trending_comment_factor=%g|author_affinity=%g|author_affinity_scale=%g|following_bonus=%g|out_ratio=%g|hard_minutes=%d|soft_days=%d|served_limit=%d|diversity_enabled=%t|author_window=%d|max_author=%d|duplicate_threshold=%g|duplicate_penalty=%g|exploration_ratio=%g|exploration_max_slots=%d|exploration_recent_window_days=%d|exploration_novel_article_max_age_days=%d|personalized_caps=%d,%d,%d,%d,%d|cold_caps=%d,%d,%d,%d|candidate_retrieval=%s|materialized_profile=%s|profile_config=%s|canonical_outcome=%s|passive_recency=%s|read_policy=%s|selection_policy=%s|embedding_version=%s",
		cfg.BehaviorWeights.View, cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Click,
		cfg.BehaviorWeights.QualifiedRead, cfg.BehaviorWeights.Reply, cfg.BehaviorWeights.QuickBounce,
		cfg.BehaviorWeights.NotInterested, cfg.SignalHalfLifeDays, cfg.FeedbackLookbackDays,
		cfg.PositiveSignalCoexistBonus, cfg.PositiveArticleWeightCap, cfg.SemanticWeight,
		cfg.NegativeSemanticWeight, cfg.NegativeConfidenceSaturationScale, cfg.SemanticRecall.RecentWindowDays, cfg.SemanticRecall.RecentRatio,
		cfg.TrendingWeight, cfg.Trending.MaxAgeDays, cfg.Trending.HalfLifeHours, cfg.Trending.CommentFactor,
		cfg.AuthorAffinityWeight, cfg.AuthorAffinitySaturationScale, cfg.FollowingBonus,
		cfg.OutOfNetworkMinRatio, cfg.ServedHardExclusionMinutes,
		cfg.ServedSoftLookbackDays, cfg.ServedHistoryLimit, cfg.Diversity.Enabled,
		cfg.Diversity.AuthorWindowSize, cfg.Diversity.MaxSameAuthorInWindow,
		cfg.Diversity.SemanticDuplicateThreshold, cfg.Diversity.SemanticDuplicatePenalty,
		cfg.Exploration.Ratio, cfg.Exploration.MaxSlots, cfg.Exploration.RecentWindowDays, cfg.Exploration.NovelArticleMaxAgeDays,
		p.Semantic, p.Following, p.Recent, p.Trending, p.Merged,
		c.Following, c.Recent, c.Trending, c.Merged, recommendationCandidateRetrievalVersion,
		recommendation.MaterializedProfileVersion, recommendation.ProfileConfigHash(cfg, config.ActiveEmbeddingVersion()),
		recommendationCanonicalOutcomeVersion, recommendationPassiveRecencyPolicy,
		recommendationReadPolicyVersion, recommendationSelectionPolicyVersion, config.ActiveEmbeddingVersion(),
	)
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
	if err := eventing.ValidateRecommendationProvenance(claims.ExplorationOpportunity, claims.SelectionMode, claims.ExplorationReason); err != nil {
		return recommendationTrackingClaims{}, err
	}
	return claims, nil
}
