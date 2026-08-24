package tasks

import (
	"testing"
	"time"
)

func TestPipelineFailureThresholdAndRecovery(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	registerPipelineAt("threshold_pipeline", 1, base)
	pipelineStartedAt("threshold_pipeline", base.Add(time.Second))

	if reasons := evaluatePipelineHealth(base.Add(2 * time.Second)); len(reasons) != 0 {
		t.Fatalf("zero failures unexpectedly blocked pipeline: %#v", reasons)
	}
	for failures := 1; failures <= 2; failures++ {
		pipelineFailureAt("threshold_pipeline", "dependency_failed", 0, base.Add(time.Duration(failures+2)*time.Second))
		if reasons := evaluatePipelineHealth(base.Add(time.Duration(failures+2) * time.Second)); len(reasons) != 0 {
			t.Fatalf("%d consecutive failures unexpectedly blocked pipeline: %#v", failures, reasons)
		}
	}
	pipelineFailureAt("threshold_pipeline", "dependency_failed", 0, base.Add(5*time.Second))
	if reasons := evaluatePipelineHealth(base.Add(6 * time.Second)); !containsWorkerReason(reasons, "worker_pipeline_consecutive_failures") {
		t.Fatalf("expected third failure to block pipeline, got %#v", reasons)
	}

	pipelineCommitAt("threshold_pipeline", base.Add(7*time.Second), 0, base.Add(7*time.Second))
	state := pipelineSnapshots()["threshold_pipeline"]
	if state.ConsecutiveFailures != 0 || state.LastCommittedOffsetAt.IsZero() || !state.LastProgressAt.Equal(base.Add(7*time.Second)) {
		t.Fatalf("successful commit did not reset pipeline state: %#v", state)
	}
	if reasons := evaluatePipelineHealth(base.Add(8 * time.Second)); len(reasons) != 0 {
		t.Fatalf("successful commit did not recover pipeline: %#v", reasons)
	}
}

func TestPipelineSuccessAndIdleZeroResetFailureState(t *testing.T) {
	for _, test := range []struct {
		name    string
		recover func(string, time.Time)
	}{
		{name: "success", recover: func(name string, now time.Time) { pipelineSuccessAt(name, 0, now) }},
		{name: "idle", recover: func(name string, now time.Time) { pipelineIdleAt(name, 0, now) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetPipelineStates(t)
			base := time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC)
			registerPipelineAt("reset_pipeline", 1, base)
			pipelineStartedAt("reset_pipeline", base.Add(time.Second))
			pipelineFailureAt("reset_pipeline", "failed", 0, base.Add(2*time.Second))
			test.recover("reset_pipeline", base.Add(3*time.Second))
			state := pipelineSnapshots()["reset_pipeline"]
			if state.ConsecutiveFailures != 0 || state.LastSuccessAt.IsZero() || !state.LastProgressAt.Equal(base.Add(3*time.Second)) || state.ReasonCode != "" {
				t.Fatalf("recovery did not reset failure state: %#v", state)
			}
			if reasons := evaluatePipelineHealth(base.Add(4 * time.Second)); len(reasons) != 0 {
				t.Fatalf("recovered pipeline remained unhealthy: %#v", reasons)
			}
		})
	}
}

func TestPipelineStartupAndWorkerShortfallReasons(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	registerPipelineAt("startup_pipeline", 2, base)
	pipelineStartedAt("startup_pipeline", base.Add(time.Second))
	if reasons := evaluatePipelineHealth(base.Add(15 * time.Second)); !containsWorkerReason(reasons, "worker_pipeline_starting") {
		t.Fatalf("expected startup grace reason, got %#v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(15*time.Second + time.Nanosecond)); !containsWorkerReason(reasons, "worker_workers_below_expected") {
		t.Fatalf("expected worker shortfall reason after grace, got %#v", reasons)
	}
}

func TestRegisterPipelineIsIdempotentAndPreservesHistory(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	registerPipelineAt("idempotent_pipeline", 1, base)
	pipelineStartedAt("idempotent_pipeline", base.Add(time.Second))
	pipelineFailureAt("idempotent_pipeline", "failed", 1, base.Add(2*time.Second))
	registerPipelineAt("idempotent_pipeline", 3, base.Add(10*time.Second))

	state := pipelineSnapshots()["idempotent_pipeline"]
	if state.ExpectedWorkers != 3 || state.ActiveWorkers != 1 || !state.StartedAt.Equal(base.Add(time.Second)) || state.ConsecutiveFailures != 1 || state.Backlog != 1 || !state.BacklogSince.Equal(base.Add(2*time.Second)) || !state.LastProgressAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("re-registration did not preserve pipeline history: %#v", state)
	}
	workerPipelines.RLock()
	registeredAt := workerPipelines.states["idempotent_pipeline"].registeredAt
	workerPipelines.RUnlock()
	if !registeredAt.Equal(base) {
		t.Fatalf("re-registration changed registered_at: %v", registeredAt)
	}
}

