package tasks

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"Go.exchange/config"
)

func TestRecommendationTraceCleanupConfig(t *testing.T) {
	tests := []struct {
		name          string
		appConfig     *config.Config
		resultDays    int
		requestDays   int
		cleanupConfig config.RecommendationTraceConfig
	}{
		{
			name:          "nil app config uses defaults",
			resultDays:    30,
			requestDays:   90,
			cleanupConfig: (config.RecommendationTraceConfig{}).Normalized(),
		},
		{
			name: "request override is preserved",
			appConfig: &config.Config{Recommendation: config.RecommendationConfig{Trace: config.RecommendationTraceConfig{
				ResultRetentionDays: 30, RequestRetentionDays: 120,
			}}},
			resultDays: 30, requestDays: 120, cleanupConfig: (config.RecommendationTraceConfig{}).Normalized(),
		},
		{
			name: "request retention is raised to result retention",
			appConfig: &config.Config{Recommendation: config.RecommendationConfig{Trace: config.RecommendationTraceConfig{
				ResultRetentionDays: 120, RequestRetentionDays: 90,
			}}},
			resultDays: 120, requestDays: 120, cleanupConfig: (config.RecommendationTraceConfig{}).Normalized(),
		},
		{
			name: "request retention floor is preserved",
			appConfig: &config.Config{Recommendation: config.RecommendationConfig{Trace: config.RecommendationTraceConfig{
				ResultRetentionDays: 30, RequestRetentionDays: 20,
			}}},
			resultDays: 30, requestDays: 90, cleanupConfig: (config.RecommendationTraceConfig{}).Normalized(),
		},
		{
			name: "custom cleanup values are preserved",
			appConfig: &config.Config{Recommendation: config.RecommendationConfig{Trace: config.RecommendationTraceConfig{
				ResultRetentionDays: 30, RequestRetentionDays: 120,
				CleanupIntervalSeconds: 300, CleanupCatchupIntervalSeconds: 30,
				CleanupResultBatchSize: 25, CleanupRequestBatchSize: 10,
				CleanupRunBudgetSeconds: 15, CleanupMaxResultRowsPerRun: 100,
				CleanupMaxRequestRowsPerRun: 20,
			}}},
			resultDays: 30, requestDays: 120,
			cleanupConfig: config.RecommendationTraceConfig{
				ResultRetentionDays: 30, RequestRetentionDays: 120,
				CleanupIntervalSeconds: 300, CleanupCatchupIntervalSeconds: 30,
				CleanupResultBatchSize: 25, CleanupRequestBatchSize: 10,
				CleanupRunBudgetSeconds: 15, CleanupMaxResultRowsPerRun: 100,
				CleanupMaxRequestRowsPerRun: 20,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalConfig := config.AppConfig
			t.Cleanup(func() { config.AppConfig = originalConfig })
			config.AppConfig = test.appConfig

			got := recommendationTraceCleanupConfig()
			if got.ResultRetentionDays != test.resultDays || got.RequestRetentionDays != test.requestDays {
				t.Fatalf("retention result=%d request=%d, want result=%d request=%d", got.ResultRetentionDays, got.RequestRetentionDays, test.resultDays, test.requestDays)
			}
			if got.CleanupIntervalSeconds != test.cleanupConfig.CleanupIntervalSeconds ||
				got.CleanupCatchupIntervalSeconds != test.cleanupConfig.CleanupCatchupIntervalSeconds ||
				got.CleanupResultBatchSize != test.cleanupConfig.CleanupResultBatchSize ||
				got.CleanupRequestBatchSize != test.cleanupConfig.CleanupRequestBatchSize ||
				got.CleanupRunBudgetSeconds != test.cleanupConfig.CleanupRunBudgetSeconds ||
				got.CleanupMaxResultRowsPerRun != test.cleanupConfig.CleanupMaxResultRowsPerRun ||
				got.CleanupMaxRequestRowsPerRun != test.cleanupConfig.CleanupMaxRequestRowsPerRun {
				t.Fatalf("cleanup config=%+v want=%+v", got, test.cleanupConfig)
			}
		})
	}
}

