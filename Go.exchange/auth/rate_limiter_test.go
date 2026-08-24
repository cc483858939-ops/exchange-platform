package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type attemptScriptFake struct {
	counts map[string]int64
	keys   [][]string
	ttls   []int64
	err    error
}

func (fake *attemptScriptFake) run(_ context.Context, keys []string, ttlSeconds int64) (int64, int64, error) {
	fake.keys = append(fake.keys, append([]string(nil), keys...))
	fake.ttls = append(fake.ttls, ttlSeconds)
	if fake.err != nil {
		return 0, 0, fake.err
	}
	if fake.counts == nil {
		fake.counts = make(map[string]int64)
	}
	fake.counts[keys[0]]++
	fake.counts[keys[1]]++
	return fake.counts[keys[0]], fake.counts[keys[1]], nil
}

func newTestAttemptLimiter(now func() time.Time, fake *attemptScriptFake) *RedisAttemptLimiter {
	return &RedisAttemptLimiter{
		client:    redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}),
		now:       now,
		runScript: fake.run,
	}
}

func TestAttemptLimiterUsesExactSubjectPolicies(t *testing.T) {
	for _, test := range []struct {
		action AttemptAction
		limit  int
	}{
		{action: AttemptLogin, limit: 5},
		{action: AttemptRegister, limit: 3},
		{action: AttemptRefresh, limit: 20},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			fake := &attemptScriptFake{}
			limiter := newTestAttemptLimiter(func() time.Time { return time.Unix(120, 0) }, fake)
			input := AttemptInput{Action: test.action, ClientIP: "192.0.2.1", Subject: "Alice"}
			for attempt := 0; attempt < test.limit; attempt++ {
				decision, err := limiter.Allow(context.Background(), input)
				if err != nil || !decision.Allowed {
					t.Fatalf("attempt %d decision=%+v err=%v", attempt+1, decision, err)
				}
			}
			decision, err := limiter.Allow(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed {
				t.Fatalf("attempt %d unexpectedly allowed", test.limit+1)
			}
		})
	}
}

func TestAttemptLimiterRejectsWhenIPBucketExceeds(t *testing.T) {
	for _, test := range []struct {
		action AttemptAction
		limit  int
	}{
		{action: AttemptLogin, limit: 30},
		{action: AttemptRegister, limit: 10},
		{action: AttemptRefresh, limit: 60},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			fake := &attemptScriptFake{}
			limiter := newTestAttemptLimiter(func() time.Time { return time.Unix(120, 0) }, fake)
			for attempt := 0; attempt < test.limit; attempt++ {
				decision, err := limiter.Allow(context.Background(), AttemptInput{
					Action:   test.action,
					ClientIP: "192.0.2.1",
					Subject:  "user-" + string(rune('a'+attempt)),
				})
				if err != nil || !decision.Allowed {
					t.Fatalf("attempt %d decision=%+v err=%v", attempt+1, decision, err)
				}
			}
			decision, err := limiter.Allow(context.Background(), AttemptInput{
				Action:   test.action,
				ClientIP: "192.0.2.1",
				Subject:  "user-over-limit",
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed {
				t.Fatalf("IP attempt %d unexpectedly allowed", test.limit+1)
			}
		})
	}
}

