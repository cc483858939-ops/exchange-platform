import {
  PERF_EXECUTIONS_PER_SCENARIO,
  PERF_MAX_RECORDED_RETRIES,
  PERF_RECORDED_RUNS,
  scenarioPlanLabel,
  type PerfScenarioPlan,
} from './runnerPlan';
import { viewportDefaults } from './scenarioConfig';
import type {
  PerfEnvironment,
  PerfPendingScenarioEnvelope,
  PerfRawRun,
  PerfScenarioFailure,
  PerfSuitePhase,
  PerfSuiteResult,
  PerfSuiteStatus,
  PerfViewport,
  PerfViewportRequirement,
} from './types';

export const PERF_SESSION_SCHEMA_VERSION = 2 as const;
export const PERF_SESSION_STORAGE_KEY = 'exchange.perf.long-list.v2.session';
export const PERF_PENDING_STORAGE_KEY = 'exchange.perf.long-list.v2.pending';
export const PERF_STORAGE_PROBE_KEY = 'exchange.perf.long-list.v2.probe';
export const PERF_VIEWPORT_TOLERANCE = 8;
export const PERF_DPR_TOLERANCE = 0.01;
export const PERF_ZERO_RAF_FATAL_CODE = 'TOP_LEVEL_RAF_UNAVAILABLE';
export const PERF_PLAN_COUNT = 24;
export const PERF_MAX_COMPLETED_SLOTS = PERF_PLAN_COUNT * PERF_EXECUTIONS_PER_SCENARIO;

export interface PerfTopLevelSuiteSession {
  schemaVersion: typeof PERF_SESSION_SCHEMA_VERSION;
  suiteId: string;
  status: PerfSuiteStatus;
  harnessHead: string;
  environment: PerfEnvironment;
  planIndex: number;
  phase: PerfSuitePhase;
  recordedIndex: number;
  attemptNumber: number;
  expectedRunId: string | null;
  rawRuns: PerfRawRun[];
  failures: PerfScenarioFailure[];
  rejectedTimingAttempts: number;
  completedSlots: number;
  processedRunIds: string[];
  consecutiveZeroRafAttempts: number;
  fatalCode: string | null;
  fatalError: string | null;
  createdAt: string;
}

export interface PerfScenarioExecution {
  plan: PerfScenarioPlan;
  planIndex: number;
  phase: PerfSuitePhase;
  recordedIndex: number;
  attemptNumber: number;
  runId: string;
}

export interface PerfPendingApplyResult {
  session: PerfTopLevelSuiteSession;
  accepted: boolean;
  duplicate: boolean;
  rejected: boolean;
  error: string | null;
}

export interface CreateSuiteSessionOptions {
  suiteId: string;
  harnessHead: string;
  environment: PerfEnvironment;
  createdAt?: string;
}

export const isViewportWithinTolerance = (
  actualWidth: number,
  actualHeight: number,
  targetWidth: number,
  targetHeight: number,
  tolerance = PERF_VIEWPORT_TOLERANCE,
): boolean => (
  Number.isFinite(actualWidth)
  && Number.isFinite(actualHeight)
  && Math.abs(actualWidth - targetWidth) <= tolerance
  && Math.abs(actualHeight - targetHeight) <= tolerance
);

export const perfViewportRequirement = (viewport: PerfViewport): PerfViewportRequirement => ({
  viewport,
  width: viewportDefaults[viewport].width,
  height: viewportDefaults[viewport].height,
  tolerance: PERF_VIEWPORT_TOLERANCE,
});

export const isScenarioViewportValid = (scenario: PerfRawRun['scenario']): boolean => {
  const target = viewportDefaults[scenario.viewport];
  return isViewportWithinTolerance(
    scenario.innerWidth,
    scenario.innerHeight,
    target.width,
    target.height,
  );
};

export const isExecutionEnvironmentConsistent = (
  run: PerfRawRun,
  environment: PerfEnvironment,
): boolean => (
  run.executionContext.userAgent === environment.userAgent
  && Number.isFinite(run.executionContext.devicePixelRatio)
  && Math.abs(run.executionContext.devicePixelRatio - environment.devicePixelRatio)
    <= PERF_DPR_TOLERANCE
);

