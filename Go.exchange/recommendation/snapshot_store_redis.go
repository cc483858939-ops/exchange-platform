package recommendation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
)

const (
	snapshotRedisKeyPrefix              = "recommendation:pool:v5:"
	maxCursorBytes                      = 2048
	defaultSnapshotMaxStorageBytes      = 8 << 20
	defaultSnapshotMaxPoolBytes         = 1 << 20
	defaultSnapshotMaxPageBytes         = 64 << 10
	defaultSnapshotMaxMetadataBytes     = 64 << 10
	defaultSnapshotMaxContextBytes      = 512 << 10
	defaultSnapshotMaxPages             = 128
	defaultSnapshotMaxPageSize          = 50
	defaultSemanticHistoryWindowSize    = 128
	maxSnapshotContextEntries           = 512
	maxSnapshotInitialPages             = 8
	maxSnapshotAtomicPageDeletes        = 8
	maxSnapshotAtomicPostStateChanges   = 1024
	maxSnapshotHashFields               = 7000
	maxSnapshotKnownPostIDsPerOperation = 2000
)

var (
	ErrInvalidCursor       = errors.New("INVALID_CURSOR")
	ErrCursorExpired       = errors.New("CURSOR_EXPIRED")
	ErrSnapshotCapacity    = fmt.Errorf("%w: recommendation snapshot capacity reached", ErrCursorExpired)
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
	_, _ = mac.Write([]byte("recommendation-owner-v3:" + identity))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (codec *CursorCodec) Encode(snapshotID string, pageIndex, pageSize int, viewer Viewer, expiresAt time.Time) (string, error) {
	if codec == nil || snapshotID == "" || pageIndex < 0 || pageSize <= 0 || expiresAt.IsZero() {
		return "", ErrInvalidCursor
	}
	payload := snapshotCursorPayload{
		Version: 3, SnapshotID: snapshotID, PageIndex: pageIndex, PageSize: pageSize,
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
	if err := json.Unmarshal(encoded, &payload); err != nil || payload.Version != 3 || payload.SnapshotID == "" || payload.PageIndex < 0 || payload.PageSize <= 0 {
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

func (store *RedisSnapshotStore) Create(ctx context.Context, snapshot CandidatePoolSnapshot, pages []CachedPage, selectionContext []SnapshotContextEntry, ttl time.Duration) error {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return errors.New("recommendation snapshot TTL must be positive")
	}
	if snapshot.ID == "" || snapshot.ExpiresAt.IsZero() {
		return ErrSnapshotUnavailable
	}
	snapshot.Version = candidatePoolSnapshotVersion
	snapshot.Revision = 1
	snapshot.PageCount = len(pages)
	snapshot.ExpiresAtUnixMilli = snapshot.ExpiresAt.UTC().UnixMilli()
	if err := validateSnapshotLimits(snapshot); err != nil {
		return err
	}
	if len(pages) > maxSnapshotInitialPages {
		return ErrSnapshotCapacity
	}
	remaining := time.Until(snapshot.ExpiresAt)
	if remaining <= 0 {
		return ErrCursorExpired
	}
	if remaining > ttl {
		remaining = ttl
	}
	ttlMS := remaining.Milliseconds()
	if ttlMS < 1 {
		return ErrCursorExpired
	}

	metaJSON, poolJSON, err := marshalSnapshotState(snapshot)
	if err != nil {
		return err
	}
	contextJSON, err := marshalSnapshotContext(selectionContext)
	if err != nil {
		return err
	}
	pendingHistory := make([]int, 0)
	fields := []string{"meta", metaJSON, "pool", poolJSON, "context", contextJSON, "pending_history", "[]"}
	seenPages := make(map[int]struct{}, len(pages))
	seenIDs := make(map[uint]struct{}, len(pages)*snapshot.PageSize)
	for _, page := range pages {
		if page.Index < 0 || page.Index >= len(pages) || page.Index >= defaultSnapshotMaxPages || len(page.Items) > snapshot.PageSize {
			return ErrSnapshotUnavailable
		}
		if _, duplicate := seenPages[page.Index]; duplicate {
			return ErrSnapshotUnavailable
		}
		seenPages[page.Index] = struct{}{}
		pageJSON, err := marshalSnapshotPage(page)
		if err != nil {
			return err
		}
		fields = append(fields, snapshotPageField(page.Index), pageJSON)
		if snapshotPageCommitted(page) && !page.HistoryRecorded {
			pendingHistory = append(pendingHistory, page.Index)
		}
		states, err := pageInitialPostStates(page)
		if err != nil {
			return err
		}
		for _, state := range states {
			if _, duplicate := seenIDs[state.PostID]; duplicate {
				return fmt.Errorf("%w: duplicate post across prepared snapshot pages", ErrSnapshotUnavailable)
			}
			seenIDs[state.PostID] = struct{}{}
			fields = append(fields, snapshotPostField(state.PostID), state.Next)
		}
	}
	if len(seenPages) != len(pages) {
		return ErrSnapshotUnavailable
	}
	sort.Ints(pendingHistory)
	pendingJSON, _ := json.Marshal(pendingHistory)
	for index := 0; index < len(fields); index += 2 {
		if fields[index] == "pending_history" {
			fields[index+1] = string(pendingJSON)
			break
		}
	}
	fieldCount := len(fields)/2 + 1 // Include the _bytes field.
	bytesUsed := snapshotFieldBytes(fields)
	if fieldCount > maxSnapshotHashFields || bytesUsed > defaultSnapshotMaxStorageBytes {
		return ErrSnapshotCapacity
	}

	const createHashScript = `
if redis.call('EXISTS', KEYS[1]) ~= 0 then return 0 end
local ttl = tonumber(ARGV[1])
local maxBytes = tonumber(ARGV[2])
local maxFields = tonumber(ARGV[3])
local count = 0
local bytes = 0
for i = 4, #ARGV, 2 do
  local field = ARGV[i]
  local value = ARGV[i + 1]
  count = count + 1
  bytes = bytes + string.len(field) + string.len(value)
end
if count + 1 > maxFields or bytes > maxBytes then return -2 end
for i = 4, #ARGV, 2 do
  redis.call('HSET', KEYS[1], ARGV[i], ARGV[i + 1])
end
redis.call('HSET', KEYS[1], '_bytes', tostring(bytes))
redis.call('PEXPIRE', KEYS[1], ttl)
return 1
`
	args := []interface{}{ttlMS, defaultSnapshotMaxStorageBytes, maxSnapshotHashFields}
	for index := 0; index < len(fields); index += 2 {
		args = append(args, fields[index], fields[index+1])
	}
	result, err := client.Eval(createHashScript, []string{snapshotRedisKey(snapshot.ID)}, args...).Int()
	if err != nil {
		return fmt.Errorf("%w: create recommendation snapshot hash: %v", ErrSnapshotUnavailable, err)
	}
	if result == 0 {
		return fmt.Errorf("%w: recommendation snapshot ID collision", ErrSnapshotUnavailable)
	}
	if result == -2 {
		return ErrSnapshotCapacity
	}
	if result != 1 {
		return ErrSnapshotUnavailable
	}
	return nil
}

func (store *RedisSnapshotStore) Load(ctx context.Context, snapshotID string) (CandidatePoolSnapshot, error) {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return CandidatePoolSnapshot{}, err
	}
	values, err := client.HMGet(snapshotRedisKey(snapshotID), "meta", "pool").Result()
	if err != nil {
		return CandidatePoolSnapshot{}, fmt.Errorf("%w: load recommendation snapshot state: %v", ErrSnapshotUnavailable, err)
	}
	if len(values) != 2 || values[0] == nil {
		return CandidatePoolSnapshot{}, ErrCursorExpired
	}
	metaJSON, ok := redisValueString(values[0])
	if !ok || values[1] == nil {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	poolJSON, ok := redisValueString(values[1])
	if !ok {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	snapshot, err := unmarshalSnapshotState(metaJSON, poolJSON)
	if err != nil {
		return CandidatePoolSnapshot{}, err
	}
	if !snapshot.ExpiresAt.After(time.Now()) {
		return CandidatePoolSnapshot{}, ErrCursorExpired
	}
	return snapshot, nil
}

func (store *RedisSnapshotStore) LoadPage(ctx context.Context, snapshotID string, pageIndex int) (CachedPage, bool, error) {
	if pageIndex < 0 || pageIndex >= defaultSnapshotMaxPages {
		return CachedPage{}, false, ErrSnapshotUnavailable
	}
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return CachedPage{}, false, err
	}
	value, err := client.HGet(snapshotRedisKey(snapshotID), snapshotPageField(pageIndex)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			if _, loadErr := store.Load(ctx, snapshotID); loadErr != nil {
				return CachedPage{}, false, loadErr
			}
			return CachedPage{}, false, nil
		}
		return CachedPage{}, false, fmt.Errorf("%w: load recommendation snapshot page: %v", ErrSnapshotUnavailable, err)
	}
	page, err := unmarshalSnapshotPage(value, pageIndex)
	if err != nil {
		return CachedPage{}, false, err
	}
	return page, true, nil
}

func (store *RedisSnapshotStore) LoadContext(ctx context.Context, snapshotID string, beforePageIndex int) ([]SnapshotContextEntry, error) {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return nil, err
	}
	values, err := client.HMGet(snapshotRedisKey(snapshotID), "meta", "context").Result()
	if err != nil {
		return nil, fmt.Errorf("%w: load recommendation snapshot context: %v", ErrSnapshotUnavailable, err)
	}
	if len(values) != 2 || values[0] == nil {
		return nil, ErrCursorExpired
	}
	metaJSON, ok := redisValueString(values[0])
	if !ok || values[1] == nil {
		return nil, ErrSnapshotUnavailable
	}
	meta, err := unmarshalSnapshotMetadata(metaJSON)
	if err != nil {
		return nil, err
	}
	if !meta.ExpiresAt.After(time.Now()) {
		return nil, ErrCursorExpired
	}
	contextJSON, ok := redisValueString(values[1])
	if !ok {
		return nil, ErrSnapshotUnavailable
	}
	var entries []SnapshotContextEntry
	if err := json.Unmarshal([]byte(contextJSON), &entries); err != nil || len(entries) > maxSnapshotContextEntries {
		return nil, fmt.Errorf("%w: decode recommendation snapshot context", ErrSnapshotUnavailable)
	}
	result := make([]SnapshotContextEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.PageIndex < 0 || entry.PageIndex >= meta.PageCount || entry.Candidate.PostID == 0 {
			return nil, fmt.Errorf("%w: invalid recommendation snapshot context entry", ErrSnapshotUnavailable)
		}
		if entry.PageIndex < beforePageIndex {
			result = append(result, entry)
		}
	}
	return result, nil
}

func (store *RedisSnapshotStore) LoadPendingHistory(ctx context.Context, snapshotID string, limit int) ([]CachedPage, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > 32 {
		limit = 32
	}
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return nil, err
	}
	values, err := client.HMGet(snapshotRedisKey(snapshotID), "meta", "pending_history").Result()
	if err != nil {
		return nil, fmt.Errorf("%w: load pending recommendation history: %v", ErrSnapshotUnavailable, err)
	}
	if len(values) != 2 || values[0] == nil {
		return nil, ErrCursorExpired
	}
	metaJSON, ok := redisValueString(values[0])
	if !ok || values[1] == nil {
		return nil, ErrSnapshotUnavailable
	}
	meta, err := unmarshalSnapshotMetadata(metaJSON)
	if err != nil {
		return nil, err
	}
	if !meta.ExpiresAt.After(time.Now()) {
		return nil, ErrCursorExpired
	}
	pendingJSON, ok := redisValueString(values[1])
	if !ok {
		return nil, ErrSnapshotUnavailable
	}
	var indexes []int
	if err := json.Unmarshal([]byte(pendingJSON), &indexes); err != nil || len(indexes) > defaultSnapshotMaxPages {
		return nil, fmt.Errorf("%w: decode pending recommendation history index", ErrSnapshotUnavailable)
	}
	if len(indexes) > limit {
		indexes = indexes[:limit]
	}
	if len(indexes) == 0 {
		return nil, nil
	}
	fields := make([]string, len(indexes))
	for index, pageIndex := range indexes {
		if pageIndex < 0 || pageIndex >= meta.PageCount {
			return nil, fmt.Errorf("%w: invalid pending recommendation history page index", ErrSnapshotUnavailable)
		}
		fields[index] = snapshotPageField(pageIndex)
	}
	pageValues, err := client.HMGet(snapshotRedisKey(snapshotID), fields...).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: load pending recommendation history pages: %v", ErrSnapshotUnavailable, err)
	}
	pages := make([]CachedPage, 0, len(indexes))
	for index, value := range pageValues {
		if value == nil {
			return nil, fmt.Errorf("%w: pending recommendation page is missing", ErrSnapshotUnavailable)
		}
		encoded, ok := redisValueString(value)
		if !ok {
			return nil, ErrSnapshotUnavailable
		}
		page, err := unmarshalSnapshotPage(encoded, indexes[index])
		if err != nil {
			return nil, err
		}
		if !snapshotPageCommitted(page) || page.HistoryRecorded {
			return nil, fmt.Errorf("%w: invalid pending recommendation page state", ErrSnapshotUnavailable)
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func (store *RedisSnapshotStore) KnownPostIDs(ctx context.Context, snapshotID string, postIDs []uint) (map[uint]struct{}, error) {
	if len(postIDs) > maxSnapshotKnownPostIDsPerOperation {
		return nil, ErrSnapshotCapacity
	}
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return nil, err
	}
	fields := make([]string, len(postIDs)+1)
	fields[0] = "meta"
	for index, id := range postIDs {
		fields[index+1] = snapshotPostField(id)
	}
	values, err := client.HMGet(snapshotRedisKey(snapshotID), fields...).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: check recommendation snapshot post state: %v", ErrSnapshotUnavailable, err)
	}
	if len(values) != len(fields) || values[0] == nil {
		return nil, ErrCursorExpired
	}
	metaJSON, ok := redisValueString(values[0])
	if !ok {
		return nil, ErrSnapshotUnavailable
	}
	meta, err := unmarshalSnapshotMetadata(metaJSON)
	if err != nil {
		return nil, err
	}
	if !meta.ExpiresAt.After(time.Now()) {
		return nil, ErrCursorExpired
	}
	known := make(map[uint]struct{})
	for index, value := range values[1:] {
		if value != nil {
			known[postIDs[index]] = struct{}{}
		}
	}
	return known, nil
}

