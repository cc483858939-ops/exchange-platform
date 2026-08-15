package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestVerifyAccessRejectsMissingKIDAndExpiredToken(t *testing.T) {
	manager, _, privateKey := testManager(t)
	now := time.Now().UTC()
	claims := AccessClaims{
		SessionID: uuid.NewString(),
		TokenType: accessTokenType,
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
		SessionID: uuid.NewString(),
		TokenType: accessTokenType,
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
