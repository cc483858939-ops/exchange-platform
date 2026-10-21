package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/apierrors"
	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

const (
	recommendationTelemetryMaxBatchEvents     = 50
	recommendationTelemetryMaxBodyBytes       = 64 * 1024
	recommendationClickCooldownPublishTimeout = 30 * time.Second
	recommendationClickCooldownCleanupTimeout = 2 * time.Second
)

type recommendationEventInput struct {
	EventID               string  `json:"event_id"`
	EventType             string  `json:"event_type"`
	TrackingToken         string  `json:"tracking_token"`
	OccurredAt            string  `json:"occurred_at"`
	ForegroundTimeMS      *int64  `json:"foreground_time_ms,omitempty"`
	ScrollProgressPercent *int    `json:"scroll_progress_percent,omitempty"`
	ExitType              *string `json:"exit_type,omitempty"`
	FeedVisibleTimeMS     *int64  `json:"feed_visible_time_ms,omitempty"`
}

type recommendationEventBatchRequest struct {
	Events []recommendationEventInput `json:"events"`
}

type recommendationEventResult struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type recommendationEventBatchResponse struct {
	Accepted   int                         `json:"accepted"`
	Duplicates int                         `json:"duplicates"`
	Rejected   int                         `json:"rejected"`
	Results    []recommendationEventResult `json:"results"`
}

type recommendationEventValidationResponse struct {
	apierrors.ErrorResponse
	recommendationEventBatchResponse
}

type validatedRecommendationEvent struct {
	EventID    string
	EventType  string
	OccurredAt time.Time
	Payload    eventing.RecommendationBehaviorPayload
}

var recommendationTelemetryNow = func() time.Time { return time.Now().UTC() }

type RecommendationTelemetryRateLimiter func(userID uint, eventCount int) (bool, error)

