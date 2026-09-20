package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
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
