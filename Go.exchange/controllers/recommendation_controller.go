package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	recommendationFeedbackPostLimit         = recommendation.ProfileReplyLimit
	recommendationRecentViewPostLimit       = recommendation.ProfileRecentViewLimit
	recommendationCandidateRetrievalVersion = "social_semantic_materialized_profile_rrf_v5"
	guestRecommendationSessionHeader        = "X-Guest-Recommendation-Session"
)

type postBehaviorSignal struct {
	Behavior models.PostBehavior
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

// RecommendationHandler is the HTTP adapter for the recommendation Service.
// The database is used only by the response mapper to hydrate API DTOs.
type RecommendationHandler struct {
	service        recommendation.Service
	db             *gorm.DB
	servingTimeout time.Duration
}

func NewRecommendationHandler(service recommendation.Service, db *gorm.DB, servingTimeout time.Duration) (*RecommendationHandler, error) {
	if service == nil {
		return nil, errors.New("recommendation service is required")
	}
	return &RecommendationHandler{service: service, db: db, servingTimeout: servingTimeout}, nil
}

func (handler *RecommendationHandler) GetPostRecommendations(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	handler.serve(ctx, recommendation.Viewer{Kind: recommendation.ViewerAuthenticated, UserID: userID})
}

func (handler *RecommendationHandler) GetPublicPostRecommendations(ctx *gin.Context) {
	guestSessionID, _ := parseGuestRecommendationSessionID(ctx.GetHeader(guestRecommendationSessionHeader))
	handler.serve(ctx, recommendation.Viewer{Kind: recommendation.ViewerGuest, GuestSessionID: guestSessionID})
}

func (handler *RecommendationHandler) serve(ctx *gin.Context, viewer recommendation.Viewer) {
	if handler == nil || handler.service == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation service is unavailable"})
		return
	}
	requestCtx := ctx.Request.Context()
	servingCtx := requestCtx
	cancel := func() {}
	if handler.servingTimeout > 0 {
		servingCtx, cancel = context.WithTimeout(requestCtx, handler.servingTimeout)
	}
	defer cancel()

	browserPrior, browserPrimary := parseRecommendationAcceptLanguageWithPrimary(ctx.GetHeader("Accept-Language"))
	requestedLimit := 0
	if parsed, err := strconv.Atoi(strings.TrimSpace(ctx.Query("limit"))); err == nil {
		requestedLimit = parsed
	}
	result, err := handler.service.Serve(servingCtx, recommendation.ServeRequest{
		Viewer: viewer, Limit: requestedLimit, BrowserLanguage: recommendation.LanguageContext{
			Browser: browserPrior, BrowserPrimary: browserPrimary,
		},
	})
	if err != nil {
		recommendationErrorResponse(ctx, err, result.StrategyID)
		return
	}
	if err := servingCtx.Err(); err != nil {
		recommendationErrorResponse(ctx, err, result.StrategyID)
		return
	}
	var responseDB *gorm.DB
	if handler.db != nil {
		responseDB = handler.db.WithContext(servingCtx)
	}
	recommendations, err := selectedRecommendationResponsesFromDB(responseDB, result.Selected, result.Now)
	if err != nil {
		recommendationErrorResponse(ctx, err, result.StrategyID)
		return
	}
	trackingByPost := make(map[uint]recommendation.TrackingFact, len(result.Tracking))
	for _, fact := range result.Tracking {
		trackingByPost[fact.PostID] = fact
	}
	for index := range recommendations {
		postID := recommendations[index].Post.ID
		if fact, exists := trackingByPost[postID]; exists {
			recommendations[index].Tracking = &recommendationTrackingResponse{
				RequestID: fact.RequestID, Position: fact.Position, Scene: fact.Scene,
				RankerVersion: fact.RankerVersion, RankerConfigHash: fact.RankerConfigHash,
				StrategyID: fact.StrategyID, Token: fact.Token, ExpiresAt: fact.ExpiresAt,
			}
		}
	}
	ctx.JSON(http.StatusOK, postRecommendationPageResponse{
		Items: recommendations, RequestID: result.RequestID, Depleted: result.Depleted,
	})
}

// These unbound handlers remain for routers assembled without API services in
// tests or utility processes. Production wiring always uses the injected handler.
func GetPostRecommendations(ctx *gin.Context) {
	ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation service is unavailable"})
}

func GetPublicPostRecommendations(ctx *gin.Context) {
	ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation service is unavailable"})
}

func recommendationErrorResponse(ctx *gin.Context, err error, _ string) {
	if err == nil {
		return
	}
	requestErr := ctx.Request.Context().Err()
	if requestErr != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		ctx.JSON(http.StatusGatewayTimeout, gin.H{"error": "recommendation timed out"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func strconvUint(id uint) string { return strconv.FormatUint(uint64(id), 10) }
