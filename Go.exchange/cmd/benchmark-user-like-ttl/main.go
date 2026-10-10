package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"Go.exchange/config"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var benchmarkRelationSizes = []int{0, 10, 100, 1000, 10000}
var benchmarkGroups = []string{"baseline", "ledger_active", "cold", "concurrent_restore"}

type options struct {
	Apply               bool
	ConfirmDisposable   bool
	RedisDB             int
	ExpireAfter         time.Duration
	HotOperationSamples int
}

type benchmarkUser struct {
	ID      uint
	Group   string
	Size    int
	PostIDs []uint
}

type benchmarkPostRow struct {
	ID         uint
	DeletedAt  *time.Time
	Visibility string
}

func (benchmarkPostRow) TableName() string { return "posts" }

type benchmarkUserRow struct {
	ID        uint
	DeletedAt *time.Time
}

func (benchmarkUserRow) TableName() string { return "users" }

type benchmarkReactionRow struct {
	UserID          uint
	PostID          uint
	Reaction        int16
	Liked           bool
	ReactionVersion int64
	UpdatedAt       time.Time
	StateChangedAt  time.Time
}

func (benchmarkReactionRow) TableName() string { return "post_reaction" }

type reactionQueryCounter struct {
	gormlogger.Interface
	queries atomic.Int64
	rows    atomic.Int64
}

func (counter *reactionQueryCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	query, rows := fc()
	if strings.Contains(strings.ToLower(query), "from post_reaction as reaction") {
		counter.queries.Add(1)
		if rows > 0 {
			counter.rows.Add(rows)
		}
	}
	counter.Interface.Trace(ctx, begin, func() (string, int64) { return query, rows }, err)
}

