package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type memoryRefreshStore struct {
	mu       sync.Mutex
	sessions map[string]RefreshSession
}

func newMemoryRefreshStore() *memoryRefreshStore {
	return &memoryRefreshStore{sessions: make(map[string]RefreshSession)}
}

func (s *memoryRefreshStore) Create(_ context.Context, session RefreshSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.SessionID]; exists {
		return errors.New("session exists")
	}
	s.sessions[session.SessionID] = session
	return nil
}

func (s *memoryRefreshStore) Rotate(
	_ context.Context,
	sessionID string,
	expectedSecretHash string,
	newSecretHash string,
	now time.Time,
	idleTTL time.Duration,
) (uint, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[sessionID]
	if !exists {
		return 0, 0, ErrRefreshInvalid
	}
	if session.SecretHash != expectedSecretHash {
		return 0, 0, ErrRefreshReused
	}
	if !now.Before(session.AbsoluteExpiresAt) {
		delete(s.sessions, sessionID)
		return 0, 0, ErrRefreshExpired
	}
	ttl := minDuration(idleTTL, session.AbsoluteExpiresAt.Sub(now))
	session.SecretHash = newSecretHash
	session.LastRotatedAt = now
	session.TTL = ttl
	s.sessions[sessionID] = session
	return session.UserID, ttl, nil
}

func testManager(t *testing.T) (*Manager, *memoryRefreshStore, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryRefreshStore()
	manager, err := NewManager(Config{
		ActiveKID:          "test-v1",
		PrivateKey:         privateKey,
		VerifyKeys:         map[string]ed25519.PublicKey{"test-v1": publicKey},
		Issuer:             "go.exchange.test",
		Audience:           "go.exchange.test.api",
		AccessTTL:          15 * time.Minute,
		RefreshIdleTTL:     7 * 24 * time.Hour,
		RefreshAbsoluteTTL: 30 * 24 * time.Hour,
		ClockSkew:          0,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	return manager, store, privateKey
}

func TestIssuePairUsesEdDSAAndOpaqueRefreshToken(t *testing.T) {
	manager, store, _ := testManager(t)
	pair, err := manager.IssuePair(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if pair.TokenType != "Bearer" || pair.AccessExpiresIn != 15*time.Minute {
		t.Fatalf("unexpected token metadata: %+v", pair)
	}
	if len(pair.RefreshToken) < 70 || pair.RefreshToken[:4] != "rt1." {
		t.Fatalf("unexpected refresh token format: %q", pair.RefreshToken)
	}
	if len(pair.AccessToken) > 7 && pair.AccessToken[:7] == "Bearer " {
		t.Fatal("access token must not include the Bearer transport prefix")
	}
	claims, err := manager.VerifyAccess(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "42" || claims.TokenType != accessTokenType || claims.SessionID == "" || claims.ID == "" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	parsed, err := parseRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	session := store.sessions[parsed.sessionID]
	store.mu.Unlock()
	if session.SecretHash == "" || session.SecretHash == string(parsed.secret) || session.SecretHash == pair.RefreshToken {
		t.Fatal("refresh store must contain only a one-way secret hash")
	}
}

func TestIssuePairAlwaysUsesUniqueJWTID(t *testing.T) {
	manager, _, _ := testManager(t)
	first, err := manager.IssuePair(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.IssuePair(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	firstClaims, err := manager.VerifyAccess(first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	secondClaims, err := manager.VerifyAccess(second.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if firstClaims.ID == secondClaims.ID || first.RefreshToken == second.RefreshToken {
		t.Fatal("separately issued token pairs must be unique")
	}
}

func TestVerifyAccessRejectsHMACAndUnknownKID(t *testing.T) {
	manager, _, privateKey := testManager(t)
	now := time.Now().UTC()
	claims := AccessClaims{
		SessionID: uuid.NewString(),
		TokenType: accessTokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    manager.config.Issuer,
			Subject:   "42",
			Audience:  jwt.ClaimStrings{manager.config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}

	hmac := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hmac.Header["kid"] = manager.config.ActiveKID
	hmacRaw, err := hmac.SignedString([]byte("not-used"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(hmacRaw); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("HMAC token error=%v", err)
	}

	unknown := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	unknown.Header["kid"] = "unknown"
	unknownRaw, err := unknown.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(unknownRaw); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("unknown kid error=%v", err)
	}
}

func TestVerifyAccessRejectsMissingRequiredClaims(t *testing.T) {
	manager, _, privateKey := testManager(t)
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, AccessClaims{
		TokenType: accessTokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    manager.config.Issuer,
			Subject:   strconv.Itoa(1),
			Audience:  jwt.ClaimStrings{manager.config.Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	})
	token.Header["kid"] = manager.config.ActiveKID
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(raw); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("missing claims error=%v", err)
	}
}

func TestRefreshRotationAllowsExactlyOneConcurrentWinner(t *testing.T) {
	manager, _, _ := testManager(t)
	pair, err := manager.IssuePair(context.Background(), 99)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 100
	var successes atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, rotateErr := manager.RotateRefresh(context.Background(), pair.RefreshToken)
			if rotateErr == nil {
				successes.Add(1)
				return
			}
			if !errors.Is(rotateErr, ErrRefreshReused) {
				t.Errorf("unexpected rotation error: %v", rotateErr)
			}
			failures.Add(1)
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || failures.Load() != workers-1 {
		t.Fatalf("successes=%d failures=%d", successes.Load(), failures.Load())
	}
}
