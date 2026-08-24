package auth

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestRedisAttemptLimiterIntegration(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("REDIS_TEST_ADDR"))
	if address == "" {
		t.Skip("SKIPPED — REDIS_TEST_ADDR unavailable")
	}
	database := 0
	if raw := strings.TrimSpace(os.Getenv("REDIS_TEST_DB")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			t.Fatalf("invalid REDIS_TEST_DB: %q", raw)
		}
		database = parsed
	}

	newClient := func() *redis.Client {
		return redis.NewClient(&redis.Options{
			Addr:     address,
			Password: os.Getenv("REDIS_TEST_PASSWORD"),
			DB:       database,
		})
	}
	clientA := newClient()
	clientB := newClient()
	t.Cleanup(func() { _ = clientA.Close() })
	t.Cleanup(func() { _ = clientB.Close() })
	if _, err := clientA.Ping().Result(); err != nil {
		t.Fatalf("Redis ping: %v", err)
	}

	now := time.Now()
	limiterA, err := NewRedisAttemptLimiter(clientA)
	if err != nil {
		t.Fatal(err)
	}
	limiterB, err := NewRedisAttemptLimiter(clientB)
	if err != nil {
		t.Fatal(err)
	}
	limiterA.(*RedisAttemptLimiter).now = func() time.Time { return now }
	limiterB.(*RedisAttemptLimiter).now = func() time.Time { return now }

	loginIP := "198.18.0.1"
	loginSubject := "rate-login-" + uuid.NewString()
	loginInput := AttemptInput{Action: AttemptLogin, ClientIP: loginIP, Subject: loginSubject}
	loginKeys := keysForAttempt(now, loginInput)
	registerIP := "198.18.0.2"
	registerSubjects := make([]string, 12)
	registerKeys := make([][]string, 0, len(registerSubjects))
	for index := range registerSubjects {
		registerSubjects[index] = fmt.Sprintf("rate-register-%s-%d", uuid.NewString(), index)
		registerKeys = append(registerKeys, keysForAttempt(now, AttemptInput{
			Action:   AttemptRegister,
			ClientIP: registerIP,
			Subject:  registerSubjects[index],
		}))
	}
	refreshToken := "rt1.integration-" + uuid.NewString()
	refreshInput := AttemptInput{Action: AttemptRefresh, ClientIP: "198.18.0.3", Subject: refreshToken}
	refreshKeys := keysForAttempt(now, refreshInput)
	allKeys := append([]string{}, loginKeys...)
	for _, keys := range registerKeys {
		allKeys = append(allKeys, keys...)
	}
	allKeys = append(allKeys, refreshKeys...)
	t.Cleanup(func() { _, _ = clientA.Del(allKeys...).Result() })

	var allowed int32
	errorsCh := make(chan error, 100)
	var waitGroup sync.WaitGroup
	for index := 0; index < 100; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			limiter := limiterA
			if index%2 == 1 {
				limiter = limiterB
			}
			decision, err := limiter.Allow(context.Background(), loginInput)
			if err != nil {
				errorsCh <- err
				return
			}
			if decision.Allowed {
				atomic.AddInt32(&allowed, 1)
			}
		}(index)
	}
	waitGroup.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	if allowed != 5 {
		t.Fatalf("concurrent login allowed=%d, want 5", allowed)
	}

	registerAllowed := 0
	for _, subject := range registerSubjects {
		decision, err := limiterA.Allow(context.Background(), AttemptInput{
			Action:   AttemptRegister,
			ClientIP: registerIP,
			Subject:  subject,
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed {
			registerAllowed++
		}
	}
	if registerAllowed != 10 {
		t.Fatalf("register IP bucket allowed=%d, want 10", registerAllowed)
	}
	for _, key := range registerKeys[0] {
		exists, err := clientA.Exists(key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists != 1 {
			t.Fatalf("expected Redis key %q to exist", key)
		}
		ttl, err := clientA.TTL(key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if ttl <= 0 || ttl > 605*time.Second {
			t.Fatalf("key %q TTL=%s, want (0, 605s]", key, ttl)
		}
	}

	if _, err := limiterA.Allow(context.Background(), refreshInput); err != nil {
		t.Fatal(err)
	}
	for _, key := range append(refreshKeys, loginKeys...) {
		if strings.Contains(key, loginIP) || strings.Contains(key, loginSubject) || strings.Contains(key, refreshToken) {
			t.Fatalf("Redis key contains sensitive plaintext: %q", key)
		}
	}

	closedClient := newClient()
	closedLimiter, err := NewRedisAttemptLimiter(closedClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := closedClient.Close(); err != nil {
		t.Fatal(err)
	}
	decision, err := closedLimiter.Allow(context.Background(), AttemptInput{
		Action:   AttemptLogin,
		ClientIP: "198.18.0.4",
		Subject:  "closed-client-" + uuid.NewString(),
	})
	if err == nil || decision.Allowed {
		t.Fatalf("closed Redis decision=%+v err=%v, want fail closed", decision, err)
	}
}

func keysForAttempt(now time.Time, input AttemptInput) []string {
	policy := attemptPolicies[input.Action]
	windowID := now.Unix() / int64(policy.window/time.Second)
	clientIP, _ := normalizeClientIP(input.ClientIP)
	subject := normalizeAttemptSubject(input.Action, input.Subject)
	return []string{
		attemptKey(input.Action, "ip", hashAttemptValue(clientIP), windowID),
		attemptKey(input.Action, "subject", hashAttemptValue(subject), windowID),
	}
}
