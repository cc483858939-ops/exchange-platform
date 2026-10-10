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

var benchmarkRelationSizes = []int{0, 100, 1000, 9999, 10000}
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
	cleanupCompleted := false
	defer func() {
		if cleanupCompleted {
			return
		}
		fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer fallbackCancel()
		if err := cleanupBenchmarkRedis(fallbackCtx, client); err != nil {
			log.Printf("ERROR: clean benchmark-owned Redis keys: %v", err)
		}
	}()
	if err := seedBenchmarkPostKeys(ctx, client, posts); err != nil {
		return err
	}

	baseMemory, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Redis version validated; DB=%d keys_before=%d used_memory_before=%d bytes\n", options.RedisDB, databaseSize, baseMemory)
	userSets := make(map[string][]int64, len(benchmarkGroups))
	userOrders := make(map[string][]int64, len(benchmarkGroups))
	userRelations := make(map[string][]int64, len(benchmarkGroups))
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
		setBytes, orderBytes, relationBytes, err := benchmarkGroupRelationBytes(ctx, client, groupUsers)
		if err != nil {
			return err
		}
		userSets[groupName] = setBytes
		userOrders[groupName] = orderBytes
		userRelations[groupName] = relationBytes
		memory, err := usedMemory(ctx, client)
		if err != nil {
			return err
		}
		ledgerBytes, err := keyMemoryUsage(ctx, client, likes.UserLikesExpiryLedgerKey)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "Scenario=%s used_memory_total=%d scenario_used_memory_change=%d user_set_bytes_total=%d user_order_zset_bytes_total=%d paired_relation_bytes_total=%d paired_relation_bytes_avg=%d ledger_hash_bytes=%d\n",
			groupName, memory, memory-memoryBeforeScenario, sum(setBytes), sum(orderBytes), sum(relationBytes), average(relationBytes), ledgerBytes)
		for index, user := range groupUsers {
			_, _ = fmt.Fprintf(stdout, "MemoryCohort scenario=%s relations=%d user_set_bytes=%d order_zset_bytes=%d paired_relation_bytes=%d\n",
				groupName, user.Size, setBytes[index], orderBytes[index], relationBytes[index])
		}
		if groupName == "ledger_active" {
			ledgerPerUser := float64(ledgerBytes) / float64(len(benchmarkRelationSizes))
			pairedBytes := sum(setBytes) + sum(orderBytes)
			_, _ = fmt.Fprintf(stdout, "Scenario=ledger_active paired_plus_ledger_bytes_total=%d ledger_increment_bytes_per_user=%.2f baseline_paired_relation_bytes_avg=%d\n",
				pairedBytes+ledgerBytes, ledgerPerUser, average(userRelations["baseline"]))
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
	restoreScriptSHA, err := restoreStore.PrimeUserLikeRestoreFinalizeScript(ctx)
	if err != nil {
		return fmt.Errorf("prime restore finalization script for SLOWLOG measurement: %w", err)
	}
	restoreSlowlogConfig, err := beginRestoreSlowlogCapture(ctx, client)
	if err != nil {
		return err
	}
	defer func() {
		if err := restoreSlowlogConfig(); err != nil {
			log.Printf("ERROR: restore Redis SLOWLOG settings: %v", err)
		}
	}()
	restoreDurationsBySize := make(map[int]time.Duration, len(coldUsers))
	restoreFinalizeLuaBySize := make(map[int]time.Duration, len(coldUsers))
	redisProbeTimes := make([]time.Duration, 0, 1024)
	var probeMu sync.Mutex
	var probeErr error
	var restoreRows int64
	var installedSetBytes int64
	var installedOrderBytes int64
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
				probeStarted := time.Now()
				pingErr := client.WithContext(monitorCtx).Ping().Err()
				probeDuration := time.Since(probeStarted)
				probeMu.Lock()
				if pingErr != nil && probeErr == nil {
					probeErr = pingErr
				} else if pingErr == nil {
					redisProbeTimes = append(redisProbeTimes, probeDuration)
				}
				probeMu.Unlock()
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
		latestSlowlogID, err := latestRedisSlowlogID(ctx, client)
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("read Redis SLOWLOG baseline before restore cohort %d: %w", user.Size, err)
		}
		started := time.Now()
		if err := lifecycle.RecoverIfCold(ctx, user.ID); err != nil {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("restore benchmark UserID=%d: %w", user.ID, err)
		}
		restoreDurationsBySize[user.Size] = time.Since(started)
		slowlogEntries, err := client.WithContext(ctx).Do("SLOWLOG", "GET", 4096).Result()
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("read Redis SLOWLOG after restore cohort %d: %w", user.Size, err)
		}
		restoreFinalizeLuaBySize[user.Size], err = findSlowlogScriptDuration(slowlogEntries, restoreScriptSHA, latestSlowlogID)
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("measure restore finalization for cohort %d: %w", user.Size, err)
		}
		restoreRows += counter.rows.Load()
		bytes, err := keyMemoryUsage(ctx, client, likes.UserLikesKey(user.ID))
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return err
		}
		installedSetBytes += bytes
		orderBytes, err := keyMemoryUsage(ctx, client, likes.UserLikesOrderKey(user.ID))
		if err != nil {
			stopMonitor()
			monitor.Wait()
			return err
		}
		installedOrderBytes += orderBytes
		cardinality, err := client.WithContext(ctx).SCard(likes.UserLikesKey(user.ID)).Result()
		if err != nil || cardinality != int64(user.Size+1) {
			stopMonitor()
			monitor.Wait()
			return fmt.Errorf("restore result cardinality UserID=%d got=%d err=%v want=%d", user.ID, cardinality, err, user.Size+1)
		}
	}
	stopMonitor()
	monitor.Wait()
	if err := restoreSlowlogConfig(); err != nil {
		return fmt.Errorf("restore Redis SLOWLOG settings: %w", err)
	}
	probeMu.Lock()
	latencyProbeErr := probeErr
	probeMu.Unlock()
	if latencyProbeErr != nil {
		return fmt.Errorf("Redis latency probe during restore: %w", latencyProbeErr)
	}
	if len(redisProbeTimes) == 0 {
		return errors.New("Redis latency probe collected no samples during restore")
	}
	restoredMemory, err := usedMemory(ctx, client)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Scenario=restored used_memory=%d restored_user_set_bytes_total=%d restored_order_zset_bytes_total=%d restored_paired_relation_bytes_total=%d pg_rows_returned=%d redis_used_memory_peak_during_restore=%d\n",
		restoredMemory, installedSetBytes, installedOrderBytes, installedSetBytes+installedOrderBytes, restoreRows, peakMemory.Load())
	for _, relationCount := range benchmarkRelationSizes {
		_, _ = fmt.Fprintf(stdout, "RestoreCohort relations=%d total_duration=%s finalize_lua_duration=%s\n",
			relationCount, restoreDurationsBySize[relationCount], restoreFinalizeLuaBySize[relationCount])
	}
	_, _ = fmt.Fprintf(stdout, "RedisProbeDuringRestore samples=%d p50=%s p95=%s p99=%s max=%s probe_interval=10ms\n",
		len(redisProbeTimes), quantileDuration(redisProbeTimes, .50), quantileDuration(redisProbeTimes, .95), quantileDuration(redisProbeTimes, .99), quantileDuration(redisProbeTimes, 1))
	_, _ = fmt.Fprintf(stdout, "MemoryComparison baseline_paired_relation_bytes_avg=%d ledger_active_paired_relation_bytes_avg=%d cold_net_memory_saved_bytes=%d restored_total_memory_change_from_cold=%d restored_set_bytes=%d restored_order_zset_bytes=%d\n",
		average(userRelations["baseline"]), average(userRelations["ledger_active"]), coldBeforeExpiry-coldAfterExpiry, restoredMemory-coldAfterExpiry, average(userSets["cold"]), average(userOrders["cold"]))

	if err := reportPostgresPlan(ctx, stdout, db, users); err != nil {
		return err
	}
	if err := benchmarkHotMutationLatency(ctx, stdout, client, users, options.HotOperationSamples); err != nil {
		return err
	}
	if err := benchmarkCapEvictionLatency(ctx, stdout, client, users, posts, options.HotOperationSamples); err != nil {
		return err
	}
	if err := benchmarkConcurrentRestoreQueryCount(ctx, stdout, client, db, counter, restoreStore, users, options.ExpireAfter); err != nil {
		return err
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	cleanupErr := cleanupBenchmarkRedis(cleanupCtx, client)
	cleanupCancel()
	if cleanupErr != nil {
		return fmt.Errorf("clean benchmark-owned Redis keys: %w", cleanupErr)
	}
	cleanupCompleted = true
	_, err = fmt.Fprintln(stdout, "Cleanup: cleared the keys created in the initially empty dedicated benchmark Redis DB with SCAN/UNLINK; no Redis database flush was issued. PostgreSQL benchmark schema is dropped on exit.")
	return err
}

