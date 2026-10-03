package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (s *memoryRefreshStore) Revoke(ctx context.Context, sessionID, expectedHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[sessionID]
	if !exists {
		return nil
	}
	_, used := s.used[usedRefreshKey(sessionID, expectedHash)]
	if session.SecretHash != expectedHash && !used {
		return ErrRefreshInvalid
	}
	delete(s.sessions, sessionID)
	return nil
}

func TestLogoutRevokesOnlyCapturedFamilyAndKeepsAccessExpiryContract(t *testing.T) {
	manager, _, _ := testManager(t)
	now := time.Now().UTC()
	manager.now = func() time.Time { return now }
	ctx := context.Background()
	original, err := manager.IssuePair(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.IssuePair(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	successor, err := manager.RotateRefresh(ctx, original.RefreshToken, requestID)
	if err != nil {
		t.Fatal(err)
	}
	// Logout captured the old secret before an in-flight refresh completed.
	for i := 0; i < 2; i++ {
		if err := manager.RevokeRefresh(ctx, original.RefreshToken); err != nil {
			t.Fatal(err)
		}
	}
	for _, attempt := range []struct{ token, requestID string }{
		{original.RefreshToken, requestID}, {successor.RefreshToken, uuid.NewString()},
	} {
		if _, err := manager.RotateRefresh(ctx, attempt.token, attempt.requestID); err == nil {
			t.Fatal("revoked family refreshed or recovered")
		}
	}
	if _, err := manager.RotateRefresh(ctx, other.RefreshToken, uuid.NewString()); err != nil {
		t.Fatalf("other session revoked: %v", err)
	}
	if _, err := manager.VerifyAccess(original.AccessToken); err != nil {
		t.Fatalf("stateless access lifetime changed: %v", err)
	}
	claims, _ := manager.VerifyAccess(original.AccessToken)
	expired, err := manager.signAccessToken(42, claims.SessionID, time.Now().Add(-manager.config.AccessTTL-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(expired); err == nil {
		t.Fatal("expired access still accepted")
	}
}

func TestLogoutCannotRevokeBySessionIDOrForeignSecret(t *testing.T) {
	manager, store, _ := testManager(t)
	ctx := context.Background()
	pair, err := manager.IssuePair(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := parseRefreshToken(pair.RefreshToken)
	foreign, _ := newRefreshSecret()
	for _, raw := range []string{"", parsed.sessionID, "rt1." + parsed.sessionID + ".bad", formatRefreshToken(parsed.sessionID, foreign)} {
		if err := manager.RevokeRefresh(ctx, raw); !errors.Is(err, ErrRefreshInvalid) {
			t.Fatalf("invalid credential accepted: %v", err)
		}
	}
	store.mu.Lock()
	_, exists := store.sessions[parsed.sessionID]
	store.mu.Unlock()
	if !exists {
		t.Fatal("invalid credential deleted session")
	}
	if _, err := manager.RotateRefresh(ctx, pair.RefreshToken, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutRacingRotationCannotResurrectFamily(t *testing.T) {
	manager, store, _ := testManager(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		pair, err := manager.IssuePair(ctx, 42)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := parseRefreshToken(pair.RefreshToken)
		requestID := uuid.NewString()
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); <-start; _, _ = manager.RotateRefresh(ctx, pair.RefreshToken, requestID) }()
		go func() {
			defer wait.Done()
			<-start
			if err := manager.RevokeRefresh(ctx, pair.RefreshToken); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wait.Wait()
		store.mu.Lock()
		_, exists := store.sessions[parsed.sessionID]
		store.mu.Unlock()
		if exists {
			t.Fatal("rotation resurrected revoked session")
		}
		if _, err := manager.RotateRefresh(ctx, pair.RefreshToken, requestID); err == nil {
			t.Fatal("recovery resurrected revoked session")
		}
	}
}

func TestLogoutCancellationDoesNotReportSuccessfulRevocation(t *testing.T) {
	manager, _, _ := testManager(t)
	pair, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.RevokeRefresh(ctx, pair.RefreshToken); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if _, err := manager.RotateRefresh(context.Background(), pair.RefreshToken, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}
