package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v7"
)

const recommendationClickCooldownPendingTTL = 60 * time.Second

var ErrRecommendationClickCooldownOwnershipLost = errors.New("recommendation click cooldown ownership lost")

type RecommendationClickCooldownAcquireResult uint8

const (
	RecommendationClickCooldownAcquired RecommendationClickCooldownAcquireResult = iota + 1
	RecommendationClickCooldownDuplicate
	RecommendationClickCooldownPending
)

// RecommendationClickCooldown atomically reserves a signed recommendation
// opportunity (user/request/Post) until the Kafka publish outcome is known.
// Durable per-opportunity CTR deduplication happens in PostgreSQL.
type RecommendationClickCooldown interface {
	Acquire(ctx context.Context, userID, postID uint, requestID, eventID string) (RecommendationClickCooldownAcquireResult, error)
	Commit(ctx context.Context, userID, postID uint, requestID, eventID string) error
	Release(ctx context.Context, userID, postID uint, requestID, eventID string) error
}

type redisRecommendationClickCooldown struct {
	client          *redis.Client
	pendingSeconds  int64
	cooldownSeconds int64
}

var acquireRecommendationClickCooldownScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current then
  local acquired = redis.call('SET', KEYS[1], ARGV[1], 'NX', 'EX', ARGV[2])
  if acquired then
    return 1
  end
  current = redis.call('GET', KEYS[1])
end
if string.sub(current, 1, 10) == 'COMMITTED:' then
  return 2
end
if string.sub(current, 1, 8) == 'PENDING:' then
  return 3
end
return redis.error_reply('invalid recommendation click cooldown state')
`)

var commitRecommendationClickCooldownScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'EX', ARGV[3])
  return 1
end
return 0
`)

var releaseRecommendationClickCooldownScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func NewRedisRecommendationClickCooldown(client *redis.Client, cooldownTTL time.Duration) (RecommendationClickCooldown, error) {
	return newRedisRecommendationClickCooldown(client, recommendationClickCooldownPendingTTL, cooldownTTL)
}

func newRedisRecommendationClickCooldown(client *redis.Client, pendingTTL, cooldownTTL time.Duration) (*redisRecommendationClickCooldown, error) {
	if client == nil {
		return nil, errors.New("redis client is required for recommendation click cooldown")
	}
	pendingSeconds, err := clickCooldownTTLSeconds(pendingTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid recommendation click pending TTL: %w", err)
	}
	cooldownSeconds, err := clickCooldownTTLSeconds(cooldownTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid recommendation click cooldown TTL: %w", err)
	}
	return &redisRecommendationClickCooldown{
		client: client, pendingSeconds: pendingSeconds, cooldownSeconds: cooldownSeconds,
	}, nil
}

func clickCooldownTTLSeconds(ttl time.Duration) (int64, error) {
	if ttl <= 0 || ttl%time.Second != 0 {
		return 0, errors.New("TTL must be a positive whole number of seconds")
	}
	return int64(ttl / time.Second), nil
}

func (store *redisRecommendationClickCooldown) Acquire(ctx context.Context, userID, postID uint, requestID, eventID string) (RecommendationClickCooldownAcquireResult, error) {
	if store == nil || store.client == nil {
		return 0, errors.New("redis click cooldown is unavailable")
	}
	key := recommendationClickCooldownKey(userID, postID, requestID)
	pendingValue := "PENDING:" + eventID
	result, err := acquireRecommendationClickCooldownScript.Run(
		store.client.WithContext(ctx), []string{key}, pendingValue, store.pendingSeconds,
	).Int()
	if err != nil {
		return 0, err
	}
	switch result {
	case 1:
		return RecommendationClickCooldownAcquired, nil
	case 2:
		return RecommendationClickCooldownDuplicate, nil
	case 3:
		return RecommendationClickCooldownPending, nil
	default:
		return 0, errors.New("unexpected recommendation click cooldown acquire result")
	}
}

func (store *redisRecommendationClickCooldown) Commit(ctx context.Context, userID, postID uint, requestID, eventID string) error {
	if store == nil || store.client == nil {
		return errors.New("redis click cooldown is unavailable")
	}
	result, err := commitRecommendationClickCooldownScript.Run(
		store.client.WithContext(ctx), []string{recommendationClickCooldownKey(userID, postID, requestID)},
		"PENDING:"+eventID, "COMMITTED:"+eventID, store.cooldownSeconds,
	).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrRecommendationClickCooldownOwnershipLost
	}
	return nil
}

func (store *redisRecommendationClickCooldown) Release(ctx context.Context, userID, postID uint, requestID, eventID string) error {
	if store == nil || store.client == nil {
		return errors.New("redis click cooldown is unavailable")
	}
	_, err := releaseRecommendationClickCooldownScript.Run(
		store.client.WithContext(ctx), []string{recommendationClickCooldownKey(userID, postID, requestID)}, "PENDING:"+eventID,
	).Result()
	return err
}

func recommendationClickCooldownKey(userID, postID uint, requestID string) string {
	return fmt.Sprintf("recommendation:telemetry:click:cooldown:%d:%s:%d", userID, requestID, postID)
}
