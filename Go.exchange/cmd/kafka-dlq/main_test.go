package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/dlq"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/segmentio/kafka-go"
)

type fakeReader struct {
	records    []dlq.LocatedRecord
	readErr    error
	scanErr    error
	scanResult dlq.ScanResult
	scanCalls  int
	readCalls  int
}

func (f *fakeReader) ReadAt(_ context.Context, partition int, offset int64) (dlq.LocatedRecord, error) {
	f.readCalls++
	if f.readErr != nil {
		return dlq.LocatedRecord{}, f.readErr
	}
	for _, record := range f.records {
		if record.DLQPartition == partition && record.DLQOffset == offset {
			return record, nil
		}
	}
	return dlq.LocatedRecord{}, errors.New("record not found")
}

func (f *fakeReader) Scan(_ context.Context, filter dlq.Filter) ([]dlq.LocatedRecord, dlq.ScanResult, error) {
	f.scanCalls++
	if f.scanErr != nil {
		return nil, dlq.ScanResult{}, f.scanErr
	}
	matched := make([]dlq.LocatedRecord, 0, len(f.records))
	for _, record := range f.records {
		if filter.Consumer != "" && record.Record.Consumer != filter.Consumer ||
			filter.SourceTopic != "" && record.Record.Source.Topic != filter.SourceTopic ||
			filter.ErrorClass != "" && record.Record.Failure.Class != filter.ErrorClass ||
			filter.ErrorCode != "" && record.Record.Failure.Code != filter.ErrorCode ||
			filter.EventID != "" && record.Record.EventID != filter.EventID {
			continue
		}
		matched = append(matched, record)
	}
	result := f.scanResult
	if result.Scanned == 0 {
		result.Scanned = len(f.records)
	}
	result.Matched = len(matched)
	if len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	result.Returned = len(matched)
	return matched, result, nil
}

type fakeAudit struct {
	starts      []models.KafkaDLQReplay
	completions []fakeAuditCompletion
	startErr    error
	completeErr error
}

type fakeAuditCompletion struct {
	id     string
	status string
	error  string
}

func (f *fakeAudit) Start(_ context.Context, row models.KafkaDLQReplay) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.starts = append(f.starts, row)
	return nil
}

func (f *fakeAudit) Complete(_ context.Context, id, status string, _ time.Time, message string) error {
	f.completions = append(f.completions, fakeAuditCompletion{id: id, status: status, error: message})
	return f.completeErr
}

type fakePublisher struct {
	topics   []string
	messages []kafka.Message
	calls    int
	errorAt  int
}

func (f *fakePublisher) PublishRaw(_ context.Context, topic string, messages ...kafka.Message) error {
	f.calls++
	f.topics = append(f.topics, topic)
	f.messages = append(f.messages, messages...)
	if f.errorAt == f.calls {
		return errors.New("broker write timed out")
	}
	return nil
}

func TestReplayDryRunBuildsPlansWithoutOpeningDBOrPublisher(t *testing.T) {
	reader := &fakeReader{records: []dlq.LocatedRecord{cliTestLocated(2)}}
	openedDB, openedPublisher := 0, 0
	deps := cliDependencies{
		reader: reader,
		openAudit: func(*config.Config) (dlq.AuditRepository, io.Closer, error) {
			openedDB++
			return nil, nil, nil
		},
		openPublisher: func(config.KafkaConfig) (eventing.RawPublisher, io.Closer, error) {
			openedPublisher++
			return nil, nil, nil
		},
	}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"replay", "--consumer", "user_behavior_projection", "--error-code", "decode_envelope"}, cliTestConfig(), deps, &out, &errOut)
	if code != 0 || openedDB != 0 || openedPublisher != 0 || reader.scanCalls != 1 {
		t.Fatalf("code=%d db=%d publisher=%d scans=%d out=%s err=%s", code, openedDB, openedPublisher, reader.scanCalls, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), `"safety_mode":"consumer_inbox"`) || !strings.Contains(out.String(), "no Kafka publish or audit write performed") {
		t.Fatalf("dry-run output=%s", out.String())
	}
}

func TestReplayExecuteRequiresNarrowingSelector(t *testing.T) {
	reader := &fakeReader{records: []dlq.LocatedRecord{cliTestLocated(2)}}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"replay", "--consumer", "user_behavior_projection", "--execute"}, cliTestConfig(), cliDependencies{reader: reader}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "refusing broad replay: add at least one narrowing selector") || reader.scanCalls != 0 {
		t.Fatalf("code=%d scans=%d out=%s err=%s", code, reader.scanCalls, out.String(), errOut.String())
	}
}

