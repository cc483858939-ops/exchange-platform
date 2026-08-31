package likes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func (s *Store) ClaimBehaviorDirty(ctx context.Context, batch int, lease time.Duration) ([]BehaviorClaim, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("redis is not initialized")
	}
	if batch <= 0 {
		batch = 500
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	prefix := uuid.NewString()
	value, err := behaviorClaimScript.Run(
		s.client,
		[]string{BehaviorDirtyKey, BehaviorProcessingKey, BehaviorClaimsKey},
		batch,
		time.Now().Add(lease).UnixMilli(),
		prefix,
	).Result()
	if err != nil {
		return nil, err
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected behavior claim response %T", value)
	}
	claims := make([]BehaviorClaim, 0, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		claims = append(claims, BehaviorClaim{Pair: asString(items[i]), ClaimID: asString(items[i+1])})
	}
	return claims, nil
}

func (s *Store) LoadBehaviorDeliveries(ctx context.Context, claims []BehaviorClaim) ([]BehaviorDelivery, error) {
	if len(claims) == 0 {
		return nil, nil
	}
	fields := make([]string, 0, len(claims))
	for _, claim := range claims {
		fields = append(fields, claim.Pair)
	}
	values, err := s.client.HMGet(BehaviorStateKey, fields...).Result()
	if err != nil {
		return nil, err
	}
	deliveries := make([]BehaviorDelivery, 0, len(claims))
	for i, claim := range claims {
		if i >= len(values) || values[i] == nil {
			return nil, fmt.Errorf("behavior state missing for %q", claim.Pair)
		}
		userID, postID, err := parseBehaviorPair(claim.Pair)
		if err != nil {
			return nil, err
		}
		liked, version, occurredAt, err := parseBehaviorState(asString(values[i]))
		if err != nil {
			return nil, fmt.Errorf("behavior state %q: %w", claim.Pair, err)
		}
		deliveries = append(deliveries, BehaviorDelivery{
			Claim: claim, UserID: userID, PostID: postID,
			Liked: liked, Version: version, OccurredAt: occurredAt,
		})
	}
	return deliveries, nil
}

func (s *Store) AckBehaviorDeliveries(ctx context.Context, deliveries []BehaviorDelivery) (int, error) {
	if len(deliveries) == 0 {
		return 0, nil
	}
	pipe := s.client.Pipeline()
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
		return 0, err
	}
	acked := 0
	for _, command := range commands {
		value, err := command.Int64()
		if err != nil {
			return acked, err
		}
		if value == 1 {
			acked++
		}
	}
	return acked, nil
}

func (s *Store) RequeueBehaviorClaims(ctx context.Context, claims []BehaviorClaim) error {
	if len(claims) == 0 {
		return nil
	}
	pipe := s.client.Pipeline()
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
	return err
}

func (s *Store) ReapBehaviorExpired(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		batch = 500
	}
	return behaviorReapExpiredScript.Run(
		s.client,
		[]string{BehaviorDirtyKey, BehaviorStateKey, BehaviorProcessingKey, BehaviorClaimsKey},
		time.Now().UnixMilli(),
		batch,
	).Int64()
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
	occurredAt, err := time.Parse(time.RFC3339Nano, parts[2])
	if err != nil {
		return false, 0, time.Time{}, fmt.Errorf("invalid occurred_at: %w", err)
	}
	return parts[0] == "1", version, occurredAt, nil
}
