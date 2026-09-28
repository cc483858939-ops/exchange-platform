package dlq

import (
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
)

func replayTestKafkaConfig() config.KafkaConfig {
	return config.KafkaConfig{
		UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot", RecommendationEventsTopic: "recommendation",
		PostEmbeddingTopic: "embedding", ActivityEventsTopic: "activity", ConsumerDLQTopic: "consumer-dlq",
	}
}

func replayTestRecord(consumer, topic string) eventing.DeadLetterRecord {
	return eventing.DeadLetterRecord{
		SchemaVersion: eventing.DeadLetterSchemaVersion,
		Consumer:      consumer,
		Source:        eventing.DeadLetterSource{Topic: topic, Partition: 1, Offset: 22, Key: []byte("key"), Value: []byte("value")},
		Failure:       eventing.DeadLetterFailure{Class: "permanent", Code: "decode_envelope", Attempts: 1, FailedAt: time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)},
	}
}

func TestReplayPolicyAllowsConfiguredConsumerTopicPairs(t *testing.T) {
	registry, err := NewReplayPolicyRegistry(replayTestKafkaConfig())
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]ReplaySafetyMode{
		"like_snapshot_projection": ReplaySafetyVersioned,
		"user_behavior_projection": ReplaySafetyInbox,
		"recommendation_metrics":   ReplaySafetyInbox,
		"post_embedding":           ReplaySafetyReconcile,
		"notification_projection":  ReplaySafetyInbox,
	}
	for consumer, mode := range wants {
		topic := map[string]string{
			"like_snapshot_projection": "snapshot", "user_behavior_projection": "behavior",
			"recommendation_metrics": "recommendation", "post_embedding": "embedding", "notification_projection": "activity",
		}[consumer]
		policy, err := registry.Validate(replayTestRecord(consumer, topic))
		if err != nil || policy.SafetyMode != mode {
			t.Fatalf("consumer=%s policy=%+v err=%v want mode=%s", consumer, policy, err, mode)
		}
	}
}

func TestReplayPolicyRejectsUnknownMismatchedAndDLQTopics(t *testing.T) {
	registry, err := NewReplayPolicyRegistry(replayTestKafkaConfig())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		record  eventing.DeadLetterRecord
		wantErr string
	}{
		{name: "wrong topic", record: replayTestRecord("like_snapshot_projection", "behavior"), wantErr: "does not match"},
		{name: "unknown consumer", record: replayTestRecord("unknown", "snapshot"), wantErr: "unknown replay consumer"},
		{name: "DLQ source", record: replayTestRecord("user_behavior_projection", "consumer-dlq"), wantErr: "cannot be replayed"},
		{name: "empty source", record: replayTestRecord("user_behavior_projection", " "), wantErr: "source topic is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registry.Validate(test.record); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validation error=%v want %q", err, test.wantErr)
			}
		})
	}
}

func TestNewReplayPolicyRegistryRejectsMissingOrDLQSourceTopics(t *testing.T) {
	cfg := replayTestKafkaConfig()
	cfg.PostEmbeddingTopic = ""
	if _, err := NewReplayPolicyRegistry(cfg); err == nil {
		t.Fatal("empty configured source topic was accepted")
	}
	cfg = replayTestKafkaConfig()
	cfg.ActivityEventsTopic = cfg.ConsumerDLQTopic
	if _, err := NewReplayPolicyRegistry(cfg); err == nil {
		t.Fatal("DLQ as a configured source topic was accepted")
	}
}

func TestBuildReplayPlanContainsOnlySafeReplayMetadata(t *testing.T) {
	registry, err := NewReplayPolicyRegistry(replayTestKafkaConfig())
	if err != nil {
		t.Fatal(err)
	}
	located := LocatedRecord{DLQTopic: "consumer-dlq", DLQPartition: 2, DLQOffset: 45, Record: replayTestRecord("post_embedding", "embedding")}
	plan, err := BuildReplayPlan(located, registry)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceTopic != "embedding" || plan.SourcePartition != 1 || plan.SourceOffset != 22 || plan.SafetyMode != ReplaySafetyReconcile || plan.KeySize != 3 || plan.ValueSize != 5 {
		t.Fatalf("plan=%+v", plan)
	}
}
