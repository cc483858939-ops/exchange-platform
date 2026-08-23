package cdc

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const cdcAcceptanceActivityTopic = "goexchange.activity.events.v1"

type cdcAcceptanceFixture struct {
	db         *sql.DB
	brokers    []string
	connectURL string
	topic      string
}

func newCDCAcceptanceFixture(t *testing.T) *cdcAcceptanceFixture {
	t.Helper()
	if os.Getenv("RUN_CDC_INTEGRATION") != "1" {
		t.Skip("set RUN_CDC_INTEGRATION=1 to run real PostgreSQL/Kafka/Debezium acceptance")
	}
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	brokers := splitNonEmpty(os.Getenv("KAFKA_BROKERS"))
	connectURL := strings.TrimSpace(os.Getenv("CDC_CONNECT_URL"))
	if dsn == "" || len(brokers) == 0 || connectURL == "" {
		t.Skip("RUN_CDC_INTEGRATION requires POSTGRES_TEST_DSN, KAFKA_BROKERS, and CDC_CONNECT_URL")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	environment := Environment()
	source, err := ParseSourceDatabaseConfig(dsn, environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Run(ctx, sqlDB, config.KafkaConfig{ActivityEventsTopic: cdcAcceptanceActivityTopic}, connectURL, source); err != nil {
		t.Fatal(err)
	}
	return &cdcAcceptanceFixture{db: sqlDB, brokers: brokers, connectURL: connectURL, topic: cdcAcceptanceActivityTopic}
}

func splitNonEmpty(raw string) []string {
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

type cdcAcceptanceEvent struct {
	RowID        string
	PartitionKey string
	Envelope     eventing.Envelope
	Message      []byte
}

func insertCDCAcceptanceEvent(ctx context.Context, db *sql.DB) (cdcAcceptanceEvent, error) {
	now := time.Now().UTC()
	id := uuid.NewString()
	aggregateID := uuid.NewString()
	partitionKey := "cdc-acceptance-" + uuid.NewString()
	envelope := eventing.Envelope{
		ID: id, Type: "cdc.acceptance.v1", SchemaVersion: 1,
		AggregateType: "cdc_acceptance", AggregateID: aggregateID,
		OccurredAt: now, Payload: json.RawMessage(`{"source":"rev4.2-acceptance"}`),
	}
	message, err := json.Marshal(envelope)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO outbox_events
  (id, topic, partition_key, event_type, schema_version, aggregate_type, aggregate_id, message, occurred_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
`, id, cdcAcceptanceActivityTopic, partitionKey, envelope.Type, envelope.SchemaVersion, envelope.AggregateType, envelope.AggregateID, message, now, now)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
	return cdcAcceptanceEvent{RowID: id, PartitionKey: partitionKey, Envelope: envelope, Message: message}, nil
}

func captureActivityTopicBoundary(ctx context.Context, fixture *cdcAcceptanceFixture) (map[int]int64, error) {
	conn, err := kafka.Dial("tcp", fixture.brokers[0])
	if err != nil {
		return nil, err
	}
	partitions, err := conn.ReadPartitions(fixture.topic)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}
	result := make(map[int]int64, len(partitions))
	dialer := &kafka.Dialer{Timeout: 5 * time.Second}
	for _, partition := range partitions {
		// Kafka metadata can advertise the Compose-only hostname "kafka".
		// The acceptance test runs from the host, so use the explicitly
		// configured broker address instead of replaying that advertised name.
		leaderConn, err := dialer.DialLeader(ctx, "tcp", fixture.brokers[0], fixture.topic, partition.ID)
		if err != nil {
			return nil, err
		}
		last, err := leaderConn.ReadLastOffset()
		_ = leaderConn.Close()
		if err != nil {
			return nil, err
		}
		if last < 0 {
			last = 0
		}
		result[partition.ID] = last
	}
	return result, nil
}

type cdcKafkaSearchResult struct {
	message kafka.Message
	found   bool
	err     error
}

func findActivityEventAfterBoundary(ctx context.Context, fixture *cdcAcceptanceFixture, boundary map[int]int64, eventID string, timeout time.Duration) (kafka.Message, bool, error) {
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make(chan cdcKafkaSearchResult, len(boundary))
	var waitGroup sync.WaitGroup
	for partition, start := range boundary {
		waitGroup.Add(1)
		go func(partition int, start int64) {
			defer waitGroup.Done()
			reader := kafka.NewReader(kafka.ReaderConfig{
				Brokers: fixture.brokers, Topic: fixture.topic, Partition: partition,
				StartOffset: start, MinBytes: 1, MaxBytes: 4 * 1024 * 1024, MaxWait: 500 * time.Millisecond,
			})
			defer reader.Close()
			for {
				message, err := reader.ReadMessage(searchCtx)
				if err != nil {
					results <- cdcKafkaSearchResult{err: err}
					return
				}
				var envelope eventing.Envelope
				if json.Unmarshal(message.Value, &envelope) == nil && envelope.ID == eventID {
					results <- cdcKafkaSearchResult{message: message, found: true}
					return
				}
			}
		}(partition, start)
	}
	waitGroup.Wait()
	close(results)
	var firstErr error
	for result := range results {
		if result.found {
			return result.message, true, nil
		}
		if result.err != nil && !errors.Is(result.err, context.DeadlineExceeded) && !errors.Is(result.err, context.Canceled) && firstErr == nil {
			firstErr = result.err
		}
	}
	return kafka.Message{}, false, firstErr
}

func TestRealCDCCommitRollbackUpdateAndDeleteAcceptance(t *testing.T) {
	fixture := newCDCAcceptanceFixture(t)
	ctx := context.Background()

	boundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := insertCDCAcceptanceEvent(ctx, fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	message, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, committed.Envelope.ID, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("committed event %s did not reach Kafka", committed.Envelope.ID)
	}
	if message.Topic != fixture.topic || string(message.Key) != committed.PartitionKey {
		t.Fatalf("Kafka routing topic=%q key=%q", message.Topic, string(message.Key))
	}
	var observed eventing.Envelope
	if err := json.Unmarshal(message.Value, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.ID != committed.Envelope.ID || observed.Type != committed.Envelope.Type || observed.AggregateID != committed.Envelope.AggregateID || !bytes.Equal(observed.Payload, committed.Envelope.Payload) {
		t.Fatalf("Kafka envelope=%+v want=%+v", observed, committed.Envelope)
	}

	if _, err := fixture.db.ExecContext(ctx, "UPDATE outbox_events SET event_type = event_type WHERE id = $1", committed.RowID); err == nil {
		t.Fatal("append-only Outbox UPDATE unexpectedly succeeded")
	}

	rollbackBoundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := insertAcceptanceEventTx(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, rollbackBoundary, rolledBack.Envelope.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rolled-back event %s reached Kafka", rolledBack.Envelope.ID)
	}

	deleteBoundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, "DELETE FROM outbox_events WHERE id = $1", committed.RowID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, deleteBoundary, committed.Envelope.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("Outbox DELETE produced a duplicate Activity event")
	}
}

func insertAcceptanceEventTx(ctx context.Context, tx *sql.Tx) (cdcAcceptanceEvent, error) {
	now := time.Now().UTC()
	id := uuid.NewString()
	aggregateID := uuid.NewString()
	partitionKey := "cdc-acceptance-" + uuid.NewString()
	envelope := eventing.Envelope{
		ID: id, Type: "cdc.acceptance.v1", SchemaVersion: 1,
		AggregateType: "cdc_acceptance", AggregateID: aggregateID,
		OccurredAt: now, Payload: json.RawMessage(`{"source":"rev4.2-acceptance-rollback"}`),
	}
	message, err := json.Marshal(envelope)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO outbox_events
  (id, topic, partition_key, event_type, schema_version, aggregate_type, aggregate_id, message, occurred_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
`, id, cdcAcceptanceActivityTopic, partitionKey, envelope.Type, envelope.SchemaVersion, envelope.AggregateType, envelope.AggregateID, message, now, now)
	return cdcAcceptanceEvent{RowID: id, PartitionKey: partitionKey, Envelope: envelope, Message: message}, err
}

func TestRealCDCConnectorPauseRecoveryAcceptance(t *testing.T) {
	fixture := newCDCAcceptanceFixture(t)
	ctx := context.Background()
	if err := connectorAction(ctx, fixture.connectURL, "pause"); err != nil {
		t.Fatal(err)
	}
	if err := waitForConnectorState(ctx, fixture.connectURL, "PAUSED", "STOPPED"); err != nil {
		t.Fatal(err)
	}
	boundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	event, err := insertCDCAcceptanceEvent(ctx, fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := connectorAction(ctx, fixture.connectURL, "resume"); err != nil {
		t.Fatal(err)
	}
	if err := waitForConnectorState(ctx, fixture.connectURL, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, event.Envelope.ID, 45*time.Second); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("paused-then-resumed event %s did not reach Kafka", event.Envelope.ID)
	}
}

func connectorAction(ctx context.Context, connectURL, action string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(connectURL, "/")+"/connectors/"+ConnectorName+"/"+action, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("connector %s returned %s", action, response.Status)
	}
	return nil
}

func waitForConnectorState(ctx context.Context, connectURL string, wanted ...string) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		status, err := connectorStatus(deadline, strings.TrimRight(connectURL, "/"))
		if err == nil {
			state := strings.ToUpper(status.Connector.State)
			for _, candidate := range wanted {
				if state == candidate {
					return nil
				}
			}
			if state == "FAILED" {
				return fmt.Errorf("connector entered FAILED: %s", status.Connector.Trace)
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("connector did not reach %v: %w", wanted, deadline.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestRealCDCContainerRestartResumeAcceptance(t *testing.T) {
	if os.Getenv("RUN_CDC_CONTAINER_RESTART") != "1" {
		t.Skip("set RUN_CDC_CONTAINER_RESTART=1 with RUN_CDC_INTEGRATION=1 to run Docker restart acceptance")
	}
	fixture := newCDCAcceptanceFixture(t)
	command := exec.Command("docker", "compose", "restart", "kafka-connect")
	command.Dir = "../.."
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("BLOCKED: docker compose restart kafka-connect failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	ctx := context.Background()
	if err := waitForConnectorState(ctx, fixture.connectURL, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	boundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	event, err := insertCDCAcceptanceEvent(ctx, fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, event.Envelope.ID, 45*time.Second); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("post-restart event %s did not reach Kafka", event.Envelope.ID)
	}
}

func TestRealCDCIsolatedInitialSnapshotAcceptance(t *testing.T) {
	if os.Getenv("RUN_CDC_INTEGRATION") != "1" {
		t.Skip("set RUN_CDC_INTEGRATION=1 to run real PostgreSQL/Kafka/Debezium acceptance")
	}
	if os.Getenv("RUN_CDC_SNAPSHOT_INTEGRATION") != "1" {
		t.Skip("set RUN_CDC_SNAPSHOT_INTEGRATION=1 to run isolated initial-snapshot acceptance")
	}
	dsn := strings.TrimSpace(os.Getenv("CDC_SNAPSHOT_DATABASE_DSN"))
	connectURL := strings.TrimSpace(os.Getenv("CDC_SNAPSHOT_CONNECT_URL"))
	brokers := splitNonEmpty(os.Getenv("CDC_SNAPSHOT_KAFKA_BROKERS"))
	if len(brokers) == 0 {
		brokers = splitNonEmpty(os.Getenv("KAFKA_BROKERS"))
	}
	if dsn == "" || connectURL == "" || len(brokers) == 0 {
		t.Skip("RUN_CDC_SNAPSHOT_INTEGRATION requires CDC_SNAPSHOT_DATABASE_DSN, CDC_SNAPSHOT_CONNECT_URL, and Kafka brokers")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &cdcAcceptanceFixture{db: sqlDB, brokers: brokers, connectURL: connectURL, topic: cdcAcceptanceActivityTopic}
	ctx := context.Background()
	if err := prepareIsolatedSnapshotOutbox(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := deleteConnector(cleanupCtx, connectURL); err != nil {
			t.Logf("snapshot connector cleanup: %v", err)
		}
		if _, err := sqlDB.ExecContext(cleanupCtx, "SELECT pg_drop_replication_slot($1) WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", SlotName); err != nil {
			t.Logf("snapshot slot cleanup: %v", err)
		}
		if _, err := sqlDB.ExecContext(cleanupCtx, "DROP PUBLICATION IF EXISTS "+PublicationName); err != nil {
			t.Logf("snapshot publication cleanup: %v", err)
		}
		if _, err := sqlDB.ExecContext(cleanupCtx, "DROP TABLE IF EXISTS outbox_events"); err != nil {
			t.Logf("snapshot table cleanup: %v", err)
		}
	})

	boundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := insertCDCAcceptanceEvent(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	environment := Environment()
	if value := strings.TrimSpace(os.Getenv("CDC_SNAPSHOT_DATABASE_USER")); value != "" {
		environment.User = value
	}
	if value, ok := os.LookupEnv("CDC_SNAPSHOT_DATABASE_PASSWORD"); ok {
		environment.Password = value
	}
	if value := strings.TrimSpace(os.Getenv("CDC_SNAPSHOT_DATABASE_SSLMODE")); value != "" {
		environment.SSLMode = value
	}
	source, err := ParseSourceDatabaseConfig(dsn, environment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, sqlDB, config.KafkaConfig{ActivityEventsTopic: cdcAcceptanceActivityTopic}, connectURL, source); err != nil {
		t.Fatal(err)
	}
	if err := waitForConnectorState(ctx, connectURL, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, seeded.Envelope.ID, 60*time.Second); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("pre-existing snapshot event %s did not reach Kafka", seeded.Envelope.ID)
	}
}

func prepareIsolatedSnapshotOutbox(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("snapshot database connection is nil")
	}
	statements := []string{
		"DROP PUBLICATION IF EXISTS " + PublicationName,
		"CREATE TABLE IF NOT EXISTS outbox_events (id UUID PRIMARY KEY, topic VARCHAR(255) NOT NULL, partition_key VARCHAR(255) NOT NULL, event_type VARCHAR(128) NOT NULL, schema_version INTEGER NOT NULL, aggregate_type VARCHAR(64) NOT NULL, aggregate_id VARCHAR(128) NOT NULL, message JSONB NOT NULL, occurred_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL)",
		"TRUNCATE TABLE outbox_events",
		"SELECT pg_drop_replication_slot($1) WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)",
		"CREATE PUBLICATION " + PublicationName + " FOR TABLE public.outbox_events",
	}
	for index, statement := range statements {
		var err error
		if index == 3 {
			_, err = db.ExecContext(ctx, statement, SlotName)
		} else {
			_, err = db.ExecContext(ctx, statement)
		}
		if err != nil {
			return fmt.Errorf("prepare isolated snapshot statement %d: %w", index+1, err)
		}
	}
	return nil
}

func deleteConnector(ctx context.Context, connectURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(connectURL, "/")+"/connectors/"+ConnectorName, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete CDC connector returned %s", response.Status)
	}
	return nil
}
