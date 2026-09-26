package recommendation

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
	"unicode"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
)

const (
	recommendationScene                            = "recommendation_page"
	RecommendationPersonalizedStrategyID           = "for_you_materialized_profile_v6"
	RecommendationColdStartStrategyID              = "for_you_materialized_profile_v6"
	RecommendationTrackingTokenVersion             = "v3"
	RecommendationPassiveRecencyPolicy             = "read_end_recency_v2"
	RecommendationReadPolicyVersion                = "read_v1"
	recommendationSigningKeyMinBytes               = 32
	recommendationReadMinimumDwellMS         int64 = 3 * 1000
	recommendationReadMaxForegroundMS        int64 = 6 * 60 * 60 * 1000
	recommendationReadMaxProgress                  = 100
	recommendationReadMinimumProgress              = 50
	recommendationReadQuickBounceProgress          = 10
	recommendationReadCJKCharactersPerMinute       = 300
	recommendationReadLatinWordsPerMinute          = 220
	recommendationReadMinimumEstimateMS      int64 = 3 * 1000
	recommendationReadMaximumEstimateMS      int64 = 120 * 1000
)

type TrackingConfig struct {
	Enabled        bool
	RolloutPercent int
	SigningKey     []byte
	TokenTTL       time.Duration
}

type TrackingFact struct {
	PostID           uint
	RequestID        string
	Position         int
	Scene            string
	RankerVersion    string
	RankerConfigHash string
	StrategyID       string
	Token            string
	ExpiresAt        time.Time
}

type TrackingClaims struct {
	UserID                 uint   `json:"user_id"`
	RequestID              string `json:"request_id"`
	PostID                 uint   `json:"post_id"`
	Position               int    `json:"position"`
	Scene                  string `json:"scene"`
	RankerVersion          string `json:"ranker_version"`
	RankerConfigHash       string `json:"ranker_config_hash"`
	StrategyID             string `json:"strategy_id"`
	IssuedAtUnix           int64  `json:"iat"`
	ExpiresAtUnix          int64  `json:"exp"`
	EstimatedReadTimeMS    int64  `json:"estimated_read_time_ms"`
	ReadPolicyVersion      string `json:"read_policy_version"`
	ExplorationOpportunity bool   `json:"exploration_opportunity"`
	SelectionMode          string `json:"selection_mode"`
	ExplorationReason      string `json:"exploration_reason"`
}

func BuildTrackingFacts(result ServeResult, cfg TrackingConfig) ([]TrackingFact, error) {
	if len(result.Selected) == 0 || !cfg.Enabled {
		return nil, nil
	}
	if result.Viewer.UserID == 0 {
		return nil, errors.New("recommendation tracking user id is required")
	}
	if !RecommendationTelemetryRequestSelected(result.Viewer.UserID, result.RequestID, cfg.RolloutPercent) {
		return nil, nil
	}
	if _, err := uuid.Parse(result.RequestID); err != nil {
		return nil, errors.New("recommendation tracking request id must be a UUID")
	}
	if len(cfg.SigningKey) < recommendationSigningKeyMinBytes {
		return nil, errors.New("recommendation telemetry signing key must contain at least 32 bytes")
	}
	issuedAt := result.Now.UTC()
	ttl := cfg.TokenTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	expiresAt := issuedAt.Add(ttl)
	return buildTrackingFacts(result.Viewer.UserID, result.RequestID, result.RankerConfigHash, result.StrategyID, result.Selected, issuedAt, expiresAt, cfg.SigningKey)
}

