package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type TokenPair struct {
	UserID           uint
	AccessToken      string
	RefreshToken     string
	TokenType        string
	AccessExpiresIn  time.Duration
	RefreshExpiresIn time.Duration
}

type TokenService interface {
	AccessTokenVerifier
	IssuePair(ctx context.Context, userID uint) (TokenPair, error)
	RotateRefresh(ctx context.Context, rawRefreshToken string) (TokenPair, error)
}

type Manager struct {
	config Config
	store  RefreshStore
	now    func() time.Time
}

func NewManager(config Config, store RefreshStore) (*Manager, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("refresh store is required")
	}
	return &Manager{config: config, store: store, now: time.Now}, nil
}

func (m *Manager) IssuePair(ctx context.Context, userID uint) (TokenPair, error) {
	if userID == 0 {
		return TokenPair{}, fmt.Errorf("user ID must be positive")
	}
	now := m.now().UTC()
	sessionID := uuid.NewString()
	secret, err := newRefreshSecret()
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate refresh token: %w", err)
	}
	accessToken, err := m.signAccessToken(userID, sessionID, now)
	if err != nil {
		return TokenPair{}, err
	}

	absoluteExpiresAt := now.Add(m.config.RefreshAbsoluteTTL)
	refreshTTL := minDuration(m.config.RefreshIdleTTL, absoluteExpiresAt.Sub(now))
	if refreshTTL <= 0 {
		return TokenPair{}, ErrRefreshExpired
	}
	if err := m.store.Create(ctx, RefreshSession{
		SessionID:         sessionID,
		UserID:            userID,
		SecretHash:        hashRefreshSecret(secret),
		CreatedAt:         now,
		LastRotatedAt:     now,
		AbsoluteExpiresAt: absoluteExpiresAt,
		TTL:               refreshTTL,
	}); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		UserID:           userID,
		AccessToken:      accessToken,
		RefreshToken:     formatRefreshToken(sessionID, secret),
		TokenType:        "Bearer",
		AccessExpiresIn:  m.config.AccessTTL,
		RefreshExpiresIn: refreshTTL,
	}, nil
}

func (m *Manager) RotateRefresh(ctx context.Context, rawRefreshToken string) (TokenPair, error) {
	parsed, err := parseRefreshToken(rawRefreshToken)
	if err != nil {
		return TokenPair{}, err
	}
	newSecret, err := newRefreshSecret()
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate refresh token: %w", err)
	}
	now := m.now().UTC()
	userID, effectiveTTL, err := m.store.Rotate(
		ctx,
		parsed.sessionID,
		hashRefreshSecret(parsed.secret),
		hashRefreshSecret(newSecret),
		now,
		m.config.RefreshIdleTTL,
	)
	if err != nil {
		return TokenPair{}, err
	}
	accessToken, err := m.signAccessToken(userID, parsed.sessionID, now)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		UserID:           userID,
		AccessToken:      accessToken,
		RefreshToken:     formatRefreshToken(parsed.sessionID, newSecret),
		TokenType:        "Bearer",
		AccessExpiresIn:  m.config.AccessTTL,
		RefreshExpiresIn: effectiveTTL,
	}, nil
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
