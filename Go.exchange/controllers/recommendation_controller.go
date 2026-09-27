package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
)

const guestRecommendationSessionHeader = "X-Guest-Recommendation-Session"

type RecommendedPostResponse struct {
	Post     postResponse                    `json:"post"`
	Score    float64                         `json:"score"`
	Tracking *recommendationTrackingResponse `json:"tracking,omitempty"`
}

type postRecommendationPageResponse struct {
	Items     []RecommendedPostResponse `json:"items"`
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
type RecommendationHandler struct {
	service        recommendation.Service
	responseMapper RecommendationResponseMapper
	servingTimeout time.Duration
}

func NewRecommendationHandler(service recommendation.Service, responseMapper RecommendationResponseMapper, servingTimeout time.Duration) (*RecommendationHandler, error) {
	if service == nil {
		return nil, errors.New("recommendation service is required")
	}
	if responseMapper == nil {
		return nil, errors.New("recommendation response mapper is required")
	}
	return &RecommendationHandler{service: service, responseMapper: responseMapper, servingTimeout: servingTimeout}, nil
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
	if handler == nil || handler.service == nil || handler.responseMapper == nil {
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
	recommendations, err := handler.responseMapper.Map(servingCtx, result.Selected, result.Now)
	if err != nil {
		recommendationErrorResponse(ctx, err, result.StrategyID)
		return
	}
	attachRecommendationTrackingFacts(recommendations, result.Tracking)
	ctx.JSON(http.StatusOK, postRecommendationPageResponse{
		Items: recommendations, RequestID: result.RequestID, Depleted: result.Depleted,
	})
}

func attachRecommendationTrackingFacts(recommendations []RecommendedPostResponse, facts []recommendation.TrackingFact) {
	trackingByPost := make(map[uint]recommendation.TrackingFact, len(facts))
	for _, fact := range facts {
		trackingByPost[fact.PostID] = fact
	}
	for index := range recommendations {
		if fact, exists := trackingByPost[recommendations[index].Post.ID]; exists {
			recommendations[index].Tracking = &recommendationTrackingResponse{
				RequestID: fact.RequestID, Position: fact.Position, Scene: fact.Scene,
				RankerVersion: fact.RankerVersion, RankerConfigHash: fact.RankerConfigHash,
				StrategyID: fact.StrategyID, Token: fact.Token, ExpiresAt: fact.ExpiresAt,
			}
		}
	}
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
