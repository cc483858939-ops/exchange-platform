import { describe, expect, it } from 'vitest';
import type { PerfPendingScenarioEnvelope, PerfRawRun } from './types';

describe('performance result contracts', () => {
  it('keeps execution context separate from performance result metrics', () => {
    const context: PerfRawRun['executionContext'] = {
      topLevel: true,
      visibilityState: 'visible',
      devicePixelRatio: 1,
      userAgent: 'test-agent',
    };
    const pending: PerfPendingScenarioEnvelope = {
      schemaVersion: 2,
      suiteId: 'suite-1',
      runId: 'run-1',
      type: 'error',
      error: 'scenario failed',
    };

    expect(context.topLevel).toBe(true);
    expect(pending.schemaVersion).toBe(2);
    expect(pending.type).toBe('error');
  });
});
