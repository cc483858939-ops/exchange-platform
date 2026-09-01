package likes

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
		DirtyKey, BehaviorDirtyKey, BehaviorStateKey, RegistryKey,
		ExpiryCandidatesKey, RecoverableVersionsKey,
	}
	now := time.Now().UTC()
	value, err := mutateScript.Run(
		s.client.WithContext(ctx), keys, postID, userID, desired,
		now.Format(time.RFC3339Nano), now.UnixMilli(),
	).Result()
	if err != nil {
		return MutationResult{}, mapScriptError(err)
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
	pipe := s.client.WithContext(ctx).Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	cardinality := pipe.SCard(UsersKey(postID))
	var member *redis.BoolCmd
	if userID > 0 {
		member = pipe.SIsMember(UsersKey(postID), strconv.FormatUint(uint64(userID), 10))
	}
	_, err := pipe.ExecContext(ctx)
	if err != nil && err != redis.Nil {
		return State{}, err
	}
	if err := requireReadyCommand(ready); err != nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(count.Err()); err != nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(version.Err()); err != nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(cardinality.Err()); err != nil {
		return State{}, err
	}
	if member != nil {
		if err := commandErrorOrNotReady(member.Err()); err != nil {
			return State{}, err
		}
	}
	countValue, ok := parseNonNegativeInt64(count.Val())
	if !ok || count.Err() == redis.Nil {
		return State{}, ErrNotReady
	}
	versionValue, ok := parseNonNegativeInt64(version.Val())
	if !ok || version.Err() == redis.Nil || cardinality.Val() != countValue {
		return State{}, ErrNotReady
	}
	state := State{Count: countValue, Version: versionValue}
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
		postID      uint
		ready       *redis.StringCmd
		count       *redis.StringCmd
		version     *redis.StringCmd
		cardinality *redis.IntCmd
		member      *redis.BoolCmd
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	batch := make([]commands, 0, len(postIDs))
	user := strconv.FormatUint(uint64(userID), 10)
	for _, postID := range postIDs {
		batch = append(batch, commands{
			postID:      postID,
			ready:       pipe.Get(ReadyKey(postID)),
			count:       pipe.Get(CountKey(postID)),
			version:     pipe.Get(VersionKey(postID)),
			cardinality: pipe.SCard(UsersKey(postID)),
			member:      pipe.SIsMember(UsersKey(postID), user),
		})
	}
	_, err := pipe.ExecContext(ctx)
	if err != nil && err != redis.Nil {
		return nil, nil, err
	}

	for _, command := range batch {
		for _, commandErr := range []error{command.ready.Err(), command.count.Err(), command.version.Err(), command.cardinality.Err(), command.member.Err()} {
			if commandErr != nil && commandErr != redis.Nil {
				return nil, nil, commandErr
			}
		}
		count, countOK := parseNonNegativeInt64(command.count.Val())
		version, versionOK := parseNonNegativeInt64(command.version.Val())
		if command.ready.Val() != "1" || command.ready.Err() == redis.Nil ||
			command.count.Err() == redis.Nil || command.version.Err() == redis.Nil ||
			!countOK || !versionOK || command.cardinality.Val() != count {
			unavailable = append(unavailable, command.postID)
			continue
		}
		states[command.postID] = State{
			Count:   count,
			Version: version,
			Liked:   command.member.Val(),
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
	userIDs, ok := normalizeUserIDs(userIDs)
	if !ok || int64(len(userIDs)) != count {
		return false, errors.New("invalid post like baseline")
	}
	args := []interface{}{count, version, postID, time.Now().UTC().UnixMilli()}
	for _, id := range userIDs {
		args = append(args, id)
	}
	value, err := initializeScript.Run(
		s.client.WithContext(ctx),
		[]string{ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID), RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey},
		args...,
	).Int64()
	return value == 1, mapScriptError(err)
}

func (s *Store) Recover(ctx context.Context, postID uint, baseline FullState, fence RecoveryFence) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || baseline.Count < 0 || baseline.Version < 0 {
		return false, ErrLikeRecoveryUnsafe
	}
	userIDs, ok := normalizeUserIDs(baseline.UserIDs)
	if !ok || int64(len(userIDs)) != baseline.Count {
		return false, ErrLikeRecoveryUnsafe
	}

	mode := "zero"
	expectedVersion := ""
	if fence.ExpectedVersion != nil {
		if *fence.ExpectedVersion < 0 {
			return false, ErrLikeRecoveryUnsafe
		}
		mode = "marker"
		expectedVersion = strconv.FormatInt(*fence.ExpectedVersion, 10)
	} else if !fence.AllowZeroBootstrap {
		return false, ErrLikeRecoveryUnsafe
	}
	args := []interface{}{
		baseline.Count, baseline.Version, postID, mode, expectedVersion,
		len(userIDs), time.Now().UTC().UnixMilli(),
	}
	for _, id := range userIDs {
		args = append(args, id)
	}
	value, err := recoverScript.Run(
		s.client.WithContext(ctx),
		[]string{ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID), RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey},
		args...,
	).Int64()
	return value == 1, mapScriptError(err)
}

