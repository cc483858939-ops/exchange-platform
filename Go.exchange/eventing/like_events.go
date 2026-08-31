package eventing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const EventTypePostLikeSnapshot = "post.like.snapshot"

type PostLikeSnapshotPayload struct {
	PostID    uint  `json:"post_id"`
	LikeCount int64 `json:"like_count"`
	Version   int64 `json:"version"`
}

func NewLikeSnapshotEnvelope(postID uint, count, version int64) (Envelope, error) {
	if postID == 0 || count < 0 || version < 0 {
		return Envelope{}, fmt.Errorf("invalid post like snapshot")
	}
	payload, err := json.Marshal(PostLikeSnapshotPayload{PostID: postID, LikeCount: count, Version: version})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal like snapshot: %w", err)
	}
	return Envelope{
		ID:   fmt.Sprintf("like-snapshot:%d:%d", postID, version),
		Type: EventTypePostLikeSnapshot, SchemaVersion: 1,
		AggregateType: "post", AggregateID: strconv.FormatUint(uint64(postID), 10),
		OccurredAt: time.Now().UTC(), Payload: payload,
	}, nil
}

func NewLikeBehaviorEnvelope(eventID string, userID, postID uint, action string, version int64, occurredAt time.Time) (Envelope, error) {
	if strings.TrimSpace(eventID) == "" || userID == 0 || postID == 0 ||
		(strings.TrimSpace(action) != "like" && strings.TrimSpace(action) != "unlike") ||
		version <= 0 || occurredAt.IsZero() {
		return Envelope{}, fmt.Errorf("invalid post like behavior")
	}
	payload, err := json.Marshal(UserBehaviorPayload{UserID: userID, PostID: postID, Action: strings.TrimSpace(action), Source: "redis", LikeVersion: version})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal like behavior: %w", err)
	}
	return Envelope{ID: eventID, Type: EventTypeForBehaviorAction(action), SchemaVersion: 1, AggregateType: "post", AggregateID: strconv.FormatUint(uint64(postID), 10), OccurredAt: occurredAt.UTC(), Payload: payload}, nil
}
