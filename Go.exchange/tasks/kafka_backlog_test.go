package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
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

func TestKafkaPipelineIdleCannotClearFailureWithoutDedicatedRecovery(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	name := PipelineNotificationProjection
	registerPipelineAt(name, 1, base)
	pipelineStartedAt(name, base)
	for i := 0; i < 3; i++ {
		pipelineFailureAtWithKind(name, "kafka_fetch_failed", 0, pipelineFailureTransport, base.Add(time.Duration(i+1)*time.Second))
	}
	pipelineIdleAt(name, 0, base.Add(4*time.Second))
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 3 || state.State != "failed" || state.ReasonCode != "kafka_fetch_failed" {
		t.Fatalf("Kafka Idle cleared a failure without consumer-group evidence: %+v", state)
	}
}

func TestKafkaTransportIdleRecoveryRequiresAllEvidence(t *testing.T) {
	for _, test := range []struct {
		name          string
		failureKinds  []pipelineFailureKinds
		sessions      []string
		lag           int64
		lagErr        error
		groupState    string
		memberMode    string
		shuttingDown  bool
		wantRecovered bool
		wantGroupCall bool
		queryMutation string
	}{
		{name: "stable local members", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantRecovered: true, wantGroupCall: true},
		{name: "no local readers", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, groupState: "Stable", memberMode: "other", wantGroupCall: false},
		{name: "empty group", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Empty", memberMode: "local", wantGroupCall: true},
		{name: "preparing rebalance", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "PreparingRebalance", memberMode: "local", wantGroupCall: true},
		{name: "completing rebalance", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "CompletingRebalance", memberMode: "local", wantGroupCall: true},
		{name: "client id mismatch", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "other", wantGroupCall: true},
		{name: "member does not subscribe to target topic", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "wrong topic", wantGroupCall: true},
		{name: "partial local recovery", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1"}, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "other workers only", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "other", wantGroupCall: true},
		{name: "positive lag", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, lag: 2, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "lag sample error", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, lagErr: errors.New("broker unavailable"), groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "processing failure", failureKinds: []pipelineFailureKinds{pipelineFailureProcessing, pipelineFailureProcessing, pipelineFailureProcessing}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "commit failure", failureKinds: []pipelineFailureKinds{pipelineFailureCommit, pipelineFailureCommit, pipelineFailureCommit}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "configuration failure", failureKinds: []pipelineFailureKinds{pipelineFailureConfiguration, pipelineFailureConfiguration, pipelineFailureConfiguration}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "mixed failure history", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureProcessing, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantGroupCall: false},
		{name: "shutdown", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", shuttingDown: true, wantGroupCall: false},
		{name: "new failure during group query", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, sessions: []string{"reader-1", "reader-2"}, groupState: "Stable", memberMode: "local", wantGroupCall: true, queryMutation: "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetPipelineStates(t)
			base := time.Now().UTC()
			const name = PipelineNotificationProjection
			registerPipelineAt(name, 2, base)
			pipelineStartedAt(name, base)
			pipelineStartedAt(name, base)
			pipelineCommitAt(name, base, 0, base)
			for index, kind := range test.failureKinds {
				pipelineFailureAtWithKind(name, "test_failure", 0, kind, base.Add(time.Duration(index+1)*time.Second))
			}
			unregister := make([]func(), 0, len(test.sessions))
			for _, clientID := range test.sessions {
				unregister = append(unregister, registerKafkaConsumerSession(name, "events", "projection", clientID))
			}
			if test.shuttingDown {
				workerPipelines.Lock()
				workerShuttingDown.Store(true)
				workerPipelines.Unlock()
			}
			members := []eventing.KafkaGroupMember{}
			if test.memberMode == "local" {
				for _, clientID := range test.sessions {
					members = append(members, eventing.KafkaGroupMember{ClientID: clientID, Topics: []string{"events"}})
				}
			} else if test.memberMode == "wrong topic" {
				for _, clientID := range test.sessions {
					members = append(members, eventing.KafkaGroupMember{ClientID: clientID, Topics: []string{"other"}})
				}
			} else if test.memberMode == "other" {
				members = []eventing.KafkaGroupMember{{ClientID: "remote-1", Topics: []string{"events"}}, {ClientID: "remote-2", Topics: []string{"events"}}}
			}
			groupCalls := 0
			_, _, valid, err := sampleKafkaPipelineBacklog(
				context.Background(), name, "events", "projection",
				func(context.Context, string, string) (int64, error) { return test.lag, test.lagErr },
				func(context.Context, string) (eventing.KafkaGroupDescription, error) {
					groupCalls++
					if test.queryMutation == "failure" {
						pipelineFailureAtWithKind(name, "newer_failure", 0, pipelineFailureTransport, base.Add(10*time.Second))
					}
					return eventing.KafkaGroupDescription{GroupID: "projection", State: test.groupState, Members: members}, nil
				},
			)
			if valid != (test.lagErr == nil) || (err != nil) != (test.lagErr != nil) {
				t.Fatalf("valid=%v err=%v, lagErr=%v", valid, err, test.lagErr)
			}
			if (groupCalls > 0) != test.wantGroupCall {
				t.Fatalf("group query calls=%d, wantCalled=%v", groupCalls, test.wantGroupCall)
			}
			state := workerPipelines.states[name]
			if test.wantRecovered {
				if state.consecutiveFailures != 0 || state.state != "idle" || state.reasonCode != "" {
					t.Fatalf("valid idle recovery did not clear the failure: %+v", state)
				}
				if !state.lastCommittedOffsetAt.Equal(base) {
					t.Fatalf("idle recovery changed real commit time: %v", state.lastCommittedOffsetAt)
				}
				if reasons := evaluatePipelineHealth(time.Now().UTC()); containsWorkerReason(reasons, "worker_pipeline_consecutive_failures") || len(reasons) != 0 {
					t.Fatalf("recovered Kafka pipeline remains unhealthy: %v", reasons)
				}
			} else if state.consecutiveFailures == 0 {
				t.Fatalf("invalid recovery cleared failure state: %+v", state)
			}
			for _, closeSession := range unregister {
				closeSession()
			}
		})
	}
}

func TestKafkaIdleRecoveryRejectsSessionExitDuringGroupQuery(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	const name = PipelineNotificationProjection
	registerPipelineAt(name, 1, base)
	pipelineStartedAt(name, base)
	for i := 0; i < 3; i++ {
		pipelineFailureAtWithKind(name, "kafka_fetch_failed", 0, pipelineFailureTransport, base.Add(time.Duration(i+1)*time.Second))
	}
	unregister := registerKafkaConsumerSession(name, "events", "projection", "reader-1")
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	go func() {
		defer calls.Done()
		sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-1", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("group query did not start")
	}
	unregister()
	close(releaseGroup)
	calls.Wait()
	if state := pipelineSnapshots()[name]; state.ConsecutiveFailures != 3 || state.State != "failed" {
		t.Fatalf("closed reader was accepted as recovery evidence: %+v", state)
	}
}

func TestKafkaIdleRecoveryRejectsNewFailureDuringGroupQuery(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	const name = PipelineNotificationProjection
	registerPipelineAt(name, 1, base)
	pipelineStartedAt(name, base)
	for i := 0; i < 3; i++ {
		pipelineFailureAtWithKind(name, "kafka_fetch_failed", 0, pipelineFailureTransport, base.Add(time.Duration(i+1)*time.Second))
	}
	closeSession := registerKafkaConsumerSession(name, "events", "projection", "reader-1")
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	go func() {
		defer calls.Done()
		sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-1", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("group query did not start")
	}
	pipelineFailureWithKind(name, "newer_transport_failure", 0, pipelineFailureTransport)
	close(releaseGroup)
	calls.Wait()
	closeSession()
	if state := pipelineSnapshots()[name]; state.ConsecutiveFailures != 4 || state.State != "failed" {
		t.Fatalf("old recovery sample cleared a newer failure: %+v", state)
	}
}

func TestKafkaIdleRecoveryCannotOverwriteCommitOrShutdown(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(string)
		wantState string
	}{
		{name: "commit", mutate: func(name string) { pipelineCommitAt(name, time.Now().UTC(), 0, time.Now().UTC()) }, wantState: "running"},
		{name: "shutdown", mutate: func(string) {
			workerPipelines.Lock()
			workerShuttingDown.Store(true)
			workerPipelines.Unlock()
		}, wantState: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetPipelineStates(t)
			base := time.Now().UTC()
			const name = PipelineNotificationProjection
			registerPipelineAt(name, 1, base)
			pipelineStartedAt(name, base)
			for i := 0; i < 3; i++ {
				pipelineFailureAtWithKind(name, "kafka_fetch_failed", 0, pipelineFailureTransport, base.Add(time.Duration(i+1)*time.Second))
			}
			closeSession := registerKafkaConsumerSession(name, "events", "projection", "reader-1")
			groupStarted := make(chan struct{})
			releaseGroup := make(chan struct{})
			var calls sync.WaitGroup
			calls.Add(1)
			go func() {
				defer calls.Done()
				sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
					func(context.Context, string, string) (int64, error) { return 0, nil },
					func(context.Context, string) (eventing.KafkaGroupDescription, error) {
						close(groupStarted)
						<-releaseGroup
						return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-1", Topics: []string{"events"}}}}, nil
					})
			}()
			select {
			case <-groupStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("group query did not start")
			}
			test.mutate(name)
			close(releaseGroup)
			calls.Wait()
			closeSession()
			if state := workerPipelines.states[name]; state.state != test.wantState {
				t.Fatalf("state=%q want=%q details=%+v", state.state, test.wantState, state)
			}
		})
	}
}