func TestRecommendationTraceCleanupRunDrainsMultipleBatchesFairly(t *testing.T) {
	cfg := testRecommendationTraceCleanupConfig()
	remainingResults, remainingRequests := int64(12), int64(12)
	var execution []string
	var resultBatchRows, requestBatchRows []int64
	deleteResult := func(_ context.Context, _ time.Time, limit int) (int64, error) {
		execution = append(execution, "result")
		rows := min(remainingResults, int64(limit))
		remainingResults -= rows
		resultBatchRows = append(resultBatchRows, rows)
		return rows, nil
	}
	deleteRequest := func(_ context.Context, _ time.Time, limit int) (int64, error) {
		execution = append(execution, "request")
		rows := min(remainingRequests, int64(limit))
		remainingRequests -= rows
		requestBatchRows = append(requestBatchRows, rows)
		return rows, nil
	}

	result, err := runRecommendationTraceCleanup(context.Background(), cfg, fixedCleanupClock, deleteResult, deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CaughtUp || result.BudgetReached || result.Cycles != 3 || result.ResultRows != 12 || result.RequestRows != 12 {
		t.Fatalf("run result=%+v", result)
	}
	if remainingResults != 0 || remainingRequests != 0 {
		t.Fatalf("remaining results=%d requests=%d", remainingResults, remainingRequests)
	}
	if !reflect.DeepEqual(execution, []string{"result", "request", "result", "request", "result", "request"}) {
		t.Fatalf("execution order=%v", execution)
	}
	if !reflect.DeepEqual(resultBatchRows, []int64{5, 5, 2}) || !reflect.DeepEqual(requestBatchRows, []int64{5, 5, 2}) {
		t.Fatalf("batch rows result=%v request=%v", resultBatchRows, requestBatchRows)
	}
}

func TestRecommendationTraceCleanupUnderfilledBatchesStop(t *testing.T) {
	resultCalls, requestCalls := 0, 0
	result, err := runRecommendationTraceCleanup(context.Background(), testRecommendationTraceCleanupConfig(), fixedCleanupClock,
		func(context.Context, time.Time, int) (int64, error) { resultCalls++; return 2, nil },
		func(context.Context, time.Time, int) (int64, error) { requestCalls++; return 3, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CaughtUp || result.Cycles != 1 || resultCalls != 1 || requestCalls != 1 {
		t.Fatalf("run=%+v resultCalls=%d requestCalls=%d", result, resultCalls, requestCalls)
	}
}

func TestRecommendationTraceCleanupOneFullBatchDoesNotMeanCaughtUp(t *testing.T) {
	resultBatchRows := []int64{5, 0}
	requestBatchRows := []int64{2, 0}
	resultCalls, requestCalls := 0, 0
	result, err := runRecommendationTraceCleanup(context.Background(), testRecommendationTraceCleanupConfig(), fixedCleanupClock,
		func(context.Context, time.Time, int) (int64, error) {
			rows := resultBatchRows[resultCalls]
			resultCalls++
			return rows, nil
		},
		func(context.Context, time.Time, int) (int64, error) {
			rows := requestBatchRows[requestCalls]
			requestCalls++
			return rows, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CaughtUp || result.Cycles != 2 || result.ResultRows != 5 || result.RequestRows != 2 {
		t.Fatalf("run=%+v", result)
	}
}

func TestRecommendationTraceCleanupRowBudgetsAreIndependent(t *testing.T) {
	tests := []struct {
		name             string
		maxResultRows    int
		maxRequestRows   int
		resultBatchRows  int64
		requestBatchRows int64
	}{
		{name: "result rows allow one bounded batch overshoot", maxResultRows: 4, maxRequestRows: 100, resultBatchRows: 5, requestBatchRows: 1},
		{name: "request rows allow one bounded batch overshoot", maxResultRows: 100, maxRequestRows: 4, resultBatchRows: 1, requestBatchRows: 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testRecommendationTraceCleanupConfig()
			cfg.CleanupMaxResultRowsPerRun = test.maxResultRows
			cfg.CleanupMaxRequestRowsPerRun = test.maxRequestRows
			resultCalls, requestCalls := 0, 0
			result, err := runRecommendationTraceCleanup(context.Background(), cfg, fixedCleanupClock,
				func(context.Context, time.Time, int) (int64, error) { resultCalls++; return test.resultBatchRows, nil },
				func(context.Context, time.Time, int) (int64, error) {
					requestCalls++
					return test.requestBatchRows, nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !result.BudgetReached || result.CaughtUp || result.Cycles != 1 || resultCalls != 1 || requestCalls != 1 {
				t.Fatalf("run=%+v resultCalls=%d requestCalls=%d", result, resultCalls, requestCalls)
			}
		})
	}
}

func TestRecommendationTraceCleanupRowBudgetResumesOnNextRun(t *testing.T) {
	remainingResults := int64(12)
	cfg := testRecommendationTraceCleanupConfig()
	cfg.CleanupMaxResultRowsPerRun = 4
	deleteResult := func(_ context.Context, _ time.Time, limit int) (int64, error) {
		rows := min(remainingResults, int64(limit))
		remainingResults -= rows
		return rows, nil
	}
	deleteRequest := func(context.Context, time.Time, int) (int64, error) { return 0, nil }

	firstRun, err := runRecommendationTraceCleanup(context.Background(), cfg, fixedCleanupClock, deleteResult, deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !firstRun.BudgetReached || firstRun.ResultRows != 5 || remainingResults != 7 {
		t.Fatalf("first run=%+v remaining results=%d", firstRun, remainingResults)
	}

	cfg.CleanupMaxResultRowsPerRun = 100
	secondRun, err := runRecommendationTraceCleanup(context.Background(), cfg, fixedCleanupClock, deleteResult, deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !secondRun.CaughtUp || secondRun.ResultRows != 7 || remainingResults != 0 {
		t.Fatalf("second run=%+v remaining results=%d", secondRun, remainingResults)
	}
}

func TestRecommendationTraceCleanupTimeBudget(t *testing.T) {
	cfg := testRecommendationTraceCleanupConfig()
	cfg.CleanupRunBudgetSeconds = 1
	clockCalls, resultCalls, requestCalls := 0, 0, 0
	clock := func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return time.Unix(100, 0)
		}
		return time.Unix(101, 0)
	}
	result, err := runRecommendationTraceCleanup(context.Background(), cfg, clock,
		func(context.Context, time.Time, int) (int64, error) { resultCalls++; return 5, nil },
		func(context.Context, time.Time, int) (int64, error) { requestCalls++; return 0, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.BudgetReached || result.Cycles != 1 || resultCalls != 1 || requestCalls != 1 {
		t.Fatalf("run=%+v resultCalls=%d requestCalls=%d", result, resultCalls, requestCalls)
	}
}

func TestRecommendationTraceCleanupCancellationStopsBetweenBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCalls, requestCalls := 0, 0
	result, err := runRecommendationTraceCleanup(ctx, testRecommendationTraceCleanupConfig(), fixedCleanupClock,
		func(context.Context, time.Time, int) (int64, error) { resultCalls++; return 0, nil },
		func(context.Context, time.Time, int) (int64, error) { requestCalls++; cancel(); return 0, nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
	if result.Cycles != 1 || resultCalls != 1 || requestCalls != 1 {
		t.Fatalf("run=%+v resultCalls=%d requestCalls=%d", result, resultCalls, requestCalls)
	}
}

func TestRecommendationTraceCleanupCancellationAfterResultPreservesProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requestCalls := 0
	result, err := runRecommendationTraceCleanup(ctx, testRecommendationTraceCleanupConfig(), fixedCleanupClock,
		func(context.Context, time.Time, int) (int64, error) {
			cancel()
			return 3, nil
		},
		func(context.Context, time.Time, int) (int64, error) { requestCalls++; return 0, nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
	if result.ResultRows != 3 || result.RequestRows != 0 || result.Cycles != 1 || requestCalls != 0 {
		t.Fatalf("partial progress=%+v requestCalls=%d", result, requestCalls)
	}
}

func TestRecommendationTraceCleanupRequestErrorPreservesPartialProgress(t *testing.T) {
	requestErr := errors.New("request delete failed")
	requestCalls := 0
	result, err := runRecommendationTraceCleanup(context.Background(), testRecommendationTraceCleanupConfig(), fixedCleanupClock,
		func(context.Context, time.Time, int) (int64, error) { return 5, nil },
		func(context.Context, time.Time, int) (int64, error) {
			requestCalls++
			if requestCalls == 1 {
				return 5, nil
			}
			return 0, requestErr
		},
	)
	if !errors.Is(err, requestErr) {
		t.Fatalf("error=%v, want wrapped request error", err)
	}
	if result.ResultRows != 10 || result.RequestRows != 5 || result.Cycles != 2 {
		t.Fatalf("partial progress=%+v", result)
	}
}

func TestRecommendationTraceCleanupNextDelay(t *testing.T) {
	cfg := testRecommendationTraceCleanupConfig()
	cfg.CleanupIntervalSeconds = 600
	cfg.CleanupCatchupIntervalSeconds = 60
	for _, test := range []struct {
		name   string
		result recommendationTraceCleanupRunResult
		err    error
		want   time.Duration
	}{
		{name: "caught up uses normal cadence", result: recommendationTraceCleanupRunResult{CaughtUp: true}, want: 10 * time.Minute},
		{name: "backlog uses catch-up cadence", result: recommendationTraceCleanupRunResult{BudgetReached: true}, want: time.Minute},
		{name: "errors use normal cadence", result: recommendationTraceCleanupRunResult{BudgetReached: true}, err: errors.New("database unavailable"), want: 10 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := recommendationTraceCleanupNextDelay(cfg, test.result, test.err); got != test.want {
				t.Fatalf("delay=%s, want %s", got, test.want)
			}
		})
	}
}

func testRecommendationTraceCleanupConfig() config.RecommendationTraceConfig {
	return config.RecommendationTraceConfig{
		ResultRetentionDays: 30, RequestRetentionDays: 90,
		CleanupIntervalSeconds: 600, CleanupCatchupIntervalSeconds: 60,
		CleanupResultBatchSize: 5, CleanupRequestBatchSize: 5,
		CleanupRunBudgetSeconds: 60, CleanupMaxResultRowsPerRun: 1000,
		CleanupMaxRequestRowsPerRun: 1000,
	}
}

func fixedCleanupClock() time.Time { return time.Unix(100, 0).UTC() }