export const executionContextValidationIssues = (
  run: PerfRawRun,
  environment: PerfEnvironment,
): string[] => {
  const issues: string[] = [];
  if (!run.executionContext.topLevel) {
    issues.push('Performance scenario must execute as a top-level document.');
  }
  if (run.executionContext.visibilityState !== 'visible') {
    issues.push('Performance scenario must execute while document.visibilityState=visible.');
  }
  if (!isScenarioViewportValid(run.scenario)) {
    const target = viewportDefaults[run.scenario.viewport];
    issues.push(
      `viewport ${run.scenario.innerWidth}x${run.scenario.innerHeight} is outside `
      + `the ${run.scenario.viewport} target ${target.width}x${target.height} ±${PERF_VIEWPORT_TOLERANCE}px`,
    );
  }
  if (run.executionContext.userAgent !== environment.userAgent) {
    issues.push('userAgent changed during the performance suite');
  }
  if (!Number.isFinite(run.executionContext.devicePixelRatio)
    || Math.abs(run.executionContext.devicePixelRatio - environment.devicePixelRatio)
      > PERF_DPR_TOLERANCE) {
    issues.push('devicePixelRatio changed during the performance suite');
  }
  return issues;
};

function buildInitialSession(options: CreateSuiteSessionOptions): PerfTopLevelSuiteSession {
  return {
    schemaVersion: PERF_SESSION_SCHEMA_VERSION,
    suiteId: options.suiteId,
    status: 'running',
    harnessHead: options.harnessHead,
    environment: options.environment,
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
    fatalCode: null,
    fatalError: null,
    createdAt: options.createdAt ?? new Date().toISOString(),
  };
}

export function createSuiteSession(options: CreateSuiteSessionOptions): PerfTopLevelSuiteSession;
export function createSuiteSession(
  suiteId: string,
  harnessHead: string,
  environment: PerfEnvironment,
  createdAt?: string,
): PerfTopLevelSuiteSession;
export function createSuiteSession(
  optionsOrSuiteId: CreateSuiteSessionOptions | string,
  harnessHead?: string,
  environment?: PerfEnvironment,
  createdAt?: string,
): PerfTopLevelSuiteSession {
  const options: CreateSuiteSessionOptions = typeof optionsOrSuiteId === 'string'
    ? {
        suiteId: optionsOrSuiteId,
        harnessHead: harnessHead ?? 'unknown',
        environment: environment as PerfEnvironment,
        createdAt,
      }
    : optionsOrSuiteId;
  return buildInitialSession(options);
}

export const serializeSuiteSession = (session: PerfTopLevelSuiteSession): string => (
  JSON.stringify(session)
);

const isRecord = (value: unknown): value is Record<string, unknown> => (
  Boolean(value) && typeof value === 'object'
);

const isNonEmptyString = (value: unknown): value is string => (
  typeof value === 'string' && value.trim().length > 0
);

const isNonNegativeInteger = (value: unknown): value is number => (
  Number.isSafeInteger(value) && (value as number) >= 0
);

const isFiniteNumber = (value: unknown): value is number => (
  typeof value === 'number' && Number.isFinite(value)
);

const isStringArray = (value: unknown): value is string[] => (
  Array.isArray(value) && value.every(item => typeof item === 'string')
);

const hasFiniteFields = (
  value: Record<string, unknown>,
  fields: readonly string[],
): boolean => fields.every(field => isFiniteNumber(value[field]));