func (counter *reactionQueryCounter) reset() {
	counter.queries.Store(0)
	counter.rows.Store(0)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	if !options.Apply {
		_, err := fmt.Fprintln(stdout, "DRY RUN: no services contacted or data written. Use --apply --confirm-disposable-services with dedicated REDIS_BENCH_ADDR, an empty nonzero Redis DB, and POSTGRES_BENCH_DSN.")
		return err
	}
	if !options.ConfirmDisposable {
		return errors.New("refusing benchmark writes without --confirm-disposable-services")
	}
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_BENCH_ADDR"))
	postgresDSN := strings.TrimSpace(os.Getenv("POSTGRES_BENCH_DSN"))
	if redisAddr == "" || postgresDSN == "" {
		return errors.New("REDIS_BENCH_ADDR and POSTGRES_BENCH_DSN must point to dedicated disposable services")
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Minute)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: redisAddr, DB: options.RedisDB})
	defer client.Close()
	if err := client.WithContext(ctx).Ping().Err(); err != nil {
		return fmt.Errorf("connect benchmark Redis: %w", err)
	}
	if err := requireRedisEffectsReplicationVersion(ctx, client); err != nil {
		return err
	}
	databaseSize, err := client.WithContext(ctx).DBSize().Result()
	if err != nil {
		return fmt.Errorf("inspect benchmark Redis DB size: %w", err)
	}
	if databaseSize != 0 {
		return fmt.Errorf("refusing benchmark Redis DB %d with %d existing keys; use a dedicated empty DB; this tool never runs FLUSHDB", options.RedisDB, databaseSize)
	}

	counter := &reactionQueryCounter{Interface: gormlogger.New(log.New(io.Discard, "", 0), gormlogger.Config{LogLevel: gormlogger.Silent})}
	db, schema, err := openBenchmarkPostgres(ctx, postgresDSN, counter)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Exec("SET search_path TO public").Error
		_ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	users, posts, err := buildBenchmarkDataset()
	if err != nil {
		return err
	}
	if err := seedBenchmarkPostgres(ctx, db, users, posts); err != nil {
		return err
	}
	redisStoreSettings := lifecycleSettings(true, config.DefaultUserLikeSetTTL)
	redisStore := likes.NewStoreWithUserLikeSettings(client, redisStoreSettings)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cleanupCancel()
	defer cleanupBenchmarkRedis(cleanupCtx, client, users, posts)
	if err := seedBenchmarkPostKeys(ctx, client, posts); err != nil {
		return err
	}

	baseMemory, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Redis version validated; DB=%d keys_before=%d used_memory_before=%d bytes\n", options.RedisDB, databaseSize, baseMemory)
	userSets := make(map[string][]int64, len(benchmarkGroups))
	for groupIndex, groupName := range benchmarkGroups[:3] {
		arming := groupName != "baseline"
		store := redisStore
		if !arming {
			store = likes.NewStoreWithUserLikeSettings(client, lifecycleSettings(false, config.DefaultUserLikeSetTTL))
		}
		groupUsers := users[groupIndex]
		memoryBeforeScenario, err := usedMemory(ctx, client)
		if err != nil {
			return err
		}
		for _, user := range groupUsers {
			if err := populateBenchmarkUser(ctx, store, user); err != nil {
				return fmt.Errorf("populate %s UserID=%d: %w", groupName, user.ID, err)
			}
		}
		setBytes, err := benchmarkGroupSetBytes(ctx, client, groupUsers)
		if err != nil {
			return err
		}
		userSets[groupName] = setBytes
		memory, err := usedMemory(ctx, client)
		if err != nil {
			return err
		}
		ledgerBytes, err := keyMemoryUsage(ctx, client, likes.UserLikesExpiryLedgerKey)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "Scenario=%s used_memory_total=%d scenario_used_memory_change=%d user_set_bytes_total=%d user_set_bytes_avg=%d ledger_hash_bytes=%d\n",
			groupName, memory, memory-memoryBeforeScenario, sum(setBytes), average(setBytes), ledgerBytes)
		if groupName == "ledger_active" {
			ledgerPerUser := float64(ledgerBytes) / float64(len(benchmarkRelationSizes))
			_, _ = fmt.Fprintf(stdout, "Scenario=ledger_active ledger_increment_bytes_per_user=%.2f baseline_user_set_avg_bytes=%d\n", ledgerPerUser, average(userSets["baseline"]))
		}
	}

	coldUsers := users[2]
	for _, user := range coldUsers {
		if err := armBenchmarkShortTTL(ctx, client, user.ID, options.ExpireAfter); err != nil {
			return fmt.Errorf("arm benchmark cold TTL for UserID %d: %w", user.ID, err)
		}
	}
	coldBeforeExpiry, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	for _, user := range coldUsers {
		if err := waitForUserSetExpiry(ctx, client, user.ID, options.ExpireAfter+5*time.Second); err != nil {
			return err
		}
	}
	coldAfterExpiry, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	coldLedgerBytes, err := keyMemoryUsage(ctx, client, likes.UserLikesExpiryLedgerKey)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Scenario=cold used_memory=%d expired_net_memory_change=%d ledger_hash_bytes=%d user_set_bytes_total=%d\n",
		coldAfterExpiry, coldAfterExpiry-coldBeforeExpiry, coldLedgerBytes, 0)

	restoreStore := likes.NewStoreWithUserLikeSettings(client, lifecycleSettings(true, config.DefaultUserLikeSetTTL))
	lifecycle := likes.NewUserLikeLifecycle(restoreStore, db)
	restoreTimes := make([]time.Duration, 0, len(coldUsers))
	var restoreRows int64
	var installedSetBytes int64
	var peakMemory atomic.Int64
	peakMemory.Store(coldAfterExpiry)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(10 * time.Millisecond):
				memory, err := usedMemory(monitorCtx, client)
				if err == nil {
					for current := peakMemory.Load(); memory > current && !peakMemory.CompareAndSwap(current, memory); current = peakMemory.Load() {
					}
				}
			}
		}
	}()
	for _, user := range coldUsers {
		counter.reset()
		started := time.Now()
		if err := lifecycle.RecoverIfCold(ctx, user.ID); err != nil {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("restore benchmark UserID=%d: %w", user.ID, err)
		}
		restoreTimes = append(restoreTimes, time.Since(started))
		restoreRows += counter.rows.Load()
		bytes, err := keyMemoryUsage(ctx, client, likes.UserLikesKey(user.ID))
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return err
		}
		installedSetBytes += bytes
		cardinality, err := client.WithContext(ctx).SCard(likes.UserLikesKey(user.ID)).Result()
		if err != nil || cardinality != int64(user.Size+1) {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("restore result cardinality UserID=%d got=%d err=%v want=%d", user.ID, cardinality, err, user.Size+1)
		}
	}
	stopMonitor()
	monitor.Wait()
	restoredMemory, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Scenario=restored used_memory=%d restored_user_set_bytes_total=%d restored_user_set_bytes_avg=%d pg_rows_returned=%d restore_duration_samples=%d restore_p50=%s restore_p95=%s restore_p99=%s redis_used_memory_peak_during_restore=%d\n",
		restoredMemory, installedSetBytes, installedSetBytes/int64(len(coldUsers)), restoreRows, len(restoreTimes),
		quantileDuration(restoreTimes, .50), quantileDuration(restoreTimes, .95), quantileDuration(restoreTimes, .99), peakMemory.Load())
	_, _ = fmt.Fprintf(stdout, "MemoryComparison baseline_set_bytes_avg=%d ledger_active_set_bytes_avg=%d cold_net_memory_saved_bytes=%d restored_total_memory_change_from_cold=%d installed_set_bytes_are_the_restored_temporary_set_object_snapshot=true\n",
		average(userSets["baseline"]), average(userSets["ledger_active"]), coldBeforeExpiry-coldAfterExpiry, restoredMemory-coldAfterExpiry)

	if err := reportPostgresPlan(ctx, stdout, db, users); err != nil {
		return err
	}
	if err := benchmarkHotMutationLatency(ctx, stdout, client, users, options.HotOperationSamples); err != nil {
		return err
	}
	if err := benchmarkConcurrentRestoreQueryCount(ctx, stdout, client, db, counter, restoreStore, users, options.ExpireAfter); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Cleanup: only benchmark-owned User/Post/queue keys are deleted; no Redis database flush was issued. PostgreSQL benchmark schema is dropped on exit.")
	return err
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("benchmark-user-like-ttl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.BoolVar(&result.Apply, "apply", false, "run the benchmark against explicitly configured disposable services")
	flags.BoolVar(&result.ConfirmDisposable, "confirm-disposable-services", false, "confirm Redis DB and PostgreSQL DSN are disposable benchmark targets")
	flags.IntVar(&result.RedisDB, "db", 15, "dedicated nonzero Redis database number (1-15), which must be empty")
	flags.DurationVar(&result.ExpireAfter, "expire-after", 2*time.Second, "short test-only User Set lifetime used to measure cold memory and recovery (100ms-30s)")
	flags.IntVar(&result.HotOperationSamples, "hot-operation-samples", 200, "number of Like and Unlike latency samples, each bounded to 10000")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected positional arguments")
	}
	if result.RedisDB < 1 || result.RedisDB > 15 {
		return options{}, errors.New("db must be between 1 and 15 so the default Redis DB is never targeted")
	}
	if result.ExpireAfter < 100*time.Millisecond || result.ExpireAfter > 30*time.Second {
		return options{}, errors.New("expire-after must be between 100ms and 30s")
	}
	if result.HotOperationSamples < 100 || result.HotOperationSamples > 10000 {
		return options{}, errors.New("hot-operation-samples must be between 100 and 10000")
	}
	return result, nil
}

