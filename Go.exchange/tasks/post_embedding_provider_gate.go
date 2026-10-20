package tasks

import (
	"context"
	"errors"
	"sync"
	"time"

	"Go.exchange/metrics"
)

const postEmbeddingProviderRequestLimit = 2

type postEmbeddingFailureScope uint8

const (
	postEmbeddingFailureScopePartition postEmbeddingFailureScope = iota + 1
	postEmbeddingFailureScopeInstance
	postEmbeddingFailureScopeSharedOrUnknown
)

type postEmbeddingCircuitState uint8

const (
	postEmbeddingCircuitClosed postEmbeddingCircuitState = iota
	postEmbeddingCircuitOpen
	postEmbeddingCircuitHalfOpen
)

type postEmbeddingProviderGate struct {
	mu         sync.Mutex
	changed    chan struct{}
	state      postEmbeddingCircuitState
	openUntil  time.Time
	openReason string
	inFlight   int
	epoch      uint64
	runtime    providerRetryRuntime
	recovery   *postEmbeddingRecoveryCoordinator
}

type postEmbeddingProviderPermit struct {
	gate     *postEmbeddingProviderGate
	epoch    uint64
	halfOpen bool
	once     sync.Once
}

func newPostEmbeddingProviderGate(runtime providerRetryRuntime) *postEmbeddingProviderGate {
	return &postEmbeddingProviderGate{
		changed:  make(chan struct{}),
		state:    postEmbeddingCircuitClosed,
		runtime:  normalizedProviderRetryRuntime(runtime),
		recovery: newPostEmbeddingRecoveryCoordinator(runtime),
	}
}

