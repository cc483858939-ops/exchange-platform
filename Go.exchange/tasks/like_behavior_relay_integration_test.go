package tasks

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"

	"github.com/go-redis/redis/v7"
)

type relayTestPublisher struct {
	fail       bool
	batchCalls int
	events     []eventing.Envelope
}

func (p *relayTestPublisher) Publish(_ context.Context, event eventing.Envelope) error {
	if p.fail {
		return errors.New("kafka unavailable")
	}
	p.events = append(p.events, event)
	return nil
}

func (p *relayTestPublisher) PublishBatch(_ context.Context, events []eventing.Envelope) error {
	if p.fail {
		return errors.New("kafka unavailable")
	}
	p.batchCalls++
	p.events = append(p.events, events...)
	return nil
}

func (*relayTestPublisher) Close() error { return nil }

func TestLikeBehaviorRelayBatchesAndAcksAfterPublishIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	originalRedis := global.RedisDB
	global.RedisDB = client
	defer func() { global.RedisDB = originalRedis }()
	ctx := context.Background()
	store := likes.NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1
	pair := likes.BehaviorPair(userID, postID)
	cleanup := func() {
		client.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		client.SRem(likes.DirtyKey, postID)
		client.SRem(likes.BehaviorDirtyKey, pair)
		client.HDel(likes.BehaviorStateKey, pair)
		client.ZRem(likes.BehaviorProcessingKey, pair)
		client.HDel(likes.BehaviorClaimsKey, pair)
	}
	cleanup()
	defer cleanup()
	if created, err := store.Initialize(ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	if _, err := store.Mutate(ctx, userID, postID, true); err != nil {
		t.Fatal(err)
	}
	failed := &relayTestPublisher{fail: true}
	if err := runLikeBehaviorRelayBatch(ctx, store, failed); err == nil {
		t.Fatal("expected Kafka failure")
	}
	if dirty, _ := client.SIsMember(likes.BehaviorDirtyKey, pair).Result(); !dirty {
		t.Fatal("failed publish did not requeue pair")
	}
	success := &relayTestPublisher{}
	if err := runLikeBehaviorRelayBatch(ctx, store, success); err != nil {
		t.Fatal(err)
	}
	if success.batchCalls != 1 || len(success.events) != 1 {
		t.Fatalf("batchCalls=%d events=%d", success.batchCalls, len(success.events))
	}
	wantID := "like-state:" + strconv.FormatUint(uint64(userID), 10) + ":" + strconv.FormatUint(uint64(postID), 10) + ":1"
	if success.events[0].ID != wantID {
		t.Fatalf("event=%+v want=%s", success.events[0], wantID)
	}
	if exists, _ := client.HExists(likes.BehaviorStateKey, pair).Result(); exists {
		t.Fatal("published state was not acknowledged")
	}
}
