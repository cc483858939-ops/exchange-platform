package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBootstrapOwnedSlotCompensationIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN unavailable")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL test database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	assertLogicalReplicationCapacity(t, sqlDB)
	source, err := ParseSourceDatabaseConfig(dsn, Environment())
	if err != nil {
		t.Fatalf("parse source database config: %v", err)
	}

	t.Run("Connect unavailable before slot creation", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil {
			t.Fatal("Connect preflight unexpectedly succeeded")
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, false)
	})

	t.Run("PUT failure with confirmed absent Connector cleans owned slot", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{
				bootstrapStatusResponse(http.StatusNotFound, ""),
				bootstrapStatusResponse(http.StatusNotFound, ""),
			},
			putResponse: bootstrapConnectResponse{status: http.StatusServiceUnavailable},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil {
			t.Fatal("PUT failure unexpectedly succeeded")
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, false)
		if script.deleteCountValue() != 0 || script.getCountValue() != 2 {
			t.Fatalf("unexpected compensation requests: deletes=%d gets=%d", script.deleteCountValue(), script.getCountValue())
		}
	})

	t.Run("ambiguous PUT with existing Connector deletes Connector then slot", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{
				bootstrapStatusResponse(http.StatusNotFound, ""),
				bootstrapStatusResponse(http.StatusOK, bootstrapConnectorJSON(identity.ConnectorName, "RUNNING", "RUNNING")),
				bootstrapStatusResponse(http.StatusNotFound, ""),
			},
			putResponse:    bootstrapConnectResponse{status: http.StatusServiceUnavailable},
			deleteResponse: bootstrapConnectResponse{status: http.StatusNoContent},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil {
			t.Fatal("ambiguous PUT unexpectedly succeeded")
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, false)
		if script.deleteCountValue() != 1 || script.getCountValue() != 3 {
			t.Fatalf("unexpected Connector compensation requests: deletes=%d gets=%d", script.deleteCountValue(), script.getCountValue())
		}
	})

	t.Run("unknown Connector state retains owned slot", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{
				bootstrapStatusResponse(http.StatusNotFound, ""),
				bootstrapStatusResponse(http.StatusInternalServerError, ""),
			},
			putResponse: bootstrapConnectResponse{status: http.StatusServiceUnavailable},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "state unknown") || !strings.Contains(strings.ToLower(err.Error()), "retained") {
			t.Fatalf("unknown Connector state was not reported clearly: %v", err)
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, true)
		if script.deleteCountValue() != 0 {
			t.Fatalf("unknown Connector state triggered DELETE: %d", script.deleteCountValue())
		}
	})

	t.Run("pre-existing slot is never deleted", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		createBootstrapSlot(t, sqlDB, identity.SlotName)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{bootstrapStatusResponse(http.StatusNotFound, "")},
			putResponse:  bootstrapConnectResponse{status: http.StatusServiceUnavailable},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil {
			t.Fatal("pre-existing slot failure unexpectedly succeeded")
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, true)
		if script.deleteCountValue() != 0 {
			t.Fatalf("pre-existing slot path issued Connector DELETE: %d", script.deleteCountValue())
		}
	})

	t.Run("pre-existing Connector retains newly created slot", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{
				bootstrapStatusResponse(http.StatusOK, bootstrapConnectorJSON(identity.ConnectorName, "RUNNING", "RUNNING")),
			},
			putResponse: bootstrapConnectResponse{status: http.StatusServiceUnavailable},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		_, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "pre-existing connector") || !strings.Contains(strings.ToLower(err.Error()), "resources retained") {
			t.Fatalf("pre-existing Connector was not retained explicitly: %v", err)
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, true)
		if script.deleteCountValue() != 0 {
			t.Fatalf("pre-existing Connector path issued DELETE: %d", script.deleteCountValue())
		}
	})

	t.Run("strict readiness succeeds and retains slot", func(t *testing.T) {
		identity := newBootstrapIntegrationIdentity()
		registerBootstrapTestResources(t, sqlDB, identity)
		script := &bootstrapFakeConnect{
			getResponses: []bootstrapConnectResponse{
				bootstrapStatusResponse(http.StatusNotFound, ""),
				bootstrapStatusResponse(http.StatusOK, bootstrapConnectorJSON(identity.ConnectorName, "RUNNING")),
				bootstrapStatusResponse(http.StatusOK, bootstrapConnectorJSON(identity.ConnectorName, "RUNNING", "UNASSIGNED")),
				bootstrapStatusResponse(http.StatusOK, bootstrapConnectorJSON(identity.ConnectorName, "RUNNING", "RUNNING")),
			},
			putResponse: bootstrapConnectResponse{status: http.StatusAccepted},
		}
		server := httptest.NewServer(script)
		defer server.Close()

		status, err := runBootstrapIntegration(t, sqlDB, source, identity, server.URL)
		if err != nil {
			t.Fatalf("strict readiness failed: %v", err)
		}
		if assessConnectorReadiness(identity.ConnectorName, status) != connectorReadinessReady {
			t.Fatalf("returned status was not strictly ready: %s", connectorStatusSummary(status))
		}
		assertBootstrapSlotExists(t, sqlDB, identity.SlotName, true)
		if script.deleteCountValue() != 0 || script.getCountValue() != 4 {
			t.Fatalf("unexpected successful bootstrap requests: deletes=%d gets=%d", script.deleteCountValue(), script.getCountValue())
		}
	})
}

