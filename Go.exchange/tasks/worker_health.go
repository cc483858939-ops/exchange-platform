package tasks

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
	"Go.exchange/likes"
	"Go.exchange/metrics"
	"Go.exchange/runtimehealth"

	"github.com/segmentio/kafka-go"
)

var workerReady atomic.Bool
var postAnalysisConsumers atomic.Int32
var userBehaviorConsumers atomic.Int32
var recommendationMetricsConsumers atomic.Int32
var likeSnapshotConsumers atomic.Int32
var likeEventRelayRunning atomic.Bool
var likeBehaviorRelayWorkers atomic.Int32
var likeSnapshotRelayRunning atomic.Bool

const (
	PipelineUserBehaviorProjection = "user_behavior_projection"
	PipelineNotificationProjection = "notification_projection"
	PipelineRecommendationMetrics  = "recommendation_metrics"
	PipelineRecommendationProfile  = "recommendation_profile_materializer"
	PipelineLikeStateRelay         = "like_state_relay"
	PipelineLikeBehaviorRelay      = "like_behavior_relay"
	PipelineLikeSnapshotRelay      = "like_snapshot_relay"
	PipelineLikeSnapshotProjection = "like_snapshot_projection"
	PipelineLikeStateMaintenance   = "like_state_maintenance"
	PipelinePostEmbedding          = "post_embedding"
)

const (
	workerReadinessInterval = 2 * time.Second
	workerSnapshotMaxAge    = 5 * time.Second
	workerCheckTimeout      = time.Second
	workerTotalCheckTimeout = 2 * time.Second
	workerActiveGrace       = 15 * time.Second
	workerBacklogGrace      = 60 * time.Second
	workerDueQueueGrace     = 120 * time.Second
)

type workerPipelineState struct {
	registered            bool
	expectedWorkers       int
	activeWorkers         int
	registeredAt          time.Time
	startedAt             time.Time
	lastSuccessAt         time.Time
	lastFailureAt         time.Time
	consecutiveFailures   int
	lastCommittedOffsetAt time.Time
	backlogSince          time.Time
	lastProgressAt        time.Time
	backlog               int64
	state                 string
	reasonCode            string
}

var workerPipelines = struct {
	sync.RWMutex
	states map[string]workerPipelineState
}{states: make(map[string]workerPipelineState)}

var workerSnapshot atomic.Value
var workerShuttingDown atomic.Bool
var workerReadinessTransitionMu sync.Mutex
var workerLastTransitionKey string

func init() {
	workerSnapshot.Store(runtimehealth.WorkerReadinessSnapshot{
		Status: "not_ready", Role: config.RuntimeRoleWorker,
		Checks:      map[string]string{"database": "not_checked", "schema": "not_checked", "redis": "not_checked", "kafka": "not_checked"},
		Pipelines:   map[string]runtimehealth.WorkerPipelineSnapshot{},
		ReasonCodes: []string{"readiness_snapshot_stale"},
	})
}

func WorkerReady() bool {
	return WorkerReadinessSnapshot().Status == "ready"
}
func refreshWorkerReadiness(ctx context.Context) error {
	if err := refreshDatabaseReadiness(ctx); err != nil {
		return err
	}
	if err := refreshWorkerSchemaReadiness(ctx); err != nil {
		return err
	}
	if err := refreshRedisReadiness(ctx); err != nil {
		return err
	}
	return refreshKafkaReadiness(ctx)
}

func refreshDatabaseReadiness(ctx context.Context) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	db, err := global.Db.DB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func refreshWorkerSchemaReadiness(ctx context.Context) error {
	return initialize.CheckRuntimeSchema(ctx, global.Db, initialize.SchemaValidationOptions{
		RequiredVersion:     initialize.RequiredSchemaVersion,
		IncludeWorkerTables: true,
		EmbeddingEnabled:    config.AppConfig != nil && config.AppConfig.Embedding.Enabled,
	})
}

func refreshRedisReadiness(ctx context.Context) error {
	if global.RedisDB == nil {
		return errors.New("redis is not initialized")
	}
	return global.RedisDB.WithContext(ctx).Ping().Err()
}