func marshalSnapshotMutationLists(mutation SnapshotMutation) (string, string, error) {
	deletePageIndices := mutation.DeletePageIndices
	if deletePageIndices == nil {
		deletePageIndices = []int{}
	}
	postStates := mutation.PostStates
	if postStates == nil {
		postStates = []SnapshotPostStateUpdate{}
	}
	deleteJSON, err := json.Marshal(deletePageIndices)
	if err != nil {
		return "", "", fmt.Errorf("marshal snapshot page deletes: %w", err)
	}
	postStateJSON, err := json.Marshal(postStates)
	if err != nil {
		return "", "", fmt.Errorf("marshal snapshot post states: %w", err)
	}
	return string(deleteJSON), string(postStateJSON), nil
}

func (store *RedisSnapshotStore) Apply(ctx context.Context, snapshotID string, expectedRevision int64, mutation SnapshotMutation) (bool, error) {
	client, err := snapshotRedisContext(ctx, store.client)
	if err != nil {
		return false, err
	}
	snapshot := mutation.Snapshot
	snapshot.ExpiresAtUnixMilli = snapshot.ExpiresAt.UTC().UnixMilli()
	if snapshot.ID != snapshotID || snapshot.Version != candidatePoolSnapshotVersion ||
		snapshot.Revision != expectedRevision+1 {
		return false, ErrSnapshotUnavailable
	}
	if err := validateSnapshotLimits(snapshot); err != nil {
		return false, err
	}
	metaJSON, poolJSON, err := marshalSnapshotState(snapshot)
	if err != nil {
		return false, err
	}
	var contextJSON string
	contextSet := "0"
	if mutation.Context != nil {
		contextJSON, err = marshalSnapshotContext(*mutation.Context)
		if err != nil {
			return false, err
		}
		contextSet = "1"
	}
	pageField, pageJSON := "", ""
	if mutation.Page != nil {
		page := *mutation.Page
		if page.Index < 0 || page.Index >= snapshot.PageCount || len(page.Items) > snapshot.PageSize {
			return false, ErrSnapshotUnavailable
		}
		pageField = snapshotPageField(page.Index)
		pageJSON, err = marshalSnapshotPage(page)
		if err != nil {
			return false, err
		}
	}
	if len(mutation.DeletePageIndices) > maxSnapshotAtomicPageDeletes ||
		len(mutation.PostStates) > maxSnapshotAtomicPostStateChanges {
		return false, ErrSnapshotCapacity
	}
	seenDeletes := make(map[int]struct{}, len(mutation.DeletePageIndices))
	for _, index := range mutation.DeletePageIndices {
		if index < 0 || index >= defaultSnapshotMaxPages || index == pageIndexOf(mutation.Page) {
			return false, ErrSnapshotUnavailable
		}
		if _, duplicate := seenDeletes[index]; duplicate {
			return false, ErrSnapshotUnavailable
		}
		seenDeletes[index] = struct{}{}
	}
	seenPostIDs := make(map[uint]struct{}, len(mutation.PostStates))
	for _, change := range mutation.PostStates {
		if change.PostID == 0 {
			return false, ErrSnapshotUnavailable
		}
		if _, duplicate := seenPostIDs[change.PostID]; duplicate {
			return false, ErrSnapshotUnavailable
		}
		seenPostIDs[change.PostID] = struct{}{}
	}
	if mutation.PendingHistoryChange != SnapshotPendingHistoryUnchanged {
		if mutation.PendingHistoryChange != SnapshotPendingHistoryAdd && mutation.PendingHistoryChange != SnapshotPendingHistoryRemove {
			return false, ErrSnapshotUnavailable
		}
		if mutation.PendingHistoryPage < 0 || mutation.PendingHistoryPage >= snapshot.PageCount {
			return false, ErrSnapshotUnavailable
		}
	}
	deleteJSON, postStateJSON, err := marshalSnapshotMutationLists(mutation)
	if err != nil {
		return false, err
	}
	nowMS := time.Now().UnixMilli()

	const applyScript = `
if redis.call('EXISTS', KEYS[1]) == 0 then return -1 end
local expected = tonumber(ARGV[1])
local nowMs = tonumber(ARGV[2])
local currentRaw = redis.call('HGET', KEYS[1], 'meta')
if not currentRaw then return -2 end
local okCurrent, current = pcall(cjson.decode, currentRaw)
if not okCurrent or type(current) ~= 'table' then return -2 end
local okNext, nextMeta = pcall(cjson.decode, ARGV[3])
if not okNext or type(nextMeta) ~= 'table' then return -2 end
if tonumber(current.revision) ~= expected then return 0 end
if tonumber(current.expires_at_unix_milli or 0) <= nowMs or redis.call('PTTL', KEYS[1]) <= 0 then return -1 end
if tonumber(nextMeta.revision) ~= expected + 1 or tonumber(nextMeta.version) ~= 5 then return -2 end
if nextMeta.id ~= current.id or nextMeta.owner_digest ~= current.owner_digest or
   nextMeta.expires_at_unix_milli ~= current.expires_at_unix_milli or nextMeta.page_size ~= current.page_size or
   nextMeta.embedding_version ~= current.embedding_version or nextMeta.ranker_config_hash ~= current.ranker_config_hash or
   nextMeta.strategy_version ~= current.strategy_version or nextMeta.language_key ~= current.language_key then return -2 end
local newPageCount = tonumber(nextMeta.page_count)
if not newPageCount or newPageCount < 0 or newPageCount > 128 or tonumber(nextMeta.page_size) > 50 then return -4 end
local poolOK, pool = pcall(cjson.decode, ARGV[4])
if not poolOK or type(pool) ~= 'table' or #pool > 500 then return -2 end

local updates = {}
local deletes = {}
local bytes = tonumber(redis.call('HGET', KEYS[1], '_bytes') or '')
if not bytes then return -2 end
local fieldCount = redis.call('HLEN', KEYS[1])
local function setField(field, value)
  local old = redis.call('HGET', KEYS[1], field)
  if old then
    bytes = bytes - string.len(field) - string.len(old)
  else
    fieldCount = fieldCount + 1
  end
  bytes = bytes + string.len(field) + string.len(value)
  updates[#updates + 1] = field
  updates[#updates + 1] = value
end
local function deleteField(field)
  local old = redis.call('HGET', KEYS[1], field)
  if old then
    bytes = bytes - string.len(field) - string.len(old)
    fieldCount = fieldCount - 1
    deletes[#deletes + 1] = field
  end
end

setField('meta', ARGV[3])
setField('pool', ARGV[4])
if ARGV[5] == '1' then setField('context', ARGV[6]) end
if ARGV[7] ~= '' then
  local pageOK, page = pcall(cjson.decode, ARGV[8])
  if not pageOK or type(page) ~= 'table' or tonumber(page.index) == nil or type(page.items) ~= 'table' or #page.items > tonumber(nextMeta.page_size) then return -2 end
  local fieldIndex = tonumber(string.match(ARGV[7], '^page:(%d+)$'))
  if not fieldIndex or fieldIndex < 0 or fieldIndex >= newPageCount or tonumber(page.index) ~= fieldIndex then return -2 end
  setField(ARGV[7], ARGV[8])
end

local deleteOK, pageDeletes = pcall(cjson.decode, ARGV[9])
if not deleteOK or type(pageDeletes) ~= 'table' or #pageDeletes > 8 then return -2 end
for _, pageIndex in ipairs(pageDeletes) do
  pageIndex = tonumber(pageIndex)
  if not pageIndex or pageIndex < 0 or pageIndex > 127 then return -2 end
  local field = 'page:' .. tostring(pageIndex)
  local pageRaw = redis.call('HGET', KEYS[1], field)
  if pageRaw then
    local pageOK, page = pcall(cjson.decode, pageRaw)
    if not pageOK or type(page) ~= 'table' then return -2 end
    if page.committed == true or page.finalized == true then return -2 end
    deleteField(field)
  end
end

local stateOK, postStates = pcall(cjson.decode, ARGV[10])
if not stateOK or type(postStates) ~= 'table' or #postStates > 1024 then return -2 end
for _, change in ipairs(postStates) do
  local postID = tonumber(change.PostID)
  if not postID or postID <= 0 then return -2 end
  local field = 'post:' .. string.format('%.0f', postID)
  local currentState = redis.call('HGET', KEYS[1], field)
  local expectedState = change.Expected or ''
  if expectedState == '' then
    if currentState then return 0 end
  elseif currentState ~= expectedState then
    return 0
  end
end
for _, change in ipairs(postStates) do
  local postID = tonumber(change.PostID)
  local field = 'post:' .. string.format('%.0f', postID)
  local nextState = change.Next or ''
  if nextState == '' then deleteField(field) else setField(field, nextState) end
end

local pendingOp = tonumber(ARGV[11])
if pendingOp ~= 0 then
  local pendingRaw = redis.call('HGET', KEYS[1], 'pending_history')
  if not pendingRaw then return -2 end
  local pendingOK, pending = pcall(cjson.decode, pendingRaw)
  if not pendingOK or type(pending) ~= 'table' or #pending > 128 then return -2 end
  local pageIndex = tonumber(ARGV[12])
  if not pageIndex or pageIndex < 0 or pageIndex >= newPageCount then return -2 end
  local found = false
  for index, value in ipairs(pending) do
    if tonumber(value) == pageIndex then
      found = true
      if pendingOp == -1 then table.remove(pending, index) end
      break
    end
  end
  if pendingOp == 1 and not found then pending[#pending + 1] = pageIndex end
  if #pending > 128 then return -4 end
  table.sort(pending)
  -- Redis cjson encodes an empty Lua table as {}, so preserve the array shape.
  if #pending == 0 then
    setField('pending_history', '[]')
  else
    setField('pending_history', cjson.encode(pending))
  end
end
if bytes < 0 or bytes > tonumber(ARGV[13]) or fieldCount > tonumber(ARGV[14]) then return -4 end
for _, field in ipairs(deletes) do redis.call('HDEL', KEYS[1], field) end
for index = 1, #updates, 2 do redis.call('HSET', KEYS[1], updates[index], updates[index + 1]) end
redis.call('HSET', KEYS[1], '_bytes', tostring(bytes))
return 1
`
	args := []interface{}{
		expectedRevision, nowMS, metaJSON, poolJSON, contextSet, contextJSON,
		pageField, pageJSON, deleteJSON, postStateJSON,
		int(mutation.PendingHistoryChange), mutation.PendingHistoryPage,
		defaultSnapshotMaxStorageBytes, maxSnapshotHashFields,
	}
	result, err := client.Eval(applyScript, []string{snapshotRedisKey(snapshotID)}, args...).Int()
	if err != nil {
		return false, fmt.Errorf("%w: apply recommendation snapshot mutation: %v", ErrSnapshotUnavailable, err)
	}
	switch result {
	case -1:
		return false, ErrCursorExpired
	case -2:
		return false, ErrSnapshotUnavailable
	case -4:
		return false, ErrSnapshotCapacity
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, ErrSnapshotUnavailable
	}
}

