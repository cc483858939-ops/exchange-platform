package likes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

type Store struct{ client *redis.Client }

func NewStore(client *redis.Client) *Store { return &Store{client: client} }

func (s *Store) Mutate(ctx context.Context, userID, postID uint, liked bool) (MutationResult, error) {
	if s == nil || s.client == nil {
		return MutationResult{}, errors.New("redis is not initialized")
	}
	if userID == 0 || postID == 0 {
		if userID == 0 {
			return MutationResult{}, userNotReadyError()
		}
		return MutationResult{}, postNotReadyError()
	}
	desired := "0"
	if liked {
		desired = "1"
	}
	keys := []string{
		ReadyKey(postID), CountKey(postID), VersionKey(postID), UserLikesKey(userID),
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
	return s.get(ctx, userID, postID)
}

// GetForServing reads and validates Like state for a client-facing request.
// Lease arguments are rejected because Post-only expiry cannot preserve the
// Persistent User -> Posts relations cannot be expired independently from the
// corresponding Post aggregates.
func (s *Store) GetForServing(ctx context.Context, userID, postID uint, ttl, renewalThreshold time.Duration) (State, error) {
	if config.LikeStateExpiryEnabled() || ttl != 0 || renewalThreshold != 0 {
		return State{}, ErrLikeStateExpiryUnsupported
	}
	return s.get(ctx, userID, postID)
}

func (s *Store) get(ctx context.Context, userID, postID uint) (State, error) {
	if s == nil || s.client == nil {
		return State{}, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return State{}, postNotReadyError()
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	var initialized *redis.BoolCmd
	var member *redis.BoolCmd
	if userID > 0 {
		userKey := UserLikesKey(userID)
		initialized = pipe.SIsMember(userKey, UserLikesInitSentinel)
		member = pipe.SIsMember(userKey, strconv.FormatUint(uint64(postID), 10))
	}
	_, execErr := pipe.ExecContext(ctx)
	if ready.Err() == nil && ready.Val() == "deleted" {
		return State{}, ErrPostLikeUnavailable
	}
	if execErr != nil && execErr != redis.Nil {
		return State{}, mapScriptError(execErr)
	}
	if initialized != nil {
		if err := userCommandErrorOrNotReady(initialized.Err()); err != nil {
			return State{}, err
		}
		if !initialized.Val() {
			return State{}, userNotReadyError()
		}
	}
	if err := requireReadyCommand(ready); err != nil {
		return State{}, err
	}
	if err := postCommandErrorOrNotReady(count.Err()); err != nil {
		return State{}, err
	}
	if err := postCommandErrorOrNotReady(version.Err()); err != nil {
		return State{}, err
	}
	if member != nil {
		if err := userCommandErrorOrNotReady(member.Err()); err != nil {
			return State{}, err
		}
	}
	countValue, ok := parseNonNegativeInt64(count.Val())
	if !ok || count.Err() == redis.Nil {
		return State{}, postNotReadyError()
	}
	versionValue, ok := parseNonNegativeInt64(version.Val())
	if !ok || version.Err() == redis.Nil {
		return State{}, postNotReadyError()
	}
	state := State{Count: countValue, Version: versionValue}
	if member != nil {
		state.Liked = member.Val()
	}
	return state, nil
}

// LoadSummary reads only the aggregate Redis Like state required by
// maintenance. It deliberately does not load the users Set members.
func (s *Store) LoadSummary(ctx context.Context, postID uint) (State, error) {
	return s.Get(ctx, 0, postID)
}

func (s *Store) GetMany(ctx context.Context, userID uint, postIDs []uint) (map[uint]State, []uint, error) {
	return s.getMany(ctx, userID, postIDs)
}

// GetManyForServing keeps batched read semantics. Lease arguments are rejected
// until expiry can preserve both aggregate and per-user relation state.
func (s *Store) GetManyForServing(ctx context.Context, userID uint, postIDs []uint, ttl, renewalThreshold time.Duration) (map[uint]State, []uint, error) {
	if config.LikeStateExpiryEnabled() || ttl != 0 || renewalThreshold != 0 {
		return nil, nil, ErrLikeStateExpiryUnsupported
	}
	return s.getMany(ctx, userID, postIDs)
}

func (s *Store) getMany(ctx context.Context, userID uint, postIDs []uint) (map[uint]State, []uint, error) {
	if s == nil || s.client == nil {
		return nil, nil, errors.New("redis is not initialized")
	}
	states := make(map[uint]State, len(postIDs))
	unavailable := make([]uint, 0)
	if len(postIDs) == 0 {
		return states, unavailable, nil
	}

	type commands struct {
		postID  uint
		ready   *redis.StringCmd
		count   *redis.StringCmd
		version *redis.StringCmd
		member  *redis.BoolCmd
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	batch := make([]commands, 0, len(postIDs))
	var initialized *redis.BoolCmd
	userKey := ""
	if userID > 0 {
		userKey = UserLikesKey(userID)
		initialized = pipe.SIsMember(userKey, UserLikesInitSentinel)
	}
	for _, postID := range postIDs {
		if postID == 0 {
			unavailable = append(unavailable, postID)
			continue
		}
		command := commands{
			postID:  postID,
			ready:   pipe.Get(ReadyKey(postID)),
			count:   pipe.Get(CountKey(postID)),
			version: pipe.Get(VersionKey(postID)),
		}
		if userID > 0 {
			command.member = pipe.SIsMember(userKey, strconv.FormatUint(uint64(postID), 10))
		}
		batch = append(batch, command)
	}
	_, execErr := pipe.ExecContext(ctx)
	if initialized != nil {
		if err := userCommandErrorOrNotReady(initialized.Err()); err != nil {
			recordStoreLifecycleFailure(err, userID, 0)
			return nil, nil, err
		}
		if !initialized.Val() {
			err := userNotReadyError()
			recordStoreLifecycleFailure(err, userID, 0)
			return nil, nil, err
		}
	}
	for _, command := range batch {
		if command.ready.Err() == nil && command.ready.Val() == "deleted" {
			unavailable = append(unavailable, command.postID)
			continue
		}
		for _, commandErr := range []error{command.ready.Err(), command.count.Err(), command.version.Err()} {
			if commandErr != nil && commandErr != redis.Nil {
				mapped := mapScriptError(commandErr)
				recordStoreLifecycleFailure(mapped, userID, command.postID)
				return nil, nil, mapped
			}
		}
		if command.member != nil {
			if err := userCommandErrorOrNotReady(command.member.Err()); err != nil {
				recordStoreLifecycleFailure(err, userID, command.postID)
				return nil, nil, err
			}
		}
		count, countOK := parseNonNegativeInt64(command.count.Val())
		version, versionOK := parseNonNegativeInt64(command.version.Val())
		if command.ready.Val() != "1" || command.ready.Err() == redis.Nil ||
			command.count.Err() == redis.Nil || command.version.Err() == redis.Nil ||
			!countOK || !versionOK {
			unavailable = append(unavailable, command.postID)
			err := postNotReadyError()
			recordStoreLifecycleFailure(err, userID, command.postID)
			continue
		}
		states[command.postID] = State{
			Count:   count,
			Version: version,
			Liked:   command.member != nil && command.member.Val(),
		}
	}
	if execErr != nil && execErr != redis.Nil {
		mapped := mapScriptError(execErr)
		recordStoreLifecycleFailure(mapped, userID, 0)
		return nil, nil, mapped
	}
	return states, unavailable, nil
}

// InitializeUserEmpty creates the explicit initialized-empty relation only for
// callers that know this user has no prior Like state. It never repairs a
// partially lost or malformed relation set.
func (s *Store) InitializeUserEmpty(ctx context.Context, userID uint) error {
	_, err := s.InitializeUserEmptyWithResult(ctx, userID)
	return err
}

// InitializeUserEmptyWithResult returns true only when this call created the
// initialized-empty sentinel. Existing initialized relations are never reset.
func (s *Store) InitializeUserEmptyWithResult(ctx context.Context, userID uint) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if userID == 0 {
		return false, errors.New("invalid user id")
	}
	value, err := initializeUserEmptyScript.Run(
		s.client.WithContext(ctx), []string{UserLikesKey(userID)},
	).Int64()
	if err != nil {
		return false, mapScriptError(err)
	}
	return value == 1, nil
}

// Initialize requires a rebuild token acquired before loading the active SQL
// baseline. SPEC-01 only permits a confirmed zero-state bootstrap because it
// cannot reconstruct User -> Posts relationships from a Post aggregate.
func (s *Store) Initialize(ctx context.Context, postID uint, count, version int64, rebuildToken string) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || count < 0 || version < 0 {
		return false, ErrLikeRecoveryUnsafe
	}
	if count != 0 || version != 0 {
		return false, ErrLikeRecoveryUnsafe
	}
	args := []interface{}{count, version, postID, time.Now().UTC().UnixMilli(), rebuildToken}
	value, err := initializeScript.Run(
		s.client.WithContext(ctx),
		[]string{ReadyKey(postID), CountKey(postID), VersionKey(postID), RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey, RebuildTokenKey(postID)},
		args...,
	).Int64()
	return value == 1, mapScriptError(err)
}

func (s *Store) Recover(ctx context.Context, postID uint, baseline FullState, fence RecoveryFence) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	if postID == 0 || baseline.Count != 0 || baseline.Version != 0 ||
		fence.ExpectedVersion != nil || !fence.AllowZeroBootstrap {
		return false, ErrLikeRecoveryUnsafe
	}
	args := []interface{}{baseline.Count, baseline.Version, postID, time.Now().UTC().UnixMilli(), fence.RebuildToken}
	value, err := recoverScript.Run(
		s.client.WithContext(ctx),
		[]string{ReadyKey(postID), CountKey(postID), VersionKey(postID), RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey, RebuildTokenKey(postID)},
		args...,
	).Int64()
	return value == 1, mapScriptError(err)
}

