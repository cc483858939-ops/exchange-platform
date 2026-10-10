package likes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func (s *Store) ClaimBehaviorDirty(ctx context.Context, batch int, lease time.Duration) ([]BehaviorClaim, error) {
	started := time.Now()
	defer func() { metrics.ObserveLikeClaimDuration("behavior", time.Since(started)) }()
	if s == nil || s.client == nil {
		metrics.RecordLikeQueueOperation("behavior", "claim", "error")
		return nil, fmt.Errorf("redis is not initialized")
	}
	if batch <= 0 {
		batch = config.MaxLikeBehaviorBatchSize
	}
	batch = min(batch, config.MaxLikeBehaviorBatchSize)
	if lease <= 0 {
		lease = 30 * time.Second
	}
	prefix := uuid.NewString()
	value, err := behaviorClaimScript.Run(
		s.client.WithContext(ctx),
		[]string{BehaviorDirtyKey, BehaviorProcessingKey, BehaviorClaimsKey},
		batch,
		time.Now().Add(lease).UnixMilli(),
		prefix,
	).Result()
	if err != nil {
		metrics.RecordLikeQueueOperation("behavior", "claim", "error")
		return nil, mapScriptError(err)
	}
	claims, err := parseBehaviorClaimReply(value, batch)
	if err != nil {
		metrics.RecordLikeQueueOperation("behavior", "claim", "error")
		return nil, err
	}
	if len(claims) == 0 {
		metrics.RecordLikeQueueOperation("behavior", "claim", "empty")
	} else {
		metrics.RecordLikeQueueOperation("behavior", "claim", "success")
	}
	return claims, nil
}

func parseBehaviorClaimReply(value interface{}, batch int) ([]BehaviorClaim, error) {
	items, ok := value.([]interface{})
	if !ok || len(items)%2 != 0 || len(items) > batch*2 {
		return nil, fmt.Errorf("unexpected behavior claim response %T", value)
	}
	claims := make([]BehaviorClaim, 0, len(items)/2)
	seen := make(map[string]struct{}, len(items)/2)
	for i := 0; i < len(items); i += 2 {
		pair, pairOK := redisBulkString(items[i])
		claimID, claimOK := redisBulkString(items[i+1])
		if !pairOK || pair == "" || !claimOK || claimID == "" {
			return nil, fmt.Errorf("malformed behavior claim response at index %d", i)
		}
		if _, duplicate := seen[pair]; duplicate {
			return nil, fmt.Errorf("duplicate behavior pair in claim response: %q", pair)
		}
		seen[pair] = struct{}{}
		claims = append(claims, BehaviorClaim{Pair: pair, ClaimID: claimID})
	}
	return claims, nil
}

func (s *Store) LoadBehaviorDeliveries(ctx context.Context, claims []BehaviorClaim) ([]BehaviorDelivery, error) {
	deliveries, invalid, err := s.LoadBehaviorDeliveriesWithIssues(ctx, claims)
	if err != nil {
		return nil, err
	}
	if len(invalid) != 0 {
		return nil, fmt.Errorf("%d invalid behavior states", len(invalid))
	}
	return deliveries, nil
}

