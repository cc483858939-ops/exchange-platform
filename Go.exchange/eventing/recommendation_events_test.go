package eventing

import (
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/google/uuid"
)

func TestRecommendationEventsRecordedRoutesAndKeysByUser(t *testing.T) {
	event, err := NewRecommendationEventsRecorded(7, []models.RecommendationEvent{{
		EventID: "550e8400-e29b-41d4-a716-446655440000", UserID: 7,
		RequestID: "550e8400-e29b-41d4-a716-446655440001", ArticleID: 11,
		EventType: models.RecommendationEventTypeImpression, Scene: "recommendation_page",
		Position: 1, RankerVersion: "rules_v1", RankerConfigHash: "0123456789ab",
		StrategyID: "personalized_rules_v1", OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	envelope := EnvelopeFromOutbox(event)
	if key := KeyForEvent(envelope); key != "7" {
		t.Fatalf("key=%q", key)
	}
	topic, err := TopicForEvent(config.KafkaConfig{RecommendationEventsTopic: "goexchange.recommendation.events.v1"}, envelope.Type)
	if err != nil || topic != "goexchange.recommendation.events.v1" {
		t.Fatalf("topic=%q err=%v", topic, err)
	}
	var payload RecommendationEventsRecordedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != 7 || len(payload.Events) != 1 || payload.Events[0].ArticleID != 11 {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestRecommendationEventsRecordedCopiesFeedVisibleTime(t *testing.T) {
	now := time.Now().UTC()
	duration := int64(8234)
	event, err := NewRecommendationEventsRecorded(7, []models.RecommendationEvent{{
		EventID: uuid.NewString(), UserID: 7, RequestID: uuid.NewString(), ArticleID: 11,
		EventType: models.RecommendationEventTypeFeedDwell, Scene: "recommendation_page",
		Position: 1, RankerVersion: "rules_v3", RankerConfigHash: "0123456789ab",
		StrategyID: "personalized_rules_v3", OccurredAt: now, ReceivedAt: now,
		FeedVisibleTimeMS: &duration,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var payload RecommendationEventsRecordedPayload
	if err := json.Unmarshal(EnvelopeFromOutbox(event).Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 || payload.Events[0].EventType != models.RecommendationEventTypeFeedDwell ||
		payload.Events[0].FeedVisibleTimeMS == nil || *payload.Events[0].FeedVisibleTimeMS != duration {
		t.Fatalf("payload=%#v", payload)
	}
}
