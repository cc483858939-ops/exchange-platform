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
	recommendationTopCategoryCount          = 8
	recommendationCategoryCandidateCap      = 200
	recommendationRecentCandidateCap        = 150
	recommendationPopularCandidateCap       = 150
	recommendationMergedCandidateCap        = 500
	recommendationCandidateRetrievalVersion = "multi_source_v1"
)

type articleBehaviorSignal struct {
	Behavior models.ArticleBehavior
	Article  models.Article
}

type userInterestProfile struct {
	Categories              map[string]float64
	Tags                    map[string]float64
	InteractedArticleIDs    map[uint]struct{}
	PersonalizedSignalCount int
}

type recommendedArticleResponse struct {
	ID            uint                            `json:"id"`
	Title         string                          `json:"title"`
	Content       string                          `json:"content"`
	Preview       string                          `json:"preview"`
	Summary       string                          `json:"summary"`
	CoverImageURL string                          `json:"cover_image_url"`
	Tags          []string                        `json:"tags"`
	Category      string                          `json:"category"`
	LikeCount     int64                           `json:"like_count"`
	CommentCount  int64                           `json:"comment_count"`
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
	if len(behaviors) == 0 {
		return nil, nil
	}

	articleIDs := make([]uint, 0, len(behaviors))
	seenIDs := make(map[uint]struct{}, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID == 0 {
			continue
		}
		if _, exists := seenIDs[behavior.ArticleID]; exists {
			continue
		}
		seenIDs[behavior.ArticleID] = struct{}{}
		articleIDs = append(articleIDs, behavior.ArticleID)
	}
	if len(articleIDs) == 0 {
		return nil, nil
	}

	var articles []models.Article
	if err := global.Db.
		Select("id,tags,category").
		Where("id IN ?", articleIDs).
		Find(&articles).Error; err != nil {
		return nil, err
	}

	articleByID := make(map[uint]models.Article, len(articles))
	for _, article := range articles {
		articleByID[article.ID] = article
	}

	signals := make([]articleBehaviorSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		article, exists := articleByID[behavior.ArticleID]
		if !exists {
			continue
		}
		signals = append(signals, articleBehaviorSignal{Behavior: behavior, Article: article})
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
	cfg := normalizedRulesV3RecommendationConfig()
	lookbackStart := now.AddDate(0, 0, -cfg.FeedbackLookbackDays)
	behaviors, err := loadRecommendationBehaviorSignals(userID)
	if err != nil {
		metrics.RecordRecommendationRequest("error", "unknown")
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	feedback, err := loadRecommendationFeedbackSignals(userID, lookbackStart)
	if err != nil {
		metrics.RecordRecommendationRequest("error", "unknown")
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	reactions, err := loadRecommendationReactionStates(userID)
	if err != nil {
		metrics.RecordRecommendationRequest("error", "unknown")
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	profile := buildRulesV3InterestProfile(behaviors, feedback, reactions, now, cfg)
	strategyID := recommendationStrategyID(profile)
	candidates, err := loadRulesV3Candidates(userID, profile, lookbackStart, now, limit)
	if err != nil {
		metrics.RecordRecommendationRequest("error", strategyID)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	recommendations := recommendRulesV3Articles(profile, candidates, now, cfg, limit)
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
	requestRecord := models.RecommendationRequest{RequestID: requestID, UserID: userID, Scene: recommendationScene, StrategyID: strategyID, RankerVersion: recommendationRankerVersion, RankerConfigHash: recommendationRankerConfigHash(cfg), RequestedLimit: limit, CandidateCount: len(candidates), ResultCount: len(recommendations), TrackedResultCount: trackedCount, PersonalizedSignalCount: profile.PersonalizedSignalCount, FallbackReason: recommendationFallbackReason(profile.PersonalizedSignalCount, len(recommendations), limit), GenerationLatencyMS: duration.Milliseconds(), CreatedAt: now}
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

func freshnessScore(createdAt time.Time, now time.Time) float64 {
	age := now.Sub(createdAt)
	switch {
	case age <= 7*24*time.Hour:
		return 1
	case age <= 30*24*time.Hour:
		return 0.5
	default:
		return 0
	}
} //根据时间算分

func normalizeRecommendationLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

func recommendationTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	return tags
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
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids
} //把文章id转化成数组