// LoadBehaviorDeliveriesWithIssues preserves healthy Pair progress when a
// separate Pair has malformed or missing state. Invalid claims remain owned
// until the relay requeues them; this method never deletes Behavior State.
func (s *Store) LoadBehaviorDeliveriesWithIssues(ctx context.Context, claims []BehaviorClaim) ([]BehaviorDelivery, []BehaviorClaim, error) {
	if len(claims) == 0 {
		return nil, nil, nil
	}
	if len(claims) > config.MaxLikeBehaviorBatchSize {
		return nil, nil, fmt.Errorf("behavior delivery batch exceeds %d", config.MaxLikeBehaviorBatchSize)
	}
	fields := make([]string, 0, len(claims))
	for _, claim := range claims {
		fields = append(fields, claim.Pair)
	}
	values, err := s.client.WithContext(ctx).HMGet(BehaviorStateKey, fields...).Result()
	if err != nil {
		return nil, nil, err
	}
	if len(values) != len(claims) {
		return nil, nil, fmt.Errorf("behavior state response has %d values for %d claims", len(values), len(claims))
	}
	deliveries := make([]BehaviorDelivery, 0, len(claims))
	invalid := make([]BehaviorClaim, 0)
	var firstErr error
	for i, claim := range claims {
		if values[i] == nil {
			invalid = append(invalid, claim)
			if firstErr == nil {
				firstErr = fmt.Errorf("behavior state missing for %q", claim.Pair)
			}
			continue
		}
		userID, postID, err := parseBehaviorPair(claim.Pair)
		if err != nil {
			invalid = append(invalid, claim)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		liked, version, occurredAt, err := parseBehaviorState(asString(values[i]))
		if err != nil {
			invalid = append(invalid, claim)
			if firstErr == nil {
				firstErr = fmt.Errorf("behavior state %q: %w", claim.Pair, err)
			}
			continue
		}
		deliveries = append(deliveries, BehaviorDelivery{
			Claim: claim, UserID: userID, PostID: postID,
			Liked: liked, Version: version, OccurredAt: occurredAt,
		})
	}
	if firstErr != nil {
		return deliveries, invalid, fmt.Errorf("%d invalid behavior states: %w", len(invalid), firstErr)
	}
	return deliveries, nil, nil
}

func (s *Store) AckBehaviorDeliveries(ctx context.Context, deliveries []BehaviorDelivery) (int, error) {
	if len(deliveries) == 0 {
		return 0, nil
	}
	if len(deliveries) > config.MaxLikeBehaviorBatchSize {
		return 0, fmt.Errorf("behavior ACK batch exceeds %d", config.MaxLikeBehaviorBatchSize)
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	commands := make([]*redis.Cmd, 0, len(deliveries))
	for _, delivery := range deliveries {
		commands = append(commands, behaviorAckScript.Eval(
			pipe,
			[]string{BehaviorDirtyKey, BehaviorStateKey, BehaviorProcessingKey, BehaviorClaimsKey},
			delivery.Claim.Pair,
			delivery.Claim.ClaimID,
			delivery.Version,
		))
	}
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		metrics.RecordLikeQueueOperation("behavior", "ack", "error")
		return 0, err
	}
	acked := 0
	for _, command := range commands {
		value, err := command.Int64()
		if err != nil {
			metrics.RecordLikeQueueOperation("behavior", "ack", "error")
			return acked, err
		}
		if value == 1 {
			acked++
		}
	}
	metrics.RecordLikeQueueOperation("behavior", "ack", "success")
	return acked, nil
}

func (s *Store) RequeueBehaviorClaims(ctx context.Context, claims []BehaviorClaim) error {
	if len(claims) == 0 {
		return nil
	}
	if len(claims) > config.MaxLikeBehaviorBatchSize {
		return fmt.Errorf("behavior requeue batch exceeds %d", config.MaxLikeBehaviorBatchSize)
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	for _, claim := range claims {
		behaviorRequeueScript.Eval(
			pipe,
			[]string{BehaviorDirtyKey, BehaviorStateKey, BehaviorProcessingKey, BehaviorClaimsKey},
			claim.Pair,
			claim.ClaimID,
		)
	}
	_, err := pipe.ExecContext(ctx)
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		metrics.RecordLikeQueueOperation("behavior", "requeue", "error")
	} else {
		metrics.RecordLikeQueueOperation("behavior", "requeue", "success")
	}
	return err
}

func (s *Store) ReapBehaviorExpired(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		batch = config.MaxLikeBehaviorBatchSize
	}
	batch = min(batch, config.MaxLikeBehaviorBatchSize)
	value, err := behaviorReapExpiredScript.Run(
		s.client.WithContext(ctx),
		[]string{BehaviorDirtyKey, BehaviorStateKey, BehaviorProcessingKey, BehaviorClaimsKey},
		time.Now().UnixMilli(),
		batch,
	).Int64()
	if err != nil {
		metrics.RecordLikeQueueOperation("behavior", "reap", "error")
	} else if value > 0 {
		metrics.RecordLikeQueueOperation("behavior", "reap", "success")
	} else {
		metrics.RecordLikeQueueOperation("behavior", "reap", "empty")
	}
	return value, mapScriptError(err)
}

func parseBehaviorPair(pair string) (uint, uint, error) {
	parts := strings.Split(pair, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid behavior pair %q", pair)
	}
	userID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || userID == 0 {
		return 0, 0, fmt.Errorf("invalid behavior user id in %q", pair)
	}
	postID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || postID == 0 {
		return 0, 0, fmt.Errorf("invalid behavior post id in %q", pair)
	}
	return uint(userID), uint(postID), nil
}

func parseBehaviorState(value string) (bool, int64, time.Time, error) {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 {
		return false, 0, time.Time{}, fmt.Errorf("invalid encoded state")
	}
	if parts[0] != "0" && parts[0] != "1" {
		return false, 0, time.Time{}, fmt.Errorf("invalid liked flag %q", parts[0])
	}
	version, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || version <= 0 {
		return false, 0, time.Time{}, fmt.Errorf("invalid version %q", parts[1])
	}
	occurredAtMicros, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || occurredAtMicros <= 0 {
		return false, 0, time.Time{}, fmt.Errorf("invalid occurred_at unix micros %q", parts[2])
	}
	return parts[0] == "1", version, time.UnixMicro(occurredAtMicros).UTC(), nil
}
