package likes

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

// SPEC-02-FIX comparison only; production never runs this all-member scan.
var legacySnapshotQueueClaimScript = redis.NewScript(`
local ids = redis.call('SMEMBERS', KEYS[1])
local result = {}
local limit = tonumber(ARGV[1])
for _, post_id in ipairs(ids) do
  if #result >= limit * 2 then break end
  if redis.call('HEXISTS', KEYS[3], post_id) == 0 then
    local claim_id = ARGV[3] .. ':' .. post_id
    redis.call('SREM', KEYS[1], post_id)
    redis.call('HSET', KEYS[3], post_id, claim_id)
    redis.call('ZADD', KEYS[2], ARGV[2], post_id)
    table.insert(result, post_id)
    table.insert(result, claim_id)
  end
end
return result
`)

// This manual benchmark never flushes a DB. It uses unique keys and requires
// an explicitly confirmed disposable loopback Redis instance.
func TestLikeQueueBenchmarkIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_LIKE_QUEUE_BENCH_ADDR")
	dbText := os.Getenv("REDIS_LIKE_QUEUE_BENCH_DB")
	if addr == "" || dbText == "" || os.Getenv("REDIS_LIKE_QUEUE_BENCH_CONFIRM") != "dedicated-disposable" {
		t.Skip("set REDIS_LIKE_QUEUE_BENCH_ADDR, REDIS_LIKE_QUEUE_BENCH_DB and REDIS_LIKE_QUEUE_BENCH_CONFIRM=dedicated-disposable")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		t.Fatal("benchmark Redis must be an explicitly dedicated loopback endpoint")
	}
	db, err := strconv.Atoi(dbText)
	if err != nil || db < 0 {
		t.Fatal("REDIS_LIKE_QUEUE_BENCH_DB must be a nonnegative integer")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	rounds := 20
	if requested, err := strconv.Atoi(os.Getenv("REDIS_LIKE_QUEUE_BENCH_ROUNDS")); err == nil && requested >= 3 && requested <= 100 {
		rounds = requested
	}
	sizes := []int{1_000, 10_000, 100_000}
	if os.Getenv("REDIS_LIKE_QUEUE_BENCH_MILLION") == "1" {
		sizes = append(sizes, 1_000_000)
	}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("snapshot_%d", size), func(t *testing.T) {
			keys := snapshotClaimTestKeys(t, client)
			for start := 1; start <= size; start += 1000 {
				members := make([]interface{}, 0, min(1000, size-start+1))
				for id := start; id <= min(start+999, size); id++ {
					members = append(members, id)
				}
				if err := client.SAdd(keys[0], members...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			memoryBefore := client.Info("memory").Val()
			cpuBefore := client.Info("cpu").Val()
			claimTimes := make([]time.Duration, 0, rounds)
			ackTimes := make([]time.Duration, 0, rounds)
			for round := 0; round < rounds; round++ {
				started := time.Now()
				value, err := claimScript.Run(client, keys, 100, time.Now().Add(time.Minute).UnixMilli(), fmt.Sprintf("round-%d", round)).Result()
				claimTimes = append(claimTimes, time.Since(started))
				if err != nil {
					t.Fatal(err)
				}
				claims, err := parseSnapshotClaimReply(value, 100)
				if err != nil || len(claims) != 100 {
					t.Fatalf("claims=%d err=%v", len(claims), err)
				}
				started = time.Now()
				for _, claim := range claims {
					if _, err := ackClaimScript.Run(client, []string{keys[1], keys[2]}, claim.PostID, claim.ClaimID).Int64(); err != nil {
						t.Fatal(err)
					}
				}
				ackTimes = append(ackTimes, time.Since(started))
				members := make([]interface{}, 0, len(claims))
				for _, claim := range claims {
					members = append(members, claim.PostID)
				}
				if err := client.SAdd(keys[0], members...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			memoryAfter := client.Info("memory").Val()
			cpuAfter := client.Info("cpu").Val()
			requeueTimes := make([]time.Duration, 0, rounds)
			reapTimes := make([]time.Duration, 0, rounds)
			for round := 0; round < rounds; round++ {
				value, err := claimScript.Run(client, keys, 100, time.Now().Add(time.Minute).UnixMilli(), fmt.Sprintf("requeue-%d", round)).Result()
				if err != nil {
					t.Fatal(err)
				}
				claims, err := parseSnapshotClaimReply(value, 100)
				if err != nil || len(claims) != 100 {
					t.Fatalf("requeue setup claims=%d err=%v", len(claims), err)
				}
				started := time.Now()
				for _, claim := range claims {
					if requeued, err := requeueClaimScript.Run(client, keys, claim.PostID, claim.ClaimID).Int64(); err != nil || requeued != 1 {
						t.Fatalf("requeue=%d err=%v", requeued, err)
					}
				}
				requeueTimes = append(requeueTimes, time.Since(started))
				if _, err := claimScript.Run(client, keys, 100, time.Now().Add(-time.Second).UnixMilli(), fmt.Sprintf("reap-%d", round)).Result(); err != nil {
					t.Fatal(err)
				}
				started = time.Now()
				if reaped, err := reapExpiredScript.Run(client, keys, time.Now().UnixMilli(), 100).Int64(); err != nil || reaped != 100 {
					t.Fatalf("reap=%d err=%v", reaped, err)
				}
				reapTimes = append(reapTimes, time.Since(started))
			}
			keyBytes, _ := client.MemoryUsage(keys[0]).Result()
			t.Logf("size=%d rounds=%d claim=%s claim_ops_per_s=%.1f ack_batch=%s ack_pairs_per_s=%.1f requeue_batch=%s requeue_pairs_per_s=%.1f reap_batch=%s reap_pairs_per_s=%.1f dirty_key_bytes=%d memory_before=%q memory_after=%q cpu_before=%q cpu_after=%q", size, rounds, queueBenchQuantiles(claimTimes), queueBenchRate(claimTimes, 1), queueBenchQuantiles(ackTimes), queueBenchRate(ackTimes, 100), queueBenchQuantiles(requeueTimes), queueBenchRate(requeueTimes, 100), queueBenchQuantiles(reapTimes), queueBenchRate(reapTimes, 100), keyBytes, queueBenchInfoValue(memoryBefore, "used_memory"), queueBenchInfoValue(memoryAfter, "used_memory"), queueBenchInfoValue(cpuBefore, "used_cpu_sys"), queueBenchInfoValue(cpuAfter, "used_cpu_sys"))
		})
		t.Run(fmt.Sprintf("snapshot_legacy_%d", size), func(t *testing.T) {
			keys := snapshotClaimTestKeys(t, client)
			for start := 1; start <= size; start += 1000 {
				members := make([]interface{}, 0, min(1000, size-start+1))
				for id := start; id <= min(start+999, size); id++ {
					members = append(members, id)
				}
				if err := client.SAdd(keys[0], members...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			claimTimes := make([]time.Duration, 0, rounds)
			for round := 0; round < rounds; round++ {
				started := time.Now()
				value, err := legacySnapshotQueueClaimScript.Run(client, keys, 100, time.Now().Add(time.Minute).UnixMilli(), fmt.Sprintf("legacy-%d", round)).Result()
				claimTimes = append(claimTimes, time.Since(started))
				if err != nil {
					t.Fatal(err)
				}
				claims, err := parseSnapshotClaimReply(value, 100)
				if err != nil || len(claims) != 100 {
					t.Fatalf("claims=%d err=%v", len(claims), err)
				}
				members := make([]interface{}, 0, len(claims))
				for _, claim := range claims {
					if _, err := ackClaimScript.Run(client, []string{keys[1], keys[2]}, claim.PostID, claim.ClaimID).Int64(); err != nil {
						t.Fatal(err)
					}
					members = append(members, claim.PostID)
				}
				if err := client.SAdd(keys[0], members...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("size=%d rounds=%d legacy_snapshot_claim=%s legacy_claim_ops_per_s=%.1f", size, rounds, queueBenchQuantiles(claimTimes), queueBenchRate(claimTimes, 1))
		})
		t.Run(fmt.Sprintf("behavior_%d", size), func(t *testing.T) {
			prefix := fmt.Sprintf("test:like:behavior-bench:%d", time.Now().UnixNano())
			keys := []string{prefix + ":dirty", prefix + ":state", prefix + ":processing", prefix + ":claims"}
			t.Cleanup(func() { client.Del(keys...) })
			for start := 1; start <= size; start += 500 {
				pipe := client.Pipeline()
				for id := start; id <= min(start+499, size); id++ {
					pair := fmt.Sprintf("1:%d", id)
					pipe.SAdd(keys[0], pair)
					pipe.HSet(keys[1], pair, "1|1|2026-01-01T00:00:00Z")
				}
				if _, err := pipe.Exec(); err != nil {
					t.Fatal(err)
				}
			}
			claimTimes := make([]time.Duration, 0, rounds)
			ackTimes := make([]time.Duration, 0, rounds)
			for round := 0; round < rounds; round++ {
				started := time.Now()
				value, err := behaviorClaimScript.Run(client, []string{keys[0], keys[2], keys[3]}, 100, time.Now().Add(time.Minute).UnixMilli(), fmt.Sprintf("round-%d", round)).Result()
				claimTimes = append(claimTimes, time.Since(started))
				if err != nil {
					t.Fatal(err)
				}
				claims, err := parseBehaviorClaimReply(value, 100)
				if err != nil || len(claims) != 100 {
					t.Fatalf("claims=%d err=%v", len(claims), err)
				}
				started = time.Now()
				for _, claim := range claims {
					if _, err := behaviorAckScript.Run(client, keys, claim.Pair, claim.ClaimID, 1).Int64(); err != nil {
						t.Fatal(err)
					}
				}
				ackTimes = append(ackTimes, time.Since(started))
				pipe := client.Pipeline()
				for _, claim := range claims {
					pipe.SAdd(keys[0], claim.Pair)
					pipe.HSet(keys[1], claim.Pair, "1|1|2026-01-01T00:00:00Z")
				}
				if _, err := pipe.Exec(); err != nil {
					t.Fatal(err)
				}
			}
			keyBytes, _ := client.MemoryUsage(keys[0]).Result()
			t.Logf("size=%d rounds=%d behavior_claim=%s claim_ops_per_s=%.1f behavior_ack_batch=%s ack_pairs_per_s=%.1f dirty_key_bytes=%d", size, rounds, queueBenchQuantiles(claimTimes), queueBenchRate(claimTimes, 1), queueBenchQuantiles(ackTimes), queueBenchRate(ackTimes, 100), keyBytes)
		})
	}
}

func queueBenchQuantiles(samples []time.Duration) string {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	quantile := func(p float64) time.Duration {
		index := int(float64(len(samples)-1)*p + 0.5)
		return samples[index]
	}
	return fmt.Sprintf("p50=%s p95=%s p99=%s", quantile(0.5), quantile(0.95), quantile(0.99))
}

func queueBenchRate(samples []time.Duration, operationsPerSample int) float64 {
	var elapsed time.Duration
	for _, sample := range samples {
		elapsed += sample
	}
	if elapsed == 0 {
		return 0
	}
	return float64(len(samples)*operationsPerSample) / elapsed.Seconds()
}

func queueBenchInfoValue(info, key string) string {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return "unavailable"
}
