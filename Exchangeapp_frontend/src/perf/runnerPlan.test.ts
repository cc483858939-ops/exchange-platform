import { describe, expect, it } from 'vitest';
import {
  createPerfScenarioPlans,
  PERF_APPEND_BASES,
  PERF_COUNTS,
  PERF_EXECUTIONS_PER_SCENARIO,
  PERF_RECORDED_RUNS,
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
});
