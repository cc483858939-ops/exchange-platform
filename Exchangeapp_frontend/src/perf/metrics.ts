import type {
  PerfClassification,
  PerfLongTaskMetrics,
  PerfRawRun,
  PerfRafHealth,
  PerfSummary,
  PerfSummaryLongTasks,
  PerfSummaryMemory,
  PerfSummaryRow,
  PerfSuiteResult,
  PerfTimingHealth,
  PerfTrackedUntrackedDelta,
  PerfScalingRatio,
} from './types';

const round = (value: number): number => Math.round(value * 100) / 100;

const finiteValues = (values: number[]): number[] => values.filter(value => Number.isFinite(value));

export function percentile(values: number[], probability: number): number {
  const sorted = finiteValues(values).sort((left, right) => left - right);
  if (sorted.length === 0) {
    return 0;
  }

  const clampedProbability = Math.min(1, Math.max(0, probability));
  const rank = (sorted.length - 1) * clampedProbability;
  const lower = Math.floor(rank);
  const upper = Math.ceil(rank);
  if (lower === upper) {
    return sorted[lower];
  }
  return sorted[lower] + (sorted[upper] - sorted[lower]) * (rank - lower);
}

export const median = (values: number[]): number => percentile(values, 0.5);

export interface FrameMetrics {
  samples: number;
  medianFrameMs: number;
  p95FrameMs: number;
  maxFrameMs: number;
  framesOver34Ms: number;
  framesOver50Ms: number;
  percentOver50Ms: number;
}

export const PERF_RAF_HEALTH_SAMPLES = 12;

export interface RafCadenceAssessment {
  metrics: PerfRafHealth;
  issues: string[];
}

export interface RafHealthProbe extends RafCadenceAssessment {
  intervals: number[];
}

export function summarizeRafCadence(intervals: number[]): PerfRafHealth {
  const validIntervals = finiteValues(intervals).filter(interval => interval >= 0);
  return {
    samples: validIntervals.length,
    medianMs: round(median(validIntervals)),
    p95Ms: round(percentile(validIntervals, 0.95)),
    maxMs: round(validIntervals.length > 0 ? Math.max(...validIntervals) : 0),
  };
}

export function assessRafCadence(
  intervals: number[],
  phase: string,
): RafCadenceAssessment {
  const metrics = summarizeRafCadence(intervals);
  const validIntervals = finiteValues(intervals).filter(interval => interval >= 0);
  const severeIntervals = validIntervals.filter(interval => interval >= 250).length;
  const issues: string[] = [];

  if (metrics.samples < PERF_RAF_HEALTH_SAMPLES) {
    issues.push(`${phase} RAF cadence collected ${metrics.samples}/${PERF_RAF_HEALTH_SAMPLES} intervals`);
  }
  if (metrics.medianMs > 50) {
    issues.push(`${phase} RAF median interval ${metrics.medianMs} ms exceeds 50 ms`);
  }
  if (severeIntervals >= 2) {
    issues.push(`${phase} RAF cadence recorded ${severeIntervals} intervals >=250 ms`);
  }

  return { metrics, issues };
}

export function assessTimingHealth(
  preflight: RafHealthProbe,
  postScroll: RafHealthProbe,
  postCleanup: RafHealthProbe,
  scrollIssues: string[],
  visibilityLost: boolean,
): PerfTimingHealth {
  const postflightAssessment = assessRafCadence(
    [...postScroll.intervals, ...postCleanup.intervals],
    'postflight',
  );
  const issues = [
    ...preflight.issues,
    ...postScroll.issues,
    ...postCleanup.issues,
    ...postflightAssessment.issues,
    ...scrollIssues,
  ];
  if (visibilityLost) {
    issues.push('document visibility was hidden during the scenario');
  }

  return {
    valid: issues.length === 0,
    preflight: preflight.metrics,
    postflight: summarizeRafCadence([...postScroll.intervals, ...postCleanup.intervals]),
    visibilityLost,
    issues: Array.from(new Set(issues)),
  };
}

export interface ScrollTimingAssessment {
  valid: boolean;
  issues: string[];
}