export const isPerfRawRun = (value: unknown): value is PerfRawRun => {
  if (!isRecord(value)
    || !isRecord(value.scenario)
    || !isRecord(value.executionContext)
    || !isRecord(value.render)
    || !isRecord(value.append)
    || !isRecord(value.scroll)
    || !isRecord(value.longTasks)
    || !isRecord(value.memory)
    || !isRecord(value.observer)
    || !isRecord(value.timingHealth)
    || !isRecord(value.validation)) {
    return false;
  }
  const scenario = value.scenario;
  const context = value.executionContext;
  const render = value.render;
  const append = value.append;
  const scroll = value.scroll;
  const observer = value.observer;
  const timing = value.timingHealth;
  const validation = value.validation;
  const preflight = isRecord(timing.preflight) ? timing.preflight : null;
  const postflight = isRecord(timing.postflight) ? timing.postflight : null;
  return (scenario.viewport === 'desktop' || scenario.viewport === 'mobile')
    && isFiniteNumber(scenario.width)
    && isFiniteNumber(scenario.height)
    && isFiniteNumber(scenario.innerWidth)
    && isFiniteNumber(scenario.innerHeight)
    && isNonNegativeInteger(scenario.count)
    && typeof scenario.trackView === 'boolean'
    && (scenario.fixture === 'mixed' || scenario.fixture === 'text-only')
    && (scenario.runType === 'matrix' || scenario.runType === 'append')
    && typeof context.topLevel === 'boolean'
    && (context.visibilityState === 'visible'
      || context.visibilityState === 'hidden'
      || context.visibilityState === 'prerender'
      || context.visibilityState === 'unloaded')
    && isFiniteNumber(context.devicePixelRatio)
    && context.devicePixelRatio > 0
    && isNonEmptyString(context.userAgent)
    && hasFiniteFields(render, ['requestedPosts', 'postCards', 'domElements', 'mountMs'])
    && typeof append.measured === 'boolean'
    && hasFiniteFields(append, ['from', 'to', 'durationMs'])
    && hasFiniteFields(scroll, [
      'samples',
      'medianFrameMs',
      'p95FrameMs',
      'maxFrameMs',
      'framesOver34Ms',
      'framesOver50Ms',
      'percentOver50Ms',
    ])
    && typeof value.longTasks.supported === 'boolean'
    && typeof value.memory.supported === 'boolean'
    && hasFiniteFields(observer, [
      'instancesCreated',
      'observeCalls',
      'unobserveCalls',
      'targetsBeforeCleanup',
      'currentTargets',
      'peakTargets',
    ])
    && typeof timing.valid === 'boolean'
    && preflight !== null
    && postflight !== null
    && hasFiniteFields(preflight, ['samples', 'medianMs', 'p95Ms', 'maxMs'])
    && hasFiniteFields(postflight, ['samples', 'medianMs', 'p95Ms', 'maxMs'])
    && typeof timing.visibilityLost === 'boolean'
    && isStringArray(timing.issues)
    && typeof validation.valid === 'boolean'
    && isStringArray(validation.issues)
    && isNonNegativeInteger(validation.telemetryNetworkRequests);
};

const isEnvironment = (value: unknown): value is PerfEnvironment => {
  if (!isRecord(value)) return false;
  return isNonEmptyString(value.gitHead)
    && isNonEmptyString(value.timestamp)
    && isNonEmptyString(value.userAgent)
    && typeof value.devicePixelRatio === 'number'
    && Number.isFinite(value.devicePixelRatio)
    && value.devicePixelRatio > 0
    && typeof value.longTaskSupported === 'boolean'
    && typeof value.memorySupported === 'boolean'
    && (value.platform === undefined || typeof value.platform === 'string')
    && (value.hardwareConcurrency === undefined
      || (typeof value.hardwareConcurrency === 'number'
        && Number.isFinite(value.hardwareConcurrency)))
    && (value.deviceMemory === undefined
      || (typeof value.deviceMemory === 'number' && Number.isFinite(value.deviceMemory)));
};

const isValidCursor = (value: Record<string, unknown>): boolean => {
  if (!isNonNegativeInteger(value.planIndex)
    || !isNonNegativeInteger(value.recordedIndex)
    || !isNonNegativeInteger(value.attemptNumber)
    || value.attemptNumber < 1
    || value.attemptNumber > PERF_MAX_RECORDED_RETRIES + 1
    || value.planIndex > PERF_PLAN_COUNT
    || value.recordedIndex >= PERF_RECORDED_RUNS) {
    return false;
  }
  if (value.phase !== 'warmup' && value.phase !== 'recorded') return false;
  return value.phase === 'warmup'
    ? value.recordedIndex === 0 && value.attemptNumber === 1
    : true;
};

