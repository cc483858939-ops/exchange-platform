import { describe, expect, it } from 'vitest';
import {
  createPerfScenarioPlans,
  PERF_APPEND_BASES,
  PERF_COUNTS,
  PERF_EXECUTIONS_PER_SCENARIO,
  PERF_MAX_RECORDED_RETRIES,
  PERF_RECORDED_RUNS,
  retryRecordedAttempt,
} from './runnerPlan';

describe('performance runner plan', () => {
  it('covers the required matrix and append bases', () => {
    const plans = createPerfScenarioPlans();
    const matrixPlans = plans.filter(plan => plan.runType === 'matrix');
    const appendPlans = plans.filter(plan => plan.runType === 'append');

    expect(PERF_COUNTS).toEqual([20, 50, 100, 200, 300]);
    expect(PERF_APPEND_BASES).toEqual([20, 100, 200, 280]);
    expect(matrixPlans).toHaveLength(20);
    expect(appendPlans).toHaveLength(4);
    expect(plans.filter(plan => plan.appendFrom !== undefined).map(plan => plan.appendFrom)).toEqual([
      20, 100, 200, 280,
      20, 100, 200, 280,
      20, 100, 200, 280,
      20, 100, 200, 280,
    ]);
    expect(PERF_EXECUTIONS_PER_SCENARIO).toBe(PERF_RECORDED_RUNS + 1);
  });

  it('retries a timing-invalid recorded slot and accepts the next valid result', async () => {
    const attempts: number[] = [];
    const retry = await retryRecordedAttempt(
      async attemptNumber => {
        attempts.push(attemptNumber);
        return { valid: attemptNumber === 2, timingValid: attemptNumber === 2 };
      },
      result => result.valid && result.timingValid,
      result => !result.timingValid,
    );

    expect(attempts).toEqual([1, 2]);
    expect(retry.result?.valid).toBe(true);
    expect(retry.attempts).toBe(2);
    expect(retry.rejectedTimingAttempts).toBe(1);
  });

  it('does not fill a recorded slot when every timing attempt is invalid', async () => {
    const retry = await retryRecordedAttempt(
      async () => ({ valid: false, timingValid: false }),
      result => result.valid && result.timingValid,
      result => !result.timingValid,
    );

    expect(retry.result).toBeNull();
    expect(retry.attempts).toBe(PERF_MAX_RECORDED_RETRIES + 1);
    expect(retry.rejectedTimingAttempts).toBe(PERF_MAX_RECORDED_RETRIES + 1);
  });
});
