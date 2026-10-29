package tasks

import (
	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/likes"
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v7"
	"log"
	"os"
	"strconv"
	"testing"
	"time"
)

func relayBatchClient(t testing.TB, benchmark bool) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to an isolated Redis")
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	if benchmark {
		if db == 15 {
			t.Fatal("benchmark DB15 must differ from REDIS_TEST_DB")
		}
		if os.Getenv("LIKE_RELAY_BENCH_ALLOW_FLUSHDB") != "1" {
			t.Skip("set LIKE_RELAY_BENCH_ALLOW_FLUSHDB=1 for disposable DB15")
		}
		db = 15
	}
	c := redis.NewClient(&redis.Options{Addr: addr, DB: db, Password: os.Getenv("REDIS_TEST_PASSWORD")})
	if err := c.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func seedRelayBatch(t testing.TB, c *redis.Client, n int) []uint {
	t.Helper()
	if err := resetLikeRelayIntegrationQueues(c); err != nil {
		t.Fatal(err)
	}
	ids := make([]uint, n)
	pipe := c.Pipeline()
	for i := range ids {
		id := uint(970000 + i)
		ids[i] = id
		pipe.MSet(likes.ReadyKey(id), "1", likes.CountKey(id), "42", likes.VersionKey(id), "7")
		pipe.SAdd(likes.DirtyKey, id)
	}
	if _, err := pipe.Exec(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanupLikeRelayIntegrationState(c, ids, nil); _ = resetLikeRelayIntegrationQueues(c) })
	return ids
}

type relayBatchPublisher struct {
	calls int
	count int
	run   func() error
}

func (p *relayBatchPublisher) Publish(context.Context, eventing.Envelope) error {
	return errors.New("unexpected single publish")
}
func (p *relayBatchPublisher) Close() error { return nil }
func (p *relayBatchPublisher) PublishBatch(_ context.Context, events []eventing.Envelope) error {
	p.calls++
	p.count += len(events)
	if p.run != nil {
		return p.run()
	}
	return nil
}

type relayBatchHook struct {
	delay            time.Duration
	reads, pipelines int
	pipelineFailure  error
}