func beginRestoreSlowlogCapture(ctx context.Context, client *redis.Client) (func() error, error) {
	threshold, err := redisConfigValue(ctx, client, "slowlog-log-slower-than")
	if err != nil {
		return nil, fmt.Errorf("read Redis slowlog-log-slower-than: %w", err)
	}
	maxLen, err := redisConfigValue(ctx, client, "slowlog-max-len")
	if err != nil {
		return nil, fmt.Errorf("read Redis slowlog-max-len: %w", err)
	}
	configuredMaxLen, err := strconv.Atoi(maxLen)
	if err != nil || configuredMaxLen < 0 {
		return nil, fmt.Errorf("Redis slowlog-max-len has invalid value %q", maxLen)
	}
	if err := client.WithContext(ctx).ConfigSet("slowlog-log-slower-than", "0").Err(); err != nil {
		return nil, fmt.Errorf("enable Redis SLOWLOG capture on the confirmed disposable benchmark server: %w", err)
	}
	if configuredMaxLen < 4096 {
		if err := client.WithContext(ctx).ConfigSet("slowlog-max-len", "4096").Err(); err != nil {
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			restoreErr := client.WithContext(restoreCtx).ConfigSet("slowlog-log-slower-than", threshold).Err()
			cancel()
			if restoreErr != nil {
				return nil, fmt.Errorf("set Redis SLOWLOG capacity: %v; failed to restore slowlog-log-slower-than: %w", err, restoreErr)
			}
			return nil, fmt.Errorf("set Redis SLOWLOG capacity: %w", err)
		}
	}
	var restored bool
	return func() error {
		if restored {
			return nil
		}
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var failures []string
		if err := client.WithContext(restoreCtx).ConfigSet("slowlog-log-slower-than", threshold).Err(); err != nil {
			failures = append(failures, "slowlog-log-slower-than: "+err.Error())
		}
		if err := client.WithContext(restoreCtx).ConfigSet("slowlog-max-len", maxLen).Err(); err != nil {
			failures = append(failures, "slowlog-max-len: "+err.Error())
		}
		if len(failures) != 0 {
			return fmt.Errorf("failed to restore Redis configuration (%s)", strings.Join(failures, "; "))
		}
		restored = true
		return nil
	}, nil
}

