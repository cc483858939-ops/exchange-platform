package controllers

import (
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
)

func TestRecommendationEventMatchesFeedVisibleTime(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	duration := int64(3000)
	changedDuration := int64(5000)
	base := models.RecommendationEvent{
		EventID: uuid.NewString(), UserID: 7, RequestID: uuid.NewString(), ArticleID: 11,
		EventType: models.RecommendationEventTypeFeedDwell, Scene: recommendationScene,
		Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab",
		StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now, ReceivedAt: now,
		FeedVisibleTimeMS: &duration,
	}
	same := base
	changed := base
	changed.FeedVisibleTimeMS = &changedDuration

	if !recommendationEventMatches(base, same) {
		t.Fatal("same feed dwell payload should match")
	}
	if recommendationEventMatches(base, changed) {
		t.Fatal("changed feed dwell duration must conflict")
	}
}
