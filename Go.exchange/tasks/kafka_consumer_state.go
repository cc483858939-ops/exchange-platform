package tasks

import "time"

// kafkaConsumerSlotState describes a stable logical consumer position. Reader
// ClientIDs change on every restart; the slot ID does not.
type kafkaConsumerSlotState struct {
	workerStarted           bool
	session                 kafkaConsumerSession
	sessionActive           bool
	sessionRecoveryEligible bool
	sessionVersion          uint64
	membership              kafkaConsumerMembershipState
	failureVersion          uint64
	consecutiveFailures     int
	failureKinds            pipelineFailureKinds
	startedAt               time.Time
	lastSuccessAt           time.Time
	lastFailureAt           time.Time
	lastCommittedOffsetAt   time.Time
	postEmbeddingPartitions map[int]postEmbeddingPartitionHealth
	state                   string
	reasonCode              string
}

type kafkaConsumerMembershipState struct {
	Verified       bool
	VerifiedAt     time.Time
	SessionVersion uint64
}

type postEmbeddingPartitionHealth struct {
	Pending             bool
	PendingSince        time.Time
	PendingNextOffset   int64
	LastConfirmedOffset int64
}

type postEmbeddingGenerationHealthIdentity struct {
	pipeline       string
	slotID         int
	generationID   int32
	sessionVersion uint64
}

func (identity postEmbeddingGenerationHealthIdentity) valid() bool {
	return identity.pipeline == PipelinePostEmbedding && identity.sessionVersion != 0
}

func recordPostEmbeddingPartitionBatchPending(
	identity postEmbeddingGenerationHealthIdentity,
	partition int,
	nextOffset int64,
	now time.Time,
) bool {
	if !identity.valid() || partition < 0 || nextOffset < 0 || workerShuttingDown.Load() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	if workerShuttingDown.Load() {
		return false
	}
	state, found := workerPipelines.states[identity.pipeline]
	if !found {
		return false
	}
	slot, found := state.kafkaSlots[identity.slotID]
	if !postEmbeddingGenerationSessionMatches(slot, found, identity) {
		return false
	}
	partitionHealth, assigned := slot.postEmbeddingPartitions[partition]
	if !assigned || nextOffset <= partitionHealth.LastConfirmedOffset {
		return false
	}
	if !partitionHealth.Pending {
		partitionHealth.Pending = true
		partitionHealth.PendingSince = now.UTC()
		partitionHealth.PendingNextOffset = nextOffset
	} else if nextOffset > partitionHealth.PendingNextOffset {
		partitionHealth.PendingNextOffset = nextOffset
		if partitionHealth.PendingSince.IsZero() {
			partitionHealth.PendingSince = now.UTC()
		}
	}
	slot.postEmbeddingPartitions[partition] = partitionHealth
	state.kafkaSlots[identity.slotID] = slot
	workerPipelines.states[identity.pipeline] = state
	return true
}

