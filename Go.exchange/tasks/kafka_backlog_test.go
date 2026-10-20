package tasks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"Go.exchange/eventing"

	"github.com/segmentio/kafka-go"
)

func setupKafkaPipeline(t *testing.T, name string, expected int, clients []string) []func() {
	t.Helper()
	now := time.Now().UTC()
	registerPipelineAt(name, expected, now)
	unregister := make([]func(), 0, len(clients))
	for slotID := 0; slotID < expected; slotID++ {
		KafkaConsumerSlotStarted(name, slotID)
		if slotID < len(clients) {
			unregister = append(unregister, registerKafkaConsumerSession(name, slotID, "events", "projection", clients[slotID]))
		}
	}
	if expected > 0 && len(clients) == expected {
		_, candidate := beginKafkaBacklogSample(name)
		members := make([]eventing.KafkaGroupMember, 0, len(clients))
		for _, clientID := range clients {
			members = append(members, eventing.KafkaGroupMember{ClientID: clientID, Topics: []string{"events"}})
		}
		recordKafkaMembershipSample(context.Background(), name, candidate, "projection", eventing.KafkaGroupDescription{
			GroupID: "projection", State: "Stable", Members: members,
		}, now, true)
	}
	t.Cleanup(func() {
		for _, closeSession := range unregister {
			closeSession()
		}
	})
	return unregister
}

func expireKafkaStartupGrace(name string) {
	workerPipelines.Lock()
	state := workerPipelines.states[name]
	state.registeredAt = time.Now().UTC().Add(-workerActiveGrace - time.Second)
	workerPipelines.states[name] = state
	workerPipelines.Unlock()
}

func TestReaderCreatedButNotJoinedGroupIsNotReady(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	now := time.Now().UTC()
	registerPipelineAt(name, 1, now.Add(-workerActiveGrace-time.Second))
	KafkaConsumerSlotStarted(name, 0)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	pipelineKafkaBacklogSample(name, 0, now, true)

	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.sessionActive || slot.membership.Verified {
		t.Fatalf("new Reader session must remain locally active but group-unverified: %+v", slot)
	}
	if reasons := evaluatePipelineHealth(now); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("Reader creation made the pipeline ready without group proof: %v", reasons)
	}
}

func TestStableGroupMembershipMakesConsumerReady(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	lag := func(context.Context, string, string) (int64, error) { return 0, nil }
	group := func(context.Context, string) (eventing.KafkaGroupDescription, error) {
		return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
	}
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection", lag, group)
	if err != nil || !valid || !accepted {
		t.Fatalf("sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("Stable local membership did not make the pipeline ready: %v", reasons)
	}
}

func TestGroupVerificationRunsWithoutHistoricalFailure(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	groupCalls := 0
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			groupCalls++
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if err != nil || !valid || !accepted || groupCalls != 1 {
		t.Fatalf("zero-failure sample did not verify group: valid=%v accepted=%v calls=%d err=%v", valid, accepted, groupCalls, err)
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if !verified {
		t.Fatal("Stable membership was not recorded without historical failures")
	}
}

func TestMembershipVerificationDoesNotRequireZeroLag(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 7, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if err != nil || !valid || !accepted {
		t.Fatalf("positive lag sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if !verified {
		t.Fatal("positive lag incorrectly blocked membership verification")
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("fresh in-grace backlog should not block membership readiness: %v", reasons)
	}
}

func TestReaderRestartInvalidatesOldMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeA := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	_, candidate := beginKafkaBacklogSample(name)
	descriptionA := eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}
	if !recordKafkaMembershipSample(context.Background(), name, candidate, "projection", descriptionA, time.Now().UTC(), true) {
		t.Fatal("could not record Reader A membership")
	}
	closeA()
	closeB := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-b")
	t.Cleanup(closeB)
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.sessionActive || slot.session.clientID != "reader-b" || slot.membership.Verified {
		t.Fatalf("Reader B inherited Reader A membership: %+v", slot)
	}
	_, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-b", Topics: []string{"events"}}}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	workerPipelines.RLock()
	slot = workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.membership.Verified || slot.membership.SessionVersion != slot.sessionVersion {
		t.Fatalf("Reader B did not receive its own membership proof: %+v", slot)
	}
}

func TestRebalanceInvalidatesMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	query := func(groupState string) kafkaGroupQuery {
		return func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: groupState, Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		}
	}
	lag := func(context.Context, string, string) (int64, error) { return 0, nil }
	if _, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection", lag, query("PreparingRebalance")); err != nil {
		t.Fatal(err)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("rebalance did not make the pipeline not-ready: %v", reasons)
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if verified {
		t.Fatal("PreparingRebalance retained the previous proof")
	}
	if _, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection", lag, query("Stable")); err != nil {
		t.Fatal(err)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("Stable group did not restore readiness: %v", reasons)
	}
}

func TestMissingLocalMemberIsNotReady(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	expireKafkaStartupGrace(name)
	_, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("one remote/local member incorrectly represented both slots: %v", reasons)
	}
}

func TestGroupQueryFailureFailsClosedAndPreservesLag(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 12, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{}, errors.New("group unavailable")
		})
	if err != nil || !valid || !accepted {
		t.Fatalf("valid lag was lost with group error: valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	state := pipelineSnapshots()[name]
	if !state.BacklogSampleValid || state.Backlog != 12 {
		t.Fatalf("group failure replaced valid lag: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("old membership proof survived group query failure: %v", reasons)
	}
}

func TestLagQueryFailurePreservesSuccessfulMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	expectedErr := errors.New("lag unavailable")
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, expectedErr },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if valid || !accepted || !errors.Is(err, expectedErr) {
		t.Fatalf("lag failure sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if !verified {
		t.Fatal("successful Group proof was discarded because the independent Lag query failed")
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatalf("failed Lag sample did not independently block readiness: %v", reasons)
	}
}

func TestMembershipProofExpires(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	workerPipelines.Lock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	slot.membership.VerifiedAt = time.Now().UTC().Add(-kafkaMembershipMaxAge)
	workerPipelines.states[name].kafkaSlots[0] = slot
	workerPipelines.Unlock()
	if reasons := evaluatePipelineHealth(time.Now().UTC().Add(time.Nanosecond)); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("expired proof remained ready: %v", reasons)
	}
}

