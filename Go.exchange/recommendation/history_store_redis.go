package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
)

const (
	userHistoryKeyPrefix        = "recommendation:user:served:v1:"
	guestHistoryKeyPrefix       = "recommendation:guest:served:v1:"
	pageHistoryReceiptKeyPrefix = "recommendation:user:served-page:v1:"
)

type RedisHistoryStore struct {
	client *redis.Client
}

func NewRedisHistoryStore(client *redis.Client) (*RedisHistoryStore, error) {
	if client == nil {
		return nil, errors.New("recommendation history Redis client is nil")
	}
	return &RedisHistoryStore{client: client}, nil
}

func (s *RedisHistoryStore) LoadUserHistory(ctx context.Context, userID uint, window HistoryWindow) (ServedHistory, error) {
	history := make(ServedHistory)
	if userID == 0 {
		return history, nil
	}
	client, err := recommendationRedisContext(ctx, s.client)
	if err != nil {
		return nil, fmt.Errorf("load user served history: %w", err)
	}
	entries, err := client.ZRevRangeByScoreWithScores(userHistoryKey(userID), &redis.ZRangeBy{
		Min: "(" + strconv.FormatInt(window.SoftStart.Unix(), 10), Max: "+inf", Count: int64(window.Limit),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("load user served history: %w", err)
	}
	return parseServedHistory(entries, window), nil
}

func (s *RedisHistoryStore) RecordUserServed(ctx context.Context, userID uint, postIDs []uint, window HistoryWindow) error {
	if userID == 0 || len(historyMembers(postIDs, 0)) == 0 {
		return nil
	}
	client, err := recommendationRedisContext(ctx, s.client)
	if err != nil {
		return fmt.Errorf("record user served history: %w", err)
	}
	return recordServedHistory(ctx, client, userHistoryKey(userID), postIDs, window, strconv.FormatInt(window.SoftStart.Unix(), 10))
}

func (s *RedisHistoryStore) RecordUserServedForPage(ctx context.Context, userID uint, pageKey string, postIDs []uint, window HistoryWindow) error {
	if userID == 0 || strings.TrimSpace(pageKey) == "" || len(historyMembers(postIDs, 0)) == 0 {
		return nil
	}
	if window.Now.IsZero() || window.Limit <= 0 || window.TTL <= 0 {
		return errors.New("recommendation page history window is invalid")
	}
	client, err := recommendationRedisContext(ctx, s.client)
	if err != nil {
		return fmt.Errorf("record user served page history: %w", err)
	}
	members := historyMembers(postIDs, float64(window.Now.UTC().Unix()))
	receiptKey := userPageHistoryReceiptKey(pageKey)
	args := []interface{}{
		strconv.FormatInt(window.SoftStart.Unix(), 10), window.Limit,
		window.TTL.Milliseconds(),
	}
	for _, member := range members {
		args = append(args, member.Score, member.Member)
	}
	const recordPageScript = `
if redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
local limit = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
for index = 4, #ARGV, 2 do
  redis.call('ZADD', KEYS[1], ARGV[index], ARGV[index + 1])
end
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local cardinality = redis.call('ZCARD', KEYS[1])
if cardinality > limit then
  redis.call('ZREMRANGEBYRANK', KEYS[1], 0, cardinality - limit - 1)
end
redis.call('PEXPIRE', KEYS[1], ttl_ms)
redis.call('SET', KEYS[2], '1', 'PX', ttl_ms)
return 1
`
	if _, err := client.Eval(recordPageScript, []string{userHistoryKey(userID), receiptKey}, args...).Int(); err != nil {
		return fmt.Errorf("record user served page history: %w", err)
	}
	return nil
}

func (s *RedisHistoryStore) LoadGuestHistory(ctx context.Context, sessionID string, window HistoryWindow) (ServedHistory, error) {
	history := make(ServedHistory)
	if strings.TrimSpace(sessionID) == "" {
		return history, nil
	}
	client, err := recommendationRedisContext(ctx, s.client)
	if err != nil {
		return nil, fmt.Errorf("load guest served history: %w", err)
	}
	entries, err := client.ZRevRangeByScoreWithScores(guestHistoryKey(sessionID), &redis.ZRangeBy{
		Min: strconv.FormatInt(window.SoftStart.Unix(), 10), Max: "+inf", Count: int64(window.Limit),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("load guest served history: %w", err)
	}
	return parseServedHistory(entries, window), nil
}

func (s *RedisHistoryStore) RecordGuestServed(ctx context.Context, sessionID string, postIDs []uint, window HistoryWindow) error {
	if strings.TrimSpace(sessionID) == "" || len(historyMembers(postIDs, 0)) == 0 {
		return nil
	}
	client, err := recommendationRedisContext(ctx, s.client)
	if err != nil {
		return fmt.Errorf("record guest served history: %w", err)
	}
	return recordServedHistory(ctx, client, guestHistoryKey(sessionID), postIDs, window, "("+strconv.FormatInt(window.SoftStart.Unix(), 10))
}

func recommendationRedisContext(ctx context.Context, client *redis.Client) (*redis.Client, error) {
	if ctx == nil {
		return nil, errors.New("recommendation history context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("recommendation history Redis client is nil")
	}
	return client.WithContext(ctx), nil
}

func userHistoryKey(userID uint) string {
	return userHistoryKeyPrefix + strconv.FormatUint(uint64(userID), 10)
}

func guestHistoryKey(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return guestHistoryKeyPrefix + hex.EncodeToString(digest[:])
}

func userPageHistoryReceiptKey(pageKey string) string {
	digest := sha256.Sum256([]byte(pageKey))
	return pageHistoryReceiptKeyPrefix + hex.EncodeToString(digest[:])
}

func parseServedHistory(entries []redis.Z, window HistoryWindow) ServedHistory {
	history := make(ServedHistory)
	for _, entry := range entries {
		postID, err := strconv.ParseUint(strings.TrimSpace(fmt.Sprint(entry.Member)), 10, 64)
		if err != nil || postID == 0 || uint64(uint(postID)) != postID {
			continue
		}
		item, active := ClassifyServedAt(uint(postID), time.Unix(int64(entry.Score), 0), window)
		if active {
			history[uint(postID)] = item
		}
	}
	return history
}

func recordServedHistory(ctx context.Context, client *redis.Client, key string, postIDs []uint, window HistoryWindow, trimMax string) error {
	if window.Now.IsZero() {
		return errors.New("recommendation history timestamp is required")
	}
	if window.Limit <= 0 {
		return errors.New("recommendation history limit must be positive")
	}
	if window.TTL <= 0 {
		return errors.New("recommendation history TTL must be positive")
	}
	members := historyMembers(postIDs, float64(window.Now.UTC().Unix()))
	if len(members) == 0 {
		return nil
	}
	pipe := client.TxPipeline()
	pipe.ZAdd(key, members...)
	pipe.ZRemRangeByScore(key, "-inf", trimMax)
	cardinality := pipe.ZCard(key)
	pipe.Expire(key, window.TTL)
	if _, err := pipe.ExecContext(ctx); err != nil {
		return fmt.Errorf("write recommendation served history: %w", err)
	}
	if cardinality.Val() > int64(window.Limit) {
		if err := client.ZRemRangeByRank(key, 0, cardinality.Val()-int64(window.Limit)-1).Err(); err != nil {
			return fmt.Errorf("trim recommendation served history: %w", err)
		}
		if err := client.Expire(key, window.TTL).Err(); err != nil {
			return fmt.Errorf("refresh recommendation served history TTL: %w", err)
		}
	}
	return nil
}

func historyMembers(postIDs []uint, score float64) []*redis.Z {
	seen := make(map[uint]struct{}, len(postIDs))
	members := make([]*redis.Z, 0, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		if _, exists := seen[postID]; exists {
			continue
		}
		seen[postID] = struct{}{}
		members = append(members, &redis.Z{Score: score, Member: strconv.FormatUint(uint64(postID), 10)})
	}
	return members
}

var _ HistoryStore = (*RedisHistoryStore)(nil)
