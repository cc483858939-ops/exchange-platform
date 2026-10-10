package likes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/metrics"
)

const maxUserLikeReadScriptBatch = 100

// PrimeUserLikeRestoreFinalizeScript loads the atomic restore-install script
// into Redis and returns its SHA, allowing isolated benchmarks to correlate
// the exact EVALSHA entry in Redis SLOWLOG.
func (s *Store) PrimeUserLikeRestoreFinalizeScript(ctx context.Context) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("redis is not initialized")
	}
	sha, err := finishUserLikeRestoreScript.Load(s.client.WithContext(ctx)).Result()
	if err != nil {
		return "", err
	}
	return sha, nil
}

type UserLikeRedisState struct {
	Status    string
	ExpiresAt time.Time
	TTL       time.Duration
}

type userLikeRestoreRelation struct {
	PostID         uint
	StateChangedAt time.Time
}

func boolIntString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func (s *Store) readUserLikeMembers(ctx context.Context, userID uint, postIDs []uint) (map[uint]bool, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("redis is not initialized")
	}
	result := make(map[uint]bool, len(postIDs))
	for start := 0; start < len(postIDs); start += maxUserLikeReadScriptBatch {
		end := min(start+maxUserLikeReadScriptBatch, len(postIDs))
		ids := postIDs[start:end]
		args := make([]interface{}, 0, len(ids))
		for _, postID := range ids {
			if postID == 0 {
				return nil, postNotReadyError()
			}
			args = append(args, strconv.FormatUint(uint64(postID), 10))
		}
		value, err := readUserLikeMembersScript.Run(
			s.client.WithContext(ctx), []string{UserLikesKey(userID), UserLikesOrderKey(userID)}, args...,
		).Result()
		if err != nil {
			return nil, mapScriptError(err)
		}
		values, ok := value.([]interface{})
		if !ok || len(values) != len(ids) {
			return nil, fmt.Errorf("unexpected User Like member response %T", value)
		}
		for index, postID := range ids {
			result[postID] = asInt64(values[index]) == 1
		}
	}
	return result, nil
}

func (s *Store) InspectUserLikeState(ctx context.Context, userID uint) (UserLikeRedisState, error) {
	if s == nil || s.client == nil {
		return UserLikeRedisState{}, errors.New("redis is not initialized")
	}
	if userID == 0 {
		return UserLikeRedisState{}, userNotReadyError()
	}
	value, err := inspectUserLikeStateScript.Run(
		s.client.WithContext(ctx),
		[]string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesRestoreLockKey(userID), UserLikesOrderKey(userID)},
		userID,
	).Result()
	if err != nil {
		return UserLikeRedisState{}, mapScriptError(err)
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 3 {
		return UserLikeRedisState{}, fmt.Errorf("unexpected User Like state response %T", value)
	}
	state := UserLikeRedisState{Status: asString(items[0])}
	if expiry := asString(items[1]); expiry != "" {
		millis, parseErr := strconv.ParseInt(expiry, 10, 64)
		if parseErr != nil || millis <= 0 {
			return UserLikeRedisState{}, ErrUserLikeRecoveryUnsafe
		}
		state.ExpiresAt = time.UnixMilli(millis)
	}
	pttl, parseErr := strconv.ParseInt(asString(items[2]), 10, 64)
	if parseErr == nil && pttl > 0 {
		state.TTL = time.Duration(pttl) * time.Millisecond
	} else if parseErr == nil && pttl < 0 {
		state.TTL = time.Duration(pttl) * time.Millisecond
	}
	return state, nil
}

// IsUserLikeCold classifies a missing relation set without attempting to
// rebuild it. The cleanup worker uses this to skip legal cold users.
func (s *Store) IsUserLikeCold(ctx context.Context, userID uint) (bool, error) {
	state, err := s.InspectUserLikeState(ctx, userID)
	if err != nil {
		return false, err
	}
	switch state.Status {
	case "cold", "busy":
		return true, nil
	case "unexpected_missing":
		return false, ErrUserLikeRecoveryUnsafe
	case "ready":
		return false, nil
	default:
		return false, ErrUserLikeRecoveryUnsafe
	}
}

func (s *Store) beginUserLikeRestore(ctx context.Context, userID uint, token string, lockTTL time.Duration) (string, string, error) {
	value, err := beginUserLikeRestoreScript.Run(
		s.client.WithContext(ctx),
		[]string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesRestoreLockKey(userID), UserLikesOrderKey(userID)},
		userID, token, lockTTL.Milliseconds(),
	).Result()
	if err != nil {
		return "", "", mapScriptError(err)
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 2 {
		return "", "", fmt.Errorf("unexpected User Like restore lock response %T", value)
	}
	return asString(items[0]), asString(items[1]), nil
}