func pageInitialPostStates(page CachedPage) ([]SnapshotPostStateUpdate, error) {
	states := make([]SnapshotPostStateUpdate, 0, len(page.Items))
	seen := make(map[uint]struct{}, len(page.Items))
	if snapshotPageCommitted(page) {
		returned := make(map[uint]struct{}, len(page.ReturnedPostIDs))
		for _, id := range page.ReturnedPostIDs {
			returned[id] = struct{}{}
		}
		for _, item := range page.Items {
			if item.PostID == 0 {
				continue
			}
			if _, duplicate := seen[item.PostID]; duplicate {
				return nil, ErrSnapshotUnavailable
			}
			seen[item.PostID] = struct{}{}
			next := snapshotPostState("rejected", page.Index)
			if _, ok := returned[item.PostID]; ok {
				next = snapshotPostState("used", page.Index)
			}
			states = append(states, SnapshotPostStateUpdate{PostID: item.PostID, Next: next})
		}
		return states, nil
	}
	for _, item := range page.Items {
		if item.PostID == 0 {
			continue
		}
		if _, duplicate := seen[item.PostID]; duplicate {
			return nil, ErrSnapshotUnavailable
		}
		seen[item.PostID] = struct{}{}
		states = append(states, SnapshotPostStateUpdate{PostID: item.PostID, Next: snapshotPostState("reserved", page.Index)})
	}
	return states, nil
}

