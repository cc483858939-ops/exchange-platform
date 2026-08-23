package cdc

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	identity   ConnectorIdentity
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
	identity := ProductionConnectorIdentity()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := runWithIdentity(ctx, sqlDB, config.KafkaConfig{ActivityEventsTopic: cdcAcceptanceActivityTopic}, connectURL, source, identity); err != nil {
		t.Fatal(err)
	}
	return &cdcAcceptanceFixture{db: sqlDB, brokers: brokers, connectURL: connectURL, topic: cdcAcceptanceActivityTopic, identity: identity}
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

func acceptanceTableReference(identity ConnectorIdentity) (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	return quotedIdentifier(identity.SchemaName) + "." + quotedIdentifier(identity.TableName), nil
}

type cdcAcceptanceEvent struct {
	RowID        string
	PartitionKey string
	Envelope     eventing.Envelope
	Message      []byte
}

func insertCDCAcceptanceEvent(ctx context.Context, db *sql.DB) (cdcAcceptanceEvent, error) {
	return insertAcceptanceEventInto(ctx, db, ProductionConnectorIdentity(), cdcAcceptanceActivityTopic, `{"source":"rev4.2-acceptance"}`)
}

func insertAcceptanceEventInto(ctx context.Context, db *sql.DB, identity ConnectorIdentity, topic, payload string) (cdcAcceptanceEvent, error) {
	table, err := acceptanceTableReference(identity)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	aggregateID := uuid.NewString()
	partitionKey := "cdc-acceptance-" + uuid.NewString()
	envelope := eventing.Envelope{
		ID: id, Type: "cdc.acceptance.v1", SchemaVersion: 1,
		AggregateType: "cdc_acceptance", AggregateID: aggregateID,
		OccurredAt: now, Payload: json.RawMessage(payload),
	}
	message, err := json.Marshal(envelope)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s
  (id, topic, partition_key, event_type, schema_version, aggregate_type, aggregate_id, message, occurred_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
	`, table), id, topic, partitionKey, envelope.Type, envelope.SchemaVersion, envelope.AggregateType, envelope.AggregateID, message, now, now)
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
					cancel()
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

	table, err := acceptanceTableReference(fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET event_type = event_type WHERE id = $1", table), committed.RowID); err == nil {
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
	rolledBack, err := insertAcceptanceEventTx(ctx, tx, fixture.identity, fixture.topic)
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
	if _, err := fixture.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", table), committed.RowID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, deleteBoundary, committed.Envelope.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("Outbox DELETE produced a duplicate Activity event")
	}
}

func insertAcceptanceEventTx(ctx context.Context, tx *sql.Tx, identity ConnectorIdentity, topic string) (cdcAcceptanceEvent, error) {
	table, err := acceptanceTableReference(identity)
	if err != nil {
		return cdcAcceptanceEvent{}, err
	}
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
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s
  (id, topic, partition_key, event_type, schema_version, aggregate_type, aggregate_id, message, occurred_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
	`, table), id, topic, partitionKey, envelope.Type, envelope.SchemaVersion, envelope.AggregateType, envelope.AggregateID, message, now, now)
	return cdcAcceptanceEvent{RowID: id, PartitionKey: partitionKey, Envelope: envelope, Message: message}, err
}

func TestRealCDCConnectorPauseRecoveryAcceptance(t *testing.T) {
	fixture := newCDCAcceptanceFixture(t)
	ctx := context.Background()
	if err := waitForConnectorState(ctx, fixture.connectURL, fixture.identity.ConnectorName, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	if err := connectorAction(ctx, fixture.connectURL, fixture.identity.ConnectorName, "pause"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := connectorAction(cleanupCtx, fixture.connectURL, fixture.identity.ConnectorName, "resume"); err != nil {
			t.Logf("resume production connector during pause-test cleanup: %v", err)
		}
	})
	if err := waitForConnectorState(ctx, fixture.connectURL, fixture.identity.ConnectorName, "PAUSED", "STOPPED"); err != nil {
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
	if err := connectorAction(ctx, fixture.connectURL, fixture.identity.ConnectorName, "resume"); err != nil {
		t.Fatal(err)
	}
	if err := waitForConnectorState(ctx, fixture.connectURL, fixture.identity.ConnectorName, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, event.Envelope.ID, 45*time.Second); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("paused-then-resumed event %s did not reach Kafka", event.Envelope.ID)
	}
}

func connectorAction(ctx context.Context, connectURL, connectorName, action string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(connectURL, "/")+"/connectors/"+url.PathEscape(connectorName)+"/"+url.PathEscape(action), nil)
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

