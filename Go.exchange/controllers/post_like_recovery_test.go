package controllers

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/likes"
	"Go.exchange/models"
)

// Exercise the actual coordinator and baseline validation with a controlled
// Store boundary. These tests do not execute Redis Lua or PostgreSQL queries.
type recoveryTestStore struct {
	mu         sync.Mutex
	ready      map[uint]likes.State
	registered map[uint]bool
	markers    map[uint]int64
	fail       map[uint]error
	writes     map[uint]likes.FullState
	fences     map[uint]likes.RecoveryFence
	reads      int
	registries int
	versions   int
}

func newRecoveryTestStore() *recoveryTestStore {
	return &recoveryTestStore{
		ready: make(map[uint]likes.State), registered: make(map[uint]bool), markers: make(map[uint]int64),
		fail: make(map[uint]error), writes: make(map[uint]likes.FullState), fences: make(map[uint]likes.RecoveryFence),
	}
}

func (s *recoveryTestStore) GetMany(_ context.Context, _ uint, ids []uint) (map[uint]likes.State, []uint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	states := make(map[uint]likes.State)
	var missing []uint
	for _, id := range ids {
		if state, ok := s.ready[id]; ok {
			states[id] = state
		} else {
			missing = append(missing, id)
		}
	}
	return states, missing, nil
}

func (s *recoveryTestStore) RegistryContainsMany(_ context.Context, ids []uint) (map[uint]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registries++
	result := make(map[uint]bool)
	for _, id := range ids {
		result[id] = s.registered[id]
	}
	return result, nil
}

func (s *recoveryTestStore) GetRecoverableVersions(_ context.Context, ids []uint) (map[uint]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions++
	result := make(map[uint]int64)
	for _, id := range ids {
		if v, ok := s.markers[id]; ok {
			result[id] = v
		}
	}
	return result, nil
}

func (s *recoveryTestStore) Recover(ctx context.Context, id uint, state likes.FullState, fence likes.RecoveryFence) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail[id]; err != nil {
		return false, err
	}
	s.writes[id], s.fences[id] = state, fence
	s.ready[id] = likes.State{Count: state.Count, Version: state.Version}
	return true, nil
}

// Done is observed only after this caller has joined/claimed its flights, so
// tests can hold the load until every concurrent caller is waiting, without
// scheduler sleeps or timing-dependent assumptions about overlap.
type recoveryWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *recoveryWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func waitForRecoverySignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery test did not reach expected boundary")
	}
}

func TestPostLikeRecoveryCoalescesConcurrentBaselineLoads(t *testing.T) {
	var group postLikeRecoveryFlights
	store := newRecoveryTestStore()
	const callers = 32
	store.registered[9], store.markers[9] = true, 10000
	rows := make([]models.PostReaction, 10000)
	for i := range rows {
		rows[i] = models.PostReaction{UserID: uint(i + 1), Version: int64(i + 1), Liked: i%2 == 0}
	}
	var loads atomic.Int32
	release := make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	defer releaseLoad()
	load := func(ctx context.Context, ids []uint) (map[uint]postLikeBaseline, error) {
		loads.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[uint]postLikeBaseline{9: buildPostLikeBaseline(5000, 10000, rows)}, nil
	}
	results := make(chan error, callers)
	waiters := make([]*recoveryWaitContext, callers)
	for i := range waiters {
		waiters[i] = &recoveryWaitContext{Context: t.Context(), waiting: make(chan struct{})}
		go func(ctx context.Context) {
			perID, err := group.recover(ctx, store, []uint{9, 9}, load)
			if err == nil {
				err = perID[9]
			}
			results <- err
		}(waiters[i])
	}
	for _, ctx := range waiters {
		waitForRecoverySignal(t, ctx.waiting)
	}
	releaseLoad()
	for range callers {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("callers did not complete")
		}
	}
	if loads.Load() != 1 || store.reads != 1 || store.registries != 1 || store.versions != 1 {
		t.Fatalf("loads=%d ready reads=%d registry=%d markers=%d", loads.Load(), store.reads, store.registries, store.versions)
	}
	state := store.writes[9]
	if len(state.UserIDs) != 5000 || state.Count != 5000 || state.Version != 10000 || *store.fences[9].ExpectedVersion != 10000 {
		t.Fatalf("recovered state/fence lost members or version: %+v", store.fences[9])
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if len(group.inflight) != 0 {
		t.Fatal("completed flights retained")
	}
}

