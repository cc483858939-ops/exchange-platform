package likes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

type Store struct{ client *redis.Client }

func NewStore(client *redis.Client) *Store { return &Store{client: client} }

func (s *Store) Mutate(ctx context.Context, userID, postID uint, liked bool) (MutationResult, error) {
	if s == nil || s.client == nil {
		return MutationResult{}, errors.New("redis is not initialized")
	}
	desired := "0"
	if liked {
		desired = "1"
	}
	keys := []string{
		ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID),
		DirtyKey, BehaviorDirtyKey, BehaviorStateKey,
	}
	value, err := mutateScript.Run(s.client, keys, postID, userID, desired, time.Now().UTC().Format(time.RFC3339Nano)).Result()
	if err != nil {
		if strings.Contains(err.Error(), "LIKE_NOT_READY") {
			return MutationResult{}, ErrNotReady
		}
		return MutationResult{}, err
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 4 {
		return MutationResult{}, fmt.Errorf("unexpected mutation response %T", value)
	}
	return MutationResult{Count: asInt64(items[0]), Liked: asInt64(items[1]) == 1, Changed: asInt64(items[2]) == 1, Version: asInt64(items[3])}, nil
}

func (s *Store) Get(ctx context.Context, userID, postID uint) (State, error) {
	if s == nil || s.client == nil {
		return State{}, errors.New("redis is not initialized")
	}
	pipe := s.client.Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	var member *redis.BoolCmd
	if userID > 0 {
		member = pipe.SIsMember(UsersKey(postID), strconv.FormatUint(uint64(userID), 10))
	}
	_, err := pipe.ExecContext(ctx)
	if err != nil && err != redis.Nil {
		return State{}, err
	}
	if ready.Val() != "1" {
		return State{}, ErrNotReady
	}
	state := State{Count: parseInt64(count.Val()), Version: parseInt64(version.Val())}
	if member != nil {
		state.Liked = member.Val()
	}
	return state, nil
}

func (s *Store) GetMany(ctx context.Context, userID uint, postIDs []uint) (map[uint]State, []uint, error) {
	if s == nil || s.client == nil {
		return nil, nil, errors.New("redis is not initialized")
	}
	states := make(map[uint]State, len(postIDs))
	unavailable := make([]uint, 0)
	if len(postIDs) == 0 {
		return states, unavailable, nil
	}

	type commands struct {
		postID uint
		ready     *redis.StringCmd
		count     *redis.StringCmd
		member    *redis.BoolCmd
	}
	pipe := s.client.Pipeline()
	batch := make([]commands, 0, len(postIDs))
	user := strconv.FormatUint(uint64(userID), 10)
	for _, postID := range postIDs {
		batch = append(batch, commands{
			postID: postID,
			ready:     pipe.Get(ReadyKey(postID)),
			count:     pipe.Get(CountKey(postID)),
			member:    pipe.SIsMember(UsersKey(postID), user),
		})
	}
	_, err := pipe.ExecContext(ctx)
	if err != nil && err != redis.Nil {
		return nil, nil, err
	}

	for _, command := range batch {
		for _, commandErr := range []error{command.ready.Err(), command.count.Err(), command.member.Err()} {
			if commandErr != nil && commandErr != redis.Nil {
				return nil, nil, commandErr
			}
		}
		if command.ready.Val() != "1" {
			unavailable = append(unavailable, command.postID)
			continue
		}
		states[command.postID] = State{
			Count: parseInt64(command.count.Val()),
			Liked: command.member.Val(),
		}
	}
	return states, unavailable, nil
}
func (s *Store) Initialize(ctx context.Context, postID uint, count, version int64, userIDs []uint) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || count < 0 || version < 0 {
		return false, errors.New("invalid post like baseline")
	}
	args := []interface{}{count, version}
	for _, id := range userIDs {
		args = append(args, id)
	}
	value, err := initializeScript.Run(s.client, []string{ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID)}, args...).Int64()
	return value == 1, err
}

func (s *Store) ClaimDirty(ctx context.Context, batch int, lease time.Duration) ([]SnapshotClaim, error) {
	if batch <= 0 {
		batch = 100
	}
	prefix := uuid.NewString()
	deadline := time.Now().Add(lease).UnixMilli()
	value, err := claimScript.Run(s.client, []string{DirtyKey, ProcessingKey, ClaimsKey}, batch, deadline, prefix).Result()
	if err != nil {
		return nil, err
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected claim response %T", value)
	}
	claims := make([]SnapshotClaim, 0, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		claims = append(claims, SnapshotClaim{PostID: uint(asInt64(items[i])), ClaimID: asString(items[i+1])})
	}
	return claims, nil
}

func (s *Store) LoadSnapshot(ctx context.Context, postID uint) (Snapshot, error) {
	values, err := s.client.MGet(CountKey(postID), VersionKey(postID), ReadyKey(postID)).Result()
	if err != nil {
		return Snapshot{}, err
	}
	if len(values) != 3 || asString(values[2]) != "1" {
		return Snapshot{}, ErrNotReady
	}
	return Snapshot{PostID: postID, Count: asInt64(values[0]), Version: asInt64(values[1])}, nil
}

func (s *Store) AckClaim(ctx context.Context, claim SnapshotClaim) (bool, error) {
	v, err := ackClaimScript.Run(s.client, []string{ProcessingKey, ClaimsKey}, claim.PostID, claim.ClaimID).Int64()
	return v == 1, err
}
func (s *Store) RequeueClaim(ctx context.Context, claim SnapshotClaim) (bool, error) {
	v, err := requeueClaimScript.Run(s.client, []string{DirtyKey, ProcessingKey, ClaimsKey}, claim.PostID, claim.ClaimID).Int64()
	return v == 1, err
}
func (s *Store) ReapExpired(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		batch = 100
	}
	return reapExpiredScript.Run(s.client, []string{DirtyKey, ProcessingKey, ClaimsKey}, time.Now().UnixMilli(), batch).Int64()
}

func asInt64(v interface{}) int64 {
	switch value := v.(type) {
	case int64:
		return value
	case string:
		return parseInt64(value)
	case []byte:
		return parseInt64(string(value))
	default:
		return 0
	}
}
func asString(v interface{}) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return ""
	}
}
func parseInt64(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }
