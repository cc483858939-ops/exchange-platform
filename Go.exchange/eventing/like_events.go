package eventing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const EventTypeArticleLikeSnapshot = "article.like.snapshot"

type ArticleLikeSnapshotPayload struct {
	ArticleID uint  `json:"article_id"`
	LikeCount int64 `json:"like_count"`
	Version   int64 `json:"version"`
}

func NewLikeSnapshotEnvelope(articleID uint, count, version int64) (Envelope, error) {
	payload, err := json.Marshal(ArticleLikeSnapshotPayload{ArticleID: articleID, LikeCount: count, Version: version})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal like snapshot: %w", err)
	}
	return Envelope{
		ID:   fmt.Sprintf("like-snapshot:%d:%d", articleID, version),
		Type: EventTypeArticleLikeSnapshot, SchemaVersion: 1,
		AggregateType: "article", AggregateID: strconv.FormatUint(uint64(articleID), 10),
		OccurredAt: time.Now().UTC(), Payload: payload,
	}, nil
}

func NewLikeBehaviorEnvelope(eventID string, userID, articleID uint, action string, version int64, occurredAt time.Time) (Envelope, error) {
	payload, err := json.Marshal(UserBehaviorPayload{UserID: userID, ArticleID: articleID, Action: strings.TrimSpace(action), Source: "redis", LikeVersion: version})
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal like behavior: %w", err)
	}
	return Envelope{ID: eventID, Type: EventTypeForBehaviorAction(action), SchemaVersion: 1, AggregateType: "article", AggregateID: strconv.FormatUint(uint64(articleID), 10), OccurredAt: occurredAt.UTC(), Payload: payload}, nil
}
