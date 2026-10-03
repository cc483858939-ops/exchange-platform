package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// Recovery is limited to the exact request and the still-current successor.
// Replays do not extend this window or the refresh session's idle lifetime.
const refreshRecoveryTTL = 2 * time.Minute

type RefreshRecovery struct {
	RequestID    string
	SealedSecret string
}

func refreshRecoveryCipher(previousSecret []byte) (cipher.AEAD, error) {
	// The old opaque secret is random and is never stored in Redis. Deriving
	// the wrapping key from it keeps recovery independent of JWT key rotation;
	// neither the stored old hash nor the sealed successor reveals this key.
	key := hmac.New(sha256.New, previousSecret)
	_, _ = key.Write([]byte("go.exchange:refresh-recovery:v1"))
	block, err := aes.NewCipher(key.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func refreshRecoveryBinding(sessionID, expectedHash, requestID string) []byte {
	return []byte(sessionID + ":" + expectedHash + ":" + requestID)
}

func sealRefreshSecret(previousSecret []byte, sessionID, expectedHash, requestID string, secret []byte) (string, error) {
	aead, err := refreshRecoveryCipher(previousSecret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := aead.Seal(nonce, nonce, secret, refreshRecoveryBinding(sessionID, expectedHash, requestID))
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func openRefreshSecret(previousSecret []byte, sessionID, expectedHash, requestID, sealedSecret string) ([]byte, error) {
	aead, err := refreshRecoveryCipher(previousSecret)
	if err != nil {
		return nil, err
	}
	payload, err := base64.RawURLEncoding.DecodeString(sealedSecret)
	nonceSize := aead.NonceSize()
	if err != nil || len(payload) != nonceSize+32+aead.Overhead() {
		return nil, errors.New("invalid refresh recovery result")
	}
	secret, err := aead.Open(nil, payload[:nonceSize], payload[nonceSize:], refreshRecoveryBinding(sessionID, expectedHash, requestID))
	if err != nil {
		return nil, errors.New("cannot authenticate refresh recovery result")
	}
	return secret, nil
}
