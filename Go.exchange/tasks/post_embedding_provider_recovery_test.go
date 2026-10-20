package tasks

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/embeddings"
	appmetrics "Go.exchange/metrics"
)

func TestClassifyPostEmbeddingProviderErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		batchSize int
		action    ProviderAction
		reason    string
	}{
		{name: "400 unknown code", err: &embeddings.ProviderHTTPError{StatusCode: 400}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "400 model config", err: &embeddings.ProviderHTTPError{StatusCode: 400, ProviderCode: "model_not_found"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "401", err: &embeddings.ProviderHTTPError{StatusCode: 401}, batchSize: 1, action: ProviderActionPause, reason: providerReasonAuth},
		{name: "402", err: &embeddings.ProviderHTTPError{StatusCode: 402}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "403", err: &embeddings.ProviderHTTPError{StatusCode: 403}, batchSize: 1, action: ProviderActionPause, reason: providerReasonPermission},
		{name: "404", err: &embeddings.ProviderHTTPError{StatusCode: 404}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "408", err: &embeddings.ProviderHTTPError{StatusCode: 408}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonTransport},
		{name: "409 unknown", err: &embeddings.ProviderHTTPError{StatusCode: 409}, batchSize: 1, action: ProviderActionPause, reason: providerReasonUnknown},
		{name: "413 batch", err: &embeddings.ProviderHTTPError{StatusCode: 413}, batchSize: 4, action: ProviderActionSplitBatch, reason: providerReasonConfig},
		{name: "413 single", err: &embeddings.ProviderHTTPError{StatusCode: 413}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "422 unknown", err: &embeddings.ProviderHTTPError{StatusCode: 422}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "425", err: &embeddings.ProviderHTTPError{StatusCode: 425}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonTransport},
		{name: "429 rate limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "rate_limit_exceeded"}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonRateLimit},
		{name: "429 slow down", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "slow_down"}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonRateLimit},
		{name: "429 type rate limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderType: "rate_limit_error"}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonRateLimit},
		{name: "429 unknown", err: &embeddings.ProviderHTTPError{StatusCode: 429}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonRateLimit},
		{name: "429 credit balance", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "credit_balance_exhausted"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "429 quota overrides limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "insufficient_quota", ProviderType: "rate_limit_error"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "429 organization spend limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "organization_spend_limit_exceeded"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "429 project spend limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "project_spend_limit_exceeded"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "429 organization usage limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "organization_usage_limit_exceeded"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "429 usage limit", err: &embeddings.ProviderHTTPError{StatusCode: 429, ProviderCode: "usage_limit_exceeded"}, batchSize: 1, action: ProviderActionPause, reason: providerReasonQuota},
		{name: "500", err: &embeddings.ProviderHTTPError{StatusCode: 500}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonServer},
		{name: "501", err: &embeddings.ProviderHTTPError{StatusCode: 501}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "502", err: &embeddings.ProviderHTTPError{StatusCode: 502}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonServer},
		{name: "503", err: &embeddings.ProviderHTTPError{StatusCode: 503}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonServer},
		{name: "504", err: &embeddings.ProviderHTTPError{StatusCode: 504}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonServer},
		{name: "unknown HTTP", err: &embeddings.ProviderHTTPError{StatusCode: 418}, batchSize: 1, action: ProviderActionPause, reason: providerReasonUnknown},
		{name: "network temporary", err: &net.DNSError{IsTimeout: true}, batchSize: 1, action: ProviderActionRetry, reason: providerReasonTransport},
		{name: "TLS verification", err: x509.UnknownAuthorityError{}, batchSize: 1, action: ProviderActionPause, reason: providerReasonConfig},
		{name: "unknown call failure", err: errors.New("unrecognized provider error"), batchSize: 1, action: ProviderActionRetry, reason: providerReasonUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyPostEmbeddingProviderError(test.err, test.batchSize)
			if got.Action != test.action || got.Reason != test.reason {
				t.Fatalf("decision=%+v want action=%s reason=%s", got, test.action, test.reason)
			}
		})
	}
	if got := classifyPostEmbeddingProviderError(context.Canceled, 1); got.Action != "" {
		t.Fatalf("canceled context should be propagated, got decision=%+v", got)
	}
	decision := classifyPostEmbeddingProviderError(&embeddings.ProviderHTTPError{
		StatusCode: 429, ProviderCode: "slow_down", RetryAfter: 3 * time.Second,
		RetryAfterProvided: true, RetryAfterValid: true, RequestID: "request-42",
	}, 1)
	if decision.HTTPStatus != 429 || decision.ProviderCode != "slow_down" || decision.RetryAfter != 3*time.Second || !decision.RetryAfterValid || decision.RequestID != "request-42" {
		t.Fatalf("provider decision lost bounded diagnostic metadata: %+v", decision)
	}
}