func requireRedisEffectsReplicationVersion(ctx context.Context, client *redis.Client) error {
	info, err := client.WithContext(ctx).Info("server").Result()
	if err != nil {
		return fmt.Errorf("inspect benchmark Redis version: %w", err)
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			version := strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
			major, parseErr := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
			if parseErr != nil || major < 5 {
				return fmt.Errorf("Redis %q is unsupported; User Like expiry scripts require Redis 5+ effects replication", version)
			}
			return nil
		}
	}
	return errors.New("Redis INFO server did not report redis_version")
}

func openBenchmarkPostgres(ctx context.Context, dsn string, counter *reactionQueryCounter) (*gorm.DB, string, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: counter})
	if err != nil {
		return nil, "", fmt.Errorf("open benchmark PostgreSQL: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, "", err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	var databaseName string
	if err := db.WithContext(ctx).Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		_ = sqlDB.Close()
		return nil, "", fmt.Errorf("verify benchmark PostgreSQL database: %w", err)
	}
	lowerDatabaseName := strings.ToLower(databaseName)
	if !strings.Contains(lowerDatabaseName, "bench") && !strings.Contains(lowerDatabaseName, "test") && !strings.Contains(lowerDatabaseName, "integration") && !strings.Contains(lowerDatabaseName, "disposable") {
		_ = sqlDB.Close()
		return nil, "", fmt.Errorf("refusing PostgreSQL database %q: use a dedicated database whose name includes bench, test, integration, or disposable", databaseName)
	}
	schema := fmt.Sprintf("user_like_bench_%d", time.Now().UnixNano())
	if err := db.WithContext(ctx).Exec("CREATE SCHEMA " + schema).Error; err != nil {
		_ = sqlDB.Close()
		return nil, "", fmt.Errorf("create benchmark schema: %w", err)
	}
	if err := db.WithContext(ctx).Exec("SET search_path TO " + schema).Error; err != nil {
		_ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
		_ = sqlDB.Close()
		return nil, "", fmt.Errorf("select benchmark schema: %w", err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz NULL)",
		"CREATE TABLE posts (id bigint PRIMARY KEY, deleted_at timestamptz NULL, visibility text NOT NULL)",
		"CREATE TABLE post_reaction (user_id bigint NOT NULL, post_id bigint NOT NULL, reaction smallint NOT NULL, liked boolean NOT NULL, reaction_version bigint NOT NULL, updated_at timestamptz NOT NULL, state_changed_at timestamptz NOT NULL, PRIMARY KEY (user_id, post_id))",
	} {
		if err := db.WithContext(ctx).Exec(statement).Error; err != nil {
			_ = db.Exec("SET search_path TO public").Error
			_ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
			_ = sqlDB.Close()
			return nil, "", fmt.Errorf("create benchmark schema table: %w", err)
		}
	}
	return db, schema, nil
}