export const isPerfTopLevelSuiteSession = (
  value: unknown,
): value is PerfTopLevelSuiteSession => {
  if (!isRecord(value)
    || value.schemaVersion !== PERF_SESSION_SCHEMA_VERSION
    || !isNonEmptyString(value.suiteId)
    || !isNonEmptyString(value.harnessHead)
    || !isEnvironment(value.environment)
    || !isValidCursor(value)
    || !isNonEmptyString(value.createdAt)
    || !['running', 'waiting-for-viewport', 'completed', 'failed'].includes(String(value.status))
    || !Array.isArray(value.rawRuns)
    || value.rawRuns.length > 24 * PERF_RECORDED_RUNS
    || !value.rawRuns.every(isPerfRawRun)
    || !Array.isArray(value.failures)
    || !value.failures.every(value => (
      isRecord(value)
      && isNonEmptyString(value.scenario)
      && (value.phase === 'warm-up' || value.phase === 'recorded')
      && isNonEmptyString(value.error)
    ))
    || !Array.isArray(value.processedRunIds)
    || !value.processedRunIds.every(isNonEmptyString)
    || new Set(value.processedRunIds).size !== value.processedRunIds.length
    || !isNonNegativeInteger(value.rejectedTimingAttempts)
    || !isNonNegativeInteger(value.completedSlots)
    || value.completedSlots > PERF_MAX_COMPLETED_SLOTS
    || !isNonNegativeInteger(value.consecutiveZeroRafAttempts)
    || value.fatalCode !== null && !isNonEmptyString(value.fatalCode)
    || value.fatalError !== null && !isNonEmptyString(value.fatalError)
    || value.expectedRunId !== null && !isNonEmptyString(value.expectedRunId)) {
    return false;
  }
  if (value.status === 'completed'
    && (value.planIndex !== PERF_PLAN_COUNT || value.completedSlots !== PERF_MAX_COMPLETED_SLOTS)) {
    return false;
  }
  if (value.planIndex === PERF_PLAN_COUNT && value.completedSlots !== PERF_MAX_COMPLETED_SLOTS) {
    return false;
  }
  return true;
};

export const parseSuiteSession = (serialized: string | null): PerfTopLevelSuiteSession | null => {
  if (typeof serialized !== 'string' || serialized.trim() === '') return null;
  try {
    const parsed: unknown = JSON.parse(serialized);
    return isPerfTopLevelSuiteSession(parsed) ? parsed : null;
  } catch {
    return null;
  }
};

export const serializePendingScenarioEnvelope = (envelope: PerfPendingScenarioEnvelope): string => (
  JSON.stringify(envelope)
);

export const isPerfPendingScenarioEnvelope = (
  value: unknown,
): value is PerfPendingScenarioEnvelope => {
  if (!isRecord(value)
    || value.schemaVersion !== PERF_SESSION_SCHEMA_VERSION
    || !isNonEmptyString(value.suiteId)
    || !isNonEmptyString(value.runId)
    || (value.type !== 'result' && value.type !== 'error')) {
    return false;
  }
  if (value.type === 'result') return isPerfRawRun(value.result);
  return isNonEmptyString(value.error);
};

export const parsePendingScenarioEnvelope = (
  serialized: string | null,
): PerfPendingScenarioEnvelope | null => {
  if (typeof serialized !== 'string' || serialized.trim() === '') return null;
  try {
    const parsed: unknown = JSON.parse(serialized);
    return isPerfPendingScenarioEnvelope(parsed) ? parsed : null;
  } catch {
    return null;
  }
};

export const createRunId = (
  suiteId: string,
  planIndex: number,
  phase: PerfSuitePhase,
  recordedIndex = 0,
  attemptNumber = 1,
): string => {
  const planLabel = `plan-${planIndex + 1}`;
  if (phase === 'warmup') return `suite-${suiteId}-${planLabel}-warmup`;
  return `suite-${suiteId}-${planLabel}-recorded-${recordedIndex + 1}-attempt-${attemptNumber}`;
};

