package recommendation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
)

const (
	snapshotRedisKeyPrefix  = "recommendation:pool:v2:"
	maxCursorBytes          = 2048
	defaultSnapshotMaxBytes = 1 << 20
)

var (
	ErrInvalidCursor       = errors.New("INVALID_CURSOR")
	ErrCursorExpired       = errors.New("CURSOR_EXPIRED")
	ErrSnapshotTooLarge    = errors.New("recommendation candidate snapshot exceeds size limit")
	ErrSnapshotUnavailable = errors.New("recommendation candidate snapshot is unavailable")
)

type snapshotCursorPayload struct {
	Version     int    `json:"v"`
	SnapshotID  string `json:"sid"`
	PageIndex   int    `json:"pi"`
	PageSize    int    `json:"ps"`
	OwnerDigest string `json:"owner"`
	ExpiresAt   int64  `json:"exp"`
}

type CursorCodec struct {
	key []byte
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("recommendation cursor signing key must contain at least 32 bytes")
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

func (codec *CursorCodec) OwnerDigest(viewer Viewer) string {
	if codec == nil {
		return ""
	}
	identity := ""
	switch viewer.Kind {
	case ViewerAuthenticated:
		identity = "user:" + strconv.FormatUint(uint64(viewer.UserID), 10)
	case ViewerGuest:
		identity = "guest:" + strings.TrimSpace(viewer.GuestSessionID)
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write([]byte("recommendation-owner-v2:" + identity))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (codec *CursorCodec) Encode(snapshotID string, pageIndex, pageSize int, viewer Viewer, expiresAt time.Time) (string, error) {
	if codec == nil || snapshotID == "" || pageIndex < 0 || pageSize <= 0 || expiresAt.IsZero() {
		return "", ErrInvalidCursor
	}
	payload := snapshotCursorPayload{
		Version: 2, SnapshotID: snapshotID, PageIndex: pageIndex, PageSize: pageSize,
		OwnerDigest: codec.OwnerDigest(viewer), ExpiresAt: expiresAt.UTC().Unix(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode recommendation cursor: %w", err)
	}
	message := base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write([]byte(message))
	cursor := message + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(cursor) > maxCursorBytes {
		return "", ErrInvalidCursor
	}
	return cursor, nil
}

func (codec *CursorCodec) Decode(cursor string, viewer Viewer, pageSize int, now time.Time) (snapshotCursorPayload, error) {
	if codec == nil || len(cursor) == 0 || len(cursor) > maxCursorBytes {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	var payload snapshotCursorPayload
	if err := json.Unmarshal(encoded, &payload); err != nil || payload.Version != 2 || payload.SnapshotID == "" || payload.PageIndex < 0 || payload.PageSize <= 0 {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	if payload.ExpiresAt <= now.UTC().Unix() {
		return snapshotCursorPayload{}, ErrCursorExpired
	}
	if payload.OwnerDigest != codec.OwnerDigest(viewer) || payload.PageSize != pageSize {
		return snapshotCursorPayload{}, ErrInvalidCursor
	}
	return payload, nil
}

type RedisSnapshotStore struct {
	client *redis.Client
}

func NewRedisSnapshotStore(client *redis.Client) (*RedisSnapshotStore, error) {
	if client == nil {
		return nil, errors.New("recommendation snapshot Redis client is nil")
	}
	return &RedisSnapshotStore{client: client}, nil
}

func (store *RedisSnapshotStore) Create(ctx context.Context, snapshot CandidatePoolSnapshot, ttl time.Duration, maxBytes int) error {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return errors.New("recommendation snapshot TTL must be positive")
	}
	snapshot.Version = candidatePoolSnapshotVersion
	snapshot.Revision = 1
	value, err := marshalSnapshot(snapshot, maxBytes)
	if err != nil {
		return err
	}
	created, err := client.SetNX(snapshotRedisKey(snapshot.ID), value, ttl).Result()
	if err != nil {
		return fmt.Errorf("%w: create recommendation snapshot: %v", ErrSnapshotUnavailable, err)
	}
	if !created {
		return fmt.Errorf("%w: recommendation snapshot ID collision", ErrSnapshotUnavailable)
	}
	return nil
}

func (store *RedisSnapshotStore) Load(ctx context.Context, snapshotID string) (CandidatePoolSnapshot, error) {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return CandidatePoolSnapshot{}, err
	}
	value, err := client.Get(snapshotRedisKey(snapshotID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return CandidatePoolSnapshot{}, ErrCursorExpired
		}
		return CandidatePoolSnapshot{}, fmt.Errorf("%w: load recommendation snapshot: %v", ErrSnapshotUnavailable, err)
	}
	return unmarshalSnapshot(value)
}

func (store *RedisSnapshotStore) CompareAndSwap(ctx context.Context, snapshotID string, expectedRevision int64, snapshot CandidatePoolSnapshot, maxBytes int) (bool, error) {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return false, err
	}
	remaining := time.Until(snapshot.ExpiresAt)
	if remaining <= 0 {
		return false, ErrCursorExpired
	}
	value, err := marshalSnapshot(snapshot, maxBytes)
	if err != nil {
		return false, err
	}
	const compareAndSwapScript = `
local current = redis.call('GET', KEYS[1])
if not current then return -1 end
local delimiter = string.find(current, '|', 1, true)
if not delimiter then return -2 end
local revision = tonumber(string.sub(current, 1, delimiter - 1))
if revision ~= tonumber(ARGV[1]) then return 0 end
redis.call('PSETEX', KEYS[1], ARGV[3], ARGV[2])
return 1
`
	result, err := client.Eval(compareAndSwapScript, []string{snapshotRedisKey(snapshotID)}, expectedRevision, value, remaining.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("%w: compare-and-swap recommendation snapshot: %v", ErrSnapshotUnavailable, err)
	}
	if result == -1 {
		return false, ErrCursorExpired
	}
	if result == -2 {
		return false, ErrSnapshotUnavailable
	}
	return result == 1, nil
}

func snapshotRedisContext(ctx context.Context, client *redis.Client) (*redis.Client, error) {
	if ctx == nil {
		return nil, errors.New("recommendation snapshot context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("recommendation snapshot Redis client is nil")
	}
	return client.WithContext(ctx), nil
}

func snapshotRedisKey(id string) string { return snapshotRedisKeyPrefix + id }

func marshalSnapshot(snapshot CandidatePoolSnapshot, maxBytes int) (string, error) {
	if snapshot.ID == "" || snapshot.Version != candidatePoolSnapshotVersion {
		return "", ErrSnapshotUnavailable
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal recommendation snapshot: %w", err)
	}
	value := strconv.FormatInt(snapshot.Revision, 10) + "|" + string(body)
	if maxBytes <= 0 {
		maxBytes = defaultSnapshotMaxBytes
	}
	if len(value) > maxBytes {
		return "", ErrSnapshotTooLarge
	}
	return value, nil
}

func unmarshalSnapshot(value string) (CandidatePoolSnapshot, error) {
	delimiter := strings.IndexByte(value, '|')
	if delimiter <= 0 {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	revision, err := strconv.ParseInt(value[:delimiter], 10, 64)
	if err != nil {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	var snapshot CandidatePoolSnapshot
	if err := json.Unmarshal([]byte(value[delimiter+1:]), &snapshot); err != nil {
		return CandidatePoolSnapshot{}, fmt.Errorf("decode recommendation snapshot: %w", err)
	}
	if snapshot.Version != candidatePoolSnapshotVersion || snapshot.ID == "" || snapshot.Revision != revision {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	return snapshot, nil
}

var _ SnapshotStore = (*RedisSnapshotStore)(nil)