func TestOldGroupSampleCannotVerifyNewSession(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeA := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _, _, _, _ = sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("group query did not start")
	}
	closeA()
	closeB := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-b")
	t.Cleanup(closeB)
	close(releaseGroup)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("group sample did not finish")
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.session.clientID != "reader-b" || slot.membership.Verified {
		t.Fatalf("old Group sample verified the replacement session: %+v", slot)
	}
}

func TestOldStableSampleCannotOverrideNewRebalanceResult(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _, _, _, _ = sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("older group query did not start")
	}
	_, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "PreparingRebalance", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	close(releaseGroup)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("older group sample did not finish")
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if verified {
		t.Fatal("older Stable sample overrode the newer PreparingRebalance result")
	}
}

func TestIdleRecoveryStillWorksWithNoNewMessages(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		})
	if err != nil || !valid || !accepted {
		t.Fatalf("idle recovery sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 0 || state.State != "idle" {
		t.Fatalf("transport failure did not recover without a new commit: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("idle recovery left pipeline unhealthy: %v", reasons)
	}
}

func TestCancelledGroupSampleCannotVerifyMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	expireKafkaStartupGrace(name)
	now := time.Now().UTC()
	pipelineKafkaBacklogSample(name, 0, now, true)
	_, candidate := beginKafkaBacklogSample(name)

	ctx, cancel := context.WithCancel(context.Background())
	completedAt := time.Now().UTC()
	cancel()
	description := eventing.KafkaGroupDescription{
		GroupID: "projection", State: "Stable",
		Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}},
	}
	if kafkaSampleCompletedBeforeDeadline(ctx, completedAt) {
		t.Fatal("cancelled Group result was accepted as current")
	}
	if recordKafkaMembershipSample(ctx, name, candidate, "projection", description, completedAt, true) {
		t.Fatal("cancelled Group sample was committed")
	}
	workerPipelines.RLock()
	membership := workerPipelines.states[name].kafkaSlots[0].membership
	workerPipelines.RUnlock()
	if membership.Verified {
		t.Fatalf("cancelled Group sample created membership proof: %+v", membership)
	}
	if reasons := evaluatePipelineHealth(now); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("cancelled Group sample made pipeline ready: %v", reasons)
	}
}

func TestCancelledGroupContextKeepsIndependentCompletedLag(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	expireKafkaStartupGrace(name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	groupCtx, cancelGroup := context.WithCancel(ctx)
	defer cancelGroup()
	lagStarted := make(chan struct{})
	releaseLag := make(chan struct{})
	groupStarted := make(chan struct{})
	groupReturned := make(chan struct{})
	type sampleResult struct {
		valid, accepted bool
		err             error
	}
	done := make(chan sampleResult, 1)
	go func() {
		_, _, valid, accepted, err := sampleKafkaPipelineBacklogWithGroupContext(
			ctx, groupCtx, name, "events", "projection",
			func(context.Context, string, string) (int64, error) {
				close(lagStarted)
				<-releaseLag
				return 0, nil
			},
			func(queryCtx context.Context, _ string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-queryCtx.Done()
				close(groupReturned)
				return eventing.KafkaGroupDescription{}, queryCtx.Err()
			},
		)
		done <- sampleResult{valid: valid, accepted: accepted, err: err}
	}()
	waitForSignal := func(signal <-chan struct{}, name string) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not start", name)
		}
	}
	waitForSignal(lagStarted, "Lag query")
	waitForSignal(groupStarted, "Group query")
	cancelGroup()
	waitForSignal(groupReturned, "cancelled Group query")
	close(releaseLag)
	var result sampleResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Lag sample did not finish after its result was released")
	}
	if result.err != nil || !result.valid || !result.accepted {
		t.Fatalf("independent completed Lag was lost after Group cancellation: %+v", result)
	}
	workerPipelines.RLock()
	state := workerPipelines.states[name]
	membershipVerified := state.kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if !state.backlogSampleValid || state.backlog != 0 || membershipVerified {
		t.Fatalf("Group cancellation did not preserve Lag separately from membership: %+v", state)
	}
}

func TestCancelledSampleCannotTriggerIdleRecovery(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	sampleVersion, candidate := beginKafkaBacklogSample(name)
	now := time.Now().UTC()
	if !recordKafkaBacklogSample(name, sampleVersion, 0, now, true) {
		t.Fatal("could not retain independent valid zero Lag sample")
	}
	ctx, cancel := context.WithCancel(context.Background())
	description := eventing.KafkaGroupDescription{
		GroupID: "projection", State: "Stable",
		Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}},
	}
	if !recordKafkaMembershipSample(ctx, name, candidate, "projection", description, now, true) {
		t.Fatal("active Group sample was not recorded before the recovery boundary")
	}
	cancel()
	if markKafkaIdleRecovered(ctx, candidate, now) {
		t.Fatal("cancelled sample cleared historical transport failures")
	}
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 3 {
		t.Fatalf("cancelled sample changed failure count to %d", state.ConsecutiveFailures)
	}
	if reasons := evaluatePipelineHealth(now); !containsWorkerReason(reasons, "worker_pipeline_consecutive_failures") {
		t.Fatalf("cancelled sample restored readiness: %v", reasons)
	}
}