export function assessScrollTiming(
  deltas: number[],
  longTasks: PerfLongTaskMetrics,
): ScrollTimingAssessment {
  const validDeltas = finiteValues(deltas).filter(delta => delta >= 0);
  const largeGaps = validDeltas.filter(delta => delta >= 250);
  const oneHzGaps = validDeltas.filter(delta => delta >= 800);
  const largestGap = largeGaps.length > 0 ? Math.max(...largeGaps) : 0;
  const matchingLongTask = longTasks.supported
    && (longTasks.count ?? 0) > 0
    && (longTasks.longestMs ?? 0) >= largestGap * 0.8;
  const issues: string[] = [];

  if (validDeltas.length < 4) {
    issues.push(`scroll cadence collected only ${validDeltas.length} samples`);
  }
  if (oneHzGaps.length >= 2 || (validDeltas.length <= 5 && oneHzGaps.length >= 1)) {
    issues.push('scroll cadence contains approximately 1 Hz intervals; browser timing is throttled');
  } else if (validDeltas.length <= 6 && largeGaps.length >= 2 && !matchingLongTask) {
    issues.push('scroll cadence has low sample count and large gaps without a matching long task');
  }

  return { valid: issues.length === 0, issues };
}

export function summarizeFrameDeltas(deltas: number[]): FrameMetrics {
  const validDeltas = finiteValues(deltas).filter(delta => delta >= 0);
  const framesOver34Ms = validDeltas.filter(delta => delta > 34).length;
  const framesOver50Ms = validDeltas.filter(delta => delta > 50).length;

  return {
    samples: validDeltas.length,
    medianFrameMs: round(median(validDeltas)),
    p95FrameMs: round(percentile(validDeltas, 0.95)),
    maxFrameMs: round(validDeltas.length > 0 ? Math.max(...validDeltas) : 0),
    framesOver34Ms,
    framesOver50Ms,
    percentOver50Ms: validDeltas.length > 0
      ? round((framesOver50Ms / validDeltas.length) * 100)
      : 0,
  };
}

const medianOrNull = (values: number[]): number | null => {
  const validValues = finiteValues(values);
  return validValues.length > 0 ? round(median(validValues)) : null;
};

const sumOrNull = (values: number[]): number | null => {
  const validValues = finiteValues(values);
  return validValues.length > 0
    ? round(validValues.reduce((total, value) => total + value, 0))
    : null;
};

const scenarioKey = (run: PerfRawRun): string => [
  run.scenario.viewport,
  run.scenario.count,
  run.scenario.trackView ? 'tracked' : 'untracked',
  run.scenario.fixture,
  run.scenario.runType,
].join(':');

const aggregateLongTasks = (runs: PerfRawRun[]): PerfSummaryLongTasks => {
  const supportedRuns = runs.filter(run => run.longTasks.supported);
  if (supportedRuns.length === 0) {
    return {
      supported: false,
      totalCount: null,
      longestMs: null,
      totalMs: null,
    };
  }

  const counts = supportedRuns
    .map(run => run.longTasks.count)
    .filter((count): count is number => typeof count === 'number' && Number.isFinite(count));
  const longestValues = supportedRuns
    .map(run => run.longTasks.longestMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value));
  const totalValues = supportedRuns
    .map(run => run.longTasks.totalMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value));

  return {
    supported: true,
    totalCount: counts.length > 0 ? counts.reduce((total, count) => total + count, 0) : 0,
    longestMs: longestValues.length > 0 ? round(Math.max(...longestValues)) : 0,
    totalMs: totalValues.length > 0
      ? round(totalValues.reduce((total, value) => total + value, 0))
      : 0,
  };
};

const aggregateMemory = (runs: PerfRawRun[]): PerfSummaryMemory => {
  const supportedRuns = runs.filter(run => run.memory.supported);
  return {
    supported: supportedRuns.length > 0,
    medianBeforeMount: medianOrNull(supportedRuns
      .map(run => run.memory.beforeMount)
      .filter((value): value is number => typeof value === 'number')),
    medianAfterMount: medianOrNull(supportedRuns
      .map(run => run.memory.afterMount)
      .filter((value): value is number => typeof value === 'number')),
    medianAfterScroll: medianOrNull(supportedRuns
      .map(run => run.memory.afterScroll)
      .filter((value): value is number => typeof value === 'number')),
  };
};