func (s *Store) ArmExpiry(ctx context.Context, postID uint, expectedVersion int64, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	return false, ErrLikeStateExpiryUnsupported
}

// RenewExpiryLease is disabled until expiration can preserve both aggregate
// and per-user relation state.
func (s *Store) RenewExpiryLease(ctx context.Context, postID uint, expectedVersion int64, ttl, renewalThreshold time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("redis is not initialized")
	}
	return false, ErrLikeStateExpiryUnsupported
}

// DeletePost first fences the identity using the existing Ready key, so partial
// cleanup cannot serve old state or let a pre-deletion recovery reinstall it.
// Revoke pre-deletion rebuild tokens independently of cleanup. The fence gets
// a TTL only after successful cleanup when deletion expiry is enabled.
// Failed propagation is retried from the durable deletion queue.
func (s *Store) DeletePost(ctx context.Context, postID uint) error {
	if s == nil || s.client == nil {
		return errors.New("redis is not initialized")
	}
	if postID == 0 {
		return errors.New("invalid post id")
	}
	if err := markPostDeletedScript.Run(s.client.WithContext(ctx), []string{ReadyKey(postID), RebuildTokenKey(postID)}).Err(); err != nil {
		return mapScriptError(err)
	}
	metrics.RecordLikeLifecycleEvent("post_delete_fence_success")
	return s.PurgePost(ctx, postID)
}

