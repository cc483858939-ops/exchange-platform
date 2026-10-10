package tasks

import (
	"context"
	"errors"
	"fmt"
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

func TestLikeBehaviorRelayIsolatesMalformedPairIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	base := uint(time.Now().UnixNano() & 0x3fffffff)
	good := likes.BehaviorPair(base, base+1)
	bad := likes.BehaviorPair(base, base+2)
	badPair := fmt.Sprintf("bad-pair-%d", base)
	validState := fmt.Sprintf("1|3|%d", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro())
	t.Cleanup(func() {
		client.SRem(likes.BehaviorDirtyKey, good, bad, badPair)
		client.HDel(likes.BehaviorStateKey, good, bad, badPair)
		client.ZRem(likes.BehaviorProcessingKey, good, bad, badPair)
		client.HDel(likes.BehaviorClaimsKey, good, bad, badPair)
	})
	if err := client.SAdd(likes.BehaviorDirtyKey, good, bad, badPair).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(likes.BehaviorStateKey, good, validState).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(likes.BehaviorStateKey, bad, "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(likes.BehaviorStateKey, badPair, validState).Err(); err != nil {
		t.Fatal(err)
	}
	publisher := &relayTestPublisher{}
	if err := runLikeBehaviorRelayBatch(t.Context(), likes.NewStore(client), publisher); err == nil {
		t.Fatal("malformed Pair was not reported")
	}
	goodEventID := fmt.Sprintf("like-state:%d:%d:3", base, base+1)
	goodEvents := 0
	for _, event := range publisher.events {
		if event.ID == goodEventID {
			goodEvents++
		}
	}
	if goodEvents != 1 {
		t.Fatalf("healthy Pair published %d events, want 1", goodEvents)
	}
	if dirty, err := client.SIsMember(likes.BehaviorDirtyKey, bad).Result(); err != nil || !dirty {
		t.Fatalf("bad Pair lost: dirty=%t err=%v", dirty, err)
	}
	if dirty, err := client.SIsMember(likes.BehaviorDirtyKey, badPair).Result(); err != nil || !dirty {
		t.Fatalf("malformed Pair lost: dirty=%t err=%v", dirty, err)
	}
	if state, err := client.HGet(likes.BehaviorStateKey, bad).Result(); err != nil || state != "corrupt" {
		t.Fatalf("bad State changed: %q err=%v", state, err)
	}
	if exists, err := client.HExists(likes.BehaviorStateKey, good).Result(); err != nil || exists {
		t.Fatalf("healthy Pair not ACKed: exists=%t err=%v", exists, err)
	}
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
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := resetLikeRelayIntegrationQueues(client); err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis integration client: %v", err)
		}
	})
	originalRedis := global.RedisDB
	global.RedisDB = client
	t.Cleanup(func() { global.RedisDB = originalRedis })
	ctx := context.Background()
	store := likes.NewStore(client)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1
	pair := likes.BehaviorPair(userID, postID)
	t.Cleanup(func() {
		if err := resetLikeRelayIntegrationQueues(client); err != nil {
			t.Errorf("reset Like relay integration queues: %v", err)
		}
	})
	t.Cleanup(func() {
		if err := cleanupLikeRelayIntegrationState(client, []uint{postID}, []uint{userID}); err != nil {
			t.Errorf("cleanup Like relay integration state: %v", err)
		}
	})
	if err := cleanupLikeRelayIntegrationState(client, []uint{postID}, []uint{userID}); err != nil {
		t.Fatal(err)
	}
	if created, err := initializeLikeStore(store, ctx, postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(ctx, userID); err != nil {
		t.Fatal(err)
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