func TestPostEmbeddingProviderRetryWaitHonorsRetryAfter(t *testing.T) {
	embedder := &postEmbeddingTestEmbedder{errors: []error{
		&embeddings.ProviderHTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 900 * time.Millisecond, RetryAfterProvided: true, RetryAfterValid: true},
		nil,
	}}
	var waits []time.Duration
	runtime := providerRetryRuntime{
		Sleep:  func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
	}
	result, err := generatePostEmbeddingTextsWithRuntime(context.Background(), embedder, []string{"text"}, kafkaRetryPolicy{
		MaxAttempts: 2, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 2 * time.Second,
	}, runtime, nil)
	if err != nil || embedder.calls != 2 || result.Model != "test-model" || len(waits) != 1 || waits[0] != 900*time.Millisecond {
		t.Fatalf("result=%+v err=%v calls=%d waits=%v", result, err, embedder.calls, waits)
	}
}

func TestPostEmbeddingShortRetryDoesNotEnterGlobalCooldownPrematurely(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "503", err: &embeddings.ProviderHTTPError{StatusCode: http.StatusServiceUnavailable}},
		{name: "429 rate limit", err: &embeddings.ProviderHTTPError{
			StatusCode: http.StatusTooManyRequests, ProviderCode: "rate_limit_exceeded", ProviderType: "rate_limit_error",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanupPostEmbeddingGateMetrics(t)
			now := time.Unix(200, 0)
			var gate *postEmbeddingProviderGate
			var waits []time.Duration
			runtime := providerRetryRuntime{
				Now:    func() time.Time { return now },
				Jitter: func(delay time.Duration) time.Duration { return delay },
				Sleep: func(_ context.Context, delay time.Duration) error {
					gate.mu.Lock()
					state := gate.state
					gate.mu.Unlock()
					if state != postEmbeddingCircuitClosed {
						t.Fatalf("breaker entered %v during a bounded short retry", state)
					}
					waits = append(waits, delay)
					return nil
				},
			}
			gate = newPostEmbeddingProviderGate(runtime)
			embedder := &postEmbeddingTestEmbedder{errors: []error{test.err, nil}}
			retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable)
			exhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable)
			result, err := generatePostEmbeddingTextsWithRuntimeAndGate(
				context.Background(), embedder, []string{"text"},
				kafkaRetryPolicy{MaxAttempts: 3, InitialBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond},
				runtime, nil, gate,
			)
			if err != nil || result.Model != "test-model" || embedder.calls != 2 || len(waits) != 1 || waits[0] != 20*time.Millisecond {
				t.Fatalf("result=%+v err=%v calls=%d waits=%v", result, err, embedder.calls, waits)
			}
			gate.mu.Lock()
			state, inFlight, failures := gate.state, gate.inFlight, gate.recovery.consecutiveRetryFailures
			gate.mu.Unlock()
			if state != postEmbeddingCircuitClosed || inFlight != 0 || failures != 0 {
				t.Fatalf("breaker state=%v inFlight=%d consecutive failures=%d, want Closed/0/0", state, inFlight, failures)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable); got != retryBefore+1 {
				t.Fatalf("retry_attempt=%v want %v", got, retryBefore+1)
			}
			if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable); got != exhaustedBefore {
				t.Fatalf("retry_exhausted=%v want unchanged %v", got, exhaustedBefore)
			}
		})
	}
}

