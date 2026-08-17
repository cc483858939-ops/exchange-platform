package controllers

import (
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	defaultRecommendationLimit              = 20
	maxRecommendationLimit                  = 50
	recommendationSemanticCandidateCap      = 200
	recommendationRecentCandidateCap        = 150
	recommendationPopularCandidateCap       = 150
	recommendationColdStartRecentCap        = 200
	recommendationColdStartPopularCap       = 200
	recommendationMergedCandidateCap        = 500
	recommendationFeedbackArticleLimit      = 500
	recommendationRecentViewArticleLimit    = 200
	recommendationCandidateRetrievalVersion = "semantic_multi_source_v1"
)

type articleBehaviorSignal struct {
	Behavior models.ArticleBehavior
}

type userInterestProfile struct {
	Vector                  []float32
	InteractedArticleIDs    map[uint]struct{}
	PersonalizedSignalCount int
}

type embeddingCandidate struct {
	ArticleID          uint
	SemanticSimilarity float64
	FromSemantic       bool
	FromRecent         bool
	FromPopular        bool
}

type recommendedArticleResponse struct {
	ID            uint                            `json:"id"`
	Title         string                          `json:"title"`
	Content       string                          `json:"content"`
	Preview       string                          `json:"preview"`
	CoverImageURL string                          `json:"cover_image_url"`
	LikeCount     int64                           `json:"like_count"`
	CommentCount  int64                           `json:"comment_count"`
	ViewCount     int64                           `json:"view_count"`
	CreatedAt     time.Time                       `json:"created_at"`
	Author        publicAuthorResponse            `json:"author"`
	Score         float64                         `json:"score"`
	Tracking      *recommendationTrackingResponse `json:"tracking,omitempty"`
}

var loadRecommendationBehaviorSignals = func(userID uint) ([]articleBehaviorSignal, error) {
	if userID == 0 {
		return nil, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var behaviors []models.ArticleBehavior
	if err := global.Db.
		Where("user_id = ? AND action = ?", userID, ArticleBehaviorActionView).
		Order("last_seen_at DESC, id DESC").
		Limit(recommendationRecentViewArticleLimit).
		Find(&behaviors).Error; err != nil {
		return nil, err
	}
	signals := make([]articleBehaviorSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID != 0 {
			signals = append(signals, articleBehaviorSignal{Behavior: behavior})
		}
	}
	return signals, nil
}

func strconvUint(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func articleIDs(articles []models.Article) []uint {
	ids := make([]uint, 0, len(articles))
	for _, article := range articles {
		if article.ID != 0 {
			ids = append(ids, article.ID)
		}
	}
	return ids
}

func GetArticleRecommendations(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	started := time.Now()
	now := started.UTC()
	requestID := uuid.NewString()
	limit := parseRecommendationLimit(ctx.Query("limit"))
	cfg := normalizedEmbeddingRecommendationConfig()
	lookbackStart := now.AddDate(0, 0, -cfg.FeedbackLookbackDays)

	behaviors, err := loadRecommendationBehaviorSignals(userID)
	if err != nil {
		metrics.RecordRecommendationRequest("error", recommendationStrategyID(userInterestProfile{}))
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	feedback, err := loadRecommendationFeedbackSignals(userID, lookbackStart)
	if err != nil {
		metrics.RecordRecommendationRequest("error", recommendationStrategyID(userInterestProfile{}))
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	reactions, err := loadRecommendationReactionStates(userID)
	if err != nil {
		metrics.RecordRecommendationRequest("error", recommendationStrategyID(userInterestProfile{}))
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	profile, err := buildEmbeddingInterestProfile(behaviors, feedback, reactions, now, cfg)
	if err != nil {
		metrics.RecordRecommendationRequest("error", recommendationStrategyID(profile))
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	strategyID := recommendationStrategyID(profile)
	candidates, err := loadEmbeddingFeedCandidates(userID, profile, lookbackStart, now)
	if err != nil {
		metrics.RecordRecommendationRequest("error", strategyID)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	articles, err := hydrateEmbeddingCandidates(candidates, now)
	if err != nil {
		metrics.RecordRecommendationRequest("error", strategyID)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	recommendations := rankEmbeddingCandidates(profile, candidates, articles, now, cfg, limit)
	trackedCount, trackingErr := attachRecommendationTracking(userID, requestID, profile, recommendations, now)
	if trackingErr != nil {
		log.Printf("[RecommendationTelemetry] omit tracking metadata: %v", trackingErr)
	}
	metrics.AddRecommendationTrackingResults("tracked", trackedCount)
	metrics.AddRecommendationTrackingResults("untracked", len(recommendations)-trackedCount)
	outcome := "success"
	if len(recommendations) == 0 {
		outcome = "empty"
	}
	duration := time.Since(started)
	metrics.RecordRecommendationRequest(outcome, strategyID)
	metrics.ObserveRecommendationCandidateCount(len(candidates))
	metrics.ObserveRecommendationResultCount(len(recommendations))
	metrics.ObserveRecommendationGenerationDuration(strategyID, duration)
	requestRecord := models.RecommendationRequest{
		RequestID: requestID, UserID: userID, Scene: recommendationScene, StrategyID: strategyID,
		RankerVersion: recommendationRankerVersion, RankerConfigHash: recommendationRankerConfigHash(cfg),
		RequestedLimit: limit, CandidateCount: len(candidates), ResultCount: len(recommendations),
		TrackedResultCount: trackedCount, PersonalizedSignalCount: profile.PersonalizedSignalCount,
		FallbackReason:      recommendationFallbackReason(profile.PersonalizedSignalCount, len(recommendations), limit),
		GenerationLatencyMS: duration.Milliseconds(), CreatedAt: now,
	}
	if err := persistRecommendationRequest(requestRecord); err != nil {
		log.Printf("[RecommendationTelemetry] persist request %s: %v", requestID, err)
		metrics.RecordRecommendationRequestLogFailure()
	}
	ctx.JSON(http.StatusOK, recommendations)
}

func parseRecommendationLimit(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultRecommendationLimit
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultRecommendationLimit
	}
	if limit > maxRecommendationLimit {
		return maxRecommendationLimit
	}
	return limit
}

func articleIDList(set map[uint]struct{}) []uint {
	if len(set) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(set))
	for id := range set {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
