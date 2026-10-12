package tasks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserLikeRelationCleanupTickStopsAfterSixteenPasses(t *testing.T) {
	state := userLikeRelationCleanupState{cycleStartedAt: time.Now()}
	calls := 0
	var firstState *userLikeRelationCleanupState

	err := runUserLikeRelationCleanupTick(context.Background(), &state, func(_ context.Context, got *userLikeRelationCleanupState) error {
		calls++
		if firstState == nil {
			firstState = got
		} else if got != firstState {
			t.Fatal("tick did not reuse the same cleanup state")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run cleanup tick: %v", err)
	}
	if calls != userLikeCleanupPassesPerTick {
		t.Fatalf("pass calls = %d, want %d", calls, userLikeCleanupPassesPerTick)
	}
}

func TestUserLikeRelationCleanupTickStopsAtTimeBudget(t *testing.T) {
	state := userLikeRelationCleanupState{cycleStartedAt: time.Now()}
	calls := 0
	err := runUserLikeRelationCleanupTick(context.Background(), &state, func(context.Context, *userLikeRelationCleanupState) error {
		calls++
		time.Sleep(userLikeCleanupTickBudget + 10*time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("run cleanup tick: %v", err)
	}
	if calls != 1 {
		t.Fatalf("pass calls = %d after exceeding budget, want 1", calls)
	}
}

func TestUserLikeRelationCleanupTickStopsOnErrorAndRetainsProgress(t *testing.T) {
	wantErr := errors.New("simulated pass failure")
	state := userLikeRelationCleanupState{
		cycleStartedAt: time.Now(),
		lastUserID:     21,
		userID:         13,
		scanCursor:     7,
		pending:        []uint{101, 102},
	}
	calls := 0
	err := runUserLikeRelationCleanupTick(context.Background(), &state, func(_ context.Context, got *userLikeRelationCleanupState) error {
		calls++
		if got != &state {
			t.Fatal("pass received a different cleanup state")
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("pass calls = %d after error, want 1", calls)
	}
	if state.lastUserID != 21 || state.userID != 13 || state.scanCursor != 7 || len(state.pending) != 2 {
		t.Fatalf("cleanup progress changed after failed pass: %+v", state)
	}
}

func TestUserLikeRelationCleanupTickStopsAfterCycleCompletes(t *testing.T) {
	state := userLikeRelationCleanupState{
		cycleStartedAt: time.Now(),
		lastUserID:     21,
		userID:         13,
		scanCursor:     7,
		pending:        []uint{101},
	}
	calls := 0
	err := runUserLikeRelationCleanupTick(context.Background(), &state, func(_ context.Context, got *userLikeRelationCleanupState) error {
		calls++
		*got = userLikeRelationCleanupState{}
		return nil
	})
	if err != nil {
		t.Fatalf("run cleanup tick: %v", err)
	}
	if calls != 1 {
		t.Fatalf("pass calls = %d after cycle completion, want 1", calls)
	}
}

func TestUserLikeRelationCleanupTickStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := userLikeRelationCleanupState{cycleStartedAt: time.Now()}
	calls := 0
	err := runUserLikeRelationCleanupTick(ctx, &state, func(context.Context, *userLikeRelationCleanupState) error {
		calls++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("pass calls = %d after cancellation, want 1", calls)
	}
}
