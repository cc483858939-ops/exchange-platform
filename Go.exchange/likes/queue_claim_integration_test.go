package likes

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func queueClaimTestClient(t *testing.T) *redis.Client {
	t.Helper()
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
	return client
}

func snapshotClaimTestKeys(t *testing.T, client *redis.Client) []string {
	t.Helper()
	prefix := fmt.Sprintf("test:like:claim:%d", time.Now().UnixNano())
	keys := []string{prefix + ":dirty", prefix + ":processing", prefix + ":claims"}
	t.Cleanup(func() { client.Del(keys...) })
	return keys
}

func TestSnapshotClaimBoundedLargeDirtyIntegration(t *testing.T) {
	client := queueClaimTestClient(t)
	for _, size := range []int{10, 10_000, 100_000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			keys := snapshotClaimTestKeys(t, client)
			for start := 1; start <= size; start += 1000 {
				end := min(start+999, size)
				members := make([]interface{}, 0, end-start+1)
				for id := start; id <= end; id++ {
					members = append(members, id)
				}
				if err := client.SAdd(keys[0], members...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			batch := min(size, 100)
			if size == 10 {
				batch = 3
			}
			value, err := claimScript.Run(client.WithContext(t.Context()), keys, batch, time.Now().Add(time.Minute).UnixMilli(), "owner").Result()
			if err != nil {
				t.Fatal(err)
			}
			claims, err := parseSnapshotClaimReply(value, batch)
			if err != nil || len(claims) != batch {
				t.Fatalf("claims=%d want=%d err=%v", len(claims), batch, err)
			}
			if dirty, err := client.SCard(keys[0]).Result(); err != nil || dirty != int64(size-batch) {
				t.Fatalf("dirty=%d want=%d err=%v", dirty, size-batch, err)
			}
			if processing, err := client.ZCard(keys[1]).Result(); err != nil || processing != int64(batch) {
				t.Fatalf("processing=%d want=%d err=%v", processing, batch, err)
			}
			if claimCount, err := client.HLen(keys[2]).Result(); err != nil || claimCount != int64(batch) {
				t.Fatalf("claim count=%d want=%d err=%v", claimCount, batch, err)
			}
			// Go callers clamp too, but the Lua entry point must reject an oversized batch.
			if _, err := claimScript.Run(client.WithContext(t.Context()), keys, 101, 1, "oversized").Result(); err == nil {
				t.Fatal("Lua accepted an oversized claim batch")
			}
		})
	}
}