func TestCancelledOldSampleCannotOverrideNewSample(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)

	oldCtx, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	_, oldCandidate := beginKafkaBacklogSample(name)
	_, newCandidate := beginKafkaBacklogSample(name)
	newVerifiedAt := time.Now().UTC()
	description := eventing.KafkaGroupDescription{
		GroupID: "projection", State: "Stable",
		Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}},
	}
	if !recordKafkaMembershipSample(context.Background(), name, newCandidate, "projection", description, newVerifiedAt, true) {
		t.Fatal("new Group sample was not recorded")
	}
	cancelOld()
	if recordKafkaMembershipSample(oldCtx, name, oldCandidate, "projection", description, newVerifiedAt.Add(time.Second), true) {
		t.Fatal("cancelled old Group sample overwrote the newer proof")
	}
	workerPipelines.RLock()
	membership := workerPipelines.states[name].kafkaSlots[0].membership
	workerPipelines.RUnlock()
	if !membership.Verified || !membership.VerifiedAt.Equal(newVerifiedAt) {
		t.Fatalf("newer membership proof was changed: %+v", membership)
	}
}

func TestActiveSampleStillAllowsMembershipAndIdleRecovery(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	expireKafkaStartupGrace(name)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
		},
	)
	if err != nil || !valid || !accepted {
		t.Fatalf("active sample valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("active Stable/zero-Lag sample did not recover transport failure: %+v", state)
	}
	workerPipelines.RLock()
	verified := workerPipelines.states[name].kafkaSlots[0].membership.Verified
	workerPipelines.RUnlock()
	if !verified {
		t.Fatal("active sample did not retain Stable Group membership")
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("active sample left pipeline unhealthy: %v", reasons)
	}
}

func TestCommitDoesNotBypassMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	registerPipelineAt(name, 1, time.Now().UTC().Add(-workerActiveGrace-time.Second))
	KafkaConsumerSlotStarted(name, 0)
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	t.Cleanup(closeSession)
	KafkaConsumerSlotCommitted(name, 0, time.Now().UTC())
	pipelineKafkaBacklogSample(name, 0, time.Now().UTC(), true)
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("successful commit bypassed membership proof: %v", reasons)
	}
}

func TestMultiConsumerMembershipAndFailureIsolation(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	expireKafkaStartupGrace(name)
	KafkaConsumerSlotCommitted(name, 1, time.Now().UTC())
	workerPipelines.Lock()
	slotA := workerPipelines.states[name].kafkaSlots[0]
	slotA.membership = kafkaConsumerMembershipState{}
	workerPipelines.states[name].kafkaSlots[0] = slotA
	workerPipelines.Unlock()
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("verified and committed Slot B represented unverified Slot A: %v", reasons)
	}
}

func TestShutdownImmediatelyInvalidatesMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	workerPipelines.RLock()
	versionBefore := workerPipelines.states[name].kafkaSlots[0].sessionVersion
	workerPipelines.RUnlock()
	MarkWorkerShuttingDown()
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.membership.Verified || slot.sessionVersion <= versionBefore {
		t.Fatalf("shutdown retained or failed to fence membership: %+v", slot)
	}
}

func TestTerminalFetchFailureInvalidatesMembership(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "reader-a", &scriptedKafkaReader{
		steps: []kafkaFetchStep{{err: errors.New("terminal fetch error")}},
	})
	result := fetchKafkaBatch(context.Background(), reader, 1, 0)
	if result.TerminalFetchErr == nil {
		t.Fatal("expected terminal fetch error")
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.sessionRecoveryEligible || slot.membership.Verified {
		t.Fatalf("terminal fetch left session or membership eligible: %+v", slot)
	}
	_ = reader.Close()
}

func TestEvaluatePipelineHealthConcurrentKafkaSlotMutation(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-initial"})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < 500; i++ {
			evaluatePipelineHealth(time.Now().UTC())
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 500; i++ {
			closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", fmt.Sprintf("reader-%d", i))
			closeSession()
		}
	}()
	workers.Wait()
}

func TestKafkaBacklogRequiresFreshGroupSamplesAndIgnoresReaderUpdates(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	name := PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	KafkaConsumerSlotCommitted(name, 0, base)
	KafkaConsumerSlotCommitted(name, 1, base)
	if reasons := evaluatePipelineHealth(base); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatalf("unmeasured backlog considered healthy: %v", reasons)
	}
	pipelineKafkaBacklogSample(name, 500, base, true)
	KafkaConsumerSlotFailed(name, 0, "temporary", pipelineFailureTransport)
	PipelineIdle(name, 0)
	state := pipelineSnapshots()[name]
	if state.Backlog != 500 || !state.LastProgressAt.Equal(base) || state.ConsecutiveFailures != 1 {
		t.Fatalf("consumer updates changed sampled backlog, progress, or failure: %+v", state)
	}
	pipelineKafkaBacklogSample(name, 0, base.Add(time.Second), false)
	state = pipelineSnapshots()[name]
	if state.Backlog != 500 || state.BacklogSampleValid || !state.BacklogSampleAt.Equal(base) {
		t.Fatalf("failed sample replaced the last known lag: %+v", state)
	}
	if reasons := evaluatePipelineHealth(base.Add(time.Second)); !containsWorkerReason(reasons, "worker_pipeline_backlog_unknown") {
		t.Fatal(reasons)
	}
	pipelineKafkaBacklogSample(name, 0, base.Add(2*time.Second), true)
	if reasons := evaluatePipelineHealth(base.Add(2 * time.Second)); len(reasons) != 0 {
		t.Fatalf("successful empty sample not healthy: %v", reasons)
	}
	if reasons := evaluatePipelineHealth(base.Add(2*time.Second + kafkaBacklogMaxAge + time.Nanosecond)); !containsWorkerReason(reasons, "worker_kafka_consumer_group_unverified") {
		t.Fatalf("expired membership proof did not fail closed: %v", reasons)
	}
}

