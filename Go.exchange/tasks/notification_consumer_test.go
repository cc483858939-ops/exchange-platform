package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type fakeNotificationMessageReader struct {
	message     kafka.Message
	fetchCalls  int
	commitErr   error
	commitCalls int
}

func (r *fakeNotificationMessageReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.fetchCalls++
	if err := ctx.Err(); err != nil {
		return kafka.Message{}, err
	}
	if r.fetchCalls == 1 {
		return r.message, nil
	}
	return kafka.Message{}, errors.New("no additional notification messages")
}

func (r *fakeNotificationMessageReader) CommitMessages(_ context.Context, _ ...kafka.Message) error {
	r.commitCalls++
	return r.commitErr
}

func (*fakeNotificationMessageReader) Close() error { return nil }

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

func TestNotificationDatabaseApplyRetriesThenSucceeds(t *testing.T) {
	attempts := 0
	err := retryNotificationDatabaseApply(context.Background(), kafkaRetryPolicy{MaxAttempts: 3}, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database transaction failed")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("retry result err=%v attempts=%d", err, attempts)
	}
}

func TestNotificationDatabaseRetryExhaustionRequiresRedelivery(t *testing.T) {
	attempts := 0
	err := retryNotificationDatabaseApply(context.Background(), kafkaRetryPolicy{MaxAttempts: 2}, func() error {
		attempts++
		return errors.New("database transaction failed")
	})
	if err == nil || attempts != 2 || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
		t.Fatalf("retry exhaustion err=%v class=%q code=%q attempts=%d", err, kafkaFailureClassOf(err), kafkaFailureCode(err), attempts)
	}
}

func TestNotificationDatabaseFailureIsNotSentToDLQ(t *testing.T) {
	originalConfig, originalDB := config.AppConfig, global.WorkerDb
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{
		ConsumerDLQTopic: "goexchange.consumer.dlq.v1", NotificationGroupID: "notification-test-group",
	}}
	global.WorkerDb = nil
	t.Cleanup(func() { config.AppConfig, global.WorkerDb = originalConfig, originalDB })

	now := time.Now().UTC()
	envelope, err := eventing.NewUserFollowCreatedEnvelope(uuid.NewString(), eventing.UserFollowCreatedPayload{
		FollowID: 10, FollowerID: 11, FollowingID: 12, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	err = processNotificationBatchWithRetry(context.Background(), []kafka.Message{notificationMessage(t, envelope)}, publisher, kafkaRetryPolicy{MaxAttempts: 2})
	if err == nil || kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
		t.Fatalf("database error=%v class=%q code=%q", err, kafkaFailureClassOf(err), kafkaFailureCode(err))
	}
	if len(publisher.topics) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("retryable DB failure was sent to DLQ: topics=%v messages=%d", publisher.topics, len(publisher.messages))
	}
}

func TestNotificationCommitFailureIsRetryableAndRequiresRedelivery(t *testing.T) {
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ConsumerDLQTopic: "goexchange.consumer.dlq.v1"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })

	reader := &fakeNotificationMessageReader{
		message:   kafka.Message{Topic: "goexchange.activity.events.v1", Partition: 2, Offset: 8, Value: []byte("bad-json")},
		commitErr: errors.New("commit acknowledgement unknown"),
	}
	publisher := &fakeRawKafkaMessagePublisher{}
	err := consumeNotificationMessages(context.Background(), reader, publisher)
	if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeKafkaCommit || reader.commitCalls != 1 {
		t.Fatalf("commit error=%v class=%q code=%q commits=%d", err, kafkaFailureClassOf(err), kafkaFailureCode(err), reader.commitCalls)
	}
	if len(publisher.messages) != 1 {
		t.Fatalf("commit failure triggered additional DLQ publishes: messages=%d", len(publisher.messages))
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
			name: "quote",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewQuoteCreatedEnvelope(id, eventing.QuoteCreatedPayload{
					QuotePostID: 100, TargetPostID: 42, ActorID: 7, TargetAuthorID: 9, CreatedAt: now,
				})
			},
			typeName: models.NotificationTypePostQuoted, dedupeKey: "post_quote:100", postID: 100, recipient: 9, actor: 7,
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
		{
			name: "self-quote",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewQuoteCreatedEnvelope(id, eventing.QuoteCreatedPayload{
					QuotePostID: 100, TargetPostID: 42, ActorID: 9, TargetAuthorID: 9, CreatedAt: now,
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

func TestDecodeNotificationActivityRejectsMalformedQuoteEvents(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	validEnvelope := func(t *testing.T) eventing.Envelope {
		t.Helper()
		envelope, err := eventing.NewQuoteCreatedEnvelope(uuid.NewString(), eventing.QuoteCreatedPayload{
			QuotePostID: 100, TargetPostID: 42, ActorID: 7, TargetAuthorID: 9, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	payloadMutation := func(mutate func(*eventing.QuoteCreatedPayload)) func(*eventing.Envelope) {
		return func(envelope *eventing.Envelope) {
			var payload eventing.QuoteCreatedPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			mutate(&payload)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			envelope.Payload = raw
		}
	}
	tests := []struct {
		name   string
		mutate func(*eventing.Envelope)
	}{
		{name: "wrong schema", mutate: func(e *eventing.Envelope) { e.SchemaVersion = 2 }},
		{name: "wrong aggregate type", mutate: func(e *eventing.Envelope) { e.AggregateType = "user" }},
		{name: "aggregate id mismatch", mutate: func(e *eventing.Envelope) { e.AggregateID = "101" }},
		{name: "zero quote post", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.QuotePostID = 0 })},
		{name: "zero target post", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.TargetPostID = 0 })},
		{name: "zero actor", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.ActorID = 0 })},
		{name: "zero target author", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.TargetAuthorID = 0 })},
		{name: "zero created at", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.CreatedAt = time.Time{} })},
		{name: "occurred at mismatch", mutate: payloadMutation(func(p *eventing.QuoteCreatedPayload) { p.CreatedAt = now.Add(time.Second) })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := validEnvelope(t)
			test.mutate(&envelope)
			_, err := decodeNotificationActivity(notificationMessage(t, envelope))
			if err == nil || kafkaFailureClassOf(err) != kafkaFailurePermanent {
				t.Fatalf("error=%v class=%q, want permanent malformed-event failure", err, kafkaFailureClassOf(err))
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
