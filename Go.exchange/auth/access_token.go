package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const accessTokenType = "access"

type AccessClaims struct {
	SessionID string `json:"sid"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

func (c AccessClaims) Validate() error {
	if c.TokenType != accessTokenType || strings.TrimSpace(c.SessionID) == "" {
		return ErrAccessTokenInvalid
	}
	if c.Subject == "" || c.ID == "" || c.IssuedAt == nil || c.NotBefore == nil || c.ExpiresAt == nil {
		return ErrAccessTokenInvalid
	}
	userID, err := strconv.ParseUint(c.Subject, 10, 64)
	if err != nil || userID == 0 {
		return ErrAccessTokenInvalid
	}
	return nil
}

type AccessTokenVerifier interface {
	VerifyAccess(rawToken string) (*AccessClaims, error)
}

func (m *Manager) signAccessToken(userID uint, sessionID string, now time.Time) (string, error) {
	claims := AccessClaims{
		SessionID: sessionID,
		TokenType: accessTokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.config.Issuer,
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Audience:  jwt.ClaimStrings{m.config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(m.config.AccessTTL)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = m.config.ActiveKID
	signed, err := token.SignedString(m.config.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

func (m *Manager) VerifyAccess(rawToken string) (*AccessClaims, error) {
	if strings.TrimSpace(rawToken) == "" || strings.ContainsAny(rawToken, " \t\r\n") {
		return nil, ErrAccessTokenInvalid
	}
	claims := &AccessClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(m.config.Issuer),
		jwt.WithAudience(m.config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(m.config.ClockSkew),
	)
	token, err := parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodEdDSA {
			return nil, ErrAccessTokenInvalid
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, ErrAccessTokenInvalid
		}
		publicKey, ok := m.config.VerifyKeys[kid]
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return nil, ErrAccessTokenInvalid
		}
		return publicKey, nil
	})
	if err != nil || token == nil || !token.Valid {
		return nil, errors.Join(ErrAccessTokenInvalid, err)
	}
	return claims, nil
}
