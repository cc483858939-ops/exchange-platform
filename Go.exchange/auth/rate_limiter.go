package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
)

type AttemptAction string

const (
	AttemptLogin    AttemptAction = "login"
	AttemptRegister AttemptAction = "register"
	AttemptRefresh  AttemptAction = "refresh"
)

type AttemptInput struct {
	Action   AttemptAction
	ClientIP string
	Subject  string
}

type AttemptDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type AttemptLimiter interface {
	Allow(context.Context, AttemptInput) (AttemptDecision, error)
}

type attemptPolicy struct {
	window       time.Duration
	ipLimit      int64
	subjectLimit int64
}

var attemptPolicies = map[AttemptAction]attemptPolicy{
	AttemptLogin:    {window: time.Minute, ipLimit: 30, subjectLimit: 5},
	AttemptRegister: {window: 10 * time.Minute, ipLimit: 10, subjectLimit: 3},
	AttemptRefresh:  {window: time.Minute, ipLimit: 60, subjectLimit: 20},
}

var attemptCounterScript = redis.NewScript(`
local ip_count = redis.call('INCR', KEYS[1])
if ip_count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
local subject_count = redis.call('INCR', KEYS[2])
if subject_count == 1 then
  redis.call('EXPIRE', KEYS[2], ARGV[1])
end
return {ip_count, subject_count}
`)

type RedisAttemptLimiter struct {
	client    *redis.Client
	now       func() time.Time
	runScript func(context.Context, []string, int64) (int64, int64, error)
}

func NewRedisAttemptLimiter(client *redis.Client) (AttemptLimiter, error) {
	if client == nil {
		return nil, errors.New("Redis client is required for authentication rate limiting")
	}
	return &RedisAttemptLimiter{
		client: client,
		now:    time.Now,
	}, nil
}

func (l *RedisAttemptLimiter) Allow(ctx context.Context, input AttemptInput) (AttemptDecision, error) {
	if l == nil {
		return AttemptDecision{}, errors.New("authentication rate limiter is unavailable")
	}
	policy, ok := attemptPolicies[input.Action]
	if !ok {
		return AttemptDecision{}, errors.New("unsupported authentication attempt action")
	}

	clientIP, err := normalizeClientIP(input.ClientIP)
	if err != nil {
		return AttemptDecision{}, err
	}
	subject := normalizeAttemptSubject(input.Action, input.Subject)
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	windowSeconds := int64(policy.window / time.Second)
	windowID := now.Unix() / windowSeconds
	keys := []string{
		attemptKey(input.Action, "ip", hashAttemptValue(clientIP), windowID),
		attemptKey(input.Action, "subject", hashAttemptValue(subject), windowID),
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var ipCount, subjectCount int64
	if l.runScript != nil {
		ipCount, subjectCount, err = l.runScript(ctx, keys, windowSeconds+5)
	} else {
		if l.client == nil {
			return AttemptDecision{}, errors.New("Redis client is required for authentication rate limiting")
		}
		ipCount, subjectCount, err = runAttemptCounterScript(ctx, l.client, keys, windowSeconds+5)
	}
	if err != nil {
		return AttemptDecision{}, err
	}

	if ipCount <= policy.ipLimit && subjectCount <= policy.subjectLimit {
		return AttemptDecision{Allowed: true}, nil
	}
	return AttemptDecision{
		Allowed:    false,
		RetryAfter: retryAfterForWindow(now, policy.window),
	}, nil
}

// RetryAfterSeconds converts a limiter decision into the bounded integer
// value required by the HTTP Retry-After header.
func RetryAfterSeconds(action AttemptAction, retryAfter time.Duration) int {
	policy, ok := attemptPolicies[action]
	if !ok {
		return 1
	}
	seconds := retryAfter / time.Second
	if retryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	maxSeconds := policy.window / time.Second
	if seconds > maxSeconds {
		seconds = maxSeconds
	}
	return int(seconds)
}

func runAttemptCounterScript(ctx context.Context, client *redis.Client, keys []string, ttlSeconds int64) (int64, int64, error) {
	result, err := attemptCounterScript.Run(client.WithContext(ctx), keys, ttlSeconds).Result()
	if err != nil {
		return 0, 0, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return 0, 0, errors.New("invalid authentication rate limiter response")
	}
	ipCount, err := parseRedisInt(values[0])
	if err != nil {
		return 0, 0, errors.New("invalid authentication rate limiter IP count")
	}
	subjectCount, err := parseRedisInt(values[1])
	if err != nil {
		return 0, 0, errors.New("invalid authentication rate limiter subject count")
	}
	return ipCount, subjectCount, nil
}

func normalizeClientIP(raw string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return "", errors.New("invalid authentication client IP")
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.String(), nil
	}
	return ip.String(), nil
}

func normalizeAttemptSubject(action AttemptAction, raw string) string {
	subject := strings.TrimSpace(raw)
	if action == AttemptLogin || action == AttemptRegister {
		return strings.ToLower(subject)
	}
	return subject
}

func hashAttemptValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func attemptKey(action AttemptAction, bucket, digest string, windowID int64) string {
	return "auth:attempts:v1:" + string(action) + ":" + bucket + ":" + digest + ":" + strconv.FormatInt(windowID, 10)
}

func retryAfterForWindow(now time.Time, window time.Duration) time.Duration {
	windowSeconds := int64(window / time.Second)
	windowID := now.Unix() / windowSeconds
	nextBoundary := time.Unix((windowID+1)*windowSeconds, 0)
	retryAfter := nextBoundary.Sub(now)
	if retryAfter < time.Second {
		return time.Second
	}
	if retryAfter > window {
		return window
	}
	return retryAfter
}