func TestPostEmbeddingRetryExhaustionOpensCircuit(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	now := time.Unix(250, 0)
	runtime := providerRetryRuntime{
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return now },
	}
	gate := newPostEmbeddingProviderGate(runtime)
	embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{StatusCode: http.StatusServiceUnavailable}}
	retryBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable)
	exhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable)
	_, err := generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), embedder, []string{"text"},
		kafkaRetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond},
		runtime, nil, gate,
	)
	if _, ok := providerFailureDecision(err); !ok || embedder.calls != 3 {
		t.Fatalf("provider result err=%v requests=%d, want retryable provider failure after 3 requests", err, embedder.calls)
	}
	gate.mu.Lock()
	state, inFlight := gate.state, gate.inFlight
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen || inFlight != 0 {
		t.Fatalf("breaker after retry exhaustion state=%v inFlight=%d, want Open/0", state, inFlight)
	}
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryAttempt, kafkaFailureCodeProviderRetryable); got != retryBefore+2 {
		t.Fatalf("retry_attempt=%v want %v", got, retryBefore+2)
	}
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable); got != exhaustedBefore+1 {
		t.Fatalf("retry_exhausted=%v want %v", got, exhaustedBefore+1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = generatePostEmbeddingTextsWithRuntimeAndGate(
		ctx, embedder, []string{"must wait"}, kafkaRetryPolicy{MaxAttempts: 3}, runtime, nil, gate,
	)
	if !errors.Is(err, context.DeadlineExceeded) || embedder.calls != 3 {
		t.Fatalf("open-circuit request err=%v provider calls=%d, want cancelled wait and no additional call", err, embedder.calls)
	}

	gate.mu.Lock()
	gate.openUntil = now.Add(-time.Millisecond)
	gate.mu.Unlock()
	_, err = generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), embedder, []string{"single half-open probe"},
		kafkaRetryPolicy{MaxAttempts: 3}, runtime, nil, gate,
	)
	if _, ok := providerFailureDecision(err); !ok || embedder.calls != 4 {
		t.Fatalf("HalfOpen result err=%v provider calls=%d, want one failing probe", err, embedder.calls)
	}
	gate.mu.Lock()
	state, inFlight = gate.state, gate.inFlight
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen || inFlight != 0 {
		t.Fatalf("breaker after failed HalfOpen probe state=%v inFlight=%d, want Open/0", state, inFlight)
	}
}

func TestPostEmbeddingQuotaPauseOpensCircuitWithoutShortRetry(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	now := time.Unix(275, 0)
	var waits int
	runtime := providerRetryRuntime{
		Sleep:  func(context.Context, time.Duration) error { waits++; return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return now },
	}
	gate := newPostEmbeddingProviderGate(runtime)
	embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{
		StatusCode: http.StatusTooManyRequests, ProviderCode: "insufficient_quota", ProviderType: "rate_limit_error",
	}}
	_, err := generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), embedder, []string{"quota"}, kafkaRetryPolicy{MaxAttempts: 5}, runtime, nil, gate,
	)
	decision, ok := providerFailureDecision(err)
	if !ok || decision.Action != ProviderActionPause || decision.Reason != providerReasonQuota || embedder.calls != 1 || waits != 0 {
		t.Fatalf("decision=%+v err=%v requests=%d waits=%d, want immediate quota Pause", decision, err, embedder.calls, waits)
	}
	gate.mu.Lock()
	state, inFlight, openUntil := gate.state, gate.inFlight, gate.openUntil
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen || inFlight != 0 || openUntil.Sub(now) != postEmbeddingServicePause {
		t.Fatalf("quota breaker state=%v inFlight=%d cooldown=%s, want Open/0/service pause", state, inFlight, openUntil.Sub(now))
	}
}

