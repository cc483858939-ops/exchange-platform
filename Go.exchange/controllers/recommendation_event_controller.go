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
	"Go.exchange/metrics"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	recommendationTelemetryMaxBatchEvents = 50
	recommendationTelemetryMaxBodyBytes   = 64 * 1024
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

type recommendationPersistenceResult struct {
	Status string
	Reason string
}

var (
	recommendationTelemetryNow             = func() time.Time { return time.Now().UTC() }
	allowRecommendationTelemetryEvents     = enforceRecommendationTelemetryRateLimit
	persistRecommendationEventBatch        = persistRecommendationEvents
	recommendationTelemetryRateLimitScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return current
`)
)

func RecordRecommendationEvents(ctx *gin.Context) {
	started := time.Now()
	defer func() { metrics.ObserveRecommendationTelemetryIngestDuration(time.Since(started)) }()

	if !config.RecommendationTelemetryEnabled() {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "recommendation telemetry is disabled"})
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, recommendationTelemetryMaxBodyBytes)
	var request recommendationEventBatchRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 64 KiB"})
			return
		}
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid recommendation event batch"})
		return
	}
	if len(request.Events) == 0 || len(request.Events) > recommendationTelemetryMaxBatchEvents {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "events must contain between 1 and 50 items"})
		return
	}
	metrics.ObserveRecommendationTelemetryBatchSize(len(request.Events))

	allowed, err := allowRecommendationTelemetryEvents(userID, len(request.Events))
	if err != nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation telemetry rate limiter unavailable"})
		return
	}
	if !allowed {
		ctx.Header("Retry-After", "60")
		ctx.JSON(http.StatusTooManyRequests, gin.H{"error": "recommendation telemetry rate limit exceeded"})
		return
	}

	now := recommendationTelemetryNow().UTC()
	key := []byte(config.RecommendationTelemetrySigningKey())
	response := recommendationEventBatchResponse{Results: make([]recommendationEventResult, len(request.Events))}
	validEvents := make([]models.RecommendationEvent, 0, len(request.Events))
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

	if len(validEvents) > 0 {
		persistenceResults, err := persistRecommendationEventBatch(userID, validEvents)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist recommendation events"})
			return
		}
		for index, result := range persistenceResults {
			responseIndex := validIndexes[index]
			response.Results[responseIndex].Status = result.Status
			response.Results[responseIndex].Reason = result.Reason
		}
	}

	for index, result := range response.Results {
		switch result.Status {
		case "accepted":
			response.Accepted++
		case "duplicate":
			response.Duplicates++
		default:
			response.Rejected++
		}
		metrics.RecordRecommendationTelemetryEvent(result.Status, recommendationEventTypeMetricLabel(request.Events[index].EventType), result.Reason)
	}
	if response.Accepted+response.Duplicates == 0 {
		ctx.JSON(http.StatusUnprocessableEntity, response)
		return
	}
	ctx.JSON(http.StatusAccepted, response)
}

func validateRecommendationTelemetryEvent(userID uint, input recommendationEventInput, now time.Time, key []byte) (models.RecommendationEvent, string) {
	eventID := strings.TrimSpace(input.EventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return models.RecommendationEvent{}, "invalid_event_id"
	}
	eventType := strings.TrimSpace(input.EventType)
	if !isRecommendationEventType(eventType) {
		return models.RecommendationEvent{}, "unsupported_event_type"
	}

	claims, err := verifyRecommendationTrackingToken(strings.TrimSpace(input.TrackingToken), key)
	if err != nil {
		return models.RecommendationEvent{}, "invalid_tracking_token"
	}
	if claims.UserID != userID {
		return models.RecommendationEvent{}, "user_mismatch"
	}
	if claims.Scene != recommendationScene {
		return models.RecommendationEvent{}, "unsupported_scene"
	}

	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.OccurredAt))
	if err != nil {
		return models.RecommendationEvent{}, "invalid_occurred_at"
	}
	occurredAt = occurredAt.UTC()
	feedVisibleTimeMS, feedReason := validateRecommendationFeedPayload(eventType, input)
	if feedReason != "" {
		return models.RecommendationEvent{}, feedReason
	}
	issuedAt := time.Unix(claims.IssuedAtUnix, 0).UTC()
	expiresAt := time.Unix(claims.ExpiresAtUnix, 0).UTC()
	skew := config.RecommendationTelemetryMaxClockSkew()
	if now.After(expiresAt.Add(skew)) {
		return models.RecommendationEvent{}, "token_expired"
	}
	if occurredAt.Before(issuedAt.Add(-skew)) || occurredAt.After(expiresAt) || occurredAt.After(now.Add(skew)) {
		return models.RecommendationEvent{}, "occurred_at_out_of_range"
	}

	foregroundTimeMS, scrollProgressPercent, exitType, estimatedReadTimeMS, readPolicyVersion, readOutcome, reason :=
		validateRecommendationReadPayload(eventType, input, claims.EstimatedReadTimeMS, claims.ReadPolicyVersion)
	if reason != "" {
		return models.RecommendationEvent{}, reason
	}
	return models.RecommendationEvent{
		EventID: eventID, UserID: userID, RequestID: claims.RequestID,
		ArticleID: claims.ArticleID, EventType: eventType, Scene: claims.Scene,
		Position: claims.Position, RankerVersion: claims.RankerVersion,
		RankerConfigHash: claims.RankerConfigHash, StrategyID: claims.StrategyID,
		OccurredAt: occurredAt, ReceivedAt: now, ForegroundTimeMS: foregroundTimeMS,
		ScrollProgressPercent: scrollProgressPercent, ExitType: exitType,
		EstimatedReadTimeMS: estimatedReadTimeMS, ReadPolicyVersion: readPolicyVersion,
		ReadOutcome: readOutcome, FeedVisibleTimeMS: feedVisibleTimeMS, CreatedAt: now,
	}, ""
}
func persistRecommendationEvents(userID uint, events []models.RecommendationEvent) ([]recommendationPersistenceResult, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	results := make([]recommendationPersistenceResult, len(events))
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		inserted := make([]models.RecommendationEvent, 0, len(events))
		for index := range events {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&events[index])
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				results[index] = recommendationPersistenceResult{Status: "accepted"}
				inserted = append(inserted, events[index])
				continue
			}

			var existing models.RecommendationEvent
			err := tx.Where("event_id = ?", events[index].EventID).First(&existing).Error
			switch {
			case err == nil && recommendationEventMatches(existing, events[index]):
				results[index] = recommendationPersistenceResult{Status: "duplicate"}
			case err == nil:
				results[index] = recommendationPersistenceResult{Status: "rejected", Reason: "event_id_conflict"}
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return err
			default:
				if err := tx.Where(
					"request_id = ? AND article_id = ? AND event_type = ?",
					events[index].RequestID, events[index].ArticleID, events[index].EventType,
				).First(&existing).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				results[index] = recommendationPersistenceResult{Status: "duplicate"}
			}
		}
		if len(inserted) == 0 {
			return nil
		}
		outboxEvent, err := eventing.NewRecommendationEventsRecorded(userID, inserted)
		if err != nil {
			return err
		}
		return eventing.AddOutboxEvent(tx, outboxEvent)
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func recommendationEventMatches(existing, incoming models.RecommendationEvent) bool {
	return existing.EventID == incoming.EventID && existing.UserID == incoming.UserID &&
		existing.RequestID == incoming.RequestID && existing.ArticleID == incoming.ArticleID &&
		existing.EventType == incoming.EventType && existing.Scene == incoming.Scene &&
		existing.Position == incoming.Position && existing.RankerVersion == incoming.RankerVersion &&
		existing.RankerConfigHash == incoming.RankerConfigHash && existing.StrategyID == incoming.StrategyID &&
		existing.OccurredAt.Equal(incoming.OccurredAt) &&
		recommendationReadPayloadMatches(existing, incoming)
}

func enforceRecommendationTelemetryRateLimit(userID uint, eventCount int) (bool, error) {
	if global.RedisDB == nil {
		return false, errors.New("redis is not initialized")
	}
	minute := time.Now().UTC().Unix() / 60
	key := fmt.Sprintf("recommendation:telemetry:rate:%d:%d", userID, minute)
	count, err := recommendationTelemetryRateLimitScript.Run(
		global.RedisDB,
		[]string{key},
		strconv.Itoa(eventCount),
		"70",
	).Int()
	if err != nil {
		return false, err
	}
	return count <= config.RecommendationTelemetryEventsPerMinute(), nil
}

func recommendationEventTypeMetricLabel(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick, models.RecommendationEventTypeReadEnd, models.RecommendationEventTypeFeedDwell, models.RecommendationEventTypeNotInterested:
		return eventType
	default:
		return "unknown"
	}
}
