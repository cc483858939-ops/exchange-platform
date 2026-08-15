package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v7"
)

const (
	refreshKeyPrefix     = "auth:refresh:v1:"
	usedRefreshKeyPrefix = "auth:refresh:used:v1:"
)

type RefreshSession struct {
	SessionID         string
	UserID            uint
	SecretHash        string
	CreatedAt         time.Time
	LastRotatedAt     time.Time
	AbsoluteExpiresAt time.Time
	TTL               time.Duration
}

type RefreshStore interface {
	Create(ctx context.Context, session RefreshSession) error
	Rotate(
		ctx context.Context,
		sessionID string,
		expectedSecretHash string,
		newSecretHash string,
		now time.Time,
		idleTTL time.Duration,
	) (userID uint, effectiveTTL time.Duration, err error)
}

type RedisRefreshStore struct {
	client *redis.Client
}

func NewRedisRefreshStore(client *redis.Client) (*RedisRefreshStore, error) {
	if client == nil {
		return nil, errors.New("Redis client is required for refresh sessions")
	}
	return &RedisRefreshStore{client: client}, nil
}

var createRefreshSessionScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('HSET', KEYS[1],
  'user_id', ARGV[1],
  'secret_hash', ARGV[2],
  'created_at', ARGV[3],
  'last_rotated_at', ARGV[4],
  'absolute_expires_at', ARGV[5])
redis.call('EXPIRE', KEYS[1], ARGV[6])
return 1
`)

var rotateRefreshSessionScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  if redis.call('EXISTS', KEYS[2]) == 1 then
    return {-2}
  end
  return {-1}
end
local current_hash = redis.call('HGET', KEYS[1], 'secret_hash')
if not current_hash or current_hash ~= ARGV[1] then
  if redis.call('EXISTS', KEYS[2]) == 1 then
    redis.call('DEL', KEYS[1])
    return {-2}
  end
  return {-1}
end
local absolute_expires_at = tonumber(redis.call('HGET', KEYS[1], 'absolute_expires_at'))
local now = tonumber(ARGV[3])
if not absolute_expires_at or absolute_expires_at <= now then
  redis.call('DEL', KEYS[1])
  return {-3}
end
local idle_ttl = tonumber(ARGV[4])
local absolute_remaining = absolute_expires_at - now
local session_ttl = idle_ttl
if session_ttl > absolute_remaining then
  session_ttl = absolute_remaining
end
if session_ttl < 1 then
  redis.call('DEL', KEYS[1])
  return {-3}
end
local user_id = redis.call('HGET', KEYS[1], 'user_id')
redis.call('SET', KEYS[2], 'used', 'EX', absolute_remaining)
redis.call('HSET', KEYS[1], 'secret_hash', ARGV[2], 'last_rotated_at', ARGV[3])
redis.call('EXPIRE', KEYS[1], session_ttl)
return {1, user_id, session_ttl}
`)

func (s *RedisRefreshStore) Create(ctx context.Context, session RefreshSession) error {
	if session.SessionID == "" || session.UserID == 0 || session.SecretHash == "" || session.TTL <= 0 {
		return errors.New("invalid refresh session")
	}
	result, err := createRefreshSessionScript.Run(
		s.client.WithContext(ctx),
		[]string{refreshSessionKey(session.SessionID)},
		strconv.FormatUint(uint64(session.UserID), 10),
		session.SecretHash,
		strconv.FormatInt(session.CreatedAt.Unix(), 10),
		strconv.FormatInt(session.LastRotatedAt.Unix(), 10),
		strconv.FormatInt(session.AbsoluteExpiresAt.Unix(), 10),
		strconv.FormatInt(durationSecondsCeil(session.TTL), 10),
	).Int64()
	if err != nil {
		return fmt.Errorf("create refresh session: %w", err)
	}
	if result != 1 {
		return errors.New("refresh session already exists")
	}
	return nil
}

func (s *RedisRefreshStore) Rotate(
	ctx context.Context,
	sessionID string,
	expectedSecretHash string,
	newSecretHash string,
	now time.Time,
	idleTTL time.Duration,
) (uint, time.Duration, error) {
	result, err := rotateRefreshSessionScript.Run(
		s.client.WithContext(ctx),
		[]string{
			refreshSessionKey(sessionID),
			usedRefreshKey(sessionID, expectedSecretHash),
		},
		expectedSecretHash,
		newSecretHash,
		strconv.FormatInt(now.Unix(), 10),
		strconv.FormatInt(durationSecondsCeil(idleTTL), 10),
	).Result()
	if err != nil {
		return 0, 0, fmt.Errorf("rotate refresh session: %w", err)
	}
	values, ok := result.([]interface{})
	if !ok || len(values) == 0 {
		return 0, 0, errors.New("invalid Redis refresh rotation response")
	}
	status, err := parseRedisInt(values[0])
	if err != nil {
		return 0, 0, errors.New("invalid Redis refresh rotation status")
	}
	switch status {
	case -1:
		return 0, 0, ErrRefreshInvalid
	case -2:
		return 0, 0, ErrRefreshReused
	case -3:
		return 0, 0, ErrRefreshExpired
	case 1:
		if len(values) != 3 {
			return 0, 0, errors.New("incomplete Redis refresh rotation response")
		}
		userIDValue, err := parseRedisInt(values[1])
		if err != nil || userIDValue <= 0 {
			return 0, 0, errors.New("invalid refresh session user ID")
		}
		ttlSeconds, err := parseRedisInt(values[2])
		if err != nil || ttlSeconds <= 0 {
			return 0, 0, errors.New("invalid refresh session TTL")
		}
		return uint(userIDValue), time.Duration(ttlSeconds) * time.Second, nil
	default:
		return 0, 0, errors.New("unknown Redis refresh rotation status")
	}
}

func refreshSessionKey(sessionID string) string {
	return refreshKeyPrefix + "{" + sessionID + "}"
}

func usedRefreshKey(sessionID, secretHash string) string {
	return usedRefreshKeyPrefix + "{" + sessionID + "}:" + secretHash
}

func parseRedisInt(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}

func durationSecondsCeil(value time.Duration) int64 {
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}
