package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

const (
	postViewTelemetryMaxBatchEvents = 50
	postViewTelemetryMaxBodyBytes   = 64 * 1024
)

type postViewEventInput struct {
	EventID    string `json:"event_id"`
	PostID     uint   `json:"post_id"`
	OccurredAt string `json:"occurred_at"`
	Source     string `json:"source"`
}

type postViewEventBatchRequest struct {
	Events []postViewEventInput `json:"events"`
}

type postViewEventResult struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type postViewEventBatchResponse struct {
	Accepted int                   `json:"accepted"`
	Rejected int                   `json:"rejected"`
	Results  []postViewEventResult `json:"results"`
}

type validatedPostViewEvent struct {
	EventID    string
	PostID     uint
	OccurredAt time.Time
	Source     string
}

var (
	postViewTelemetryNow    = func() time.Time { return time.Now().UTC() }
	allowPostViewEvents     = enforcePostViewEventRateLimit
	postViewRateLimitScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return current
`)
)

func NewPostViewEventsHandler(publisher eventing.BatchPublisher) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		recordPostViewEvents(ctx, publisher)
	}
}

func RecordPostViewEvents(ctx *gin.Context) {
	recordPostViewEvents(ctx, nil)
}

func recordPostViewEvents(ctx *gin.Context, publisher eventing.BatchPublisher) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, postViewTelemetryMaxBodyBytes)
	var request postViewEventBatchRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 64 KiB"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid post view event batch"})
		return
	}
	if len(request.Events) == 0 || len(request.Events) > postViewTelemetryMaxBatchEvents {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "events must contain between 1 and 50 items"})
		return
	}

	allowed, err := allowPostViewEvents(userID, len(request.Events))
	if err != nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "post view telemetry rate limiter unavailable"})
		return
	}
	if !allowed {
		ctx.Header("Retry-After", "60")
		ctx.JSON(http.StatusTooManyRequests, gin.H{"error": "post view telemetry rate limit exceeded"})
		return
	}

	now := postViewTelemetryNow().UTC()
	response := postViewEventBatchResponse{Results: make([]postViewEventResult, len(request.Events))}
	valid := make([]validatedPostViewEvent, 0, len(request.Events))
	validIndexes := make([]int, 0, len(request.Events))
	for index, input := range request.Events {
		response.Results[index].EventID = strings.TrimSpace(input.EventID)
		event, reason := validatePostViewEvent(input, now)
		if reason != "" {
			response.Results[index].Status = "rejected"
			response.Results[index].Reason = reason
			continue
		}
		valid = append(valid, event)
		validIndexes = append(validIndexes, index)
	}
	if len(valid) == 0 {
		completePostViewEventResponse(&response)
		ctx.JSON(http.StatusUnprocessableEntity, response)
		return
	}

	envelopes := make([]eventing.Envelope, 0, len(valid))
	for _, event := range valid {
		envelope, err := eventing.NewPostViewedEnvelope(event.EventID, userID, event.PostID, event.OccurredAt, event.Source)
		if err != nil {
			ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid post view event"})
			return
		}
		envelopes = append(envelopes, envelope)
	}
	if publisher == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "post view publisher unavailable"})
		return
	}
	if err := publisher.PublishBatch(ctx.Request.Context(), envelopes); err != nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to publish post view events"})
		return
	}
	for _, index := range validIndexes {
		response.Results[index].Status = "accepted"
	}
	completePostViewEventResponse(&response)
	ctx.JSON(http.StatusAccepted, response)
}

func completePostViewEventResponse(response *postViewEventBatchResponse) {
	for _, result := range response.Results {
		if result.Status == "accepted" {
			response.Accepted++
		} else {
			response.Rejected++
		}
	}
}

func enforcePostViewEventRateLimit(userID uint, eventCount int) (bool, error) {
	if global.RedisDB == nil {
		return false, errors.New("redis is not initialized")
	}
	if userID == 0 || eventCount < 1 {
		return false, errors.New("post view rate-limit input is invalid")
	}
	minute := time.Now().UTC().Unix() / 60
	key := fmt.Sprintf("post:view:telemetry:rate:%d:%d", userID, minute)
	count, err := postViewRateLimitScript.Run(
		global.RedisDB,
		[]string{key},
		strconv.Itoa(eventCount),
		"70",
	).Int()
	if err != nil {
		return false, err
	}
	return count <= config.PostViewEventsPerMinute(), nil
}

func validatePostViewEvent(input postViewEventInput, now time.Time) (validatedPostViewEvent, string) {
	eventID := strings.TrimSpace(input.EventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return validatedPostViewEvent{}, "invalid_event_id"
	}
	if input.PostID == 0 {
		return validatedPostViewEvent{}, "invalid_post_id"
	}
	source := strings.TrimSpace(input.Source)
	if source != "post_detail" && source != "feed" {
		return validatedPostViewEvent{}, "invalid_source"
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.OccurredAt))
	if err != nil {
		return validatedPostViewEvent{}, "invalid_occurred_at"
	}
	occurredAt = occurredAt.UTC()
	if occurredAt.After(now.Add(config.RecommendationTelemetryMaxClockSkew())) {
		return validatedPostViewEvent{}, "occurred_at_in_future"
	}
	return validatedPostViewEvent{EventID: eventID, PostID: input.PostID, OccurredAt: occurredAt, Source: source}, ""
}