func TestReplayExecuteSharesAuditAndProvenanceID(t *testing.T) {
	reader := &fakeReader{records: []dlq.LocatedRecord{cliTestLocated(2)}}
	audit := &fakeAudit{}
	publisher := &fakePublisher{}
	deps := cliDependencies{
		reader:        reader,
		openAudit:     func(*config.Config) (dlq.AuditRepository, io.Closer, error) { return audit, nil, nil },
		openPublisher: func(config.KafkaConfig) (eventing.RawPublisher, io.Closer, error) { return publisher, nil, nil },
		now:           func() time.Time { return time.Date(2026, 9, 28, 5, 6, 7, 0, time.UTC) },
		newReplayID:   func() string { return "replay-shared-id" },
	}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"replay", "--partition", "1", "--offset", "2", "--execute"}, cliTestConfig(), deps, &out, &errOut)
	if code != 0 || errOut.Len() != 0 || publisher.calls != 1 || len(audit.starts) != 1 || len(audit.completions) != 1 {
		t.Fatalf("code=%d publisher=%d starts=%d completions=%d out=%s err=%s", code, publisher.calls, len(audit.starts), len(audit.completions), out.String(), errOut.String())
	}
	if publisher.topics[0] != "behavior" || audit.starts[0].ID != "replay-shared-id" || string(publisher.messages[0].Headers[2].Value) != audit.starts[0].ID || audit.completions[0].status != dlq.ReplayStatusSucceeded {
		t.Fatalf("topic=%v audit=%+v completion=%+v headers=%#v", publisher.topics, audit.starts[0], audit.completions[0], publisher.messages[0].Headers)
	}
	if !strings.Contains(out.String(), "status=succeeded") || !strings.Contains(out.String(), "attempted=1 succeeded=1 unknown=0 remaining=0") {
		t.Fatalf("execute summary=%s", out.String())
	}
}

func TestReplayBatchStopsAndReturnsPartialExitCode(t *testing.T) {
	reader := &fakeReader{records: []dlq.LocatedRecord{cliTestLocated(1), cliTestLocated(2), cliTestLocated(3)}}
	audit := &fakeAudit{}
	publisher := &fakePublisher{errorAt: 3}
	deps := cliDependencies{
		reader:        reader,
		openAudit:     func(*config.Config) (dlq.AuditRepository, io.Closer, error) { return audit, nil, nil },
		openPublisher: func(config.KafkaConfig) (eventing.RawPublisher, io.Closer, error) { return publisher, nil, nil },
		newReplayID:   func() string { return "batch-replay" },
	}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"replay", "--consumer", "user_behavior_projection", "--error-code", "decode_envelope", "--execute"}, cliTestConfig(), deps, &out, &errOut)
	if code != 2 || publisher.calls != 3 || len(audit.starts) != 3 || len(audit.completions) != 3 {
		t.Fatalf("code=%d publishes=%d starts=%d completions=%d out=%s err=%s", code, publisher.calls, len(audit.starts), len(audit.completions), out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "attempted=3 succeeded=2 unknown=1 remaining=0") || !strings.Contains(errOut.String(), "outcome unknown") {
		t.Fatalf("partial batch output=%s error=%s", out.String(), errOut.String())
	}
}

func TestListDoesNotPrintPayloadOrSourceKey(t *testing.T) {
	located := cliTestLocated(2)
	located.Record.Source.Key = []byte("private-key")
	located.Record.Source.Value = []byte("private-payload")
	reader := &fakeReader{records: []dlq.LocatedRecord{located}}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"list", "--json"}, cliTestConfig(), cliDependencies{reader: reader}, &out, &errOut)
	if code != 0 || strings.Contains(out.String(), "private-key") || strings.Contains(out.String(), "private-payload") {
		t.Fatalf("code=%d output leaked payload: %s error=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), `"error_code":"decode_envelope"`) || !strings.Contains(out.String(), `"returned":1`) {
		t.Fatalf("safe metadata missing from JSON: %s", out.String())
	}
}

func TestShowEncodesBinaryBytesAndUsesSafePreview(t *testing.T) {
	located := cliTestLocated(2)
	located.Record.Source.Key = []byte{0xff, 0, 1}
	located.Record.Source.Value = []byte("line\ncontrol\x1b")
	reader := &fakeReader{records: []dlq.LocatedRecord{located}}
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"show", "--partition", "1", "--offset", "2"}, cliTestConfig(), cliDependencies{reader: reader}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "key_base64=/wAB") || !strings.Contains(out.String(), `key_preview="<non-utf8>"`) || !strings.Contains(out.String(), `value_preview="line\ncontrol�"`) {
		t.Fatalf("code=%d output=%s error=%s", code, out.String(), errOut.String())
	}
}

func TestReplayDoesNotAcceptAllFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runCLI(context.Background(), []string{"replay", "--all", "--execute"}, cliTestConfig(), cliDependencies{}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "flag provided but not defined: -all") {
		t.Fatalf("code=%d err=%s", code, errOut.String())
	}
}

func cliTestLocated(offset int64) dlq.LocatedRecord {
	failedAt := time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)
	return dlq.LocatedRecord{
		DLQTopic: "consumer-dlq", DLQPartition: 1, DLQOffset: offset,
		Record: eventing.DeadLetterRecord{
			SchemaVersion: eventing.DeadLetterSchemaVersion, Consumer: "user_behavior_projection", EventID: "event-" + string(rune('0'+offset)),
			Source:  eventing.DeadLetterSource{Topic: "behavior", Partition: 3, Offset: 91, Key: []byte("key"), Value: []byte("opaque payload"), Headers: []eventing.DeadLetterHeader{{Key: "trace", Value: []byte("trace-id")}}, Time: failedAt.Add(-time.Minute)},
			Failure: eventing.DeadLetterFailure{Class: "permanent", Code: "decode_envelope", Reason: "not valid JSON", Attempts: 1, FailedAt: failedAt},
		},
	}
}

func cliTestConfig() *config.Config {
	return &config.Config{Kafka: config.KafkaConfig{
		Brokers: []string{"127.0.0.1:9092"}, UserBehaviorTopic: "behavior", LikeSnapshotTopic: "snapshot",
		RecommendationEventsTopic: "recommendation", PostEmbeddingTopic: "embedding", ActivityEventsTopic: "activity", ConsumerDLQTopic: "consumer-dlq",
	}}
}