func redisConfigValue(ctx context.Context, client *redis.Client, name string) (string, error) {
	values, err := client.WithContext(ctx).ConfigGet(name).Result()
	if err != nil {
		return "", err
	}
	if len(values) != 2 {
		return "", fmt.Errorf("CONFIG GET %q returned %d values", name, len(values))
	}
	key, keyOK := redisReplyString(values[0])
	value, valueOK := redisReplyString(values[1])
	if !keyOK || key != name || !valueOK || value == "" {
		return "", fmt.Errorf("CONFIG GET %q returned an invalid response", name)
	}
	return value, nil
}

func latestRedisSlowlogID(ctx context.Context, client *redis.Client) (int64, error) {
	response, err := client.WithContext(ctx).Do("SLOWLOG", "GET", 1).Result()
	if err != nil {
		return 0, err
	}
	entries, ok := response.([]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected SLOWLOG GET response %T", response)
	}
	if len(entries) == 0 {
		return 0, nil
	}
	entry, ok := entries[0].([]interface{})
	if !ok || len(entry) < 1 {
		return 0, errors.New("malformed latest Redis SLOWLOG entry")
	}
	entryID, ok := redisReplyInt64(entry[0])
	if !ok || entryID < 0 {
		return 0, errors.New("invalid latest Redis SLOWLOG entry ID")
	}
	return entryID, nil
}

