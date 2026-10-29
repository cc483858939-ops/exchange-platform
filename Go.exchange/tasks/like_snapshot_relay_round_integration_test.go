package tasks

import (
	"Go.exchange/eventing"
	"Go.exchange/likes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTestPublisher struct {
	run func(context.Context, []eventing.Envelope) error
}

func (p roundTestPublisher) Publish(context.Context, eventing.Envelope) error {
	return errors.New("unexpected single publish")
}
func (p roundTestPublisher) Close() error { return nil }
func (p roundTestPublisher) PublishBatch(ctx context.Context, e []eventing.Envelope) error {
	return p.run(ctx, e)
}

func TestLikeSnapshotRelayRoundIntegration(t *testing.T) {
	t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", "1000")
	t.Setenv("LIKE_SNAPSHOT_BATCH_SIZE", "100")
	t.Setenv("LIKE_CLAIM_LEASE", "30s")
	t.Run("preclaim-entire-round-and-retain-redirty", func(t *testing.T) {
		c := relayBatchClient(t, false)
		ids := seedRelayBatch(t, c, 1000)
		calls, total := 0, 0
		pub := roundTestPublisher{run: func(_ context.Context, e []eventing.Envelope) error {
			calls++
			total += len(e)
			if len(e) > 100 {
				t.Fatal("publish chunk exceeds limit")
			}
			if calls == 1 {
				if n := c.HLen(likes.ClaimsKey).Val(); n != 1000 {
					t.Fatalf("first send owned=%d", n)
				}
				return c.SAdd(likes.DirtyKey, ids[0]).Err()
			}
			return nil
		}}
		if err := runLikeSnapshotRelayRound(t.Context(), likes.NewStore(c), pub); err != nil {
			t.Fatal(err)
		}
		if calls != 10 || total != 1000 || c.HLen(likes.ClaimsKey).Val() != 0 || c.SCard(likes.DirtyKey).Val() != 1 {
			t.Fatalf("calls=%d items=%d dirty=%d claims=%d", calls, total, c.SCard(likes.DirtyKey).Val(), c.HLen(likes.ClaimsKey).Val())
		}
	})
	t.Run("bounded-budget-leaves-unclaimed-work", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 1100)
		p := &relayBatchPublisher{}
		if err := runLikeSnapshotRelayRound(t.Context(), likes.NewStore(c), p); err != nil {
			t.Fatal(err)
		}
		if p.count != 1000 || c.SCard(likes.DirtyKey).Val() != 100 {
			t.Fatalf("sent=%d dirty=%d", p.count, c.SCard(likes.DirtyKey).Val())
		}
	})
	t.Run("failed-publish-releases-unsent-chunks", func(t *testing.T) {
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 1000)
		p := &relayBatchPublisher{run: func() error { return errors.New("broker failed") }}
		if err := runLikeSnapshotRelayRound(t.Context(), likes.NewStore(c), p); err == nil {
			t.Fatal("failure lost")
		}
		if p.calls != 1 || c.SCard(likes.DirtyKey).Val() != 1000 || c.HLen(likes.ClaimsKey).Val() != 0 {
			t.Fatalf("calls=%d dirty=%d claims=%d", p.calls, c.SCard(likes.DirtyKey).Val(), c.HLen(likes.ClaimsKey).Val())
		}
	})
	t.Run("round-deadline-releases-work-with-separate-budget", func(t *testing.T) {
		t.Setenv("LIKE_SNAPSHOT_ROUND_TIMEOUT", "100ms")
		c := relayBatchClient(t, false)
		seedRelayBatch(t, c, 1000)
		entered := false
		p := roundTestPublisher{run: func(ctx context.Context, _ []eventing.Envelope) error { entered = true; <-ctx.Done(); return ctx.Err() }}
		err := runLikeSnapshotRelayRound(t.Context(), likes.NewStore(c), p)
		if !errors.Is(err, context.DeadlineExceeded) || !entered {
			t.Fatalf("entered=%t err=%v", entered, err)
		}
		if c.HLen(likes.ClaimsKey).Val() != 0 || c.SCard(likes.DirtyKey).Val() != 1000 {
			t.Fatal("deadline cleanup lost work")
		}
	})
}

func TestLikeSnapshotRelayLoopDoesNotOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	go func() {
		defer close(done)
		runLikeSnapshotRelayLoop(ctx, time.Millisecond, func(context.Context) error {
			if calls.Add(1) == 1 {
				close(entered)
			}
			<-release
			return nil
		}, func(error) {})
	}()
	<-entered
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Error("slow rounds overlap")
	}
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
}

