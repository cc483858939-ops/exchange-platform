package likes

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

// BeginRebuild reserves a short-lived write token BEFORE the caller reads SQL.
// Deletion revokes it atomically; missing tokens always fail closed, including
// after expiry, eviction or a Redis restart. Tokens must never be reused.
func (s *Store) BeginRebuild(ctx context.Context, postID uint) (string, error) {
	tokens, unavailable, err := s.BeginRebuildMany(ctx, []uint{postID})
	if err != nil {
		return "", err
	}
	if err := unavailable[postID]; err != nil {
		return "", err
	}
	return tokens[postID], nil
}

// BeginRebuildMany keeps batch recovery's Redis and SQL round trips bounded.
func (s *Store) BeginRebuildMany(ctx context.Context, postIDs []uint) (map[uint]string, map[uint]error, error) {
	return s.beginRebuildMany(ctx, postIDs, false)
}

// InitializeFrom reads current SQL only after the rebuild token exists.
// Same-ID reactivation is fail-closed because this model cannot prove every
// stale User -> Posts link is gone without reintroducing a reverse index.
func (s *Store) InitializeFrom(ctx context.Context, postID uint, reactivation bool, load func(context.Context) (FullState, error)) (bool, error) {
	if reactivation {
		// User -> Posts has no reverse index, so reactivation cannot prove or
		// clear stale membership safely.
		return false, ErrLikeRecoveryUnsafe
	}
	tokens, unavailable, err := s.beginRebuildMany(ctx, []uint{postID}, reactivation)
	if err != nil {
		return false, err
	}
	if err := unavailable[postID]; err != nil {
		return false, err
	}
	defer s.ReleaseRebuildMany(ctx, tokens)
	baseline, err := load(ctx)
	if err != nil {
		return false, err
	}
	return s.Initialize(ctx, postID, baseline.Count, baseline.Version, tokens[postID])
}

const newPostInitializationAttempts = 3

var newPostInitializationRetryDelays = [...]time.Duration{50 * time.Millisecond, 100 * time.Millisecond}

// InitializeNewPostFrom is only for a trusted new Post creation context. It
// retries transient infrastructure failures a bounded number of times while
// retaining the same rebuild token; safety and lifecycle errors fail closed.
func (s *Store) InitializeNewPostFrom(ctx context.Context, postID uint, load func(context.Context) (FullState, error)) (bool, error) {
	if ctx == nil {
		return false, errors.New("new Post Like initialization context is nil")
	}
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || load == nil {
		return false, ErrLikeRecoveryUnsafe
	}

	token := uuid.NewString()
	acquired := false
	acquire := func(attemptCtx context.Context, ownerToken string) error {
		err := beginRebuildScript.Run(
			s.client.WithContext(attemptCtx),
			[]string{ReadyKey(postID), RebuildTokenKey(postID)},
			ownerToken,
			config.LikeRebuildTokenTTL().Milliseconds(),
			0,
		).Err()
		if err == nil {
			acquired = true
		}
		return mapScriptError(err)
	}
	initialize := func(attemptCtx context.Context, ownerToken string) (bool, error) {
		baseline, err := load(attemptCtx)
		if err != nil {
			return false, err
		}
		if baseline.Count != 0 || baseline.Version != 0 {
			return false, ErrLikeRecoveryUnsafe
		}
		created, err := s.Initialize(attemptCtx, postID, baseline.Count, baseline.Version, ownerToken)
		if err == nil {
			return created, nil
		}
		if errors.Is(err, ErrLikeRecoveryFenceLost) {
			if _, stateErr := s.Get(attemptCtx, 0, postID); stateErr == nil {
				return false, nil
			}
		}
		return false, err
	}
	created, err := runNewPostInitializationWithRetry(ctx, token, acquire, initialize)
	if acquired {
		s.ReleaseRebuildMany(ctx, map[uint]string{postID: token})
	}
	return created, err
}

