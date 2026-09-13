package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

const (
	connectorReadyTimeout          = 60 * time.Second
	connectorPollInterval          = time.Second
	connectorRequestTimeout        = 15 * time.Second
	connectorCompensationTimeout   = 30 * time.Second
	connectorSlotPollInterval      = 500 * time.Millisecond
	maxConnectorResponseBodyLength = 4096
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

type connectorReadinessDecision uint8

const (
	connectorReadinessRetry connectorReadinessDecision = iota
	connectorReadinessReady
	connectorReadinessTerminalFailure
)

func assessConnectorReadiness(expectedName string, status ConnectorStatus) connectorReadinessDecision {
	if strings.TrimSpace(expectedName) == "" || status.Name != expectedName {
		return connectorReadinessTerminalFailure
	}
	switch normalizeConnectorState(status.Connector.State) {
	case "RUNNING":
		if len(status.Tasks) == 0 {
			return connectorReadinessRetry
		}
		for _, task := range status.Tasks {
			if normalizeConnectorState(task.State) != "RUNNING" {
				switch normalizeConnectorState(task.State) {
				case "FAILED", "PAUSED", "STOPPED":
					return connectorReadinessTerminalFailure
				default:
					return connectorReadinessRetry
				}
			}
		}
		return connectorReadinessReady
	case "FAILED", "PAUSED", "STOPPED":
		return connectorReadinessTerminalFailure
	default:
		return connectorReadinessRetry
	}
}

func normalizeConnectorState(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func connectorStatusSummary(status ConnectorStatus) string {
	taskStates := make([]string, 0, len(status.Tasks))
	for _, task := range status.Tasks {
		taskStates = append(taskStates, fmt.Sprintf("%d:%s", task.ID, normalizeConnectorState(task.State)))
	}
	return fmt.Sprintf("name=%q connector=%s tasks=[%s]", status.Name, normalizeConnectorState(status.Connector.State), strings.Join(taskStates, ","))
}

type connectorHTTPError struct {
	operation  string
	statusCode int
	status     string
}

func (e *connectorHTTPError) Error() string {
	if e == nil {
		return "Kafka Connect request failed"
	}
	return fmt.Sprintf("%s returned %s", e.operation, e.status)
}

type connectorStatusDecodeError struct{ err error }

func (e *connectorStatusDecodeError) Error() string {
	if e == nil || e.err == nil {
		return "decode Kafka Connect status failed"
	}
	return fmt.Sprintf("decode Kafka Connect status: %v", e.err)
}

func (e *connectorStatusDecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type connectorStatusReader func(context.Context) (ConnectorStatus, error)
type connectorPollWaiter func(context.Context, time.Duration) error

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
	connectorExistedBefore, err := connectorPresenceForName(ctx, connectURL, identity.ConnectorName)
	if err != nil {
		return ConnectorStatus{}, err
	}
	if err := ensurePublicationForIdentity(ctx, db, identity); err != nil {
		return ConnectorStatus{}, err
	}
	slotCreatedByThisRun, err := ensureReplicationSlotForIdentity(ctx, db, identity)
	if err != nil {
		return ConnectorStatus{}, compensateBootstrapFailure(err, db, connectURL, identity, connectorExistedBefore, slotCreatedByThisRun, false)
	}
	putSent, err := putConnectorConfig(ctx, connectURL, identity.ConnectorName, buildConnectorConfig(kafkaConfig, source, identity))
	if err != nil {
		return ConnectorStatus{}, compensateBootstrapFailure(err, db, connectURL, identity, connectorExistedBefore, slotCreatedByThisRun, putSent)
	}
	status, err := waitForConnectorReady(ctx, connectURL, identity.ConnectorName)
	if err != nil {
		return status, compensateBootstrapFailure(err, db, connectURL, identity, connectorExistedBefore, slotCreatedByThisRun, true)
	}
	if assessConnectorReadiness(identity.ConnectorName, status) != connectorReadinessReady {
		err := fmt.Errorf("CDC connector did not reach strict readiness: %s", connectorStatusSummary(status))
		return status, compensateBootstrapFailure(err, db, connectURL, identity, connectorExistedBefore, slotCreatedByThisRun, true)
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
	_, err := ensureReplicationSlotForIdentity(ctx, db, ProductionConnectorIdentity())
	return err
}

func ensureReplicationSlotForIdentity(ctx context.Context, db *sql.DB, identity ConnectorIdentity) (bool, error) {
	if err := identity.Validate(); err != nil {
		return false, err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", identity.SlotName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check CDC replication slot: %w", err)
	}
	createdByThisRun := false
	if !exists {
		if _, err := db.ExecContext(ctx, "SELECT * FROM pg_create_logical_replication_slot($1, 'pgoutput')", identity.SlotName); err != nil {
			return false, fmt.Errorf("create CDC replication slot: %w", err)
		}
		createdByThisRun = true
	}
	var plugin string
	var temporary bool
	if err := db.QueryRowContext(ctx, "SELECT plugin, temporary FROM pg_replication_slots WHERE slot_name = $1", identity.SlotName).Scan(&plugin, &temporary); err != nil {
		return createdByThisRun, fmt.Errorf("verify CDC replication slot: %w", err)
	}
	if plugin != "pgoutput" || temporary {
		return createdByThisRun, fmt.Errorf("CDC replication slot %s has plugin=%q temporary=%t", identity.SlotName, plugin, temporary)
	}
	return createdByThisRun, nil
}

func RegisterConnector(ctx context.Context, connectURL string, connectorConfig map[string]string) (ConnectorStatus, error) {
	return registerConnector(ctx, connectURL, ProductionConnectorIdentity().ConnectorName, connectorConfig)
}

func registerConnector(ctx context.Context, connectURL, connectorName string, connectorConfig map[string]string) (ConnectorStatus, error) {
	_, err := putConnectorConfig(ctx, connectURL, connectorName, connectorConfig)
	if err != nil {
		return ConnectorStatus{}, err
	}
	return waitForConnectorReady(ctx, connectURL, connectorName)
}

func connectorStatus(ctx context.Context, connectURL string) (ConnectorStatus, error) {
	return connectorStatusForName(ctx, connectURL, ProductionConnectorIdentity().ConnectorName)
}

func connectorPresenceForName(ctx context.Context, connectURL, connectorName string) (bool, error) {
	_, err := connectorStatusForName(ctx, connectURL, connectorName)
	if err == nil {
		return true, nil
	}
	if isConnectorNotFound(err) {
		return false, nil
	}
	return false, err
}

func connectorStatusForName(ctx context.Context, connectURL, connectorName string) (ConnectorStatus, error) {
	if len(connectorName) > 249 || !connectorNamePattern.MatchString(connectorName) {
		return ConnectorStatus{}, fmt.Errorf("invalid CDC connector name %q", connectorName)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connectorEndpoint(connectURL, connectorName, "status"), nil)
	if err != nil {
		return ConnectorStatus{}, err
	}
	response, err := (&http.Client{Timeout: connectorRequestTimeout}).Do(request)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("read CDC connector status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return ConnectorStatus{}, &connectorHTTPError{operation: "GET connector status", statusCode: response.StatusCode, status: response.Status}
	}
	payload, err := readConnectorResponseBody(response.Body)
	if err != nil {
		return ConnectorStatus{}, fmt.Errorf("read CDC connector status body: %w", err)
	}
	var status ConnectorStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return ConnectorStatus{}, &connectorStatusDecodeError{err: err}
	}
	return status, nil
}

func putConnectorConfig(ctx context.Context, connectURL, connectorName string, connectorConfig map[string]string) (bool, error) {
	if len(connectorName) > 249 || !connectorNamePattern.MatchString(connectorName) {
		return false, fmt.Errorf("invalid CDC connector name %q", connectorName)
	}
	body, err := json.Marshal(connectorConfig)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, connectorEndpoint(connectURL, connectorName, "config"), strings.NewReader(string(body)))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: connectorRequestTimeout}).Do(request)
	if err != nil {
		return true, fmt.Errorf("register CDC connector: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return true, &connectorHTTPError{operation: "PUT connector config", statusCode: response.StatusCode, status: response.Status}
	}
	return true, nil
}

