package controllers

import (
	"context"
	"sync"

	"Go.exchange/config"
	"Go.exchange/likes"
)

type postLikeRecoveryStore interface {
	BeginRebuildMany(context.Context, []uint) (map[uint]string, map[uint]error, error)
	ReleaseRebuildMany(context.Context, map[uint]string)
	GetMany(context.Context, uint, []uint) (map[uint]likes.State, []uint, error)
	RegistryContainsMany(context.Context, []uint) (map[uint]bool, error)
	GetRecoverableVersions(context.Context, []uint) (map[uint]int64, error)
	Recover(context.Context, uint, likes.FullState, likes.RecoveryFence) (bool, error)
}

type postLikeBaselineLoader func(context.Context, []uint) (map[uint]postLikeBaseline, error)

type postLikeRecoveryFlight struct {
	done chan struct{}
	err  error // Published before done is closed.
}

// Claim each post before any baseline loading. Unlike a whole-batch key,
// per-post flights also merge partially overlapping batches and single reads.
// Newly claimed posts retain one batched Redis/SQL load.
type postLikeRecoveryFlights struct {
	mu       sync.Mutex
	inflight map[uint]*postLikeRecoveryFlight
}

func (g *postLikeRecoveryFlights) recover(ctx context.Context, store postLikeRecoveryStore, postIDs []uint, load postLikeBaselineLoader) (map[uint]error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := uniquePostIDs(postIDs)
	calls := make(map[uint]*postLikeRecoveryFlight, len(ids))
	owned := make([]uint, 0, len(ids))
	g.mu.Lock()
	if g.inflight == nil {
		g.inflight = make(map[uint]*postLikeRecoveryFlight)
	}
	for _, id := range ids {
		call := g.inflight[id]
		if call == nil {
			call = &postLikeRecoveryFlight{done: make(chan struct{})}
			g.inflight[id] = call
			owned = append(owned, id)
		}
		calls[id] = call
	}
	g.mu.Unlock()

	if len(owned) > 0 {
		// One caller leaving must not abort recovery for other callers. The
		// existing API budget bounds all shared Redis/SQL work, including when
		// every caller has left; each caller still waits on its own context.
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.APIRequestTimeout())
		go func() {
			defer cancel()
			results, err := recoverPostLikeBatch(workCtx, store, owned, load)
			g.mu.Lock()
			defer g.mu.Unlock()
			for _, id := range owned {
				call := calls[id]
				postErr, completed := results[id]
				call.err = postErr
				if !completed && err != nil {
					call.err = err
				}
				delete(g.inflight, id)
				close(call.done)
			}
		}()
	}
	results := make(map[uint]error, len(ids))
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-calls[id].done:
			results[id] = calls[id].err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func recoverPostLikeBatch(ctx context.Context, store postLikeRecoveryStore, ids []uint, load postLikeBaselineLoader) (map[uint]error, error) {
	results := make(map[uint]error, len(ids))
	// A previous recovery may have completed after the serving read missed.
	states, missing, err := store.GetMany(ctx, 0, ids)
	if err != nil || len(missing) == 0 {
		return results, err
	}
	for id := range states {
		results[id] = nil
	}
	tokens, unavailable, err := store.BeginRebuildMany(ctx, missing)
	if err != nil {
		return results, err
	}
	defer store.ReleaseRebuildMany(ctx, tokens)
	acquired := make([]uint, 0, len(tokens))
	for _, id := range missing {
		if err := unavailable[id]; err != nil {
			results[id] = err
		} else if _, ok := tokens[id]; ok {
			acquired = append(acquired, id)
		}
	}
	missing = acquired
	if len(missing) == 0 {
		return results, nil
	}
	registered, err := store.RegistryContainsMany(ctx, missing)
	if err != nil {
		return results, err
	}
	markers, err := store.GetRecoverableVersions(ctx, missing)
	if err != nil {
		return results, err
	}
	baselines, err := load(ctx, missing)
	if err != nil {
		return results, err
	}
	for _, id := range missing {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		baseline, active := baselines[id]
		if !active {
			results[id] = likes.ErrPostLikeUnavailable
			continue
		}
		var markerPtr *int64
		if marker, ok := markers[id]; ok {
			markerPtr = &marker
		}
		fence, err := classifyPostLikeRecovery(registered[id], markerPtr, baseline)
		if err != nil {
			results[id] = err
			continue
		}
		fence.RebuildToken = tokens[id]
		_, err = store.Recover(ctx, id, likes.FullState{Count: baseline.Count, Version: baseline.Version}, fence)
		results[id] = err
		if err != nil && !isPostLikeBatchUnavailableError(err) {
			return results, err
		}
	}
	return results, nil
}
