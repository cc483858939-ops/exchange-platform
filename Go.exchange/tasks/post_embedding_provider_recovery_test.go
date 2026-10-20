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

	"github.com/segmentio/kafka-go"
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
	runtime := providerRetryRuntime{
		Sleep:  func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Now:    func() time.Time { return now },
	}
	_, err := generatePostEmbeddingTextsWithRuntime(context.Background(), embedder, []string{"text"}, kafkaRetryPolicy{
		MaxAttempts: 5, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 2 * time.Second,
	}, runtime, nil)
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
}

func TestPostEmbeddingRecoveryCooldownBackoffResetsOnlyOnOffsetCommit(t *testing.T) {
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
	recovery.offsetCommitted()
	third, _, _ := recovery.cooldownFor(retryFailure)
	if third != 5*time.Second {
		t.Fatalf("cooldown after committed recovery=%s, want 5s", third)
	}
}

func TestPostEmbeddingRecoveryCounterWaitsForKafkaOffsetCommit(t *testing.T) {
	store := newPostEmbeddingTestStore()
	embedder := &postEmbeddingTestEmbedder{errors: []error{
		&embeddings.ProviderHTTPError{StatusCode: http.StatusTooManyRequests, ProviderCode: "rate_limit_exceeded"},
		nil,
	}}
	runtime := providerRetryRuntime{
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Jitter: func(delay time.Duration) time.Duration { return delay },
	}
	recovery := newPostEmbeddingRecoveryCoordinator(runtime)
	hooks := postEmbeddingConsumerHooks{
		retryRuntime:      runtime,
		onProviderFailure: recovery.noteProviderFailure,
		onOffsetCommit:    recovery.offsetCommitted,
	}
	message := postEmbeddingTestMessage(t, 42)
	appmetrics.RecordEmbeddingProviderRecovery()
	recoveryBefore := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total")
	firstReader := &postEmbeddingTestReader{messages: []kafka.Message{message}, commitErr: errors.New("Kafka commit failed"), publisher: &fakeRawKafkaMessagePublisher{}}
	firstErr := consumePostEmbeddingMessagesWithBatchPolicyAndHooks(
		context.Background(), firstReader, firstReader.publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), 1, 0,
		kafkaRetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}, hooks,
	)
	if firstErr == nil || firstReader.commitCalls != 1 || embedder.calls != 2 || len(store.upserted) != 1 {
		t.Fatalf("first err=%v commits=%d provider=%d upserts=%d", firstErr, firstReader.commitCalls, embedder.calls, len(store.upserted))
	}
	if got := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total"); got != recoveryBefore {
		t.Fatalf("provider recovery counted before offset commit: before=%v after=%v", recoveryBefore, got)
	}

	secondReader := &postEmbeddingTestReader{messages: []kafka.Message{message}, stopErr: errors.New("reader stopped"), publisher: &fakeRawKafkaMessagePublisher{}}
	secondErr := consumePostEmbeddingMessagesWithBatchPolicyAndHooks(
		context.Background(), secondReader, secondReader.publisher, embedder, store, "v1", postEmbeddingRecoveryConfig(), 1, 0,
		kafkaRetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}, hooks,
	)
	if !errors.Is(secondErr, secondReader.stopErr) || secondReader.commitCalls != 1 || embedder.calls != 2 {
		t.Fatalf("replay err=%v commits=%d provider=%d", secondErr, secondReader.commitCalls, embedder.calls)
	}
	if got := postEmbeddingScalarMetric(t, "go_exchange_embedding_provider_recoveries_total"); got != recoveryBefore+1 {
		t.Fatalf("provider recovery after committed replay=%v want=%v", got, recoveryBefore+1)
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