func waitForConnectorReady(ctx context.Context, connectURL, expectedName string) (ConnectorStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline, cancel := context.WithTimeout(ctx, connectorReadyTimeout)
	defer cancel()
	return waitForConnectorReadyWith(deadline, expectedName, func(readCtx context.Context) (ConnectorStatus, error) {
		return connectorStatusForName(readCtx, connectURL, expectedName)
	}, waitForNextConnectorPoll)
}

func waitForConnectorReadyWith(ctx context.Context, expectedName string, read connectorStatusReader, wait connectorPollWaiter) (ConnectorStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if read == nil {
		return ConnectorStatus{}, errors.New("CDC connector readiness status reader is nil")
	}
	if wait == nil {
		wait = waitForNextConnectorPoll
	}
	var lastStatus ConnectorStatus
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return lastStatus, connectorReadinessWaitError(expectedName, lastStatus, lastErr, err)
		}
		status, err := read(ctx)
		if err == nil {
			lastStatus = status
			switch assessConnectorReadiness(expectedName, status) {
			case connectorReadinessReady:
				return status, nil
			case connectorReadinessTerminalFailure:
				return status, fmt.Errorf("CDC connector readiness terminal failure: %s", connectorStatusSummary(status))
			}
			lastErr = nil
		} else {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return lastStatus, connectorReadinessWaitError(expectedName, lastStatus, lastErr, err)
			}
			if !retryableConnectorStatusError(err) {
				return lastStatus, fmt.Errorf("CDC connector readiness status failed: %w", err)
			}
			lastErr = err
		}
		if err := wait(ctx, connectorPollInterval); err != nil {
			return lastStatus, connectorReadinessWaitError(expectedName, lastStatus, lastErr, err)
		}
	}
}