func runNewPostInitializationWithRetry(
	ctx context.Context,
	token string,
	acquire func(context.Context, string) error,
	initialize func(context.Context, string) (bool, error),
) (bool, error) {
	if ctx == nil || token == "" || acquire == nil || initialize == nil {
		return false, ErrLikeRecoveryUnsafe
	}
	var lastErr error
	for attempt := 0; attempt < newPostInitializationAttempts; attempt++ {
		if err := acquire(ctx, token); err != nil {
			lastErr = err
			if !isRetryableLikeInitializationError(err) || attempt+1 == newPostInitializationAttempts {
				return false, err
			}
			if err := waitNewPostInitializationRetry(ctx, attempt); err != nil {
				return false, errors.Join(lastErr, err)
			}
			continue
		}
		created, err := initialize(ctx, token)
		if err == nil {
			return created, nil
		}
		lastErr = err
		if !isRetryableLikeInitializationError(err) || attempt+1 == newPostInitializationAttempts {
			return false, err
		}
		if err := waitNewPostInitializationRetry(ctx, attempt); err != nil {
			return false, errors.Join(lastErr, err)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("new Post Like initialization exhausted retries")
	}
	return false, lastErr
}

func waitNewPostInitializationRetry(ctx context.Context, attempt int) error {
	metrics.RecordLikeLifecycleEvent("post_init_retry")
	delay := newPostInitializationRetryDelays[len(newPostInitializationRetryDelays)-1]
	if attempt >= 0 && attempt < len(newPostInitializationRetryDelays) {
		delay = newPostInitializationRetryDelays[attempt]
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableLikeInitializationError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	var redisErr redis.Error
	if errors.As(err, &redisErr) {
		message := strings.TrimSpace(redisErr.Error())
		for _, prefix := range []string{"LOADING ", "TRYAGAIN ", "READONLY ", "CLUSTERDOWN ", "MASTERDOWN "} {
			if strings.HasPrefix(message, prefix) {
				return true
			}
		}
	}
	return false
}

func (s *Store) beginRebuildMany(ctx context.Context, postIDs []uint, reactivation bool) (map[uint]string, map[uint]error, error) {
	if s == nil || s.client == nil {
		return nil, nil, errors.New("redis is not initialized")
	}
	tokens := make(map[uint]string, len(postIDs))
	unavailable := make(map[uint]error)
	commands := make(map[uint]*redis.Cmd, len(postIDs))
	pipe := s.client.WithContext(ctx).Pipeline()
	defer pipe.Close()
	for _, id := range postIDs {
		if id == 0 {
			return nil, nil, errors.New("invalid post id")
		}
		if _, exists := tokens[id]; exists {
			continue
		}
		token := uuid.NewString()
		allowDeleted := 0
		if reactivation {
			token = "reactivate:" + token
			allowDeleted = 1
		}
		tokens[id] = token
		// EVAL works on a fresh server without an EVALSHA pipeline retry.
		commands[id] = beginRebuildScript.Eval(pipe, []string{ReadyKey(id), RebuildTokenKey(id)}, token, config.LikeRebuildTokenTTL().Milliseconds(), allowDeleted)
	}
	_, execErr := pipe.Exec()
	for id, command := range commands {
		if err := mapScriptError(command.Err()); err != nil {
			delete(tokens, id)
			if errors.Is(err, ErrPostLikeUnavailable) || errors.Is(err, ErrLikeRecoveryFenceLost) {
				unavailable[id] = err
				continue
			}
			// Successfully acquired tokens on a failed batch expire by themselves.
			return nil, nil, err
		}
	}
	if execErr != nil && len(unavailable) == 0 {
		return nil, nil, execErr
	}
	return tokens, unavailable, nil
}

// ReleaseRebuild removes only this operation's token, never a newer owner's.
func (s *Store) ReleaseRebuild(ctx context.Context, postID uint, token string) error {
	_, err := releaseRebuildScript.Run(s.client.WithContext(ctx), []string{RebuildTokenKey(postID)}, token).Result()
	return err
}

func (s *Store) ReleaseRebuildMany(ctx context.Context, tokens map[uint]string) {
	// The request may already have timed out. Bound best-effort release separately;
	// expiry remains the fallback if Redis is unavailable.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	pipe := s.client.WithContext(releaseCtx).Pipeline()
	defer pipe.Close()
	for id, token := range tokens {
		releaseRebuildScript.Eval(pipe, []string{RebuildTokenKey(id)}, token)
	}
	_, _ = pipe.Exec()
}