func buildBenchmarkDataset() ([][]*benchmarkUser, []benchmarkPostRow, error) {
	base := uint(time.Now().UnixNano() & 0x1fffffff)
	userBase := base + 3000000000
	groups := make([][]*benchmarkUser, len(benchmarkGroups))
	posts := make([]benchmarkPostRow, 0, 44440)
	postID := base
	for groupIndex, groupName := range benchmarkGroups {
		for sizeIndex, size := range benchmarkRelationSizes {
			user := &benchmarkUser{ID: userBase + uint(groupIndex*100+sizeIndex), Group: groupName, Size: size, PostIDs: make([]uint, 0, size)}
			groups[groupIndex] = append(groups[groupIndex], user)
			for range size {
				postID++
				user.PostIDs = append(user.PostIDs, postID)
				posts = append(posts, benchmarkPostRow{ID: postID, Visibility: "private"})
			}
		}
	}
	return groups, posts, nil
}

func seedBenchmarkPostgres(ctx context.Context, db *gorm.DB, groups [][]*benchmarkUser, posts []benchmarkPostRow) error {
	userRows := make([]benchmarkUserRow, 0, len(groups)*len(benchmarkRelationSizes))
	reactionRows := make([]benchmarkReactionRow, 0, len(posts))
	for _, group := range groups {
		for _, user := range group {
			userRows = append(userRows, benchmarkUserRow{ID: user.ID})
			for _, postID := range user.PostIDs {
				now := time.Now()
				reactionRows = append(reactionRows, benchmarkReactionRow{UserID: user.ID, PostID: postID, Reaction: models.PostReactionLike, Liked: true, ReactionVersion: 1, UpdatedAt: now, StateChangedAt: now})
			}
		}
	}
	if err := db.WithContext(ctx).CreateInBatches(&userRows, 500).Error; err != nil {
		return fmt.Errorf("seed benchmark Users: %w", err)
	}
	if err := db.WithContext(ctx).CreateInBatches(&posts, 500).Error; err != nil {
		return fmt.Errorf("seed benchmark Posts: %w", err)
	}
	if err := db.WithContext(ctx).CreateInBatches(&reactionRows, 500).Error; err != nil {
		return fmt.Errorf("seed benchmark post_reaction rows: %w", err)
	}
	if err := db.WithContext(ctx).Exec("ANALYZE post_reaction").Error; err != nil {
		return fmt.Errorf("analyze benchmark post_reaction: %w", err)
	}
	return nil
}

