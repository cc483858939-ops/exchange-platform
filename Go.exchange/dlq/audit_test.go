package dlq

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/segmentio/kafka-go"
)

type fakeAuditRepository struct {
	started     []models.KafkaDLQReplay
	completions []auditCompletion
	startErr    error
	completeErr error
	sequence    *[]string
}

type auditCompletion struct {
	id          string
	status      string
	completedAt time.Time
	errMessage  string
}

func (f *fakeAuditRepository) Start(_ context.Context, row models.KafkaDLQReplay) error {
	if f.sequence != nil {
		*f.sequence = append(*f.sequence, "audit-start")
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, row)
	return nil
}

func (f *fakeAuditRepository) Complete(_ context.Context, id, status string, completedAt time.Time, errorMessage string) error {
	if f.sequence != nil {
		*f.sequence = append(*f.sequence, "audit-"+status)
	}
	f.completions = append(f.completions, auditCompletion{id: id, status: status, completedAt: completedAt, errMessage: errorMessage})
	return f.completeErr
}

type fakeRawPublisher struct {
	topics   []string
	messages []kafka.Message
	err      error
	sequence *[]string
}

func (f *fakeRawPublisher) PublishRaw(_ context.Context, topic string, messages ...kafka.Message) error {
	if f.sequence != nil {
		*f.sequence = append(*f.sequence, "publish")
	}
	f.topics = append(f.topics, topic)
	f.messages = append(f.messages, messages...)
	return f.err
}

func TestExecuteReplayAuditsStartThenPublishThenSuccess(t *testing.T) {
	located, plan := validReplayExecution(t)
	sequence := []string{}
	audit := &fakeAuditRepository{sequence: &sequence}
	publisher := &fakeRawPublisher{sequence: &sequence}
	replayID := "replay-123"
	now := time.Date(2026, 9, 28, 3, 4, 5, 0, time.UTC)
	result, err := ExecuteReplay(context.Background(), located, plan, replayID, now, audit, publisher)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sequence, []string{"audit-start", "publish", "audit-succeeded"}) {
		t.Fatalf("execution order=%v", sequence)
	}
	if len(audit.started) != 1 || audit.started[0].ID != replayID || audit.started[0].Status != ReplayStatusStarted || audit.started[0].StartedAt != now {
		t.Fatalf("audit start=%+v", audit.started)
	}
	if len(audit.completions) != 1 || audit.completions[0].id != replayID || audit.completions[0].status != ReplayStatusSucceeded {
		t.Fatalf("audit completions=%+v", audit.completions)
	}
	if !result.PublisherInvoked || result.Status != ReplayStatusSucceeded || len(publisher.messages) != 1 || publisher.topics[0] != plan.SourceTopic {
		t.Fatalf("result=%+v topics=%v messages=%d", result, publisher.topics, len(publisher.messages))
	}
	var provenanceID string
	for _, header := range publisher.messages[0].Headers {
		if header.Key == ReplayIDHeader {
			provenanceID = string(header.Value)
		}
	}
	if provenanceID != replayID || audit.started[0].ID != provenanceID {
		t.Fatalf("audit/provenance replay IDs differ: audit=%s header=%s", audit.started[0].ID, provenanceID)
	}
}

func TestExecuteReplayPublisherErrorRecordsUnknown(t *testing.T) {
	located, plan := validReplayExecution(t)
	audit := &fakeAuditRepository{}
	publisher := &fakeRawPublisher{err: errors.New("write timeout")}
	result, err := ExecuteReplay(context.Background(), located, plan, "replay-unknown", time.Now().UTC(), audit, publisher)
	if err == nil || result.Status != ReplayStatusUnknown || !result.PublisherInvoked {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(audit.completions) != 1 || audit.completions[0].status != ReplayStatusUnknown || audit.completions[0].errMessage != "write timeout" {
		t.Fatalf("unknown audit=%+v", audit.completions)
	}
}

func TestExecuteReplayAuditInsertFailurePreventsPublish(t *testing.T) {
	located, plan := validReplayExecution(t)
	audit := &fakeAuditRepository{startErr: errors.New("database unavailable")}
	publisher := &fakeRawPublisher{}
	result, err := ExecuteReplay(context.Background(), located, plan, "replay-no-start", time.Now().UTC(), audit, publisher)
	if err == nil || result.PublisherInvoked || len(publisher.messages) != 0 {
		t.Fatalf("result=%+v err=%v publisher calls=%d", result, err, len(publisher.messages))
	}
}

func TestExecuteReplayRejectsModifiedPlanBeforeAuditOrPublish(t *testing.T) {
	located, plan := validReplayExecution(t)
	plan.SourcePartition++
	audit := &fakeAuditRepository{}
	publisher := &fakeRawPublisher{}
	result, err := ExecuteReplay(context.Background(), located, plan, "replay-invalid-plan", time.Now().UTC(), audit, publisher)
	if err == nil || result.PublisherInvoked || len(audit.started) != 0 || len(publisher.messages) != 0 {
		t.Fatalf("result=%+v err=%v audit starts=%d publishes=%d", result, err, len(audit.started), len(publisher.messages))
	}
}

func TestExecuteReplayAuditCompletionFailureDoesNotRepublish(t *testing.T) {
	located, plan := validReplayExecution(t)
	audit := &fakeAuditRepository{completeErr: errors.New("database disconnected")}
	publisher := &fakeRawPublisher{}
	result, err := ExecuteReplay(context.Background(), located, plan, "replay-completion-fails", time.Now().UTC(), audit, publisher)
	if err == nil || result.Status != ReplayStatusUnknown || len(publisher.messages) != 1 || len(audit.completions) != 1 {
		t.Fatalf("result=%+v err=%v publish calls=%d completions=%d", result, err, len(publisher.messages), len(audit.completions))
	}
	if audit.completions[0].status != ReplayStatusSucceeded {
		t.Fatalf("completion status=%q want succeeded update attempt", audit.completions[0].status)
	}
}

func validReplayExecution(t *testing.T) (LocatedRecord, ReplayPlan) {
	t.Helper()
	registry, err := NewReplayPolicyRegistry(replayTestKafkaConfig())
	if err != nil {
		t.Fatal(err)
	}
	located := LocatedRecord{DLQTopic: "consumer-dlq", DLQPartition: 1, DLQOffset: 2, Record: replayTestRecord("user_behavior_projection", "behavior")}
	plan, err := BuildReplayPlan(located, registry)
	if err != nil {
		t.Fatal(err)
	}
	return located, plan
}

var _ eventing.RawPublisher = (*fakeRawPublisher)(nil)
