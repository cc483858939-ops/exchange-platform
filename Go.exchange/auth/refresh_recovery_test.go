package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRefreshRecoveryReturnsSameSuccessorWithoutExtendingSession(t *testing.T) {
	manager, store, _ := testManager(t)
	now := time.Now().UTC()
	manager.now = func() time.Time { return now }
	original, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	first, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	recovered, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RefreshToken != first.RefreshToken || recovered.UserID != first.UserID || recovered.RefreshExpiresIn != first.RefreshExpiresIn-time.Minute {
		t.Fatal("retry changed the successor or extended the refresh lifetime")
	}
	parsed, _ := parseRefreshToken(original.RefreshToken)
	store.mu.Lock()
	saved := store.used[usedRefreshKey(parsed.sessionID, hashRefreshSecret(parsed.secret))]
	store.mu.Unlock()
	next, _ := parseRefreshToken(first.RefreshToken)
	if saved.sealedSecret == first.RefreshToken || saved.sealedSecret == base64.RawURLEncoding.EncodeToString(next.secret) {
		t.Fatal("recovery storage contains plaintext refresh credentials")
	}
	// A restart or JWT signing-key rotation must not lose the committed result.
	replacement, _, _ := testManager(t)
	restarted, err := NewManager(replacement.config, store)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = manager.now
	afterRestart, err := restarted.RotateRefresh(context.Background(), original.RefreshToken, requestID)
	if err != nil || afterRestart.RefreshToken != first.RefreshToken {
		t.Fatalf("restart recovery failed: %v", err)
	}
}

func TestRefreshRecoveryConcurrentSameRequestReturnsOneSuccessor(t *testing.T) {
	manager, _, _ := testManager(t)
	original, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	const callers = 12
	tokens := make(chan string, callers)
	failures := make(chan error, callers)
	var wait sync.WaitGroup
	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			pair, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
			if err != nil {
				failures <- err
			} else {
				tokens <- pair.RefreshToken
			}
		}()
	}
	wait.Wait()
	close(tokens)
	close(failures)
	for err := range failures {
		t.Errorf("rotation: %v", err)
	}
	winner := ""
	count := 0
	for token := range tokens {
		count++
		if winner == "" {
			winner = token
		}
		if token != winner {
			t.Fatal("concurrent retry minted a different successor")
		}
	}
	if count != callers {
		t.Fatalf("successes=%d", count)
	}
}

func TestRefreshRecoveryRejectsReplayOutsideItsExactTransition(t *testing.T) {
	for _, scenario := range []string{"different request", "missing request", "expired recovery", "newer generation", "session removed"} {
		t.Run(scenario, func(t *testing.T) {
			manager, store, _ := testManager(t)
			now := time.Now().UTC()
			manager.now = func() time.Time { return now }
			original, err := manager.IssuePair(context.Background(), 42)
			if err != nil {
				t.Fatal(err)
			}
			requestID := uuid.NewString()
			next, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID)
			if err != nil {
				t.Fatal(err)
			}
			parsed, _ := parseRefreshToken(original.RefreshToken)
			switch scenario {
			case "different request":
				requestID = uuid.NewString()
			case "missing request":
				requestID = ""
			case "expired recovery":
				now = now.Add(refreshRecoveryTTL)
			case "newer generation":
				if _, err := manager.RotateRefresh(context.Background(), next.RefreshToken, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "session removed":
				store.mu.Lock()
				delete(store.sessions, parsed.sessionID)
				store.mu.Unlock()
			}
			if _, err := manager.RotateRefresh(context.Background(), original.RefreshToken, requestID); !errors.Is(err, ErrRefreshReused) {
				t.Fatalf("replay=%v", err)
			}
			store.mu.Lock()
			_, alive := store.sessions[parsed.sessionID]
			store.mu.Unlock()
			if alive {
				t.Fatal("replay did not revoke the refresh session")
			}
		})
	}
}

func TestRefreshRecoveryCipherRejectsChangedBindingAndTampering(t *testing.T) {
	secret, err := newRefreshSecret()
	if err != nil {
		t.Fatal(err)
	}
	previousSecret, err := newRefreshSecret()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := sealRefreshSecret(previousSecret, "session-a", "old-hash", "request-a", secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ session, hash, request, payload string }{
		{"session-b", "old-hash", "request-a", payload},
		{"session-a", "other-hash", "request-a", payload},
		{"session-a", "old-hash", "request-b", payload},
		{"session-a", "old-hash", "request-a", "invalid"},
	} {
		if _, err := openRefreshSecret(previousSecret, test.session, test.hash, test.request, test.payload); err == nil {
			t.Fatal("accepted a mismatched recovery binding")
		}
	}
	bytes, _ := base64.RawURLEncoding.DecodeString(payload)
	bytes[len(bytes)-1] ^= 1
	if _, err := openRefreshSecret(previousSecret, "session-a", "old-hash", "request-a", base64.RawURLEncoding.EncodeToString(bytes)); err == nil {
		t.Fatal("accepted a modified recovery result")
	}
	wrongSecret, err := newRefreshSecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openRefreshSecret(wrongSecret, "session-a", "old-hash", "request-a", payload); err == nil {
		t.Fatal("accepted recovery without the original credential")
	}
}

func TestRefreshRecoveryRejectsInvalidRequestIDBeforeRotation(t *testing.T) {
	manager, store, _ := testManager(t)
	original, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := parseRefreshToken(original.RefreshToken)
	for _, id := range []string{"not-uuid", uuid.NewString() + " ", "550e8400-e29b-11d4-a716-446655440000"} {
		if _, err := manager.RotateRefresh(context.Background(), original.RefreshToken, id); !errors.Is(err, ErrRefreshInvalid) {
			t.Fatalf("invalid ID=%v", err)
		}
	}
	store.mu.Lock()
	saved := store.sessions[parsed.sessionID]
	store.mu.Unlock()
	if saved.SecretHash != hashRefreshSecret(parsed.secret) {
		t.Fatal("invalid request changed the session")
	}
}