func seedBenchmarkPostKeys(ctx context.Context, client *redis.Client, posts []benchmarkPostRow) error {
	for start := 0; start < len(posts); start += 500 {
		end := min(start+500, len(posts))
		pipe := client.WithContext(ctx).Pipeline()
		for _, post := range posts[start:end] {
			pipe.Set(likes.ReadyKey(post.ID), "1", 0)
			pipe.Set(likes.CountKey(post.ID), "0", 0)
			pipe.Set(likes.VersionKey(post.ID), "0", 0)
		}
		if _, err := pipe.ExecContext(ctx); err != nil && err != redis.Nil {
			return fmt.Errorf("seed persistent Post Like keys: %w", err)
		}
	}
	return nil
}

func populateBenchmarkUser(ctx context.Context, store *likes.Store, user *benchmarkUser) error {
	if err := store.InitializeUserEmpty(ctx, user.ID); err != nil {
		return err
	}
	for _, postID := range user.PostIDs {
		if _, err := store.Mutate(ctx, user.ID, postID, true); err != nil {
			return err
		}
	}
	return nil
}

func lifecycleSettings(arming bool, ttl time.Duration) config.UserLikeLifecycleConfig {
	return config.UserLikeLifecycleConfig{
		ArmingEnabled: arming, RestoreEnabled: true, SetTTL: ttl,
		RestoreLockTTL: 32 * time.Second, RestoreBatchSize: 500,
		RestoreMaxRelations: 100000, RestoreRequestTimeout: 30 * time.Second,
		RestoreConcurrency: 8,
	}
}

func armBenchmarkShortTTL(ctx context.Context, client *redis.Client, userID uint, ttl time.Duration) error {
	return client.WithContext(ctx).Eval(`
local user_type = redis.call('TYPE', KEYS[1]).ok
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if user_type ~= 'set' or ledger_type ~= 'hash' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('BENCH_USER_STATE_INVALID') end
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local expiry = now_ms + tonumber(ARGV[2])
redis.call('PEXPIREAT', KEYS[1], expiry)
redis.call('HSET', KEYS[2], ARGV[1], tostring(expiry))
return expiry
`, []string{likes.UserLikesKey(userID), likes.UserLikesExpiryLedgerKey}, strconv.FormatUint(uint64(userID), 10), ttl.Milliseconds()).Err()
}

