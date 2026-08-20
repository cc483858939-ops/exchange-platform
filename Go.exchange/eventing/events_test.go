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
		RankerVersion: "embedding_v1", RankerConfigHash: "0123456789ab", StrategyID: "embedding_feed_v1", ReceivedAt: now,
		SelectionMode: RecommendationSelectionModeRanked,
	}
}

func TestRecommendationBehaviorEnvelopeRoutesByUserAndPreservesClientID(t *testing.T) {
	configValue := config.KafkaConfig{RecommendationEventsTopic: "recommendation-events"}
	for _, eventType := range []string{EventTypeRecommendationImpression, EventTypeRecommendationClick, EventTypeRecommendationReadEnd, EventTypeRecommendationFeedDwell, EventTypeRecommendationNotInterested} {
		id := uuid.NewString()
		event, err := NewRecommendationBehaviorEnvelope(id, eventType, time.Unix(10, 0), testRecommendationPayload())
		if err != nil {
			t.Fatal(err)
		}
		if event.ID != id || event.Type != eventType || KeyForEvent(event) != "7" {
			t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
		}
		if event.SchemaVersion != RecommendationBehaviorSchemaVersion {
			t.Fatalf("schema=%d want=%d", event.SchemaVersion, RecommendationBehaviorSchemaVersion)
		}
		topic, err := TopicForEvent(configValue, event.Type)
		if err != nil || topic != "recommendation-events" {
			t.Fatalf("type=%q topic=%q err=%v", eventType, topic, err)
		}
	}
}

func TestRecommendationBehaviorEnvelopeRejectsInvalidProvenance(t *testing.T) {
	payload := testRecommendationPayload()
	payload.SelectionMode = RecommendationSelectionModeExploration
	payload.ExplorationReason = RecommendationExplorationReasonRecent
	if _, err := NewRecommendationBehaviorEnvelope(uuid.NewString(), EventTypeRecommendationImpression, time.Now(), payload); err == nil {
		t.Fatal("exploration without opportunity must be rejected")
	}
	payload.ExplorationOpportunity = true
	if _, err := NewRecommendationBehaviorEnvelope(uuid.NewString(), EventTypeRecommendationImpression, time.Now(), payload); err != nil {
		t.Fatal(err)
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
	cfg := config.KafkaConfig{Brokers: []string{"kafka:9092"}, UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot", RecommendationEventsTopic: "recommendation", ArticleEmbeddingTopic: "embedding", TopicReplicationFactor: 1, UserBehaviorPartitions: 12, LikeSnapshotPartitions: 6, RecommendationEventsPartitions: 12, ArticleEmbeddingPartitions: 6}
	specs, err := RequiredKafkaTopics(cfg)
	if err != nil {
		t.Fatal(err)
	}
	provisioned := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		provisioned[spec.Name] = struct{}{}
	}
	for _, eventType := range []string{EventTypeArticleViewed, EventTypeArticleLiked, EventTypeArticleUnliked, EventTypeArticleEmbeddingRequested, EventTypeArticleLikeSnapshot, EventTypeRecommendationImpression, EventTypeRecommendationClick, EventTypeRecommendationReadEnd, EventTypeRecommendationFeedDwell, EventTypeRecommendationNotInterested} {
		topic, err := TopicForEvent(cfg, eventType)
		if err != nil {
			t.Fatalf("event type %q: %v", eventType, err)
		}
		if _, ok := provisioned[topic]; !ok {
			t.Fatalf("event type %q resolves to unprovisioned topic %q", eventType, topic)
		}
	}
}

func TestArticleEmbeddingRequestedEnvelopeUsesArticleKey(t *testing.T) {
	now := time.Date(2026, 8, 18, 1, 2, 3, 4, time.FixedZone("CST", 8*60*60))
	event, err := NewArticleEmbeddingRequestedEnvelope(uuid.NewString(), 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != 1 || event.AggregateType != "article" || event.AggregateID != "42" || KeyForEvent(event) != "42" || !event.OccurredAt.Equal(now.UTC()) {
		t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
	}
	var payload ArticleEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ArticleID != 42 {
		t.Fatalf("payload=%#v err=%v", payload, err)
	}
}

func TestArticleEmbeddingRequestedEnvelopeRejectsInvalidFields(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name       string
		id         string
		articleID  uint
		occurredAt time.Time
	}{
		{name: "id", id: "bad", articleID: 1, occurredAt: now},
		{name: "article", id: uuid.NewString(), articleID: 0, occurredAt: now},
		{name: "occurred at", id: uuid.NewString(), articleID: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewArticleEmbeddingRequestedEnvelope(test.id, test.articleID, test.occurredAt); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
