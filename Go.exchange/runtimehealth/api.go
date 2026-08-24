package runtimehealth

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/metrics"
)

const (
	defaultEvaluationInterval = 2 * time.Second
	readinessSnapshotMaxAge   = 5 * time.Second
	perCheckTimeout           = time.Second
	totalCheckTimeout         = 2 * time.Second
)

type APISnapshot struct {
	Status      string            `json:"status"`
	Role        string            `json:"role"`
	Checks      map[string]string `json:"checks"`
	ReasonCodes []string          `json:"reason_codes,omitempty"`
	EvaluatedAt time.Time         `json:"-"`
}

type APIReadinessProvider interface {
	Snapshot() APISnapshot
}

func (snapshot APISnapshot) clone() APISnapshot {
	checks := make(map[string]string, len(snapshot.Checks))
	for key, value := range snapshot.Checks {
		checks[key] = value
	}
	reasons := append([]string(nil), snapshot.ReasonCodes...)
	snapshot.Checks = checks
	snapshot.ReasonCodes = reasons
	return snapshot
}

type APIOptions struct {
	Role                  string
	RequiredSchemaVersion int64
	Interval              time.Duration
	EmbeddingEnabled      bool
	Now                   func() time.Time
	DatabasePing          func(context.Context) error
	SchemaCheck           func(context.Context) error
	RedisPing             func(context.Context) error
	KafkaPing             func(context.Context) error
}

type APIReadiness struct {
	options           APIOptions
	snapshot          atomic.Value
	startOnce         sync.Once
	stopOnce          sync.Once
	cancelMu          sync.Mutex
	cancel            context.CancelFunc
	stateMu           sync.Mutex
	lastTransitionKey string
	shuttingDown      bool
}

func NewAPIReadiness(options APIOptions) *APIReadiness {
	if strings.TrimSpace(options.Role) == "" {
		options.Role = config.RuntimeRoleAPI
	}
	if options.RequiredSchemaVersion <= 0 {
		options.RequiredSchemaVersion = initialize.RequiredSchemaVersion
	}
	if options.Interval <= 0 {
		options.Interval = defaultEvaluationInterval
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DatabasePing == nil {
		options.DatabasePing = defaultDatabasePing
	}
	if options.SchemaCheck == nil {
		options.SchemaCheck = func(ctx context.Context) error {
			return initialize.CheckRuntimeSchema(ctx, global.Db, initialize.SchemaValidationOptions{
				RequiredVersion:  options.RequiredSchemaVersion,
				EmbeddingEnabled: options.EmbeddingEnabled,
			})
		}
	}
	if options.RedisPing == nil {
		options.RedisPing = defaultRedisPing
	}
	if options.KafkaPing == nil {
		options.KafkaPing = func(ctx context.Context) error {
			if config.AppConfig == nil {
				return errors.New("Kafka configuration is not initialized")
			}
			return eventing.KafkaReachable(ctx, config.AppConfig.Kafka)
		}
	}
	readiness := &APIReadiness{options: options}
	readiness.snapshot.Store(APISnapshot{
		Status: "not_ready",
		Role:   options.Role,
		Checks: map[string]string{
			"database": "not_checked",
			"schema":   "not_checked",
			"redis":    "not_checked",
			"kafka":    "degraded",
		},
		ReasonCodes: []string{"readiness_snapshot_stale"},
	})
	return readiness
}

func (readiness *APIReadiness) Start(ctx context.Context) {
	if readiness == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	readiness.startOnce.Do(func() {
		loopCtx, cancel := context.WithCancel(ctx)
		readiness.cancelMu.Lock()
		readiness.cancel = cancel
		readiness.cancelMu.Unlock()
		go readiness.run(loopCtx)
	})
}

func (readiness *APIReadiness) run(ctx context.Context) {
	readiness.EvaluateNow(ctx)
	ticker := time.NewTicker(readiness.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			readiness.EvaluateNow(ctx)
		}
	}
}