func waitForUserSetExpiry(ctx context.Context, client *redis.Client, userID uint, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		exists, err := client.WithContext(ctx).Exists(likes.UserLikesKey(userID)).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for benchmark UserID %d Set expiry", userID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func benchmarkGroupSetBytes(ctx context.Context, client *redis.Client, users []*benchmarkUser) ([]int64, error) {
	result := make([]int64, 0, len(users))
	for _, user := range users {
		bytes, err := keyMemoryUsage(ctx, client, likes.UserLikesKey(user.ID))
		if err != nil {
			return nil, err
		}
		result = append(result, bytes)
	}
	return result, nil
}

func keyMemoryUsage(ctx context.Context, client *redis.Client, key string) (int64, error) {
	bytes, err := client.WithContext(ctx).MemoryUsage(key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("MEMORY USAGE %q: %w", key, err)
	}
	return bytes, nil
}

func usedMemory(ctx context.Context, client *redis.Client) (int64, error) {
	info, err := client.WithContext(ctx).Info("memory").Result()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "used_memory:") {
			return strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "used_memory:")), 10, 64)
		}
	}
	return 0, errors.New("Redis INFO memory did not report used_memory")
}

func benchmarkHotMutationLatency(ctx context.Context, stdout io.Writer, client *redis.Client, groups [][]*benchmarkUser, sampleCount int) error {
	var target *benchmarkUser
	for _, user := range groups[1] {
		if user.Size == 10 {
			target = user
			break
		}
	}
	if target == nil || len(target.PostIDs) == 0 {
		return errors.New("hot Like benchmark User fixture was not found")
	}
	store := likes.NewStoreWithUserLikeSettings(client, lifecycleSettings(true, config.DefaultUserLikeSetTTL))
	likeTimes := make([]time.Duration, 0, sampleCount)
	unlikeTimes := make([]time.Duration, 0, sampleCount)
	for range sampleCount {
		started := time.Now()
		if _, err := store.Mutate(ctx, target.ID, target.PostIDs[0], false); err != nil {
			return fmt.Errorf("benchmark hot Unlike: %w", err)
		}
		unlikeTimes = append(unlikeTimes, time.Since(started))
		started = time.Now()
		if _, err := store.Mutate(ctx, target.ID, target.PostIDs[0], true); err != nil {
			return fmt.Errorf("benchmark hot Like: %w", err)
		}
		likeTimes = append(likeTimes, time.Since(started))
	}
	_, err := fmt.Fprintf(stdout, "HotPath samples_per_operation=%d like_p95=%s like_p99=%s unlike_p95=%s unlike_p99=%s\n",
		sampleCount, quantileDuration(likeTimes, .95), quantileDuration(likeTimes, .99), quantileDuration(unlikeTimes, .95), quantileDuration(unlikeTimes, .99))
	return err
}

func benchmarkConcurrentRestoreQueryCount(ctx context.Context, stdout io.Writer, client *redis.Client, db *gorm.DB, counter *reactionQueryCounter, store *likes.Store, groups [][]*benchmarkUser, expireAfter time.Duration) error {
	var target *benchmarkUser
	for _, user := range groups[3] {
		if user.Size == 100 {
			target = user
			break
		}
	}
	if target == nil {
		return errors.New("concurrent restore benchmark User fixture was not found")
	}
	if err := populateBenchmarkUser(ctx, store, target); err != nil {
		return fmt.Errorf("populate concurrent restore UserID=%d: %w", target.ID, err)
	}
	if err := armBenchmarkShortTTL(ctx, client, target.ID, expireAfter); err != nil {
		return err
	}
	if err := waitForUserSetExpiry(ctx, client, target.ID, expireAfter+5*time.Second); err != nil {
		return err
	}
	lifecycle := likes.NewUserLikeLifecycle(store, db)
	counter.reset()
	var wait sync.WaitGroup
	errorsCh := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsCh <- lifecycle.RecoverIfCold(ctx, target.ID)
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			return fmt.Errorf("concurrent cold restore: %w", err)
		}
	}
	queries, rows := counter.queries.Load(), counter.rows.Load()
	expectedSingleRestoreQueries := int64(int(math.Ceil(float64(target.Size)/500)) + 1)
	duplicates := max(queries-expectedSingleRestoreQueries, 0)
	_, err := fmt.Fprintf(stdout, "ConcurrentRestore requests=8 relation_count=%d post_reaction_queries=%d post_reaction_rows=%d duplicate_queries_over_single_restore=%d\n",
		target.Size, queries, rows, duplicates)
	return err
}