func connectorReadinessWaitError(expectedName string, lastStatus ConnectorStatus, lastErr, cause error) error {
	last := "none"
	if lastStatus.Name != "" || lastStatus.Connector.State != "" || len(lastStatus.Tasks) > 0 {
		last = connectorStatusSummary(lastStatus)
	}
	if lastErr != nil {
		return fmt.Errorf("CDC connector %s did not become ready: last_status=%s last_error=%v: %w", expectedName, last, lastErr, cause)
	}
	return fmt.Errorf("CDC connector %s did not become ready: last_status=%s: %w", expectedName, last, cause)
}

func retryableConnectorStatusError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *connectorHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.statusCode == http.StatusNotFound || httpErr.statusCode >= 500
	}
	var decodeErr *connectorStatusDecodeError
	if errors.As(err, &decodeErr) {
		return false
	}
	return true
}

func isConnectorNotFound(err error) bool {
	var httpErr *connectorHTTPError
	return errors.As(err, &httpErr) && httpErr.statusCode == http.StatusNotFound
}

func waitForNextConnectorPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func connectorEndpoint(connectURL, connectorName, suffix string) string {
	base := strings.TrimRight(strings.TrimSpace(connectURL), "/")
	if base == "" {
		base = ConnectURL
	}
	endpoint := base + "/connectors/" + url.PathEscape(connectorName)
	if suffix != "" {
		endpoint += "/" + suffix
	}
	return endpoint
}

func readConnectorResponseBody(reader io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxConnectorResponseBodyLength+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxConnectorResponseBodyLength {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxConnectorResponseBodyLength)
	}
	return payload, nil
}

func deleteConnectorForNameOwned(ctx context.Context, connectURL, connectorName string) error {
	if len(connectorName) > 249 || !connectorNamePattern.MatchString(connectorName) {
		return fmt.Errorf("invalid CDC connector name %q", connectorName)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, connectorEndpoint(connectURL, connectorName, ""), nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: connectorRequestTimeout}).Do(request)
	if err != nil {
		return fmt.Errorf("delete CDC connector: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 == 2 || response.StatusCode == http.StatusNotFound {
		return nil
	}
	return &connectorHTTPError{operation: "DELETE connector", statusCode: response.StatusCode, status: response.Status}
}