func snapshotPostState(kind string, pageIndex int) string {
	return fmt.Sprintf("%s:%d", kind, pageIndex)
}

func snapshotPageField(index int) string { return "page:" + strconv.Itoa(index) }

func snapshotPostField(id uint) string { return "post:" + strconv.FormatUint(uint64(id), 10) }

func pageIndexOf(page *CachedPage) int {
	if page == nil {
		return -1
	}
	return page.Index
}

func validateSnapshotLimits(snapshot CandidatePoolSnapshot) error {
	if snapshot.ID == "" || snapshot.PageSize <= 0 || snapshot.PageSize > defaultSnapshotMaxPageSize ||
		snapshot.PageCount < 0 || snapshot.PageCount > defaultSnapshotMaxPages ||
		len(snapshot.Candidates) > 500 {
		return ErrSnapshotCapacity
	}
	return nil
}

func marshalSnapshotState(snapshot CandidatePoolSnapshot) (string, string, error) {
	if err := validateSnapshotLimits(snapshot); err != nil {
		return "", "", err
	}
	snapshot.ExpiresAtUnixMilli = snapshot.ExpiresAt.UTC().UnixMilli()
	meta, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", fmt.Errorf("marshal recommendation snapshot metadata: %w", err)
	}
	candidates := snapshot.Candidates
	if candidates == nil {
		candidates = []CachedCandidate{}
	}
	pool, err := json.Marshal(candidates)
	if err != nil {
		return "", "", fmt.Errorf("marshal recommendation candidate pool: %w", err)
	}
	if len(meta) > defaultSnapshotMaxMetadataBytes || len(pool) > defaultSnapshotMaxPoolBytes {
		return "", "", ErrSnapshotCapacity
	}
	return string(meta), string(pool), nil
}

