package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Execute the real GORM collector queries and capture their frequency without
// requiring an external PostgreSQL service. This does not measure query plans.
type pipelineMetricsConnector struct {
	query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

func (c pipelineMetricsConnector) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c pipelineMetricsConnector) Driver() driver.Driver                        { return pipelineMetricsDriver{} }
func (c pipelineMetricsConnector) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, query, args)
}
func (pipelineMetricsConnector) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (pipelineMetricsConnector) Close() error { return nil }
func (pipelineMetricsConnector) Begin() (driver.Tx, error) {
	return nil, errors.New("metrics must not start a transaction")
}

type pipelineMetricsDriver struct{}

func (pipelineMetricsDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected open")
}

type pipelineMetricsRows struct {
	columns []string
	values  []driver.Value
}

func (r *pipelineMetricsRows) Columns() []string { return r.columns }
func (r *pipelineMetricsRows) Close() error      { return nil }
func (r *pipelineMetricsRows) Next(dest []driver.Value) error {
	if r.values == nil {
		return io.EOF
	}
	copy(dest, r.values)
	r.values = nil
	return nil
}

func pipelineMetricsDB(t *testing.T, query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)) {
	t.Helper()
	connection := sql.OpenDB(pipelineMetricsConnector{query: query})
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	originalDB, originalRedis, originalConfig := global.WorkerDb, global.RedisDB, config.AppConfig
	global.WorkerDb, global.RedisDB = db, nil
	config.AppConfig = nil
	t.Cleanup(func() {
		global.WorkerDb, global.RedisDB, config.AppConfig = originalDB, originalRedis, originalConfig
		_ = connection.Close()
	})
}

func pipelineMetricValue(t *testing.T, name string) float64 {
	t.Helper()
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if strings.HasPrefix(line, name+" ") {
			value, err := strconv.ParseFloat(strings.TrimPrefix(line, name+" "), 64)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
	}
	t.Fatalf("metric %s missing", name)
	return 0
}

func TestPipelineHealthSamplesDoNotRepeatHistoricalCounts(t *testing.T) {
	counts := map[string]int{}
	consumer := "goexchange-notification-projection-v1"
	pipelineMetricsDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "count(*)") && strings.Contains(query, "outbox_events"):
			counts["outbox"]++
			return &pipelineMetricsRows{[]string{"count"}, []driver.Value{int64(11)}}, nil
		case strings.Contains(query, "consumer_inboxes"):
			if len(args) != 1 || args[0].Value != consumer {
				t.Fatalf("unscoped Inbox count: %s %v", query, args)
			}
			counts["inbox"]++
			return &pipelineMetricsRows{[]string{"count"}, []driver.Value{int64(13)}}, nil
		case strings.Contains(query, "MIN(created_at)"):
			counts["oldest"]++
			return &pipelineMetricsRows{[]string{"created_at"}, []driver.Value{time.Now().Add(-time.Minute)}}, nil
		case strings.Contains(query, "pg_replication_slots"):
			counts["cdc"]++
			return &pipelineMetricsRows{[]string{"active", "confirmed_lsn", "wal_lag_bytes"}, []driver.Value{true, float64(50), float64(7)}}, nil
		case strings.Contains(query, "user_reco_profile_dirty"):
			counts["dirty"]++
			return &pipelineMetricsRows{[]string{"count"}, []driver.Value{int64(4)}}, nil
		default:
			t.Fatalf("unexpected or mutating metrics SQL: %s", query)
			return nil, errors.New("unexpected query")
		}
	})
	refreshPipelineRetainedMetrics(context.Background())
	for i := 0; i < 30; i++ {
		refreshPipelineMetrics(context.Background())
	}
	if counts["outbox"] != 1 || counts["inbox"] != 1 || counts["cdc"] != 30 || counts["oldest"] != 30 || counts["dirty"] != 30 {
		t.Fatalf("fast samples repeated retained counts or skipped health queries: %v", counts)
	}
	if pipelineMetricValue(t, "go_exchange_outbox_cdc_wal_lag_bytes") != 7 || pipelineMetricValue(t, "go_exchange_recommendation_profile_dirty_queue_depth") != 4 {
		t.Fatal("live health values not published")
	}
	if pipelineMetricValue(t, "go_exchange_outbox_rows_last_success_timestamp_seconds") <= 0 {
		t.Fatal("successful sample freshness missing")
	}
	refreshPipelineRetainedMetrics(context.Background())
	if counts["outbox"] != 2 || counts["inbox"] != 2 {
		t.Fatalf("next retained sample not refreshed: %v", counts)
	}
}

