package likes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type aggregateReadCommandHook struct {
	mu            sync.Mutex
	commands      []string
	pipelineCalls int
}

func (h *aggregateReadCommandHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	h.mu.Lock()
	h.commands = append(h.commands, cmd.Name())
	h.mu.Unlock()
	return ctx, nil
}

func (*aggregateReadCommandHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (h *aggregateReadCommandHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	h.mu.Lock()
	h.pipelineCalls++
	for _, cmd := range cmds {
		h.commands = append(h.commands, cmd.Name())
	}
	h.mu.Unlock()
	return ctx, nil
}

func (*aggregateReadCommandHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (h *aggregateReadCommandHook) reset() {
	h.mu.Lock()
	h.commands = nil
	h.pipelineCalls = 0
	h.mu.Unlock()
}

func (h *aggregateReadCommandHook) snapshot() ([]string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.commands...), h.pipelineCalls
}

func openAggregateReadRedisIntegration(t *testing.T) (*redis.Client, *Store) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis aggregate read integration test")
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
	return client, NewStore(client)
}

func TestLikeAggregateSnapshotUsesAtomicMGetIntegration(t *testing.T) {
	client, store := openAggregateReadRedisIntegration(t)
	postID := aggregateReadTestPostID()
	cleanupAggregateReadPost(t, client, postID)
	t.Cleanup(func() { cleanupAggregateReadPost(t, client, postID) })
	if err := client.MSet(ReadyKey(postID), "1", CountKey(postID), "100", VersionKey(postID), "50").Err(); err != nil {
		t.Fatal(err)
	}

	hook := &aggregateReadCommandHook{}
	client.AddHook(hook)
	snapshot, err := store.LoadSnapshot(t.Context(), postID)
	if err != nil || snapshot != (Snapshot{PostID: postID, Count: 100, Version: 50}) {
		t.Fatalf("snapshot=(%+v, %v), want Post=%d Count=100 Version=50", snapshot, err, postID)
	}
	commands, pipelineCalls := hook.snapshot()
	if len(commands) != 1 || commands[0] != "mget" || pipelineCalls != 0 {
		t.Fatalf("snapshot Redis commands=%v pipeline calls=%d, want one MGET", commands, pipelineCalls)
	}

	full, err := store.LoadFullState(t.Context(), postID)
	if err != nil || full != (FullState{Count: 100, Version: 50}) {
		t.Fatalf("full state=(%+v, %v), want Count=100 Version=50", full, err)
	}
	state, err := store.Get(t.Context(), 0, postID)
	if err != nil || state != (State{Count: 100, Version: 50}) {
		t.Fatalf("Get state=(%+v, %v), want Count=100 Version=50", state, err)
	}
}

