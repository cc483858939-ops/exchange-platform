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
func TestSupportedEventTypesResolveToProvisionedTopics(t *testing.T) {
	cfg := config.KafkaConfig{
		Brokers:              []string{"kafka:9092"},
		ArticleAnalysisTopic: "analysis", ArticleAnalysisDLQTopic: "analysis-dlq",
		UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot", RecommendationEventsTopic: "recommendation",
		TopicReplicationFactor:    1,
		ArticleAnalysisPartitions: 3, ArticleAnalysisDLQPartitions: 3, UserBehaviorPartitions: 12,
		LikeSnapshotPartitions: 6, RecommendationEventsPartitions: 12,
	}
	specs, err := RequiredKafkaTopics(cfg)
	if err != nil {
		t.Fatal(err)
	}
	provisioned := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		provisioned[spec.Name] = struct{}{}
	}
	for _, eventType := range []string{
		EventTypeArticleAnalysisRequested, EventTypeArticleAnalysisDead,
		EventTypeArticleViewed, EventTypeArticleLiked, EventTypeArticleUnliked,
		EventTypeArticleLikeSnapshot,
		EventTypeRecommendationImpression, EventTypeRecommendationClick,
		EventTypeRecommendationReadEnd, EventTypeRecommendationFeedDwell,
		EventTypeRecommendationNotInterested,
	} {
		topic, err := TopicForEvent(cfg, eventType)
		if err != nil {
			t.Fatalf("event type %q: %v", eventType, err)
		}
		if _, ok := provisioned[topic]; !ok {
			t.Fatalf("event type %q resolves to unprovisioned topic %q", eventType, topic)
		}
	}
}
