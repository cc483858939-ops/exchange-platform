package likes

import (
	"context"
	"errors"
	"time"

	"Go.exchange/config"

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
// Reactivation is rejected until SPEC-02 can remove stale User -> Posts links.
func (s *Store) InitializeFrom(ctx context.Context, postID uint, reactivation bool, load func(context.Context) (FullState, error)) (bool, error) {
	if reactivation {
		// User -> Posts has no reverse index, so Post reactivation cannot prove
		// or clear stale membership until SPEC-02 owns the lifecycle transition.
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
