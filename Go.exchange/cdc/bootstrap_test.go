package cdc

import (
	"encoding/json"
	"regexp"
	"testing"

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
		"goexchange.cdc.public.articles",
		"goexchange.cdc.public.comments",
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