func (s *Store) ArmExpiry(ctx context.Context, postID uint, expectedVersion int64, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || expectedVersion < 0 || ttl <= 0 {
		return false, errors.New("invalid like state expiry arguments")
	}
	seconds := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	value, err := armExpiryScript.Run(
		s.client.WithContext(ctx),
		[]string{
			ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID),
			RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey,
			DirtyKey, ProcessingKey, ClaimsKey,
		},
		postID, expectedVersion, seconds,
	).Int64()
	return value == 1, mapScriptError(err)
}

// PurgePost removes all Redis-owned Like state for a deleted Post. Relational
// PostReaction rows and behavior-relay keys are intentionally outside this
// cleanup contract.
func (s *Store) PurgePost(ctx context.Context, postID uint) error {
	if s == nil || s.client == nil {
		return errors.New("redis is not initialized")
	}
	if postID == 0 {
		return errors.New("invalid post id")
	}

	_, err := purgePostScript.Run(
		s.client.WithContext(ctx),
		[]string{
			ReadyKey(postID), CountKey(postID), UsersKey(postID), VersionKey(postID),
			DirtyKey, ProcessingKey, ClaimsKey,
			RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey,
		},
		postID,
	).Int64()
	return mapScriptError(err)
}

func (s *Store) LoadFullState(ctx context.Context, postID uint) (FullState, error) {
	if s == nil || s.client == nil {
		return FullState{}, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return FullState{}, ErrNotReady
	}
	client := s.client.WithContext(ctx)
	pipe := client.Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	users := pipe.SMembers(UsersKey(postID))
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		return FullState{}, err
	}
	if err := commandErrorOrNotReady(ready.Err()); err != nil {
		return FullState{}, err
	}
	if ready.Val() != "1" {
		return FullState{}, ErrNotReady
	}
	if err := commandErrorOrNotReady(count.Err()); err != nil {
		return FullState{}, err
	}
	if err := commandErrorOrNotReady(version.Err()); err != nil {
		return FullState{}, err
	}
	countValue, ok := parseNonNegativeInt64(count.Val())
	if count.Err() == redis.Nil || !ok {
		return FullState{}, ErrNotReady
	}
	versionValue, ok := parseNonNegativeInt64(version.Val())
	if version.Err() == redis.Nil || !ok {
		return FullState{}, ErrNotReady
	}
	if err := commandErrorOrNotReady(users.Err()); err != nil {
		return FullState{}, err
	}
	userIDs, ok := parseUserIDs(users.Val())
	if !ok || int64(len(userIDs)) != countValue {
		return FullState{}, ErrNotReady
	}
	return FullState{Count: countValue, Version: versionValue, UserIDs: userIDs}, nil
}

