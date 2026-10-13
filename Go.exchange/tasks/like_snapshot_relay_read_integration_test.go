package tasks

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

type snapshotRelayCommandHook struct {
	commands []string
}

func (h *snapshotRelayCommandHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	h.commands = append(h.commands, cmd.Name())
	return ctx, nil
}

func (*snapshotRelayCommandHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (*snapshotRelayCommandHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*snapshotRelayCommandHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestLikeSnapshotRelayPublishesAtomicSnapshotIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis snapshot relay integration test")
	}
	db, err := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	if err != nil && os.Getenv("REDIS_TEST_DB") != "" {
		t.Fatalf("parse REDIS_TEST_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	if err := client.Ping().Err(); err != nil {
		_ = client.Close()
		t.Fatalf("ping Redis integration service: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis integration client: %v", err)
		}
	})

	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	if postID == 0 {
		postID = 1
	}
	postIDString := strconv.FormatUint(uint64(postID), 10)
	cleanup := func() error {
		pipe := client.Pipeline()
		pipe.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.VersionKey(postID))
		pipe.SRem(likes.DirtyKey, postIDString)
		pipe.ZRem(likes.ProcessingKey, postIDString)
		pipe.HDel(likes.ClaimsKey, postIDString)
		_, err := pipe.Exec()
		return err
	}
	if err := cleanup(); err != nil {
		t.Fatalf("clear snapshot relay test Post %d: %v", postID, err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("clean up snapshot relay test Post %d: %v", postID, err)
		}
	})
	if err := client.MSet(
		likes.ReadyKey(postID), "1",
		likes.CountKey(postID), "42",
		likes.VersionKey(postID), "7",
	).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(likes.DirtyKey, postIDString).Err(); err != nil {
		t.Fatal(err)
	}

	hook := &snapshotRelayCommandHook{}
	client.AddHook(hook)
	publisher := &relayTestPublisher{}
	if err := runLikeSnapshotRelayBatch(t.Context(), likes.NewStore(client), publisher); err != nil {
		t.Fatalf("run snapshot relay batch: %v", err)
	}
	if publisher.batchCalls != 1 || len(publisher.events) != 1 {
		t.Fatalf("publisher batch calls=%d events=%d, want one published event", publisher.batchCalls, len(publisher.events))
	}
	var mgetCalls int
	for _, command := range hook.commands {
		if command == "mget" {
			mgetCalls++
		}
	}
	if mgetCalls != 1 {
		t.Fatalf("relay used %d MGET calls, want one atomic aggregate read", mgetCalls)
	}
	event := publisher.events[0]
	if event.Type != eventing.EventTypePostLikeSnapshot {
		t.Fatalf("published event type=%q, want %q", event.Type, eventing.EventTypePostLikeSnapshot)
	}
	var payload eventing.PostLikeSnapshotPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode snapshot payload: %v", err)
	}
	if payload != (eventing.PostLikeSnapshotPayload{PostID: postID, LikeCount: 42, Version: 7}) {
		t.Fatalf("snapshot payload=%+v, want PostID=%d Count=42 Version=7", payload, postID)
	}
}