func TestAttemptLimiterNormalizesSubjectsAndHidesSensitiveValuesInKeys(t *testing.T) {
	fake := &attemptScriptFake{}
	limiter := newTestAttemptLimiter(func() time.Time { return time.Unix(120, 0) }, fake)
	if _, err := limiter.Allow(context.Background(), AttemptInput{
		Action:   AttemptLogin,
		ClientIP: "::ffff:192.0.2.1",
		Subject:  "  Alice ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow(context.Background(), AttemptInput{
		Action:   AttemptLogin,
		ClientIP: "192.0.2.1",
		Subject:  "alice",
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.keys) != 2 || fake.keys[0][0] != fake.keys[1][0] || fake.keys[0][1] != fake.keys[1][1] {
		t.Fatalf("normalized inputs produced different keys: %v", fake.keys)
	}
	for _, key := range fake.keys[0] {
		for _, forbidden := range []string{"192.0.2.1", "Alice", "alice", "rt1."} {
			if strings.Contains(key, forbidden) {
				t.Fatalf("key %q contains sensitive value %q", key, forbidden)
			}
		}
	}
}

func TestAttemptLimiterRefreshSubjectTrimsButDoesNotLowercase(t *testing.T) {
	fake := &attemptScriptFake{}
	limiter := newTestAttemptLimiter(func() time.Time { return time.Unix(120, 0) }, fake)
	_, err := limiter.Allow(context.Background(), AttemptInput{
		Action:   AttemptRefresh,
		ClientIP: "192.0.2.1",
		Subject:  "  Rt1.MixedCase  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.keys) != 1 || strings.Contains(fake.keys[0][1], "Rt1.MixedCase") {
		t.Fatalf("refresh key leaked or retained plaintext: %v", fake.keys)
	}
}

func TestAttemptLimiterRetryAfterAndWindowRotation(t *testing.T) {
	now := time.Unix(179, 0)
	fake := &attemptScriptFake{}
	limiter := newTestAttemptLimiter(func() time.Time { return now }, fake)
	limiter.runScript = func(_ context.Context, keys []string, _ int64) (int64, int64, error) {
		fake.keys = append(fake.keys, append([]string(nil), keys...))
		return 6, 6, nil
	}
	decision, err := limiter.Allow(context.Background(), AttemptInput{Action: AttemptLogin, ClientIP: "192.0.2.1", Subject: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.RetryAfter < time.Second || decision.RetryAfter > time.Minute {
		t.Fatalf("unexpected retry decision: %+v", decision)
	}
	if got := RetryAfterSeconds(AttemptLogin, decision.RetryAfter); got < 1 || got > 60 {
		t.Fatalf("Retry-After seconds=%d", got)
	}

	firstKey := fake.keys[0][0]
	now = time.Unix(240, 0)
	if _, err := limiter.Allow(context.Background(), AttemptInput{Action: AttemptLogin, ClientIP: "192.0.2.1", Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	if firstKey == fake.keys[1][0] {
		t.Fatalf("window rotation reused key %q", firstKey)
	}
}

func TestAttemptLimiterUsesOneAtomicScriptCallAndWindowTTL(t *testing.T) {
	fake := &attemptScriptFake{}
	limiter := newTestAttemptLimiter(func() time.Time { return time.Unix(120, 0) }, fake)
	if _, err := limiter.Allow(context.Background(), AttemptInput{Action: AttemptRegister, ClientIP: "192.0.2.1", Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.keys) != 1 || len(fake.keys[0]) != 2 {
		t.Fatalf("script calls=%v, want one call with two keys", fake.keys)
	}
	if len(fake.ttls) != 1 || fake.ttls[0] != 605 {
		t.Fatalf("script TTL=%v, want [605]", fake.ttls)
	}
}

func TestAttemptLimiterRejectsUnsupportedActionAndInvalidIP(t *testing.T) {
	fake := &attemptScriptFake{}
	limiter := newTestAttemptLimiter(time.Now, fake)
	if _, err := limiter.Allow(context.Background(), AttemptInput{Action: "logout", ClientIP: "192.0.2.1"}); err == nil {
		t.Fatal("unsupported action unexpectedly succeeded")
	}
	if _, err := limiter.Allow(context.Background(), AttemptInput{Action: AttemptLogin, ClientIP: "not-an-ip"}); err == nil {
		t.Fatal("invalid IP unexpectedly succeeded")
	}
}

func TestNewRedisAttemptLimiterRequiresRedisClient(t *testing.T) {
	if _, err := NewRedisAttemptLimiter(nil); err == nil {
		t.Fatal("nil Redis client unexpectedly succeeded")
	}
}

func TestAttemptLimiterFailsClosedOnRedisError(t *testing.T) {
	fake := &attemptScriptFake{err: errors.New("redis unavailable")}
	limiter := newTestAttemptLimiter(time.Now, fake)
	decision, err := limiter.Allow(context.Background(), AttemptInput{Action: AttemptLogin, ClientIP: "192.0.2.1", Subject: "alice"})
	if err == nil || decision.Allowed {
		t.Fatalf("decision=%+v err=%v, want fail closed", decision, err)
	}
}
