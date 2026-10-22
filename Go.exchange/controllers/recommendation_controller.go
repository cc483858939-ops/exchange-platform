package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const guestRecommendationSessionHeader = "X-Guest-Recommendation-Session"

type RecommendedPostResponse struct {
	Post     postResponse                    `json:"post"`
	Score    float64                         `json:"score"`
	Tracking *recommendationTrackingResponse `json:"tracking,omitempty"`
}

type postRecommendationPageResponse struct {
	Items              []RecommendedPostResponse         `json:"items"`
	RequestID          string                            `json:"request_id"`
	NextCursor         *string                           `json:"next_cursor"`
	Depleted           bool                              `json:"depleted"`
	ContinuationStatus recommendation.ContinuationStatus `json:"continuation_status"`
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
		writeAPIError(ctx, "UNAUTHORIZED")
		return
	}
	handler.serve(ctx, recommendation.Viewer{Kind: recommendation.ViewerAuthenticated, UserID: userID})
}

func (handler *RecommendationHandler) GetPublicPostRecommendations(ctx *gin.Context) {
	guestSessionID, ok := parseGuestRecommendationSessionID(ctx.GetHeader(guestRecommendationSessionHeader))
	if !ok {
		writeAPIError(ctx, "INVALID_REQUEST")
		return
	}
	handler.serve(ctx, recommendation.Viewer{Kind: recommendation.ViewerGuest, GuestSessionID: guestSessionID})
}

func (handler *RecommendationHandler) serve(ctx *gin.Context, viewer recommendation.Viewer) {
	if handler == nil || handler.service == nil || handler.responseMapper == nil {
		writeInternalAPIErrorCode(ctx, "SERVICE_UNAVAILABLE")
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
		Viewer: viewer, Limit: requestedLimit, Cursor: strings.TrimSpace(ctx.Query("cursor")), BrowserLanguage: recommendation.LanguageContext{
			Browser: browserPrior, BrowserPrimary: browserPrimary,
		},
	})
	if result.RequestID != "" {
		trace.SpanFromContext(requestCtx).SetAttributes(attribute.String("recommendation.request_id", result.RequestID))
	}
	if err != nil {
		recommendationErrorResponse(ctx, err)
		return
	}
	if err := servingCtx.Err(); err != nil {
		recommendationErrorResponse(ctx, err)
		return
	}
	recommendations, err := func() ([]RecommendedPostResponse, error) {
		if !recommendation.TracingEnabled() {
			return handler.responseMapper.Map(servingCtx, viewer.UserID, result.Selected, result.Now)
		}
		mapCtx, mapSpan := trace.SpanFromContext(requestCtx).TracerProvider().Tracer("Go.exchange/controllers/recommendation").Start(
			servingCtx,
			"recommendation.response.map",
		)
		defer mapSpan.End()
		mapped, mapErr := handler.responseMapper.Map(mapCtx, viewer.UserID, result.Selected, result.Now)
		if mapErr != nil {
			recordRecommendationControllerSpanError(mapSpan, mapErr)
		}
		return mapped, mapErr
	}()
	if err != nil {
		recommendationErrorResponse(ctx, err)
		return
	}
	mappedSelected := selectedForMappedRecommendations(result.Selected, recommendations)
	if finalizer, ok := handler.service.(recommendation.MappedServingFinalizer); ok {
		facts, err := finalizer.FinalizeMappedServing(servingCtx, result, mappedSelected)
		if err != nil {
			recommendationErrorResponse(ctx, err)
			return
		}
		attachRecommendationTrackingFacts(recommendations, facts)
	} else {
		attachRecommendationTrackingFacts(recommendations, result.Tracking)
		if finalizer, ok := handler.service.(recommendation.ServingFinalizer); ok {
			if err := finalizer.FinalizeServing(servingCtx, result, mappedSelected); err != nil {
				recommendationErrorResponse(ctx, err)
				return
			}
		}
	}
	var nextCursor *string
	if result.NextCursor != "" {
		value := result.NextCursor
		nextCursor = &value
	}
	continuationStatus := result.ContinuationStatus
	if continuationStatus == "" {
		continuationStatus = recommendation.ContinuationAvailable
		if result.Depleted {
			continuationStatus = recommendation.ContinuationExhausted
		}
	}
	ctx.JSON(http.StatusOK, postRecommendationPageResponse{
		Items: recommendations, RequestID: result.RequestID, NextCursor: nextCursor,
		Depleted: result.Depleted, ContinuationStatus: continuationStatus,
	})
}

func selectedForMappedRecommendations(selected []recommendation.SelectedCandidate, mapped []RecommendedPostResponse) []recommendation.SelectedCandidate {
	byID := make(map[uint]recommendation.SelectedCandidate, len(selected))
	for _, item := range selected {
		byID[item.Post.ID] = item
	}
	result := make([]recommendation.SelectedCandidate, 0, len(mapped))
	for _, item := range mapped {
		if selected, exists := byID[item.Post.ID]; exists {
			result = append(result, selected)
		}
	}
	return result
}

func recordRecommendationControllerSpanError(span trace.Span, err error) {
	if err == nil {
		return
	}
	errorType := fmt.Sprintf("%T", err)
	if errors.Is(err, context.Canceled) {
		errorType = "context.canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		errorType = "context.deadline_exceeded"
	}
	span.RecordError(errors.New("recommendation response mapping failed"), trace.WithAttributes(attribute.String("error.type", errorType)))
	span.SetStatus(codes.Error, "recommendation response mapping failed")
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

func recommendationErrorResponse(ctx *gin.Context, err error) {
	if err == nil {
		return
	}
	requestErr := ctx.Request.Context().Err()
	if requestErr != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeAPIError(ctx, "REQUEST_TIMEOUT")
		return
	}
	if errors.Is(err, recommendation.ErrInvalidCursor) {
		writeAPIError(ctx, "INVALID_CURSOR")
		return
	}
	if errors.Is(err, recommendation.ErrCursorExpired) {
		writeAPIError(ctx, "CURSOR_EXPIRED")
		return
	}
	writeInternalAPIError(ctx, err)
}

func strconvUint(id uint) string { return strconv.FormatUint(uint64(id), 10) }
