package eventing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/google/uuid"
)

const (
	EventTypeArticleAnalysisRequested     = "article.analysis.requested"
	EventTypeArticleAnalysisDead          = "article.analysis.dead"
	EventTypeArticleViewed                = "article.viewed"
	EventTypeArticleLiked                 = "article.liked"
	EventTypeArticleUnliked               = "article.unliked"
	EventTypeRecommendationEventsRecorded = "recommendation.events.recorded"
)

type Envelope struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

type ArticleAnalysisRequestedPayload struct {
	JobID           uint   `json:"job_id"`
	ArticleID       uint   `json:"article_id"`
	AnalysisVersion string `json:"analysis_version"`
}

type UserBehaviorPayload struct {
	UserID      uint   `json:"user_id"`
	ArticleID   uint   `json:"article_id"`
	Action      string `json:"action"`
	Source      string `json:"source"`
	LikeVersion int64  `json:"like_version,omitempty"`
}

func NewOutboxEvent(eventType, aggregateType, aggregateID string, payload interface{}) (models.OutboxEvent, error) {
	eventType = strings.TrimSpace(eventType)
	aggregateType = strings.TrimSpace(aggregateType)
	aggregateID = strings.TrimSpace(aggregateID)
	if eventType == "" || aggregateType == "" || aggregateID == "" {
		return models.OutboxEvent{}, errors.New("event type, aggregate type, and aggregate id are required")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return models.OutboxEvent{}, fmt.Errorf("marshal outbox payload: %w", err)
	}

	return models.OutboxEvent{
		ID:            uuid.NewString(),
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		SchemaVersion: 1,
		Payload:       string(body),
		OccurredAt:    time.Now().UTC(),
	}, nil
}

func NewArticleAnalysisRequested(job models.ArticleAnalysisJob, analysisVersion string) (models.OutboxEvent, error) {
	return NewOutboxEvent(EventTypeArticleAnalysisRequested, "article", strconv.FormatUint(uint64(job.ArticleID), 10), ArticleAnalysisRequestedPayload{
		JobID:           job.ID,
		ArticleID:       job.ArticleID,
		AnalysisVersion: analysisVersion,
	})
}

func NewUserBehavior(userID, articleID uint, action, source string) (models.OutboxEvent, error) {
	return NewVersionedUserBehavior(userID, articleID, action, source, 0)
}

func NewVersionedUserBehavior(userID, articleID uint, action, source string, likeVersion int64) (models.OutboxEvent, error) {
	return NewOutboxEvent(EventTypeForBehaviorAction(action), "article", strconv.FormatUint(uint64(articleID), 10), UserBehaviorPayload{
		UserID:      userID,
		ArticleID:   articleID,
		Action:      strings.TrimSpace(action),
		Source:      strings.TrimSpace(source),
		LikeVersion: likeVersion,
	})
}

func EventTypeForBehaviorAction(action string) string {
	switch strings.TrimSpace(action) {
	case "like":
		return EventTypeArticleLiked
	case "unlike":
		return EventTypeArticleUnliked
	default:
		return EventTypeArticleViewed
	}
}

func EnvelopeFromOutbox(event models.OutboxEvent) Envelope {
	return Envelope{
		ID:            event.ID,
		Type:          event.EventType,
		SchemaVersion: event.SchemaVersion,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		OccurredAt:    event.OccurredAt,
		Payload:       json.RawMessage(event.Payload),
	}
}

func DecodeEnvelope(raw []byte) (Envelope, error) {
	var event Envelope
	if err := json.Unmarshal(raw, &event); err != nil {
		return Envelope{}, fmt.Errorf("decode event envelope: %w", err)
	}
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Type) == "" {
		return Envelope{}, errors.New("event id and type are required")
	}
	return event, nil
}

func TopicForEvent(kafkaConfig config.KafkaConfig, eventType string) (string, error) {
	switch eventType {
	case EventTypeArticleAnalysisRequested:
		return strings.TrimSpace(kafkaConfig.ArticleAnalysisTopic), nil
	case EventTypeArticleAnalysisDead:
		return strings.TrimSpace(kafkaConfig.ArticleAnalysisDLQTopic), nil
	case EventTypeArticleViewed, EventTypeArticleLiked, EventTypeArticleUnliked:
		return strings.TrimSpace(kafkaConfig.UserBehaviorTopic), nil
	case EventTypeArticleLikeSnapshot:
		return strings.TrimSpace(kafkaConfig.LikeSnapshotTopic), nil
	case EventTypeRecommendationEventsRecorded:
		return strings.TrimSpace(kafkaConfig.RecommendationEventsTopic), nil
	default:
		return "", fmt.Errorf("unsupported event type %q", eventType)
	}
}

func KeyForEvent(event Envelope) string {
	switch event.Type {
	case EventTypeArticleLiked, EventTypeArticleUnliked:
		var payload UserBehaviorPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.UserID > 0 && payload.ArticleID > 0 {
			return fmt.Sprintf("%d:%d", payload.UserID, payload.ArticleID)
		}
	case EventTypeArticleViewed:
		var payload UserBehaviorPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.UserID > 0 {
			return strconv.FormatUint(uint64(payload.UserID), 10)
		}
	case EventTypeRecommendationEventsRecorded:
		var payload RecommendationEventsRecordedPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.UserID > 0 {
			return strconv.FormatUint(uint64(payload.UserID), 10)
		}
	}
	return event.AggregateID
}
