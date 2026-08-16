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
	EventTypeArticleAnalysisRequested = "article.analysis.requested"
	EventTypeArticleAnalysisDead      = "article.analysis.dead"
	EventTypeArticleViewed            = "article.viewed"
	EventTypeArticleLiked             = "article.liked"
	EventTypeArticleUnliked           = "article.unliked"

	EventTypeRecommendationImpression    = "recommendation.impression"
	EventTypeRecommendationClick         = "recommendation.click"
	EventTypeRecommendationReadEnd       = "recommendation.read_end"
	EventTypeRecommendationFeedDwell     = "recommendation.feed_dwell"
	EventTypeRecommendationNotInterested = "recommendation.not_interested"
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

type RecommendationBehaviorPayload struct {
	UserID    uint   `json:"user_id"`
	ArticleID uint   `json:"article_id"`
	RequestID string `json:"request_id"`

	Scene    string `json:"scene"`
	Position int    `json:"position"`

	RankerVersion    string `json:"ranker_version"`
	RankerConfigHash string `json:"ranker_config_hash"`
	StrategyID       string `json:"strategy_id"`

	ReceivedAt time.Time `json:"received_at"`

	ForegroundTimeMS      *int64  `json:"foreground_time_ms,omitempty"`
	ScrollProgressPercent *int    `json:"scroll_progress_percent,omitempty"`
	ExitType              *string `json:"exit_type,omitempty"`

	EstimatedReadTimeMS *int64  `json:"estimated_read_time_ms,omitempty"`
	ReadPolicyVersion   *string `json:"read_policy_version,omitempty"`
	ReadOutcome         *string `json:"read_outcome,omitempty"`

	FeedVisibleTimeMS *int64 `json:"feed_visible_time_ms,omitempty"`
}

const (
	RecommendationBehaviorActionClick           = "recommendation_click"
	RecommendationBehaviorActionReadQualified   = "recommendation_read_qualified"
	RecommendationBehaviorActionReadQuickBounce = "recommendation_read_quick_bounce"
	RecommendationBehaviorActionReadNeutral     = "recommendation_read_neutral"
	RecommendationBehaviorActionNotInterested   = "recommendation_not_interested"
)

func RecommendationEventTypeForAction(action string) (string, bool) {
	switch strings.TrimSpace(action) {
	case "impression":
		return EventTypeRecommendationImpression, true
	case "click":
		return EventTypeRecommendationClick, true
	case "read_end":
		return EventTypeRecommendationReadEnd, true
	case "feed_dwell":
		return EventTypeRecommendationFeedDwell, true
	case "not_interested":
		return EventTypeRecommendationNotInterested, true
	default:
		return "", false
	}
}

func IsRecommendationEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case EventTypeRecommendationImpression,
		EventTypeRecommendationClick,
		EventTypeRecommendationReadEnd,
		EventTypeRecommendationFeedDwell,
		EventTypeRecommendationNotInterested:
		return true
	default:
		return false
	}
}

func NewRecommendationBehaviorEnvelope(eventID, eventType string, occurredAt time.Time, payload RecommendationBehaviorPayload) (Envelope, error) {
	eventID = strings.TrimSpace(eventID)
	eventType = strings.TrimSpace(eventType)
	if _, err := uuid.Parse(eventID); err != nil {
		return Envelope{}, errors.New("recommendation event id must be a UUID")
	}
	if !IsRecommendationEventType(eventType) {
		return Envelope{}, fmt.Errorf("unsupported recommendation event type %q", eventType)
	}
	if occurredAt.IsZero() {
		return Envelope{}, errors.New("recommendation occurred_at is required")
	}
	if payload.UserID == 0 || payload.ArticleID == 0 || strings.TrimSpace(payload.RequestID) == "" ||
		strings.TrimSpace(payload.Scene) == "" || payload.Position <= 0 ||
		strings.TrimSpace(payload.RankerVersion) == "" || strings.TrimSpace(payload.RankerConfigHash) == "" ||
		strings.TrimSpace(payload.StrategyID) == "" || payload.ReceivedAt.IsZero() {
		return Envelope{}, errors.New("recommendation payload is missing required fields")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal recommendation behavior payload: %w", err)
	}
	return Envelope{
		ID:            eventID,
		Type:          eventType,
		SchemaVersion: 1,
		AggregateType: "user",
		AggregateID:   strconv.FormatUint(uint64(payload.UserID), 10),
		OccurredAt:    occurredAt.UTC(),
		Payload:       body,
	}, nil
}

func NewArticleViewedEnvelope(eventID string, userID, articleID uint, occurredAt time.Time, source string) (Envelope, error) {
	eventID = strings.TrimSpace(eventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return Envelope{}, errors.New("article view event id must be a UUID")
	}
	if userID == 0 || articleID == 0 {
		return Envelope{}, errors.New("article view requires user and article")
	}
	if occurredAt.IsZero() {
		return Envelope{}, errors.New("article view occurred_at is required")
	}
	body, err := json.Marshal(UserBehaviorPayload{
		UserID: userID, ArticleID: articleID, Action: "view", Source: strings.TrimSpace(source),
	})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal article view payload: %w", err)
	}
	return Envelope{
		ID:            eventID,
		Type:          EventTypeArticleViewed,
		SchemaVersion: 1,
		AggregateType: "user",
		AggregateID:   strconv.FormatUint(uint64(userID), 10),
		OccurredAt:    occurredAt.UTC(),
		Payload:       body,
	}, nil
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
	case EventTypeRecommendationImpression,
		EventTypeRecommendationClick,
		EventTypeRecommendationReadEnd,
		EventTypeRecommendationFeedDwell,
		EventTypeRecommendationNotInterested:
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
	case EventTypeRecommendationImpression,
		EventTypeRecommendationClick,
		EventTypeRecommendationReadEnd,
		EventTypeRecommendationFeedDwell,
		EventTypeRecommendationNotInterested:
		var payload RecommendationBehaviorPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.UserID > 0 {
			return strconv.FormatUint(uint64(payload.UserID), 10)
		}
	}
	return event.AggregateID
}
