package tasks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/metrics"
)

type ProviderAction string

const (
	ProviderActionRetry      ProviderAction = "retry"
	ProviderActionPause      ProviderAction = "pause"
	ProviderActionSplitBatch ProviderAction = "split_batch"
	ProviderActionDeadLetter ProviderAction = "dead_letter"
)

const (
	providerReasonAuth              = "auth"
	providerReasonPermission        = "permission"
	providerReasonConfig            = "config"
	providerReasonQuota             = "quota"
	providerReasonRateLimit         = "rate_limit"
	providerReasonServer            = "server"
	providerReasonContract          = "contract"
	providerReasonTransport         = "transport"
	providerReasonUnknown           = "unknown"
	providerReasonRetryAfterInvalid = "retry_after_invalid"
	providerReasonRetryAfterLong    = "retry_after_long"
)

const (
	postEmbeddingRetryAfterMaxInline = 2 * time.Second
	postEmbeddingRetryBaseCooldown   = 5 * time.Second
	postEmbeddingRetryMaxCooldown    = 5 * time.Minute
	postEmbeddingServicePause        = 15 * time.Minute
	postEmbeddingContractPause       = 5 * time.Minute
	postEmbeddingLongRetryAfterAlert = postEmbeddingRetryMaxCooldown
)

// ProviderDecision is the Consumer's bounded recovery choice for a provider
// failure. The HTTP client only returns provider metadata and never chooses a
// Kafka action.
type ProviderDecision struct {
	Action             ProviderAction
	Reason             string
	HTTPStatus         int
	ProviderCode       string
	RetryAfter         time.Duration
	RetryAfterProvided bool
	RetryAfterValid    bool
	RequestID          string
}

type postEmbeddingProviderFailure struct {
	decision ProviderDecision
	cause    error
}

func (e *postEmbeddingProviderFailure) Error() string {
	if e == nil {
		return "embedding provider failure"
	}
	if e.decision.HTTPStatus != 0 {
		return fmt.Sprintf("embedding provider failure action=%s reason=%s http_status=%d", e.decision.Action, e.decision.Reason, e.decision.HTTPStatus)
	}
	return fmt.Sprintf("embedding provider failure action=%s reason=%s", e.decision.Action, e.decision.Reason)
}

func (e *postEmbeddingProviderFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func providerFailureDecision(err error) (ProviderDecision, bool) {
	var failure *postEmbeddingProviderFailure
	if !errors.As(err, &failure) || failure == nil {
		return ProviderDecision{}, false
	}
	return failure.decision, true
}

func classifyPostEmbeddingProviderError(err error, batchSize int) ProviderDecision {
	if errors.Is(err, context.Canceled) {
		// Cancellation is propagated by the caller and is not a recovery action.
		return ProviderDecision{Reason: providerReasonTransport}
	}
	if batchSize < 1 {
		batchSize = 1
	}
	var httpErr *embeddings.ProviderHTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		decision := ProviderDecision{
			HTTPStatus:         httpErr.StatusCode,
			ProviderCode:       httpErr.ProviderCode,
			RetryAfter:         httpErr.RetryAfter,
			RetryAfterProvided: httpErr.RetryAfterProvided,
			RetryAfterValid:    httpErr.RetryAfterValid,
			RequestID:          httpErr.RequestID,
		}
		switch httpErr.StatusCode {
		case 400:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonConfig
		case 401:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonAuth
		case 402:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonQuota
		case 403:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonPermission
		case 404:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonConfig
		case 408, 425:
			decision.Action, decision.Reason = ProviderActionRetry, providerReasonTransport
		case 409:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonUnknown
		case 413:
			decision.Reason = providerReasonConfig
			if batchSize > 1 {
				decision.Action = ProviderActionSplitBatch
			} else {
				decision.Action = ProviderActionPause
			}
		case 422:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonConfig
		case 429:
			if isProviderQuotaError(httpErr.ProviderCode, httpErr.ProviderType) {
				decision.Action, decision.Reason = ProviderActionPause, providerReasonQuota
			} else {
				decision.Action, decision.Reason = ProviderActionRetry, providerReasonRateLimit
			}
		case 500, 502, 503, 504:
			decision.Action, decision.Reason = ProviderActionRetry, providerReasonServer
		case 501:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonConfig
		default:
			decision.Action, decision.Reason = ProviderActionPause, providerReasonUnknown
		}
		return decision
	}

	if embeddings.IsProviderContractError(err) {
		if batchSize > 1 {
			return ProviderDecision{Action: ProviderActionSplitBatch, Reason: providerReasonContract}
		}
		return ProviderDecision{Action: ProviderActionPause, Reason: providerReasonContract}
	}

	var verificationErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var tlsRecordErr tls.RecordHeaderError
	if errors.As(err, &verificationErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) || errors.As(err, &invalidCertificate) || errors.As(err, &tlsRecordErr) {
		return ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig}
	}

	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkErr) {
		return ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonTransport}
	}
	// Unknown provider-call failures receive only bounded short retries. They
	// are never evidence that a Kafka message is permanently invalid.
	return ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonUnknown}
}

