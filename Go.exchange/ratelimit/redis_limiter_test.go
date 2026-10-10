package ratelimit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

func TestRedisLimiterBuildsAllowedDecisionFromAtomicResult(t *testing.T) {
	action := Action("test_allowed")
	withTestPolicy(t, action, Policy{Action: action, Rules: []Rule{{Limit: 5, Window: time.Minute}}})
	now := time.Unix(1_700_000_012, 250_000_000).UTC()
	limiter := &RedisLimiter{
		now: func() time.Time { return now },
		runScript: func(_ context.Context, keys []string, args []interface{}) (int64, int64, int64, error) {
			if len(keys) != 1 || keys[0] != "rl:v1:{user:123}:test_allowed:60:28333333" {
				t.Fatalf("unexpected keys: %#v", keys)
			}
			if len(args) != 3 || args[0] != int64(5) || args[1] != int64(28) || args[2] != int64(33) {
				t.Fatalf("unexpected script args: %#v", args)
			}
			return 1, 1, 4, nil
		},
	}

	decision, err := limiter.Allow(context.Background(), Input{Subject: "123", Action: action})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.Limit != 5 || decision.Remaining != 4 || decision.RetryAfter != 0 {
		t.Fatalf("unexpected allowed decision: %#v", decision)
	}
	wantReset := time.Unix(1_700_000_040, 0).UTC()
	if !decision.ResetAt.Equal(wantReset) {
		t.Fatalf("reset=%s, want %s", decision.ResetAt, wantReset)
	}
}

func TestRedisLimiterBuildsDeniedDecisionFromBlockingRule(t *testing.T) {
	action := Action("test_denied")
	withTestPolicy(t, action, Policy{Action: action, Rules: []Rule{
		{Limit: 5, Window: time.Minute},
		{Limit: 50, Window: time.Hour},
	}})
	now := time.Unix(1_700_000_012, 0).UTC()
	limiter := &RedisLimiter{
		now: func() time.Time { return now },
		runScript: func(_ context.Context, _ []string, _ []interface{}) (int64, int64, int64, error) {
			return 0, 1, 0, nil
		},
	}

	decision, err := limiter.Allow(context.Background(), Input{Subject: "123", Action: action})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Remaining != 0 || decision.Limit != 5 || decision.RetryAfter <= 0 {
		t.Fatalf("unexpected denied decision: %#v", decision)
	}
	if !decision.ResetAt.Equal(time.Unix(1_700_000_040, 0).UTC()) {
		t.Fatalf("unexpected denied reset: %s", decision.ResetAt)
	}
}

func TestRedisLimiterPropagatesScriptErrors(t *testing.T) {
	wantErr := errors.New("redis unavailable")
	limiter := &RedisLimiter{
		now: func() time.Time { return time.Unix(1_700_000_012, 0) },
		runScript: func(context.Context, []string, []interface{}) (int64, int64, int64, error) {
			return 0, 0, 0, wantErr
		},
	}
	_, err := limiter.Allow(context.Background(), Input{Subject: "123", Action: ActionRecommendations})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Allow error=%v, want %v", err, wantErr)
	}
}

func TestNewRedisLimiterRejectsInvalidPolicyAtConstruction(t *testing.T) {
	original := Policies[ActionPostCreate]
	invalid := original
	invalid.Rules = append([]Rule(nil), original.Rules...)
	invalid.Rules[0].Limit = 0
	Policies[ActionPostCreate] = invalid
	t.Cleanup(func() { Policies[ActionPostCreate] = original })

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	defer client.Close()
	_, err := NewRedisLimiter(client)
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewRedisLimiter error=%v, want ErrInvalidPolicy", err)
	}
}

func TestNewRedisLimiterConstructsWithoutRedisCall(t *testing.T) {
	clearLikeMutationRateEnvironment(t)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	defer client.Close()
	limiter, err := NewRedisLimiter(client)
	if err != nil {
		t.Fatalf("NewRedisLimiter error=%v", err)
	}
	if limiter == nil {
		t.Fatal("NewRedisLimiter returned a nil limiter")
	}
}

func TestNewRedisLimiterValidatesAndSnapshotsLikeMutationQuotas(t *testing.T) {
	t.Setenv("LIKE_MUTATION_RATE_10S", "17")
	t.Setenv("LIKE_MUTATION_RATE_1M", "180")
	t.Setenv("LIKE_MUTATION_RATE_24H", "5000")
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	defer client.Close()
	rawLimiter, err := NewRedisLimiter(client)
	if err != nil {
		t.Fatal(err)
	}
	limiter := rawLimiter.(*RedisLimiter)
	policy := limiter.policies[ActionLikeMutation]
	if len(policy.Rules) != 3 || policy.Rules[0] != (Rule{Limit: 17, Window: 10 * time.Second}) ||
		policy.Rules[1] != (Rule{Limit: 180, Window: time.Minute}) || policy.Rules[2] != (Rule{Limit: 5000, Window: 24 * time.Hour}) {
		t.Fatalf("configured Like mutation policy=%+v", policy)
	}
	previous := Policies[ActionLikeMutation]
	Policies[ActionLikeMutation] = Policy{Action: ActionLikeMutation, Rules: []Rule{{Limit: 1, Window: time.Second}}}
	t.Cleanup(func() { Policies[ActionLikeMutation] = previous })
	limiter.runScript = func(_ context.Context, _ []string, args []interface{}) (int64, int64, int64, error) {
		if args[0] != int64(17) || args[3] != int64(180) || args[6] != int64(5000) {
			t.Fatalf("Like mutation hot path did not use startup snapshot: %#v", args)
		}
		return 1, 1, 16, nil
	}
	if _, err := limiter.Allow(context.Background(), Input{Subject: "77", Action: ActionLikeMutation}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRedisLimiterRejectsInvalidLikeMutationQuotaAtStartup(t *testing.T) {
	clearLikeMutationRateEnvironment(t)
	t.Setenv("LIKE_MUTATION_RATE_24H", "1000001")
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	defer client.Close()
	if _, err := NewRedisLimiter(client); err == nil || !strings.Contains(err.Error(), "LIKE_MUTATION_RATE_24H") {
		t.Fatalf("NewRedisLimiter error=%v, want invalid 24h quota", err)
	}
}

func clearLikeMutationRateEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LIKE_MUTATION_RATE_10S", "LIKE_MUTATION_RATE_1M", "LIKE_MUTATION_RATE_24H"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFixedWindowResetAndKeyAreDeterministic(t *testing.T) {
	now := time.Unix(1_700_000_012, 0).UTC()
	if got, want := fixedWindowResetAt(now, time.Hour), time.Unix(1_700_002_800, 0).UTC(); !got.Equal(want) {
		t.Fatalf("hour reset=%s, want %s", got, want)
	}
	if got, want := rateLimitKey("123", ActionPostCreate, 60, 28_333_333), "rl:v1:{user:123}:post_create:60:28333333"; got != want {
		t.Fatalf("key=%q, want %q", got, want)
	}
}

func withTestPolicy(t *testing.T, action Action, policy Policy) {
	t.Helper()
	previous, existed := Policies[action]
	Policies[action] = policy
	t.Cleanup(func() {
		if existed {
			Policies[action] = previous
		} else {
			delete(Policies, action)
		}
	})
}
