package eventing

import (
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

func activityKafkaConfig() config.KafkaConfig {
	return config.KafkaConfig{
		ActivityEventsTopic: "goexchange.activity.events.v1",
	}
}

func TestActivityEnvelopesUseCanonicalIdentityAndKeys(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 11, 12, 0, time.UTC)
	tests := []struct {
		name          string
		makeEnvelope  func(string) (Envelope, error)
		aggregateType string
		aggregateID   string
		key           string
	}{
		{
			name: "reaction",
			makeEnvelope: func(id string) (Envelope, error) {
				return NewPostReactionAppliedEnvelope(id, PostReactionAppliedPayload{
					ActorID: 7, PostID: 42, PostAuthorID: 9, Liked: true,
					ReactionVersion: 3, StateChangedAt: now,
				})
			},
			aggregateType: "post_reaction", aggregateID: "7:42", key: "7:42",
		},
		{
			name: "reply",
			makeEnvelope: func(id string) (Envelope, error) {
				return NewReplyCreatedEnvelope(id, ReplyCreatedPayload{
					ReplyPostID: 11, ParentPostID: 42, ConversationID: 42, ActorID: 7, ParentAuthorID: 9, CreatedAt: now,
				})
			},
			aggregateType: "post", aggregateID: "11", key: "42",
		},
		{
			name: "follow",
			makeEnvelope: func(id string) (Envelope, error) {
				return NewUserFollowCreatedEnvelope(id, UserFollowCreatedPayload{
					FollowID: 13, FollowerID: 7, FollowingID: 9, CreatedAt: now,
				})
			},
			aggregateType: "user_follow", aggregateID: "13", key: "7:9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := uuid.NewString()
			envelope, err := test.makeEnvelope(id)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.ID != id || envelope.SchemaVersion != 1 || envelope.AggregateType != test.aggregateType || envelope.AggregateID != test.aggregateID || !envelope.OccurredAt.Equal(now) {
				t.Fatalf("envelope=%+v", envelope)
			}
			if got := KeyForEvent(envelope); got != test.key {
				t.Fatalf("key=%q want=%q", got, test.key)
			}
			row, err := NewOutboxEvent(activityKafkaConfig(), envelope)
			if err != nil {
				t.Fatal(err)
			}
			if row.ID != envelope.ID || row.Topic != "goexchange.activity.events.v1" || row.PartitionKey != test.key || row.EventType != envelope.Type || row.SchemaVersion != envelope.SchemaVersion || row.AggregateType != envelope.AggregateType || row.AggregateID != envelope.AggregateID {
				t.Fatalf("outbox row=%+v", row)
			}
			var serialized Envelope
			if err := json.Unmarshal([]byte(row.Message), &serialized); err != nil {
				t.Fatal(err)
			}
			if serialized.ID != envelope.ID || serialized.Type != envelope.Type || serialized.AggregateID != envelope.AggregateID {
				t.Fatalf("serialized envelope=%+v", serialized)
			}
		})
	}
}

func TestNewOutboxEventRejectsMissingActivityTopic(t *testing.T) {
	envelope, err := NewReplyCreatedEnvelope(uuid.NewString(), ReplyCreatedPayload{
		ReplyPostID: 1, ParentPostID: 2, ConversationID: 2, ActorID: 3, ParentAuthorID: 4, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewOutboxEvent(config.KafkaConfig{}, envelope); err == nil {
		t.Fatal("expected missing activity topic error")
	}
}