func findSlowlogScriptDuration(response interface{}, scriptSHA string, afterID int64) (time.Duration, error) {
	entries, ok := response.([]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected SLOWLOG GET response %T", response)
	}
	for _, rawEntry := range entries {
		entry, ok := rawEntry.([]interface{})
		if !ok || len(entry) < 4 {
			continue
		}
		entryID, idOK := redisReplyInt64(entry[0])
		if !idOK || entryID <= afterID {
			continue
		}
		args, ok := entry[3].([]interface{})
		if !ok || len(args) < 2 {
			continue
		}
		command, commandOK := redisReplyString(args[0])
		sha, shaOK := redisReplyString(args[1])
		if !commandOK || !shaOK || !strings.EqualFold(command, "EVALSHA") || sha != scriptSHA {
			continue
		}
		micros, ok := redisReplyInt64(entry[2])
		if !ok || micros < 0 {
			return 0, fmt.Errorf("invalid SLOWLOG duration for restore script: %v", entry[2])
		}
		return time.Duration(micros) * time.Microsecond, nil
	}
	return 0, fmt.Errorf("SLOWLOG contains no new EVALSHA entry for restore script %s", scriptSHA)
}

func redisReplyString(value interface{}) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	default:
		return "", false
	}
}

func redisReplyInt64(value interface{}) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
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
		RestoreMaxRelations: config.DefaultUserLikeRestoreMaxRelations, RestoreRequestTimeout: 30 * time.Second,
		RestoreConcurrency: 8,
	}
}

func armBenchmarkShortTTL(ctx context.Context, client *redis.Client, userID uint, ttl time.Duration) error {
	return client.WithContext(ctx).Eval(`
local user_type = redis.call('TYPE', KEYS[1]).ok
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if user_type ~= 'set' or ledger_type ~= 'hash' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('BENCH_USER_STATE_INVALID') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('BENCH_USER_STATE_INVALID') end
local active = redis.call('SCARD', KEYS[1]) - 1
if active > 0 and (order_type ~= 'zset' or redis.call('ZCARD', KEYS[3]) ~= active) then return redis.error_reply('BENCH_USER_STATE_INVALID') end
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local expiry = now_ms + tonumber(ARGV[2])
redis.call('PEXPIREAT', KEYS[1], expiry)
if active > 0 then redis.call('PEXPIREAT', KEYS[3], expiry) end
redis.call('HSET', KEYS[2], ARGV[1], tostring(expiry))
return expiry
`, []string{likes.UserLikesKey(userID), likes.UserLikesExpiryLedgerKey, likes.UserLikesOrderKey(userID)}, strconv.FormatUint(uint64(userID), 10), ttl.Milliseconds()).Err()
}