func assertLogicalReplicationCapacity(t *testing.T, db *sql.DB) {
	t.Helper()
	var walLevel string
	if err := db.QueryRow("SHOW wal_level").Scan(&walLevel); err != nil {
		t.Fatalf("read wal_level: %v", err)
	}
	if !strings.EqualFold(strings.TrimSpace(walLevel), "logical") {
		t.Fatalf("wal_level=%q; want logical", walLevel)
	}
	for _, setting := range []string{"max_replication_slots", "max_wal_senders"} {
		var raw string
		if err := db.QueryRow("SHOW " + setting).Scan(&raw); err != nil {
			t.Fatalf("read %s: %v", setting, err)
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 2 {
			t.Fatalf("%s=%q; want at least 2", setting, raw)
		}
	}
}

func newBootstrapIntegrationIdentity() ConnectorIdentity {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	return ConnectorIdentity{
		ConnectorName:   "cdc-bootstrap-test-" + suffix,
		TopicPrefix:     "goexchange.cdc.test." + suffix,
		SlotName:        "cdc_bootstrap_slot_" + suffix,
		PublicationName: "cdc_bootstrap_pub_" + suffix,
		SchemaName:      ProductionSchemaName,
		TableName:       "cdc_bootstrap_outbox_" + suffix,
	}
}

func registerBootstrapTestResources(t *testing.T, db *sql.DB, identity ConnectorIdentity) {
	t.Helper()
	if err := identity.Validate(); err != nil {
		t.Fatalf("invalid generated test identity: %v", err)
	}
	if identity.TableName == ProductionTableName || identity.SlotName == SlotName || identity.PublicationName == PublicationName || identity.ConnectorName == ConnectorName {
		t.Fatal("generated test identity overlaps a production CDC resource")
	}
	table := quotedIdentifier(identity.SchemaName) + "." + quotedIdentifier(identity.TableName)
	if _, err := db.Exec(`CREATE TABLE ` + table + ` (
  id TEXT PRIMARY KEY,
  partition_key TEXT NOT NULL,
  topic TEXT NOT NULL,
  message JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
)`); err != nil {
		t.Fatalf("create test outbox table: %v", err)
	}
	t.Cleanup(func() { cleanupBootstrapTestResources(t, db, identity) })
}

func cleanupBootstrapTestResources(t *testing.T, db *sql.DB, identity ConnectorIdentity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), connectorCompensationTimeout)
	defer cancel()
	var cleanupErrors []string
	if err := waitForReplicationSlotInactive(ctx, db, identity.SlotName); err != nil {
		cleanupErrors = append(cleanupErrors, "wait slot inactive: "+err.Error())
	} else {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", identity.SlotName).Scan(&exists); err != nil {
			cleanupErrors = append(cleanupErrors, "check slot: "+err.Error())
		} else if exists {
			if _, err := db.ExecContext(ctx, "SELECT pg_drop_replication_slot($1)", identity.SlotName); err != nil {
				cleanupErrors = append(cleanupErrors, "drop slot: "+err.Error())
			}
		}
	}
	if _, err := db.ExecContext(ctx, "DROP PUBLICATION IF EXISTS "+quotedIdentifier(identity.PublicationName)); err != nil {
		cleanupErrors = append(cleanupErrors, "drop publication: "+err.Error())
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+quotedIdentifier(identity.SchemaName)+"."+quotedIdentifier(identity.TableName)); err != nil {
		cleanupErrors = append(cleanupErrors, "drop table: "+err.Error())
	}
	if len(cleanupErrors) > 0 {
		t.Errorf("CDC bootstrap integration cleanup failed: %s", strings.Join(cleanupErrors, "; "))
	}
}