func reportPostgresPlan(ctx context.Context, stdout io.Writer, db *gorm.DB, groups [][]*benchmarkUser) error {
	var large *benchmarkUser
	for _, user := range groups[2] {
		if user.Size == 10000 {
			large = user
			break
		}
	}
	if large == nil {
		return errors.New("large User fixture was not found")
	}
	var plan []struct {
		QueryPlan string `gorm:"column:QUERY PLAN"`
	}
	if err := db.WithContext(ctx).Raw(`EXPLAIN (COSTS, BUFFERS)
SELECT reaction.post_id
FROM post_reaction AS reaction
JOIN posts ON posts.id = reaction.post_id AND posts.deleted_at IS NULL
WHERE reaction.user_id = ? AND reaction.reaction = ? AND reaction.liked = TRUE AND reaction.post_id > 0
ORDER BY reaction.post_id ASC LIMIT 500`, large.ID, models.PostReactionLike).Scan(&plan).Error; err != nil {
		return fmt.Errorf("explain benchmark keyset query: %w", err)
	}
	for _, row := range plan {
		if _, err := fmt.Fprintf(stdout, "PostgresPlan %s\n", row.QueryPlan); err != nil {
			return err
		}
	}
	return nil
}

func cleanupBenchmarkRedis(ctx context.Context, client *redis.Client, groups [][]*benchmarkUser, posts []benchmarkPostRow) {
	if client == nil {
		return
	}
	pipe := client.WithContext(ctx).Pipeline()
	queued := 0
	flush := func() {
		if queued == 0 {
			return
		}
		_, _ = pipe.ExecContext(ctx)
		pipe = client.WithContext(ctx).Pipeline()
		queued = 0
	}
	for _, group := range groups {
		for _, user := range group {
			uid := strconv.FormatUint(uint64(user.ID), 10)
			pipe.Del(likes.UserLikesKey(user.ID), likes.UserLikesRestoreLockKey(user.ID))
			pipe.HDel(likes.UserLikesExpiryLedgerKey, uid)
			queued += 2
			for _, postID := range user.PostIDs {
				pair := likes.BehaviorPair(user.ID, postID)
				post := strconv.FormatUint(uint64(postID), 10)
				pipe.SRem(likes.DirtyKey, post)
				pipe.SRem(likes.RegistryKey, post)
				pipe.ZRem(likes.ExpiryCandidatesKey, post)
				pipe.HDel(likes.RecoverableVersionsKey, post)
				pipe.SRem(likes.BehaviorDirtyKey, pair)
				pipe.HDel(likes.BehaviorStateKey, pair)
				pipe.ZRem(likes.BehaviorProcessingKey, pair)
				pipe.HDel(likes.BehaviorClaimsKey, pair)
				queued += 8
				if queued >= 2000 {
					flush()
				}
			}
		}
	}
	for _, post := range posts {
		pipe.Del(likes.ReadyKey(post.ID), likes.CountKey(post.ID), likes.VersionKey(post.ID), likes.RebuildTokenKey(post.ID))
		queued++
		if queued >= 2000 {
			flush()
		}
	}
	flush()
}

func sum(values []int64) int64 {
	var result int64
	for _, value := range values {
		result += value
	}
	return result
}

func average(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	return sum(values) / int64(len(values))
}

func quantileDuration(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}