func waitForUserSetExpiry(ctx context.Context, client *redis.Client, userID uint, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		exists, err := client.WithContext(ctx).Exists(likes.UserLikesKey(userID), likes.UserLikesOrderKey(userID)).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for benchmark UserID %d paired relation expiry", userID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func benchmarkGroupRelationBytes(ctx context.Context, client *redis.Client, users []*benchmarkUser) ([]int64, []int64, []int64, error) {
	setBytes := make([]int64, 0, len(users))
	orderBytes := make([]int64, 0, len(users))
	relationBytes := make([]int64, 0, len(users))
	for _, user := range users {
		setUsage, err := keyMemoryUsage(ctx, client, likes.UserLikesKey(user.ID))
		if err != nil {
			return nil, nil, nil, err
		}
		orderUsage, err := keyMemoryUsage(ctx, client, likes.UserLikesOrderKey(user.ID))
		if err != nil {
			return nil, nil, nil, err
		}
		setBytes = append(setBytes, setUsage)
		orderBytes = append(orderBytes, orderUsage)
		relationBytes = append(relationBytes, setUsage+orderUsage)
	}
	return setBytes, orderBytes, relationBytes, nil
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
	store := likes.NewStoreWithUserLikeSettings(client, lifecycleSettings(true, config.DefaultUserLikeSetTTL))
	for _, relationCount := range []int{100, 1000, 9999, 10000} {
		var target *benchmarkUser
		for _, user := range groups[1] {
			if user.Size == relationCount {
				target = user
				break
			}
		}
		if target == nil || len(target.PostIDs) == 0 {
			return fmt.Errorf("normal mutation benchmark User with %d relations was not found", relationCount)
		}
		likeTimes := make([]time.Duration, 0, sampleCount)
		unlikeTimes := make([]time.Duration, 0, sampleCount)
		for range sampleCount {
			started := time.Now()
			if _, err := store.Mutate(ctx, target.ID, target.PostIDs[0], false); err != nil {
				return fmt.Errorf("benchmark normal Unlike at %d relations: %w", relationCount, err)
			}
			unlikeTimes = append(unlikeTimes, time.Since(started))
			started = time.Now()
			if _, err := store.Mutate(ctx, target.ID, target.PostIDs[0], true); err != nil {
				return fmt.Errorf("benchmark normal Like at %d relations: %w", relationCount, err)
			}
			likeTimes = append(likeTimes, time.Since(started))
		}
		if _, err := fmt.Fprintf(stdout, "NormalMutation relation_count=%d samples_per_operation=%d like_p95=%s like_p99=%s unlike_p95=%s unlike_p99=%s\n",
			relationCount, sampleCount, quantileDuration(likeTimes, .95), quantileDuration(likeTimes, .99), quantileDuration(unlikeTimes, .95), quantileDuration(unlikeTimes, .99)); err != nil {
			return err
		}
	}
	return nil
}