const aggregateRow = (runs: PerfRawRun[]): PerfSummaryRow => {
  const first = runs[0];
  const appendValues = runs
    .filter(run => run.append.measured)
    .map(run => run.append.durationMs);

  return {
    key: scenarioKey(first),
    viewport: first.scenario.viewport,
    count: first.scenario.count,
    trackView: first.scenario.trackView,
    fixture: first.scenario.fixture,
    runType: first.scenario.runType,
    recordedRuns: runs.length,
    valid: runs.every(run => run.validation.valid),
    medianMountMs: round(median(runs.map(run => run.render.mountMs))),
    medianAppendMs: appendValues.length > 0 ? round(median(appendValues)) : null,
    medianFrameMs: round(median(runs.map(run => run.scroll.medianFrameMs))),
    medianP95FrameMs: round(median(runs.map(run => run.scroll.p95FrameMs))),
    worstMaxFrameMs: round(Math.max(...runs.map(run => run.scroll.maxFrameMs))),
    worstFramesOver34Ms: Math.max(...runs.map(run => run.scroll.framesOver34Ms)),
    worstFramesOver50Ms: Math.max(...runs.map(run => run.scroll.framesOver50Ms)),
    worstPercentOver50Ms: round(Math.max(...runs.map(run => run.scroll.percentOver50Ms))),
    medianDomElements: round(median(runs.map(run => run.render.domElements))),
    medianPeakObservedTargets: round(median(runs.map(run => run.observer.peakTargets))),
    longTasks: aggregateLongTasks(runs),
    memory: aggregateMemory(runs),
  };
};

const rowMatches = (
  row: PerfSummaryRow,
  other: PerfSummaryRow,
): boolean => row.viewport === other.viewport
  && row.count === other.count
  && row.fixture === other.fixture
  && row.runType === other.runType;

const relativePercent = (value: number, baseline: number): number | null => {
  if (!Number.isFinite(value) || !Number.isFinite(baseline)) {
    return null;
  }
  if (baseline === 0) {
    return value === 0 ? 0 : null;
  }
  return round(((value - baseline) / baseline) * 100);
};

const ratioOrNull = (value: number | null, baseline: number | null): number | null => {
  if (value === null || baseline === null || baseline === 0) {
    return value === baseline ? 1 : null;
  }
  return round(value / baseline);
};

const buildTrackedUntrackedDeltas = (rows: PerfSummaryRow[]): PerfTrackedUntrackedDelta[] => (
  rows
    .filter(row => row.trackView)
    .map(row => {
      const baseline = rows.find(candidate => !candidate.trackView && rowMatches(row, candidate));
      return {
        viewport: row.viewport,
        count: row.count,
        fixture: row.fixture,
        runType: row.runType,
        mountPercent: baseline ? relativePercent(row.medianMountMs, baseline.medianMountMs) : null,
        p95FramePercent: baseline
          ? relativePercent(row.medianP95FrameMs, baseline.medianP95FrameMs)
          : null,
        longTaskDurationPercent: baseline
          && row.longTasks.totalMs !== null
          && baseline.longTasks.totalMs !== null
          ? relativePercent(row.longTasks.totalMs, baseline.longTasks.totalMs)
          : null,
      };
    })
);

const buildScalingRatios = (rows: PerfSummaryRow[]): PerfScalingRatio[] => {
  const keys = new Set(rows
    .filter(row => row.runType === 'matrix')
    .map(row => `${row.viewport}:${row.trackView}:${row.fixture}`));

  return Array.from(keys).map(key => {
    const [viewport, trackView, fixture] = key.split(':') as [PerfSummaryRow['viewport'], string, PerfSummaryRow['fixture']];
    const row100 = rows.find(row => row.runType === 'matrix'
      && row.viewport === viewport
      && row.trackView === (trackView === 'true')
      && row.fixture === fixture
      && row.count === 100);
    const row300 = rows.find(row => row.runType === 'matrix'
      && row.viewport === viewport
      && row.trackView === (trackView === 'true')
      && row.fixture === fixture
      && row.count === 300);

    return {
      viewport,
      trackView: trackView === 'true',
      fixture,
      domElements300To100: row100 && row300
        ? ratioOrNull(row300.medianDomElements, row100.medianDomElements)
        : null,
      mountMs300To100: row100 && row300
        ? ratioOrNull(row300.medianMountMs, row100.medianMountMs)
        : null,
      p95Frame300To100: row100 && row300
        ? ratioOrNull(row300.medianP95FrameMs, row100.medianP95FrameMs)
        : null,
      heapAfterMount300To100: row100 && row300
        ? ratioOrNull(row300.memory.medianAfterMount, row100.memory.medianAfterMount)
        : null,
    };
  });
};

