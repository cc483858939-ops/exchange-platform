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
	EventTypePostViewed             = "post.viewed"
	EventTypePostLiked              = "post.liked"
	EventTypePostUnliked            = "post.unliked"
	EventTypePostEmbeddingRequested = "post.embedding.requested"
	EventTypePostReactionApplied    = "post.reaction.applied"
	EventTypeReplyCreated           = "post.reply.created"
	EventTypeUserFollowCreated      = "user_follow.created"

	EventTypeRecommendationImpression    = "recommendation.impression"
	EventTypeRecommendationClick         = "recommendation.click"
	EventTypeRecommendationReadEnd       = "recommendation.read_end"
	EventTypeRecommendationFeedDwell     = "recommendation.feed_dwell"
	EventTypeRecommendationNotInterested = "recommendation.not_interested"

	RecommendationBehaviorSchemaVersion              = 3
	RecommendationSelectionModeRanked                = "ranked"
	RecommendationSelectionModeExploration           = "exploration"
	RecommendationExplorationReasonRecent            = "recent"
	RecommendationExplorationReasonNovelAuthor       = "novel_author"
	RecommendationExplorationReasonRecentNovelAuthor = "recent_novel_author"
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

type UserBehaviorPayload struct {
	UserID      uint   `json:"user_id"`
	PostID      uint   `json:"post_id"`
	Action      string `json:"action"`
	Source      string `json:"source"`
	LikeVersion int64  `json:"like_version,omitempty"`
}

type PostEmbeddingRequestedPayload struct {
	PostID uint `json:"post_id"`
}

type PostReactionAppliedPayload struct {
	ActorID         uint      `json:"actor_id"`
	PostID          uint      `json:"post_id"`
	PostAuthorID    uint      `json:"post_author_id"`
	Liked           bool      `json:"liked"`
	ReactionVersion int64     `json:"reaction_version"`
	StateChangedAt  time.Time `json:"state_changed_at"`
}

type ReplyCreatedPayload struct {
	ReplyPostID    uint      `json:"reply_post_id"`
	ParentPostID   uint      `json:"parent_post_id"`
	ConversationID uint      `json:"conversation_id"`
	ActorID        uint      `json:"actor_id"`
	ParentAuthorID uint      `json:"parent_author_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type UserFollowCreatedPayload struct {
	FollowID    uint      `json:"follow_id"`
	FollowerID  uint      `json:"follower_id"`
	FollowingID uint      `json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type RecommendationBehaviorPayload struct {
	UserID    uint   `json:"user_id"`
	PostID    uint   `json:"post_id"`
	RequestID string `json:"request_id"`

	Scene    string `json:"scene"`
	Position int    `json:"position"`

	RankerVersion    string `json:"ranker_version"`
	RankerConfigHash string `json:"ranker_config_hash"`
	StrategyID       string `json:"strategy_id"`

	ExplorationOpportunity bool   `json:"exploration_opportunity"`
	SelectionMode          string `json:"selection_mode"`
	ExplorationReason      string `json:"exploration_reason"`

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

func ValidateRecommendationProvenance(opportunity bool, selectionMode, explorationReason string) error {
	switch {
	case selectionMode == RecommendationSelectionModeRanked && explorationReason == "":
		return nil
	case opportunity && selectionMode == RecommendationSelectionModeExploration &&
		(explorationReason == RecommendationExplorationReasonRecent ||
			explorationReason == RecommendationExplorationReasonNovelAuthor ||
			explorationReason == RecommendationExplorationReasonRecentNovelAuthor):
		return nil
	default:
		return errors.New("invalid recommendation provenance")
	}
}

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
	if payload.UserID == 0 || payload.PostID == 0 || strings.TrimSpace(payload.RequestID) == "" ||
		strings.TrimSpace(payload.Scene) == "" || payload.Position <= 0 ||
		strings.TrimSpace(payload.RankerVersion) == "" || strings.TrimSpace(payload.RankerConfigHash) == "" ||
		strings.TrimSpace(payload.StrategyID) == "" || payload.ReceivedAt.IsZero() {
		return Envelope{}, errors.New("recommendation payload is missing required fields")
	}
	if err := ValidateRecommendationProvenance(payload.ExplorationOpportunity, payload.SelectionMode, payload.ExplorationReason); err != nil {
		return Envelope{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal recommendation behavior payload: %w", err)
	}
	return Envelope{
		ID:            eventID,
		Type:          eventType,
		SchemaVersion: RecommendationBehaviorSchemaVersion,
		AggregateType: "user",
		AggregateID:   strconv.FormatUint(uint64(payload.UserID), 10),
		OccurredAt:    occurredAt.UTC(),
		Payload:       body,
	}, nil
}

func NewPostViewedEnvelope(eventID string, userID, postID uint, occurredAt time.Time, source string) (Envelope, error) {
	eventID = strings.TrimSpace(eventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return Envelope{}, errors.New("post view event id must be a UUID")
	}
	if userID == 0 || postID == 0 {
		return Envelope{}, errors.New("post view requires user and post")
	}
	if occurredAt.IsZero() {
		return Envelope{}, errors.New("post view occurred_at is required")
	}
	source = strings.TrimSpace(source)
	if source != "feed" && source != "post_detail" {
		return Envelope{}, errors.New("post view source is invalid")
	}
	body, err := json.Marshal(UserBehaviorPayload{
		UserID: userID, PostID: postID, Action: "view", Source: strings.TrimSpace(source),
	})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal post view payload: %w", err)
	}
	return Envelope{
		ID:            eventID,
		Type:          EventTypePostViewed,
		SchemaVersion: 1,
		AggregateType: "user",
		AggregateID:   strconv.FormatUint(uint64(userID), 10),
		OccurredAt:    occurredAt.UTC(),
		Payload:       body,
	}, nil
}

func NewPostEmbeddingRequestedEnvelope(eventID string, postID uint, occurredAt time.Time) (Envelope, error) {
	eventID = strings.TrimSpace(eventID)
	if _, err := uuid.Parse(eventID); err != nil {
		return Envelope{}, errors.New("post embedding event id must be a UUID")
	}
	if postID == 0 {
		return Envelope{}, errors.New("post embedding requires a post")
	}
	if occurredAt.IsZero() {
		return Envelope{}, errors.New("post embedding occurred_at is required")
	}
	body, err := json.Marshal(PostEmbeddingRequestedPayload{PostID: postID})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal post embedding payload: %w", err)
	}
	return Envelope{
		ID:            eventID,
		Type:          EventTypePostEmbeddingRequested,
		SchemaVersion: 1,
		AggregateType: "post",
		AggregateID:   strconv.FormatUint(uint64(postID), 10),
		OccurredAt:    occurredAt.UTC(),
		Payload:       body,
	}, nil
}