func TestPipelineBacklogTransitionTimestamps(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	registerPipelineAt("backlog_pipeline", 1, base)
	pipelineStartedAt("backlog_pipeline", base.Add(time.Second))

	PipelineBacklogAt("backlog_pipeline", 4, base.Add(10*time.Second))
	state := pipelineSnapshots()["backlog_pipeline"]
	if !state.BacklogSince.Equal(base.Add(10*time.Second)) || !state.LastProgressAt.Equal(base.Add(10*time.Second)) {
		t.Fatalf("zero-to-positive backlog transition was not timestamped: %#v", state)
	}

	PipelineBacklogAt("backlog_pipeline", 7, base.Add(20*time.Second))
	state = pipelineSnapshots()["backlog_pipeline"]
	if !state.BacklogSince.Equal(base.Add(10*time.Second)) || !state.LastProgressAt.Equal(base.Add(10*time.Second)) {
		t.Fatalf("backlog increase reset progress timestamps: %#v", state)
	}

	PipelineBacklogAt("backlog_pipeline", 3, base.Add(30*time.Second))
	state = pipelineSnapshots()["backlog_pipeline"]
	if !state.BacklogSince.Equal(base.Add(10*time.Second)) || !state.LastProgressAt.Equal(base.Add(30*time.Second)) {
		t.Fatalf("backlog decrease did not update progress timestamp: %#v", state)
	}

	PipelineBacklogAt("backlog_pipeline", 0, base.Add(40*time.Second))
	state = pipelineSnapshots()["backlog_pipeline"]
	if !state.BacklogSince.IsZero() || !state.LastProgressAt.Equal(base.Add(40*time.Second)) {
		t.Fatalf("zero backlog transition was not recorded: %#v", state)
	}

	PipelineBacklogAt("backlog_pipeline", 2, base.Add(50*time.Second))
	state = pipelineSnapshots()["backlog_pipeline"]
	if !state.BacklogSince.Equal(base.Add(50*time.Second)) || !state.LastProgressAt.Equal(base.Add(50*time.Second)) {
		t.Fatalf("second zero-to-positive transition was not recorded: %#v", state)
	}
}

func TestPipelineBacklogGraceAndProgressBase(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	registerPipelineAt("normal_backlog_pipeline", 1, base)
	pipelineStartedAt("normal_backlog_pipeline", base)
	PipelineBacklogAt("normal_backlog_pipeline", 1, base)
	if reasons := evaluatePipelineHealth(base.Add(60 * time.Second)); len(reasons) != 0 {
		t.Fatalf("normal backlog failed at the exact grace boundary: %#v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(60*time.Second + time.Nanosecond)); len(reasons) != 1 || reasons[0] != "worker_pipeline_backlog_stalled" {
		t.Fatalf("expected only normal backlog stall after grace, got %#v", reasons)
	}

	resetPipelineStates(t)
	registerPipelineAt("progress_pipeline", 1, base)
	pipelineStartedAt("progress_pipeline", base)
	PipelineBacklogAt("progress_pipeline", 1, base.Add(time.Second))
	pipelineSuccessAt("progress_pipeline", 1, base.Add(30*time.Second))
	if reasons := evaluatePipelineHealth(base.Add(90 * time.Second)); containsWorkerReason(reasons, "worker_pipeline_backlog_stalled") {
		t.Fatalf("backlog stalled from backlog_since despite newer progress timestamp: %#v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(91 * time.Second)); !containsWorkerReason(reasons, "worker_pipeline_backlog_stalled") {
		t.Fatalf("expected stall after progress grace, got %#v", reasons)
	}
}

func TestRecommendationProfileBacklogUsesDueGrace(t *testing.T) {
	resetPipelineStates(t)
	base := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	registerPipelineAt(PipelineRecommendationProfile, 1, base)
	pipelineStartedAt(PipelineRecommendationProfile, base)
	PipelineBacklogAt(PipelineRecommendationProfile, 1, base)
	if reasons := evaluatePipelineHealth(base.Add(60*time.Second + time.Nanosecond)); len(reasons) != 0 {
		t.Fatalf("profile backlog was blocked by the normal grace: %#v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(120 * time.Second)); len(reasons) != 0 {
		t.Fatalf("profile backlog failed at the exact due grace boundary: %#v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(120*time.Second + time.Nanosecond)); len(reasons) != 1 || reasons[0] != "worker_pipeline_due_unprocessed" {
		t.Fatalf("expected only due profile backlog reason after grace, got %#v", reasons)
	}
}

func TestPipelineBacklogPolicyUsesPipelineSpecificGrace(t *testing.T) {
	profilePolicy := backlogPolicy(PipelineRecommendationProfile)
	if profilePolicy.Grace != 120*time.Second || profilePolicy.Reason != "worker_pipeline_due_unprocessed" {
		t.Fatalf("unexpected recommendation profile backlog policy: %#v", profilePolicy)
	}
	normalPolicy := backlogPolicy("normal_pipeline")
	if normalPolicy.Grace != 60*time.Second || normalPolicy.Reason != "worker_pipeline_backlog_stalled" {
		t.Fatalf("unexpected normal backlog policy: %#v", normalPolicy)
	}

	base := time.Date(2026, 8, 23, 19, 0, 0, 0, time.UTC)
	state := workerPipelineState{backlog: 1, backlogSince: base, lastProgressAt: base.Add(50 * time.Second)}
	if blocked, reason := pipelineBacklogBlocked("normal_pipeline", base.Add(110*time.Second), state); blocked || reason != "" {
		t.Fatalf("progress base was blocked at the exact normal grace boundary: blocked=%v reason=%q", blocked, reason)
	}
	if blocked, reason := pipelineBacklogBlocked("normal_pipeline", base.Add(110*time.Second+time.Nanosecond), state); !blocked || reason != "worker_pipeline_backlog_stalled" {
		t.Fatalf("expected normal backlog to block after progress grace: blocked=%v reason=%q", blocked, reason)
	}
}

func resetPipelineStates(t *testing.T) {
	t.Helper()
	workerPipelines.Lock()
	previous := workerPipelines.states
	workerPipelines.states = make(map[string]workerPipelineState)
	workerPipelines.Unlock()
	t.Cleanup(func() {
		workerPipelines.Lock()
		workerPipelines.states = previous
		workerPipelines.Unlock()
	})
}

func containsWorkerReason(reasons []string, wanted string) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}
