import { describe, expect, it } from 'vitest';
import {
  applyPendingExecution,
  completeSuite,
  createRunId,
  createSuiteSession,
  executionContextValidationIssues,
  getNextExecution,
  isPerfPendingScenarioEnvelope,
  isPerfTopLevelSuiteSession,
  isViewportWithinTolerance,
  PERF_MAX_COMPLETED_SLOTS,
  PERF_ZERO_RAF_FATAL_CODE,
  parsePendingScenarioEnvelope,
  parseSuiteSession,
  prepareNextExecution,
  serializePendingScenarioEnvelope,
  serializeSuiteSession,
} from './topLevelSuiteSession';
import { createPerfScenarioPlans } from './runnerPlan';
import type { PerfEnvironment, PerfPendingScenarioEnvelope, PerfRawRun } from './types';

const plans = createPerfScenarioPlans();
const environment: PerfEnvironment = {
  gitHead: 'test-head',
  timestamp: '2026-01-01T00:00:00.000Z',
  userAgent: 'test-agent',
  devicePixelRatio: 1,
  longTaskSupported: false,
  memorySupported: false,
};

const rawRun = (
  plan: (typeof plans)[number],
  options: {
    timingValid?: boolean;
    preflightSamples?: number;
    validationValid?: boolean;
    validationIssues?: string[];
    topLevel?: boolean;
    visibilityState?: DocumentVisibilityState;
    userAgent?: string;
    devicePixelRatio?: number;
  } = {},
): PerfRawRun => {
  const timingValid = options.timingValid ?? true;
  const preflightSamples = options.preflightSamples ?? 12;
  const timingIssues = timingValid ? [] : ['preflight invalid'];
  const appendMeasured = typeof plan.appendFrom === 'number';
  return {
    scenario: {
      viewport: plan.viewport,
      width: plan.width,
      height: plan.height,
      innerWidth: plan.width,
      innerHeight: plan.height,
      count: plan.count,
      trackView: plan.trackView,
      fixture: plan.fixture,
      runType: plan.runType,
    },
    executionContext: {
      topLevel: options.topLevel ?? true,
      visibilityState: options.visibilityState ?? 'visible',
      devicePixelRatio: options.devicePixelRatio ?? 1,
      userAgent: options.userAgent ?? environment.userAgent,
    },
    render: {
      requestedPosts: plan.count,
      postCards: plan.count,
      domElements: plan.count * 10,
      mountMs: 10,
    },
    append: {
      measured: appendMeasured,
      from: appendMeasured ? plan.appendFrom! : 0,
      to: appendMeasured ? plan.appendFrom! + 20 : 0,
      durationMs: appendMeasured ? 10 : 0,
    },
    scroll: {
      samples: 100,
      medianFrameMs: 16,
      p95FrameMs: 20,
      maxFrameMs: 30,
      framesOver34Ms: 0,
      framesOver50Ms: 0,
      percentOver50Ms: 0,
    },
    longTasks: { supported: false },
    memory: { supported: false },
    observer: {
      supported: true,
      instancesCreated: 1,
      observeCalls: plan.trackView ? plan.count : 0,
      unobserveCalls: plan.trackView ? plan.count : 0,
      targetsBeforeCleanup: plan.trackView ? plan.count : 0,
      currentTargets: 0,
      peakTargets: plan.trackView ? plan.count : 0,
    },
    timingHealth: {
      valid: timingValid,
      preflight: {
        samples: preflightSamples,
        medianMs: timingValid ? 16 : 0,
        p95Ms: timingValid ? 16 : 0,
        maxMs: timingValid ? 16 : 0,
      },
      postflight: {
        samples: timingValid ? 24 : 0,
        medianMs: timingValid ? 16 : 0,
        p95Ms: timingValid ? 16 : 0,
        maxMs: timingValid ? 16 : 0,
      },
      visibilityLost: false,
      issues: timingIssues,
    },
    validation: {
      valid: options.validationValid ?? timingValid,
      issues: options.validationIssues ?? timingIssues,
      telemetryNetworkRequests: 0,
    },
  };
};

const newSession = () => createSuiteSession({
  suiteId: 'suite-1',
  harnessHead: environment.gitHead,
  environment,
  createdAt: environment.timestamp,
});

const pendingFor = (
  session: ReturnType<typeof newSession>,
  result: PerfRawRun,
  type: 'result' | 'error' = 'result',
): PerfPendingScenarioEnvelope => ({
  schemaVersion: 2,
  suiteId: session.suiteId,
  runId: session.expectedRunId!,
  type,
  ...(type === 'result' ? { result } : { error: 'scenario failed' }),
});

const prepare = (current: ReturnType<typeof newSession>) => {
  const prepared = prepareNextExecution(current, plans);
  expect(prepared.execution).not.toBeNull();
  return prepared;
};