func (s *Store) createUserLikeRestoreTemp(ctx context.Context, userID uint, token, expectedExpiry string, ttl time.Duration) (string, error) {
	key := UserLikesRestoreTempKey(userID, token)
	orderKey := UserLikesRestoreOrderTempKey(userID, token)
	if err := createUserLikeRestoreTempScript.Run(
		s.client.WithContext(ctx),
		[]string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesRestoreLockKey(userID), key, UserLikesOrderKey(userID), orderKey},
		userID, token, expectedExpiry, ttl.Milliseconds(),
	).Err(); err != nil {
		return "", mapScriptError(err)
	}
	return key, nil
}

func (s *Store) addUserLikeRestoreTempMembers(ctx context.Context, setKey, orderKey string, relations []userLikeRestoreRelation) error {
	if len(relations) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(relations)*2)
	for _, relation := range relations {
		if relation.PostID == 0 || relation.StateChangedAt.IsZero() {
			return ErrUserLikeRecoveryUnsafe
		}
		args = append(args, strconv.FormatUint(uint64(relation.PostID), 10), relation.StateChangedAt.UnixMicro())
	}
	_, err := appendUserLikeRestoreTempScript.Run(s.client.WithContext(ctx), []string{setKey, orderKey}, args...).Result()
	return mapScriptError(err)
}

func (s *Store) finishUserLikeRestore(
	ctx context.Context,
	userID uint,
	lockToken, expectedExpiry, tempKey string,
	relationCount int,
	settings config.UserLikeLifecycleConfig,
) (string, error) {
	value, err := finishUserLikeRestoreScript.Run(
		s.client.WithContext(ctx),
		[]string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesRestoreLockKey(userID), tempKey, UserLikesOrderKey(userID), UserLikesRestoreOrderTempKey(userID, lockToken)},
		userID, lockToken, expectedExpiry, relationCount, settings.RestoreMaxRelations,
		boolIntString(settings.ArmingEnabled), settings.SetTTL.Milliseconds(), boolIntString(settings.RestoreEnabled),
	).Result()
	if err != nil {
		return "", mapScriptError(err)
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 2 {
		return "", fmt.Errorf("unexpected User Like restore install response %T", value)
	}
	return asString(items[0]), nil
}

func (s *Store) releaseUserLikeRestore(ctx context.Context, userID uint, token, tempKey string) {
	if s == nil || s.client == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if tempKey != "" {
		_ = s.client.WithContext(cleanupCtx).Del(tempKey, UserLikesRestoreOrderTempKey(userID, token)).Err()
	}
	_ = releaseUserLikeRestoreLockScript.Run(
		s.client.WithContext(cleanupCtx), []string{UserLikesRestoreLockKey(userID)}, token,
	).Err()
}

func (s *Store) MigrateUserLikeTTL(ctx context.Context, userID uint) (string, error) {
	settings, err := s.lifecycleSettings()
	if err != nil {
		return "", err
	}
	return s.migrateUserLikeTTL(ctx, userID, settings.SetTTL)
}

func (s *Store) migrateUserLikeTTL(ctx context.Context, userID uint, ttl time.Duration) (string, error) {
	value, err := migrateUserLikeTTLScript.Run(
		s.client.WithContext(ctx), []string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesOrderKey(userID)},
		userID, ttl.Milliseconds(),
	).Result()
	if err != nil {
		return "", mapScriptError(err)
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 2 {
		return "", fmt.Errorf("unexpected User Like TTL migration response %T", value)
	}
	if asString(items[0]) == "armed" {
		metrics.RecordUserLikeTTLEvent("ttl_armed")
	}
	return asString(items[0]), nil
}

func (s *Store) RollbackUserLikeTTL(ctx context.Context, userID uint) (string, error) {
	value, err := rollbackUserLikeTTLScript.Run(
		s.client.WithContext(ctx), []string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesOrderKey(userID)}, userID,
	).Result()
	if err != nil {
		return "", mapScriptError(err)
	}
	items, ok := value.([]interface{})
	if !ok || len(items) != 2 {
		return "", fmt.Errorf("unexpected User Like TTL rollback response %T", value)
	}
	return asString(items[0]), nil
}

func (s *Store) CleanupOrphanUserLikeLedger(ctx context.Context, userID uint, expectedExpiry int64) error {
	if s == nil || s.client == nil || userID == 0 || expectedExpiry <= 0 {
		return ErrUserLikeRecoveryUnsafe
	}
	return mapScriptError(cleanupOrphanUserLikeLedgerScript.Run(
		s.client.WithContext(ctx), []string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesOrderKey(userID)},
		userID, expectedExpiry,
	).Err())
}

func (s *Store) CleanupUncommittedUserLikeInitialization(ctx context.Context, userID uint) error {
	if s == nil || s.client == nil || userID == 0 {
		return errors.New("invalid User Like initialization cleanup request")
	}
	return mapScriptError(cleanupUncommittedUserLikeInitScript.Run(
		s.client.WithContext(ctx), []string{UserLikesKey(userID), UserLikesExpiryLedgerKey, UserLikesOrderKey(userID)}, userID,
	).Err())
}
