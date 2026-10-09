package likes

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
)

// This opt-in comparison deliberately flushes one dedicated logical Redis DB.
// See REDIS_RELATION_BENCHMARK.md before setting REDIS_LIKE_BENCH_ALLOW_FLUSHDB=1.
var legacyPostUserMutationBenchmarkScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end
local ready = redis.call('GET', KEYS[1])
if ready == 'deleted' then return redis.error_reply('LIKE_POST_DELETED') end
if ready ~= '1' then return redis.error_reply('LIKE_NOT_READY') end
local count_raw = redis.call('GET', KEYS[2])
local version_raw = redis.call('GET', KEYS[4])
if not count_raw or not version_raw or not string.match(count_raw, '^%d+$') or not string.match(version_raw, '^%d+$') then
  return redis.error_reply('LIKE_NOT_READY')
end
local count = tonumber(count_raw)
local version = tonumber(version_raw)
if not count or count < 0 or not version or version < 0 then return redis.error_reply('LIKE_NOT_READY') end
if redis.call('SCARD', KEYS[3]) ~= count then return redis.error_reply('LIKE_NOT_READY') end
local current = redis.call('SISMEMBER', KEYS[3], ARGV[2])
local desired = ARGV[3] == '1' and 1 or 0
local changed = (desired == 1 and current == 0) or (desired == 0 and current == 1)
if changed then
  if not type_matches(KEYS[5], 'set') or not type_matches(KEYS[6], 'set') or
     not type_matches(KEYS[7], 'hash') or not type_matches(KEYS[8], 'set') or
     not type_matches(KEYS[9], 'zset') or not type_matches(KEYS[10], 'hash') then
    return redis.error_reply('LIKE_TYPE_PRECHECK')
  end
  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[2])
  redis.call('PERSIST', KEYS[4])
  if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PERSIST', KEYS[3]) end
  redis.call('HDEL', KEYS[10], ARGV[1])
  redis.call('SADD', KEYS[8], ARGV[1])
  if desired == 1 then
    redis.call('SADD', KEYS[3], ARGV[2])
    count = count + 1
  else
    redis.call('SREM', KEYS[3], ARGV[2])
    count = math.max(0, count - 1)
  end
  version = version + 1
  redis.call('SET', KEYS[2], count)
  redis.call('SET', KEYS[4], version)
  redis.call('SADD', KEYS[5], ARGV[1])
  local pair = ARGV[2] .. ':' .. ARGV[1]
  redis.call('HSET', KEYS[7], pair, ARGV[3] .. '|' .. version .. '|' .. ARGV[4])
  redis.call('SADD', KEYS[6], pair)
  redis.call('ZADD', KEYS[9], ARGV[5], ARGV[1])
  current = desired