export const getNextExecution = (
  session: PerfTopLevelSuiteSession,
  plans: readonly PerfScenarioPlan[],
): PerfScenarioExecution | null => {
  if (session.status !== 'running' || session.planIndex >= plans.length) return null;
  const plan = plans[session.planIndex];
  if (!plan) return null;
  return {
    plan,
    planIndex: session.planIndex,
    phase: session.phase,
    recordedIndex: session.recordedIndex,
    attemptNumber: session.attemptNumber,
    runId: createRunId(
      session.suiteId,
      session.planIndex,
      session.phase,
      session.recordedIndex,
      session.attemptNumber,
    ),
  };
};

export const prepareNextExecution = (
  session: PerfTopLevelSuiteSession,
  plans: readonly PerfScenarioPlan[],
): { session: PerfTopLevelSuiteSession; execution: PerfScenarioExecution | null } => {
  const execution = getNextExecution(session, plans);
  if (!execution) return { session, execution: null };
  return {
    session: {
      ...session,
      status: 'running',
      expectedRunId: execution.runId,
      fatalCode: null,
      fatalError: null,
    },
    execution,
  };
};

const withProcessedRun = (
  session: PerfTopLevelSuiteSession,
  runId: string,
): PerfTopLevelSuiteSession => ({
  ...session,
  processedRunIds: session.processedRunIds.includes(runId)
    ? session.processedRunIds
    : [...session.processedRunIds, runId],
});

const scenarioFailure = (
  plan: PerfScenarioPlan,
  phase: PerfSuitePhase,
  error: string,
): PerfScenarioFailure => ({
  scenario: scenarioPlanLabel(plan),
  phase: phase === 'warmup' ? 'warm-up' : 'recorded',
  error,
});

const failureText = (run: PerfRawRun, fallback: string): string => (
  run.validation.issues.join('; ') || fallback
);

const hasNonTimingValidationIssue = (run: PerfRawRun): boolean => {
  const timingIssues = new Set(run.timingHealth.issues);
  return run.validation.issues.some(issue => !timingIssues.has(issue));
};

const isZeroRafAttempt = (run: PerfRawRun): boolean => (
  run.executionContext.topLevel
  && run.executionContext.visibilityState === 'visible'
  && isScenarioViewportValid(run.scenario)
  && !run.timingHealth.visibilityLost
  && run.timingHealth.preflight.samples === 0
);

const advanceRecordedCursor = (
  session: PerfTopLevelSuiteSession,
  plans: readonly PerfScenarioPlan[],
): PerfTopLevelSuiteSession => {
  const nextRecordedIndex = session.recordedIndex + 1;
  if (nextRecordedIndex < PERF_RECORDED_RUNS) {
    return {
      ...session,
      phase: 'recorded',
      recordedIndex: nextRecordedIndex,
      attemptNumber: 1,
    };
  }
  const nextPlanIndex = session.planIndex + 1;
  return {
    ...session,
    planIndex: nextPlanIndex,
    phase: 'warmup',
    recordedIndex: 0,
    attemptNumber: 1,
    status: nextPlanIndex >= plans.length ? 'running' : session.status,
  };
};

const incrementCompletedSlot = (session: PerfTopLevelSuiteSession): number => (
  Math.min(PERF_MAX_COMPLETED_SLOTS, session.completedSlots + 1)
);

const updateZeroRafStreak = (
  session: PerfTopLevelSuiteSession,
  run: PerfRawRun,
): PerfTopLevelSuiteSession => {
  if (isZeroRafAttempt(run)) {
    const streak = session.consecutiveZeroRafAttempts + 1;
    return {
      ...session,
      consecutiveZeroRafAttempts: streak,
      ...(streak >= 2
        ? {
            status: 'failed' as const,
            fatalCode: PERF_ZERO_RAF_FATAL_CODE,
            fatalError: 'Visible top-level scenarios received no requestAnimationFrame callbacks. '
              + 'This browser execution surface is unsuitable for the Spec N benchmark.',
          }
        : {}),
    };
  }
  if (run.timingHealth.preflight.samples > 0) {
    return { ...session, consecutiveZeroRafAttempts: 0 };
  }
  return session;
};

