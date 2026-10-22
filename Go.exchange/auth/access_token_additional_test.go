package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type testAuthVersionReader struct {
	version int64
	err     error
}

func (reader testAuthVersionReader) ReadAuthVersion(context.Context, uint) (int64, error) {
	return reader.version, reader.err
}

func TestVerifyAccessRejectsMissingKIDAndExpiredToken(t *testing.T) {
	manager, _, privateKey := testManager(t)
	now := time.Now().UTC()
	claims := AccessClaims{
		SessionID:   uuid.NewString(),
		TokenType:   accessTokenType,
		AuthVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    manager.config.Issuer,
			Subject:   "11",
			Audience:  jwt.ClaimStrings{manager.config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	missingKID := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	rawMissingKID, err := missingKID.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(rawMissingKID); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("missing kid error=%v", err)
	}

	claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))
	claims.NotBefore = jwt.NewNumericDate(now.Add(-2 * time.Minute))
	claims.IssuedAt = jwt.NewNumericDate(now.Add(-2 * time.Minute))
	expired := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	expired.Header["kid"] = manager.config.ActiveKID
	rawExpired, err := expired.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(rawExpired); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("expired token error=%v", err)
	}
}

func TestVersionedAccessTokenVerifierChecksDatabaseVersionAndFailsClosed(t *testing.T) {
	manager, _, _ := testManager(t)
	pair, err := manager.IssuePair(context.Background(), 84, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		version int64
		readErr error
		wantErr error
	}{
		{name: "current version", version: 3},
		{name: "stale version", version: 4, wantErr: ErrAccessTokenInvalid},
		{name: "version database unavailable", readErr: errors.New("database unavailable"), wantErr: ErrAuthVersionUnavailable},
		{name: "account missing", readErr: ErrAuthVersionUserMissing, wantErr: ErrAccessTokenInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := NewVersionedAccessTokenVerifier(manager, testAuthVersionReader{version: test.version, err: test.readErr})
			if err != nil {
				t.Fatal(err)
			}
			claims, err := verifier.VerifyAccessContext(context.Background(), pair.AccessToken)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error=%v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || claims == nil || claims.AuthVersion != 3 {
				t.Fatalf("claims=%+v error=%v", claims, err)
			}
		})
	}
}

func TestVerifyAccessAcceptsOldPublicKeyDuringRotation(t *testing.T) {
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldPublic, oldPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		ActiveKID:          "new-v2",
		PrivateKey:         newPrivate,
		VerifyKeys:         map[string]ed25519.PublicKey{"new-v2": newPublic, "old-v1": oldPublic},
		Issuer:             "go.exchange.test",
		Audience:           "go.exchange.test.api",
		AccessTTL:          15 * time.Minute,
		RefreshIdleTTL:     time.Hour,
		RefreshAbsoluteTTL: 24 * time.Hour,
	}, newMemoryRefreshStore())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldToken := jwt.NewWithClaims(jwt.SigningMethodEdDSA, AccessClaims{
		SessionID:   uuid.NewString(),
		TokenType:   accessTokenType,
		AuthVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    manager.config.Issuer,
			Subject:   "22",
			Audience:  jwt.ClaimStrings{manager.config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	})
	oldToken.Header["kid"] = "old-v1"
	raw, err := oldToken.SignedString(oldPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyAccess(raw); err != nil {
		t.Fatalf("old public key should verify during rotation: %v", err)
	}
}