export function aggregatePerfRuns(rawRuns: PerfRawRun[]): PerfSummary {
  const groups = new Map<string, PerfRawRun[]>();
  const acceptedRuns = rawRuns.filter(run => run.validation.valid && run.timingHealth.valid);
  for (const run of acceptedRuns) {
    const key = scenarioKey(run);
    const group = groups.get(key) ?? [];
    group.push(run);
    groups.set(key, group);
  }

  const rows = Array.from(groups.values())
    .map(aggregateRow)
    .sort((left, right) => left.key.localeCompare(right.key));

  return {
    rows,
    trackedVsUntracked: buildTrackedUntrackedDeltas(rows),
    scaling: buildScalingRatios(rows),
  };
}

const findMatrixRow = (
  summary: PerfSummary,
  viewport: PerfSummaryRow['viewport'],
  count: number,
  trackView: boolean,
  fixture: PerfSummaryRow['fixture'] = 'mixed',
): PerfSummaryRow | undefined => summary.rows.find(row => row.runType === 'matrix'
  && row.viewport === viewport
  && row.count === count
  && row.trackView === trackView
  && row.fixture === fixture);

const REQUIRED_MATRIX_COUNTS = [20, 50, 100, 200, 300] as const;

const hasCompleteRequiredCoverage = (rawRuns: PerfRawRun[]): boolean => {
  const complete = (runs: PerfRawRun[], requireAppend: boolean): boolean => (
    runs.length >= 3
      && runs.every(run => (
        run.validation.valid
        && run.timingHealth.valid
        && (!requireAppend || run.append.measured)
      ))
  );

  for (const viewport of ['desktop', 'mobile'] as const) {
    for (const trackView of [true, false]) {
      for (const count of REQUIRED_MATRIX_COUNTS) {
        const matrixRuns = rawRuns.filter(run => (
          run.scenario.viewport === viewport
          && run.scenario.count === count
          && run.scenario.trackView === trackView
          && run.scenario.fixture === 'mixed'
          && run.scenario.runType === 'matrix'
        ));
        if (!complete(matrixRuns, [20, 100, 200].includes(count))) {
          return false;
        }
      }

      const appendRuns = rawRuns.filter(run => (
        run.scenario.viewport === viewport
        && run.scenario.count === 280
        && run.scenario.trackView === trackView
        && run.scenario.fixture === 'mixed'
        && run.scenario.runType === 'append'
      ));
      if (!complete(appendRuns, true)) {
        return false;
      }
    }
  }

  return true;
};

const hasRatioAbove = (ratios: PerfScalingRatio[], field: keyof PerfScalingRatio, threshold: number): boolean => (
  ratios.some(ratio => {
    const value = ratio[field];
    return typeof value === 'number' && value > threshold;
  })
);

const determineBottleneck = (summary: PerfSummary, classification: PerfClassification): string => {
  if (classification === 'MEASUREMENT NOT VERIFIED') {
    return 'No browser conclusion; complete the real Chromium run first.';
  }

  const mobileDeltas = summary.trackedVsUntracked.filter(delta => delta.viewport === 'mobile');
  const observationDelta = mobileDeltas.find(delta => (
    typeof delta.p95FramePercent === 'number' && delta.p95FramePercent > 20
  ) || (
    typeof delta.mountPercent === 'number' && delta.mountPercent > 20
  ));
  if (observationDelta) {
    return 'Feed observation lifecycle is the leading diagnostic candidate.';
  }

  const untracked300 = findMatrixRow(summary, 'mobile', 300, false);
  const scale = summary.scaling.find(item => item.viewport === 'mobile' && !item.trackView);
  if (untracked300 && scale && (
    (scale.domElements300To100 ?? 0) > 2
    || (scale.mountMs300To100 ?? 0) > 2
    || (scale.p95Frame300To100 ?? 0) > 2
  )) {
    return 'DOM/Vue mounted-state cost is the leading diagnostic candidate.';
  }

  return 'No single subsystem dominates this baseline; inspect the recorded matrix before changing architecture.';
};