func TestPostLikeRecoveryOverlappingBatchesAndSingleRead(t *testing.T) {
	var group postLikeRecoveryFlights
	store := newRecoveryTestStore()
	release := make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	defer releaseLoad()
	loaded := make(chan []uint, 3)
	load := func(ctx context.Context, ids []uint) (map[uint]postLikeBaseline, error) {
		loaded <- append([]uint(nil), ids...)
		if ids[0] == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		result := make(map[uint]postLikeBaseline)
		for _, id := range ids {
			result[id] = postLikeBaseline{}
		}
		return result, nil
	}
	results := make(chan error, 3)
	start := func(ids []uint) *recoveryWaitContext {
		ctx := &recoveryWaitContext{Context: t.Context(), waiting: make(chan struct{})}
		go func() {
			perID, err := group.recover(ctx, store, ids, load)
			for _, e := range perID {
				if e != nil {
					err = e
				}
			}
			results <- err
		}()
		waitForRecoverySignal(t, ctx.waiting)
		return ctx
	}
	start([]uint{1, 2})
	if ids := <-loaded; !reflect.DeepEqual(ids, []uint{1, 2}) {
		t.Fatalf("initial SQL batch=%v", ids)
	}
	start([]uint{2, 3})
	if ids := <-loaded; !reflect.DeepEqual(ids, []uint{3}) {
		t.Fatalf("overlap reloaded shared post: %v", ids)
	}
	start([]uint{2})
	releaseLoad()
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(loaded) != 0 || len(store.writes) != 3 {
		t.Fatalf("extra loads=%d writes=%d", len(loaded), len(store.writes))
	}
}

func TestPostLikeRecoveryCallerCancellationDoesNotCancelSharedLoad(t *testing.T) {
	for _, cancelLeader := range []bool{true, false} {
		t.Run(map[bool]string{true: "leader", false: "follower"}[cancelLeader], func(t *testing.T) {
			var group postLikeRecoveryFlights
			store := newRecoveryTestStore()
			release := make(chan struct{})
			releaseLoad := sync.OnceFunc(func() { close(release) })
			defer releaseLoad()
			load := func(ctx context.Context, _ []uint) (map[uint]postLikeBaseline, error) {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return map[uint]postLikeBaseline{1: {}}, nil
			}
			leaderCtx, cancel1 := context.WithCancel(t.Context())
			defer cancel1()
			followerCtx, cancel2 := context.WithCancel(t.Context())
			defer cancel2()
			start := func(ctx context.Context) <-chan error {
				observed := &recoveryWaitContext{Context: ctx, waiting: make(chan struct{})}
				result := make(chan error, 1)
				go func() {
					perID, err := group.recover(observed, store, []uint{1}, load)
					if err == nil {
						err = perID[1]
					}
					result <- err
				}()
				waitForRecoverySignal(t, observed.waiting)
				return result
			}
			leader, follower := start(leaderCtx), start(followerCtx)
			canceled, remaining := leader, follower
			if cancelLeader {
				cancel1()
			} else {
				cancel2()
				canceled, remaining = follower, leader
			}
			if err := <-canceled; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel error=%v", err)
			}
			releaseLoad()
			if err := <-remaining; err != nil {
				t.Fatalf("shared recovery canceled: %v", err)
			}
			if len(store.writes) != 1 {
				t.Fatal("shared load did not recover")
			}
		})
	}
}