func TestOlderKafkaBacklogSampleCannotReplaceNewerSample(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	name := PipelineNotificationProjection
	registerPipelineAt(name, 1, base)
	oldVersion, _ := beginKafkaBacklogSample(name)
	newVersion, _ := beginKafkaBacklogSample(name)
	if recordKafkaBacklogSample(name, oldVersion, 99, base.Add(time.Second), true) {
		t.Fatal("older Kafka sample overwrote the current sample")
	}
	if !recordKafkaBacklogSample(name, newVersion, 0, base.Add(2*time.Second), true) {
		t.Fatal("latest Kafka sample was rejected")
	}
	state := pipelineSnapshots()[name]
	if state.Backlog != 0 || !state.BacklogSampleAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("unexpected current Kafka sample: %+v", state)
	}
}

func TestKafkaFetchFailureClassificationUsesProtocolErrorCodes(t *testing.T) {
	if got := kafkaFetchFailureKind(errors.Join(errors.New("fetch failed"), kafka.TopicAuthorizationFailed)); got != pipelineFailureConfiguration {
		t.Fatalf("topic authorization failure kind=%v, want configuration", got)
	}
	if got := kafkaFetchFailureKind(kafka.UnknownTopicOrPartition); got != pipelineFailureConfiguration {
		t.Fatalf("unknown topic failure kind=%v, want configuration", got)
	}
	if got := kafkaFetchFailureKind(errors.New("connection reset")); got != pipelineFailureTransport {
		t.Fatalf("network failure kind=%v, want transport", got)
	}
}
