package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
)

const (
	ProductionTopicPrefix = "goexchange.cdc"
	ProductionSchemaName  = "public"
	ProductionTableName   = "outbox_events"
	PublicationName       = "goexchange_outbox_pub"
	SlotName              = "goexchange_outbox_slot"
	ConnectorName         = "goexchange-outbox"
	ConnectURL            = "http://kafka-connect:8083"
)

var (
	postgresIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	connectorNamePattern      = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
)

type ConnectorIdentity struct {
	ConnectorName   string
	TopicPrefix     string
	SlotName        string
	PublicationName string
	SchemaName      string
	TableName       string
}

func ProductionConnectorIdentity() ConnectorIdentity {
	return ConnectorIdentity{
		ConnectorName:   ConnectorName,
		TopicPrefix:     ProductionTopicPrefix,
		SlotName:        SlotName,
		PublicationName: PublicationName,
		SchemaName:      ProductionSchemaName,
		TableName:       ProductionTableName,
	}
}

func (identity ConnectorIdentity) Validate() error {
	for name, value := range map[string]string{
		"slot":        identity.SlotName,
		"publication": identity.PublicationName,
		"schema":      identity.SchemaName,
		"table":       identity.TableName,
	} {
		if len(value) > 63 || !postgresIdentifierPattern.MatchString(value) {
			return fmt.Errorf("invalid CDC %s identifier %q", name, value)
		}
	}
	if len(identity.ConnectorName) > 249 || !connectorNamePattern.MatchString(identity.ConnectorName) {
		return fmt.Errorf("invalid CDC connector name %q", identity.ConnectorName)
	}
	if len(identity.TopicPrefix) == 0 || len(identity.TopicPrefix) > 249 || strings.TrimSpace(identity.TopicPrefix) != identity.TopicPrefix || strings.ContainsAny(identity.TopicPrefix, " \t\r\n") {
		return fmt.Errorf("invalid CDC topic prefix %q", identity.TopicPrefix)
	}
	return nil
}

func sourceTopicPattern(topicPrefix, schema, table string) string {
	return "^" + regexp.QuoteMeta(strings.Join([]string{topicPrefix, schema, table}, ".")) + "$"
}

func quotedIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func tableIncludeList(identity ConnectorIdentity) string {
	return identity.SchemaName + "." + identity.TableName
}

type ConnectorStatus struct {
	Name      string `json:"name"`
	Connector struct {
		State string `json:"state"`
		Trace string `json:"trace"`
	} `json:"connector"`
	Tasks []struct {
		ID    int    `json:"id"`
		State string `json:"state"`
		Trace string `json:"trace"`
	} `json:"tasks"`
}

func Run(ctx context.Context, db *sql.DB, kafkaConfig config.KafkaConfig, connectURL string, source SourceDatabaseConfig) (ConnectorStatus, error) {
	return runWithIdentity(ctx, db, kafkaConfig, connectURL, source, ProductionConnectorIdentity())
}

func runWithIdentity(ctx context.Context, db *sql.DB, kafkaConfig config.KafkaConfig, connectURL string, source SourceDatabaseConfig, identity ConnectorIdentity) (ConnectorStatus, error) {
	if db == nil {
		return ConnectorStatus{}, errors.New("database connection is nil")
	}
	if err := identity.Validate(); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ValidateSourceDatabaseConfig(source); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ValidateCurrentDatabase(ctx, db, source); err != nil {
		return ConnectorStatus{}, err
	}
	if err := verifyPostgresCDCSettings(ctx, db); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ensurePublicationForIdentity(ctx, db, identity); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ensureReplicationSlotForIdentity(ctx, db, identity); err != nil {
		return ConnectorStatus{}, err
	}
	status, err := registerConnector(ctx, connectURL, identity.ConnectorName, buildConnectorConfig(kafkaConfig, source, identity))
	if err != nil {
		return ConnectorStatus{}, err
	}
	if strings.EqualFold(status.Connector.State, "FAILED") {
		return status, fmt.Errorf("CDC connector %s is FAILED", identity.ConnectorName)
	}
	for _, task := range status.Tasks {
		if strings.EqualFold(task.State, "FAILED") {
			return status, fmt.Errorf("CDC connector task %d is FAILED: %s", task.ID, task.Trace)
		}
	}
	return status, nil
}