func TestKafkaPipelineIdleCannotClearFailureWithoutDedicatedRecovery(t *testing.T) {
	resetPipelineStates(t)
	base := time.Now().UTC()
	name := PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
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
		clients       []string
		lag           int64
		lagErr        error
		groupErr      error
		groupState    string
		memberMode    string
		shuttingDown  bool
		wantRecovered bool
		wantGroupCall bool
		queryMutation string
	}{
		{name: "stable local members", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local", wantRecovered: true, wantGroupCall: true},
		{name: "no local readers", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, groupState: "Stable", memberMode: "other"},
		{name: "empty group", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Empty", memberMode: "local", wantGroupCall: true},
		{name: "preparing rebalance", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "PreparingRebalance", memberMode: "local", wantGroupCall: true},
		{name: "completing rebalance", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "CompletingRebalance", memberMode: "local", wantGroupCall: true},
		{name: "client id mismatch", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "other", wantGroupCall: true},
		{name: "member does not subscribe to target topic", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "wrong topic", wantGroupCall: true},
		{name: "partial local recovery", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a"}, groupState: "Stable", memberMode: "local"},
		{name: "other workers only", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "other", wantGroupCall: true},
		{name: "positive lag", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, lag: 2, groupState: "Stable", memberMode: "local"},
		{name: "lag sample error", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, lagErr: errors.New("broker unavailable"), groupState: "Stable", memberMode: "local"},
		{name: "group query error preserves lag", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupErr: errors.New("group unavailable"), groupState: "Stable", memberMode: "local", wantGroupCall: true},
		{name: "processing failure", failureKinds: []pipelineFailureKinds{pipelineFailureProcessing, pipelineFailureProcessing, pipelineFailureProcessing}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local"},
		{name: "commit failure", failureKinds: []pipelineFailureKinds{pipelineFailureCommit, pipelineFailureCommit, pipelineFailureCommit}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local"},
		{name: "configuration failure", failureKinds: []pipelineFailureKinds{pipelineFailureConfiguration, pipelineFailureConfiguration, pipelineFailureConfiguration}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local"},
		{name: "mixed failure history", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureProcessing, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local"},
		{name: "shutdown", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local", shuttingDown: true},
		{name: "new failure during group query", failureKinds: []pipelineFailureKinds{pipelineFailureTransport, pipelineFailureTransport, pipelineFailureTransport}, clients: []string{"reader-a", "reader-b"}, groupState: "Stable", memberMode: "local", wantGroupCall: true, queryMutation: "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetPipelineStates(t)
			base := time.Now().UTC()
			const name = PipelineNotificationProjection
			setupKafkaPipeline(t, name, 2, test.clients)
			KafkaConsumerSlotCommitted(name, 0, base)
			for _, kind := range test.failureKinds {
				KafkaConsumerSlotFailed(name, 0, "test_failure", kind)
			}
			if test.shuttingDown {
				workerPipelines.Lock()
				workerShuttingDown.Store(true)
				workerPipelines.Unlock()
			}
			members := []eventing.KafkaGroupMember{}
			switch test.memberMode {
			case "local":
				for _, clientID := range test.clients {
					members = append(members, eventing.KafkaGroupMember{ClientID: clientID, Topics: []string{"events"}})
				}
			case "wrong topic":
				for _, clientID := range test.clients {
					members = append(members, eventing.KafkaGroupMember{ClientID: clientID, Topics: []string{"other"}})
				}
			case "other":
				members = []eventing.KafkaGroupMember{{ClientID: "remote-a", Topics: []string{"events"}}, {ClientID: "remote-b", Topics: []string{"events"}}}
			}
			groupCalls := 0
			_, _, valid, accepted, err := sampleKafkaPipelineBacklog(
				context.Background(), name, "events", "projection",
				func(context.Context, string, string) (int64, error) { return test.lag, test.lagErr },
				func(context.Context, string) (eventing.KafkaGroupDescription, error) {
					groupCalls++
					if test.queryMutation == "failure" {
						KafkaConsumerSlotFailed(name, 0, "newer_failure", pipelineFailureTransport)
					}
					return eventing.KafkaGroupDescription{GroupID: "projection", State: test.groupState, Members: members}, test.groupErr
				},
			)
			wantAccepted := !test.shuttingDown
			if valid != (test.lagErr == nil) || accepted != wantAccepted || (err != nil) != (test.lagErr != nil) {
				t.Fatalf("valid=%v accepted=%v err=%v, lagErr=%v", valid, accepted, err, test.lagErr)
			}
			if groupCalls != 1 {
				t.Fatalf("group query calls=%d, want one membership check on every sample", groupCalls)
			}
			state := pipelineSnapshots()[name]
			if test.wantRecovered {
				if state.ConsecutiveFailures != 0 || state.State != "idle" || state.ReasonCode != "" {
					t.Fatalf("valid idle recovery did not clear the failure: %+v", state)
				}
				if !state.LastCommittedOffsetAt.Equal(base) {
					t.Fatalf("idle recovery changed real commit time: %v", state.LastCommittedOffsetAt)
				}
				if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
					t.Fatalf("recovered Kafka pipeline remains unhealthy: %v", reasons)
				}
			} else if len(test.failureKinds) > 0 && state.ConsecutiveFailures == 0 && test.queryMutation != "failure" {
				t.Fatalf("invalid recovery cleared failure state: %+v", state)
			}
			if test.groupErr != nil && (!state.BacklogSampleValid || state.Backlog != 0) {
				t.Fatalf("group query error invalidated successful lag sample: %+v", state)
			}
		})
	}
}

func TestKafkaIdleRecoveryRejectsSessionExitDuringGroupQuery(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, nil)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	closeSession := registerKafkaConsumerSession(name, 0, "events", "projection", "reader-a")
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	go func() {
		defer calls.Done()
		_, _, _, _, _ = sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("group query did not start")
	}
	closeSession()
	close(releaseGroup)
	calls.Wait()
	if state := pipelineSnapshots()[name]; state.ConsecutiveFailures != 3 || state.State != "failed" {
		t.Fatalf("closed reader was accepted as recovery evidence: %+v", state)
	}
}

func TestKafkaIdleRecoveryRejectsNewFailureDuringGroupQuery(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	groupStarted := make(chan struct{})
	releaseGroup := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	go func() {
		defer calls.Done()
		_, _, _, _, _ = sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				close(groupStarted)
				<-releaseGroup
				return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
			})
	}()
	select {
	case <-groupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("group query did not start")
	}
	KafkaConsumerSlotFailed(name, 0, "newer_transport_failure", pipelineFailureTransport)
	close(releaseGroup)
	calls.Wait()
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
		{name: "commit", mutate: func(name string) { KafkaConsumerSlotCommitted(name, 0, time.Now().UTC()) }, wantState: "running"},
		{name: "shutdown", mutate: func(string) {
			workerPipelines.Lock()
			workerShuttingDown.Store(true)
			workerPipelines.Unlock()
		}, wantState: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetPipelineStates(t)
			const name = PipelineNotificationProjection
			setupKafkaPipeline(t, name, 1, []string{"reader-a"})
			for i := 0; i < 3; i++ {
				KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
			}
			groupStarted := make(chan struct{})
			releaseGroup := make(chan struct{})
			var calls sync.WaitGroup
			calls.Add(1)
			go func() {
				defer calls.Done()
				_, _, _, _, _ = sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
					func(context.Context, string, string) (int64, error) { return 0, nil },
					func(context.Context, string) (eventing.KafkaGroupDescription, error) {
						close(groupStarted)
						<-releaseGroup
						return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{{ClientID: "reader-a", Topics: []string{"events"}}}}, nil
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
			if state := pipelineSnapshots()[name]; state.State != test.wantState {
				t.Fatalf("state=%q want=%q details=%+v", state.State, test.wantState, state)
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

func TestMultiConsumerCommitDoesNotClearOtherConsumerFailure(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	pipelineKafkaBacklogSample(name, 0, time.Now().UTC(), true)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	KafkaConsumerSlotCommitted(name, 1, time.Now().UTC())
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 3 {
		t.Fatalf("slot B commit cleared slot A failures: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_pipeline_consecutive_failures") {
		t.Fatalf("pipeline became ready while slot A remains failed: %v", reasons)
	}
}

func TestMultiConsumerPartialRecoveryRemainsUnhealthy(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	pipelineKafkaBacklogSample(name, 0, time.Now().UTC(), true)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	KafkaConsumerSlotCommitted(name, 1, time.Now().UTC())
	if reasons := evaluatePipelineHealth(time.Now().UTC()); !containsWorkerReason(reasons, "worker_pipeline_consecutive_failures") {
		t.Fatalf("partial slot recovery became ready: %v", reasons)
	}
}

func TestMultiConsumerAllSlotsRecovered(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a", "reader-b"})
	KafkaConsumerSlotCommitted(name, 1, time.Now().UTC())
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	_, _, _, _, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{GroupID: "projection", State: "Stable", Members: []eventing.KafkaGroupMember{
				{ClientID: "reader-a", Topics: []string{"events"}},
				{ClientID: "reader-b", Topics: []string{"events"}},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 0 || state.State != "idle" {
		t.Fatalf("all-slot recovery did not clear aggregate state: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("pipeline remained unhealthy after all slots recovered: %v", reasons)
	}
}

func TestKafkaConsumerMissingSessionAfterStartupGrace(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 2, []string{"reader-a"})

	workerPipelines.RLock()
	state, ok := workerPipelines.states[name]
	registeredAt := state.registeredAt
	workerPipelines.RUnlock()
	if !ok {
		t.Fatal("Kafka pipeline was not registered")
	}
	if registeredAt.IsZero() {
		t.Fatal("Kafka pipeline registration time was not recorded")
	}

	pipelineKafkaBacklogSample(name, 0, registeredAt, true)
	reasons := evaluatePipelineHealth(registeredAt.Add(workerActiveGrace))
	if !containsWorkerReason(reasons, "worker_pipeline_starting") {
		t.Fatalf("missing slot session should remain in startup grace at the boundary: %v", reasons)
	}

	reasons = evaluatePipelineHealth(registeredAt.Add(workerActiveGrace + time.Nanosecond))
	if !containsWorkerReason(reasons, "worker_kafka_consumer_session_unavailable") {
		t.Fatalf("missing slot session was not fail-closed after startup grace: %v", reasons)
	}
}

func TestSingleConsumerSlotCommitRecoversFailure(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineLikeSnapshotProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-only"})
	pipelineKafkaBacklogSample(name, 0, time.Now().UTC(), true)
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	KafkaConsumerSlotCommitted(name, 0, time.Now().UTC())
	if state := pipelineSnapshots()[name]; state.ConsecutiveFailures != 0 {
		t.Fatalf("single consumer commit did not recover its slot: %+v", state)
	}
	if reasons := evaluatePipelineHealth(time.Now().UTC()); len(reasons) != 0 {
		t.Fatalf("single consumer pipeline remained unhealthy: %v", reasons)
	}
}

type kafkaFetchStep struct {
	message kafka.Message
	err     error
	wait    bool
}

type scriptedKafkaReader struct {
	mu         sync.Mutex
	steps      []kafkaFetchStep
	commitErr  error
	fetches    int
	commits    int
	closeCalls int
	committed  []kafka.Message
}

func (r *scriptedKafkaReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	r.fetches++
	if len(r.steps) == 0 {
		r.mu.Unlock()
		return kafka.Message{}, errors.New("unexpected fetch")
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	r.mu.Unlock()
	if step.wait {
		<-ctx.Done()
		return kafka.Message{}, ctx.Err()
	}
	return step.message, step.err
}

func (r *scriptedKafkaReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if r.commitErr == nil {
		r.committed = append(r.committed, messages...)
	}
	return r.commitErr
}

func (r *scriptedKafkaReader) Close() error {
	r.mu.Lock()
	r.closeCalls++
	r.mu.Unlock()
	return nil
}

func TestBatchFetchErrorClosesReaderAfterSafeCommit(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineUserBehaviorProjection
	setupKafkaPipeline(t, name, 1, nil)
	terminalErr := errors.New("broker connection lost")
	inner := &scriptedKafkaReader{steps: []kafkaFetchStep{{message: kafka.Message{Offset: 7}}, {err: terminalErr}}}
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "reader-old", inner)
	result := fetchKafkaBatch(context.Background(), reader, 10, time.Second)
	if len(result.Messages) != 1 || !errors.Is(result.TerminalFetchErr, terminalErr) || result.ContextErr != nil {
		t.Fatalf("batch result did not retain fetched message and terminal error: %+v", result)
	}
	committed, err := finishKafkaBatch(context.Background(), name, reader, result, nil, reader.CommitMessages, nil)
	if !committed || !errors.Is(err, terminalErr) {
		t.Fatalf("committed=%v err=%v, want committed batch followed by terminal fetch error", committed, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	inner.mu.Lock()
	fetches, commits, closeCalls, committedMessages := inner.fetches, inner.commits, inner.closeCalls, len(inner.committed)
	inner.mu.Unlock()
	if fetches != 2 || commits != 1 || closeCalls != 1 || committedMessages != 1 {
		t.Fatalf("reader reused or close/commit count wrong: fetches=%d commits=%d closes=%d committed=%d", fetches, commits, closeCalls, committedMessages)
	}
	state := pipelineSnapshots()[name]
	if state.ConsecutiveFailures != 1 || state.LastCommittedOffsetAt.IsZero() {
		t.Fatalf("safe commit was not followed by preserved fetch failure: %+v", state)
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.sessionActive || slot.sessionRecoveryEligible || slot.failureKinds != pipelineFailureTransport {
		t.Fatalf("closed reader session/failure state inconsistent: %+v", slot)
	}
}

func TestBatchCollectionDeadlineDoesNotUnregisterReader(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineUserBehaviorProjection
	setupKafkaPipeline(t, name, 1, nil)
	inner := &scriptedKafkaReader{steps: []kafkaFetchStep{{message: kafka.Message{Offset: 1}}, {wait: true}}}
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "reader-window", inner)
	result := fetchKafkaBatch(context.Background(), reader, 10, 15*time.Millisecond)
	if result.ContextErr != nil || result.TerminalFetchErr != nil || len(result.Messages) != 1 {
		t.Fatalf("batch deadline was not treated as a normal window end: %+v", result)
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.sessionActive || !slot.sessionRecoveryEligible || slot.consecutiveFailures != 0 {
		t.Fatalf("normal batch deadline changed Reader recovery state: %+v", slot)
	}
	_ = reader.Close()
}

func TestKafkaReaderRestartRegistersNewSession(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineUserBehaviorProjection
	setupKafkaPipeline(t, name, 1, nil)
	oldReader := trackKafkaConsumerReader(name, 0, "events", "projection", "client-old", &scriptedKafkaReader{})
	oldReader.Close()
	newReader := trackKafkaConsumerReader(name, 0, "events", "projection", "client-new", &scriptedKafkaReader{})
	oldReader.Close()
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if !slot.sessionActive || !slot.sessionRecoveryEligible || slot.session.clientID != "client-new" || slot.sessionVersion != 3 {
		t.Fatalf("Reader restart did not register a fresh session version: %+v", slot)
	}
	_ = newReader.Close()
}

func TestKafkaReaderCloseIsIdempotent(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineLikeSnapshotProjection
	setupKafkaPipeline(t, name, 1, nil)
	inner := &scriptedKafkaReader{}
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "client", inner)
	_ = reader.Close()
	_ = reader.Close()
	inner.mu.Lock()
	closeCalls := inner.closeCalls
	inner.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("reader Close called %d times", closeCalls)
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.sessionActive || slot.sessionVersion != 2 {
		t.Fatalf("session was not removed exactly once: %+v", slot)
	}
}

func TestKafkaReaderCancellationDoesNotCreateFailure(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelinePostEmbedding
	setupKafkaPipeline(t, name, 1, nil)
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "client", &scriptedKafkaReader{steps: []kafkaFetchStep{{wait: true}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := fetchKafkaBatch(ctx, reader, 1, 0)
	if !errors.Is(result.ContextErr, context.Canceled) || result.TerminalFetchErr != nil {
		t.Fatalf("cancellation reported as a fetch failure: %+v", result)
	}
	_, err := finishKafkaBatch(ctx, name, reader, result, nil, reader.CommitMessages, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("finish error=%v, want context canceled", err)
	}
	if state := pipelineSnapshots()[name]; state.ConsecutiveFailures != 0 {
		t.Fatalf("shutdown cancellation created a pipeline failure: %+v", state)
	}
	_ = reader.Close()
}

func TestBatchCommitFailurePreservesUncommittedMessages(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineRecommendationMetrics
	setupKafkaPipeline(t, name, 1, nil)
	commitErr := errors.New("commit failed")
	inner := &scriptedKafkaReader{steps: []kafkaFetchStep{{message: kafka.Message{Offset: 9}}}, commitErr: commitErr}
	reader := trackKafkaConsumerReader(name, 0, "events", "projection", "reader", inner)
	batch := fetchKafkaBatch(context.Background(), reader, 1, 0)
	committed, err := finishKafkaBatch(context.Background(), name, reader, batch, nil, reader.CommitMessages, nil)
	if committed || !errors.Is(err, commitErr) || len(batch.Messages) != 1 {
		t.Fatalf("commit result committed=%v err=%v messages=%d", committed, err, len(batch.Messages))
	}
	inner.mu.Lock()
	committedCount := len(inner.committed)
	inner.mu.Unlock()
	if committedCount != 0 {
		t.Fatalf("failed commit was recorded as committed: %d", committedCount)
	}
	workerPipelines.RLock()
	slot := workerPipelines.states[name].kafkaSlots[0]
	workerPipelines.RUnlock()
	if slot.consecutiveFailures != 1 || slot.failureKinds != pipelineFailureCommit || !slot.lastCommittedOffsetAt.IsZero() {
		t.Fatalf("commit failure state incorrect: %+v", slot)
	}
	_ = reader.Close()
}

func TestKafkaGroupTimeoutDoesNotBlockOtherGroups(t *testing.T) {
	resetPipelineStates(t)
	const (
		blockedPipeline = PipelineNotificationProjection
		healthyPipeline = PipelineLikeSnapshotProjection
	)
	setupKafkaPipeline(t, blockedPipeline, 1, nil)
	setupKafkaPipeline(t, healthyPipeline, 1, nil)
	closeBlockedSession := registerKafkaConsumerSession(blockedPipeline, 0, "topic-a", "group-a", "reader-a")
	defer closeBlockedSession()
	closeHealthySession := registerKafkaConsumerSession(healthyPipeline, 0, "topic-b", "group-b", "reader-b")
	defer closeHealthySession()
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(blockedPipeline, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	blockedGroupContext := make(chan context.Context, 1)
	blockedGroupFinished := make(chan time.Time, 1)
	healthySampledWhileBlocked := make(chan struct{}, 1)
	sampleKafkaBacklogGroups(context.Background(), []kafkaBacklogTarget{
		{pipeline: blockedPipeline, topic: "topic-a", group: "group-a"},
		{pipeline: healthyPipeline, topic: "topic-b", group: "group-b"},
	}, 2*time.Second, time.Second, 2,
		func(_ context.Context, topic, _ string) (int64, error) {
			if topic == "topic-b" {
				blockedCtx := <-blockedGroupContext
				if blockedCtx.Err() != nil {
					return 0, errors.New("group-a timed out before group-b Lag sampling")
				}
				healthySampledWhileBlocked <- struct{}{}
			}
			return 0, nil
		},
		func(ctx context.Context, group string) (eventing.KafkaGroupDescription, error) {
			if group != "group-a" {
				return eventing.KafkaGroupDescription{}, errors.New("unexpected group query")
			}
			blockedGroupContext <- ctx
			<-ctx.Done()
			blockedGroupFinished <- time.Now().UTC()
			return eventing.KafkaGroupDescription{}, ctx.Err()
		},
	)
	select {
	case <-healthySampledWhileBlocked:
	default:
		t.Fatal("group-b Lag was not sampled while group-a metadata query was still blocked")
	}
	blockedAt := <-blockedGroupFinished
	workerPipelines.RLock()
	blockedState := workerPipelines.states[blockedPipeline]
	healthyState := workerPipelines.states[healthyPipeline]
	workerPipelines.RUnlock()
	if !blockedState.backlogSampleValid || blockedState.consecutiveFailures != 3 {
		t.Fatalf("group timeout discarded valid lag or recovered the failed slot: %+v", blockedState)
	}
	if !healthyState.backlogSampleValid || healthyState.backlogSampleAt.IsZero() || !healthyState.backlogSampleAt.Before(blockedAt) {
		t.Fatalf("unrelated group did not receive a fresh lag sample: %+v", healthyState)
	}
	if blocked, reason := pipelineBacklogBlocked(healthyPipeline, blockedAt, healthyState); blocked {
		t.Fatalf("group-a timeout blocked healthy pipeline with %s", reason)
	}
}

func TestKafkaSamplingOverallDeadline(t *testing.T) {
	resetPipelineStates(t)
	pipelines := []string{PipelineNotificationProjection, PipelineLikeSnapshotProjection, PipelineRecommendationMetrics}
	suffixes := []string{"a", "b", "c"}
	groups := make([]kafkaBacklogTarget, 0, len(pipelines))
	for index, pipeline := range pipelines {
		setupKafkaPipeline(t, pipeline, 1, nil)
		topic := "topic-" + suffixes[index]
		group := "group-" + suffixes[index]
		client := "reader-" + suffixes[index]
		unregister := registerKafkaConsumerSession(pipeline, 0, topic, group, client)
		t.Cleanup(unregister)
		for i := 0; i < 3; i++ {
			KafkaConsumerSlotFailed(pipeline, 0, "kafka_fetch_failed", pipelineFailureTransport)
		}
		groups = append(groups, kafkaBacklogTarget{pipeline: pipeline, topic: topic, group: group})
	}
	started := make(chan struct{}, len(groups))
	returned := make(chan struct{}, len(groups))
	done := make(chan struct{})
	startedAt := time.Now()
	go func() {
		sampleKafkaBacklogGroups(context.Background(), groups, time.Second, 2*time.Second, len(groups),
			func(context.Context, string, string) (int64, error) { return 0, nil },
			func(ctx context.Context, _ string) (eventing.KafkaGroupDescription, error) {
				started <- struct{}{}
				<-ctx.Done()
				returned <- struct{}{}
				return eventing.KafkaGroupDescription{}, ctx.Err()
			},
		)
		close(done)
	}()
	for range groups {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("not all group queries started")
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sampling did not stop at its overall deadline")
	}
	if elapsed := time.Since(startedAt); elapsed > 1500*time.Millisecond {
		t.Fatalf("sampling exceeded its one-second overall budget: %s", elapsed)
	}
	for range groups {
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("context-aware group query did not return after cancellation")
		}
	}
}

func TestKafkaGroupQueryFailurePreservesValidLagSample(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	setupKafkaPipeline(t, name, 1, []string{"reader-a"})
	for i := 0; i < 3; i++ {
		KafkaConsumerSlotFailed(name, 0, "kafka_fetch_failed", pipelineFailureTransport)
	}
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 0, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{}, errors.New("group metadata unavailable")
		},
	)
	if err != nil || !valid || !accepted {
		t.Fatalf("successful lag sample was rejected after group query failure: valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	state := pipelineSnapshots()[name]
	if !state.BacklogSampleValid || state.Backlog != 0 || state.ConsecutiveFailures != 3 {
		t.Fatalf("group query failure changed valid Lag or cleared failure state: %+v", state)
	}
}

func TestKafkaLagTimeoutMarksOnlyThatGroupSampleInvalid(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	registerPipelineAt(name, 1, time.Now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _, valid, accepted, err := sampleKafkaPipelineBacklog(ctx, name, "events", "projection",
		func(ctx context.Context, _, _ string) (int64, error) {
			<-ctx.Done()
			return 0, nil
		},
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{}, nil
		},
	)
	if err == nil || valid || !accepted {
		t.Fatalf("timed-out lag query was published as valid: valid=%v accepted=%v err=%v", valid, accepted, err)
	}
	if state := pipelineSnapshots()[name]; state.BacklogSampleValid {
		t.Fatalf("lag timeout left the group sample valid: %+v", state)
	}
}

func TestOlderConcurrentSampleCannotOverwriteNewerSample(t *testing.T) {
	resetPipelineStates(t)
	const name = PipelineNotificationProjection
	registerPipelineAt(name, 1, time.Now().UTC())
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	type sampleResult struct {
		lag      int64
		valid    bool
		accepted bool
		err      error
	}
	oldResult := make(chan sampleResult, 1)
	go func() {
		lag, _, valid, accepted, err := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
			func(context.Context, string, string) (int64, error) {
				close(oldStarted)
				<-releaseOld
				return 99, nil
			},
			func(context.Context, string) (eventing.KafkaGroupDescription, error) {
				return eventing.KafkaGroupDescription{}, nil
			},
		)
		oldResult <- sampleResult{lag: lag, valid: valid, accepted: accepted, err: err}
	}()
	select {
	case <-oldStarted:
	case <-time.After(time.Second):
		t.Fatal("older lag query did not start")
	}
	newLag, _, newValid, newAccepted, newErr := sampleKafkaPipelineBacklog(context.Background(), name, "events", "projection",
		func(context.Context, string, string) (int64, error) { return 7, nil },
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{}, nil
		},
	)
	if newErr != nil || !newValid || !newAccepted || newLag != 7 {
		t.Fatalf("newer sample failed: lag=%d valid=%v accepted=%v err=%v", newLag, newValid, newAccepted, newErr)
	}
	close(releaseOld)
	old := <-oldResult
	if old.err != nil || !old.valid || old.accepted || old.lag != 99 {
		t.Fatalf("older sample result mismatch: %+v", old)
	}
	state := pipelineSnapshots()[name]
	if state.Backlog != 7 {
		t.Fatalf("late old sample overwrote newer lag: %+v", state)
	}
}

func TestOneGroupFailureDoesNotInvalidateOtherPipelineReadiness(t *testing.T) {
	resetPipelineStates(t)
	const (
		failedPipeline  = PipelineNotificationProjection
		healthyPipeline = PipelineLikeSnapshotProjection
	)
	setupKafkaPipeline(t, failedPipeline, 1, nil)
	setupKafkaPipeline(t, healthyPipeline, 1, nil)
	sampleKafkaBacklogGroups(context.Background(), []kafkaBacklogTarget{
		{pipeline: failedPipeline, topic: "topic-a", group: "group-a"},
		{pipeline: healthyPipeline, topic: "topic-b", group: "group-b"},
	}, time.Second, 100*time.Millisecond, 2,
		func(_ context.Context, topic, _ string) (int64, error) {
			if topic == "topic-a" {
				return 0, errors.New("group-a broker unavailable")
			}
			return 0, nil
		},
		func(context.Context, string) (eventing.KafkaGroupDescription, error) {
			return eventing.KafkaGroupDescription{}, nil
		},
	)
	workerPipelines.RLock()
	failedState := workerPipelines.states[failedPipeline]
	healthyState := workerPipelines.states[healthyPipeline]
	workerPipelines.RUnlock()
	if failedState.backlogSampleValid {
		t.Fatalf("failed group's lag sample was marked valid: %+v", failedState)
	}
	if blocked, reason := pipelineBacklogBlocked(healthyPipeline, time.Now().UTC(), healthyState); blocked {
		t.Fatalf("failed group invalidated healthy pipeline readiness: %s", reason)
	}
}
