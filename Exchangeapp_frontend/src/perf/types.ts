export const PERF_MESSAGE_NAMESPACE = 'exchange-platform.perf.v1' as const;
export const PERF_SCENARIO_COMPLETE = 'scenario-complete' as const;
export const PERF_SCENARIO_ERROR = 'scenario-error' as const;

export type PerfViewport = 'desktop' | 'mobile';
export type PerfFixture = 'mixed' | 'text-only';
export type PerfRunType = 'matrix' | 'append';
export type PerfClassification = 'GREEN' | 'YELLOW' | 'RED' | 'MEASUREMENT NOT VERIFIED';

export interface PerfScenarioConfig {
  runId: string;
  viewport: PerfViewport;
  width: number;
  height: number;
  count: number;
  trackView: boolean;
  fixture: PerfFixture;
  runType: PerfRunType;
  appendFrom?: number;
}

export interface PerfRawScenario {
  viewport: PerfViewport;
  width: number;
  height: number;
  innerWidth: number;
  innerHeight: number;
  count: number;
  trackView: boolean;
  fixture: PerfFixture;
  runType: PerfRunType;
}

export interface PerfRenderMetrics {
  requestedPosts: number;
  postCards: number;
  domElements: number;
  mountMs: number;
}

export interface PerfAppendMetrics {
  measured: boolean;
  from: number;
  to: number;
  durationMs: number;
}

export interface PerfScrollMetrics {
  samples: number;
  medianFrameMs: number;
  p95FrameMs: number;
  maxFrameMs: number;
  framesOver34Ms: number;
  framesOver50Ms: number;
  percentOver50Ms: number;
}

export interface PerfLongTaskMetrics {
  supported: boolean;
  count?: number;
  longestMs?: number;
  totalMs?: number;
}

export interface PerfMemoryMetrics {
  supported: boolean;
  beforeMount?: number;
  afterMount?: number;
  afterScroll?: number;
}

export interface PerfObserverMetrics {
  supported: boolean;
  instancesCreated: number;
  observeCalls: number;
  unobserveCalls: number;
  targetsBeforeCleanup: number;
  currentTargets: number;
  peakTargets: number;
}

export interface PerfValidation {
  valid: boolean;
  issues: string[];
  telemetryNetworkRequests: number;
}

export interface PerfRawRun {
  scenario: PerfRawScenario;
  render: PerfRenderMetrics;
  append: PerfAppendMetrics;
  scroll: PerfScrollMetrics;
  longTasks: PerfLongTaskMetrics;
  memory: PerfMemoryMetrics;
  observer: PerfObserverMetrics;
  validation: PerfValidation;
}

export interface PerfEnvironment {
  gitHead: string;
  timestamp: string;
  userAgent: string;
  platform?: string;
  devicePixelRatio: number;
  hardwareConcurrency?: number;
  deviceMemory?: number;
  longTaskSupported: boolean;
  memorySupported: boolean;
}

export interface PerfSummaryLongTasks {
  supported: boolean;
  totalCount: number | null;
  longestMs: number | null;
  totalMs: number | null;
}

export interface PerfSummaryMemory {
  supported: boolean;
  medianBeforeMount: number | null;
  medianAfterMount: number | null;
  medianAfterScroll: number | null;
}

export interface PerfSummaryRow {
  key: string;
  viewport: PerfViewport;
  count: number;
  trackView: boolean;
  fixture: PerfFixture;
  runType: PerfRunType;
  recordedRuns: number;
  valid: boolean;
  medianMountMs: number;
  medianAppendMs: number | null;
  medianFrameMs: number;
  medianP95FrameMs: number;
  worstMaxFrameMs: number;
  worstFramesOver34Ms: number;
  worstFramesOver50Ms: number;
  worstPercentOver50Ms: number;
  medianDomElements: number;
  medianPeakObservedTargets: number;
  longTasks: PerfSummaryLongTasks;
  memory: PerfSummaryMemory;
}

export interface PerfTrackedUntrackedDelta {
  viewport: PerfViewport;
  count: number;
  fixture: PerfFixture;
  runType: PerfRunType;
  mountPercent: number | null;
  p95FramePercent: number | null;
  longTaskDurationPercent: number | null;
}

export interface PerfScalingRatio {
  viewport: PerfViewport;
  trackView: boolean;
  fixture: PerfFixture;
  domElements300To100: number | null;
  mountMs300To100: number | null;
  p95Frame300To100: number | null;
  heapAfterMount300To100: number | null;
}

export interface PerfSummary {
  rows: PerfSummaryRow[];
  trackedVsUntracked: PerfTrackedUntrackedDelta[];
  scaling: PerfScalingRatio[];
}

export interface PerfScenarioFailure {
  scenario: string;
  phase: 'warm-up' | 'recorded';
  error: string;
}

export interface PerfSuiteResult {
  status: 'completed';
  environment: PerfEnvironment;
  rawRuns: PerfRawRun[];
  summary: PerfSummary;
  classification: PerfClassification;
  bottleneck: string;
  recommendation: string;
  failures: PerfScenarioFailure[];
}

export type PerfScenarioMessage =
  | {
      namespace: typeof PERF_MESSAGE_NAMESPACE;
      type: typeof PERF_SCENARIO_COMPLETE;
      runId: string;
      result: PerfRawRun;
    }
  | {
      namespace: typeof PERF_MESSAGE_NAMESPACE;
      type: typeof PERF_SCENARIO_ERROR;
      runId: string;
      error: string;
    };

declare const __EXCHANGE_PERF_GIT_HEAD__: string;

declare global {
  interface Window {
    __EXCHANGE_PERF_RESULT__?: PerfSuiteResult;
  }
}

export const getPerfGitHead = (): string => (
  typeof __EXCHANGE_PERF_GIT_HEAD__ === 'string' && __EXCHANGE_PERF_GIT_HEAD__.trim()
    ? __EXCHANGE_PERF_GIT_HEAD__.trim()
    : 'unknown'
);

const isRecord = (value: unknown): value is Record<string, unknown> => (
  Boolean(value) && typeof value === 'object'
);

export const isPerfScenarioMessage = (value: unknown): value is PerfScenarioMessage => {
  if (!isRecord(value)
    || value.namespace !== PERF_MESSAGE_NAMESPACE
    || typeof value.runId !== 'string'
    || value.runId.trim() === '') {
    return false;
  }

  if (value.type === PERF_SCENARIO_COMPLETE) {
    return isRecord(value.result);
  }

  return value.type === PERF_SCENARIO_ERROR && typeof value.error === 'string';
};