func confirmPostEmbeddingPartitionCommit(
	identity postEmbeddingGenerationHealthIdentity,
	partition int,
	nextOffset int64,
) bool {
	if !identity.valid() || partition < 0 || nextOffset < 0 || workerShuttingDown.Load() {
		return false
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	if workerShuttingDown.Load() {
		return false
	}
	state, found := workerPipelines.states[identity.pipeline]
	if !found {
		return false
	}
	slot, found := state.kafkaSlots[identity.slotID]
	if !postEmbeddingGenerationSessionMatches(slot, found, identity) {
		return false
	}
	partitionHealth, assigned := slot.postEmbeddingPartitions[partition]
	if !assigned || nextOffset < partitionHealth.LastConfirmedOffset {
		return false
	}
	partitionHealth.LastConfirmedOffset = nextOffset
	if partitionHealth.Pending && nextOffset >= partitionHealth.PendingNextOffset {
		partitionHealth.Pending = false
		partitionHealth.PendingSince = time.Time{}
		partitionHealth.PendingNextOffset = 0
	}
	slot.postEmbeddingPartitions[partition] = partitionHealth
	state.kafkaSlots[identity.slotID] = slot
	workerPipelines.states[identity.pipeline] = state
	return true
}

func postEmbeddingGenerationSessionMatches(
	slot kafkaConsumerSlotState,
	found bool,
	identity postEmbeddingGenerationHealthIdentity,
) bool {
	return found && slot.workerStarted && slot.sessionActive && slot.sessionRecoveryEligible &&
		slot.session.generationScoped && slot.session.generationID == identity.generationID &&
		slot.sessionVersion == identity.sessionVersion
}

const kafkaMembershipMaxAge = 30 * time.Second

// KafkaConsumerSlotStarted marks the long-lived worker loop for a Kafka
// consumer. Reader retries keep the same slot ID.
func KafkaConsumerSlotStarted(pipeline string, slotID int) {
	if workerShuttingDown.Load() {
		return
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	if workerShuttingDown.Load() {
		return
	}
	state, found := workerPipelines.states[pipeline]
	if !found || !isKafkaPipeline(pipeline) {
		return
	}
	slot, found := state.kafkaSlots[slotID]
	if !found {
		return
	}
	if !slot.workerStarted {
		slot.workerStarted = true
		slot.startedAt = time.Now().UTC()
		slot.membership = kafkaConsumerMembershipState{}
		slot.postEmbeddingPartitions = nil
		state.membershipSampleVersion++
		if state.startedAt.IsZero() {
			state.startedAt = slot.startedAt
		}
	}
	state.kafkaSlots[slotID] = slot
	if state.consecutiveFailures == 0 && state.unattributedFailures == 0 && state.state != "idle" {
		state.state = "running"
		state.reasonCode = ""
	}
	recomputeKafkaPipelineAggregate(&state, "")
	workerPipelines.states[pipeline] = state
}

// KafkaConsumerSlotStopped marks the logical worker loop stopped and fences
// any Reader session it still owned.
func KafkaConsumerSlotStopped(pipeline string, slotID int) {
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[pipeline]
	if !found || !isKafkaPipeline(pipeline) {
		return
	}
	slot, found := state.kafkaSlots[slotID]
	if !found {
		return
	}
	slot.sessionActive = false
	slot.sessionRecoveryEligible = false
	slot.session = kafkaConsumerSession{}
	slot.sessionVersion++
	slot.membership = kafkaConsumerMembershipState{}
	slot.postEmbeddingPartitions = nil
	state.membershipSampleVersion++
	slot.workerStarted = false
	state.kafkaSlots[slotID] = slot
	recomputeKafkaPipelineAggregate(&state, "")
	workerPipelines.states[pipeline] = state
}

func KafkaConsumerSlotFailed(pipeline string, slotID int, reason string, kind pipelineFailureKinds) {
	if kind == 0 {
		kind = pipelineFailureUnclassified
	}
	now := time.Now().UTC()
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[pipeline]
	if !found || !isKafkaPipeline(pipeline) {
		return
	}
	slot, found := state.kafkaSlots[slotID]
	if !found {
		return
	}
	slot.lastFailureAt = now
	slot.consecutiveFailures++
	slot.failureKinds |= kind
	slot.failureVersion++
	slot.state = "failed"
	slot.reasonCode = stablePipelineReason(reason)
	state.kafkaSlots[slotID] = slot
	recomputeKafkaPipelineAggregate(&state, "")
	workerPipelines.states[pipeline] = state
}

func KafkaConsumerSlotCommitted(pipeline string, slotID int, offset time.Time) {
	if workerShuttingDown.Load() {
		return
	}
	now := time.Now().UTC()
	if offset.IsZero() {
		offset = now
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	if workerShuttingDown.Load() {
		return
	}
	state, found := workerPipelines.states[pipeline]
	if !found || !isKafkaPipeline(pipeline) {
		return
	}
	slot, found := state.kafkaSlots[slotID]
	if !found {
		return
	}
	slot.lastCommittedOffsetAt = offset
	markKafkaConsumerSlotSuccess(&slot, now, "running")
	state.kafkaSlots[slotID] = slot
	state.lastProgressAt = now
	recomputeKafkaPipelineAggregate(&state, "running")
	workerPipelines.states[pipeline] = state
}

func kafkaConsumerSlotID(reader interface{}) int {
	if tracked, ok := reader.(*trackedKafkaConsumerReader); ok && tracked != nil {
		return tracked.slotID
	}
	return 0
}

func invalidateKafkaReaderSession(reader interface{}) {
	tracked, ok := reader.(*trackedKafkaConsumerReader)
	if !ok || tracked == nil {
		return
	}
	workerPipelines.Lock()
	defer workerPipelines.Unlock()
	state, found := workerPipelines.states[tracked.pipeline]
	if !found {
		return
	}
	slot, found := state.kafkaSlots[tracked.slotID]
	if !found || !slot.sessionActive || slot.session.clientID != tracked.clientID || !slot.sessionRecoveryEligible {
		return
	}
	slot.sessionRecoveryEligible = false
	slot.sessionVersion++
	slot.membership = kafkaConsumerMembershipState{}
	slot.postEmbeddingPartitions = nil
	state.membershipSampleVersion++
	state.kafkaSlots[tracked.slotID] = slot
	workerPipelines.states[tracked.pipeline] = state
}

func failKafkaConsumerForReader(reader interface{}, pipeline, reason string, kind pipelineFailureKinds) {
	KafkaConsumerSlotFailed(pipeline, kafkaConsumerSlotID(reader), reason, kind)
}

func commitKafkaConsumerForReader(reader interface{}, pipeline string, offset time.Time) {
	KafkaConsumerSlotCommitted(pipeline, kafkaConsumerSlotID(reader), offset)
}

func markKafkaConsumerSlotSuccess(slot *kafkaConsumerSlotState, now time.Time, stateName string) {
	if slot == nil {
		return
	}
	slot.lastSuccessAt = now
	slot.consecutiveFailures = 0
	slot.failureKinds = 0
	slot.failureVersion++
	slot.state = stateName
	slot.reasonCode = ""
}

func recomputeKafkaPipelineAggregate(state *workerPipelineState, recoveredState string) {
	if state == nil {
		return
	}
	activeWorkers := 0
	failures := state.unattributedFailures
	failureKinds := state.unattributedFailureKinds
	lastSuccess := state.lastSuccessAt
	lastFailure := state.lastFailureAt
	lastCommit := state.lastCommittedOffsetAt
	reasonCode := state.unattributedReasonCode
	for slotID := 0; slotID < state.expectedWorkers; slotID++ {
		slot, found := state.kafkaSlots[slotID]
		if !found {
			continue
		}
		if slot.workerStarted {
			activeWorkers++
		}
		if slot.consecutiveFailures > failures {
			failures = slot.consecutiveFailures
			reasonCode = slot.reasonCode
		}
		failureKinds |= slot.failureKinds
		if slot.lastSuccessAt.After(lastSuccess) {
			lastSuccess = slot.lastSuccessAt
		}
		if slot.lastFailureAt.After(lastFailure) {
			lastFailure = slot.lastFailureAt
		}
		if slot.lastCommittedOffsetAt.After(lastCommit) {
			lastCommit = slot.lastCommittedOffsetAt
		}
	}
	state.activeWorkers = activeWorkers
	state.consecutiveFailures = failures
	state.failureKinds = failureKinds
	state.lastSuccessAt = lastSuccess
	state.lastFailureAt = lastFailure
	state.lastCommittedOffsetAt = lastCommit
	switch {
	case failures > 0:
		state.state = "failed"
		if reasonCode == "" {
			state.reasonCode = "pipeline_failure"
		} else {
			state.reasonCode = reasonCode
		}
	case recoveredState != "":
		state.state = recoveredState
		state.reasonCode = ""
	case activeWorkers == 0 && state.state != "failed":
		state.state = "stopped"
	}
}

func kafkaConsumerSlotsSessionReady(state workerPipelineState) bool {
	if state.expectedWorkers < 1 {
		return false
	}
	for slotID := 0; slotID < state.expectedWorkers; slotID++ {
		slot, found := state.kafkaSlots[slotID]
		if !found || !slot.workerStarted || !slot.sessionActive || !slot.sessionRecoveryEligible {
			return false
		}
	}
	return true
}

func kafkaConsumerSlotsReady(state workerPipelineState, now time.Time) bool {
	if !kafkaConsumerSlotsSessionReady(state) {
		return false
	}
	for slotID := 0; slotID < state.expectedWorkers; slotID++ {
		slot := state.kafkaSlots[slotID]
		membership := slot.membership
		if !membership.Verified || membership.SessionVersion != slot.sessionVersion || membership.VerifiedAt.IsZero() || now.Sub(membership.VerifiedAt) > kafkaMembershipMaxAge {
			return false
		}
	}
	return true
}
