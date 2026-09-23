package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type fakeUserBehaviorReader struct {
	messages   []kafka.Message
	index      int
	commits    [][]kafka.Message
	commitErr  error
	closeCount int
}

func (r *fakeUserBehaviorReader) FetchMessage(_ context.Context) (kafka.Message, error) {
	if r.index >= len(r.messages) {
		return kafka.Message{}, io.EOF
	}
	message := r.messages[r.index]
	r.index++
	return message, nil
}

func (r *fakeUserBehaviorReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.commits = append(r.commits, append([]kafka.Message(nil), messages...))
	return r.commitErr
}

func (r *fakeUserBehaviorReader) Close() error {
	r.closeCount++
	return nil
}

func TestUserBehaviorConsumerCommitsOnlyAfterSuccessfulApply(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustUserBehaviorRecoveryMessage(t, 0, "view-1", eventing.EventTypePostViewed, 7, 42, 0)}}
	applied := false
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, nil, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(_ context.Context, _ *gorm.DB, _ string, _ config.KafkaConfig, records []userBehaviorEventRecord) error {
		applied = true
		if len(records) != 1 {
			t.Fatalf("records=%d want=1", len(records))
		}
		return nil
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("consume error=%v", err)
	}
	if !applied || len(reader.commits) != 1 || len(reader.commits[0]) != 1 {
		t.Fatalf("applied=%t commits=%d", applied, len(reader.commits))
	}
}

func TestUserBehaviorConsumerDoesNotCommitWhenApplyFails(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{mustUserBehaviorRecoveryMessage(t, 0, "view-1", eventing.EventTypePostViewed, 7, 42, 0)}}
	wantErr := errors.New("database unavailable")
	err := consumeUserBehaviorMessagesWithApply(context.Background(), reader, nil, nil, userBehaviorRecoveryConfig(), kafkaRetryPolicy{MaxAttempts: 1}, func(context.Context, *gorm.DB, string, config.KafkaConfig, []userBehaviorEventRecord) error {
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, wantErr)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("consume error=%v want=%v", err, wantErr)
	}
	if len(reader.commits) != 0 {
		t.Fatalf("commits=%d want=0", len(reader.commits))
	}
}

func TestDecodeUserBehaviorEventAllowsVersionedLikeEventID(t *testing.T) {
	body, err := json.Marshal(eventing.UserBehaviorPayload{
		UserID: 7, PostID: 42, Action: "like", LikeVersion: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeUserBehaviorMessage(kafka.Message{Value: mustUserBehaviorEnvelopeBytes(t, eventing.Envelope{
		ID: "like-state:7:42:5", Type: eventing.EventTypePostLiked,
		SchemaVersion: 1, OccurredAt: time.Now().UTC(), Payload: body,
	})})
	if err != nil {
		t.Fatalf("decode error=%v", err)
	}
	if record.Envelope.ID != "like-state:7:42:5" || record.Payload.LikeVersion != 5 {
		t.Fatalf("record=%#v", record)
	}
}

func TestCollectUserBehaviorBatchStopsAt500(t *testing.T) {
	messages := make([]kafka.Message, 500)
	for index := range messages {
		messages[index] = kafka.Message{Offset: int64(index)}
	}
	reader := &fakeUserBehaviorReader{messages: messages}
	batch := collectUserBehaviorBatch(context.Background(), reader, kafka.Message{Offset: -1})
	if len(batch) != userBehaviorBatchSize {
		t.Fatalf("batch=%d want=%d", len(batch), userBehaviorBatchSize)
	}
	if batch[0].Offset != -1 || batch[499].Offset != 498 {
		t.Fatalf("batch order/limit unexpected first=%d last=%d", batch[0].Offset, batch[499].Offset)
	}
}

func TestAggregateUserBehaviorViewsUsesMaximumOccurredAt(t *testing.T) {
	early := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	late := early.Add(2 * time.Minute)
	records := []userBehaviorEventRecord{
		{Envelope: eventing.Envelope{ID: "view-1", Type: eventing.EventTypePostViewed, OccurredAt: late}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42}},
		{Envelope: eventing.Envelope{ID: "view-2", Type: eventing.EventTypePostViewed, OccurredAt: early}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42}},
	}
	aggregates := aggregateUserBehaviorViews(records, map[string]struct{}{"view-1": {}, "view-2": {}})
	if len(aggregates) != 1 || aggregates[0].Count != 2 || !aggregates[0].LastSeenAt.Equal(late) {
		t.Fatalf("aggregates=%#v", aggregates)
	}
}

func TestAggregatePostViewCountDeltasCountsOnlyFirstDeliveryViews(t *testing.T) {
	records := []userBehaviorEventRecord{
		{Envelope: eventing.Envelope{ID: "view-1", Type: eventing.EventTypePostViewed}, Payload: eventing.UserBehaviorPayload{PostID: 10}},
		{Envelope: eventing.Envelope{ID: "view-2", Type: eventing.EventTypePostViewed}, Payload: eventing.UserBehaviorPayload{PostID: 10}},
		{Envelope: eventing.Envelope{ID: "view-3", Type: eventing.EventTypePostViewed}, Payload: eventing.UserBehaviorPayload{PostID: 10}},
		{Envelope: eventing.Envelope{ID: "view-4", Type: eventing.EventTypePostViewed}, Payload: eventing.UserBehaviorPayload{PostID: 20}},
		{Envelope: eventing.Envelope{ID: "like-1", Type: eventing.EventTypePostLiked}, Payload: eventing.UserBehaviorPayload{PostID: 10}},
		{Envelope: eventing.Envelope{ID: "bad", Type: "unknown"}, Payload: eventing.UserBehaviorPayload{PostID: 10}},
	}
	deltas := aggregatePostViewCountDeltas(records, map[string]struct{}{"view-1": {}, "view-2": {}, "view-4": {}})
	if len(deltas) != 2 || deltas[10] != 2 || deltas[20] != 1 {
		t.Fatalf("deltas=%#v", deltas)
	}
}

func TestCollapseUserBehaviorReactionsUsesHighestVersionAndEarliestEqualTie(t *testing.T) {
	now := time.Now().UTC()
	records := []userBehaviorEventRecord{
		{Envelope: eventing.Envelope{ID: "v5", Type: eventing.EventTypePostLiked, OccurredAt: now}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42, LikeVersion: 5}},
		{Envelope: eventing.Envelope{ID: "v7", Type: eventing.EventTypePostUnliked, OccurredAt: now}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42, LikeVersion: 7}},
		{Envelope: eventing.Envelope{ID: "v6", Type: eventing.EventTypePostLiked, OccurredAt: now}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42, LikeVersion: 6}},
		{Envelope: eventing.Envelope{ID: "v7-conflict", Type: eventing.EventTypePostLiked, OccurredAt: now}, Payload: eventing.UserBehaviorPayload{UserID: 7, PostID: 42, LikeVersion: 7}},
	}
	candidates := collapseUserBehaviorReactions(records, map[string]struct{}{
		"v5": {}, "v7": {}, "v6": {}, "v7-conflict": {},
	})
	if len(candidates) != 1 || candidates[0].Payload.LikeVersion != 7 || candidates[0].Liked {
		t.Fatalf("candidates=%#v", candidates)
	}
}

func mustUserBehaviorEnvelopeBytes(t *testing.T, event eventing.Envelope) []byte {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