func refreshKafkaReadiness(ctx context.Context) error {
	if config.AppConfig == nil {
		return errors.New("Kafka configuration is not initialized")
	}
	return eventing.KafkaReachable(ctx, config.AppConfig.Kafka)
}
func startWorkerReadinessProbe(ctx context.Context, wg interface {
	Add(int)
	Done()
}) {
	RegisterWorkerPipelines()
	wg.Add(1)
	go func() {
		defer wg.Done()
		evaluateWorkerReadiness(ctx)
		ticker := time.NewTicker(workerReadinessInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				workerReady.Store(false)
				storeWorkerSnapshot(runtimehealth.WorkerReadinessSnapshot{
					Status: "not_ready", Role: config.RuntimeRoleWorker,
					Checks:    map[string]string{"database": "not_checked", "schema": "not_checked", "redis": "not_checked", "kafka": "not_checked"},
					Pipelines: pipelineSnapshots(), ReasonCodes: []string{"shutting_down"}, EvaluatedAt: time.Now().UTC(),
				})
				return
			case <-ticker.C:
				evaluateWorkerReadiness(ctx)
			}
		}
	}()
}

func WorkerReadinessSnapshot() runtimehealth.WorkerReadinessSnapshot {
	snapshot := workerSnapshot.Load().(runtimehealth.WorkerReadinessSnapshot)
	result := cloneWorkerSnapshot(snapshot)
	if result.EvaluatedAt.IsZero() || time.Since(result.EvaluatedAt) > workerSnapshotMaxAge {
		result.Status = "not_ready"
		result.ReasonCodes = appendReason(result.ReasonCodes, "readiness_snapshot_stale")
	}
	return result
}

func MarkWorkerShuttingDown() {
	workerShuttingDown.Store(true)
	storeWorkerSnapshot(runtimehealth.WorkerReadinessSnapshot{
		Status: "not_ready", Role: config.RuntimeRoleWorker,
		Checks:    map[string]string{"database": "not_checked", "schema": "not_checked", "redis": "not_checked", "kafka": "not_checked"},
		Pipelines: pipelineSnapshots(), ReasonCodes: []string{"shutting_down"}, EvaluatedAt: time.Now().UTC(),
	})
}

func RegisterWorkerPipelines() {
	RegisterPipeline(PipelineUserBehaviorProjection, config.LikeBehaviorProjectionConsumers())
	RegisterPipeline(PipelineRecommendationMetrics, 1)
	RegisterPipeline(PipelineRecommendationProfile, 1)
	RegisterPipeline(PipelineLikeStateRelay, 1)
	RegisterPipeline(PipelineLikeBehaviorRelay, 1)
	RegisterPipeline(PipelineLikeSnapshotRelay, 1)
	RegisterPipeline(PipelineLikeSnapshotProjection, 1)
	RegisterPipeline(PipelineLikeStateMaintenance, 1)
	if notificationConsumerConfigured() {
		RegisterPipeline(PipelineNotificationProjection, config.NotificationProjectionConsumers())
	} else {
		UnregisterPipeline(PipelineNotificationProjection)
	}
	if config.AppConfig != nil && config.AppConfig.Embedding.Enabled {
		RegisterPipeline(PipelinePostEmbedding, 1)
	} else {
		UnregisterPipeline(PipelinePostEmbedding)
	}
}

func RegisterPipeline(name string, expectedWorkers int) {
	registerPipelineAt(name, expectedWorkers, time.Now().UTC())
}

func registerPipelineAt(name string, expectedWorkers int, now time.Time) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if expectedWorkers < 1 {
		expectedWorkers = 1
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	workerPipelines.Lock()
	state := workerPipelines.states[name]
	state.registered = true
	state.expectedWorkers = expectedWorkers
	if state.registeredAt.IsZero() {
		state.registeredAt = now
	}
	if state.state == "" {
		state.state = "starting"
	}
	workerPipelines.states[name] = state
	workerPipelines.Unlock()
}

func UnregisterPipeline(name string) {
	workerPipelines.Lock()
	delete(workerPipelines.states, name)
	workerPipelines.Unlock()
}

func PipelineStarted(name string) {
	pipelineStartedAt(name, time.Now().UTC())
}

func pipelineStartedAt(name string, now time.Time) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		state.activeWorkers++
		if state.startedAt.IsZero() {
			state.startedAt = now
		}
		state.state = "running"
		state.reasonCode = ""
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineStopped(name string) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		if state.activeWorkers > 0 {
			state.activeWorkers--
		}
		if state.activeWorkers == 0 && state.state != "failed" {
			state.state = "stopped"
		}
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineSuccess(name string, backlog int64) {
	pipelineSuccessAt(name, backlog, time.Now().UTC())
}