export interface PerfDecision {
  classification: PerfClassification;
  bottleneck: string;
  recommendation: string;
}

export function classifyPerfBaseline(rawRuns: PerfRawRun[], summary: PerfSummary): PerfDecision {
  if (rawRuns.length === 0 || rawRuns.some(run => !run.validation.valid || !run.timingHealth.valid)) {
    return {
      classification: 'MEASUREMENT NOT VERIFIED',
      bottleneck: 'Scenario validation failed or no browser runs were recorded.',
      recommendation: 'Rerun the isolated benchmark in a real Chromium-family browser.',
    };
  }

  if (!hasCompleteRequiredCoverage(rawRuns)) {
    return {
      classification: 'MEASUREMENT NOT VERIFIED',
      bottleneck: 'One or more required viewport, count, mode, or append scenarios are incomplete.',
      recommendation: 'Complete all three recorded runs for the full matrix before drawing a performance conclusion.',
    };
  }

  const mobileTracked300 = findMatrixRow(summary, 'mobile', 300, true);
  if (!mobileTracked300) {
    return {
      classification: 'MEASUREMENT NOT VERIFIED',
      bottleneck: 'The required 300-card mobile tracked scenario is missing.',
      recommendation: 'Complete the required matrix before drawing a performance conclusion.',
    };
  }

  const measuredAppends = summary.rows
    .filter(row => row.medianAppendMs !== null)
    .map(row => row.medianAppendMs as number);
  const worstAppend = measuredAppends.length > 0 ? Math.max(...measuredAppends) : 0;
  const selectedRuns = rawRuns.filter(run => run.scenario.viewport === 'mobile'
    && run.scenario.count === 300
    && run.scenario.trackView
    && run.scenario.runType === 'matrix');
  const recurringLongTaskOver100 = selectedRuns.filter(run => (
    run.longTasks.supported && (run.longTasks.longestMs ?? 0) > 100
  )).length >= 2;
  const recurringLongTaskOver200 = selectedRuns.filter(run => (
    run.longTasks.supported && (run.longTasks.longestMs ?? 0) > 200
  )).length >= 2;
  const observerLeak = rawRuns.some(run => (
    run.observer.currentTargets !== 0
    || (run.scenario.trackView
      && run.observer.targetsBeforeCleanup < run.render.postCards + (run.append.measured ? 20 : 0))
    || (!run.scenario.trackView && run.observer.observeCalls !== 0)
  ));
  const scalingBad = hasRatioAbove(summary.scaling, 'mountMs300To100', 3)
    || hasRatioAbove(summary.scaling, 'p95Frame300To100', 3);
  const green = mobileTracked300.medianP95FrameMs <= 34
    && mobileTracked300.worstPercentOver50Ms <= 1
    && worstAppend <= 50
    && !recurringLongTaskOver100
    && !scalingBad
    && !observerLeak;
  const red = mobileTracked300.medianP95FrameMs > 50
    || mobileTracked300.worstPercentOver50Ms > 5
    || worstAppend > 100
    || recurringLongTaskOver200
    || hasRatioAbove(summary.scaling, 'mountMs300To100', 4)
    || hasRatioAbove(summary.scaling, 'p95Frame300To100', 4)
    || observerLeak;
  const classification: PerfClassification = green ? 'GREEN' : red ? 'RED' : 'YELLOW';
  const bottleneck = determineBottleneck(summary, classification);
  const recommendation = classification === 'GREEN'
    ? 'NO LONG-LIST OPTIMIZATION. Next: Image Delivery Optimization.'
    : classification === 'RED'
      ? 'LONG-LIST OPTIMIZATION REQUIRED. Write a targeted Spec N.1 for the measured bottleneck.'
      : 'Spec N.1 required. Diagnose the dominant measured subsystem before choosing an optimization.';

  return { classification, bottleneck, recommendation };
}