func TestSnapshotClaimOwnershipAndRecoveryIntegration(t *testing.T) {
	client := queueClaimTestClient(t)
	keys := snapshotClaimTestKeys(t, client)
	for id := 1; id <= 10; id++ {
		if err := client.SAdd(keys[0], id).Err(); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make([][]SnapshotClaim, 2)
	errorsByWorker := make([]error, 2)
	for worker := range results {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			value, err := claimScript.Run(client, keys, 10, time.Now().Add(time.Minute).UnixMilli(), fmt.Sprintf("worker-%d", worker)).Result()
			if err == nil {
				results[worker], err = parseSnapshotClaimReply(value, 10)
			}
			errorsByWorker[worker] = err
		}(worker)
	}
	wg.Wait()
	claims := make(map[uint]SnapshotClaim)
	for worker, batch := range results {
		if errorsByWorker[worker] != nil {
			t.Fatal(errorsByWorker[worker])
		}
		for _, claim := range batch {
			if _, duplicate := claims[claim.PostID]; duplicate {
				t.Fatalf("two workers claimed Post %d", claim.PostID)
			}
			claims[claim.PostID] = claim
		}
	}
	if len(claims) != 10 {
		t.Fatalf("claimed %d Posts, want 10", len(claims))
	}

	// A new mutation re-dirties a claimed Post; its old ACK cannot remove it.
	if err := client.SAdd(keys[0], 1).Err(); err != nil {
		t.Fatal(err)
	}
	if acked, err := ackClaimScript.Run(client, []string{keys[1], keys[2]}, 1, claims[1].ClaimID).Int64(); err != nil || acked != 1 {
		t.Fatalf("ACK=%d err=%v", acked, err)
	}
	if dirty, err := client.SIsMember(keys[0], 1).Result(); err != nil || !dirty {
		t.Fatalf("new Dirty lost: dirty=%t err=%v", dirty, err)
	}

	// A Dirty Post with a live claim is retained, without an unbounded retry loop.
	if err := client.SAdd(keys[0], 2).Err(); err != nil {
		t.Fatal(err)
	}
	value, err := claimScript.Run(client, keys, 10, time.Now().Add(time.Minute).UnixMilli(), "occupied").Result()
	if err != nil {
		t.Fatal(err)
	}
	newClaims, err := parseSnapshotClaimReply(value, 10)
	if err != nil || len(newClaims) != 1 || newClaims[0].PostID != 1 {
		t.Fatalf("claims=%v err=%v", newClaims, err)
	}
	if dirty, err := client.SIsMember(keys[0], 2).Result(); err != nil || !dirty {
		t.Fatalf("occupied Dirty lost: dirty=%t err=%v", dirty, err)
	}

	if err := client.ZAdd(keys[1], &redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: 2}).Err(); err != nil {
		t.Fatal(err)
	}
	if reaped, err := reapExpiredScript.Run(client, keys, time.Now().UnixMilli(), 10).Int64(); err != nil || reaped != 1 {
		t.Fatalf("reaped=%d err=%v", reaped, err)
	}
	value, err = claimScript.Run(client, keys, 10, time.Now().Add(time.Minute).UnixMilli(), "new-owner").Result()
	if err != nil {
		t.Fatal(err)
	}
	newClaims, err = parseSnapshotClaimReply(value, 10)
	if err != nil || len(newClaims) != 1 || newClaims[0].PostID != 2 {
		t.Fatalf("reclaimed=%v err=%v", newClaims, err)
	}
	if acked, err := ackClaimScript.Run(client, []string{keys[1], keys[2]}, 2, claims[2].ClaimID).Int64(); err != nil || acked != 0 {
		t.Fatalf("late ACK=%d err=%v", acked, err)
	}
	if requeued, err := requeueClaimScript.Run(client, keys, 2, claims[2].ClaimID).Int64(); err != nil || requeued != 0 {
		t.Fatalf("late requeue=%d err=%v", requeued, err)
	}
	if current, err := client.HGet(keys[2], "2").Result(); err != nil || current != newClaims[0].ClaimID {
		t.Fatalf("new claim changed: %q err=%v", current, err)
	}
}

func TestSnapshotClaimTypeFaultAndEmptyQueueIntegration(t *testing.T) {
	client := queueClaimTestClient(t)
	keys := snapshotClaimTestKeys(t, client)
	value, err := claimScript.Run(client, keys, 3, time.Now().Add(time.Minute).UnixMilli(), "empty").Result()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := parseSnapshotClaimReply(value, 3)
	if err != nil || len(claims) != 0 {
		t.Fatalf("empty claims=%v err=%v", claims, err)
	}
	if err := client.SAdd(keys[0], 1).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(keys[2], "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := claimScript.Run(client, keys, 3, time.Now().Add(time.Minute).UnixMilli(), "fault").Result(); !errors.Is(mapScriptError(err), ErrLikeRedisType) {
		t.Fatalf("claim type fault=%v", err)
	}
	if dirty, err := client.SIsMember(keys[0], 1).Result(); err != nil || !dirty {
		t.Fatalf("type fault changed Dirty: dirty=%t err=%v", dirty, err)
	}
	if exists, err := client.Exists(keys[1]).Result(); err != nil || exists != 0 {
		t.Fatalf("type fault created Processing: exists=%d err=%v", exists, err)
	}
}