// Opt-in sustained load against disposable Redis DB15. Kafka is a publisher
// substitute, so this measures scheduling/Redis capacity, not broker capacity.
func TestLikeSnapshotRelaySustainedLoadIntegration(t *testing.T) {
	if os.Getenv("LIKE_RELAY_SUSTAINED_LOAD") != "1" {
		t.Skip("set LIKE_RELAY_SUSTAINED_LOAD=1 and disposable benchmark Redis settings")
	}
	for _, budget := range []int{100, 1000} {
		t.Run(fmt.Sprintf("round-%d", budget), func(t *testing.T) {
			t.Setenv("LIKE_SNAPSHOT_ROUND_SIZE", strconv.Itoa(budget))
			t.Setenv("LIKE_SNAPSHOT_BATCH_SIZE", "100")
			t.Setenv("LIKE_SNAPSHOT_ROUND_TIMEOUT", "5s")
			t.Setenv("LIKE_CLAIM_LEASE", "30s")
			c := relayBatchClient(t, true)
			if err := c.FlushDB().Err(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.FlushDB() })
			var mu sync.Mutex
			created := map[uint]time.Time{}
			received := map[uint]time.Time{}
			var active, maxActive atomic.Int32
			p := roundTestPublisher{run: func(ctx context.Context, e []eventing.Envelope) error {
				n := active.Add(1)
				defer active.Add(-1)
				if n > maxActive.Load() {
					maxActive.Store(n)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
				now := time.Now()
				mu.Lock()
				defer mu.Unlock()
				for _, event := range e {
					var payload eventing.PostLikeSnapshotPayload
					if err := json.Unmarshal(event.Payload, &payload); err != nil {
						return err
					}
					received[payload.PostID] = now
				}
				return nil
			}}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			roundErrors := make(chan error, 32)
			go func() {
				defer close(done)
				runLikeSnapshotRelayLoop(ctx, time.Second, func(ctx context.Context) error { return runLikeSnapshotRelayRound(ctx, likes.NewStore(c), p) }, func(err error) {
					if err != nil {
						roundErrors <- err
					}
				})
			}()
			t.Cleanup(func() { cancel(); <-done })
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			started := time.Now()
			produced := 0
			for step := 0; step < 60; step++ {
				<-tick.C
				now := time.Now()
				pipe := c.Pipeline()
				mu.Lock()
				for j := 0; j < 30; j++ {
					id := uint(3000000 + produced)
					produced++
					created[id] = now
					pipe.MSet(likes.ReadyKey(id), "1", likes.CountKey(id), "1", likes.VersionKey(id), "1")
					pipe.SAdd(likes.DirtyKey, id)
				}
				mu.Unlock()
				if _, err := pipe.Exec(); err != nil {
					t.Fatal(err)
				}
				if (step+1)%20 == 0 {
					t.Logf("budget=%d elapsed=%s produced=%d dirty=%d processing=%d", budget, time.Since(started), produced, c.SCard(likes.DirtyKey).Val(), c.ZCard(likes.ProcessingKey).Val())
				}
			}
			producerElapsed := time.Since(started)
			time.Sleep(1200 * time.Millisecond)
			cancel()
			<-done

			select {
			case err := <-roundErrors:
				t.Fatal(err)
			default:
			}
			mu.Lock()
			samples := make([]time.Duration, 0, len(received))
			oldestPending := time.Duration(0)
			for id, at := range created {
				if sent, ok := received[id]; ok {
					samples = append(samples, sent.Sub(at))
				} else if age := time.Since(at); age > oldestPending {
					oldestPending = age
				}
			}
			sent := len(received)
			mu.Unlock()
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			var p50, p95 time.Duration
			if len(samples) > 0 {
				p50 = samples[len(samples)/2]
				p95 = samples[(len(samples)-1)*95/100]
			}
			dirty := c.SCard(likes.DirtyKey).Val()
			t.Logf("SUSTAINED budget=%d produced=%d producer_elapsed=%s received=%d dirty_after_1.2s_drain=%d p50_delivered=%s p95_delivered=%s oldest_pending=%s max_inflight_publish=%d", budget, produced, producerElapsed, sent, dirty, p50, p95, oldestPending, maxActive.Load())
			if maxActive.Load() != 1 {
				t.Fatal("publisher concurrency exceeded one")
			}
			if budget == 1000 && (sent != produced || dirty != 0) {
				t.Fatal("raised budget did not drain workload")
			}
			if budget == 100 && dirty == 0 {
				t.Fatal("control failed to exercise old rate cap")
			}
		})
	}
}
