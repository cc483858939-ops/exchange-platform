package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
)

const (
	PublicationName = "goexchange_outbox_pub"
	SlotName        = "goexchange_outbox_slot"
	ConnectorName   = "goexchange-outbox"
	ConnectURL      = "http://kafka-connect:8083"
)

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

func Run(ctx context.Context, db *sql.DB, kafkaConfig config.KafkaConfig, connectURL, databaseUser, databasePassword string) (ConnectorStatus, error) {
	if db == nil {
		return ConnectorStatus{}, errors.New("database connection is nil")
	}
	if err := verifyPostgresCDCSettings(ctx, db); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ensurePublication(ctx, db); err != nil {
		return ConnectorStatus{}, err
	}
	if err := ensureReplicationSlot(ctx, db); err != nil {
		return ConnectorStatus{}, err
	}
	status, err := RegisterConnector(ctx, connectURL, BuildConnectorConfig(kafkaConfig, databaseUser, databasePassword))
	if err != nil {
		return ConnectorStatus{}, err
	}
	if strings.EqualFold(status.Connector.State, "FAILED") {
		return status, fmt.Errorf("CDC connector %s is FAILED", ConnectorName)
	}
	for _, task := range status.Tasks {
		if strings.EqualFold(task.State, "FAILED") {
			return status, fmt.Errorf("CDC connector task %d is FAILED: %s", task.ID, task.Trace)
		}
	}
	return status, nil
}

func BuildConnectorConfig(_ config.KafkaConfig, databaseUser, databasePassword string) map[string]string {
	if strings.TrimSpace(databaseUser) == "" {
		databaseUser = "postgres"
	}
	return map[string]string{
		"connector.class":                               "io.debezium.connector.postgresql.PostgresConnector",
		"plugin.name":                                   "pgoutput",
		"database.hostname":                             "db",
		"database.port":                                 "5432",
		"database.dbname":                               "test",
		"database.user":                                 databaseUser,
		"database.password":                             databasePassword,
		"topic.prefix":                                  "goexchange.cdc",
		"slot.name":                                     SlotName,
		"publication.name":                              PublicationName,
		"publication.autocreate.mode":                   "disabled",
		"table.include.list":                            "public.outbox_events",
		"snapshot.mode":                                 "initial",
		"heartbeat.interval.ms":                         "10000",
		"slot.drop.on.stop":                             "false",
		"tombstones.on.delete":                          "false",
		"transforms":                                    "outbox",
		"transforms.outbox.type":                        "io.debezium.transforms.outbox.EventRouter",
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
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", PublicationName).Scan(&exists); err != nil {
		return fmt.Errorf("check CDC publication: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, "CREATE PUBLICATION "+PublicationName+" FOR TABLE public.outbox_events"); err != nil {
			return fmt.Errorf("create CDC publication: %w", err)
		}
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_publication_tables WHERE pubname = $1 AND schemaname = 'public' AND tablename = 'outbox_events'", PublicationName).Scan(&tableCount); err != nil {
		return fmt.Errorf("verify CDC publication table: %w", err)
	}
	var publishedCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_publication_tables WHERE pubname = $1", PublicationName).Scan(&publishedCount); err != nil {
		return fmt.Errorf("verify CDC publication set: %w", err)
	}
	if tableCount != 1 || publishedCount != 1 {
		return fmt.Errorf("CDC publication %s must contain only public.outbox_events", PublicationName)
	}
	return nil
}

func ensureReplicationSlot(ctx context.Context, db *sql.DB) error {
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", SlotName).Scan(&exists); err != nil {
		return fmt.Errorf("check CDC replication slot: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, "SELECT * FROM pg_create_logical_replication_slot($1, 'pgoutput')", SlotName); err != nil {
			return fmt.Errorf("create CDC replication slot: %w", err)
		}
	}
	var plugin string
	var temporary bool
	if err := db.QueryRowContext(ctx, "SELECT plugin, temporary FROM pg_replication_slots WHERE slot_name = $1", SlotName).Scan(&plugin, &temporary); err != nil {
		return fmt.Errorf("verify CDC replication slot: %w", err)
	}
	if plugin != "pgoutput" || temporary {
		return fmt.Errorf("CDC replication slot %s has plugin=%q temporary=%t", SlotName, plugin, temporary)
	}
	return nil
}

func RegisterConnector(ctx context.Context, connectURL string, connectorConfig map[string]string) (ConnectorStatus, error) {
	connectURL = strings.TrimRight(strings.TrimSpace(connectURL), "/")
	if connectURL == "" {
		connectURL = ConnectURL
	}
	body, err := json.Marshal(connectorConfig)
	if err != nil {
		return ConnectorStatus{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, connectURL+"/connectors/"+ConnectorName+"/config", strings.NewReader(string(body)))
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
	return connectorStatus(ctx, connectURL)
}

func connectorStatus(ctx context.Context, connectURL string) (ConnectorStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connectURL+"/connectors/"+ConnectorName+"/status", nil)
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

func Environment() (connectURL, databaseUser, databasePassword string) {
	connectURL = strings.TrimSpace(os.Getenv("CDC_CONNECT_URL"))
	if connectURL == "" {
		connectURL = ConnectURL
	}
	databaseUser = strings.TrimSpace(os.Getenv("CDC_DATABASE_USER"))
	databasePassword = os.Getenv("CDC_DATABASE_PASSWORD")
	return
}