func (g *postEmbeddingProviderGate) acquire(ctx context.Context) (*postEmbeddingProviderPermit, error) {
	if ctx == nil {
		return nil, errors.New("post embedding provider context is nil")
	}
	if g == nil {
		return &postEmbeddingProviderPermit{}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		g.mu.Lock()
		now := g.runtime.Now()
		var waitFor time.Duration
		switch g.state {
		case postEmbeddingCircuitClosed:
			if g.inFlight < postEmbeddingProviderRequestLimit {
				g.inFlight++
				permit := &postEmbeddingProviderPermit{gate: g, epoch: g.epoch}
				g.mu.Unlock()
				return permit, nil
			}
		case postEmbeddingCircuitOpen:
			if !now.Before(g.openUntil) && g.inFlight == 0 {
				g.state = postEmbeddingCircuitHalfOpen
				g.epoch++
				g.inFlight++
				g.signalLocked()
				metrics.SetPostEmbeddingCircuitBreakerState("half_open")
				permit := &postEmbeddingProviderPermit{gate: g, epoch: g.epoch, halfOpen: true}
				g.mu.Unlock()
				return permit, nil
			}
			if now.Before(g.openUntil) {
				waitFor = g.openUntil.Sub(now)
			}
		case postEmbeddingCircuitHalfOpen:
			// A failed partition-scoped probe keeps the breaker HalfOpen. Once
			// that probe releases its permit, another partition may make the
			// single recovery attempt instead of leaving the breaker stuck.
			if g.inFlight == 0 {
				g.inFlight++
				permit := &postEmbeddingProviderPermit{gate: g, epoch: g.epoch, halfOpen: true}
				g.mu.Unlock()
				return permit, nil
			}
		}
		changed := g.changed
		g.mu.Unlock()

		if waitFor > 0 {
			timer := time.NewTimer(waitFor)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, ctx.Err()
			case <-changed:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (g *postEmbeddingProviderGate) providerRequestSucceeded(permit *postEmbeddingProviderPermit) {
	if permit == nil || permit.gate != g {
		return
	}
	permit.once.Do(func() {
		g.mu.Lock()
		if g.inFlight > 0 {
			g.inFlight--
		}
		recovered := permit.halfOpen && permit.epoch == g.epoch && g.state == postEmbeddingCircuitHalfOpen
		if recovered {
			g.state = postEmbeddingCircuitClosed
			g.recovery.providerRequestSucceeded()
			g.openUntil = time.Time{}
			g.openReason = ""
			g.epoch++
			g.signalLocked()
			metrics.SetPostEmbeddingCircuitBreakerState("closed")
			metrics.SetEmbeddingProviderPause(false, "", 0)
			metrics.RecordEmbeddingProviderRecovery()
			metrics.RecordPostEmbeddingHalfOpenProbe("success")
		} else {
			g.signalLocked()
		}
		g.mu.Unlock()
	})
}

func (g *postEmbeddingProviderGate) providerRequestFailed(
	permit *postEmbeddingProviderPermit,
	decision ProviderDecision,
	scope postEmbeddingFailureScope,
	retryExhausted bool,
) {
	if permit == nil || permit.gate != g {
		return
	}
	permit.once.Do(func() {
		g.mu.Lock()
		if g.inFlight > 0 {
			g.inFlight--
		}
		probeFailed := permit.halfOpen && permit.epoch == g.epoch && g.state == postEmbeddingCircuitHalfOpen
		if probeFailed && scope != postEmbeddingFailureScopePartition {
			g.openLocked(decision)
		} else if !probeFailed && permit.epoch == g.epoch && g.state == postEmbeddingCircuitClosed &&
			scope != postEmbeddingFailureScopePartition &&
			(decision.Action != ProviderActionRetry || retryExhausted) {
			g.openLocked(decision)
		}
		if probeFailed {
			metrics.RecordPostEmbeddingHalfOpenProbe("failure")
		}
		g.signalLocked()
		g.mu.Unlock()
	})
}

func (g *postEmbeddingProviderGate) providerRequestPanicked(permit *postEmbeddingProviderPermit) {
	if permit == nil || permit.gate != g {
		return
	}
	permit.once.Do(func() {
		g.mu.Lock()
		if g.inFlight > 0 {
			g.inFlight--
		}
		metrics.RecordEmbeddingProviderFailure(providerReasonUnknown)
		if permit.halfOpen && permit.epoch == g.epoch && g.state == postEmbeddingCircuitHalfOpen {
			decision := ProviderDecision{Action: ProviderActionRetry, Reason: providerReasonUnknown}
			delay, normalizedDecision, _ := g.recovery.cooldownFor(&postEmbeddingProviderFailure{
				decision: decision, cause: errors.New("provider request panicked during HalfOpen probe"),
			})
			g.state = postEmbeddingCircuitOpen
			g.openUntil = g.runtime.Now().Add(delay)
			g.epoch++
			g.openReason = normalizedDecision.Reason
			metrics.SetPostEmbeddingCircuitBreakerState("open")
			metrics.SetEmbeddingProviderPause(true, g.openReason, g.openUntil.Unix())
			metrics.RecordPostEmbeddingHalfOpenProbe("panic")
		}
		g.signalLocked()
		g.mu.Unlock()
	})
}

func (g *postEmbeddingProviderGate) providerRequestCanceled(permit *postEmbeddingProviderPermit) {
	if permit == nil || permit.gate != g {
		return
	}
	permit.once.Do(func() {
		g.mu.Lock()
		if g.inFlight > 0 {
			g.inFlight--
		}
		if permit.halfOpen && permit.epoch == g.epoch && g.state == postEmbeddingCircuitHalfOpen {
			g.state = postEmbeddingCircuitOpen
			delay := postEmbeddingRetryBaseCooldown
			g.openUntil = g.runtime.Now().Add(delay)
			g.epoch++
			if g.openReason == "" {
				g.openReason = providerReasonTransport
			}
			metrics.SetPostEmbeddingCircuitBreakerState("open")
			metrics.SetEmbeddingProviderPause(true, g.openReason, g.openUntil.Unix())
			metrics.RecordPostEmbeddingHalfOpenProbe("canceled")
		}
		g.signalLocked()
		g.mu.Unlock()
	})
}

func (g *postEmbeddingProviderGate) openLocked(decision ProviderDecision) {
	delay, normalizedDecision, _ := g.recovery.cooldownFor(&postEmbeddingProviderFailure{decision: decision})
	g.state = postEmbeddingCircuitOpen
	g.openReason = normalizedDecision.Reason
	g.openUntil = g.runtime.Now().Add(delay)
	g.epoch++
	g.signalLocked()
	metrics.SetPostEmbeddingCircuitBreakerState("open")
}

func (g *postEmbeddingProviderGate) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func classifyPostEmbeddingFailureScope(decision ProviderDecision, batchSize int) postEmbeddingFailureScope {
	if decision.Reason == providerReasonContract || decision.HTTPStatus == 413 || (decision.Action == ProviderActionSplitBatch && batchSize > 1) {
		return postEmbeddingFailureScopePartition
	}
	return postEmbeddingFailureScopeSharedOrUnknown
}

func shouldQuarantinePostEmbeddingFailure(scope postEmbeddingFailureScope, hasLocalEvidence bool) bool {
	return scope == postEmbeddingFailureScopeInstance && hasLocalEvidence
}