func BuildConnectorConfig(kafkaConfig config.KafkaConfig, source SourceDatabaseConfig) map[string]string {
	return buildConnectorConfig(kafkaConfig, source, ProductionConnectorIdentity())
}

func buildConnectorConfig(_ config.KafkaConfig, source SourceDatabaseConfig, identity ConnectorIdentity) map[string]string {
	result := map[string]string{
		"connector.class":                               "io.debezium.connector.postgresql.PostgresConnector",
		"plugin.name":                                   "pgoutput",
		"database.hostname":                             source.Host,
		"database.port":                                 strconv.FormatUint(uint64(source.Port), 10),
		"database.dbname":                               source.Database,
		"database.user":                                 source.User,
		"database.password":                             source.Password,
		"database.sslmode":                              source.SSLMode,
		"topic.prefix":                                  identity.TopicPrefix,
		"slot.name":                                     identity.SlotName,
		"publication.name":                              identity.PublicationName,
		"publication.autocreate.mode":                   "disabled",
		"table.include.list":                            tableIncludeList(identity),
		"snapshot.mode":                                 "initial",
		"heartbeat.interval.ms":                         "10000",
		"slot.drop.on.stop":                             "false",
		"tombstones.on.delete":                          "false",
		"transforms":                                    "outbox",
		"transforms.outbox.type":                        "io.debezium.transforms.outbox.EventRouter",
		"predicates":                                    "IsOutboxTable",
		"predicates.IsOutboxTable.type":                 "org.apache.kafka.connect.transforms.predicates.TopicNameMatches",
		"predicates.IsOutboxTable.pattern":              sourceTopicPattern(identity.TopicPrefix, identity.SchemaName, identity.TableName),
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
		"errors.tolerance":                              "none",
		"key.converter":                                 "org.apache.kafka.connect.storage.StringConverter",
		"value.converter":                               "org.apache.kafka.connect.json.JsonConverter",
		"value.converter.schemas.enable":                "false",
	}
	if source.SSLCert != "" {
		result["database.sslcert"] = source.SSLCert
	}
	if source.SSLKey != "" {
		result["database.sslkey"] = source.SSLKey
	}
	if source.SSLRootCert != "" {
		result["database.sslrootcert"] = source.SSLRootCert
	}
	return result
}

func verifyPostgresCDCSettings(ctx context.Context, db *sql.DB) error {
	var walLevel string
	if err := db.QueryRowContext(ctx, "SHOW wal_level").Scan(&walLevel); err != nil {
		return fmt.Errorf("read wal_level: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(walLevel), "logical") {
		return fmt.Errorf("wal_level=%q; want logical", walLevel)
	}
	for _, setting := range []string{"max_replication_slots", "max_wal_senders"} {
		var raw string
		if err := db.QueryRowContext(ctx, "SHOW "+setting).Scan(&raw); err != nil {
			return fmt.Errorf("read %s: %w", setting, err)
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 2 {
			return fmt.Errorf("%s=%q; want at least 2", setting, raw)
		}
	}
	return nil
}

func ensurePublication(ctx context.Context, db *sql.DB) error {
	return ensurePublicationForIdentity(ctx, db, ProductionConnectorIdentity())
}

func ensurePublicationForIdentity(ctx context.Context, db *sql.DB, identity ConnectorIdentity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", identity.PublicationName).Scan(&exists); err != nil {
		return fmt.Errorf("check CDC publication: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, "CREATE PUBLICATION "+quotedIdentifier(identity.PublicationName)+" FOR TABLE "+quotedIdentifier(identity.SchemaName)+"."+quotedIdentifier(identity.TableName)); err != nil {
			return fmt.Errorf("create CDC publication: %w", err)
		}
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_publication_tables WHERE pubname = $1 AND schemaname = $2 AND tablename = $3", identity.PublicationName, identity.SchemaName, identity.TableName).Scan(&tableCount); err != nil {
		return fmt.Errorf("verify CDC publication table: %w", err)
	}
	var publishedCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_publication_tables WHERE pubname = $1", identity.PublicationName).Scan(&publishedCount); err != nil {
		return fmt.Errorf("verify CDC publication set: %w", err)
	}
	if tableCount != 1 || publishedCount != 1 {
		return fmt.Errorf("CDC publication %s must contain only %s", identity.PublicationName, tableIncludeList(identity))
	}
	return nil
}