func (s *Store) RegistryContains(ctx context.Context, postID uint) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return false, errors.New("invalid post id")
	}
	return s.client.WithContext(ctx).SIsMember(RegistryKey, postID).Result()
}

func (s *Store) RegistryContainsMany(ctx context.Context, postIDs []uint) (map[uint]bool, error) {
	result := make(map[uint]bool, len(postIDs))
	if s == nil || s.client == nil {
		return nil, errors.New("redis is not initialized")
	}
	if len(postIDs) == 0 {
		return result, nil
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	commands := make([]struct {
		postID uint
		cmd    *redis.BoolCmd
	}, 0, len(postIDs))
	for _, postID := range postIDs {
		commands = append(commands, struct {
			postID uint
			cmd    *redis.BoolCmd
		}{postID: postID, cmd: pipe.SIsMember(RegistryKey, postID)})
	}
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	for _, command := range commands {
		if err := commandErrorOrNotReady(command.cmd.Err()); err != nil {
			return nil, err
		}
		result[command.postID] = command.cmd.Val()
	}
	return result, nil
}

func (s *Store) GetRecoverableVersion(ctx context.Context, postID uint) (*int64, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return nil, errors.New("invalid post id")
	}
	value, err := s.client.WithContext(ctx).HGet(RecoverableVersionsKey, strconv.FormatUint(uint64(postID), 10)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	version, ok := parseNonNegativeInt64(value)
	if !ok {
		return nil, errors.New("invalid recoverable like version")
	}
	return &version, nil
}

func (s *Store) GetRecoverableVersions(ctx context.Context, postIDs []uint) (map[uint]int64, error) {
	result := make(map[uint]int64, len(postIDs))
	if s == nil || s.client == nil {
		return nil, errors.New("redis is not initialized")
	}
	if len(postIDs) == 0 {
		return result, nil
	}
	fields := make([]string, 0, len(postIDs))
	for _, postID := range postIDs {
		fields = append(fields, strconv.FormatUint(uint64(postID), 10))
	}
	values, err := s.client.WithContext(ctx).HMGet(RecoverableVersionsKey, fields...).Result()
	if err != nil {
		return nil, err
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		version, ok := parseNonNegativeInt64(asString(value))
		if !ok {
			return nil, errors.New("invalid recoverable like version")
		}
		result[postIDs[index]] = version
	}
	return result, nil
}

func (s *Store) ScanRegistry(ctx context.Context, cursor uint64, count int) ([]uint, uint64, error) {
	if s == nil || s.client == nil {
		return nil, 0, errors.New("redis is not initialized")
	}
	if count <= 0 {
		count = 100
	}
	members, next, err := s.client.WithContext(ctx).SScan(RegistryKey, cursor, "", int64(count)).Result()
	if err != nil {
		return nil, 0, err
	}
	postIDs := make([]uint, 0, len(members))
	for _, member := range members {
		postID, parseErr := strconv.ParseUint(member, 10, 64)
		if parseErr != nil || postID == 0 || uint64(uint(postID)) != postID {
			continue
		}
		postIDs = append(postIDs, uint(postID))
	}
	return postIDs, next, nil
}

func (s *Store) LoadExpiryCandidates(ctx context.Context, cutoff time.Time, batch int) ([]uint, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("redis is not initialized")
	}
	if batch <= 0 {
		batch = 100
	}
	members, err := s.client.WithContext(ctx).ZRangeByScore(ExpiryCandidatesKey, &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(cutoff.UnixMilli(), 10), Offset: 0, Count: int64(batch),
	}).Result()
	if err != nil {
		return nil, err
	}
	postIDs := make([]uint, 0, len(members))
	for _, member := range members {
		postID, parseErr := strconv.ParseUint(member, 10, 64)
		if parseErr != nil || postID == 0 || uint64(uint(postID)) != postID {
			continue
		}
		postIDs = append(postIDs, uint(postID))
	}
	return postIDs, nil
}