func NewRecommendationTelemetryRedisRateLimiter(client *redis.Client) RecommendationTelemetryRateLimiter {
	script := redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return current
`)
	return func(userID uint, eventCount int) (bool, error) {
		if client == nil {
			return false, errors.New("redis is not initialized")
		}
		minute := time.Now().UTC().Unix() / 60
		key := fmt.Sprintf("recommendation:telemetry:rate:%d:%d", userID, minute)
		count, err := script.Run(client, []string{key}, strconv.Itoa(eventCount), "70").Int()
		if err != nil {
			return false, err
		}
		return count <= config.RecommendationTelemetryEventsPerMinute(), nil
	}
}

func NewRecommendationEventsHandler(publisher eventing.BatchPublisher, rateLimiter RecommendationTelemetryRateLimiter, clickCooldown ...RecommendationClickCooldown) gin.HandlerFunc {
	var cooldown RecommendationClickCooldown
	if len(clickCooldown) > 0 {
		cooldown = clickCooldown[0]
	}
	return func(ctx *gin.Context) {
		recordRecommendationEvents(ctx, publisher, rateLimiter, cooldown)
	}
}

// RecordRecommendationEvents is retained as a direct handler entry point for
// callers that do not use the router factory. Production routing injects the
// process-lifetime publisher through NewRecommendationEventsHandler.
func RecordRecommendationEvents(ctx *gin.Context) {
	recordRecommendationEvents(ctx, nil, nil, nil)
}

func recordRecommendationEvents(ctx *gin.Context, publisher eventing.BatchPublisher, rateLimiter RecommendationTelemetryRateLimiter, clickCooldown RecommendationClickCooldown) {
	started := time.Now()
	defer func() { metrics.ObserveRecommendationTelemetryIngestDuration(time.Since(started)) }()

	if !config.RecommendationTelemetryEnabled() {
		writeAPIError(ctx, "RECOMMENDATION_TELEMETRY_DISABLED")
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeAPIError(ctx, "UNAUTHORIZED")
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, recommendationTelemetryMaxBodyBytes)
	var request recommendationEventBatchRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAPIError(ctx, "PAYLOAD_TOO_LARGE")
			return
		}
		writeAPIError(ctx, "INVALID_RECOMMENDATION_EVENT_BATCH")
		return
	}
	if len(request.Events) == 0 || len(request.Events) > recommendationTelemetryMaxBatchEvents {
		writeAPIError(ctx, "EVENTS_MUST_CONTAIN_BETWEEN_1_AND_50_ITEMS")
		return
	}
	metrics.ObserveRecommendationTelemetryBatchSize(len(request.Events))

	if rateLimiter == nil {
		writeInternalAPIErrorCode(ctx, "SERVICE_UNAVAILABLE")
		return
	}
	allowed, err := rateLimiter(userID, len(request.Events))
	if err != nil {
		writeInternalAPIErrorAs(ctx, err, "SERVICE_UNAVAILABLE")
		return
	}
	if !allowed {
		ctx.Header("Retry-After", "60")
		writeAPIError(ctx, "RECOMMENDATION_RATE_LIMITED")
		return
	}

	now := recommendationTelemetryNow().UTC()
	key := []byte(config.RecommendationTelemetrySigningKey())
	response := recommendationEventBatchResponse{Results: make([]recommendationEventResult, len(request.Events))}
	validEvents := make([]validatedRecommendationEvent, 0, len(request.Events))
	validIndexes := make([]int, 0, len(request.Events))
	for index, input := range request.Events {
		response.Results[index].EventID = strings.TrimSpace(input.EventID)
		event, reason := validateRecommendationTelemetryEvent(userID, input, now, key)
		if reason != "" {
			response.Results[index].Status = "rejected"
			response.Results[index].Reason = reason
			continue
		}
		validEvents = append(validEvents, event)
		validIndexes = append(validIndexes, index)
	}

	if len(validEvents) == 0 {
		completeRecommendationEventResponse(&response, request.Events)
		writeRecommendationValidationResponse(ctx, response)
		return
	}

	envelopes := make([]eventing.Envelope, len(validEvents))
	for eventIndex, event := range validEvents {
		envelope, err := eventing.NewRecommendationBehaviorEnvelope(event.EventID, event.EventType, event.OccurredAt, event.Payload)
		if err != nil {
			writeAPIError(ctx, "VALIDATION_FAILED")
			return
		}
		envelopes[eventIndex] = envelope
	}

	publishIndexes := make([]int, 0, len(validEvents))
	publishEnvelopes := make([]eventing.Envelope, 0, len(validEvents))
	reservations := make([]validatedRecommendationEvent, 0, len(validEvents))
	seenClickOpportunities := make(map[string]struct{})
	for eventIndex, event := range validEvents {
		if event.EventType == eventing.EventTypeRecommendationClick {
			postID := event.Payload.PostID
			clickKey := fmt.Sprintf("%d:%s", postID, event.Payload.RequestID)
			if _, duplicateInBatch := seenClickOpportunities[clickKey]; duplicateInBatch {
				markRecommendationEventDuplicate(&response, validIndexes[eventIndex], "click_cooldown")
				metrics.RecordRecommendationClickCooldown("duplicate")
				continue
			}
			seenClickOpportunities[clickKey] = struct{}{}
			if clickCooldown == nil {
				metrics.RecordRecommendationClickCooldown("redis_error")
				releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
				writeInternalAPIErrorCode(ctx, "SERVICE_UNAVAILABLE")
				return
			}
			result, acquireErr := clickCooldown.Acquire(ctx.Request.Context(), userID, postID, event.Payload.RequestID, event.EventID)
			if acquireErr != nil {
				metrics.RecordRecommendationClickCooldown("redis_error")
				releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
				writeInternalAPIErrorAs(ctx, acquireErr, "SERVICE_UNAVAILABLE")
				return
			}
			switch result {
			case RecommendationClickCooldownAcquired:
				reservations = append(reservations, event)
				metrics.RecordRecommendationClickCooldown("acquired")
			case RecommendationClickCooldownDuplicate:
				metrics.RecordRecommendationClickCooldown("duplicate")
				markRecommendationEventDuplicate(&response, validIndexes[eventIndex], "click_cooldown")
				continue
			case RecommendationClickCooldownPending:
				metrics.RecordRecommendationClickCooldown("pending")
				releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
				ctx.Header("Retry-After", "1")
				writeAPIError(ctx, "RECOMMENDATION_RETRY_BATCH")
				return
			default:
				metrics.RecordRecommendationClickCooldown("redis_error")
				releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
				writeInternalAPIErrorCode(ctx, "SERVICE_UNAVAILABLE")
				return
			}
		}
		publishIndexes = append(publishIndexes, validIndexes[eventIndex])
		publishEnvelopes = append(publishEnvelopes, envelopes[eventIndex])
	}
	if len(publishEnvelopes) == 0 {
		completeRecommendationEventResponse(&response, request.Events)
		if response.Duplicates > 0 {
			ctx.JSON(http.StatusAccepted, response)
		} else {
			writeRecommendationValidationResponse(ctx, response)
		}
		return
	}
	if publisher == nil {
		releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
		writeInternalAPIErrorCode(ctx, "SERVICE_UNAVAILABLE")
		return
	}

	publishCtx := ctx.Request.Context()
	if len(reservations) > 0 {
		var cancel context.CancelFunc
		publishCtx, cancel = context.WithTimeout(publishCtx, recommendationClickCooldownPublishTimeout)
		defer cancel()
	}
	if err := publisher.PublishBatch(publishCtx, publishEnvelopes); err != nil {
		releaseRecommendationClickReservations(ctx, userID, clickCooldown, reservations)
		writeInternalAPIErrorAs(ctx, err, "SERVICE_UNAVAILABLE")
		return
	}

	commitCtx, cancelCommit := context.WithTimeout(context.Background(), recommendationClickCooldownCleanupTimeout)
	for _, event := range reservations {
		if err := clickCooldown.Commit(commitCtx, userID, event.Payload.PostID, event.Payload.RequestID, event.EventID); err != nil {
			metrics.RecordRecommendationClickCooldown("commit_error")
			logAPIInternalError(ctx, http.StatusAccepted, err)
		}
	}
	cancelCommit()

	for _, inputIndex := range publishIndexes {
		response.Results[inputIndex].Status = "accepted"
	}
	completeRecommendationEventResponse(&response, request.Events)
	ctx.JSON(http.StatusAccepted, response)
}

func markRecommendationEventDuplicate(response *recommendationEventBatchResponse, index int, reason string) {
	response.Results[index].Status = "duplicate"
	response.Results[index].Reason = reason
}

func writeRecommendationValidationResponse(ctx *gin.Context, response recommendationEventBatchResponse) {
	status, apiError := apierrors.PublicErrorResponse(ctx, apierrors.CodeValidationFailed)
	ctx.AbortWithStatusJSON(status, recommendationEventValidationResponse{
		ErrorResponse: apiError, recommendationEventBatchResponse: response,
	})
}

func releaseRecommendationClickReservations(ctx *gin.Context, userID uint, cooldown RecommendationClickCooldown, reservations []validatedRecommendationEvent) {
	if cooldown == nil || len(reservations) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), recommendationClickCooldownCleanupTimeout)
	defer cancel()
	for _, event := range reservations {
		if err := cooldown.Release(cleanupCtx, userID, event.Payload.PostID, event.Payload.RequestID, event.EventID); err != nil {
			metrics.RecordRecommendationClickCooldown("redis_error")
			logAPIInternalError(ctx, http.StatusServiceUnavailable, err)
			continue
		}
		metrics.RecordRecommendationClickCooldown("released")
	}
}

func completeRecommendationEventResponse(response *recommendationEventBatchResponse, inputs []recommendationEventInput) {
	for index, result := range response.Results {
		switch result.Status {
		case "accepted":
			response.Accepted++
		case "duplicate":
			response.Duplicates++
		default:
			result.Status = "rejected"
			response.Results[index].Status = "rejected"
			response.Rejected++
		}
		metrics.RecordRecommendationTelemetryEvent(result.Status, recommendationEventTypeMetricLabel(inputs[index].EventType), result.Reason)
	}
}

func validateRecommendationTelemetryEvent(userID uint, input recommendationEventInput, now time.Time, key []byte) (validatedRecommendationEvent, string) {
	eventID := strings.TrimSpace(input.EventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return validatedRecommendationEvent{}, "invalid_event_id"
	}
	httpEventType := strings.TrimSpace(input.EventType)
	kafkaEventType, ok := eventing.RecommendationEventTypeForAction(httpEventType)
	if !ok {
		return validatedRecommendationEvent{}, "unsupported_event_type"
	}

	claims, err := recommendation.VerifyTrackingToken(strings.TrimSpace(input.TrackingToken), key)
	if err != nil {
		return validatedRecommendationEvent{}, "invalid_tracking_token"
	}
	if claims.UserID != userID {
		return validatedRecommendationEvent{}, "user_mismatch"
	}
	if claims.Scene != recommendation.RecommendationScene {
		return validatedRecommendationEvent{}, "unsupported_scene"
	}

	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.OccurredAt))
	if err != nil {
		return validatedRecommendationEvent{}, "invalid_occurred_at"
	}
	occurredAt = occurredAt.UTC()
	feedVisibleTimeMS, feedReason := validateRecommendationFeedPayload(httpEventType, input)
	if feedReason != "" {
		return validatedRecommendationEvent{}, feedReason
	}
	issuedAt := time.Unix(claims.IssuedAtUnix, 0).UTC()
	expiresAt := time.Unix(claims.ExpiresAtUnix, 0).UTC()
	skew := config.RecommendationTelemetryMaxClockSkew()
	if now.After(expiresAt.Add(skew)) {
		return validatedRecommendationEvent{}, "token_expired"
	}
	if occurredAt.Before(issuedAt.Add(-skew)) || occurredAt.After(expiresAt) || occurredAt.After(now.Add(skew)) {
		return validatedRecommendationEvent{}, "occurred_at_out_of_range"
	}

	foregroundTimeMS, scrollProgressPercent, exitType, estimatedReadTimeMS, readPolicyVersion, readOutcome, reason :=
		validateRecommendationReadPayload(httpEventType, input, claims.EstimatedReadTimeMS, claims.ReadPolicyVersion)
	if reason != "" {
		return validatedRecommendationEvent{}, reason
	}
	return validatedRecommendationEvent{
		EventID: eventID, EventType: kafkaEventType, OccurredAt: occurredAt,
		Payload: eventing.RecommendationBehaviorPayload{
			UserID: userID, PostID: claims.PostID, RequestID: claims.RequestID,
			Scene: claims.Scene, Position: claims.Position, RankerVersion: claims.RankerVersion,
			RankerConfigHash: claims.RankerConfigHash, StrategyID: claims.StrategyID,
			ExplorationOpportunity: claims.ExplorationOpportunity, SelectionMode: claims.SelectionMode,
			ExplorationReason: claims.ExplorationReason,
			ReceivedAt:        now, ForegroundTimeMS: foregroundTimeMS,
			ScrollProgressPercent: scrollProgressPercent, ExitType: exitType,
			EstimatedReadTimeMS: estimatedReadTimeMS, ReadPolicyVersion: readPolicyVersion,
			ReadOutcome: readOutcome, FeedVisibleTimeMS: feedVisibleTimeMS,
		},
	}, ""
}

func recommendationEventTypeMetricLabel(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick,
		models.RecommendationEventTypeReadEnd, models.RecommendationEventTypeFeedDwell,
		models.RecommendationEventTypeNotInterested:
		return strings.TrimSpace(eventType)
	default:
		return "unknown"
	}
}