func (readiness *APIReadiness) Stop() {
	if readiness == nil {
		return
	}
	readiness.stopOnce.Do(func() {
		readiness.MarkShuttingDown()
		readiness.cancelMu.Lock()
		cancel := readiness.cancel
		readiness.cancelMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

func (readiness *APIReadiness) MarkShuttingDown() {
	if readiness == nil {
		return
	}
	readiness.stateMu.Lock()
	readiness.shuttingDown = true
	readiness.stateMu.Unlock()
	readiness.store(APISnapshot{
		Status: "not_ready",
		Role:   readiness.options.Role,
		Checks: map[string]string{
			"database": "not_checked",
			"schema":   "not_checked",
			"redis":    "not_checked",
			"kafka":    "degraded",
		},
		ReasonCodes: []string{"shutting_down"},
		EvaluatedAt: readiness.options.Now(),
	})
}

func (readiness *APIReadiness) Snapshot() APISnapshot {
	if readiness == nil {
		return APISnapshot{Status: "not_ready", Role: config.RuntimeRoleAPI, ReasonCodes: []string{"readiness_snapshot_stale"}}
	}
	snapshot := readiness.snapshot.Load().(APISnapshot).clone()
	now := readiness.options.Now()
	if snapshot.EvaluatedAt.IsZero() || now.Sub(snapshot.EvaluatedAt) > readinessSnapshotMaxAge {
		snapshot.Status = "not_ready"
		snapshot.ReasonCodes = addReason(snapshot.ReasonCodes, "readiness_snapshot_stale")
	}
	return snapshot
}

func (readiness *APIReadiness) EvaluateNow(ctx context.Context) APISnapshot {
	if readiness == nil {
		return APISnapshot{Status: "not_ready", Role: config.RuntimeRoleAPI, ReasonCodes: []string{"readiness_snapshot_stale"}}
	}
	readiness.stateMu.Lock()
	shuttingDown := readiness.shuttingDown
	readiness.stateMu.Unlock()
	if shuttingDown {
		return readiness.Snapshot()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, totalCheckTimeout)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	checks := []struct {
		name string
		fn   func(context.Context) error
	}{
		{name: "database", fn: readiness.options.DatabasePing},
		{name: "schema", fn: readiness.options.SchemaCheck},
		{name: "redis", fn: readiness.options.RedisPing},
		{name: "kafka", fn: readiness.options.KafkaPing},
	}
	results := make(chan result, len(checks))
	for _, check := range checks {
		go func(checkName string, checkFn func(context.Context) error) {
			child, childCancel := context.WithTimeout(checkCtx, perCheckTimeout)
			defer childCancel()
			done := make(chan error, 1)
			go func() { done <- checkFn(child) }()
			select {
			case err := <-done:
				results <- result{name: checkName, err: err}
			case <-child.Done():
				results <- result{name: checkName, err: child.Err()}
			}
		}(check.name, check.fn)
	}
	statuses := map[string]string{"database": "failed", "schema": "failed", "redis": "failed", "kafka": "degraded"}
	reasons := make([]string, 0, 3)
	timedOut := false
	for range checks {
		select {
		case result := <-results:
			if result.err == nil {
				statuses[result.name] = "ok"
				continue
			}
			if result.name == "kafka" {
				continue
			}
			reasons = append(reasons, stableCheckReason(result.name, result.err))
		case <-checkCtx.Done():
			timedOut = true
		}
		if timedOut {
			break
		}
	}
	if timedOut {
		for _, check := range checks {
			if statuses[check.name] == "failed" && check.name != "kafka" {
				reasons = append(reasons, check.name+"_check_timeout")
			}
		}
	}
	status := "ready"
	if len(reasons) > 0 {
		status = "not_ready"
	}
	snapshot := APISnapshot{
		Status: status, Role: readiness.options.Role, Checks: statuses,
		ReasonCodes: uniqueSortedReasons(reasons), EvaluatedAt: readiness.options.Now(),
	}
	readiness.store(snapshot)
	return snapshot.clone()
}

func (readiness *APIReadiness) store(snapshot APISnapshot) {
	readiness.stateMu.Lock()
	shuttingDown := readiness.shuttingDown
	readiness.stateMu.Unlock()
	if shuttingDown && !containsString(snapshot.ReasonCodes, "shutting_down") {
		return
	}
	previous := readiness.snapshot.Load().(APISnapshot)
	readiness.snapshot.Store(snapshot.clone())
	metrics.SetRuntimeReadinessLastEvaluation(readiness.options.Role, snapshot.EvaluatedAt)
	for check, status := range snapshot.Checks {
		metrics.SetRuntimeReadiness(readiness.options.Role, check, status == "ok")
	}
	if snapshot.Status == "ready" {
		metrics.SetRuntimeReadinessLastSuccess(readiness.options.Role, snapshot.EvaluatedAt)
	}
	transitionKey := snapshot.Status + ":" + strings.Join(snapshot.ReasonCodes, ",")
	readiness.stateMu.Lock()
	previousKey := readiness.lastTransitionKey
	readiness.lastTransitionKey = transitionKey
	readiness.stateMu.Unlock()
	if previousKey != "" && previousKey != transitionKey {
		metrics.RecordRuntimeReadinessTransition(readiness.options.Role, previous.Status, snapshot.Status, strings.Join(snapshot.ReasonCodes, ","))
		log.Printf("[Readiness:%s] %s -> %s", readiness.options.Role, previous.Status, snapshot.Status)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func defaultDatabasePing(ctx context.Context) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	db, err := global.Db.DB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func defaultRedisPing(ctx context.Context) error {
	if global.RedisDB == nil {
		return errors.New("redis is not initialized")
	}
	return global.RedisDB.WithContext(ctx).Ping().Err()
}

func stableCheckReason(name string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return name + "_check_timeout"
	}
	if name == "schema" {
		return initialize.SchemaReasonCode(err)
	}
	return name + "_unavailable"
}

func addReason(reasons []string, reason string) []string {
	return uniqueSortedReasons(append(reasons, reason))
}

func uniqueSortedReasons(reasons []string) []string {
	seen := make(map[string]struct{}, len(reasons))
	result := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if _, exists := seen[reason]; exists {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}