func benchmarkCapEvictionLatency(ctx context.Context, stdout io.Writer, client *redis.Client, groups [][]*benchmarkUser, posts []benchmarkPostRow, sampleCount int) error {
	var targetUser *benchmarkUser
	for _, user := range groups[2] {
		if user.Size == config.DefaultUserLikeRestoreMaxRelations {
			targetUser = user
			break
		}
	}
	if targetUser == nil || len(posts) == 0 {
		return errors.New("10,000-relation cap eviction benchmark fixture was not found")
	}
	if len(targetUser.PostIDs) == 0 {
		return errors.New("10,000-relation normal Like comparison fixture is empty")
	}
	targetPostID := posts[len(posts)-1].ID + 1
	cleanup := func() {
		post := strconv.FormatUint(uint64(targetPostID), 10)
		pair := likes.BehaviorPair(targetUser.ID, targetPostID)
		_ = client.Del(likes.ReadyKey(targetPostID), likes.CountKey(targetPostID), likes.VersionKey(targetPostID)).Err()
		_ = client.SRem(likes.DirtyKey, post).Err()
		_ = client.SRem(likes.RegistryKey, post).Err()
		_ = client.ZRem(likes.ExpiryCandidatesKey, post).Err()
		_ = client.HDel(likes.RecoverableVersionsKey, post).Err()
		_ = client.SRem(likes.BehaviorDirtyKey, pair).Err()
		_ = client.HDel(likes.BehaviorStateKey, pair).Err()
	}
	cleanup()
	defer cleanup()
	if err := client.WithContext(ctx).Set(likes.ReadyKey(targetPostID), "1", 0).Err(); err != nil {
		return err
	}
	if err := client.WithContext(ctx).Set(likes.CountKey(targetPostID), "0", 0).Err(); err != nil {
		return err
	}
	if err := client.WithContext(ctx).Set(likes.VersionKey(targetPostID), "0", 0).Err(); err != nil {
		return err
	}
	store := likes.NewStoreWithUserLikeSettings(client, lifecycleSettings(true, config.DefaultUserLikeSetTTL))
	evictionTimes := make([]time.Duration, 0, sampleCount)
	normalLikeTimes := make([]time.Duration, 0, sampleCount)
	normalPostID := targetUser.PostIDs[0]
	for range sampleCount {
		started := time.Now()
		result, err := store.Mutate(ctx, targetUser.ID, targetPostID, true)
		evictionTimes = append(evictionTimes, time.Since(started))
		if err != nil {
			return fmt.Errorf("benchmark cap eviction mutation: %w", err)
		}
		if !result.Changed || result.EvictedPostID == 0 || result.ActiveRelations != int64(config.DefaultUserLikeRestoreMaxRelations) {
			return fmt.Errorf("cap eviction result=%+v, expected oldest relation replaced at 10,000", result)
		}
		if _, err := store.Mutate(ctx, targetUser.ID, targetPostID, false); err != nil {
			return fmt.Errorf("restore cap headroom after benchmark eviction: %w", err)
		}
		if _, err := store.Mutate(ctx, targetUser.ID, result.EvictedPostID, true); err != nil {
			return fmt.Errorf("restore evicted relation after benchmark sample: %w", err)
		}
		if normalUnlike, err := store.Mutate(ctx, targetUser.ID, normalPostID, false); err != nil || !normalUnlike.Changed {
			return fmt.Errorf("create normal 9999-relation Like comparison headroom: result=%+v err=%v", normalUnlike, err)
		}
		started = time.Now()
		normalLike, err := store.Mutate(ctx, targetUser.ID, normalPostID, true)
		normalLikeTimes = append(normalLikeTimes, time.Since(started))
		if err != nil || !normalLike.Changed || normalLike.EvictedPostID != 0 || normalLike.ActiveRelations != int64(config.DefaultUserLikeRestoreMaxRelations) {
			return fmt.Errorf("benchmark normal Like at 9999 relations: result=%+v err=%v", normalLike, err)
		}
	}
	capP95, capP99 := quantileDuration(evictionTimes, .95), quantileDuration(evictionTimes, .99)
	normalP95, normalP99 := quantileDuration(normalLikeTimes, .95), quantileDuration(normalLikeTimes, .99)
	_, err := fmt.Fprintf(stdout, "CapEviction relation_count=10000 samples=%d p95=%s p99=%s normal_like_from_9999_p95=%s normal_like_from_9999_p99=%s p95_extra_over_normal=%s p99_extra_over_normal=%s\n",
		sampleCount, capP95, capP99, normalP95, normalP99, capP95-normalP95, capP99-normalP99)
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

func cleanupBenchmarkRedis(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return errors.New("Redis client is not initialized")
	}
	for pass := 1; pass <= 8; pass++ {
		var cursor uint64
		for {
			keys, nextCursor, err := client.WithContext(ctx).Scan(cursor, "*", 5000).Result()
			if err != nil {
				return fmt.Errorf("scan benchmark Redis DB during cleanup: %w", err)
			}
			if len(keys) > 0 {
				if err := client.WithContext(ctx).Unlink(keys...).Err(); err != nil {
					return fmt.Errorf("unlink benchmark Redis key batch of %d: %w", len(keys), err)
				}
			}
			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
		remaining, err := client.WithContext(ctx).DBSize().Result()
		if err != nil {
			return fmt.Errorf("check benchmark Redis DB cleanup pass %d: %w", pass, err)
		}
		if remaining == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("benchmark Redis DB still has %d keys after cleanup pass %d: %w", remaining, pass, err)
		}
	}
	remaining, err := client.WithContext(ctx).DBSize().Result()
	if err != nil {
		return fmt.Errorf("verify benchmark Redis cleanup: %w", err)
	}
	if remaining != 0 {
		return fmt.Errorf("benchmark Redis DB still has %d keys after 8 SCAN/UNLINK passes", remaining)
	}
	return nil
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