// NewOutboxEvent materializes one immutable row from the complete canonical
// envelope. It intentionally does not publish or mutate delivery metadata.
func NewOutboxEvent(kafkaConfig config.KafkaConfig, envelope Envelope) (models.OutboxEvent, error) {
	if err := validateOutboxEnvelope(envelope); err != nil {
		return models.OutboxEvent{}, err
	}
	topic, err := TopicForEvent(kafkaConfig, envelope.Type)
	if err != nil {
		return models.OutboxEvent{}, err
	}
	if topic == "" {
		return models.OutboxEvent{}, fmt.Errorf("Kafka topic is empty for event type %q", envelope.Type)
	}
	key := strings.TrimSpace(KeyForEvent(envelope))
	if key == "" {
		return models.OutboxEvent{}, errors.New("outbox partition key is required")
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return models.OutboxEvent{}, fmt.Errorf("marshal canonical outbox envelope: %w", err)
	}
	return models.OutboxEvent{
		ID:            envelope.ID,
		Topic:         topic,
		PartitionKey:  key,
		EventType:     envelope.Type,
		SchemaVersion: envelope.SchemaVersion,
		AggregateType: envelope.AggregateType,
		AggregateID:   envelope.AggregateID,
		Message:       string(body),
		OccurredAt:    envelope.OccurredAt.UTC(),
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func validateOutboxEnvelope(event Envelope) error {
	if _, err := uuid.Parse(strings.TrimSpace(event.ID)); err != nil {
		return errors.New("outbox event id must be a UUID")
	}
	if strings.TrimSpace(event.Type) == "" || event.SchemaVersion < 1 ||
		strings.TrimSpace(event.AggregateType) == "" || strings.TrimSpace(event.AggregateID) == "" ||
		event.OccurredAt.IsZero() {
		return errors.New("outbox envelope has missing required fields")
	}
	if len(event.Payload) == 0 || string(event.Payload) == "null" {
		return errors.New("outbox envelope payload is required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &object); err != nil || object == nil {
		return errors.New("outbox envelope payload must be a JSON object")
	}
	return nil
}

func NewPostReactionAppliedEnvelope(eventID string, payload PostReactionAppliedPayload) (Envelope, error) {
	if payload.ActorID == 0 || payload.PostID == 0 || payload.PostAuthorID == 0 || payload.ReactionVersion <= 0 || payload.StateChangedAt.IsZero() {
		return Envelope{}, errors.New("post reaction activity payload is missing required fields")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal post reaction activity payload: %w", err)
	}
	return newActivityEnvelope(eventID, EventTypePostReactionApplied, "post_reaction",
		fmt.Sprintf("%d:%d", payload.ActorID, payload.PostID), payload.StateChangedAt, body)
}

func NewReplyCreatedEnvelope(eventID string, payload ReplyCreatedPayload) (Envelope, error) {
	if payload.ReplyPostID == 0 || payload.ParentPostID == 0 || payload.ConversationID == 0 || payload.ActorID == 0 || payload.ParentAuthorID == 0 || payload.CreatedAt.IsZero() {
		return Envelope{}, errors.New("reply activity payload is missing required fields")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal reply activity payload: %w", err)
	}
	return newActivityEnvelope(eventID, EventTypeReplyCreated, "post", strconv.FormatUint(uint64(payload.ReplyPostID), 10), payload.CreatedAt, body)
}

func NewUserFollowCreatedEnvelope(eventID string, payload UserFollowCreatedPayload) (Envelope, error) {
	if payload.FollowID == 0 || payload.FollowerID == 0 || payload.FollowingID == 0 || payload.FollowerID == payload.FollowingID || payload.CreatedAt.IsZero() {
		return Envelope{}, errors.New("follow activity payload is missing required fields")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal follow activity payload: %w", err)
	}
	return newActivityEnvelope(eventID, EventTypeUserFollowCreated, "user_follow", strconv.FormatUint(uint64(payload.FollowID), 10), payload.CreatedAt, body)
}

func newActivityEnvelope(eventID, eventType, aggregateType, aggregateID string, occurredAt time.Time, payload []byte) (Envelope, error) {
	if _, err := uuid.Parse(strings.TrimSpace(eventID)); err != nil {
		return Envelope{}, errors.New("activity event id must be a UUID")
	}
	if occurredAt.IsZero() {
		return Envelope{}, errors.New("activity occurred_at is required")
	}
	return Envelope{ID: strings.TrimSpace(eventID), Type: eventType, SchemaVersion: 1, AggregateType: aggregateType, AggregateID: aggregateID, OccurredAt: occurredAt.UTC(), Payload: payload}, nil
}

func EventTypeForBehaviorAction(action string) string {
	switch strings.TrimSpace(action) {
	case "like":
		return EventTypePostLiked
	case "unlike":
		return EventTypePostUnliked
	default:
		return EventTypePostViewed
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
	case EventTypePostViewed, EventTypePostLiked, EventTypePostUnliked:
		return strings.TrimSpace(kafkaConfig.UserBehaviorTopic), nil
	case EventTypePostLikeSnapshot:
		return strings.TrimSpace(kafkaConfig.LikeSnapshotTopic), nil
	case EventTypeRecommendationImpression,
		EventTypeRecommendationClick,
		EventTypeRecommendationReadEnd,
		EventTypeRecommendationFeedDwell,
		EventTypeRecommendationNotInterested:
		return strings.TrimSpace(kafkaConfig.RecommendationEventsTopic), nil
	case EventTypePostEmbeddingRequested:
		return strings.TrimSpace(kafkaConfig.PostEmbeddingTopic), nil
	case EventTypePostReactionApplied, EventTypeReplyCreated, EventTypeUserFollowCreated:
		return strings.TrimSpace(kafkaConfig.ActivityEventsTopic), nil
	default:
		return "", fmt.Errorf("unsupported event type %q", eventType)
	}
}

func KeyForEvent(event Envelope) string {
	switch event.Type {
	case EventTypePostLiked, EventTypePostUnliked:
		var payload UserBehaviorPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.UserID > 0 && payload.PostID > 0 {
			return fmt.Sprintf("%d:%d", payload.UserID, payload.PostID)
		}
	case EventTypePostViewed:
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
	case EventTypePostEmbeddingRequested:
		var payload PostEmbeddingRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.PostID > 0 {
			return strconv.FormatUint(uint64(payload.PostID), 10)
		}
	case EventTypePostReactionApplied:
		var payload PostReactionAppliedPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.ActorID > 0 && payload.PostID > 0 {
			return fmt.Sprintf("%d:%d", payload.ActorID, payload.PostID)
		}
	case EventTypeReplyCreated:
		var payload ReplyCreatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.ConversationID > 0 {
			return strconv.FormatUint(uint64(payload.ConversationID), 10)
		}
	case EventTypeUserFollowCreated:
		var payload UserFollowCreatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.FollowerID > 0 && payload.FollowingID > 0 {
			return fmt.Sprintf("%d:%d", payload.FollowerID, payload.FollowingID)
		}
	}
	return event.AggregateID
}