end
return {count, current, changed, version}
`)

type relationBenchmarkEdge struct {
	userID uint
	postID uint
}

type relationBenchmarkQuery struct {
	userID  uint
	postIDs []uint
}

type relationBenchmarkStats struct {
	opsPerSecond float64
	p95          time.Duration
	p99          time.Duration
	operations   int
	items        int
}

type relationBenchmarkSnapshot struct {
	redisVersion      string
	usedMemory        int64
	usedMemoryRSS     string
	usedMemoryPeak    string
	usedMemoryDataset string
	fragmentation     string
	cpuSys            float64
	cpuUser           float64
	dbKeys            int64
	relationKeys      int
	maxKey            string
	maxKeyType        string
	maxKeyEncoding    string
	maxKeyMemory      int64
	relationEncodings map[string]int
}

func TestRedisLikeRelationModelBenchmark(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("REDIS_LIKE_BENCH_ADDR"))
	if addr == "" {
		t.Skip("set REDIS_LIKE_BENCH_ADDR and the dedicated DB settings; see REDIS_RELATION_BENCHMARK.md")
	}
	if os.Getenv("REDIS_LIKE_BENCH_ALLOW_FLUSHDB") != "1" {
		t.Fatal("refusing to run: explicitly set REDIS_LIKE_BENCH_ALLOW_FLUSHDB=1 for a disposable Redis DB")
	}
	db, err := strconv.Atoi(strings.TrimSpace(os.Getenv("REDIS_LIKE_BENCH_DB")))
	if err != nil || db <= 0 {
		t.Fatal("REDIS_LIKE_BENCH_DB must name a non-zero disposable logical DB")
	}
	testDB, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("REDIS_TEST_DB")))
	if db == testDB {
		t.Fatalf("benchmark DB %d must differ from REDIS_TEST_DB %d", db, testDB)
	}
	edgesCount := relationBenchmarkEnvInt(t, "REDIS_LIKE_BENCH_EDGES", 4000, 1, 100000)
	batchSize := relationBenchmarkEnvInt(t, "REDIS_LIKE_BENCH_BATCH", 64, 1, 1000)
	workers := relationBenchmarkEnvInt(t, "REDIS_LIKE_BENCH_WORKERS", 8, 1, 128)

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_LIKE_BENCH_PASSWORD"),
		DB:       db,
	})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatalf("connect benchmark Redis: %v", err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB().Err()
		_ = client.Close()
	})

	if err := client.FlushDB().Err(); err != nil {
		t.Fatalf("clear dedicated benchmark DB: %v", err)
	}
	ctx := context.Background()
	for _, scenario := range []string{"hot-post-many-users", "few-users-many-posts"} {
		edges := makeRelationBenchmarkEdges(scenario, edgesCount)
		queries := makeRelationBenchmarkQueries(scenario, edges, batchSize)
		for _, model := range []string{"post-users", "user-posts"} {
			if err := client.FlushDB().Err(); err != nil {
				t.Fatalf("clear benchmark DB before %s/%s: %v", scenario, model, err)
			}
			empty, err := readRelationBenchmarkSnapshot(ctx, client)
			if err != nil {
				t.Fatalf("read empty Redis baseline: %v", err)
			}
			if err := seedRelationBenchmark(ctx, client, model, edges); err != nil {
				t.Fatalf("seed %s/%s: %v", scenario, model, err)
			}
			mutationStats, err := runRelationBenchmarkOperations(len(edges), workers, func(index int) error {
				edge := edges[index]
				if model == "user-posts" {
					_, err := NewStore(client).Mutate(ctx, edge.userID, edge.postID, true)
					return err
				}
				return mutateLegacyPostUserRelation(ctx, client, edge)
			})
			if err != nil {
				t.Fatalf("measure %s/%s Mutation: %v", scenario, model, err)
			}
			getManyStats, err := runRelationBenchmarkOperations(len(queries), workers, func(index int) error {
				query := queries[index]
				if model == "user-posts" {
					states, unavailable, err := NewStore(client).GetMany(ctx, query.userID, query.postIDs)
					if err != nil {
						return err
					}
					if len(unavailable) != 0 || len(states) != len(query.postIDs) {
						return fmt.Errorf("GetMany states=%d unavailable=%v want=%d states", len(states), unavailable, len(query.postIDs))
					}
					for _, state := range states {
						if !state.Liked {
							return fmt.Errorf("GetMany returned an unliked relation")
						}
					}
					return nil
				}
				return getManyLegacyPostUserRelation(ctx, client, query)
			})
			if err != nil {
				t.Fatalf("measure %s/%s GetMany: %v", scenario, model, err)
			}
			for _, query := range queries {
				getManyStats.items += len(query.postIDs)
			}
			snapshot, err := readRelationBenchmarkSnapshot(ctx, client)
			if err != nil {
				t.Fatalf("read %s/%s Redis metrics: %v", scenario, model, err)
			}
			t.Logf("redis_relation_benchmark scenario=%s model=%s edges=%d active_users=%d posts=%d batch=%d workers=%d redis_version=%s db_keys=%d used_memory=%d used_memory_delta=%d used_memory_rss=%s used_memory_peak=%s used_memory_dataset=%s mem_fragmentation_ratio=%s cpu_sys_delta_seconds=%.3f cpu_user_delta_seconds=%.3f relation_keys=%d relation_encodings=%v max_key=%q max_key_type=%s max_key_encoding=%s max_key_memory=%d mutation_ops_per_sec=%.1f mutation_p95=%s mutation_p99=%s getmany_ops_per_sec=%.1f getmany_posts_per_sec=%.1f getmany_p95=%s getmany_p99=%s",
				scenario, model, len(edges), countDistinctRelationUsers(edges), countDistinctRelationPosts(edges), batchSize, workers,
				snapshot.redisVersion, snapshot.dbKeys, snapshot.usedMemory, snapshot.usedMemory-empty.usedMemory,
				snapshot.usedMemoryRSS, snapshot.usedMemoryPeak, snapshot.usedMemoryDataset, snapshot.fragmentation,
				snapshot.cpuSys-empty.cpuSys, snapshot.cpuUser-empty.cpuUser, snapshot.relationKeys, snapshot.relationEncodings,
				snapshot.maxKey, snapshot.maxKeyType, snapshot.maxKeyEncoding, snapshot.maxKeyMemory,
				mutationStats.opsPerSecond, mutationStats.p95, mutationStats.p99,
				getManyStats.opsPerSecond,
				float64(getManyStats.items)*getManyStats.opsPerSecond/float64(getManyStats.operations), getManyStats.p95, getManyStats.p99)
		}
	}
}

func relationBenchmarkEnvInt(t *testing.T, key string, fallback, min, max int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		t.Fatalf("%s must be an integer from %d to %d", key, min, max)
	}
	return value
}

func makeRelationBenchmarkEdges(scenario string, edgeCount int) []relationBenchmarkEdge {
	edges := make([]relationBenchmarkEdge, 0, edgeCount)
	if scenario == "hot-post-many-users" {
		for index := 0; index < edgeCount; index++ {
			edges = append(edges, relationBenchmarkEdge{userID: uint(100000 + index), postID: 900001})
		}
		return edges
	}
	activeUsers := min(8, edgeCount)
	for index := 0; index < edgeCount; index++ {
		edges = append(edges, relationBenchmarkEdge{
			userID: uint(200000 + index%activeUsers),
			postID: uint(900100 + index/activeUsers),
		})
	}
	return edges
}

func makeRelationBenchmarkQueries(scenario string, edges []relationBenchmarkEdge, batchSize int) []relationBenchmarkQuery {
	if scenario == "hot-post-many-users" {
		queries := make([]relationBenchmarkQuery, 0, len(edges))
		for _, edge := range edges {
			queries = append(queries, relationBenchmarkQuery{userID: edge.userID, postIDs: []uint{edge.postID}})
		}
		return queries
	}
	postsByUser := make(map[uint][]uint)
	for _, edge := range edges {
		postsByUser[edge.userID] = append(postsByUser[edge.userID], edge.postID)
	}
	queries := make([]relationBenchmarkQuery, 0)
	for userID, postIDs := range postsByUser {
		for start := 0; start < len(postIDs); start += batchSize {
			end := min(start+batchSize, len(postIDs))
			queries = append(queries, relationBenchmarkQuery{userID: userID, postIDs: postIDs[start:end]})
		}
	}
	return queries
}

func seedRelationBenchmark(ctx context.Context, client *redis.Client, model string, edges []relationBenchmarkEdge) error {
	postIDs := make(map[uint]struct{})
	userIDs := make(map[uint]struct{})
	pipe := client.WithContext(ctx).Pipeline()
	for _, edge := range edges {
		if _, exists := postIDs[edge.postID]; !exists {
			postIDs[edge.postID] = struct{}{}
			pipe.Set(ReadyKey(edge.postID), "1", 0)
			pipe.Set(CountKey(edge.postID), "0", 0)
			pipe.Set(VersionKey(edge.postID), "0", 0)
			pipe.SAdd(RegistryKey, strconv.FormatUint(uint64(edge.postID), 10))
		}
		if model == "user-posts" {
			if _, exists := userIDs[edge.userID]; !exists {
				userIDs[edge.userID] = struct{}{}
				pipe.SAdd(UserLikesKey(edge.userID), UserLikesInitSentinel)
			}
		}
	}
	_, err := pipe.ExecContext(ctx)
	if err != nil && err != redis.Nil {
		return err
	}
	return nil
}

func mutateLegacyPostUserRelation(ctx context.Context, client *redis.Client, edge relationBenchmarkEdge) error {
	now := time.Now().UTC()
	keys := []string{
		ReadyKey(edge.postID), CountKey(edge.postID), UsersKey(edge.postID), VersionKey(edge.postID),
		DirtyKey, BehaviorDirtyKey, BehaviorStateKey, RegistryKey, ExpiryCandidatesKey, RecoverableVersionsKey,
	}
	return legacyPostUserMutationBenchmarkScript.Run(client.WithContext(ctx), keys,
		edge.postID, edge.userID, "1", now.Format(time.RFC3339Nano), now.UnixMilli()).Err()
}

func getManyLegacyPostUserRelation(ctx context.Context, client *redis.Client, query relationBenchmarkQuery) error {
	type readCommands struct {
		ready   *redis.StringCmd
		count   *redis.StringCmd
		version *redis.StringCmd
		liked   *redis.BoolCmd
		card    *redis.IntCmd
	}
	pipe := client.WithContext(ctx).Pipeline()
	commands := make([]readCommands, 0, len(query.postIDs))
	for _, postID := range query.postIDs {
		key := UsersKey(postID)
		commands = append(commands, readCommands{
			ready: pipe.Get(ReadyKey(postID)), count: pipe.Get(CountKey(postID)), version: pipe.Get(VersionKey(postID)),
			liked: pipe.SIsMember(key, strconv.FormatUint(uint64(query.userID), 10)), card: pipe.SCard(key),
		})
	}
	_, execErr := pipe.ExecContext(ctx)
	if execErr != nil && execErr != redis.Nil {
		return execErr
	}
	for _, command := range commands {
		if command.ready.Err() != nil || command.count.Err() != nil || command.version.Err() != nil || command.liked.Err() != nil || command.card.Err() != nil {
			return fmt.Errorf("legacy GetMany returned a Redis command error")
		}
		count, err := strconv.ParseInt(command.count.Val(), 10, 64)
		if err != nil || command.ready.Val() != "1" || !command.liked.Val() || command.card.Val() != count {
			return fmt.Errorf("legacy GetMany returned inconsistent or missing state")
		}
	}
	return nil
}

func runRelationBenchmarkOperations(count, workers int, operation func(int) error) (relationBenchmarkStats, error) {
	if count == 0 {
		return relationBenchmarkStats{}, fmt.Errorf("cannot measure zero operations")
	}
	workers = min(workers, count)
	latencies := make([]time.Duration, count)
	var next uint64
	var wait sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	startGate := make(chan struct{})
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			<-startGate
			for {
				index := int(atomic.AddUint64(&next, 1)) - 1
				if index >= count {
					return
				}
				started := time.Now()
				if err := operation(index); err != nil {
					errOnce.Do(func() { firstErr = err })
				}
				latencies[index] = time.Since(started)
			}
		}()
	}
	started := time.Now()
	close(startGate)
	wait.Wait()
	elapsed := time.Since(started)
	if firstErr != nil {
		return relationBenchmarkStats{}, firstErr
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return relationBenchmarkStats{
		opsPerSecond: float64(count) / elapsed.Seconds(),
		p95:          relationBenchmarkPercentile(latencies, .95),
		p99:          relationBenchmarkPercentile(latencies, .99),
		operations:   count,
	}, nil
}

func relationBenchmarkPercentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*fraction)) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}

func countDistinctRelationUsers(edges []relationBenchmarkEdge) int {
	values := make(map[uint]struct{})
	for _, edge := range edges {
		values[edge.userID] = struct{}{}
	}
	return len(values)
}

func countDistinctRelationPosts(edges []relationBenchmarkEdge) int {
	values := make(map[uint]struct{})
	for _, edge := range edges {
		values[edge.postID] = struct{}{}
	}
	return len(values)
}

func readRelationBenchmarkSnapshot(ctx context.Context, client *redis.Client) (relationBenchmarkSnapshot, error) {
	serverInfo, err := client.WithContext(ctx).Info("server").Result()
	if err != nil {
		return relationBenchmarkSnapshot{}, err
	}
	memoryInfo, err := client.WithContext(ctx).Info("memory").Result()
	if err != nil {
		return relationBenchmarkSnapshot{}, err
	}
	cpuInfo, err := client.WithContext(ctx).Info("cpu").Result()
	if err != nil {
		return relationBenchmarkSnapshot{}, err
	}
	keys, err := scanRelationBenchmarkKeys(ctx, client)
	if err != nil {
		return relationBenchmarkSnapshot{}, err
	}
	dbKeys, err := client.WithContext(ctx).DBSize().Result()
	if err != nil {
		return relationBenchmarkSnapshot{}, err
	}
	snapshot := relationBenchmarkSnapshot{
		redisVersion:      redisInfoValue(serverInfo, "redis_version"),
		usedMemory:        parseRedisInfoInt(memoryInfo, "used_memory"),
		usedMemoryRSS:     redisInfoValue(memoryInfo, "used_memory_rss"),
		usedMemoryPeak:    redisInfoValue(memoryInfo, "used_memory_peak"),
		usedMemoryDataset: redisInfoValue(memoryInfo, "used_memory_dataset"),
		fragmentation:     redisInfoValue(memoryInfo, "mem_fragmentation_ratio"),
		cpuSys:            parseRedisInfoFloat(cpuInfo, "used_cpu_sys"),
		cpuUser:           parseRedisInfoFloat(cpuInfo, "used_cpu_user"),
		dbKeys:            dbKeys,
		relationEncodings: make(map[string]int),
	}
	for start := 0; start < len(keys); start += 500 {
		end := min(start+500, len(keys))
		pipe := client.WithContext(ctx).Pipeline()
		entries := make([]struct {
			key      string
			kind     *redis.Cmd
			encoding *redis.Cmd
			memory   *redis.Cmd
		}, 0, end-start)
		for _, key := range keys[start:end] {
			entries = append(entries, struct {
				key      string
				kind     *redis.Cmd
				encoding *redis.Cmd
				memory   *redis.Cmd
			}{key: key, kind: pipe.Do("TYPE", key), encoding: pipe.Do("OBJECT", "ENCODING", key), memory: pipe.Do("MEMORY", "USAGE", key)})
		}
		if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
			return relationBenchmarkSnapshot{}, err
		}
		for _, entry := range entries {
			kind, kindErr := entry.kind.Text()
			encoding, encodingErr := entry.encoding.Text()
			memory, memoryErr := entry.memory.Int64()
			if kindErr != nil || encodingErr != nil || memoryErr != nil {
				return relationBenchmarkSnapshot{}, fmt.Errorf("inspect Redis key %q: type=%v encoding=%v memory=%v", entry.key, kindErr, encodingErr, memoryErr)
			}
			if memory > snapshot.maxKeyMemory {
				snapshot.maxKey = entry.key
				snapshot.maxKeyType = kind
				snapshot.maxKeyEncoding = encoding
				snapshot.maxKeyMemory = memory
			}
			if strings.HasPrefix(entry.key, "user:likes:") || strings.HasPrefix(entry.key, "post:like:") && strings.HasSuffix(entry.key, ":users") {
				snapshot.relationKeys++
				snapshot.relationEncodings[kind+"/"+encoding]++
			}
		}
	}
	return snapshot, nil
}

func scanRelationBenchmarkKeys(ctx context.Context, client *redis.Client) ([]string, error) {
	var cursor uint64
	keys := make([]string, 0)
	for {
		batch, next, err := client.WithContext(ctx).Scan(cursor, "*", 1000).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys, nil
		}
	}
}

func redisInfoValue(info, key string) string {
	for _, line := range strings.Split(info, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && name == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseRedisInfoInt(info, key string) int64 {
	value, _ := strconv.ParseInt(redisInfoValue(info, key), 10, 64)
	return value
}

func parseRedisInfoFloat(info, key string) float64 {
	value, _ := strconv.ParseFloat(redisInfoValue(info, key), 64)
	return value
}