func createBootstrapSlot(t *testing.T, db *sql.DB, slotName string) {
	t.Helper()
	if _, err := db.Exec("SELECT * FROM pg_create_logical_replication_slot($1, 'pgoutput')", slotName); err != nil {
		t.Fatalf("create pre-existing test slot: %v", err)
	}
}

func assertBootstrapSlotExists(t *testing.T, db *sql.DB, slotName string, want bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slotName).Scan(&exists); err != nil {
		t.Fatalf("check test slot: %v", err)
	}
	if exists != want {
		t.Fatalf("slot %s exists=%v want=%v", slotName, exists, want)
	}
}

func runBootstrapIntegration(t *testing.T, db *sql.DB, source SourceDatabaseConfig, identity ConnectorIdentity, connectURL string) (ConnectorStatus, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runWithIdentity(ctx, db, config.KafkaConfig{ActivityEventsTopic: "goexchange.activity.events.v1"}, connectURL, source, identity)
}

type bootstrapConnectResponse struct {
	status int
	body   string
}

type bootstrapFakeConnect struct {
	mu             sync.Mutex
	getResponses   []bootstrapConnectResponse
	putResponse    bootstrapConnectResponse
	deleteResponse bootstrapConnectResponse
	getCount       int
	deleteCount    int
}

func (fake *bootstrapFakeConnect) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	var response bootstrapConnectResponse
	switch request.Method {
	case http.MethodGet:
		fake.getCount++
		if len(fake.getResponses) > 0 {
			response = fake.getResponses[0]
			fake.getResponses = fake.getResponses[1:]
		} else {
			response = bootstrapStatusResponse(http.StatusNotFound, "")
		}
	case http.MethodPut:
		response = fake.putResponse
	case http.MethodDelete:
		fake.deleteCount++
		response = fake.deleteResponse
	default:
		response = bootstrapConnectResponse{status: http.StatusMethodNotAllowed}
	}
	fake.mu.Unlock()
	if response.status == 0 {
		response.status = http.StatusNotFound
	}
	if response.body != "" {
		writer.Header().Set("Content-Type", "application/json")
	}
	writer.WriteHeader(response.status)
	if response.body != "" {
		_, _ = writer.Write([]byte(response.body))
	}
}

func (fake *bootstrapFakeConnect) getCountValue() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.getCount
}

func (fake *bootstrapFakeConnect) deleteCountValue() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.deleteCount
}

func bootstrapStatusResponse(status int, body string) bootstrapConnectResponse {
	return bootstrapConnectResponse{status: status, body: body}
}

func bootstrapConnectorJSON(name, connectorState string, taskStates ...string) string {
	tasks := make([]map[string]interface{}, 0, len(taskStates))
	for id, state := range taskStates {
		tasks = append(tasks, map[string]interface{}{"id": id, "state": state})
	}
	payload := map[string]interface{}{
		"name":      name,
		"connector": map[string]string{"state": connectorState},
		"tasks":     tasks,
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
