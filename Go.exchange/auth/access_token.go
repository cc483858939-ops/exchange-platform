package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const accessTokenType = "access"

type AccessClaims struct {
	SessionID   string `json:"sid"`
	TokenType   string `json:"typ"`
	AuthVersion int64  `json:"auth_version"`
	jwt.RegisteredClaims
}

func (c AccessClaims) Validate() error {
	if c.TokenType != accessTokenType || strings.TrimSpace(c.SessionID) == "" || c.AuthVersion <= 0 {
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

type ContextAccessTokenVerifier interface {
	VerifyAccessContext(context.Context, string) (*AccessClaims, error)
}

type AuthVersionReader interface {
	ReadAuthVersion(context.Context, uint) (int64, error)
}

var (
	ErrAuthVersionUnavailable = errors.New("account authentication version is unavailable")
	ErrAuthVersionUserMissing = errors.New("account authentication version user is missing")
)

type VersionedAccessTokenVerifier struct {
	verifier AccessTokenVerifier
	versions AuthVersionReader
}

func NewVersionedAccessTokenVerifier(verifier AccessTokenVerifier, versions AuthVersionReader) (*VersionedAccessTokenVerifier, error) {
	if verifier == nil {
		return nil, errors.New("access token verifier is required")
	}
	if versions == nil {
		return nil, errors.New("authentication version reader is required")
	}
	return &VersionedAccessTokenVerifier{verifier: verifier, versions: versions}, nil
}

func (v *VersionedAccessTokenVerifier) VerifyAccess(rawToken string) (*AccessClaims, error) {
	return v.VerifyAccessContext(context.Background(), rawToken)
}

func (v *VersionedAccessTokenVerifier) VerifyAccessContext(ctx context.Context, rawToken string) (*AccessClaims, error) {
	claims, err := v.verifier.VerifyAccess(rawToken)
	if err != nil {
		return nil, err
	}
	if claims == nil {
		return nil, ErrAccessTokenInvalid
	}
	userID, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil || userID == 0 || uint64(uint(userID)) != userID {
		return nil, ErrAccessTokenInvalid
	}
	currentVersion, err := v.versions.ReadAuthVersion(ctx, uint(userID))
	if errors.Is(err, ErrAuthVersionUserMissing) {
		return nil, ErrAccessTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthVersionUnavailable, err)
	}
	if currentVersion <= 0 {
		return nil, ErrAuthVersionUnavailable
	}
	if claims.AuthVersion != currentVersion {
		return nil, ErrAccessTokenInvalid
	}
	return claims, nil
}

// GormAuthVersionReader reads the account's current auth_version from the
// primary database so every authenticated request observes password changes.
type GormAuthVersionReader struct {
	db interface {
		WithContext(context.Context) *gorm.DB
	}
}

func NewGormAuthVersionReader(db *gorm.DB) (*GormAuthVersionReader, error) {
	if db == nil {
		return nil, errors.New("authentication version database is required")
	}
	return &GormAuthVersionReader{db: db}, nil
}

func (r *GormAuthVersionReader) ReadAuthVersion(ctx context.Context, userID uint) (int64, error) {
	if r == nil || r.db == nil || userID == 0 {
		return 0, ErrAuthVersionUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var row struct {
		AuthVersion int64 `gorm:"column:auth_version"`
	}
	err := r.db.WithContext(ctx).Table("users").Select("auth_version").Where("id = ? AND deleted_at IS NULL", userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrAuthVersionUserMissing
	}
	if err != nil || row.AuthVersion <= 0 {
		return 0, errors.Join(ErrAuthVersionUnavailable, err)
	}
	return row.AuthVersion, nil
}

func (m *Manager) signAccessToken(userID uint, authVersion int64, sessionID string, now time.Time) (string, error) {
	claims := AccessClaims{
		SessionID:   sessionID,
		TokenType:   accessTokenType,
		AuthVersion: authVersion,
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
