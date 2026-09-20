package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/metrics"

	"github.com/go-redis/redis/v7"
)

const redisCleanupBufferSeconds int64 = 5

var fixedWindowCheckScript = redis.NewScript(`
local blocked = false
local blocking_index = 0
local blocking_retry = 0

for i = 1, #KEYS do
  local offset = (i - 1) * 3
  local limit = tonumber(ARGV[offset + 1])
  local retry_after = tonumber(ARGV[offset + 2])
  local count = tonumber(redis.call('GET', KEYS[i]) or '0')
  if count >= limit then
    if not blocked or retry_after > blocking_retry then
      blocked = true
      blocking_index = i
      blocking_retry = retry_after
    end
  end
end

if blocked then
  return {0, blocking_index, 0}
end

local effective_index = 0
local effective_remaining = 0
for i = 1, #KEYS do
  local offset = (i - 1) * 3
  local limit = tonumber(ARGV[offset + 1])
  local ttl = tonumber(ARGV[offset + 3])
	local count = redis.call('INCR', KEYS[i])
	redis.call('EXPIRE', KEYS[i], ttl)
  local remaining = limit - count
  if effective_index == 0 or remaining < effective_remaining then
    effective_index = i
    effective_remaining = remaining
  end
end

return {1, effective_index, effective_remaining}
`)

type RedisLimiter struct {
	client    *redis.Client
	now       func() time.Time
	runScript func(context.Context, []string, []interface{}) (int64, int64, int64, error)
}

func NewRedisLimiter(client *redis.Client) (Limiter, error) {
	if client == nil {
		return nil, errors.New("Redis client is required for application rate limiting")
	}
	return &RedisLimiter{client: client, now: time.Now}, nil
}

func (l *RedisLimiter) Allow(ctx context.Context, input Input) (Decision, error) {
	if l == nil {
		return Decision{}, errors.New("application rate limiter is unavailable")
	}
	policy, err := PolicyFor(input.Action)
	if err != nil {
		metrics.RecordRateLimitError(string(input.Action))
		return Decision{}, err
	}
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		metrics.RecordRateLimitError(string(input.Action))
		return Decision{}, errors.New("rate-limit subject is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}

	keys := make([]string, 0, len(policy.Rules))
	args := make([]interface{}, 0, len(policy.Rules)*3)
	for _, rule := range policy.Rules {
		windowSeconds := int64(rule.Window / time.Second)
		resetAt := fixedWindowResetAt(now, rule.Window)
		retryAfterSeconds := ceilSeconds(resetAt.Sub(now))
		ttlSeconds := retryAfterSeconds + redisCleanupBufferSeconds
		windowID := now.Unix() / windowSeconds
		keys = append(keys, rateLimitKey(subject, input.Action, windowSeconds, windowID))
		args = append(args, rule.Limit, retryAfterSeconds, ttlSeconds)
	}

	var allowed, effectiveIndex, remaining int64
	if l.runScript != nil {
		allowed, effectiveIndex, remaining, err = l.runScript(ctx, keys, args)
	} else {
		if l.client == nil {
			metrics.RecordRateLimitError(string(input.Action))
			return Decision{}, errors.New("Redis client is required for application rate limiting")
		}
		allowed, effectiveIndex, remaining, err = runFixedWindowScript(ctx, l.client, keys, args)
	}
	if err != nil {
		metrics.RecordRateLimitError(string(input.Action))
		return Decision{}, err
	}
	if effectiveIndex < 1 || effectiveIndex > int64(len(policy.Rules)) {
		metrics.RecordRateLimitError(string(input.Action))
		return Decision{}, errors.New("invalid rate limiter response")
	}

	rule := policy.Rules[effectiveIndex-1]
	resetAt := fixedWindowResetAt(now, rule.Window)
	retryAfter := time.Duration(0)
	if allowed == 0 {
		retryAfter = resetAt.Sub(now)
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		remaining = 0
	} else if remaining < 0 {
		remaining = 0
	}
	decision := Decision{
		Allowed:    allowed != 0,
		Limit:      rule.Limit,
		Remaining:  remaining,
		RetryAfter: retryAfter,
		ResetAt:    resetAt,
	}
	if decision.Allowed {
		metrics.RecordRateLimitDecision(string(input.Action), "allowed")
	} else {
		metrics.RecordRateLimitDecision(string(input.Action), "denied")
	}
	return decision, nil
}

func runFixedWindowScript(ctx context.Context, client *redis.Client, keys []string, args []interface{}) (int64, int64, int64, error) {
	result, err := fixedWindowCheckScript.Run(client.WithContext(ctx), keys, args...).Result()
	if err != nil {
		return 0, 0, 0, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return 0, 0, 0, errors.New("invalid rate limiter response")
	}
	allowed, err := parseRedisInt(values[0])
	if err != nil {
		return 0, 0, 0, err
	}
	effectiveIndex, err := parseRedisInt(values[1])
	if err != nil {
		return 0, 0, 0, err
	}
	remaining, err := parseRedisInt(values[2])
	if err != nil {
		return 0, 0, 0, err
	}
	return allowed, effectiveIndex, remaining, nil
}

func parseRedisInt(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("invalid Redis integer type %T", value)
	}
}

func rateLimitKey(subject string, action Action, windowSeconds, windowID int64) string {
	return fmt.Sprintf("rl:v1:{user:%s}:%s:%d:%d", subject, action, windowSeconds, windowID)
}

func fixedWindowResetAt(now time.Time, window time.Duration) time.Time {
	windowSeconds := int64(window / time.Second)
	windowID := now.Unix() / windowSeconds
	return time.Unix((windowID+1)*windowSeconds, 0).UTC()
}

func ceilSeconds(duration time.Duration) int64 {
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return int64(seconds)
}
