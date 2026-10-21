// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  calculateTelemetryRetryDelayMS,
  parseRetryAfterMS,
  TelemetryRetryGate,
} from './telemetryRetryGate';

describe('TelemetryRetryGate', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('parses Retry-After seconds, HTTP dates, expired dates, and invalid values safely', () => {
    const now = Date.parse('2026-10-21T07:28:00.000Z');
    expect(parseRetryAfterMS('60', null, now)).toBe(60_000);
    expect(parseRetryAfterMS('0', null, now)).toBe(0);
    expect(parseRetryAfterMS('Wed, 21 Oct 2026 07:29:00 GMT', 'Wed, 21 Oct 2026 07:28:00 GMT', now)).toBe(60_000);
    expect(parseRetryAfterMS('Wed, 21 Oct 2026 07:27:00 GMT', null, now)).toBe(0);
    expect(parseRetryAfterMS('not-a-date', null, now)).toBeNull();
    expect(parseRetryAfterMS('9007199254740991', null, now)).toBeNull();
  });

  it('uses bounded equal jitter with a testable random source', () => {
    expect(calculateTelemetryRetryDelayMS(0, () => 0)).toBe(1_000);
    expect(calculateTelemetryRetryDelayMS(0, () => 1)).toBe(2_000);
    expect(calculateTelemetryRetryDelayMS(20, () => 0.5)).toBe(225_000);
    expect(calculateTelemetryRetryDelayMS(Number.MAX_VALUE, () => 0)).toBe(150_000);
  });

  it('keeps one timer at the maximum Retry-After deadline and rechecks on fire', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    const onReady = vi.fn();
    const gate = new TelemetryRetryGate(onReady, vi.fn(), Date.now, () => 0);

    gate.recordFailure('60');
    gate.request(0);
    gate.request(1_000);
    expect(vi.getTimerCount()).toBe(1);
    expect(gate.canSend()).toBe(false);

    vi.advanceTimersByTime(59_999);
    expect(onReady).not.toHaveBeenCalled();
    expect(gate.canSend()).toBe(false);
    vi.advanceTimersByTime(1);
    expect(onReady).toHaveBeenCalledTimes(1);
    expect(gate.canSend()).toBe(true);
  });

  it('does not let an old success clear a newer failure epoch', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    const gate = new TelemetryRetryGate(vi.fn(), vi.fn(), Date.now, () => 0);
    const oldRequestEpoch = gate.captureEpoch();

    gate.recordFailure('60');
    const deadline = gate.snapshot().retryNotBeforeEpochMs;
    expect(gate.clearAfterSuccess(oldRequestEpoch)).toBe(false);
    expect(gate.snapshot()).toEqual({ retryNotBeforeEpochMs: deadline, retryAttempt: 1 });
  });
});