func ensureReplicationSlot(ctx context.Context, db *sql.DB) error {
	return ensureReplicationSlotForIdentity(ctx, db, ProductionConnectorIdentity())
}

func ensureReplicationSlotForIdentity(ctx context.Context, db *sql.DB, identity ConnectorIdentity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", identity.SlotName).Scan(&exists); err != nil {
		return fmt.Errorf("check CDC replication slot: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, "SELECT * FROM pg_create_logical_replication_slot($1, 'pgoutput')", identity.SlotName); err != nil {
			return fmt.Errorf("create CDC replication slot: %w", err)
		}
	}
	var plugin string
	var temporary bool
	if err := db.QueryRowContext(ctx, "SELECT plugin, temporary FROM pg_replication_slots WHERE slot_name = $1", identity.SlotName).Scan(&plugin, &temporary); err != nil {
		return fmt.Errorf("verify CDC replication slot: %w", err)
	}
	if plugin != "pgoutput" || temporary {
		return fmt.Errorf("CDC replication slot %s has plugin=%q temporary=%t", identity.SlotName, plugin, temporary)
	}
	return nil
}

func RegisterConnector(ctx context.Context, connectURL string, connectorConfig map[string]string) (ConnectorStatus, error) {
	return registerConnector(ctx, connectURL, ProductionConnectorIdentity().ConnectorName, connectorConfig)
}

func registerConnector(ctx context.Context, connectURL, connectorName string, connectorConfig map[string]string) (ConnectorStatus, error) {
	if len(connectorName) > 249 || !connectorNamePattern.MatchString(connectorName) {
		return ConnectorStatus{}, fmt.Errorf("invalid CDC connector name %q", connectorName)
	}
	connectURL = strings.TrimRight(strings.TrimSpace(connectURL), "/")
	if connectURL == "" {
		connectURL = ConnectURL
	}
	body, err := json.Marshal(connectorConfig)
	if err != nil {
		return ConnectorStatus{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, connectURL+"/connectors/"+connectorName+"/config", strings.NewReader(string(body)))
	if err != nil {
		return ConnectorStatus{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("register CDC connector: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ConnectorStatus{}, fmt.Errorf("register CDC connector returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return connectorStatusForName(ctx, connectURL, connectorName)
}

func connectorStatus(ctx context.Context, connectURL string) (ConnectorStatus, error) {
	return connectorStatusForName(ctx, connectURL, ProductionConnectorIdentity().ConnectorName)
}

func connectorStatusForName(ctx context.Context, connectURL, connectorName string) (ConnectorStatus, error) {
	if len(connectorName) > 249 || !connectorNamePattern.MatchString(connectorName) {
		return ConnectorStatus{}, fmt.Errorf("invalid CDC connector name %q", connectorName)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connectURL+"/connectors/"+connectorName+"/status", nil)
	if err != nil {
		return ConnectorStatus{}, err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("read CDC connector status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ConnectorStatus{}, fmt.Errorf("read CDC connector status returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var status ConnectorStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return ConnectorStatus{}, fmt.Errorf("decode CDC connector status: %w", err)
	}
	return status, nil
}