func buildTrackingFacts(userID uint, requestID, configHash, strategyID string, selected []SelectedCandidate, issuedAt, expiresAt time.Time, key []byte) ([]TrackingFact, error) {
	if len(selected) == 0 || userID == 0 {
		return nil, nil
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return nil, errors.New("recommendation tracking request id must be a UUID")
	}
	if len(key) < recommendationSigningKeyMinBytes {
		return nil, errors.New("recommendation telemetry signing key must contain at least 32 bytes")
	}
	if expiresAt.IsZero() || !expiresAt.After(issuedAt) {
		expiresAt = issuedAt.Add(24 * time.Hour)
	}
	facts := make([]TrackingFact, 0, len(selected))
	for index, item := range selected {
		if err := eventing.ValidateRecommendationProvenance(item.ExplorationOpportunity, string(item.SelectionMode), string(item.ExplorationReason)); err != nil {
			return nil, err
		}
		claims := TrackingClaims{
			UserID: userID, RequestID: requestID, PostID: item.Post.ID,
			Position: index + 1, Scene: recommendationScene,
			RankerVersion: RankerVersion, RankerConfigHash: configHash,
			StrategyID: strategyID, IssuedAtUnix: issuedAt.Unix(), ExpiresAtUnix: expiresAt.Unix(),
			EstimatedReadTimeMS: EstimatePostReadTime(item.Post.Content).Milliseconds(), ReadPolicyVersion: RecommendationReadPolicyVersion,
			ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: string(item.SelectionMode),
			ExplorationReason: string(item.ExplorationReason),
		}
		token, err := SignTrackingClaims(claims, key)
		if err != nil {
			return nil, err
		}
		facts = append(facts, TrackingFact{
			PostID: item.Post.ID, RequestID: requestID, Position: index + 1, Scene: recommendationScene,
			RankerVersion: RankerVersion, RankerConfigHash: configHash, StrategyID: strategyID,
			Token: token, ExpiresAt: expiresAt,
		})
	}
	return facts, nil
}

func RecommendationTelemetryRequestSelected(userID uint, requestID string, percent int) bool {
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

func StrategyID(profile Profile) string {
	if len(profile.PositiveVector) == 0 && profile.ProfileStatus == ProfileStatusMiss {
		return RecommendationColdStartStrategyID
	}
	return RecommendationPersonalizedStrategyID
}

func RankerConfigHash(cfg config.RecommendationConfig, servingVersion string) string {
	canonical := fmt.Sprintf("view=%g|like=%g|click=%g|qualified_read=%g|reply=%g|quick_bounce=%g|not_interested=%g|signal_half_life=%g|lookback=%d|coexist=%g|post_cap=%g|semantic=%g|negative_semantic=%g|negative_confidence_scale=%g|semantic_recent_window_days=%d|semantic_recent_ratio=%g|trending_weight=%g|trending_max_age_days=%d|trending_half_life_hours=%g|trending_reply_factor=%g|author_affinity=%g|author_affinity_scale=%g|following_bonus=%g|out_ratio=%g|hard_minutes=%d|soft_days=%d|served_limit=%d|diversity_enabled=%t|author_window=%d|max_author=%d|duplicate_threshold=%g|duplicate_penalty=%g|exploration_ratio=%g|exploration_max_slots=%d|exploration_recent_window_days=%d|exploration_novel_post_max_age_days=%d|personalized_caps=%d,%d,%d,%d,%d|cold_caps=%d,%d,%d,%d|fusion_rank_constant=%d|language_enabled=%t|language_weight=%g|language_evidence_scale=%g|language_max_behavior_share=%g|candidate_retrieval=%s|materialized_profile=%s|profile_config=%s|canonical_outcome=%s|passive_recency=%s|read_policy=%s|selection_policy=%s|embedding_version=%s",
		cfg.BehaviorWeights.View, cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Click, cfg.BehaviorWeights.QualifiedRead,
		cfg.BehaviorWeights.Reply, cfg.BehaviorWeights.QuickBounce, cfg.BehaviorWeights.NotInterested,
		cfg.SignalHalfLifeDays, cfg.FeedbackLookbackDays, cfg.PositiveSignalCoexistBonus, cfg.PositivePostWeightCap,
		cfg.SemanticWeight, cfg.NegativeSemanticWeight, cfg.NegativeConfidenceSaturationScale,
		cfg.SemanticRecall.RecentWindowDays, cfg.SemanticRecall.RecentRatio, cfg.TrendingWeight,
		cfg.Trending.MaxAgeDays, cfg.Trending.HalfLifeHours, cfg.Trending.ReplyFactor, cfg.AuthorAffinityWeight,
		cfg.AuthorAffinitySaturationScale, cfg.FollowingBonus, cfg.OutOfNetworkMinRatio, cfg.ServedHardExclusionMinutes,
		cfg.ServedSoftLookbackDays, cfg.ServedHistoryLimit, cfg.Diversity.Enabled, cfg.Diversity.AuthorWindowSize,
		cfg.Diversity.MaxSameAuthorInWindow, cfg.Diversity.SemanticDuplicateThreshold, cfg.Diversity.SemanticDuplicatePenalty,
		cfg.Exploration.Ratio, cfg.Exploration.MaxSlots, cfg.Exploration.RecentWindowDays, cfg.Exploration.NovelPostMaxAgeDays,
		cfg.Candidates.Personalized.Semantic, cfg.Candidates.Personalized.Following, cfg.Candidates.Personalized.Recent,
		cfg.Candidates.Personalized.Trending, cfg.Candidates.Personalized.Merged,
		cfg.Candidates.ColdStart.Following, cfg.Candidates.ColdStart.Recent, cfg.Candidates.ColdStart.Trending, cfg.Candidates.ColdStart.Merged,
		cfg.Fusion.RankConstant, cfg.LanguageAffinity.Enabled, cfg.LanguageAffinity.Weight,
		cfg.LanguageAffinity.EvidenceSaturationScale, cfg.LanguageAffinity.MaxBehaviorShare,
		"social_semantic_materialized_profile_rrf_v5", MaterializedProfileVersion, ProfileConfigHash(cfg, servingVersion),
		CanonicalOutcomeVersion, RecommendationPassiveRecencyPolicy, RecommendationReadPolicyVersion, SelectionPolicyVersion, servingVersion)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:12]
}