func (h *relayBatchHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	if cmd.Name() == "mget" {
		h.reads++
	}
	return ctx, nil
}
func (*relayBatchHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *relayBatchHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	h.pipelines++
	return ctx, h.pipelineFailure
}
func (*relayBatchHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func TestLikeSnapshotRelayBatchIntegration(t *testing.T) {
	t.Run("hundred-posts-bounded-reads-and-pipelined-ack", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 100)
		store := likes.NewStore(c)
		// Prime the existing ACK script, so this measures the steady path.
		if _, err := store.AckClaim(t.Context(), likes.SnapshotClaim{PostID: 1, ClaimID: "stale"}); err != nil {
			t.Fatal(err)
		}
		hook := &relayBatchHook{}
		c.AddHook(hook)
		p := &relayBatchPublisher{}
		if err := runLikeSnapshotRelayBatch(t.Context(), store, p); err != nil {
			t.Fatal(err)
		}
		if p.calls != 1 || p.count != 100 || hook.reads != 4 || hook.pipelines != 1 {
			t.Fatalf("publish=%d items=%d reads=%d pipelines=%d", p.calls, p.count, hook.reads, hook.pipelines)
		}
		if c.HLen(likes.ClaimsKey).Val() != 0 || c.ZCard(likes.ProcessingKey).Val() != 0 {
			t.Fatal("claims not acknowledged")
		}
	})
	t.Run("mutation-during-send-remains-dirty", func(t *testing.T) {
		c := relayBatchClient(t, false)
		ids := seedRelayBatch(t, c, 3)
		store := likes.NewStore(c)
		p := &relayBatchPublisher{run: func() error {
			_, err := c.TxPipelined(func(pipe redis.Pipeliner) error {
				pipe.MSet(likes.CountKey(ids[0]), "43", likes.VersionKey(ids[0]), "8")
				pipe.SAdd(likes.DirtyKey, ids[0])
				return nil
			})
			return err
		}}
		if err := runLikeSnapshotRelayBatch(t.Context(), store, p); err != nil {
			t.Fatal(err)
		}
		if !c.SIsMember(likes.DirtyKey, ids[0]).Val() {
			t.Fatal("ACK lost mutation made during publish")
		}
		next := &relayBatchPublisher{}
		if err := runLikeSnapshotRelayBatch(t.Context(), store, next); err != nil || next.count != 1 {
			t.Fatalf("next count=%d err=%v", next.count, err)
		}
	})
	t.Run("publish-failure-requeues-entire-batch", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 100)
		err := runLikeSnapshotRelayBatch(t.Context(), likes.NewStore(c), &relayBatchPublisher{run: func() error { return errors.New("broker unavailable") }})
		if err == nil || c.SCard(likes.DirtyKey).Val() != 100 || c.HLen(likes.ClaimsKey).Val() != 0 {
			t.Fatalf("err=%v dirty=%d claims=%d", err, c.SCard(likes.DirtyKey).Val(), c.HLen(likes.ClaimsKey).Val())
		}
	})
	t.Run("one-corrupt-post-does-not-block-valid-posts", func(t *testing.T) {
		c := relayBatchClient(t, false)
		ids := seedRelayBatch(t, c, 4)
		c.Del(likes.CountKey(ids[0]))
		c.SAdd(likes.CountKey(ids[0]), "wrong type")
		p := &relayBatchPublisher{}
		if err := runLikeSnapshotRelayBatch(t.Context(), likes.NewStore(c), p); err == nil {
			t.Fatal("corrupt state not reported")
		}
		if p.count != 3 || c.SCard(likes.DirtyKey).Val() != 1 || !c.SIsMember(likes.DirtyKey, ids[0]).Val() {
			t.Fatalf("published=%d dirty=%d", p.count, c.SCard(likes.DirtyKey).Val())
		}
	})
	t.Run("pipeline-failure-preserves-claims-for-retry", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 3)
		s := likes.NewStore(c)
		claims, err := s.ClaimDirty(t.Context(), 3, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		failure := errors.New("injected pipeline transport failure")
		hook := &relayBatchHook{pipelineFailure: failure}
		c.AddHook(hook)
		results, err := s.AckClaims(t.Context(), claims)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if !errors.Is(result.Err, failure) || result.Applied {
				t.Fatalf("result=%+v", result)
			}
		}
		if c.HLen(likes.ClaimsKey).Val() != 3 {
			t.Fatal("failed ACK lost claims")
		}
		hook.pipelineFailure = nil
		results, err = s.AckClaims(t.Context(), claims)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if result.Err != nil || !result.Applied {
				t.Fatalf("retry=%+v", result)
			}
		}
	})
	t.Run("stale-token-script-cache-miss-and-cancellation", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 3)
		s := likes.NewStore(c)
		claims, err := s.ClaimDirty(t.Context(), 3, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		old := claims[0]
		c.HSet(likes.ClaimsKey, old.PostID, "replacement")
		// Dedicated test Redis only: force the pipeline NOSCRIPT path.
		if err := c.ScriptFlush().Err(); err != nil {
			t.Fatal(err)
		}
		results, err := s.AckClaims(t.Context(), claims)
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Applied || results[0].Err != nil || !results[1].Applied || !results[2].Applied {
			t.Fatalf("ACK results=%+v", results)
		}
		if c.HGet(likes.ClaimsKey, strconv.Itoa(int(old.PostID))).Val() != "replacement" {
			t.Fatal("stale ACK removed new owner")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := s.RequeueClaims(ctx, []likes.SnapshotClaim{{PostID: old.PostID, ClaimID: "replacement"}}); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled result=%v", err)
		}
		c.ScriptFlush()
		retry, err := s.RequeueClaims(t.Context(), []likes.SnapshotClaim{{PostID: old.PostID, ClaimID: "replacement"}})
		if err != nil || !retry[0].Applied || !c.SIsMember(likes.DirtyKey, old.PostID).Val() {
			t.Fatalf("requeue=%+v err=%v", retry, err)
		}
	})
}

