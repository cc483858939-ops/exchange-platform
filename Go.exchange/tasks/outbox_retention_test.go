package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type outboxRetentionConnector struct {
	pipelineMetricsConnector
	exec func(context.Context, string, []driver.NamedValue) (driver.Result, error)
}

func (c outboxRetentionConnector) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c outboxRetentionConnector) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.exec(ctx, query, args)
}

func TestOutboxRetentionRechecksCDCGateAfterFullBatch(t *testing.T) {
	healthChecks, deletes := 0, 0
	connector := outboxRetentionConnector{
		pipelineMetricsConnector: pipelineMetricsConnector{query: func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
			if !strings.Contains(query, "FROM pg_replication_slots") {
				t.Fatalf("unexpected health query: %s", query)
			}
			healthChecks++
			active := healthChecks != 2
			return &pipelineMetricsRows{columns: []string{"active", "confirmed_flush_lsn", "wal_lag_bytes"}, values: []driver.Value{active, "0/100", int64(0)}}, nil
		}},
		exec: func(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
			if !strings.Contains(query, "DELETE FROM outbox_events") || len(args) != 2 || args[1].Value != int64(5000) {
				t.Fatalf("unexpected deletion: %s args=%v", query, args)
			}
			deletes++
			if deletes == 1 {
				return driver.RowsAffected(5000), nil
			}
			return driver.RowsAffected(0), nil
		},
	}
	setOutboxRetentionTestDB(t, connector)
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	interval := 10 * time.Minute
	rows, err := cleanupOutboxOnce(context.Background(), cutoff, 5000)
	if err != nil || rows != 5000 || outboxRetentionNextDelay(interval, 5000, rows, err) != 5*time.Second {
		t.Fatalf("full batch: rows=%d err=%v", rows, err)
	}
	rows, err = cleanupOutboxOnce(context.Background(), cutoff, 5000)
	if err == nil || rows != 0 || deletes != 1 || outboxRetentionNextDelay(interval, 5000, rows, err) != interval {
		t.Fatalf("inactive CDC must stop catchup: rows=%d err=%v deletes=%d", rows, err, deletes)
	}
	rows, err = cleanupOutboxOnce(context.Background(), cutoff, 5000)
	if err != nil || rows != 0 || healthChecks != 3 || deletes != 2 || outboxRetentionNextDelay(interval, 5000, rows, err) != interval {
		t.Fatalf("caught up: rows=%d err=%v checks=%d deletes=%d", rows, err, healthChecks, deletes)
	}
}

func TestOutboxRetentionBlocksUnsafeSlots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []driver.Value
		err    error
	}{
		{"missing", nil, nil},
		{"unconfirmed", []driver.Value{true, nil, nil}, nil},
		{"inactive", []driver.Value{false, "0/100", int64(0)}, nil},
		{"lagged", []driver.Value{true, "0/100", int64(outboxRetentionMaxWALLagBytes + 1)}, nil},
		{"query_failure", nil, errors.New("slot unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setOutboxRetentionTestDB(t, outboxRetentionConnector{
				pipelineMetricsConnector: pipelineMetricsConnector{query: func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
					return &pipelineMetricsRows{columns: []string{"active", "confirmed_flush_lsn", "wal_lag_bytes"}, values: tc.values}, tc.err
				}},
				exec: func(context.Context, string, []driver.NamedValue) (driver.Result, error) {
					t.Fatal("unsafe CDC slot must not allow deletion")
					return nil, nil
				},
			})
			rows, err := cleanupOutboxOnce(context.Background(), time.Now().UTC(), 5000)
			if err == nil || rows != 0 {
				t.Fatalf("rows=%d err=%v", rows, err)
			}
		})
	}
}

func TestOutboxRetentionDelayPreservesConfiguredInterval(t *testing.T) {
	if got := outboxRetentionNextDelay(time.Second, 5000, 5000, nil); got != time.Second {
		t.Fatalf("catchup must not lengthen configured interval: %v", got)
	}
	for _, rows := range []int64{0, 4999} {
		if got := outboxRetentionNextDelay(10*time.Minute, 5000, rows, nil); got != 10*time.Minute {
			t.Fatalf("partial batch %d must use normal interval: %v", rows, got)
		}
	}
}

func setOutboxRetentionTestDB(t *testing.T, connector outboxRetentionConnector) {
	t.Helper()
	connection := sql.OpenDB(connector)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	original := global.WorkerDb
	global.WorkerDb = db
	t.Cleanup(func() {
		global.WorkerDb = original
		_ = connection.Close()
	})
}