func waitForConnectorDeletedOwned(ctx context.Context, connectURL, connectorName string) error {
	var lastErr error
	for {
		_, err := connectorStatusForName(ctx, connectURL, connectorName)
		if err == nil {
			lastErr = fmt.Errorf("connector %s still exists", connectorName)
		} else if isConnectorNotFound(err) {
			return nil
		} else if !retryableConnectorStatusError(err) {
			return err
		} else {
			lastErr = err
		}
		if err := waitForNextConnectorPoll(ctx, connectorPollInterval); err != nil {
			if lastErr != nil {
				return fmt.Errorf("connector %s was not deleted: %w: %v", connectorName, err, lastErr)
			}
			return fmt.Errorf("connector %s was not deleted: %w", connectorName, err)
		}
	}
}

func waitForReplicationSlotInactive(ctx context.Context, db *sql.DB, slotName string) error {
	if db == nil {
		return errors.New("database connection is nil")
	}
	if len(slotName) == 0 || len(slotName) > 63 || !postgresIdentifierPattern.MatchString(slotName) {
		return fmt.Errorf("invalid replication slot name %q", slotName)
	}
	var lastErr error
	for {
		var active bool
		err := db.QueryRowContext(ctx, "SELECT active FROM pg_replication_slots WHERE slot_name = $1", slotName).Scan(&active)
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
		if err := waitForNextConnectorPoll(ctx, connectorSlotPollInterval); err != nil {
			if lastErr != nil {
				return fmt.Errorf("replication slot %s did not become inactive: %w: %v", slotName, err, lastErr)
			}
			return fmt.Errorf("replication slot %s did not become inactive: %w", slotName, err)
		}
	}
}

func cleanupOwnedReplicationSlot(ctx context.Context, db *sql.DB, identity ConnectorIdentity, slotCreatedByThisRun bool) error {
	if !slotCreatedByThisRun {
		return errors.New("CDC compensation refused to delete a slot not created by this run")
	}
	if err := waitForReplicationSlotInactive(ctx, db, identity.SlotName); err != nil {
		return err
	}
	var active bool
	err := db.QueryRowContext(ctx, "SELECT active FROM pg_replication_slots WHERE slot_name = $1", identity.SlotName).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recheck CDC replication slot ownership target: %w", err)
	}
	if active {
		return fmt.Errorf("CDC replication slot %s became active before drop", identity.SlotName)
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_drop_replication_slot($1)", identity.SlotName); err != nil {
		return fmt.Errorf("drop CDC replication slot: %w", err)
	}
	return nil
}

func compensateBootstrapFailure(original error, db *sql.DB, connectURL string, identity ConnectorIdentity, connectorExistedBefore, slotCreatedByThisRun, putSent bool) error {
	if original == nil || !slotCreatedByThisRun {
		return original
	}
	compensationContext, cancel := context.WithTimeout(context.Background(), connectorCompensationTimeout)
	defer cancel()
	if connectorExistedBefore {
		return errors.Join(original, fmt.Errorf("CDC compensation: pre-existing Connector %q; resources retained", identity.ConnectorName))
	}
	if !putSent {
		return joinCompensationError(original, cleanupOwnedReplicationSlot(compensationContext, db, identity, true))
	}
	exists, err := connectorPresenceForName(compensationContext, connectURL, identity.ConnectorName)
	if err != nil {
		return errors.Join(original, fmt.Errorf("CDC compensation: Connector state unknown; replication slot %q retained: %w", identity.SlotName, err))
	}
	if exists {
		if err := deleteConnectorForNameOwned(compensationContext, connectURL, identity.ConnectorName); err != nil {
			return joinCompensationError(original, fmt.Errorf("delete Connector during CDC compensation: %w", err))
		}
		if err := waitForConnectorDeletedOwned(compensationContext, connectURL, identity.ConnectorName); err != nil {
			return joinCompensationError(original, fmt.Errorf("wait for Connector deletion during CDC compensation: %w", err))
		}
	}
	return joinCompensationError(original, cleanupOwnedReplicationSlot(compensationContext, db, identity, true))
}

func joinCompensationError(original, compensation error) error {
	if compensation == nil {
		return original
	}
	return errors.Join(original, fmt.Errorf("CDC compensation failed: %w", compensation))
}
