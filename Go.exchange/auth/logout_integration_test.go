package auth

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRedisLogoutRejectsForeignSecretAndRevokesRecoveryIntegration(t *testing.T) {
	manager, client, original, sessionID, _ := redisRecoveryFixture(t)
	ctx := context.Background()
	foreign, _ := newRefreshSecret()
	if err := manager.RevokeRefresh(ctx, formatRefreshToken(sessionID, foreign)); err == nil {
		t.Fatal("foreign secret accepted")
	}
	requestID := uuid.NewString()
	next, err := manager.RotateRefresh(ctx, original.RefreshToken, requestID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := manager.RevokeRefresh(ctx, original.RefreshToken); err != nil {
			t.Fatal(err)
		}
	}
	if client.Exists(refreshSessionKey(sessionID)).Val() != 0 {
		t.Fatal("session retained")
	}
	for _, attempt := range []struct{ token, requestID string }{{original.RefreshToken, requestID}, {next.RefreshToken, uuid.NewString()}} {
		if _, err := manager.RotateRefresh(ctx, attempt.token, attempt.requestID); err == nil {
			t.Fatal("revoked family refreshed")
		}
	}
}

func TestRedisLogoutRacingRotationIntegration(t *testing.T) {
	for i := 0; i < 12; i++ {
		manager, client, original, sessionID, _ := redisRecoveryFixture(t)
		ctx := context.Background()
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, _ = manager.RotateRefresh(ctx, original.RefreshToken, uuid.NewString())
		}()
		go func() {
			defer wait.Done()
			<-start
			if err := manager.RevokeRefresh(ctx, original.RefreshToken); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wait.Wait()
		if client.Exists(refreshSessionKey(sessionID)).Val() != 0 {
			t.Fatal("concurrent rotation kept family alive")
		}
	}
}
