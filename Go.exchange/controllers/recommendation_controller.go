package controllers

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	defaultRecommendationLimit              = 20
	maxRecommendationLimit                  = 50
	recommendationFeedbackArticleLimit      = recommendation.ProfileReplyLimit
	recommendationRecentViewArticleLimit    = recommendation.ProfileRecentViewLimit
	recommendationCandidateRetrievalVersion = "social_semantic_materialized_profile_v4"
)

type articleBehaviorSignal struct {
	Behavior models.ArticleBehavior
}

type embeddingCandidate struct {
	ArticleID                  uint
	PositiveSemanticSimilarity float64
	FromSemantic               bool
	FromFollowing              bool
	FromRecent                 bool
	FromTrending               bool
	WasSoftServed              bool
	LastServedAt               time.Time
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
	cfg := normalizedRecommendationConfig()

	profile, err := loadMaterializedUserInterestProfile(userID, now, cfg)
	if err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
		return
	}
	loadedAuthors := make(map[uint]struct{})

	served, err := loadRecommendationServedHistory(userID, now, cfg)
	if err != nil {
		log.Printf("[Recommendation] served history for user %d: %v", userID, err)
		metrics.RecordRecommendationServedHistoryLoadFailure()
		served = map[uint]servedArticle{}
	}
	freshSet, err := loadRecommendationCandidateSet(userID, profile, served, now, cfg, false)
	if err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
		return
	}
	recordRecallMetrics(freshSet)
	freshHydrated, err := hydrateRecommendationCandidates(freshSet.Candidates, now)
	if err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
		return
	}
	if err := loadMaterializedCandidateAuthorContext(userID, &profile, freshHydrated, loadedAuthors, cfg); err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
		return
	}
	rankedFresh := rankRecommendationCandidates(profile, freshHydrated, now, cfg)
	selected := selectRecommendationCandidates(rankedFresh, nil, limit, cfg, now, recommendationSelectionFresh, requestID)

	if len(selected) < limit {
		softSet, softErr := loadRecommendationCandidateSet(userID, profile, served, now, cfg, true)
		if softErr != nil {
			recommendationErrorResponse(ctx, softErr, recommendationStrategyID(profile))
			return
		}
		recordRecallMetrics(softSet)
		softHydrated, softErr := hydrateRecommendationCandidates(softSet.Candidates, now)
		if softErr != nil {
			recommendationErrorResponse(ctx, softErr, recommendationStrategyID(profile))
			return
		}
		if err := loadMaterializedCandidateAuthorContext(userID, &profile, softHydrated, loadedAuthors, cfg); err != nil {
			recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
			return
		}
		rankedSoft := rankRecommendationCandidates(profile, softHydrated, now, cfg)
		selected = selectRecommendationCandidates(rankedSoft, selected, limit, cfg, now, recommendationSelectionSoft, requestID)
		freshSet = mergeCandidateSets(freshSet, softSet, recommendationCandidateCaps(profile, cfg).Merged)
	}

	recommendations := selectedRecommendationResponses(selected)
	trackedCount, trackingErr := attachRecommendationTracking(userID, requestID, profile, selected, recommendations, now)
	if trackingErr != nil {
		log.Printf("[RecommendationTelemetry] omit tracking metadata: %v", trackingErr)
	}
	metrics.AddRecommendationTrackingResults("tracked", trackedCount)
	metrics.AddRecommendationTrackingResults("untracked", len(recommendations)-trackedCount)
	recordResultMetrics(selected)

	duration := time.Since(started)
	strategyID := recommendationStrategyID(profile)
	outcome := "success"
	if len(recommendations) == 0 {
		outcome = "empty"
	}
	metrics.RecordRecommendationRequest(outcome, strategyID)
	metrics.ObserveRecommendationCandidateCount(len(freshSet.Candidates))
	metrics.ObserveRecommendationResultCount(len(recommendations))
	metrics.ObserveRecommendationGenerationDuration(strategyID, duration)

	requestRecord := models.RecommendationRequest{
		RequestID: requestID, UserID: userID, Scene: recommendationScene, StrategyID: strategyID,
		RankerVersion: recommendationRankerVersion, RankerConfigHash: recommendationRankerConfigHash(cfg),
		ProfileVersion: profile.ProfileVersion, ProfileConfigHash: profile.ProfileConfigHash,
		ProfileStatus: profile.ProfileStatus, ProfileAgeMS: profile.ProfileAgeMS,
		RequestedLimit: limit, CandidateCount: len(freshSet.Candidates), ResultCount: len(recommendations),
		TrackedResultCount: trackedCount, PersonalizedSignalCount: profile.PersonalizedSignalCount,
		SemanticCandidateCount: freshSet.SemanticCount, FollowingCandidateCount: freshSet.FollowingCount,
		RecentCandidateCount: freshSet.RecentCount, TrendingCandidateCount: freshSet.TrendingCount,
		MergedCandidateCount: len(freshSet.Candidates), PositiveSignalCount: profile.PositiveSignalCount,
		NegativeSignalCount: profile.NegativeSignalCount, InNetworkResultCount: countSelectedClass(selected, func(item selectedRecommendation) bool { return item.IsInNetwork }),
		OutOfNetworkResultCount:     countSelectedClass(selected, func(item selectedRecommendation) bool { return !item.IsInNetwork }),
		NovelAuthorResultCount:      countSelectedClass(selected, func(item selectedRecommendation) bool { return item.IsNovelAuthor }),
		ExplorationTargetCount:      recommendationExplorationTarget(limit, cfg),
		ExplorationOpportunityCount: countSelectedClass(selected, func(item selectedRecommendation) bool { return item.ExplorationOpportunity }),
		ExplorationResultCount: countSelectedClass(selected, func(item selectedRecommendation) bool {
			return item.SelectionMode == recommendationResultSelectionExploration
		}),
		SoftServedFallbackCount: countSelectedClass(selected, func(item selectedRecommendation) bool { return item.Candidate.WasSoftServed }),
		PersonalizationMode:     recommendationPersonalizationMode(profile, freshSet.FollowingCount),
		FallbackReason:          recommendationFallbackReason(profile.PositiveSignalCount, len(recommendations), limit),
		GenerationLatencyMS:     duration.Milliseconds(), CreatedAt: now,
	}
	traces := buildRecommendationResultTraces(requestRecord, selected, now, cfg)
	if err := persistRecommendationServingTrace(requestRecord, traces); err != nil {
		log.Printf("[RecommendationTelemetry] persist serving trace %s: %v", requestID, err)
		metrics.RecordRecommendationTracePersistFailure()
	}
	ctx.JSON(http.StatusOK, recommendations)
}

