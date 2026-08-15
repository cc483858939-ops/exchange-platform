package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

const refreshTokenPrefix = "rt1"

type parsedRefreshToken struct {
	sessionID string
	secret    []byte
}

func newRefreshSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func formatRefreshToken(sessionID string, secret []byte) string {
	return refreshTokenPrefix + "." + sessionID + "." + base64.RawURLEncoding.EncodeToString(secret)
}

func parseRefreshToken(raw string) (parsedRefreshToken, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] != refreshTokenPrefix {
		return parsedRefreshToken{}, ErrRefreshInvalid
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return parsedRefreshToken{}, ErrRefreshInvalid
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != 32 {
		return parsedRefreshToken{}, ErrRefreshInvalid
	}
	return parsedRefreshToken{sessionID: parts[1], secret: secret}, nil
}

func hashRefreshSecret(secret []byte) string {
	digest := sha256.Sum256(secret)
	return hex.EncodeToString(digest[:])
}