func TestPipelineCountFailuresRetainPreviousValuesAndFreshness(t *testing.T) {
	for _, failedTable := range []string{"outbox_events", "consumer_inboxes"} {
		t.Run(failedTable, func(t *testing.T) {
			consumer := "goexchange-notification-projection-v1"
			metrics.SetOutboxRowsTotal(77)
			metrics.SetOutboxRowsSampleSuccess(time.Unix(100, 0))
			metrics.SetConsumerInboxRows(consumer, 88)
			metrics.SetConsumerInboxRowsSampleSuccess(consumer, time.Unix(200, 0))
			pipelineMetricsDB(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(query, failedTable) {
					return nil, errors.New("temporary count failure")
				}
				return &pipelineMetricsRows{[]string{"count"}, []driver.Value{int64(9)}}, nil
			})
			refreshPipelineRetainedMetrics(context.Background())
			inboxValue := `go_exchange_consumer_inbox_rows_total{consumer="` + consumer + `"}`
			inboxTime := `go_exchange_consumer_inbox_rows_last_success_timestamp_seconds{consumer="` + consumer + `"}`
			if failedTable == "outbox_events" {
				if pipelineMetricValue(t, "go_exchange_outbox_rows_total") != 77 || pipelineMetricValue(t, "go_exchange_outbox_rows_last_success_timestamp_seconds") != 100 {
					t.Fatal("failed count replaced last successful sample")
				}
				if pipelineMetricValue(t, inboxValue) != 9 || pipelineMetricValue(t, inboxTime) <= 200 {
					t.Fatal("independent successful Inbox sample discarded")
				}
			} else {
				if pipelineMetricValue(t, inboxValue) != 88 || pipelineMetricValue(t, inboxTime) != 200 {
					t.Fatal("failed Inbox count replaced last successful sample")
				}
				if pipelineMetricValue(t, "go_exchange_outbox_rows_total") != 9 || pipelineMetricValue(t, "go_exchange_outbox_rows_last_success_timestamp_seconds") <= 100 {
					t.Fatal("successful Outbox sample discarded")
				}
			}
		})
	}
}

func TestOutboxAgeFailurePreservesSampleAndEmptySuccessClearsIt(t *testing.T) {
	mode := "old"
	pipelineMetricsDB(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "MIN(created_at)") {
			if mode == "failure" {
				return nil, context.DeadlineExceeded
			}
			var value driver.Value
			if mode == "old" {
				value = time.Now().Add(-48 * time.Hour)
			}
			return &pipelineMetricsRows{[]string{"created_at"}, []driver.Value{value}}, nil
		}
		if strings.Contains(query, "pg_replication_slots") {
			return &pipelineMetricsRows{[]string{"active"}, []driver.Value{true}}, nil
		}
		return &pipelineMetricsRows{[]string{"count"}, []driver.Value{int64(0)}}, nil
	})
	refreshPipelineMetrics(context.Background())
	age := pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_seconds")
	stamp := pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_last_success_timestamp_seconds")
	failures := pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_sample_failures_total")
	if age < 172799 || stamp <= 0 {
		t.Fatalf("age=%v stamp=%v", age, stamp)
	}
	mode = "failure"
	refreshPipelineMetrics(context.Background())
	if pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_seconds") != age || pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_last_success_timestamp_seconds") != stamp || pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_sample_valid") != 0 || pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_sample_failures_total") != failures+1 {
		t.Fatal("failed sample was exported as cleared/fresh")
	}
	mode = "empty"
	refreshPipelineMetrics(context.Background())
	if pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_seconds") != 0 || pipelineMetricValue(t, "go_exchange_outbox_oldest_row_age_sample_valid") != 1 {
		t.Fatal("successful empty sample not cleared")
	}
}

func TestPipelineSlowCountDoesNotBlockInitialHealthSampleAndStopsOnCancel(t *testing.T) {
	started, health := make(chan struct{}), make(chan struct{})
	var slowOnce, healthOnce sync.Once
	pipelineMetricsDB(t, func(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "count(*)") && strings.Contains(query, "outbox_events") {
			slowOnce.Do(func() { close(started) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if strings.Contains(query, "pg_replication_slots") {
			healthOnce.Do(func() { close(health) })
		}
		return &pipelineMetricsRows{columns: []string{"count"}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	startPipelineMetrics(ctx, &wg)
	for _, signal := range []<-chan struct{}{started, health} {
		select {
		case <-signal:
		case <-time.After(2 * time.Second):
			t.Fatal("startup health sample blocked by historical COUNT")
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("metrics workers did not stop")
	}
}

func TestPipelineSamplerHasFreshBudgetsAndNeverOverlaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	var active atomic.Int32
	results := make(chan error, 10)
	startPipelineMetricsSampler(ctx, &wg, time.Millisecond, 15*time.Millisecond, func(sampleCtx context.Context) {
		if active.Add(1) != 1 {
			t.Error("sample overlapped")
		}
		defer active.Add(-1)
		if _, ok := sampleCtx.Deadline(); !ok {
			t.Error("sample has no deadline")
		}
		<-sampleCtx.Done()
		results <- sampleCtx.Err()
	})
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("sample budget error=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("sample budget was not enforced")
		}
	}
	cancel()
	wg.Wait()
}

func TestPipelineCancelledStartupDoesNotQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	startPipelineMetricsSampler(ctx, &wg, time.Hour, time.Second, func(context.Context) { t.Error("cancelled sampler executed") })
	wg.Wait()
}