func BenchmarkLikeSnapshotRelayBatch(b *testing.B) {
	for _, delay := range []time.Duration{0, time.Millisecond} {
		for _, legacy := range []bool{true, false} {
			name := "batch"
			if legacy {
				name = "serial"
			}
			b.Run(fmt.Sprintf("%s/delay-%s", name, delay), func(b *testing.B) {
				c := relayBatchClient(b, true)
				if err := c.FlushDB().Err(); err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { c.FlushDB() })
				ids := seedRelayBatch(b, c, 100)
				s := likes.NewStore(c)
				run := runLikeSnapshotRelayBatch
				if legacy {
					run = runLegacyLikeSnapshotRelayBatch
				}
				if err := run(b.Context(), s, &relayBatchPublisher{}); err != nil {
					b.Fatal(err)
				}
				h := &relayBatchHook{}
				c.AddHook(h)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					args := make([]interface{}, len(ids))
					for i, id := range ids {
						args[i] = id
					}
					if err := c.SAdd(likes.DirtyKey, args...).Err(); err != nil {
						b.Fatal(err)
					}
					h.delay = delay
					b.StartTimer()
					p := &relayBatchPublisher{}
					if err := run(b.Context(), s, p); err != nil || p.count != 100 {
						b.Fatalf("published=%d err=%v", p.count, err)
					}
					b.StopTimer()
					h.delay = 0
				}
				elapsed := b.Elapsed().Seconds()
				b.ReportMetric(float64(b.N*100)/elapsed, "posts/s")
			})
		}
	}
}

type pendingLikeSnapshot struct {
	claim    likes.SnapshotClaim
	envelope eventing.Envelope
}

func runLegacyLikeSnapshotRelayBatch(ctx context.Context, store *likes.Store, publisher eventing.Publisher) error {
	if _, err := store.ReapExpired(ctx, config.LikeSnapshotBatchSize()); err != nil {
		log.Printf("[LikeSnapshotRelay] reap expired claims: %v", err)
		return fmt.Errorf("reap expired claims: %w", err)
	}
	claims, err := store.ClaimDirty(ctx, config.LikeSnapshotBatchSize(), config.LikeClaimLease())
	if err != nil {
		log.Printf("[LikeSnapshotRelay] claim dirty posts: %v", err)
		return fmt.Errorf("claim dirty posts: %w", err)
	}
	pending := make([]pendingLikeSnapshot, 0, len(claims))
	events := make([]eventing.Envelope, 0, len(claims))
	var firstErr error
	for _, claim := range claims {
		snapshot, err := store.LoadSnapshot(ctx, claim.PostID)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] load post=%d claim=%s: %v", claim.PostID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", claim.PostID, requeueErr)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("load post=%d: %w", claim.PostID, err)
			}
			continue
		}
		envelope, err := eventing.NewLikeSnapshotEnvelope(snapshot.PostID, snapshot.Count, snapshot.Version)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] envelope post=%d claim=%s: %v", claim.PostID, claim.ClaimID, err)
			if _, requeueErr := store.RequeueClaim(ctx, claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", claim.PostID, requeueErr)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("create post=%d envelope: %w", claim.PostID, err)
			}
			continue
		}
		pending = append(pending, pendingLikeSnapshot{claim: claim, envelope: envelope})
		events = append(events, envelope)
	}
	if err := publishEnvelopeBatch(ctx, publisher, events); err != nil {
		log.Printf("[LikeSnapshotRelay] publish batch: %v", err)
		for _, item := range pending {
			if _, requeueErr := store.RequeueClaim(ctx, item.claim); requeueErr != nil {
				log.Printf("[LikeSnapshotRelay] requeue post=%d: %v", item.claim.PostID, requeueErr)
			}
		}
		return fmt.Errorf("publish snapshot batch: %w", err)
	}
	for _, item := range pending {
		acked, err := store.AckClaim(ctx, item.claim)
		if err != nil {
			log.Printf("[LikeSnapshotRelay] ack post=%d claim=%s: %v", item.claim.PostID, item.claim.ClaimID, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("ack post=%d claim=%s: %w", item.claim.PostID, item.claim.ClaimID, err)
			}
			continue
		}
		if !acked {
			log.Printf("[LikeSnapshotRelay] stale claim ignored post=%d claim=%s", item.claim.PostID, item.claim.ClaimID)
		}
	}
	return firstErr
}