// PurgePost removes live Post aggregate and snapshot-relay state, preserving a
// deletion fence. User relations are reclaimed later by a bounded SQL/SSCAN
// maintenance task.
func (s *Store) PurgePost(ctx context.Context, postID uint) error {
	if s == nil || s.client == nil {
		return errors.New("redis is not initialized")
	}
	if postID == 0 {
		return errors.New("invalid post id")
	}

	tombstoneTTL := int64(0)
	if config.LikeDeletionTombstoneExpiryEnabled() {
		tombstoneTTL = config.LikeDeletionTombstoneTTL().Milliseconds()
	}
	_, err := purgePostScript.Run(
		s.client.WithContext(ctx),
		[]string{
			ReadyKey(postID), CountKey(postID), VersionKey(postID),
			DirtyKey, ProcessingKey, ClaimsKey,
			RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey,
			RebuildTokenKey(postID),
		},
		postID, tombstoneTTL,
	).Int64()
	return mapScriptError(err)
}

func (s *Store) LoadFullState(ctx context.Context, postID uint) (FullState, error) {
	if s == nil || s.client == nil {
		return FullState{}, errors.New("redis is not initialized")
	}
	if postID == 0 {
		return FullState{}, postNotReadyError()
	}
	client := s.client.WithContext(ctx)
	pipe := client.Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	_, execErr := pipe.ExecContext(ctx)
	if ready.Err() == nil && ready.Val() == "deleted" {
		return FullState{}, ErrPostLikeUnavailable
	}
	if execErr != nil && execErr != redis.Nil {
		return FullState{}, execErr
	}
	if err := postCommandErrorOrNotReady(ready.Err()); err != nil {
		return FullState{}, err
	}
	if ready.Val() != "1" {
		return FullState{}, postNotReadyError()
	}
	if err := postCommandErrorOrNotReady(count.Err()); err != nil {
		return FullState{}, err
	}
	if err := postCommandErrorOrNotReady(version.Err()); err != nil {
		return FullState{}, err
	}
	countValue, ok := parseNonNegativeInt64(count.Val())
	if count.Err() == redis.Nil || !ok {
		return FullState{}, postNotReadyError()
	}
	versionValue, ok := parseNonNegativeInt64(version.Val())
	if version.Err() == redis.Nil || !ok {
		return FullState{}, postNotReadyError()
	}
	return FullState{Count: countValue, Version: versionValue}, nil
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

// ScanUserLikes enumerates one user's relation Set without loading it in full.
// The initialized-empty sentinel is omitted from returned post IDs.
func (s *Store) ScanUserLikes(ctx context.Context, userID uint, cursor uint64, count int) ([]uint, uint64, error) {
	if s == nil || s.client == nil {
		return nil, cursor, errors.New("redis is not initialized")
	}
	if userID == 0 {
		return nil, cursor, errors.New("invalid user id")
	}
	if count <= 0 {
		count = 100
	}
	if count > 128 {
		count = 128
	}
	result, err := scanUserLikesScript.Run(s.client.WithContext(ctx), []string{UserLikesKey(userID)}, cursor, count).Result()
	if err != nil {
		return nil, cursor, mapScriptError(err)
	}
	values, ok := result.([]interface{})
	if !ok {
		return nil, cursor, fmt.Errorf("unexpected User Like SSCAN response type %T", result)
	}
	if len(values) == 0 {
		return nil, cursor, errors.New("unexpected User Like SSCAN response")
	}
	next, parseErr := strconv.ParseUint(asString(values[0]), 10, 64)
	if parseErr != nil {
		return nil, cursor, fmt.Errorf("parse User Like SSCAN cursor: %w", parseErr)
	}
	postIDs := make([]uint, 0, len(values)-1)
	for _, value := range values[1:] {
		postID, parseErr := strconv.ParseUint(asString(value), 10, 64)
		if parseErr != nil || postID == 0 || uint64(uint(postID)) != postID {
			continue
		}
		postIDs = append(postIDs, uint(postID))
	}
	return postIDs, next, nil
}

// RemoveDeletedUserPostRelations removes at most 128 SQL-confirmed deleted
// Post relations. Redis rechecks each Ready fence atomically and never touches
// the sentinel or any Post aggregate, queue, or event key.
func (s *Store) RemoveDeletedUserPostRelations(ctx context.Context, userID uint, postIDs []uint) (int64, error) {
	removed, issues, err := s.RemoveDeletedUserPostRelationsDetailed(ctx, userID, postIDs)
	if err != nil {
		return removed, err
	}
	if len(issues) > 0 {
		switch issues[0].Kind {
		case UserLikeCleanupPostReadyTypeError:
			return removed, fmt.Errorf("Post %d Ready key has an unexpected type: %w", issues[0].PostID, ErrPostLikeRedisType)
		case UserLikeCleanupLifecycleMismatch:
			return removed, fmt.Errorf("Post %d is SQL-deleted but Redis Ready is active: %w", issues[0].PostID, ErrLikeRelationLifecycleMismatch)
		default:
			return removed, fmt.Errorf("Post %d has an unexpected Redis Ready value: %w", issues[0].PostID, ErrPostLikeNotReady)
		}
	}
	return removed, nil
}

// RemoveDeletedUserPostRelationsDetailed isolates malformed Post Ready keys
// so they cannot block healthy candidates in this bounded batch.
func (s *Store) RemoveDeletedUserPostRelationsDetailed(ctx context.Context, userID uint, postIDs []uint) (int64, []UserLikeCleanupIssue, error) {
	if s == nil || s.client == nil {
		return 0, nil, errors.New("redis is not initialized")
	}
	if userID == 0 {
		return 0, nil, errors.New("invalid user id")
	}
	if len(postIDs) > 128 {
		return 0, nil, errors.New("relation cleanup batch exceeds 128 Post IDs")
	}
	keys := make([]string, 1, len(postIDs)+1)
	keys[0] = UserLikesKey(userID)
	args := make([]interface{}, 0, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		keys = append(keys, ReadyKey(postID))
		args = append(args, strconv.FormatUint(uint64(postID), 10))
	}
	if len(args) == 0 {
		return 0, nil, nil
	}
	result, err := removeDeletedUserPostRelationsScript.Run(s.client.WithContext(ctx), keys, args...).Result()
	if err != nil {
		return 0, nil, mapScriptError(err)
	}
	values, ok := result.([]interface{})
	if !ok {
		return 0, nil, fmt.Errorf("unexpected User Like relation cleanup response type %T", result)
	}
	if len(values) == 0 || (len(values)-1)%2 != 0 {
		return 0, nil, errors.New("unexpected User Like relation cleanup response")
	}
	removed := asInt64(values[0])
	if removed < 0 {
		return 0, nil, errors.New("negative User Like relation cleanup count")
	}
	issues := make([]UserLikeCleanupIssue, 0, (len(values)-1)/2)
	for index := 1; index < len(values); index += 2 {
		postIDValue, parseErr := strconv.ParseUint(asString(values[index]), 10, 64)
		if parseErr != nil || postIDValue == 0 || uint64(uint(postIDValue)) != postIDValue {
			return removed, nil, fmt.Errorf("parse User Like cleanup PostID: %q", asString(values[index]))
		}
		kind := asString(values[index+1])
		switch kind {
		case UserLikeCleanupPostReadyTypeError, UserLikeCleanupLifecycleMismatch, UserLikeCleanupUnexpectedReady:
		default:
			return removed, nil, fmt.Errorf("unknown User Like cleanup issue kind %q", kind)
		}
		issues = append(issues, UserLikeCleanupIssue{PostID: uint(postIDValue), Kind: kind})
	}
	return removed, issues, nil
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
		return State{}, postNotReadyError()
	}
	pipe := s.client.WithContext(ctx).Pipeline()
	ready := pipe.Get(ReadyKey(postID))
	count := pipe.Get(CountKey(postID))
	version := pipe.Get(VersionKey(postID))
	if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
		return State{}, mapScriptError(err)
	}
	if ready.Err() == nil && ready.Val() == "deleted" {
		return State{}, ErrPostLikeUnavailable
	}
	if err := postCommandErrorOrNotReady(ready.Err()); err != nil {
		return State{}, err
	}
	if ready.Val() != "1" {
		return State{}, postNotReadyError()
	}
	if err := postCommandErrorOrNotReady(count.Err()); err != nil {
		return State{}, err
	}
	if err := postCommandErrorOrNotReady(version.Err()); err != nil {
		return State{}, err
	}
	countValue, countOK := parseNonNegativeInt64(count.Val())
	versionValue, versionOK := parseNonNegativeInt64(version.Val())
	if !countOK || !versionOK {
		return State{}, postNotReadyError()
	}
	return State{Count: countValue, Version: versionValue}, nil
}

