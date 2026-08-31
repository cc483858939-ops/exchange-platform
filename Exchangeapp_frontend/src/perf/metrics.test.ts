import { describe, expect, it } from 'vitest';
import {
  attributeLongTasksByPhase,
  assessRafCadence,
  assessScrollTiming,
  assessTimingHealth,
  aggregatePerfRuns,
  classifyPerfBaseline,
  percentile,
  PERF_RAF_HEALTH_SAMPLES,
  serializePerfMarkdown,
  summarizeFrameDeltas,
} from './metrics';
import type { PerfRawRun, PerfSuiteResult } from './types';

const rawRun = (
  count: number,
  trackView: boolean,
  options: Partial<PerfRawRun> = {},
): PerfRawRun => ({
  scenario: {
    viewport: 'mobile',
    width: 390,
    height: 844,
    innerWidth: 390,
    innerHeight: 844,
    count,
    trackView,
    fixture: 'mixed',
    runType: 'matrix',
  },
  render: {
    requestedPosts: count,
    postCards: count,
    domElements: count * 10,
    mountMs: trackView ? 12 : 10,
  },
  append: {
    measured: false,
    from: 0,
    to: 0,
    durationMs: 0,
  },
  scroll: {
    samples: 100,
    medianFrameMs: trackView ? 17 : 16,
    p95FrameMs: trackView ? 25 : 24,
    maxFrameMs: 40,
    framesOver34Ms: 2,
    framesOver50Ms: 0,
    percentOver50Ms: 0,
  },
  longTasks: {
    supported: true,
    count: 1,
    longestMs: 60,
    totalMs: 60,
  },
  memory: {
    supported: false,
  },
  observer: {
    supported: true,
    instancesCreated: 1,
    observeCalls: trackView ? count : 0,
    unobserveCalls: trackView ? count : 0,
    targetsBeforeCleanup: trackView ? count : 0,
    currentTargets: 0,
    peakTargets: trackView ? count : 0,
  },
  timingHealth: {
    valid: true,
    preflight: {
      samples: PERF_RAF_HEALTH_SAMPLES,
      medianMs: 16.67,
      p95Ms: 16.67,
      maxMs: 16.67,
    },
    postflight: {
      samples: PERF_RAF_HEALTH_SAMPLES * 2,
      medianMs: 16.67,
      p95Ms: 16.67,
      maxMs: 16.67,
    },
    visibilityLost: false,
    issues: [],
  },
  validation: {
    valid: true,
    issues: [],
    telemetryNetworkRequests: 0,
  },
  ...options,
});

const completeRecordedRun = (
  count: number,
  trackView: boolean,
  runType: 'matrix' | 'append' = 'matrix',
  appendFrom?: number,
): PerfRawRun => {
  const base = rawRun(count, trackView);
  if (typeof appendFrom !== 'number') {
    return {
      ...base,
      scenario: { ...base.scenario, runType },
    };
  }

  const finalCount = appendFrom + 20;
  return {
    ...base,
    scenario: { ...base.scenario, runType },
    append: {
      measured: true,
      from: appendFrom,
      to: finalCount,
      durationMs: 12,
    },
    observer: {
      ...base.observer,
      observeCalls: trackView ? finalCount : 0,
      unobserveCalls: trackView ? finalCount : 0,
      targetsBeforeCleanup: trackView ? finalCount : 0,
      peakTargets: trackView ? finalCount : 0,
    },
  };
};

const withViewport = (run: PerfRawRun, viewport: 'desktop' | 'mobile'): PerfRawRun => ({
  ...run,
  scenario: {
    ...run.scenario,
    viewport,
    width: viewport === 'desktop' ? 1440 : 390,
    height: viewport === 'desktop' ? 900 : 844,
    innerWidth: viewport === 'desktop' ? 1440 : 390,
    innerHeight: viewport === 'desktop' ? 900 : 844,
  },
});