func TestPostEmbeddingInvalidRetryAfterAlertsAndUsesLocalBackoff(t *testing.T) {
	embedder := &postEmbeddingTestEmbedder{errors: []error{
		&embeddings.ProviderHTTPError{StatusCode: http.StatusTooManyRequests, RetryAfterProvided: true, RetryAfterValid: false},
		nil,
	}}
	var waits []time.Duration
	before := postEmbeddingDomainMetric(t, "go_exchange_embedding_provider_failures_total", "reason", providerReasonRetryAfterInvalid)
	result, err := generatePostEmbeddingTextsWithRuntime(context.Background(), embedder, []string{"text"}, kafkaRetryPolicy{
		MaxAttempts: 2, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second,
	}, providerRetryRuntime{
		Sleep:  func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
	}, nil)
	if err != nil || result.Model != "test-model" || embedder.calls != 2 || len(waits) != 1 || waits[0] != 100*time.Millisecond {
		t.Fatalf("result=%+v err=%v calls=%d waits=%v", result, err, embedder.calls, waits)
	}
	if got := postEmbeddingDomainMetric(t, "go_exchange_embedding_provider_failures_total", "reason", providerReasonRetryAfterInvalid); got != before+1 {
		t.Fatalf("invalid Retry-After alert counter=%v want %v", got, before+1)
	}
}

func TestPostEmbeddingProviderCallCancellationPropagatesWithoutRetry(t *testing.T) {
	embedder := &postEmbeddingTestEmbedder{err: context.Canceled}
	var waits []time.Duration
	_, err := generatePostEmbeddingTextsWithRuntime(context.Background(), embedder, []string{"text"}, kafkaRetryPolicy{MaxAttempts: 5}, providerRetryRuntime{
		Sleep: func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil },
	}, nil)
	if !errors.Is(err, context.Canceled) || embedder.calls != 1 || len(waits) != 0 {
		t.Fatalf("err=%v provider calls=%d waits=%v", err, embedder.calls, waits)
	}
}

func TestPostEmbeddingLongRetryAfterMovesToCooldownWithoutShortRetry(t *testing.T) {
	t.Cleanup(func() { appmetrics.SetEmbeddingProviderPause(false, "", 0) })
	embedder := &postEmbeddingTestEmbedder{err: &embeddings.ProviderHTTPError{
		StatusCode: http.StatusTooManyRequests, RetryAfter: 6 * time.Minute, RetryAfterProvided: true, RetryAfterValid: true,
	}}
	var waits []time.Duration
	now := time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
	longRetryAfterBefore := postEmbeddingDomainMetric(t, "go_exchange_embedding_provider_failures_total", "reason", providerReasonRetryAfterLong)
	retryExhaustedBefore := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable)
	runtime := providerRetryRuntime{
		Sleep:  func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return now },
	}
	gate := newPostEmbeddingProviderGate(runtime)
	_, err := generatePostEmbeddingTextsWithRuntimeAndGate(context.Background(), embedder, []string{"text"}, kafkaRetryPolicy{
		MaxAttempts: 5, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 2 * time.Second,
	}, runtime, nil, gate)
	decision, ok := providerFailureDecision(err)
	if !ok || decision.Action != ProviderActionRetry || embedder.calls != 1 || len(waits) != 0 {
		t.Fatalf("decision=%+v typed=%t calls=%d waits=%v err=%v", decision, ok, embedder.calls, waits, err)
	}
	recovery := newPostEmbeddingRecoveryCoordinator(runtime)
	delay, _, isProvider := recovery.cooldownFor(err)
	if !isProvider || delay != 6*time.Minute {
		t.Fatalf("cooldown=%s provider=%t, want Retry-After 6m", delay, isProvider)
	}
	if next := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_next_retry_timestamp_seconds"); next != float64(now.Add(6*time.Minute).Unix()) {
		t.Fatalf("next retry timestamp=%v", next)
	}
	if got := postEmbeddingDomainMetric(t, "go_exchange_embedding_provider_failures_total", "reason", providerReasonRetryAfterLong); got != longRetryAfterBefore+1 {
		t.Fatalf("long Retry-After alert counter=%v want %v", got, longRetryAfterBefore+1)
	}
	if got := postEmbeddingRecoveryMetric(t, kafkaRecoveryOutcomeRetryExhausted, kafkaFailureCodeProviderRetryable); got != retryExhaustedBefore+1 {
		t.Fatalf("Retry-After short-retry termination counter=%v want %v", got, retryExhaustedBefore+1)
	}
	gate.mu.Lock()
	state, openUntil := gate.state, gate.openUntil
	gate.mu.Unlock()
	if state != postEmbeddingCircuitOpen || openUntil.Sub(now) != 6*time.Minute {
		t.Fatalf("breaker state=%v cooldown=%s, want Open for the full Retry-After", state, openUntil.Sub(now))
	}
}