func isProviderQuotaError(code, providerType string) bool {
	for _, value := range []string{strings.ToLower(strings.TrimSpace(code)), strings.ToLower(strings.TrimSpace(providerType))} {
		switch value {
		case "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded",
			"organization_usage_limit_exceeded", "usage_limit_exceeded", "insufficient_quota":
			return true
		}
	}
	return false
}

type providerRetryRuntime struct {
	Sleep  func(context.Context, time.Duration) error
	Jitter func(time.Duration) time.Duration
	Now    func() time.Time
}

func normalizedProviderRetryRuntime(runtime providerRetryRuntime) providerRetryRuntime {
	if runtime.Sleep == nil {
		runtime.Sleep = waitForPostEmbeddingProviderDelay
	}
	if runtime.Jitter == nil {
		runtime.Jitter = jitterPostEmbeddingDelay
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return runtime
}

func jitterPostEmbeddingDelay(delay time.Duration) time.Duration {
	if delay <= 1 {
		return delay
	}
	// A bounded +/-20% jitter reduces synchronized calls while retaining the
	// configured exponential retry scale.
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * factor)
}

func waitForPostEmbeddingProviderDelay(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		return errors.New("provider wait context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type postEmbeddingRecoveryCoordinator struct {
	mu                       sync.Mutex
	runtime                  providerRetryRuntime
	consecutiveRetryFailures uint
	providerFailurePending   bool
}

func newPostEmbeddingRecoveryCoordinator(runtime providerRetryRuntime) *postEmbeddingRecoveryCoordinator {
	return &postEmbeddingRecoveryCoordinator{runtime: normalizedProviderRetryRuntime(runtime)}
}

func (r *postEmbeddingRecoveryCoordinator) noteProviderFailure() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.providerFailurePending = true
	r.mu.Unlock()
}

func (r *postEmbeddingRecoveryCoordinator) offsetCommitted() {
	if r == nil {
		return
	}
	r.mu.Lock()
	wasPending := r.providerFailurePending
	r.providerFailurePending = false
	r.consecutiveRetryFailures = 0
	r.mu.Unlock()
	metrics.SetEmbeddingProviderPause(false, "", 0)
	if wasPending {
		metrics.RecordEmbeddingProviderRecovery()
	}
}

func (r *postEmbeddingRecoveryCoordinator) cooldownFor(err error) (time.Duration, ProviderDecision, bool) {
	decision, ok := providerFailureDecision(err)
	if !ok {
		return postEmbeddingConsumerRestartDelay, ProviderDecision{}, false
	}
	if r == nil {
		r = newPostEmbeddingRecoveryCoordinator(providerRetryRuntime{})
	}
	r.noteProviderFailure()

	var delay time.Duration
	switch decision.Action {
	case ProviderActionRetry:
		r.mu.Lock()
		r.consecutiveRetryFailures++
		n := r.consecutiveRetryFailures
		r.mu.Unlock()
		delay = postEmbeddingRetryBaseCooldown
		for step := uint(1); step < n && delay < postEmbeddingRetryMaxCooldown; step++ {
			if delay > postEmbeddingRetryMaxCooldown/2 {
				delay = postEmbeddingRetryMaxCooldown
				break
			}
			delay *= 2
		}
		if delay > postEmbeddingRetryMaxCooldown {
			delay = postEmbeddingRetryMaxCooldown
		}
		delay = r.runtime.Jitter(delay)
		if delay < postEmbeddingRetryBaseCooldown {
			delay = postEmbeddingRetryBaseCooldown
		}
		if delay > postEmbeddingRetryMaxCooldown {
			delay = postEmbeddingRetryMaxCooldown
		}
	case ProviderActionPause:
		if decision.Reason == providerReasonContract {
			delay = postEmbeddingContractPause
		} else {
			delay = postEmbeddingServicePause
		}
	default:
		// SplitBatch and DeadLetter must be resolved while the fetched batch is
		// still available. They do not normally escape processPostEmbeddingBatch.
		delay = postEmbeddingServicePause
		decision.Action = ProviderActionPause
		decision.Reason = providerReasonUnknown
	}
	if decision.RetryAfterValid && decision.RetryAfter > delay {
		delay = decision.RetryAfter
	}
	if delay <= 0 {
		delay = postEmbeddingRetryBaseCooldown
	}
	metrics.SetEmbeddingProviderPause(true, decision.Reason, r.runtime.Now().Add(delay).Unix())
	return delay, decision, true
}

func logInvalidRetryAfter(status int) {
	log.Printf("[PostEmbedding] provider returned an invalid or out-of-range Retry-After; using local backoff status=%d", status)
}
