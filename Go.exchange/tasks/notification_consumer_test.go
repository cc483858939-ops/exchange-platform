package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func notificationMessage(t *testing.T, envelope eventing.Envelope) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.activity.events.v1", Partition: 2, Offset: 8, Value: raw}
}

func TestNotificationMalformedMessageUsesUnifiedConsumerDLQ(t *testing.T) {
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ConsumerDLQTopic: "goexchange.consumer.dlq.v1"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })

	message := kafka.Message{
		Topic: "goexchange.activity.events.v1", Partition: 2, Offset: 8,
		Key: []byte{0x00, 0xff}, Value: []byte{0xff, 0x00},
		Headers: []kafka.Header{{Key: "trace", Value: []byte{0x00, 0xff}}},
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	if err := processNotificationBatch(context.Background(), []kafka.Message{message}, publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.topics) != 1 || publisher.topics[0] != "goexchange.consumer.dlq.v1" || len(publisher.messages) != 1 {
		t.Fatalf("published topics=%v messages=%d", publisher.topics, len(publisher.messages))
	}
	record, err := eventing.DecodeDeadLetterRecord(publisher.messages[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if record.Consumer != kafkaConsumerNotificationProjection || record.Source.Topic != message.Topic || record.Source.Partition != message.Partition || record.Source.Offset != message.Offset ||
		string(record.Source.Key) != string(message.Key) || string(record.Source.Value) != string(message.Value) || len(record.Source.Headers) != 1 || string(record.Source.Headers[0].Value) != string(message.Headers[0].Value) {
		t.Fatalf("dead letter source was not preserved: %+v", record)
	}
	if record.Failure.Class != string(kafkaFailurePermanent) || record.Failure.Code != kafkaFailureCodeDecodeEnvelope {
		t.Fatalf("failure=%+v want permanent decode failure", record.Failure)
	}
}

func TestDecodeNotificationActivityMapsDomainEvents(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		make       func(string) (eventing.Envelope, error)
		typeName   string
		dedupeKey  string
		postID     uint
		recipient  uint
		actor      uint
		sourceVers int64
	}{
		{
			name: "like",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewPostReactionAppliedEnvelope(id, eventing.PostReactionAppliedPayload{
					ActorID: 7, PostID: 42, PostAuthorID: 9, Liked: true, ReactionVersion: 3, StateChangedAt: now,
				})
			},
			typeName: models.NotificationTypePostLiked, dedupeKey: "post_like:7:42", postID: 42, recipient: 9, actor: 7, sourceVers: 3,
		},
		{
			name: "comment",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewReplyCreatedEnvelope(id, eventing.ReplyCreatedPayload{
					ReplyPostID: 11, ParentPostID: 42, ConversationID: 42, ActorID: 7, ParentAuthorID: 9, CreatedAt: now,
				})
			},
			typeName: models.NotificationTypePostReplied, dedupeKey: "post_reply:11", postID: 11, recipient: 9, actor: 7,
		},
		{
			name: "follow",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewUserFollowCreatedEnvelope(id, eventing.UserFollowCreatedPayload{
					FollowID: 13, FollowerID: 7, FollowingID: 9, CreatedAt: now,
				})
			},
			typeName: models.NotificationTypeUserFollowed, dedupeKey: "user_followed:7:9", recipient: 9, actor: 7, sourceVers: 13,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := test.make(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			record, err := decodeNotificationActivity(notificationMessage(t, envelope))
			if err != nil {
				t.Fatal(err)
			}
			if record.Candidate == nil {
				t.Fatal("expected notification candidate")
			}
			candidate := record.Candidate
			if candidate.Type != test.typeName || candidate.DedupeKey != test.dedupeKey || candidate.RecipientID != test.recipient || candidate.ActorID != test.actor || candidate.SourceVersion != test.sourceVers {
				t.Fatalf("candidate=%+v", candidate)
			}
			if test.postID == 0 && candidate.PostID != nil || test.postID != 0 && (candidate.PostID == nil || *candidate.PostID != test.postID) {
				t.Fatalf("post_id=%v", candidate.PostID)
			}
		})
	}
}

func TestDecodeNotificationActivitySuppressesValidNoOpEvents(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		envelope func(string) (eventing.Envelope, error)
	}{
		{
			name: "unlike",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewPostReactionAppliedEnvelope(id, eventing.PostReactionAppliedPayload{
					ActorID: 7, PostID: 42, PostAuthorID: 9, Liked: false, ReactionVersion: 2, StateChangedAt: now,
				})
			},
		},
		{
			name: "self-like",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewPostReactionAppliedEnvelope(id, eventing.PostReactionAppliedPayload{
					ActorID: 9, PostID: 42, PostAuthorID: 9, Liked: true, ReactionVersion: 1, StateChangedAt: now,
				})
			},
		},
		{
			name: "self-comment",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewReplyCreatedEnvelope(id, eventing.ReplyCreatedPayload{
					ReplyPostID: 11, ParentPostID: 42, ConversationID: 42, ActorID: 9, ParentAuthorID: 9, CreatedAt: now,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := test.envelope(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			record, err := decodeNotificationActivity(notificationMessage(t, envelope))
			if err != nil {
				t.Fatal(err)
			}
			if record.Candidate != nil {
				t.Fatalf("candidate=%+v, want valid no-op", record.Candidate)
			}
		})
	}
}

func TestDecodeNotificationActivityRejectsMalformedRelevantEnvelope(t *testing.T) {
	envelope, err := eventing.NewReplyCreatedEnvelope(uuid.NewString(), eventing.ReplyCreatedPayload{
		ReplyPostID: 11, ParentPostID: 42, ConversationID: 42, ActorID: 7, ParentAuthorID: 9, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope.ID = "not-a-uuid"
	if _, err := decodeNotificationActivity(notificationMessage(t, envelope)); err == nil {
		t.Fatal("expected malformed event id to be rejected")
	}
}
