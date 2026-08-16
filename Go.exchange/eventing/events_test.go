package eventing

import (
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

func testRecommendationPayload() RecommendationBehaviorPayload {
	now := time.Now().UTC()
	return RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 11, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v3", RankerConfigHash: "0123456789ab", StrategyID: "personalized_rules_v3", ReceivedAt: now,
	}
}

func TestRecommendationBehaviorEnvelopeRoutesByUserAndPreservesClientID(t *testing.T) {
	configValue := config.KafkaConfig{RecommendationEventsTopic: "recommendation-events"}
	for _, eventType := range []string{
		EventTypeRecommendationImpression, EventTypeRecommendationClick, EventTypeRecommendationReadEnd,
		EventTypeRecommendationFeedDwell, EventTypeRecommendationNotInterested,
	} {
		id := uuid.NewString()
		event, err := NewRecommendationBehaviorEnvelope(id, eventType, time.Unix(10, 0), testRecommendationPayload())
		if err != nil {
			t.Fatal(err)
		}
		if event.ID != id || event.Type != eventType || KeyForEvent(event) != "7" {
			t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
		}
		topic, err := TopicForEvent(configValue, event.Type)
		if err != nil || topic != "recommendation-events" {
			t.Fatalf("type=%q topic=%q err=%v", eventType, topic, err)
		}
	}
}

func TestArticleViewedEnvelopeUsesUserPartitionKey(t *testing.T) {
	id := uuid.NewString()
	event, err := NewArticleViewedEnvelope(id, 7, 42, time.Unix(10, 0), "article_detail")
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != id || event.Type != EventTypeArticleViewed || event.AggregateType != "user" || KeyForEvent(event) != "7" {
		t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
	}
	var payload UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != 7 || payload.ArticleID != 42 || payload.Action != "view" || payload.Source != "article_detail" {
		t.Fatalf("payload=%#v", payload)
	}
	if topic, err := TopicForEvent(config.KafkaConfig{UserBehaviorTopic: "user-behavior"}, event.Type); err != nil || topic != "user-behavior" {
		t.Fatalf("topic=%q err=%v", topic, err)
	}
}

func TestRecommendationBehaviorEnvelopeRejectsMissingStructuralFields(t *testing.T) {
	payload := testRecommendationPayload()
	payload.Position = 0
	if _, err := NewRecommendationBehaviorEnvelope(uuid.NewString(), EventTypeRecommendationClick, time.Now(), payload); err == nil {
		t.Fatal("expected missing position error")
	}
}