const apply = (
  current: ReturnType<typeof newSession>,
  result: PerfRawRun,
  type: 'result' | 'error' = 'result',
) => {
  const applied = applyPendingExecution(current, pendingFor(current, result, type), plans);
  expect(applied.rejected).toBe(false);
  return applied.session;
};

describe('top-level suite session state machine', () => {
  it('creates the required initial cursor and round-trips v2 JSON', () => {
    const session = newSession();
    expect(session).toMatchObject({
      schemaVersion: 2,
      suiteId: 'suite-1',
      status: 'running',
      planIndex: 0,
      phase: 'warmup',
      recordedIndex: 0,
      attemptNumber: 1,
      expectedRunId: null,
      rawRuns: [],
      failures: [],
      rejectedTimingAttempts: 0,
      completedSlots: 0,
      processedRunIds: [],
      consecutiveZeroRafAttempts: 0,
    });
    expect(parseSuiteSession(serializeSuiteSession(session))).toEqual(session);
    expect(isPerfTopLevelSuiteSession(session)).toBe(true);
  });

  it('rejects invalid JSON, incompatible schemas, missing suite IDs, and invalid cursors', () => {
    const session = newSession();
    expect(parseSuiteSession('{not-json')).toBeNull();
    expect(parseSuiteSession(JSON.stringify({ ...session, schemaVersion: 1 }))).toBeNull();
    expect(parseSuiteSession(JSON.stringify({ ...session, suiteId: '' }))).toBeNull();
    expect(parseSuiteSession(JSON.stringify({ ...session, planIndex: -1 }))).toBeNull();
    expect(parseSuiteSession(JSON.stringify({ ...session, phase: 'unknown' }))).toBeNull();
  });

  it('derives deterministic IDs from the persisted cursor', () => {
    const session = newSession();
    const warmup = getNextExecution(session, plans);
    expect(warmup?.runId).toBe('suite-suite-1-plan-1-warmup');
    expect(createRunId('suite-1', 0, 'recorded', 0, 2))
      .toBe('suite-suite-1-plan-1-recorded-1-attempt-2');
  });

  it('accepts matching pending results and rejects wrong suite or run IDs', () => {
    const session = newSession();
    const prepared = prepare(session);
    const current = prepared.session;
    const result = rawRun(prepared.execution!.plan);
    const accepted = applyPendingExecution(current, pendingFor(current, result), plans);
    expect(accepted.accepted).toBe(true);

    const wrongSuite = applyPendingExecution(current, {
      ...pendingFor(current, result),
      suiteId: 'other-suite',
    }, plans);
    expect(wrongSuite.rejected).toBe(true);

    const wrongRun = applyPendingExecution(current, {
      ...pendingFor(current, result),
      runId: 'other-run',
    }, plans);
    expect(wrongRun.rejected).toBe(true);
  });

  it('consumes duplicate pending results idempotently', () => {
    const initial = prepare(newSession());
    const result = rawRun(initial.execution!.plan);
    const first = applyPendingExecution(initial.session, pendingFor(initial.session, result), plans);
    const duplicate = applyPendingExecution(first.session, pendingFor(initial.session, result), plans);

    expect(first.session.rawRuns).toHaveLength(0);
    expect(duplicate.duplicate).toBe(true);
    expect(duplicate.session.rawRuns).toHaveLength(0);
    expect(duplicate.session.processedRunIds).toEqual(first.session.processedRunIds);
  });

  it('transitions a valid warm-up to recorded slot zero', () => {
    const prepared = prepare(newSession());
    const next = apply(prepared.session, rawRun(prepared.execution!.plan));
    expect(next.phase).toBe('recorded');
    expect(next.recordedIndex).toBe(0);
    expect(next.completedSlots).toBe(1);
    expect(next.rawRuns).toHaveLength(0);
  });

  it('does not retry a timing-invalid warm-up', () => {
    const prepared = prepare(newSession());
    const next = apply(prepared.session, rawRun(prepared.execution!.plan, { timingValid: false }));
    expect(next.phase).toBe('recorded');
    expect(next.recordedIndex).toBe(0);
    expect(next.attemptNumber).toBe(1);
    expect(next.completedSlots).toBe(1);
    expect(next.rejectedTimingAttempts).toBe(1);
  });

  it('accepts a recorded result and advances to the next recorded slot', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0]));
    const prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan));
    expect(current.phase).toBe('recorded');
    expect(current.recordedIndex).toBe(1);
    expect(current.completedSlots).toBe(2);
    expect(current.rawRuns).toHaveLength(1);
  });

  it('retries a timing-invalid recorded slot through the persisted attempt cursor', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0]));
    let prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan, { timingValid: false }));
    expect(current.recordedIndex).toBe(0);
    expect(current.attemptNumber).toBe(2);
    expect(current.completedSlots).toBe(1);
    expect(current.rejectedTimingAttempts).toBe(1);

    prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan));
    expect(current.recordedIndex).toBe(1);
    expect(current.completedSlots).toBe(2);
    expect(current.rawRuns).toHaveLength(1);
  });

  it('records a failure after the recorded timing retry budget is exhausted', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0]));
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      const prepared = prepare(current);
      current = apply(prepared.session, rawRun(prepared.execution!.plan, { timingValid: false }));
    }
    expect(current.recordedIndex).toBe(1);
    expect(current.completedSlots).toBe(2);
    expect(current.rejectedTimingAttempts).toBe(3);
    expect(current.failures).toHaveLength(1);
  });

  it('does not timing-retry a validation failure with healthy timing', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0]));
    const prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan, {
      validationValid: false,
      validationIssues: ['observer cleanup failed'],
    }));
    expect(current.recordedIndex).toBe(1);
    expect(current.attemptNumber).toBe(1);
    expect(current.completedSlots).toBe(2);
    expect(current.rejectedTimingAttempts).toBe(0);
    expect(current.failures).toHaveLength(1);
  });

  it('persists the zero-RAF streak and fails on the second consecutive valid-context attempt', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0], {
      timingValid: false,
      preflightSamples: 0,
    }));
    expect(current.consecutiveZeroRafAttempts).toBe(1);

    const prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan, {
      timingValid: false,
      preflightSamples: 0,
    }));
    expect(current.status).toBe('failed');
    expect(current.fatalCode).toBe(PERF_ZERO_RAF_FATAL_CODE);
    expect(current.consecutiveZeroRafAttempts).toBe(2);
    expect(getNextExecution(current, plans)).toBeNull();
  });

  it('resets the zero-RAF streak when a later attempt has nonzero preflight samples', () => {
    let current = apply(prepare(newSession()).session, rawRun(plans[0], {
      timingValid: false,
      preflightSamples: 0,
    }));
    const prepared = prepare(current);
    current = apply(prepared.session, rawRun(prepared.execution!.plan));
    expect(current.consecutiveZeroRafAttempts).toBe(0);
    expect(current.status).toBe('running');
  });

  it('rejects child contexts from accepted execution', () => {
    const prepared = prepare(newSession());
    const run = rawRun(prepared.execution!.plan, { topLevel: false });
    const issues = executionContextValidationIssues(run, environment);
    expect(issues).toContain('Performance scenario must execute as a top-level document.');
    const next = apply(prepared.session, run);
    expect(next.rawRuns).toHaveLength(0);
    expect(next.failures).toHaveLength(1);
  });

  it('validates the shared viewport helper and environment consistency', () => {
    expect(isViewportWithinTolerance(1280, 720, 390, 844)).toBe(false);
    expect(isViewportWithinTolerance(392, 840, 390, 844)).toBe(true);
    expect(isViewportWithinTolerance(399, 844, 390, 844)).toBe(false);

    const run = rawRun(plans[0]);
    expect(executionContextValidationIssues(run, environment)).toEqual([]);
    expect(executionContextValidationIssues(
      rawRun(plans[0], { userAgent: 'changed-agent' }),
      environment,
    )).toContain('userAgent changed during the performance suite');
    expect(executionContextValidationIssues(
      rawRun(plans[0], { devicePixelRatio: 2 }),
      environment,
    )).toContain('devicePixelRatio changed during the performance suite');
  });

  it('round-trips and validates pending v2 envelopes', () => {
    const prepared = prepare(newSession());
    const pending = pendingFor(prepared.session, rawRun(prepared.execution!.plan));
    expect(parsePendingScenarioEnvelope(serializePendingScenarioEnvelope(pending))).toEqual(pending);
    expect(isPerfPendingScenarioEnvelope(pending)).toBe(true);
    expect(parsePendingScenarioEnvelope(JSON.stringify({ ...pending, schemaVersion: 1 }))).toBeNull();
    expect(parsePendingScenarioEnvelope(JSON.stringify({ ...pending, suiteId: '' }))).toBeNull();
  });

  it('simulates all logical slots without letting retries inflate progress', () => {
    let current = newSession();
    for (let planIndex = 0; planIndex < plans.length; planIndex += 1) {
      const warmup = prepare(current);
      expect(warmup.execution?.planIndex).toBe(planIndex);
      current = apply(warmup.session, rawRun(warmup.execution!.plan));
      for (let recordedIndex = 0; recordedIndex < 3; recordedIndex += 1) {
        const recorded = prepare(current);
        expect(recorded.execution?.recordedIndex).toBe(recordedIndex);
        current = apply(recorded.session, rawRun(recorded.execution!.plan));
      }
    }
    current = completeSuite(current);
    expect(current.status).toBe('completed');
    expect(current.completedSlots).toBe(PERF_MAX_COMPLETED_SLOTS);
    expect(current.completedSlots).toBe(96);
    expect(current.rawRuns).toHaveLength(72);
    expect(current.failures).toHaveLength(0);
  });
});
