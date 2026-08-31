package cdc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestBuildConnectorConfigPinsOutboxRoutingContract(t *testing.T) {
	cfg := BuildConnectorConfig(config.KafkaConfig{Brokers: []string{"kafka:9092"}}, SourceDatabaseConfig{
		Host: "postgres.internal", Port: 6543, Database: "exchange",
		User: "cdc-user", Password: "cdc-password", SSLMode: "verify-full",
	})
	wants := map[string]string{
		"connector.class":                               "io.debezium.connector.postgresql.PostgresConnector",
		"plugin.name":                                   "pgoutput",
		"database.hostname":                             "postgres.internal",
		"database.port":                                 "6543",
		"database.dbname":                               "exchange",
		"database.user":                                 "cdc-user",
		"database.password":                             "cdc-password",
		"database.sslmode":                              "verify-full",
		"topic.prefix":                                  "goexchange.cdc",
		"slot.name":                                     SlotName,
		"publication.name":                              PublicationName,
		"publication.autocreate.mode":                   "disabled",
		"table.include.list":                            "public.outbox_events",
		"snapshot.mode":                                 "initial",
		"heartbeat.interval.ms":                         "10000",
		"slot.drop.on.stop":                             "false",
		"predicates":                                    "IsOutboxTable",
		"predicates.IsOutboxTable.type":                 "org.apache.kafka.connect.transforms.predicates.TopicNameMatches",
		"predicates.IsOutboxTable.pattern":              `^goexchange\.cdc\.public\.outbox_events$`,
		"transforms.outbox.predicate":                   "IsOutboxTable",
		"transforms.outbox.table.field.event.id":        "id",
		"transforms.outbox.table.field.event.key":       "partition_key",
		"transforms.outbox.table.field.event.payload":   "message",
		"transforms.outbox.table.field.event.timestamp": "occurred_at",
		"transforms.outbox.table.expand.json.payload":   "true",
		"transforms.outbox.table.op.invalid.behavior":   "fatal",
		"transforms.outbox.route.by.field":              "topic",
		"transforms.outbox.route.topic.regex":           "(?<routedByValue>.*)",
		"transforms.outbox.route.topic.replacement":     "${routedByValue}",
		"key.converter":                                 "org.apache.kafka.connect.storage.StringConverter",
		"value.converter":                               "org.apache.kafka.connect.json.JsonConverter",
		"value.converter.schemas.enable":                "false",
	}
	for key, want := range wants {
		if got := cfg[key]; got != want {
			t.Fatalf("config[%q]=%q want=%q", key, got, want)
		}
	}
	if cfg["database.user"] == "${CDC_DATABASE_USER}" || cfg["database.password"] == "${CDC_DATABASE_PASSWORD}" {
		t.Fatal("connector config must receive runtime credentials, not literal placeholders")
	}
	if _, exists := cfg["name"]; exists {
		t.Fatal("PUT connector config must not duplicate the connector name field")
	}
}

