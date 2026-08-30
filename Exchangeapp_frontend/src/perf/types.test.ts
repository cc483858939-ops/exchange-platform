import { describe, expect, it } from 'vitest';
import {
  isPerfScenarioMessage,
  PERF_MESSAGE_NAMESPACE,
  PERF_SCENARIO_COMPLETE,
  PERF_SCENARIO_ERROR,
} from './types';

describe('perf postMessage protocol', () => {
  it('accepts namespaced scenario completion and error messages', () => {
    expect(isPerfScenarioMessage({
      namespace: PERF_MESSAGE_NAMESPACE,
      type: PERF_SCENARIO_COMPLETE,
      runId: 'scenario-1-run-1',
      result: {},
    })).toBe(true);
    expect(isPerfScenarioMessage({
      namespace: PERF_MESSAGE_NAMESPACE,
      type: PERF_SCENARIO_ERROR,
      runId: 'scenario-1-run-1',
      error: 'failed',
    })).toBe(true);
  });

  it('rejects messages with the wrong namespace, source shape, or run ID', () => {
    expect(isPerfScenarioMessage({
      namespace: 'other',
      type: PERF_SCENARIO_ERROR,
      runId: 'scenario-1-run-1',
      error: 'failed',
    })).toBe(false);
    expect(isPerfScenarioMessage({
      namespace: PERF_MESSAGE_NAMESPACE,
      type: PERF_SCENARIO_COMPLETE,
      runId: '',
      result: {},
    })).toBe(false);
    expect(isPerfScenarioMessage({
      namespace: PERF_MESSAGE_NAMESPACE,
      type: PERF_SCENARIO_COMPLETE,
      runId: 'scenario-1-run-1',
      result: null,
    })).toBe(false);
  });
});