func (s *Store) TouchExpiryCandidate(ctx context.Context, postID uint, at time.Time) error {
	if s == nil || s.client == nil {
		return errors.New("redis is not initialized")
	}
	if postID == 0 {
		return errors.New("invalid post id")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.client.WithContext(ctx).ZAdd(ExpiryCandidatesKey, &redis.Z{
		Score: float64(at.UnixMilli()), Member: postID,
	}).Err()
}

func (s *Store) SnapshotQueueQuiescent(ctx context.Context, postID uint) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return false, errors.New("invalid post id")
	}
	postIDString := strconv.FormatUint(uint64(postID), 10)
	pipe := s.client.WithContext(ctx).Pipeline()
	dirty := pipe.SIsMember(DirtyKey, postID)
	processing := pipe.ZScore(ProcessingKey, postIDString)
	claim := pipe.HExists(ClaimsKey, postIDString)
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		return false, err
	}
	for _, err := range []error{dirty.Err(), processing.Err(), claim.Err()} {
		if err != nil && err != redis.Nil {
			return false, err
		}
	}
	return !dirty.Val() && processing.Err() == redis.Nil && !claim.Val(), nil
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
	state, err := s.loadAggregateState(ctx, postID)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{PostID: postID, Count: state.Count, Version: state.Version}, nil
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

func (s *Store) loadAggregateState(ctx context.Context, postID uint) (State, error) {
	if s == nil || s.client == nil {
		return State{}, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return State{}, ErrNotReady
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	cardinality := pipe.SCard(UsersKey(postID))
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(ready.Err()); err != nil {
		return State{}, err
	}
	if ready.Val() != "1" {
		return State{}, ErrNotReady
	}
	if err := commandErrorOrNotReady(count.Err()); err != nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(version.Err()); err != nil {
		return State{}, err
	}
	if err := commandErrorOrNotReady(cardinality.Err()); err != nil {
		return State{}, err
	}
	countValue, countOK := parseNonNegativeInt64(count.Val())
	versionValue, versionOK := parseNonNegativeInt64(version.Val())
	if !countOK || !versionOK || cardinality.Val() != countValue {
		return State{}, ErrNotReady
	}
	return State{Count: countValue, Version: versionValue}, nil
}

func requireReadyCommand(command *redis.StringCmd) error {
	if command == nil {
		return ErrNotReady
	}
	if err := commandErrorOrNotReady(command.Err()); err != nil {
		return err
	}
	if command.Val() != "1" {
		return ErrNotReady
	}
	return nil
}

func commandErrorOrNotReady(err error) error {
	if err == nil {
		return nil
	}
	if err == redis.Nil {
		return ErrNotReady
	}
	return err
}

func parseNonNegativeInt64(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

func parseUserIDs(values []string) ([]uint, bool) {
	userIDs := make([]uint, 0, len(values))
	seen := make(map[uint]struct{}, len(values))
	for _, value := range values {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
			return nil, false
		}
		userID := uint(parsed)
		if _, exists := seen[userID]; exists {
			return nil, false
		}
		seen[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	return userIDs, true
}

func normalizeUserIDs(userIDs []uint) ([]uint, bool) {
	copyIDs := append([]uint(nil), userIDs...)
	return parseUserIDs(func() []string {
		values := make([]string, 0, len(copyIDs))
		for _, userID := range copyIDs {
			values = append(values, strconv.FormatUint(uint64(userID), 10))
		}
		return values
	}())
}

func mapScriptError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "LIKE_NOT_READY"):
		return ErrNotReady
	case strings.Contains(message, "LIKE_RECOVERY_UNSAFE"):
		return ErrLikeRecoveryUnsafe
	case strings.Contains(message, "LIKE_RECOVERY_FENCE_LOST"):
		return ErrLikeRecoveryFenceLost
	case strings.Contains(message, "LIKE_TYPE_PRECHECK"):
		return fmt.Errorf("like Redis key type preflight failed: %w", ErrLikeRedisType)
	default:
		return err
	}
}

func parseInt64(value string) int64 {
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
}