const applyScenarioResult = (
  inputSession: PerfTopLevelSuiteSession,
  pending: PerfPendingScenarioEnvelope,
  run: PerfRawRun,
  plans: readonly PerfScenarioPlan[],
): PerfTopLevelSuiteSession => {
  const plan = plans[inputSession.planIndex];
  if (!plan) {
    return {
      ...inputSession,
      status: 'failed',
      fatalCode: 'INVALID_SESSION_CURSOR',
      fatalError: 'The performance suite cursor points beyond the scenario plan.',
    };
  }

  let session = withProcessedRun(inputSession, pending.runId);
  session = updateZeroRafStreak(session, run);
  if (session.status === 'failed') return session;

  const contextIssues = executionContextValidationIssues(run, session.environment);
  const appendMatches = typeof plan.appendFrom === 'number'
    ? run.append.measured && run.append.from === plan.appendFrom
    : !run.append.measured;
  const planMatches = run.scenario.viewport === plan.viewport
    && run.scenario.width === plan.width
    && run.scenario.height === plan.height
    && run.scenario.count === plan.count
    && run.scenario.trackView === plan.trackView
    && run.scenario.fixture === plan.fixture
    && run.scenario.runType === plan.runType
    && appendMatches;
  if (!planMatches) contextIssues.push('Scenario result does not match the active plan.');

  const validationIssues = Array.from(new Set([
    ...run.validation.issues,
    ...contextIssues,
  ]));
  const validationValid = run.validation.valid && validationIssues.length === 0;
  const timingValid = run.timingHealth.valid;

  if (inputSession.phase === 'warmup') {
    session = {
      ...session,
      completedSlots: incrementCompletedSlot(session),
    };
    if (!timingValid) {
      return {
        ...session,
        rejectedTimingAttempts: session.rejectedTimingAttempts + 1,
        phase: 'recorded',
        recordedIndex: 0,
        attemptNumber: 1,
      };
    }
    if (!validationValid) {
      return {
        ...advanceRecordedCursor({
          ...session,
          failures: [
            ...session.failures,
            scenarioFailure(plan, 'warmup', failureText(run, validationIssues.join('; ') || 'warm-up validation failed')),
          ],
        }, plans),
        phase: 'recorded',
        recordedIndex: 0,
        attemptNumber: 1,
      };
    }
    return {
      ...session,
      phase: 'recorded',
      recordedIndex: 0,
      attemptNumber: 1,
    };
  }

  if (!timingValid) {
    const rejected = session.rejectedTimingAttempts + 1;
    const retryBlockedByContext = contextIssues.some(issue => (
      !issue.startsWith('Performance scenario must execute while document.visibilityState=visible.')
    ));
    const retryAllowed = !hasNonTimingValidationIssue(run) && !retryBlockedByContext;
    if (retryAllowed && inputSession.attemptNumber <= PERF_MAX_RECORDED_RETRIES) {
      return {
        ...session,
        rejectedTimingAttempts: rejected,
        attemptNumber: inputSession.attemptNumber + 1,
      };
    }
    const exhausted = inputSession.attemptNumber >= PERF_MAX_RECORDED_RETRIES + 1;
    return {
      ...advanceRecordedCursor({
        ...session,
        rejectedTimingAttempts: rejected,
        completedSlots: incrementCompletedSlot(session),
        failures: exhausted || !retryAllowed
          ? [
              ...session.failures,
              scenarioFailure(
                plan,
                'recorded',
                `${failureText(run, 'no timing-valid result was recorded')} (after ${inputSession.attemptNumber} attempt${inputSession.attemptNumber === 1 ? '' : 's'})`,
              ),
            ]
          : session.failures,
      }, plans),
      rejectedTimingAttempts: rejected,
    };
  }

  if (!validationValid) {
    return advanceRecordedCursor({
      ...session,
      completedSlots: incrementCompletedSlot(session),
      failures: [
        ...session.failures,
        scenarioFailure(plan, 'recorded', failureText(run, validationIssues.join('; ') || 'recorded validation failed')),
      ],
    }, plans);
  }

  return advanceRecordedCursor({
    ...session,
    rawRuns: [...session.rawRuns, run],
    completedSlots: incrementCompletedSlot(session),
  }, plans);
};