func TestLikeAggregateReadsRemainConsistentDuringAtomicMutationsIntegration(t *testing.T) {
	client, store := openAggregateReadRedisIntegration(t)
	postID := aggregateReadTestPostID()
	cleanupAggregateReadPost(t, client, postID)
	t.Cleanup(func() {
		if err := client.Del(ReadyKey(postID), CountKey(postID), VersionKey(postID)).Err(); err != nil {
			t.Errorf("clear aggregate keys for post %d: %v", postID, err)
		}
	})
	if err := client.MSet(ReadyKey(postID), "1", CountKey(postID), "13", VersionKey(postID), "0").Err(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	update := redis.NewScript(`
local version = tonumber(redis.call('GET', KEYS[2])) + 1
local count = version * 7 + 13
redis.call('SET', KEYS[1], count)
redis.call('SET', KEYS[2], version)
return version
`)
	const readerCount = 6
	const readerRounds = 300
	const writerRounds = 300
	start := make(chan struct{})
	errs := make(chan error, readerCount*readerRounds+writerRounds)
	var wait sync.WaitGroup
	wait.Add(readerCount + 1)
	for reader := 0; reader < readerCount; reader++ {
		go func() {
			defer wait.Done()
			<-start
			for i := 0; i < readerRounds; i++ {
				snapshot, err := store.LoadSnapshot(ctx, postID)
				if err != nil {
					errs <- err
					return
				}
				if snapshot.Count != snapshot.Version*7+13 {
					errs <- fmt.Errorf("mixed aggregate snapshot: count=%d version=%d", snapshot.Count, snapshot.Version)
					return
				}
			}
		}()
	}
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < writerRounds; i++ {
			if _, err := update.Run(client.WithContext(ctx), []string{CountKey(postID), VersionKey(postID)}).Result(); err != nil {
				errs <- fmt.Errorf("atomic Count/Version update: %w", err)
				return
			}
		}
	}()
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestLikeAggregateReadErrorsRemainClassifiedIntegration(t *testing.T) {
	client, store := openAggregateReadRedisIntegration(t)
	base := aggregateReadTestPostID()
	postIDs := make([]uint, 0, 11)
	for i := uint(0); i < 11; i++ {
		postIDs = append(postIDs, base+i)
	}
	for _, postID := range postIDs {
		cleanupAggregateReadPost(t, client, postID)
	}
	t.Cleanup(func() {
		for _, postID := range postIDs {
			cleanupAggregateReadPost(t, client, postID)
		}
	})

	tests := []struct {
		name  string
		setup func(uint) error
		want  error
	}{
		{
			name: "deleted wins over corrupt aggregate fields",
			setup: func(postID uint) error {
				if err := client.Set(ReadyKey(postID), "deleted", 0).Err(); err != nil {
					return err
				}
				if err := client.LPush(CountKey(postID), "corrupt").Err(); err != nil {
					return err
				}
				return client.Set(VersionKey(postID), "1", 0).Err()
			},
			want: ErrPostLikeUnavailable,
		},
		{
			name: "missing Ready",
			setup: func(postID uint) error {
				return client.MSet(CountKey(postID), "1", VersionKey(postID), "1").Err()
			},
			want: ErrPostLikeNotReady,
		},
		{
			name: "missing Ready with Count WRONGTYPE",
			setup: func(postID uint) error {
				if err := client.LPush(CountKey(postID), "1").Err(); err != nil {
					return err
				}
				return client.Set(VersionKey(postID), "1", 0).Err()
			},
			want: ErrLikeRedisType,
		},
		{
			name: "invalid Ready with Version WRONGTYPE",
			setup: func(postID uint) error {
				if err := client.HSet(VersionKey(postID), "value", "1").Err(); err != nil {
					return err
				}
				return client.MSet(ReadyKey(postID), "invalid", CountKey(postID), "1").Err()
			},
			want: ErrLikeRedisType,
		},
		{
			name: "missing Count",
			setup: func(postID uint) error {
				return client.MSet(ReadyKey(postID), "1", VersionKey(postID), "1").Err()
			},
			want: ErrPostLikeNotReady,
		},
		{
			name: "missing Version",
			setup: func(postID uint) error {
				return client.MSet(ReadyKey(postID), "1", CountKey(postID), "1").Err()
			},
			want: ErrPostLikeNotReady,
		},
		{
			name: "invalid Count integer",
			setup: func(postID uint) error {
				return client.MSet(ReadyKey(postID), "1", CountKey(postID), "bad", VersionKey(postID), "1").Err()
			},
			want: ErrPostLikeNotReady,
		},
		{
			name: "negative Version",
			setup: func(postID uint) error {
				return client.MSet(ReadyKey(postID), "1", CountKey(postID), "1", VersionKey(postID), "-1").Err()
			},
			want: ErrPostLikeNotReady,
		},
		{
			name: "Ready WRONGTYPE",
			setup: func(postID uint) error {
				if err := client.RPush(ReadyKey(postID), "1").Err(); err != nil {
					return err
				}
				return client.MSet(CountKey(postID), "1", VersionKey(postID), "1").Err()
			},
			want: ErrLikeRedisType,
		},
		{
			name: "Count WRONGTYPE",
			setup: func(postID uint) error {
				if err := client.LPush(CountKey(postID), "1").Err(); err != nil {
					return err
				}
				return client.MSet(ReadyKey(postID), "1", VersionKey(postID), "1").Err()
			},
			want: ErrLikeRedisType,
		},
		{
			name: "Version WRONGTYPE",
			setup: func(postID uint) error {
				if err := client.HSet(VersionKey(postID), "value", "1").Err(); err != nil {
					return err
				}
				return client.MSet(ReadyKey(postID), "1", CountKey(postID), "1").Err()
			},
			want: ErrLikeRedisType,
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			postID := postIDs[i]
			if err := test.setup(postID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadSnapshot(t.Context(), postID); !errors.Is(err, test.want) {
				t.Fatalf("LoadSnapshot error=%v, want %v", err, test.want)
			}
		})
	}

	closedClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := closedClient.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(closedClient).LoadSnapshot(t.Context(), base+uint(len(tests))); err == nil || errors.Is(err, ErrPostLikeNotReady) {
		t.Fatalf("closed Redis client error=%v, want a Redis command failure", err)
	}
}

func TestLikeAggregateGetManyUsesOneMGetAndPreservesUnavailablePostsIntegration(t *testing.T) {
	client, store := openAggregateReadRedisIntegration(t)
	base := aggregateReadTestPostID()
	postIDs := []uint{base, base + 1, base + 2, base + 3, base + 4}
	for _, postID := range postIDs {
		cleanupAggregateReadPost(t, client, postID)
	}
	t.Cleanup(func() {
		for _, postID := range postIDs {
			cleanupAggregateReadPost(t, client, postID)
		}
	})
	if err := client.MSet(ReadyKey(postIDs[0]), "1", CountKey(postIDs[0]), "15", VersionKey(postIDs[0]), "5",
		ReadyKey(postIDs[1]), "1", CountKey(postIDs[1]), "1", VersionKey(postIDs[1]), "1").Err(); err != nil {
		t.Fatal(err)
	}

	hook := &aggregateReadCommandHook{}
	client.AddHook(hook)
	states, unavailable, err := store.GetMany(t.Context(), 0, postIDs[:2])
	if err != nil || len(unavailable) != 0 || states[postIDs[0]] != (State{Count: 15, Version: 5}) || states[postIDs[1]] != (State{Count: 1, Version: 1}) {
		t.Fatalf("normal batch states=%v unavailable=%v err=%v", states, unavailable, err)
	}
	commands, pipelineCalls := hook.snapshot()
	if len(commands) != 1 || commands[0] != "mget" || pipelineCalls != 0 {
		t.Fatalf("normal batch Redis commands=%v pipeline calls=%d, want one MGET", commands, pipelineCalls)
	}

	if err := client.Set(ReadyKey(postIDs[2]), "deleted", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(CountKey(postIDs[2]), "corrupt").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.MSet(ReadyKey(postIDs[3]), "1", VersionKey(postIDs[3]), "7").Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	states, unavailable, err = store.GetMany(t.Context(), 0, []uint{postIDs[0], 0, postIDs[2], postIDs[1], postIDs[3]})
	if err != nil {
		t.Fatal(err)
	}
	if states[postIDs[0]] != (State{Count: 15, Version: 5}) || states[postIDs[1]] != (State{Count: 1, Version: 1}) || len(states) != 2 {
		t.Fatalf("partial batch states=%v", states)
	}
	if fmt.Sprint(unavailable) != fmt.Sprint([]uint{0, postIDs[2], postIDs[3]}) {
		t.Fatalf("partial batch unavailable=%v", unavailable)
	}
	commands, pipelineCalls = hook.snapshot()
	if len(commands) != 2 || commands[0] != "mget" || commands[1] != "type" || pipelineCalls != 1 {
		t.Fatalf("partial batch Redis commands=%v pipeline calls=%d, want one MGET and batched TYPE classification", commands, pipelineCalls)
	}
}

func aggregateReadTestPostID() uint {
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	if postID == 0 {
		return 1
	}
	return postID
}

func cleanupAggregateReadPost(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	if err := client.Del(ReadyKey(postID), CountKey(postID), VersionKey(postID)).Err(); err != nil {
		t.Fatalf("clear aggregate keys for post %d: %v", postID, err)
	}
}
