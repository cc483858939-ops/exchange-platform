package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
)

type fakeUserBehaviorReader struct {
	messages []kafka.Message
	index    int
	commits  [][]kafka.Message
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
	return nil
}

func (*fakeUserBehaviorReader) Close() error {
	return nil
}

func TestUserBehaviorConsumerCommitsOnlyAfterSuccessfulApply(t *testing.T) {
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{{Value: []byte("{\"id\":\"view-1\",\"type\":\"post.viewed\"}")}}}
	applied := false
	err := consumeUserBehaviorMessages(context.Background(), reader, func(messages []kafka.Message) error {
		applied = true
		if len(messages) != 1 {
			t.Fatalf("messages=%d want=1", len(messages))
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
	reader := &fakeUserBehaviorReader{messages: []kafka.Message{{Value: []byte("{\"id\":\"view-1\",\"type\":\"post.viewed\"}")}}}
	wantErr := errors.New("database unavailable")
	err := consumeUserBehaviorMessages(context.Background(), reader, func([]kafka.Message) error {
		return wantErr
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
	record, err := decodeUserBehaviorEvent(mustUserBehaviorEnvelopeBytes(t, eventing.Envelope{
		ID: "like-state:7:42:5", Type: eventing.EventTypePostLiked,
		SchemaVersion: 1, OccurredAt: time.Now().UTC(), Payload: body,
	}))
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