func TestOutboxPredicateMatchesOnlyRawDebeziumTopic(t *testing.T) {
	cfg := BuildConnectorConfig(config.KafkaConfig{}, SourceDatabaseConfig{
		Host: "db", Port: 5432, Database: "exchange", User: "cdc", SSLMode: "disable",
	})
	pattern := cfg["predicates.IsOutboxTable.pattern"]
	if pattern != `^goexchange\.cdc\.public\.outbox_events$` {
		t.Fatalf("predicate=%q", pattern)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.MatchString("goexchange.cdc.public.outbox_events") {
		t.Fatalf("predicate %q does not match raw Debezium topic", pattern)
	}
	for _, topic := range []string{
		"goexchangeXcdcXpublicXoutbox_events",
		"goexchange.cdc.public.posts",
		"goexchange.cdc.public.posts",
		"goexchange.activity.events.v1",
		"__debezium-heartbeat.goexchange.cdc",
	} {
		if compiled.MatchString(topic) {
			t.Fatalf("predicate %q unexpectedly matched %q", pattern, topic)
		}
	}
}

func TestSourceTopicPatternEscapesEachTopicComponent(t *testing.T) {
	pattern := sourceTopicPattern("goexchange.cdc.acceptance.abc123", "public", "outbox_events_cdc_a_abc123")
	want := `^goexchange\.cdc\.acceptance\.abc123\.public\.outbox_events_cdc_a_abc123$`
	if pattern != want {
		t.Fatalf("pattern=%q want=%q", pattern, want)
	}
}

func TestConnectorIdentityValidation(t *testing.T) {
	identity := ProductionConnectorIdentity()
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	identity.TableName = "outbox-events"
	if err := identity.Validate(); err == nil {
		t.Fatal("invalid PostgreSQL table identifier unexpectedly accepted")
	}
}

func TestParseSourceDatabaseConfigUsesDSNIdentityAndCDCCredentials(t *testing.T) {
	source, err := ParseSourceDatabaseConfig("postgres://app-user:app-password@source-db:6543/exchange?sslmode=disable", EnvironmentConfig{
		User: "cdc-user", Password: "cdc-password", SSLMode: "require",
		SSLCert: "/cert/client.crt", SSLKey: "/cert/client.key", SSLRootCert: "/cert/ca.crt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.Host != "source-db" || source.Port != 6543 || source.Database != "exchange" {
		t.Fatalf("source identity=%+v", source)
	}
	if source.User != "cdc-user" || source.Password != "cdc-password" || source.SSLMode != "require" {
		t.Fatalf("source credentials/tls=%+v", source)
	}
	if source.SSLCert == "" || source.SSLKey == "" || source.SSLRootCert == "" {
		t.Fatalf("source certificate paths=%+v", source)
	}
}

func TestParseSourceDatabaseConfigFallsBackToDSNCredentials(t *testing.T) {
	source, err := ParseSourceDatabaseConfig("postgres://app-user:app-password@source-db:6543/exchange?sslmode=disable", EnvironmentConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if source.Host != "source-db" || source.Port != 6543 || source.Database != "exchange" {
		t.Fatalf("source identity=%+v", source)
	}
	if source.User != "app-user" || source.Password != "app-password" {
		t.Fatalf("source credentials=%+v", source)
	}
}

func TestConnectorStatusDecodesNestedConnectorState(t *testing.T) {
	var status ConnectorStatus
	if err := json.Unmarshal([]byte(`{"name":"goexchange-outbox","connector":{"state":"RUNNING","trace":""},"tasks":[{"id":0,"state":"RUNNING","trace":""}]}`), &status); err != nil {
		t.Fatal(err)
	}
	if status.Name != ConnectorName || status.Connector.State != "RUNNING" || len(status.Tasks) != 1 || status.Tasks[0].State != "RUNNING" {
		t.Fatalf("status=%+v", status)
	}
}

func TestAssessConnectorReadiness(t *testing.T) {
	const expectedName = "test-connector"
	tests := []struct {
		name   string
		status ConnectorStatus
		want   connectorReadinessDecision
	}{
		{name: "running with one running task", status: testConnectorStatus(expectedName, "RUNNING", "RUNNING"), want: connectorReadinessReady},
		{name: "running with all tasks running", status: testConnectorStatus(expectedName, "RUNNING", "RUNNING", " running "), want: connectorReadinessReady},
		{name: "running with no tasks", status: testConnectorStatus(expectedName, "RUNNING"), want: connectorReadinessRetry},
		{name: "paused connector", status: testConnectorStatus(expectedName, "PAUSED", "RUNNING"), want: connectorReadinessTerminalFailure},
		{name: "stopped connector", status: testConnectorStatus(expectedName, "STOPPED"), want: connectorReadinessTerminalFailure},
		{name: "failed connector", status: testConnectorStatus(expectedName, "FAILED"), want: connectorReadinessTerminalFailure},
		{name: "unassigned connector", status: testConnectorStatus(expectedName, "UNASSIGNED"), want: connectorReadinessRetry},
		{name: "empty connector state", status: testConnectorStatus(expectedName, ""), want: connectorReadinessRetry},
		{name: "unknown connector state", status: testConnectorStatus(expectedName, "BROKEN"), want: connectorReadinessRetry},
		{name: "failed task", status: testConnectorStatus(expectedName, "RUNNING", "FAILED"), want: connectorReadinessTerminalFailure},
		{name: "paused task", status: testConnectorStatus(expectedName, "RUNNING", "PAUSED"), want: connectorReadinessTerminalFailure},
		{name: "stopped task", status: testConnectorStatus(expectedName, "RUNNING", "STOPPED"), want: connectorReadinessTerminalFailure},
		{name: "unassigned task", status: testConnectorStatus(expectedName, "RUNNING", "UNASSIGNED"), want: connectorReadinessRetry},
		{name: "empty task state", status: testConnectorStatus(expectedName, "RUNNING", ""), want: connectorReadinessRetry},
		{name: "unknown task state", status: testConnectorStatus(expectedName, "RUNNING", "BROKEN"), want: connectorReadinessRetry},
		{name: "empty connector name", status: testConnectorStatus("", "RUNNING", "RUNNING"), want: connectorReadinessTerminalFailure},
		{name: "unexpected connector name", status: testConnectorStatus("other-connector", "RUNNING", "RUNNING"), want: connectorReadinessTerminalFailure},
		{name: "mixed case and whitespace", status: testConnectorStatus(expectedName, " rUnNiNg ", " rUnNiNg "), want: connectorReadinessReady},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := assessConnectorReadiness(expectedName, test.status); got != test.want {
				t.Fatalf("readiness=%v want=%v status=%s", got, test.want, connectorStatusSummary(test.status))
			}
		})
	}
}

func TestWaitForConnectorReadyRetriesUntilAllTasksRunning(t *testing.T) {
	statuses := []ConnectorStatus{
		testConnectorStatus(ConnectorName, "UNASSIGNED"),
		testConnectorStatus(ConnectorName, "RUNNING"),
		testConnectorStatus(ConnectorName, "RUNNING", "UNASSIGNED"),
		testConnectorStatus(ConnectorName, "RUNNING", "RUNNING"),
	}
	reads := 0
	waits := 0
	status, err := waitForConnectorReadyWith(context.Background(), ConnectorName, func(context.Context) (ConnectorStatus, error) {
		current := statuses[reads]
		reads++
		return current, nil
	}, func(context.Context, time.Duration) error {
		waits++
		return nil
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if assessConnectorReadiness(ConnectorName, status) != connectorReadinessReady || reads != len(statuses) || waits != len(statuses)-1 {
		t.Fatalf("status=%s reads=%d waits=%d", connectorStatusSummary(status), reads, waits)
	}
}

func TestWaitForConnectorReadyNeverAcceptsEmptyTasks(t *testing.T) {
	statuses := []ConnectorStatus{
		testConnectorStatus(ConnectorName, "RUNNING"),
		testConnectorStatus(ConnectorName, "RUNNING"),
		testConnectorStatus(ConnectorName, "RUNNING", "RUNNING"),
	}
	reads := 0
	status, err := waitForConnectorReadyWith(context.Background(), ConnectorName, func(context.Context) (ConnectorStatus, error) {
		current := statuses[reads]
		reads++
		return current, nil
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if reads != 3 || assessConnectorReadiness(ConnectorName, status) != connectorReadinessReady {
		t.Fatalf("empty task state was accepted early: reads=%d status=%s", reads, connectorStatusSummary(status))
	}
}

func TestWaitForConnectorReadyStopsOnTerminalState(t *testing.T) {
	reads := 0
	status, err := waitForConnectorReadyWith(context.Background(), ConnectorName, func(context.Context) (ConnectorStatus, error) {
		reads++
		if reads == 1 {
			return testConnectorStatus(ConnectorName, "RUNNING"), nil
		}
		return testConnectorStatus(ConnectorName, "FAILED"), nil
	}, func(context.Context, time.Duration) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "terminal failure") {
		t.Fatalf("terminal state did not fail immediately: status=%s err=%v", connectorStatusSummary(status), err)
	}
	if reads != 2 {
		t.Fatalf("terminal state was polled after failure: reads=%d", reads)
	}
}

func TestWaitForConnectorReadyHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	_, err := waitForConnectorReadyWith(ctx, ConnectorName, func(context.Context) (ConnectorStatus, error) {
		reads++
		return testConnectorStatus(ConnectorName, "RUNNING"), nil
	}, func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation was not preserved: %v", err)
	}
	if reads != 1 {
		t.Fatalf("polling continued after cancellation: reads=%d", reads)
	}
}

func TestConnectorHTTPStatusHandling(t *testing.T) {
	t.Run("GET 200 exists and decodes", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusOK, `{"name":"test-connector","connector":{"state":"RUNNING"},"tasks":[{"id":0,"state":"RUNNING"}]}`)
		defer server.Close()
		status, err := connectorStatusForName(context.Background(), server.URL, "test-connector")
		if err != nil || assessConnectorReadiness("test-connector", status) != connectorReadinessReady {
			t.Fatalf("status=%s err=%v", connectorStatusSummary(status), err)
		}
	})

	t.Run("GET 404 means absent", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusNotFound, "not found")
		defer server.Close()
		exists, err := connectorPresenceForName(context.Background(), server.URL, "test-connector")
		if err != nil || exists {
			t.Fatalf("exists=%v err=%v", exists, err)
		}
	})

	t.Run("GET 500 is an error", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusInternalServerError, "server error")
		defer server.Close()
		exists, err := connectorPresenceForName(context.Background(), server.URL, "test-connector")
		if err == nil || exists || isConnectorNotFound(err) {
			t.Fatalf("500 was treated as absence: exists=%v err=%v", exists, err)
		}
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusOK, "{")
		defer server.Close()
		if _, err := connectorStatusForName(context.Background(), server.URL, "test-connector"); err == nil {
			t.Fatal("invalid JSON unexpectedly decoded")
		}
	})

	t.Run("PUT non-2xx is an error without config body", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusBadRequest, `{"error":"password=secret"}`)
		defer server.Close()
		sent, err := putConnectorConfig(context.Background(), server.URL, "test-connector", map[string]string{"database.password": "secret"})
		if !sent || err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("sent=%v err=%v", sent, err)
		}
	})

	t.Run("DELETE 404 is already deleted", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusNotFound, "not found")
		defer server.Close()
		if err := deleteConnectorForNameOwned(context.Background(), server.URL, "test-connector"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("response body is bounded", func(t *testing.T) {
		server := newConnectorHTTPTestServer(http.StatusOK, strings.Repeat("x", maxConnectorResponseBodyLength+1))
		defer server.Close()
		if _, err := connectorStatusForName(context.Background(), server.URL, "test-connector"); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized response was not rejected: %v", err)
		}
	})
}

func testConnectorStatus(name, connectorState string, taskStates ...string) ConnectorStatus {
	payload := map[string]interface{}{
		"name":      name,
		"connector": map[string]string{"state": connectorState},
		"tasks":     make([]map[string]interface{}, 0, len(taskStates)),
	}
	tasks := payload["tasks"].([]map[string]interface{})
	for id, state := range taskStates {
		tasks = append(tasks, map[string]interface{}{"id": id, "state": state})
	}
	payload["tasks"] = tasks
	encoded, _ := json.Marshal(payload)
	var status ConnectorStatus
	_ = json.Unmarshal(encoded, &status)
	return status
}

func newConnectorHTTPTestServer(statusCode int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(statusCode)
		_, _ = writer.Write([]byte(body))
	}))
}