func TestPostLikeRecoveryPerPostFencesAndReadyRecheck(t *testing.T) {
	var group postLikeRecoveryFlights
	store := newRecoveryTestStore()
	store.ready[8] = likes.State{}
	store.registered[2], store.markers[2] = true, 7
	store.registered[5] = true
	store.fail[6] = likes.ErrLikeRecoveryFenceLost
	store.registered[7], store.markers[7] = true, 8
	results, err := group.recover(t.Context(), store, []uint{1, 2, 3, 4, 5, 6, 7, 8}, func(_ context.Context, ids []uint) (map[uint]postLikeBaseline, error) {
		if !reflect.DeepEqual(ids, []uint{1, 2, 3, 4, 5, 6, 7}) {
			return nil, errors.New("ready post was included in SQL batch")
		}
		return map[uint]postLikeBaseline{
			1: {}, 2: {Count: 1, Version: 7, UserIDs: []uint{10}, ReactionRowCount: 2, MaxReactionVersion: 7},
			4: {Count: 1}, 5: {}, 6: {}, 7: {},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uint]error{1: nil, 2: nil, 3: likes.ErrPostLikeUnavailable, 4: likes.ErrLikeProjectionNotReady, 5: likes.ErrLikeRecoveryUnsafe, 6: likes.ErrLikeRecoveryFenceLost, 7: likes.ErrLikeRecoveryUnsafe, 8: nil} {
		if !errors.Is(results[id], want) {
			t.Fatalf("post=%d err=%v want=%v", id, results[id], want)
		}
	}
	if len(store.writes) != 2 || !store.fences[1].AllowZeroBootstrap || *store.fences[2].ExpectedVersion != 7 {
		t.Fatal("invalid recovery writes/fences")
	}
	results, err = group.recover(t.Context(), store, []uint{1, 2, 8}, func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
		return nil, errors.New("ready recheck loaded SQL")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostLikeRecoveryFailureReleasesFlightsAndCanRetry(t *testing.T) {
	for _, failStage := range []string{"baseline", "write", "deadline"} {
		t.Run(failStage, func(t *testing.T) {
			if failStage == "deadline" {
				t.Setenv("API_REQUEST_TIMEOUT", "30ms")
			}
			var group postLikeRecoveryFlights
			store := newRecoveryTestStore()
			failure := errors.New("temporary database/Redis failure")
			if failStage == "deadline" {
				failure = context.DeadlineExceeded
			}
			if failStage == "write" {
				store.fail[1] = failure
			}
			results, err := group.recover(t.Context(), store, []uint{1}, func(ctx context.Context, _ []uint) (map[uint]postLikeBaseline, error) {
				if failStage == "deadline" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if failStage == "baseline" {
					return nil, failure
				}
				return map[uint]postLikeBaseline{1: {}}, nil
			})
			if err != nil || !errors.Is(results[1], failure) {
				t.Fatalf("results=%v err=%v", results, err)
			}
			delete(store.fail, 1)
			results, err = group.recover(t.Context(), store, []uint{1}, func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
				return map[uint]postLikeBaseline{1: {}}, nil
			})
			if err != nil || results[1] != nil || len(store.writes) != 1 {
				t.Fatalf("retry results=%v err=%v", results, err)
			}
		})
	}
}

func TestPostLikeRecoveryBatchFailureDoesNotPoisonCompletedPosts(t *testing.T) {
	var group postLikeRecoveryFlights
	store := newRecoveryTestStore()
	store.ready[4] = likes.State{}
	failure := errors.New("Redis write failed")
	store.fail[2] = failure
	results, err := group.recover(t.Context(), store, []uint{1, 2, 3, 4}, func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
		return map[uint]postLikeBaseline{1: {}, 2: {}, 3: {}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[1] != nil || results[4] != nil || !errors.Is(results[2], failure) || !errors.Is(results[3], failure) {
		t.Fatalf("per-post results=%v", results)
	}
	if len(store.writes) != 1 {
		t.Fatalf("writes after failure: %v", store.writes)
	}
}