const applyScenarioError = (
  inputSession: PerfTopLevelSuiteSession,
  pending: PerfPendingScenarioEnvelope,
  plans: readonly PerfScenarioPlan[],
): PerfTopLevelSuiteSession => {
  const plan = plans[inputSession.planIndex];
  if (!plan) return inputSession;
  const session = {
    ...withProcessedRun(inputSession, pending.runId),
    completedSlots: incrementCompletedSlot(inputSession),
    failures: [
      ...inputSession.failures,
      scenarioFailure(plan, inputSession.phase, pending.error ?? 'scenario failed'),
    ],
  };
  return inputSession.phase === 'warmup'
    ? {
        ...session,
        phase: 'recorded',
        recordedIndex: 0,
        attemptNumber: 1,
      }
    : advanceRecordedCursor(session, plans);
};

export const applyPendingExecution = (
  session: PerfTopLevelSuiteSession,
  pending: PerfPendingScenarioEnvelope,
  plans: readonly PerfScenarioPlan[],
): PerfPendingApplyResult => {
  if (session.status === 'failed' || session.status === 'completed') {
    return {
      session,
      accepted: false,
      duplicate: false,
      rejected: true,
      error: 'The performance suite is already terminal.',
    };
  }
  if (pending.suiteId !== session.suiteId) {
    return {
      session,
      accepted: false,
      duplicate: false,
      rejected: true,
      error: 'Pending scenario belongs to a different suite.',
    };
  }
  if (session.processedRunIds.includes(pending.runId)) {
    return {
      session,
      accepted: false,
      duplicate: true,
      rejected: false,
      error: null,
    };
  }
  if (!session.expectedRunId || pending.runId !== session.expectedRunId) {
    return {
      session,
      accepted: false,
      duplicate: false,
      rejected: true,
      error: 'Pending scenario does not match the active suite cursor.',
    };
  }
  const nextSession = pending.type === 'result'
    ? applyScenarioResult(session, pending, pending.result as PerfRawRun, plans)
    : applyScenarioError(session, pending, plans);
  return {
    session: nextSession,
    accepted: true,
    duplicate: false,
    rejected: false,
    error: null,
  };
};

export const markViewportWaiting = (
  session: PerfTopLevelSuiteSession,
  requirement: PerfViewportRequirement,
): PerfTopLevelSuiteSession => ({
  ...session,
  status: 'waiting-for-viewport',
  fatalCode: null,
  fatalError: null,
});

export const completeSuite = (
  session: PerfTopLevelSuiteSession,
): PerfTopLevelSuiteSession => ({
  ...session,
  status: 'completed',
  fatalCode: null,
  fatalError: null,
});

export const failSuite = (
  session: PerfTopLevelSuiteSession,
  fatalCode: string,
  fatalError: string,
): PerfTopLevelSuiteSession => ({
  ...session,
  status: 'failed',
  fatalCode,
  fatalError,
});

export const buildPerfSuiteResult = (
  session: PerfTopLevelSuiteSession,
  summary: PerfSuiteResult['summary'],
  decision: Pick<PerfSuiteResult, 'classification' | 'bottleneck' | 'recommendation'>,
): PerfSuiteResult => ({
  status: 'completed',
  measuredHarnessHead: session.harnessHead,
  environment: session.environment,
  rawRuns: session.rawRuns,
  summary,
  classification: decision.classification,
  bottleneck: decision.bottleneck,
  recommendation: decision.recommendation,
  failures: session.failures,
  rejectedTimingAttempts: session.rejectedTimingAttempts,
});