func requireReadyCommand(command *redis.StringCmd) error {
	if command == nil {
		return postNotReadyError()
	}
	if err := postCommandErrorOrNotReady(command.Err()); err != nil {
		return err
	}
	if command.Val() != "1" {
		return postNotReadyError()
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
	if strings.Contains(strings.ToUpper(err.Error()), "WRONGTYPE") {
		return fmt.Errorf("like Redis key type preflight failed: %w", ErrLikeRedisType)
	}
	return err
}

func postCommandErrorOrNotReady(err error) error {
	if err == redis.Nil {
		return postNotReadyError()
	}
	if err != nil {
		return commandErrorOrNotReady(err)
	}
	return nil
}

func userCommandErrorOrNotReady(err error) error {
	if err == redis.Nil {
		return userNotReadyError()
	}
	if err != nil {
		return commandErrorOrNotReady(err)
	}
	return nil
}

func recordStoreLifecycleFailure(err error, userID, postID uint) {
	switch {
	case errors.Is(err, ErrUserLikeNotReady):
		metrics.RecordLikeLifecycleEvent("user_not_ready")
		log.Printf("[LikeLifecycle] like_state_user_not_ready user=%d post=%d", userID, postID)
	case errors.Is(err, ErrPostLikeNotReady):
		metrics.RecordLikeLifecycleEvent("post_not_ready")
		metrics.RecordLikeLifecycleEvent("post_recovery_refused")
		log.Printf("[LikeLifecycle] like_state_post_recovery_refused user=%d post=%d", userID, postID)
	case errors.Is(err, ErrLikeRedisType):
		metrics.RecordLikeLifecycleEvent("redis_type_error")
		log.Printf("[LikeLifecycle] like_state_redis_type_error user=%d post=%d", userID, postID)
	case errors.Is(err, ErrLikeCountInconsistent):
		metrics.RecordLikeLifecycleEvent("count_inconsistent")
		log.Printf("[LikeLifecycle] like_state_count_inconsistent user=%d post=%d", userID, postID)
	}
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

func mapScriptError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "LIKE_USER_TYPE"):
		return userLikeRedisTypeError()
	case strings.Contains(message, "LIKE_POST_TYPE"):
		return postLikeRedisTypeError()
	case strings.Contains(message, "LIKE_POST_DELETED"):
		return ErrPostLikeUnavailable
	case strings.Contains(message, "LIKE_USER_NOT_READY"):
		return userNotReadyError()
	case strings.Contains(message, "LIKE_POST_NOT_READY"):
		return postNotReadyError()
	case strings.Contains(message, "LIKE_NOT_READY"):
		return ErrNotReady
	case strings.Contains(message, "LIKE_RECOVERY_UNSAFE"):
		return ErrLikeRecoveryUnsafe
	case strings.Contains(message, "LIKE_RECOVERY_FENCE_LOST"):
		return ErrLikeRecoveryFenceLost
	case strings.Contains(message, "LIKE_COUNT_INCONSISTENT"):
		return ErrLikeCountInconsistent
	case strings.Contains(message, "LIKE_TYPE_PRECHECK"), strings.Contains(strings.ToUpper(message), "WRONGTYPE"):
		return fmt.Errorf("like Redis key type preflight failed: %w", ErrLikeRedisType)
	default:
		return err
	}
}

func parseInt64(value string) int64 {
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
}