func pipelineSuccessAt(name string, backlog int64, now time.Time) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		setPipelineBacklog(&state, backlog, now)
		state.lastSuccessAt = now
		state.lastProgressAt = now
		state.consecutiveFailures = 0
		state.state = "running"
		state.reasonCode = ""
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineCommit(name string, offset time.Time, backlog int64) {
	pipelineCommitAt(name, offset, backlog, time.Now().UTC())
}

func pipelineCommitAt(name string, offset time.Time, backlog int64, now time.Time) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		if offset.IsZero() {
			offset = now
		}
		setPipelineBacklog(&state, backlog, now)
		state.lastCommittedOffsetAt = offset
		state.lastSuccessAt = now
		state.lastProgressAt = now
		state.consecutiveFailures = 0
		state.state = "running"
		state.reasonCode = ""
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineIdle(name string, backlog int64) {
	pipelineIdleAt(name, backlog, time.Now().UTC())
}

func pipelineIdleAt(name string, backlog int64, now time.Time) {
	backlog = normalizeBacklog(backlog)
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		setPipelineBacklog(&state, backlog, now)
		if backlog == 0 {
			state.lastSuccessAt = now
			state.lastProgressAt = now
			state.consecutiveFailures = 0
			state.state = "idle"
			state.reasonCode = ""
		} else {
			state.state = "idle"
		}
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineFailure(name, reason string, backlog int64) {
	pipelineFailureAt(name, reason, backlog, time.Now().UTC())
}

func pipelineFailureAt(name, reason string, backlog int64, now time.Time) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		setPipelineBacklog(&state, backlog, now)
		state.lastFailureAt = now
		state.consecutiveFailures++
		state.state = "failed"
		state.reasonCode = stablePipelineReason(reason)
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func PipelineBacklog(name string, backlog int64) {
	PipelineBacklogAt(name, backlog, time.Now().UTC())
}

func PipelineBacklogAt(name string, backlog int64, now time.Time) {
	workerPipelines.Lock()
	state, ok := workerPipelines.states[name]
	if ok {
		setPipelineBacklog(&state, backlog, now)
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

func setPipelineBacklog(state *workerPipelineState, backlog int64, now time.Time) {
	if state == nil {
		return
	}
	backlog = normalizeBacklog(backlog)
	previous := state.backlog
	state.backlog = backlog
	switch {
	case previous == 0 && backlog > 0:
		state.backlogSince = now
		state.lastProgressAt = now
	case backlog == 0:
		state.backlogSince = time.Time{}
		state.lastProgressAt = now
	case backlog < previous:
		state.lastProgressAt = now
	}
}

type workerCheck struct {
	name string
	fn   func(context.Context) error
}

type workerCheckResult struct {
	name string
	err  error
}

func evaluateWorkerReadiness(ctx context.Context) {
	if workerShuttingDown.Load() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, workerTotalCheckTimeout)
	defer cancel()
	checks := []workerCheck{
		{name: "database", fn: refreshDatabaseReadiness},
		{name: "schema", fn: refreshWorkerSchemaReadiness},
		{name: "redis", fn: refreshRedisReadiness},
		{name: "kafka", fn: refreshKafkaReadiness},
	}
	results := make(chan workerCheckResult, len(checks))
	for _, current := range checks {
		go func(current workerCheck) {
			child, childCancel := context.WithTimeout(checkCtx, workerCheckTimeout)
			defer childCancel()
			done := make(chan error, 1)
			go func() { done <- current.fn(child) }()
			select {
			case err := <-done:
				results <- workerCheckResult{name: current.name, err: err}
			case <-child.Done():
				results <- workerCheckResult{name: current.name, err: child.Err()}
			}
		}(current)
	}
	statuses := map[string]string{"database": "failed", "schema": "failed", "redis": "failed", "kafka": "failed"}
	reasons := make([]string, 0, len(checks))
	timedOut := false
	for range checks {
		select {
		case result := <-results:
			if result.err == nil {
				statuses[result.name] = "ok"
			} else {
				reasons = append(reasons, workerCheckReason(result.name, result.err))
			}
		case <-checkCtx.Done():
			timedOut = true
		}
		if timedOut {
			break
		}
	}
	if timedOut {
		for _, current := range checks {
			if statuses[current.name] == "failed" {
				reasons = append(reasons, current.name+"_check_timeout")
			}
		}
	}
	refreshWorkerBacklogs(checkCtx)
	now := time.Now().UTC()
	reasons = append(reasons, evaluatePipelineHealth(now)...)
	status := "ready"
	if len(reasons) > 0 {
		status = "not_ready"
	}
	snapshot := runtimehealth.WorkerReadinessSnapshot{
		Status: status, Role: config.RuntimeRoleWorker, Checks: statuses,
		Pipelines: pipelineSnapshots(), ReasonCodes: uniqueReasons(reasons), EvaluatedAt: now,
	}
	if workerShuttingDown.Load() {
		return
	}
	workerReady.Store(status == "ready")
	storeWorkerSnapshot(snapshot)
}

func refreshWorkerBacklogs(ctx context.Context) {
	now := time.Now().UTC()
	if global.Db != nil {
		var due int64
		if err := global.Db.WithContext(ctx).Table("user_reco_profile_dirty").Where("next_attempt_at <= ?", now).Count(&due).Error; err == nil {
			PipelineBacklogAt(PipelineRecommendationProfile, due, now)
		}
	}
	if global.RedisDB != nil {
		behaviorBacklog := int64(0)
		if value, err := global.RedisDB.WithContext(ctx).SCard(likes.BehaviorDirtyKey).Result(); err == nil {
			behaviorBacklog += value
		}
		if value, err := global.RedisDB.WithContext(ctx).ZCard(likes.BehaviorProcessingKey).Result(); err == nil {
			behaviorBacklog += value
		}
		PipelineBacklogAt(PipelineLikeBehaviorRelay, behaviorBacklog, now)

		snapshotBacklog := int64(0)
		if value, err := global.RedisDB.WithContext(ctx).SCard(likes.DirtyKey).Result(); err == nil {
			snapshotBacklog += value
		}
		if value, err := global.RedisDB.WithContext(ctx).ZCard(likes.ProcessingKey).Result(); err == nil {
			snapshotBacklog += value
		}
		PipelineBacklogAt(PipelineLikeSnapshotRelay, snapshotBacklog, now)
	}
}

func evaluatePipelineHealth(now time.Time) []string {
	workerPipelines.RLock()
	states := make(map[string]workerPipelineState, len(workerPipelines.states))
	for name, state := range workerPipelines.states {
		states[name] = state
	}
	workerPipelines.RUnlock()
	reasons := make([]string, 0)
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		state := states[name]
		blocked, backlogReason := pipelineBacklogBlocked(name, now, state)
		reason := ""
		switch {
		case !state.registered:
			reason = "worker_supervisor_not_registered"
		case state.consecutiveFailures >= 3:
			reason = "worker_pipeline_consecutive_failures"
		case state.activeWorkers < state.expectedWorkers && !state.registeredAt.IsZero() && now.Sub(state.registeredAt) <= workerActiveGrace:
			reason = "worker_pipeline_starting"
		case state.activeWorkers < state.expectedWorkers:
			reason = "worker_workers_below_expected"
		case blocked:
			reason = backlogReason
		}
		healthy := reason == ""
		metrics.SetWorkerPipelineHealthy(name, healthy)
		metrics.SetWorkerPipelineConsecutiveFailures(name, state.consecutiveFailures)
		metrics.SetWorkerPipelineLastSuccess(name, state.lastSuccessAt)
		metrics.SetWorkerPipelineBacklog(name, state.backlog)
		metrics.SetWorkerPipelineBacklogStalled(name, blocked)
		if reason != "" {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

type pipelineBacklogPolicy struct {
	Grace  time.Duration
	Reason string
}

func backlogPolicy(name string) pipelineBacklogPolicy {
	if name == PipelineRecommendationProfile {
		return pipelineBacklogPolicy{
			Grace:  workerDueQueueGrace,
			Reason: "worker_pipeline_due_unprocessed",
		}
	}
	return pipelineBacklogPolicy{
		Grace:  workerBacklogGrace,
		Reason: "worker_pipeline_backlog_stalled",
	}
}

func pipelineBacklogBlocked(name string, now time.Time, state workerPipelineState) (bool, string) {
	policy := backlogPolicy(name)
	if state.backlog <= 0 || !backlogExceeded(now, state, policy.Grace) {
		return false, ""
	}
	return true, policy.Reason
}

func backlogExceeded(now time.Time, state workerPipelineState, grace time.Duration) bool {
	progressBase := state.backlogSince
	if state.lastProgressAt.After(progressBase) {
		progressBase = state.lastProgressAt
	}
	return !progressBase.IsZero() && now.Sub(progressBase) > grace
}

func pipelineSnapshots() map[string]runtimehealth.WorkerPipelineSnapshot {
	workerPipelines.RLock()
	defer workerPipelines.RUnlock()
	result := make(map[string]runtimehealth.WorkerPipelineSnapshot, len(workerPipelines.states))
	for name, state := range workerPipelines.states {
		result[name] = runtimehealth.WorkerPipelineSnapshot{
			ExpectedWorkers: state.expectedWorkers, ActiveWorkers: state.activeWorkers,
			StartedAt: state.startedAt, LastSuccessAt: state.lastSuccessAt,
			LastFailureAt: state.lastFailureAt, ConsecutiveFailures: state.consecutiveFailures,
			LastCommittedOffsetAt: state.lastCommittedOffsetAt, BacklogSince: state.backlogSince,
			LastProgressAt: state.lastProgressAt, Backlog: state.backlog,
			State: state.state, ReasonCode: state.reasonCode,
		}
	}
	return result
}

func storeWorkerSnapshot(snapshot runtimehealth.WorkerReadinessSnapshot) {
	previous := workerSnapshot.Load().(runtimehealth.WorkerReadinessSnapshot)
	workerSnapshot.Store(cloneWorkerSnapshot(snapshot))
	metrics.SetRuntimeReadinessLastEvaluation(config.RuntimeRoleWorker, snapshot.EvaluatedAt)
	for check, status := range snapshot.Checks {
		metrics.SetRuntimeReadiness(config.RuntimeRoleWorker, check, status == "ok")
	}
	if snapshot.Status == "ready" {
		metrics.SetRuntimeReadinessLastSuccess(config.RuntimeRoleWorker, snapshot.EvaluatedAt)
	}
	transitionKey := snapshot.Status + ":" + strings.Join(snapshot.ReasonCodes, ",")
	workerReadinessTransitionMu.Lock()
	previousKey := workerLastTransitionKey
	workerLastTransitionKey = transitionKey
	workerReadinessTransitionMu.Unlock()
	if previousKey != "" && previousKey != transitionKey {
		metrics.RecordRuntimeReadinessTransition(config.RuntimeRoleWorker, previous.Status, snapshot.Status, strings.Join(snapshot.ReasonCodes, ","))
		log.Printf("[Readiness:%s] %s -> %s", config.RuntimeRoleWorker, previous.Status, snapshot.Status)
	}
}

func cloneWorkerSnapshot(snapshot runtimehealth.WorkerReadinessSnapshot) runtimehealth.WorkerReadinessSnapshot {
	checks := make(map[string]string, len(snapshot.Checks))
	for key, value := range snapshot.Checks {
		checks[key] = value
	}
	pipelines := make(map[string]runtimehealth.WorkerPipelineSnapshot, len(snapshot.Pipelines))
	for key, value := range snapshot.Pipelines {
		pipelines[key] = value
	}
	snapshot.Checks = checks
	snapshot.Pipelines = pipelines
	snapshot.ReasonCodes = append([]string(nil), snapshot.ReasonCodes...)
	return snapshot
}

func appendReason(reasons []string, reason string) []string {
	return uniqueReasons(append(reasons, reason))
}

func uniqueReasons(reasons []string) []string {
	seen := make(map[string]struct{}, len(reasons))
	result := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}

func workerCheckReason(name string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return name + "_check_timeout"
	}
	if name == "schema" {
		return initialize.SchemaReasonCode(err)
	}
	return name + "_unavailable"
}

func stablePipelineReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" || strings.IndexFunc(reason, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_'
	}) >= 0 {
		return "worker_pipeline_failure"
	}
	return reason
}

func normalizeBacklog(backlog int64) int64 {
	if backlog < 0 {
		return 0
	}
	return backlog
}

func kafkaBacklog(reader interface{ Stats() kafka.ReaderStats }) int64 {
	if reader == nil {
		return 0
	}
	lag := reader.Stats().Lag
	if lag < 0 {
		return 0
	}
	return int64(lag)
}
