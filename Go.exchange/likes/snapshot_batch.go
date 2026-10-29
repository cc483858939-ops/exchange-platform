package likes

import (
	"context"
	"errors"
	"strings"

	"Go.exchange/config"
	"Go.exchange/metrics"
	"github.com/go-redis/redis/v7"
)

// SnapshotRead keeps failures local to one claimed Post, as LoadSnapshot does.
type SnapshotRead struct {
	Claim    SnapshotClaim
	Snapshot Snapshot
	Err      error
}

type SnapshotClaimResult struct {
	Claim   SnapshotClaim
	Applied bool
	Err     error
}

func (s *Store) validateSnapshotBatch(ctx context.Context, claims []SnapshotClaim) error {
	if s == nil || s.client == nil {
		return errors.New("redis is not initialized")
	}
	if ctx == nil {
		return errors.New("snapshot batch context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(claims) > config.MaxLikeSnapshotBatchSize {
		return errors.New("snapshot batch exceeds claim limit")
	}
	for _, claim := range claims {
		if claim.PostID == 0 || claim.ClaimID == "" {
			return errors.New("invalid snapshot claim")
		}
	}
	return nil
}

func (s *Store) LoadSnapshots(ctx context.Context, claims []SnapshotClaim) ([]SnapshotRead, error) {
	if err := s.validateSnapshotBatch(ctx, claims); err != nil {
		return nil, err
	}
	ids := make([]uint, len(claims))
	for i, claim := range claims {
		ids[i] = claim.PostID
	}
	reads := make([]SnapshotRead, len(claims))
	// Bound each MGET's request/response size. Each Post's Count/Version still
	// comes from one atomic read; different Posts need not share a read instant.
	const chunkSize = 32
	for start := 0; start < len(ids); start += chunkSize {
		end := min(start+chunkSize, len(ids))
		states, err := s.readAggregateStates(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		for offset, state := range states {
			i := start + offset
			claim := claims[i]
			reads[i] = SnapshotRead{Claim: claim, Snapshot: Snapshot{PostID: claim.PostID, Count: state.state.Count, Version: state.state.Version}, Err: state.err}
		}
	}
	return reads, nil
}

func (s *Store) AckClaims(ctx context.Context, claims []SnapshotClaim) ([]SnapshotClaimResult, error) {
	return s.resolveSnapshotClaims(ctx, claims, ackClaimScript, []string{ProcessingKey, ClaimsKey}, "ack")
}

func (s *Store) RequeueClaims(ctx context.Context, claims []SnapshotClaim) ([]SnapshotClaimResult, error) {
	return s.resolveSnapshotClaims(ctx, claims, requeueClaimScript, []string{DirtyKey, ProcessingKey, ClaimsKey}, "requeue")
}

// Pipeline the existing token-checked scripts, without changing their atomicity
// or touching Dirty during ACK. Retry only NOSCRIPT, never an ambiguous write.
func (s *Store) resolveSnapshotClaims(ctx context.Context, claims []SnapshotClaim, script *redis.Script, keys []string, operation string) ([]SnapshotClaimResult, error) {
	if err := s.validateSnapshotBatch(ctx, claims); err != nil {
		return nil, err
	}
	commands := make([]*redis.Cmd, len(claims))
	if len(claims) == 0 {
		return []SnapshotClaimResult{}, nil
	}
	client := s.client.WithContext(ctx)
	pipe := client.Pipeline()
	defer pipe.Close()
	for i, claim := range claims {
		commands[i] = pipe.EvalSha(script.Hash(), keys, claim.PostID, claim.ClaimID)
	}
	execute := func() {
		_, err := pipe.ExecContext(ctx)
		if err != nil {
			for _, command := range commands {
				if command.Err() == nil && command.Val() == nil {
					command.SetErr(err)
				}
			}
		}
	}
	execute() // Inspect every command, including partial failures.
	missing := make([]int, 0)
	for i, command := range commands {
		if err := command.Err(); err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		if err := script.Load(client).Err(); err != nil {
			return nil, err
		}
		for _, i := range missing {
			commands[i] = pipe.EvalSha(script.Hash(), keys, claims[i].PostID, claims[i].ClaimID)
		}
		execute()
	}
	results := make([]SnapshotClaimResult, len(claims))
	for i, command := range commands {
		value, err := command.Int64()
		results[i] = SnapshotClaimResult{Claim: claims[i], Applied: value == 1 && err == nil, Err: mapScriptError(err)}
		outcome := "stale"
		if err != nil {
			outcome = "error"
		} else if value == 1 {
			outcome = "success"
		}
		metrics.RecordLikeQueueOperation("snapshot", operation, outcome)
	}
	return results, nil
}
