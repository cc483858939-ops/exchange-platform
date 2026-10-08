package tasks

import (
	"testing"
	"time"
)

func TestKafkaBacklogRequiresFreshGroupSamplesAndIgnoresReaderUpdates(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	name := PipelineNotificationProjection
	registerPipelineAt(name, 2, base)
	pipelineStartedAt(name, base)
	pipelineStartedAt(name, base)
	if reasons := evaluatePipelineHealth(base); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatalf("unmeasured backlog considered healthy: %v", reasons)
	}
	pipelineKafkaBacklogSample(name, 500, base, true)
	pipelineCommitAt(name, base, 0, base)
	pipelineFailureAt(name, "temporary", 0, base)
	pipelineIdleAt(name, 0, base.Add(time.Second))
	state := pipelineSnapshots()[name]
	if state.Backlog != 500 || !state.LastProgressAt.Equal(base) {
		t.Fatalf("consumer updates changed sampled backlog/progress: %+v", state)
	}
	pipelineKafkaBacklogSample(name, 0, base.Add(time.Second), false)
	state = pipelineSnapshots()[name]
	if state.Backlog != 500 || state.BacklogSampleValid || !state.BacklogSampleAt.Equal(base) {
		t.Fatalf("failure erased sample: %+v", state)
	}
	if reasons := evaluatePipelineHealth(base.Add(time.Second)); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatal(reasons)
	}
	pipelineKafkaBacklogSample(name, 0, base.Add(2*time.Second), true)
	if reasons := evaluatePipelineHealth(base.Add(2 * time.Second)); len(reasons) != 0 {
		t.Fatalf("successful empty sample not healthy: %v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(2*time.Second + kafkaBacklogMaxAge + time.Nanosecond)); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatalf("stale empty sample healthy: %v", reasons)
	}
}