func TestPostEmbeddingRecoveryCooldownResetsOnlyOnProviderRequestSuccess(t *testing.T) {
	t.Cleanup(func() { appmetrics.SetEmbeddingProviderPause(false, "", 0) })
	runtime := providerRetryRuntime{
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return time.Unix(100, 0) },
	}
	recovery := newPostEmbeddingRecoveryCoordinator(runtime)
	retryFailure := retryableKafkaError(kafkaFailureCodeProviderRetryable, &postEmbeddingProviderFailure{
		decision: ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer},
	})
	first, _, _ := recovery.cooldownFor(retryFailure)
	second, _, _ := recovery.cooldownFor(retryFailure)
	if first != 5*time.Second || second != 10*time.Second {
		t.Fatalf("retry cooldowns=%s,%s, want 5s,10s", first, second)
	}
	recovery.providerRequestSucceeded()
	third, _, _ := recovery.cooldownFor(retryFailure)
	if third != 5*time.Second {
		t.Fatalf("cooldown after validated provider request=%s, want 5s", third)
	}
}

func TestPostEmbeddingProviderRecoveryRequiresValidatedProviderSuccess(t *testing.T) {
	cleanupPostEmbeddingGateMetrics(t)
	runtime := providerRetryRuntime{
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return time.Unix(100, 0) },
	}
	gate := newPostEmbeddingProviderGate(runtime)
	permit, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.providerRequestFailed(permit, ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonServer}, postEmbeddingFailureScopeSharedOrUnknown, true)
	gate.mu.Lock()
	gate.openUntil = runtime.Now().Add(-time.Millisecond)
	gate.mu.Unlock()
	appmetrics.RecordEmbeddingProviderRecovery()
	recoveryBefore := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total")
	_, err = generatePostEmbeddingTextsWithRuntimeAndGate(
		context.Background(), &postEmbeddingTestEmbedder{}, []string{"valid post body"},
		kafkaRetryPolicy{MaxAttempts: 1}, runtime, nil, gate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total"); got != recoveryBefore+1 {
		t.Fatalf("provider recovery after validated request=%v want=%v", got, recoveryBefore+1)
	}
	gate.mu.Lock()
	state := gate.state
	gate.mu.Unlock()
	if state != postEmbeddingCircuitClosed {
		t.Fatalf("breaker state after validated Provider success=%v, want closed", state)
	}
}

func TestPostEmbeddingProviderPauseWaitStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	err := waitForPostEmbeddingProviderDelay(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("wait error=%v elapsed=%s", err, time.Since(started))
	}
}

func postEmbeddingScalarMetric(t *testing.T, metricName string) float64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	appmetrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, metricName+" ") {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse metric %q: %v", line, err)
		}
		return value
	}
	t.Fatalf("metric %q missing from /metrics", metricName)
	return 0
}
