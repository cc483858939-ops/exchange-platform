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
		UserID: 7, PostID: 11, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
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

func TestValidateRecommendationProvenanceCoversAllThreeValidStates(t *testing.T) {
	valid := []struct {
		opportunity bool
		mode        string
		reason      string
	}{
		{false, RecommendationSelectionModeRanked, ""},
		{true, RecommendationSelectionModeRanked, ""},
		{true, RecommendationSelectionModeExploration, RecommendationExplorationReasonRecent},
		{true, RecommendationSelectionModeExploration, RecommendationExplorationReasonNovelAuthor},
		{true, RecommendationSelectionModeExploration, RecommendationExplorationReasonRecentNovelAuthor},
	}
	for _, tc := range valid {
		if err := ValidateRecommendationProvenance(tc.opportunity, tc.mode, tc.reason); err != nil {
			t.Fatalf("valid provenance rejected: %#v err=%v", tc, err)
		}
	}
	invalid := []struct {
		opportunity bool
		mode        string
		reason      string
	}{
		{false, RecommendationSelectionModeExploration, RecommendationExplorationReasonRecent},
		{false, RecommendationSelectionModeRanked, RecommendationExplorationReasonRecent},
		{true, RecommendationSelectionModeRanked, RecommendationExplorationReasonRecent},
		{true, RecommendationSelectionModeExploration, ""},
		{true, RecommendationSelectionModeExploration, "unknown"},
	}
	for _, tc := range invalid {
		if err := ValidateRecommendationProvenance(tc.opportunity, tc.mode, tc.reason); err == nil {
			t.Fatalf("invalid provenance accepted: %#v", tc)
		}
	}
}

func TestRecommendationBehaviorEnvelopePreservesRankedExplorationOpportunity(t *testing.T) {
	payload := testRecommendationPayload()
	payload.ExplorationOpportunity = true
	payload.SelectionMode = RecommendationSelectionModeRanked
	payload.ExplorationReason = ""
	event, err := NewRecommendationBehaviorEnvelope(uuid.NewString(), EventTypeRecommendationImpression, time.Now(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RecommendationBehaviorPayload
	if err := json.Unmarshal(event.Payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.ExplorationOpportunity || decoded.SelectionMode != RecommendationSelectionModeRanked || decoded.ExplorationReason != "" {
		t.Fatalf("decoded provenance=%#v", decoded)
	}
}

func TestPostViewedEnvelopeUsesUserPartitionKey(t *testing.T) {
	id := uuid.NewString()
	event, err := NewPostViewedEnvelope(id, 7, 42, time.Unix(10, 0), "post_detail")
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != id || event.Type != EventTypePostViewed || event.AggregateType != "user" || KeyForEvent(event) != "7" {
		t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
	}
	var payload UserBehaviorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != 7 || payload.PostID != 42 || payload.Action != "view" || payload.Source != "post_detail" {
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
	cfg := config.KafkaConfig{Brokers: []string{"kafka:9092"}, UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot", RecommendationEventsTopic: "recommendation", PostEmbeddingTopic: "embedding", ActivityEventsTopic: "activity", NotificationDLQTopic: "notification-dlq", TopicReplicationFactor: 1, UserBehaviorPartitions: 12, LikeSnapshotPartitions: 6, RecommendationEventsPartitions: 12, PostEmbeddingPartitions: 6, ActivityEventsPartitions: 12, NotificationDLQPartitions: 3}
	specs, err := RequiredKafkaTopics(cfg)
	if err != nil {
		t.Fatal(err)
	}
	provisioned := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		provisioned[spec.Name] = struct{}{}
	}
	for _, eventType := range []string{EventTypePostViewed, EventTypePostLiked, EventTypePostUnliked, EventTypePostEmbeddingRequested, EventTypePostLikeSnapshot, EventTypeRecommendationImpression, EventTypeRecommendationClick, EventTypeRecommendationReadEnd, EventTypeRecommendationFeedDwell, EventTypeRecommendationNotInterested} {
		topic, err := TopicForEvent(cfg, eventType)
		if err != nil {
			t.Fatalf("event type %q: %v", eventType, err)
		}
		if _, ok := provisioned[topic]; !ok {
			t.Fatalf("event type %q resolves to unprovisioned topic %q", eventType, topic)
		}
	}
}

func TestPostEmbeddingRequestedEnvelopeUsesPostKey(t *testing.T) {
	now := time.Date(2026, 8, 18, 1, 2, 3, 4, time.FixedZone("CST", 8*60*60))
	event, err := NewPostEmbeddingRequestedEnvelope(uuid.NewString(), 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != 1 || event.AggregateType != "post" || event.AggregateID != "42" || KeyForEvent(event) != "42" || !event.OccurredAt.Equal(now.UTC()) {
		t.Fatalf("event=%#v key=%q", event, KeyForEvent(event))
	}
	var payload PostEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.PostID != 42 {
		t.Fatalf("payload=%#v err=%v", payload, err)
	}
}

func TestPostEmbeddingRequestedEnvelopeRejectsInvalidFields(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name       string
		id         string
		postID     uint
		occurredAt time.Time
	}{
		{name: "id", id: "bad", postID: 1, occurredAt: now},
		{name: "post", id: uuid.NewString(), postID: 0, occurredAt: now},
		{name: "occurred at", id: uuid.NewString(), postID: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPostEmbeddingRequestedEnvelope(test.id, test.postID, test.occurredAt); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
