package likes

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

type retryableRedisServerError string

func (e retryableRedisServerError) Error() string { return string(e) }

func (retryableRedisServerError) RedisError() {}

func TestNewPostInitializationRetriesTransientFailureWithSameToken(t *testing.T) {
	var acquiredTokens []string
	var initializedTokens []string
	initializeCalls := 0
	created, err := runNewPostInitializationWithRetry(
		context.Background(),
		"one-owner-token",
		func(_ context.Context, token string) error {
			acquiredTokens = append(acquiredTokens, token)
			return nil
		},
		func(_ context.Context, token string) (bool, error) {
			initializeCalls++
			initializedTokens = append(initializedTokens, token)
			if initializeCalls == 1 {
				return false, retryableRedisServerError("LOADING Redis is loading the dataset")
			}
			return true, nil
		},
	)
	if err != nil || !created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	if len(acquiredTokens) != 2 || len(initializedTokens) != 2 {
		t.Fatalf("acquire tokens=%v initialize tokens=%v", acquiredTokens, initializedTokens)
	}
	for _, token := range append(acquiredTokens, initializedTokens...) {
		if token != "one-owner-token" {
			t.Fatalf("retry changed rebuild owner token: %q", token)
		}
	}
}

func TestNewPostInitializationLongOutageIsBoundedAndDoesNotInitialize(t *testing.T) {
	acquireCalls, initializeCalls := 0, 0
	created, err := runNewPostInitializationWithRetry(
		context.Background(),
		"one-owner-token",
		func(context.Context, string) error {
			acquireCalls++
			return retryableRedisServerError("LOADING Redis is loading the dataset")
		},
		func(context.Context, string) (bool, error) {
			initializeCalls++
			return true, nil
		},
	)
	if created || err == nil {
		t.Fatalf("created=%t err=%v, want bounded failure", created, err)
	}
	if acquireCalls != newPostInitializationAttempts || initializeCalls != 0 {
		t.Fatalf("acquire calls=%d initialize calls=%d", acquireCalls, initializeCalls)
	}
}

func TestNewPostInitializationDoesNotRetrySafetyErrors(t *testing.T) {
	initializeCalls := 0
	created, err := runNewPostInitializationWithRetry(
		context.Background(),
		"one-owner-token",
		func(context.Context, string) error { return nil },
		func(context.Context, string) (bool, error) {
			initializeCalls++
			return false, ErrLikeRecoveryUnsafe
		},
	)
	if created || !errors.Is(err, ErrLikeRecoveryUnsafe) || initializeCalls != 1 {
		t.Fatalf("created=%t err=%v initialize calls=%d", created, err, initializeCalls)
	}
}

func TestNewPostInitializationRedisOutageNeverReachesBaselineOrReadyWrite(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, DialTimeout: 20 * time.Millisecond, ReadTimeout: 20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond, MaxRetries: 0,
	})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	loadCalls := 0
	created, err := NewStore(client).InitializeNewPostFrom(ctx, 42, func(context.Context) (FullState, error) {
		loadCalls++
		return FullState{}, nil
	})
	if created || err == nil || loadCalls != 0 {
		t.Fatalf("created=%t err=%v baseline loads=%d, want bounded Redis failure before initialization", created, err, loadCalls)
	}
}
