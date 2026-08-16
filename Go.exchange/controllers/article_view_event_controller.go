package controllers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	articleViewTelemetryMaxBatchEvents = 50
	articleViewTelemetryMaxBodyBytes   = 64 * 1024
)

type articleViewEventInput struct {
	EventID    string `json:"event_id"`
	ArticleID  uint   `json:"article_id"`
	OccurredAt string `json:"occurred_at"`
}

type articleViewEventBatchRequest struct {
	Events []articleViewEventInput `json:"events"`
}

type articleViewEventResult struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type articleViewEventBatchResponse struct {
	Accepted int                      `json:"accepted"`
	Rejected int                      `json:"rejected"`
	Results  []articleViewEventResult `json:"results"`
}

type validatedArticleViewEvent struct {
	EventID    string
	ArticleID  uint
	OccurredAt time.Time
}

var articleViewTelemetryNow = func() time.Time { return time.Now().UTC() }

func NewArticleViewEventsHandler(publisher eventing.BatchPublisher) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		recordArticleViewEvents(ctx, publisher)
	}
}

func RecordArticleViewEvents(ctx *gin.Context) {
	recordArticleViewEvents(ctx, nil)
}

func recordArticleViewEvents(ctx *gin.Context, publisher eventing.BatchPublisher) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, articleViewTelemetryMaxBodyBytes)
	var request articleViewEventBatchRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 64 KiB"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid article view event batch"})
		return
	}
	if len(request.Events) == 0 || len(request.Events) > articleViewTelemetryMaxBatchEvents {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "events must contain between 1 and 50 items"})
		return
	}

	now := articleViewTelemetryNow().UTC()
	response := articleViewEventBatchResponse{Results: make([]articleViewEventResult, len(request.Events))}
	valid := make([]validatedArticleViewEvent, 0, len(request.Events))
	validIndexes := make([]int, 0, len(request.Events))
	for index, input := range request.Events {
		response.Results[index].EventID = strings.TrimSpace(input.EventID)
		event, reason := validateArticleViewEvent(input, now)
		if reason != "" {
			response.Results[index].Status = "rejected"
			response.Results[index].Reason = reason
			continue
		}
		valid = append(valid, event)
		validIndexes = append(validIndexes, index)
	}
	if len(valid) == 0 {
		completeArticleViewEventResponse(&response)
		ctx.JSON(http.StatusUnprocessableEntity, response)
		return
	}

	envelopes := make([]eventing.Envelope, 0, len(valid))
	for _, event := range valid {
		envelope, err := eventing.NewArticleViewedEnvelope(event.EventID, userID, event.ArticleID, event.OccurredAt, "article_detail")
		if err != nil {
			ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid article view event"})
			return
		}
		envelopes = append(envelopes, envelope)
	}
	if publisher == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "article view publisher unavailable"})
		return
	}
	if err := publisher.PublishBatch(ctx.Request.Context(), envelopes); err != nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to publish article view events"})
		return
	}
	for _, index := range validIndexes {
		response.Results[index].Status = "accepted"
	}
	completeArticleViewEventResponse(&response)
	ctx.JSON(http.StatusAccepted, response)
}

func completeArticleViewEventResponse(response *articleViewEventBatchResponse) {
	for _, result := range response.Results {
		if result.Status == "accepted" {
			response.Accepted++
		} else {
			response.Rejected++
		}
	}
}

func validateArticleViewEvent(input articleViewEventInput, now time.Time) (validatedArticleViewEvent, string) {
	eventID := strings.TrimSpace(input.EventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return validatedArticleViewEvent{}, "invalid_event_id"
	}
	if input.ArticleID == 0 {
		return validatedArticleViewEvent{}, "invalid_article_id"
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.OccurredAt))
	if err != nil {
		return validatedArticleViewEvent{}, "invalid_occurred_at"
	}
	occurredAt = occurredAt.UTC()
	if occurredAt.After(now.Add(config.RecommendationTelemetryMaxClockSkew())) {
		return validatedArticleViewEvent{}, "occurred_at_in_future"
	}
	return validatedArticleViewEvent{EventID: eventID, ArticleID: input.ArticleID, OccurredAt: occurredAt}, ""
}
