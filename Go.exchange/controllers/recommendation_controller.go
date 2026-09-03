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
	recommendationFeedbackPostLimit         = recommendation.ProfileReplyLimit
	recommendationRecentViewPostLimit       = recommendation.ProfileRecentViewLimit
	recommendationCandidateRetrievalVersion = "social_semantic_materialized_profile_rrf_v5"
)

type postBehaviorSignal struct {
	Behavior models.PostBehavior
}

type embeddingCandidate struct {
	PostID                     uint
	PositiveSemanticSimilarity float64
	SemanticRank               int
	FollowingRank              int
	RecentRank                 int
	TrendingRank               int
	FusionScore                float64
	SourceCount                int
	FromSemantic               bool
	FromFollowing              bool
	FromRecent                 bool
	FromTrending               bool
	WasSoftServed              bool
	LastServedAt               time.Time
}

type recommendedPostResponse struct {
	Post     postResponse                    `json:"post"`
	Score    float64                         `json:"score"`
	Tracking *recommendationTrackingResponse `json:"tracking,omitempty"`
}

type postRecommendationPageResponse struct {
	Items     []recommendedPostResponse `json:"items"`
	RequestID string                    `json:"request_id"`
	Depleted  bool                      `json:"depleted"`
}

var recommendationServingPathForHandler = serveRecommendationCandidatePath
var selectedRecommendationResponsesForHandler = selectedRecommendationResponses
var attachRecommendationTrackingForHandler = attachRecommendationTracking

func buildPostRecommendationPageResponse(requestID string, recommendations []recommendedPostResponse) postRecommendationPageResponse {
	if recommendations == nil {
		recommendations = make([]recommendedPostResponse, 0)
	}
	return postRecommendationPageResponse{
		Items:     recommendations,
		RequestID: requestID,
		Depleted:  len(recommendations) == 0,
	}
}

func GetPostRecommendations(ctx *gin.Context) {
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

	serving, err := recommendationServingPathForHandler(userID, uint(limit), cfg, now, requestID)
	if err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(serving.Profile))
		return
	}
	profile := serving.Profile
	freshSet := serving.FreshSet
	selected := serving.Selected
	if serving.ServedHistoryLoadError != nil {
		log.Printf("[Recommendation] served history for user %d: %v", userID, serving.ServedHistoryLoadError)
		metrics.RecordRecommendationServedHistoryLoadFailure()
	}
	for _, recallSet := range serving.RecallSets {
		recordRecallMetrics(recallSet)
	}

	recommendations, err := selectedRecommendationResponsesForHandler(selected)
	if err != nil {
		recommendationErrorResponse(ctx, err, recommendationStrategyID(profile))
		return
	}
	trackedCount, trackingErr := attachRecommendationTrackingForHandler(userID, requestID, profile, selected, recommendations, now)
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
	explorationCounts := recommendationExplorationCountsForSelection(selected, recommendationExplorationTarget(limit, cfg))

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
		OutOfNetworkResultCount: countSelectedClass(selected, func(item selectedRecommendation) bool { return !item.IsInNetwork }),
		NovelAuthorResultCount:  countSelectedClass(selected, func(item selectedRecommendation) bool { return item.IsNovelAuthor }),
		ExplorationTargetCount:  explorationCounts.Target, ExplorationOpportunityCount: explorationCounts.Opportunities,
		ExplorationResultCount:  explorationCounts.Results,
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
	ctx.JSON(http.StatusOK, buildPostRecommendationPageResponse(requestID, recommendations))
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

type recommendationExplorationCounts struct {
	Target        int
	Opportunities int
	Results       int
}

func recommendationExplorationCountsForSelection(selected []selectedRecommendation, target int) recommendationExplorationCounts {
	return recommendationExplorationCounts{
		Target:        target,
		Opportunities: countSelectedClass(selected, func(item selectedRecommendation) bool { return item.ExplorationOpportunity }),
		Results: countSelectedClass(selected, func(item selectedRecommendation) bool {
			return item.SelectionMode == recommendationResultSelectionExploration
		}),
	}
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

func postIDs(posts []models.Post) []uint {
	ids := make([]uint, 0, len(posts))
	for _, post := range posts {
		if post.ID != 0 {
			ids = append(ids, post.ID)
		}
	}
	return ids
}

func postIDList(set map[uint]struct{}) []uint {
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