func unmarshalSnapshotMetadata(metaJSON string) (CandidatePoolSnapshot, error) {
	var snapshot CandidatePoolSnapshot
	if err := json.Unmarshal([]byte(metaJSON), &snapshot); err != nil {
		return CandidatePoolSnapshot{}, fmt.Errorf("%w: decode recommendation snapshot metadata: %v", ErrSnapshotUnavailable, err)
	}
	if snapshot.Version != candidatePoolSnapshotVersion || snapshot.ID == "" || snapshot.Revision <= 0 ||
		snapshot.PageCount < 0 || snapshot.PageCount > defaultSnapshotMaxPages ||
		snapshot.PageSize <= 0 || snapshot.PageSize > defaultSnapshotMaxPageSize ||
		snapshot.ExpiresAt.IsZero() || snapshot.ExpiresAtUnixMilli != snapshot.ExpiresAt.UTC().UnixMilli() {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	return snapshot, nil
}

func unmarshalSnapshotState(metaJSON, poolJSON string) (CandidatePoolSnapshot, error) {
	snapshot, err := unmarshalSnapshotMetadata(metaJSON)
	if err != nil {
		return CandidatePoolSnapshot{}, err
	}
	if err := json.Unmarshal([]byte(poolJSON), &snapshot.Candidates); err != nil {
		return CandidatePoolSnapshot{}, fmt.Errorf("%w: decode recommendation candidate pool: %v", ErrSnapshotUnavailable, err)
	}
	if len(snapshot.Candidates) > 500 {
		return CandidatePoolSnapshot{}, ErrSnapshotUnavailable
	}
	return snapshot, nil
}

func marshalSnapshotPage(page CachedPage) (string, error) {
	if page.Items == nil {
		page.Items = []CachedCandidate{}
	}
	if page.ReturnedPostIDs == nil {
		page.ReturnedPostIDs = []uint{}
	}
	value, err := json.Marshal(page)
	if err != nil {
		return "", fmt.Errorf("marshal recommendation snapshot page: %w", err)
	}
	if len(value) > defaultSnapshotMaxPageBytes {
		return "", ErrSnapshotCapacity
	}
	return string(value), nil
}

func unmarshalSnapshotPage(value string, expectedIndex int) (CachedPage, error) {
	if len(value) > defaultSnapshotMaxPageBytes {
		return CachedPage{}, ErrSnapshotUnavailable
	}
	var page CachedPage
	if err := json.Unmarshal([]byte(value), &page); err != nil {
		return CachedPage{}, fmt.Errorf("%w: decode recommendation snapshot page: %v", ErrSnapshotUnavailable, err)
	}
	if page.Index != expectedIndex || page.Index < 0 || page.Index >= defaultSnapshotMaxPages ||
		len(page.Items) > defaultSnapshotMaxPageSize {
		return CachedPage{}, ErrSnapshotUnavailable
	}
	return page, nil
}

func marshalSnapshotContext(entries []SnapshotContextEntry) (string, error) {
	if len(entries) > maxSnapshotContextEntries {
		entries = append([]SnapshotContextEntry(nil), entries[len(entries)-maxSnapshotContextEntries:]...)
	}
	if entries == nil {
		entries = []SnapshotContextEntry{}
	}
	value, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshal recommendation snapshot context: %w", err)
	}
	if len(value) > defaultSnapshotMaxContextBytes {
		return "", ErrSnapshotCapacity
	}
	return string(value), nil
}

func snapshotFieldBytes(fields []string) int {
	bytes := 0
	for index := 0; index < len(fields); index += 2 {
		bytes += len(fields[index]) + len(fields[index+1])
	}
	return bytes
}

func redisValueString(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
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

var _ SnapshotStore = (*RedisSnapshotStore)(nil)