func SignTrackingClaims(claims TrackingClaims, key []byte) (string, error) {
	if len(key) < recommendationSigningKeyMinBytes {
		return "", errors.New("recommendation telemetry signing key is too short")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal recommendation tracking claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signedValue := RecommendationTrackingTokenVersion + "." + encodedPayload
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signedValue))
	return signedValue + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func VerifyTrackingToken(token string, key []byte) (TrackingClaims, error) {
	if len(key) < recommendationSigningKeyMinBytes {
		return TrackingClaims{}, errors.New("recommendation telemetry signing key is too short")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != RecommendationTrackingTokenVersion {
		return TrackingClaims{}, errors.New("invalid tracking token format")
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return TrackingClaims{}, errors.New("invalid tracking token signature")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return TrackingClaims{}, errors.New("invalid tracking token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return TrackingClaims{}, errors.New("invalid tracking token payload")
	}
	var claims TrackingClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return TrackingClaims{}, errors.New("invalid tracking token payload")
	}
	if claims.UserID == 0 || claims.PostID == 0 || claims.Position <= 0 || claims.Scene == "" || claims.RankerVersion == "" ||
		claims.RankerConfigHash == "" || claims.StrategyID == "" || claims.IssuedAtUnix <= 0 || claims.ExpiresAtUnix <= claims.IssuedAtUnix ||
		claims.EstimatedReadTimeMS <= 0 || strings.TrimSpace(claims.ReadPolicyVersion) == "" {
		return TrackingClaims{}, errors.New("incomplete tracking token claims")
	}
	if _, err := uuid.Parse(claims.RequestID); err != nil {
		return TrackingClaims{}, errors.New("invalid tracking request id")
	}
	if err := eventing.ValidateRecommendationProvenance(claims.ExplorationOpportunity, claims.SelectionMode, claims.ExplorationReason); err != nil {
		return TrackingClaims{}, err
	}
	return claims, nil
}

func ClassifyRecommendationRead(foregroundTimeMS int64, scrollProgressPercent int, estimatedReadTimeMS int64, readPolicyVersion string) (string, error) {
	if strings.TrimSpace(readPolicyVersion) != RecommendationReadPolicyVersion {
		return "", errors.New("unsupported recommendation read policy")
	}
	if foregroundTimeMS < 0 || foregroundTimeMS > recommendationReadMaxForegroundMS {
		return "", errors.New("invalid foreground time")
	}
	if scrollProgressPercent < 0 || scrollProgressPercent > recommendationReadMaxProgress {
		return "", errors.New("invalid scroll progress")
	}
	if estimatedReadTimeMS <= 0 {
		return "", errors.New("invalid estimated read time")
	}
	minimumEngagedDwell := recommendationReadDwellThreshold(estimatedReadTimeMS, 35, 20*1000)
	strongDwell := recommendationReadDwellThreshold(estimatedReadTimeMS, 80, 45*1000)
	if foregroundTimeMS >= strongDwell || (foregroundTimeMS >= minimumEngagedDwell && scrollProgressPercent >= recommendationReadMinimumProgress) {
		return "qualified", nil
	}
	if foregroundTimeMS < recommendationReadMinimumDwellMS && scrollProgressPercent < recommendationReadQuickBounceProgress {
		return "quick_bounce", nil
	}
	return "neutral", nil
}

func recommendationReadDwellThreshold(estimatedReadTimeMS, numerator, maximum int64) int64 {
	rounded := (estimatedReadTimeMS*numerator + 50) / 100
	if rounded < recommendationReadMinimumDwellMS {
		return recommendationReadMinimumDwellMS
	}
	if rounded > maximum {
		return maximum
	}
	return rounded
}

func EstimatePostReadTime(content string) time.Duration {
	var cjkCharacters, latinWords int64
	inLatinWord := false
	for _, r := range content {
		if isRecommendationReadCJKLike(r) {
			if inLatinWord {
				latinWords++
				inLatinWord = false
			}
			cjkCharacters++
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			inLatinWord = true
			continue
		}
		if inLatinWord {
			latinWords++
			inLatinWord = false
		}
	}
	if inLatinWord {
		latinWords++
	}
	minuteMilliseconds := int64(time.Minute / time.Millisecond)
	denominator := int64(recommendationReadCJKCharactersPerMinute) * int64(recommendationReadLatinWordsPerMinute)
	numerator := cjkCharacters*minuteMilliseconds*int64(recommendationReadLatinWordsPerMinute) + latinWords*minuteMilliseconds*int64(recommendationReadCJKCharactersPerMinute)
	rawMilliseconds := (numerator + denominator - 1) / denominator
	if rawMilliseconds < recommendationReadMinimumEstimateMS {
		rawMilliseconds = recommendationReadMinimumEstimateMS
	}
	if rawMilliseconds > recommendationReadMaximumEstimateMS {
		rawMilliseconds = recommendationReadMaximumEstimateMS
	}
	return time.Duration(rawMilliseconds) * time.Millisecond
}

func isRecommendationReadCJKLike(r rune) bool {
	switch {
	case r >= 0x3400 && r <= 0x4DBF, r >= 0x4E00 && r <= 0x9FFF, r >= 0xF900 && r <= 0xFAFF,
		r >= 0x20000 && r <= 0x2FA1F, r >= 0x3040 && r <= 0x30FF, r >= 0x31F0 && r <= 0x31FF,
		r >= 0xAC00 && r <= 0xD7AF, r >= 0x1100 && r <= 0x11FF, r >= 0x3130 && r <= 0x318F:
		return true
	default:
		return false
	}
}