func waitForConnectorState(ctx context.Context, connectURL, connectorName string, wanted ...string) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		status, err := connectorStatusForName(deadline, strings.TrimRight(connectURL, "/"), connectorName)
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
	if err := waitForConnectorState(ctx, fixture.connectURL, fixture.identity.ConnectorName, "RUNNING"); err != nil {
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
	identity := isolatedSnapshotIdentity()
	if err := validateIsolatedSnapshotIdentity(identity); err != nil {
		t.Fatal(err)
	}
	var snapshotDB *sql.DB
	var connectURL string
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := cleanupIsolatedSnapshot(cleanupCtx, snapshotDB, connectURL, identity); err != nil {
			t.Logf("isolated snapshot cleanup: %v", err)
		}
	})

	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	connectURL = strings.TrimSpace(os.Getenv("CDC_CONNECT_URL"))
	brokers := splitNonEmpty(os.Getenv("KAFKA_BROKERS"))
	if dsn == "" || connectURL == "" || len(brokers) == 0 {
		t.Skip("RUN_CDC_SNAPSHOT_INTEGRATION requires POSTGRES_TEST_DSN, KAFKA_BROKERS, and CDC_CONNECT_URL")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	snapshotDB, err = db.DB()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &cdcAcceptanceFixture{db: snapshotDB, brokers: brokers, connectURL: connectURL, topic: cdcAcceptanceActivityTopic, identity: identity}
	ctx := context.Background()
	if err := prepareIsolatedSnapshotOutbox(ctx, snapshotDB, identity); err != nil {
		t.Fatal(err)
	}

	seeded, err := insertAcceptanceEventInto(ctx, snapshotDB, identity, cdcAcceptanceActivityTopic, `{"source":"rev4.2.1-isolated-snapshot"}`)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := captureActivityTopicBoundary(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ParseSourceDatabaseConfig(dsn, Environment())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runWithIdentity(ctx, snapshotDB, config.KafkaConfig{ActivityEventsTopic: cdcAcceptanceActivityTopic}, connectURL, source, identity); err != nil {
		t.Fatal(err)
	}
	if err := waitForConnectorState(ctx, connectURL, identity.ConnectorName, "RUNNING"); err != nil {
		t.Fatal(err)
	}
	message, found, err := findActivityEventAfterBoundary(ctx, fixture, boundary, seeded.Envelope.ID, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("pre-existing snapshot event %s did not reach Kafka", seeded.Envelope.ID)
	}
	if message.Topic != fixture.topic || string(message.Key) != seeded.PartitionKey {
		t.Fatalf("snapshot Kafka routing topic=%q key=%q", message.Topic, string(message.Key))
	}
	var observed eventing.Envelope
	if err := json.Unmarshal(message.Value, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.ID != seeded.Envelope.ID || observed.Type != seeded.Envelope.Type || observed.SchemaVersion != seeded.Envelope.SchemaVersion || observed.AggregateType != seeded.Envelope.AggregateType || observed.AggregateID != seeded.Envelope.AggregateID || !bytes.Equal(observed.Payload, seeded.Envelope.Payload) {
		t.Fatalf("snapshot Kafka envelope=%+v want=%+v", observed, seeded.Envelope)
	}
}

func isolatedSnapshotIdentity() ConnectorIdentity {
	suffix := cdcAcceptanceSuffix()
	return ConnectorIdentity{
		ConnectorName:   "goexchange-outbox-acceptance-" + suffix,
		TopicPrefix:     "goexchange.cdc.acceptance." + suffix,
		SlotName:        "goexchange_outbox_slot_a_" + suffix,
		PublicationName: "goexchange_outbox_pub_a_" + suffix,
		SchemaName:      ProductionSchemaName,
		TableName:       "outbox_events_cdc_a_" + suffix,
	}
}

func cdcAcceptanceSuffix() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func validateIsolatedSnapshotIdentity(identity ConnectorIdentity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	production := ProductionConnectorIdentity()
	// The acceptance table intentionally remains in public. Every resource
	// name and the topic namespace, which can cause destructive cross-talk,
	// must still be different from production.
	if identity.ConnectorName == production.ConnectorName ||
		identity.TopicPrefix == production.TopicPrefix ||
		identity.SlotName == production.SlotName ||
		identity.PublicationName == production.PublicationName ||
		identity.TableName == production.TableName {
		return errors.New("isolated snapshot identity overlaps a production CDC resource")
	}
	return nil
}

func prepareIsolatedSnapshotOutbox(ctx context.Context, db *sql.DB, identity ConnectorIdentity) error {
	if db == nil {
		return errors.New("snapshot database connection is nil")
	}
	if err := validateIsolatedSnapshotIdentity(identity); err != nil {
		return err
	}
	table, err := acceptanceTableReference(identity)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE %s (
  id UUID PRIMARY KEY,
  topic VARCHAR(255) NOT NULL,
  partition_key VARCHAR(255) NOT NULL,
  event_type VARCHAR(128) NOT NULL,
  schema_version INTEGER NOT NULL,
  aggregate_type VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(128) NOT NULL,
  message JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
)`, table)); err != nil {
		return fmt.Errorf("create isolated snapshot outbox: %w", err)
	}
	if err := ensurePublicationForIdentity(ctx, db, identity); err != nil {
		return fmt.Errorf("create isolated snapshot publication: %w", err)
	}
	return nil
}

func cleanupIsolatedSnapshot(ctx context.Context, db *sql.DB, connectURL string, identity ConnectorIdentity) error {
	if err := validateIsolatedSnapshotIdentity(identity); err != nil {
		return err
	}
	var cleanupErrors []string
	if strings.TrimSpace(connectURL) != "" {
		if err := deleteConnectorForName(ctx, connectURL, identity.ConnectorName); err != nil {
			cleanupErrors = append(cleanupErrors, "delete connector: "+err.Error())
		}
		if err := waitForConnectorDeleted(ctx, connectURL, identity.ConnectorName); err != nil {
			cleanupErrors = append(cleanupErrors, "wait connector deletion: "+err.Error())
		}
	}
	if db != nil {
		if err := waitForSlotInactive(ctx, db, identity.SlotName); err != nil {
			cleanupErrors = append(cleanupErrors, "wait slot inactive: "+err.Error())
		} else if err := dropReplicationSlot(ctx, db, identity.SlotName); err != nil {
			cleanupErrors = append(cleanupErrors, "drop slot: "+err.Error())
		}
		if err := dropPublication(ctx, db, identity.PublicationName); err != nil {
			cleanupErrors = append(cleanupErrors, "drop publication: "+err.Error())
		}
		if err := dropSnapshotTable(ctx, db, identity); err != nil {
			cleanupErrors = append(cleanupErrors, "drop table: "+err.Error())
		}
	}
	if len(cleanupErrors) > 0 {
		return errors.New(strings.Join(cleanupErrors, "; "))
	}
	return nil
}

func deleteConnectorForName(ctx context.Context, connectURL, connectorName string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(connectURL, "/")+"/connectors/"+url.PathEscape(connectorName), nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete CDC connector %s returned %s", connectorName, response.Status)
	}
	return nil
}

func waitForConnectorDeleted(ctx context.Context, connectURL, connectorName string) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(deadline, http.MethodGet, strings.TrimRight(connectURL, "/")+"/connectors/"+url.PathEscape(connectorName)+"/status", nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
		if err == nil {
			if response.StatusCode == http.StatusNotFound {
				_ = response.Body.Close()
				return nil
			}
			_ = response.Body.Close()
			if response.StatusCode/100 == 2 {
				lastErr = fmt.Errorf("connector %s still exists", connectorName)
			} else {
				lastErr = fmt.Errorf("connector status returned %s", response.Status)
			}
		} else {
			lastErr = err
		}
		select {
		case <-deadline.Done():
			if lastErr != nil {
				return fmt.Errorf("connector %s was not deleted: %w", connectorName, lastErr)
			}
			return fmt.Errorf("connector %s was not deleted: %w", connectorName, deadline.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func waitForSlotInactive(ctx context.Context, db *sql.DB, slotName string) error {
	if db == nil {
		return errors.New("database connection is nil")
	}
	if len(slotName) == 0 || !postgresIdentifierPattern.MatchString(slotName) || len(slotName) > 63 {
		return fmt.Errorf("invalid replication slot name %q", slotName)
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		var active bool
		err := db.QueryRowContext(deadline, "SELECT active FROM pg_replication_slots WHERE slot_name = $1", slotName).Scan(&active)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			lastErr = err
		case !active:
			return nil
		default:
			lastErr = fmt.Errorf("replication slot %s is still active", slotName)
		}
		select {
		case <-deadline.Done():
			if lastErr != nil {
				return fmt.Errorf("replication slot %s did not become inactive: %w", slotName, lastErr)
			}
			return fmt.Errorf("replication slot %s did not become inactive: %w", slotName, deadline.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func dropReplicationSlot(ctx context.Context, db *sql.DB, slotName string) error {
	if _, err := db.ExecContext(ctx, "SELECT pg_drop_replication_slot($1) WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slotName); err != nil {
		return err
	}
	return nil
}

func dropPublication(ctx context.Context, db *sql.DB, publicationName string) error {
	if len(publicationName) == 0 || !postgresIdentifierPattern.MatchString(publicationName) || len(publicationName) > 63 {
		return fmt.Errorf("invalid publication name %q", publicationName)
	}
	_, err := db.ExecContext(ctx, "DROP PUBLICATION IF EXISTS "+quotedIdentifier(publicationName))
	return err
}

func dropSnapshotTable(ctx context.Context, db *sql.DB, identity ConnectorIdentity) error {
	table, err := acceptanceTableReference(identity)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
	return err
}