const completeCoverageRuns = (): PerfRawRun[] => {
  const runs: PerfRawRun[] = [];
  const appendCounts = new Set([20, 100, 200]);
  for (const viewport of ['desktop', 'mobile'] as const) {
    for (const count of [20, 50, 100, 200, 300]) {
      for (const trackView of [true, false]) {
        for (let runIndex = 0; runIndex < 3; runIndex += 1) {
          runs.push(withViewport(completeRecordedRun(
            count,
            trackView,
            'matrix',
            appendCounts.has(count) ? count : undefined,
          ), viewport));
        }
      }
    }
    for (const trackView of [true, false]) {
      for (let runIndex = 0; runIndex < 3; runIndex += 1) {
        runs.push(withViewport(completeRecordedRun(280, trackView, 'append', 280), viewport));
      }
    }
  }
  return runs;
};

const rafProbe = (phase: string, intervals: number[]) => ({
  intervals,
  ...assessRafCadence(intervals, phase),
});

describe('performance metrics', () => {
  it('accepts healthy 60Hz and 30Hz cadence but rejects 1Hz cadence', () => {
    const sixtyHz = assessRafCadence(
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 16.67),
      'preflight',
    );
    const thirtyHz = assessRafCadence(
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 33.33),
      'preflight',
    );
    const oneHz = assessRafCadence(
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 1000),
      'preflight',
    );

    expect(sixtyHz.issues).toEqual([]);
    expect(thirtyHz.issues).toEqual([]);
    expect(oneHz.metrics.medianMs).toBe(1000);
    expect(oneHz.issues).toEqual(expect.arrayContaining([
      expect.stringContaining('exceeds 50 ms'),
      expect.stringContaining('>=250 ms'),
    ]));
  });

  it('invalidates visibility loss and postflight throttling', () => {
    const healthy = rafProbe(
      'preflight',
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 16.67),
    );
    const healthyPost = rafProbe(
      'postflight after scroll',
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 16.67),
    );
    const healthyCleanup = rafProbe(
      'postflight after PostCard cleanup',
      Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 16.67),
    );
    const visible = assessTimingHealth(healthy, healthyPost, healthyCleanup, [], false);
    const hidden = assessTimingHealth(healthy, healthyPost, healthyCleanup, [], true);
    const throttledPost = assessTimingHealth(
      healthy,
      rafProbe(
        'postflight after scroll',
        Array.from({ length: PERF_RAF_HEALTH_SAMPLES }, () => 1000),
      ),
      healthyCleanup,
      [],
      false,
    );

    expect(visible.valid).toBe(true);
    expect(hidden.valid).toBe(false);
    expect(hidden.visibilityLost).toBe(true);
    expect(throttledPost.valid).toBe(false);
    expect(throttledPost.issues).toEqual(expect.arrayContaining([
      expect.stringContaining('>=250 ms'),
    ]));
  });

  it('distinguishes throttled scroll gaps from a long-task-backed real gap', () => {
    const throttled = assessScrollTiming(
      [1000, 1000, 1000, 1000],
      { supported: false },
    );
    const realLongTask = assessScrollTiming(
      [500, 500, 500, 500],
      { supported: true, count: 4, longestMs: 500, totalMs: 2000 },
    );

    expect(throttled.valid).toBe(false);
    expect(throttled.issues.join(' ')).toContain('1 Hz');
    expect(realLongTask.valid).toBe(true);
  });

  it('calculates interpolated percentiles and frame thresholds', () => {
    expect(percentile([1, 2, 3, 4], 0.5)).toBe(2.5);
    expect(percentile([1, 2, 3, 4], 0.95)).toBeCloseTo(3.85, 10);
    expect(summarizeFrameDeltas([16, 34, 35, 50, 51])).toEqual({
      samples: 5,
      medianFrameMs: 35,
      p95FrameMs: 50.8,
      maxFrameMs: 51,
      framesOver34Ms: 3,
      framesOver50Ms: 1,
      percentOver50Ms: 20,
    });
  });

  it('aggregates scenarios and computes tracked versus untracked deltas', () => {
    const runs = [
      rawRun(100, true),
      rawRun(100, true, { render: { ...rawRun(100, true).render, mountMs: 14 } }),
      rawRun(100, false),
      rawRun(300, true, {
        render: { ...rawRun(300, true).render, mountMs: 30 },
        scroll: { ...rawRun(300, true).scroll, p95FrameMs: 31 },
      }),
      rawRun(300, false, {
        render: { ...rawRun(300, false).render, mountMs: 20 },
        scroll: { ...rawRun(300, false).scroll, p95FrameMs: 26 },
      }),
    ];
    const summary = aggregatePerfRuns(runs);
    const delta = summary.trackedVsUntracked.find(item => item.count === 100);
    const scaling = summary.scaling.find(item => item.viewport === 'mobile' && item.trackView);

    expect(summary.rows).toHaveLength(4);
    expect(delta?.mountPercent).toBe(30);
    expect(scaling?.mountMs300To100).toBe(2.31);
    expect(scaling?.p95Frame300To100).toBe(1.24);
  });

  it('excludes timing-invalid runs from accepted aggregation', () => {
    const invalid = rawRun(100, true, {
      timingHealth: {
        ...rawRun(100, true).timingHealth,
        valid: false,
        issues: ['preflight RAF cadence is throttled'],
      },
      validation: {
        ...rawRun(100, true).validation,
        valid: false,
        issues: ['preflight RAF cadence is throttled'],
      },
    });
    const summary = aggregatePerfRuns([rawRun(100, true), invalid]);

    expect(summary.rows).toHaveLength(1);
    expect(summary.rows[0].recordedRuns).toBe(1);
  });

  it('attributes long tasks to mount, append, and scroll windows', () => {
    expect(attributeLongTasksByPhase(
      [
        { startTime: 10, duration: 240 },
        { startTime: 100, duration: 30 },
        { startTime: 200, duration: 60 },
        { startTime: 400, duration: 90 },
      ],
      {
        mount: { startTime: 0, endTime: 50 },
        append: { startTime: 90, endTime: 150 },
        scroll: { startTime: 180, endTime: 300 },
      },
    )).toEqual({
      mount: { count: 1, longestMs: 240, totalMs: 240 },
      append: { count: 1, longestMs: 30, totalMs: 30 },
      scroll: { count: 1, longestMs: 60, totalMs: 60 },
    });
  });

  it('aggregates long-task phase attribution when raw runs provide it', () => {
    const phaseRun = (mountMs: number, scrollMs: number): PerfRawRun => {
      const base = rawRun(100, true);
      return {
        ...base,
        longTasks: {
          ...base.longTasks,
          phases: {
            mount: { count: 1, longestMs: mountMs, totalMs: mountMs },
            append: { count: 0, longestMs: 0, totalMs: 0 },
            scroll: { count: 1, longestMs: scrollMs, totalMs: scrollMs },
          },
        },
      };
    };
    const summary = aggregatePerfRuns([phaseRun(120, 20), phaseRun(80, 30)]);

    expect(summary.rows[0].longTasks.phases).toEqual({
      mount: { count: 2, longestMs: 120, totalMs: 200 },
      append: { count: 0, longestMs: 0, totalMs: 0 },
      scroll: { count: 2, longestMs: 30, totalMs: 50 },
    });
  });

  it('does not call a small-count tracked delta an observation bottleneck', () => {
    const runs = completeCoverageRuns().map(run => {
      const isMobile = run.scenario.viewport === 'mobile';
      if (isMobile && run.scenario.count === 20 && run.scenario.runType === 'matrix') {
        return {
          ...run,
          render: { ...run.render, mountMs: run.scenario.trackView ? 13 : 10 },
        };
      }
      if (isMobile && [200, 300].includes(run.scenario.count) && run.scenario.runType === 'matrix') {
        return {
          ...run,
          render: { ...run.render, mountMs: 40 },
          scroll: { ...run.scroll, p95FrameMs: 24 },
          longTasks: { ...run.longTasks, count: 1, longestMs: 60, totalMs: 60 },
        };
      }
      return run;
    });
    const decision = classifyPerfBaseline(runs, aggregatePerfRuns(runs));

    expect(decision.bottleneck).toBe(
      'No single subsystem dominates this baseline; inspect the recorded matrix before changing architecture.',
    );
  });

  it('calls observation a bottleneck only when 200 and 300 tracked rows are materially worse', () => {
    const runs = completeCoverageRuns().map(run => {
      const isMobile = run.scenario.viewport === 'mobile';
      if (isMobile && [200, 300].includes(run.scenario.count) && run.scenario.runType === 'matrix') {
        const tracked = run.scenario.trackView;
        return {
          ...run,
          render: { ...run.render, mountMs: tracked ? 60 : 30 },
          scroll: { ...run.scroll, p95FrameMs: tracked ? 36 : 24 },
          longTasks: {
            ...run.longTasks,
            count: tracked ? 3 : 1,
            longestMs: tracked ? 120 : 60,
            totalMs: tracked ? 120 : 60,
          },
        };
      }
      return run;
    });
    const decision = classifyPerfBaseline(runs, aggregatePerfRuns(runs));

    expect(decision.bottleneck).toBe('Feed observation lifecycle is the leading diagnostic candidate.');
  });

  it('attributes similarly slow tracked and untracked 300-card runs to mounted-state cost', () => {
    const runs = completeCoverageRuns().map(run => {
      const isMobile = run.scenario.viewport === 'mobile';
      if (isMobile && run.scenario.count === 300 && run.scenario.runType === 'matrix') {
        const tracked = run.scenario.trackView;
        return {
          ...run,
          render: { ...run.render, mountMs: tracked ? 260 : 246 },
          longTasks: {
            ...run.longTasks,
            count: 3,
            longestMs: tracked ? 280 : 285,
            totalMs: tracked ? 766 : 750,
          },
        };
      }
      return run;
    });
    const decision = classifyPerfBaseline(runs, aggregatePerfRuns(runs));

    expect(decision.bottleneck).toBe('DOM/Vue mounted-state cost is the leading diagnostic candidate.');
  });

  it('does not report a bottleneck when all high-count metrics are healthy', () => {
    const runs = completeCoverageRuns().map(run => {
      if (run.scenario.viewport === 'mobile'
        && [200, 300].includes(run.scenario.count)
        && run.scenario.runType === 'matrix') {
        return {
          ...run,
          render: { ...run.render, mountMs: 40 },
          scroll: { ...run.scroll, p95FrameMs: 24 },
          longTasks: { ...run.longTasks, count: 1, longestMs: 60, totalMs: 60 },
        };
      }
      return run;
    });
    const decision = classifyPerfBaseline(runs, aggregatePerfRuns(runs));

    expect(decision.bottleneck).toBe(
      'No single subsystem dominates this baseline; inspect the recorded matrix before changing architecture.',
    );
  });

  it('classifies only valid recorded data and serializes the report', () => {
    const runs = completeCoverageRuns();
    const summary = aggregatePerfRuns(runs);
    const decision = classifyPerfBaseline(runs, summary);
    expect(decision.classification).toBe('GREEN');
    expect(decision.recommendation).toContain('Image Delivery');

    const result: PerfSuiteResult = {
      status: 'completed',
      environment: {
        gitHead: 'test-head',
        timestamp: '2026-01-01T00:00:00.000Z',
        userAgent: 'test-agent',
        devicePixelRatio: 1,
        longTaskSupported: false,
        memorySupported: false,
      },
      rawRuns: runs,
      summary,
      classification: decision.classification,
      bottleneck: decision.bottleneck,
      recommendation: decision.recommendation,
      failures: [],
      measuredHarnessHead: 'test-head',
      rejectedTimingAttempts: 0,
    };
    expect(serializePerfMarkdown(result)).toContain('Aggregated scenarios');
    expect(serializePerfMarkdown(result)).toContain('Long-task phase attribution');
    expect(serializePerfMarkdown(result)).toContain('Median P95 frame ms');
    expect(serializePerfMarkdown(result)).toContain('Worst run >50ms %');
    expect(classifyPerfBaseline([], aggregatePerfRuns([])).classification).toBe('MEASUREMENT NOT VERIFIED');
    expect(classifyPerfBaseline(
      [
        {
          ...runs[0],
          timingHealth: {
            ...runs[0].timingHealth,
            valid: false,
            issues: ['postflight throttled'],
          },
        },
      ],
      aggregatePerfRuns([]),
    ).classification).toBe('MEASUREMENT NOT VERIFIED');
  });
});