func recommendationErrorResponse(ctx *gin.Context, err error, strategyID string) {
	if strategyID == "" {
		strategyID = recommendationStrategyID(userInterestProfile{})
	}
	metrics.RecordRecommendationRequest("error", strategyID)
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func recommendationPersonalizationMode(profile userInterestProfile, followingCount int) string {
	if len(profile.PositiveVector) > 0 {
		return "semantic_social"
	}
	if followingCount > 0 {
		return "social_only"
	}
	return "cold_start"
}

func recordRecallMetrics(set recommendationCandidateSet) {
	metrics.AddRecommendationRecallCandidates("semantic", set.SemanticCount)
	metrics.AddRecommendationRecallCandidates("following", set.FollowingCount)
	metrics.AddRecommendationRecallCandidates("recent", set.RecentCount)
	metrics.AddRecommendationRecallCandidates("trending", set.TrendingCount)
	metrics.AddRecommendationRecallCandidates("merged", len(set.Candidates))
}

func recordResultMetrics(selected []selectedRecommendation) {
	for _, item := range selected {
		if item.Candidate.FromSemantic {
			metrics.AddRecommendationResultsBySource("semantic", 1)
		}
		if item.Candidate.FromFollowing {
			metrics.AddRecommendationResultsBySource("following", 1)
		}
		if item.Candidate.FromRecent {
			metrics.AddRecommendationResultsBySource("recent", 1)
		}
		if item.Candidate.FromTrending {
			metrics.AddRecommendationResultsBySource("trending", 1)
		}
		if item.IsInNetwork {
			metrics.AddRecommendationResultsByClass("in_network", 1)
		} else {
			metrics.AddRecommendationResultsByClass("out_of_network", 1)
		}
		if item.IsNovelAuthor {
			metrics.AddRecommendationResultsByClass("novel_author", 1)
		}
		if item.Candidate.WasSoftServed {
			metrics.AddRecommendationResultsByClass("soft_served_fallback", 1)
		}
		if item.SelectionMode == recommendationResultSelectionExploration {
			metrics.AddRecommendationResultsBySelection("exploration", item.ExplorationReason, 1)
		} else {
			metrics.AddRecommendationResultsBySelection("ranked", "none", 1)
		}
	}
}

func countSelectedClass(items []selectedRecommendation, predicate func(selectedRecommendation) bool) int {
	count := 0
	for _, item := range items {
		if predicate(item) {
			count++
		}
	}
	return count
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

func strconvUint(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func articleIDs(articles []models.Article) []uint {
	ids := make([]uint, 0, len(articles))
	for _, article := range articles {
		if article.ID != 0 {
			ids = append(ids, article.ID)
		}
	}
	return ids
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
