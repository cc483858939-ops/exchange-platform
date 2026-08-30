import { describe, expect, it } from 'vitest';
import {
  aggregatePerfRuns,
  classifyPerfBaseline,
  percentile,
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

describe('performance metrics', () => {
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

  it('classifies only valid recorded data and serializes the report', () => {
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
    };
    expect(serializePerfMarkdown(result)).toContain('Aggregated scenarios');
    expect(classifyPerfBaseline([], aggregatePerfRuns([])).classification).toBe('MEASUREMENT NOT VERIFIED');
  });
});