const display = (value: number | null | undefined): string => (
  typeof value === 'number' && Number.isFinite(value) ? String(round(value)) : 'not supported'
);

export function serializePerfMarkdown(result: PerfSuiteResult): string {
  const lines = [
    '# Exchange Platform Long-list Browser Performance Baseline',
    '',
    `- Classification: **${result.classification}**`,
    `- Recommendation: ${result.recommendation}`,
    `- Bottleneck: ${result.bottleneck}`,
    '',
    '## Environment',
    '',
    `- Git HEAD: ${result.environment.gitHead}`,
    `- Measured harness HEAD: ${result.measuredHarnessHead}`,
    `- Rejected timing attempts: ${result.rejectedTimingAttempts}`,
    `- Timestamp: ${result.environment.timestamp}`,
    `- User agent: ${result.environment.userAgent}`,
    `- Platform: ${result.environment.platform ?? 'not exposed'}`,
    `- Device pixel ratio: ${display(result.environment.devicePixelRatio)}`,
    `- Hardware concurrency: ${display(result.environment.hardwareConcurrency)}`,
    `- Device memory: ${display(result.environment.deviceMemory)}`,
    `- Long Task API: ${result.environment.longTaskSupported ? 'supported' : 'not supported'}`,
    `- performance.memory: ${result.environment.memorySupported ? 'supported' : 'not supported'}`,
    '',
    '## Timing validity',
    '',
    `- Accepted recorded runs: ${result.rawRuns.length}`,
    `- Rejected timing attempts: ${result.rejectedTimingAttempts}`,
    `- All accepted runs timing-valid: ${result.rawRuns.every(run => run.timingHealth.valid) ? 'yes' : 'no'}`,
    `- Any accepted run lost visibility: ${result.rawRuns.some(run => run.timingHealth.visibilityLost) ? 'yes' : 'no'}`,
    '',
    '## Aggregated scenarios',
    '',
    '| Viewport | Count | Mode | Run | Runs | Mount ms | Append ms | Median frame ms | Median P95 frame ms | Worst max frame ms | Worst run >50ms % | DOM elements | Peak targets | Long tasks | Longest task ms | Heap after mount |',
    '| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |',
    ...result.summary.rows.map(row => `| ${row.viewport} | ${row.count} | ${row.trackView ? 'tracked' : 'untracked'} | ${row.runType} | ${row.recordedRuns} | ${display(row.medianMountMs)} | ${display(row.medianAppendMs)} | ${display(row.medianFrameMs)} | ${display(row.medianP95FrameMs)} | ${display(row.worstMaxFrameMs)} | ${display(row.worstPercentOver50Ms)}% | ${display(row.medianDomElements)} | ${display(row.medianPeakObservedTargets)} | ${display(row.longTasks.totalCount)} | ${display(row.longTasks.longestMs)} | ${display(row.memory.medianAfterMount)} |`),
    '',
    '## Tracked versus untracked delta',
    '',
    '| Viewport | Count | Run | Mount delta | P95 frame delta | Long-task duration delta |',
    '| --- | ---: | --- | ---: | ---: | ---: |',
    ...result.summary.trackedVsUntracked.map(delta => `| ${delta.viewport} | ${delta.count} | ${delta.runType} | ${display(delta.mountPercent)}% | ${display(delta.p95FramePercent)}% | ${display(delta.longTaskDurationPercent)}% |`),
    '',
    '## 300 / 100 scaling',
    '',
    '| Viewport | Mode | DOM | Mount | P95 frame | Heap after mount |',
    '| --- | --- | ---: | ---: | ---: | ---: |',
    ...result.summary.scaling.map(ratio => `| ${ratio.viewport} | ${ratio.trackView ? 'tracked' : 'untracked'} | ${display(ratio.domElements300To100)}x | ${display(ratio.mountMs300To100)}x | ${display(ratio.p95Frame300To100)}x | ${display(ratio.heapAfterMount300To100)}x |`),
    '',
    '## Failures',
    '',
    result.failures.length === 0
      ? '- None'
      : result.failures.map(failure => `- ${failure.phase}: ${failure.scenario} — ${failure.error}`),
  ];

  return lines.flat().join('\n') + '\n';
}
