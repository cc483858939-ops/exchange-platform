<template>
  <main class="perf-scenario">
    <p v-if="fatalError" class="perf-scenario__error" role="alert">{{ fatalError }}</p>
    <section ref="listRoot" class="perf-scenario__list" aria-label="Performance benchmark post list">
      <PostCard
        v-for="post in posts"
        :key="post.id"
        :post="post"
        :track-view="config.trackView"
      />
    </section>
  </main>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useAuthStore } from '../store/auth';
import PostCard from '../components/feed/PostCard.vue';
import { resetArticleViewTelemetryForTests } from '../services/articleViewTelemetry';
import { createPerfPosts } from './fixtures';
import {
  attributeLongTasksByPhase,
  assessRafCadence,
  assessScrollTiming,
  assessTimingHealth,
  PERF_RAF_HEALTH_SAMPLES,
  summarizeFrameDeltas,
} from './metrics';
import type {
  LongTaskTimingWindow,
  PerfLongTaskEntry,
  PerfLongTaskPhase,
  RafHealthProbe,
} from './metrics';
import { installIntersectionObserverInstrumentation } from './observerInstrumentation';
import {
  PERF_PENDING_STORAGE_KEY,
  PERF_SESSION_SCHEMA_VERSION,
  isViewportWithinTolerance,
  perfViewportRequirement,
} from './topLevelSuiteSession';
import {
  type PerfLongTaskMetrics,
  type PerfMemoryMetrics,
  type PerfPendingScenarioEnvelope,
  type PerfRawRun,
  type PerfRawScenario,
  type PerfScenarioConfig,
} from './types';
import { hasPerfOrchestrationIds } from './scenarioConfig';

const props = defineProps<{
  config: PerfScenarioConfig;
}>();

const config = props.config;
const posts = ref<ReturnType<typeof createPerfPosts>>([]);
const listRoot = ref<HTMLElement | null>(null);
const observerInstrumentation = installIntersectionObserverInstrumentation();
const authStore = useAuthStore();
let cleanedUp = false;
let completed = false;
const fatalError = ref<string | null>(null);

// The perf preview has its own origin. Clearing that origin guarantees that this
// isolated Pinia instance cannot accidentally use a valid viewer from the SPA.
authStore.clearAuth();

const RAF_PROBE_TIMEOUT_MS = 750;

const waitForFrame = (): Promise<number | null> => new Promise(resolve => {
  let settled = false;
  const timeoutID = window.setTimeout(() => {
    settled = true;
    resolve(null);
  }, RAF_PROBE_TIMEOUT_MS);

  window.requestAnimationFrame(timestamp => {
    if (settled) {
      return;
    }
    settled = true;
    window.clearTimeout(timeoutID);
    resolve(timestamp);
  });
});

const waitForTwoFrames = async (): Promise<void> => {
  const firstFrame = await waitForFrame();
  if (firstFrame === null) {
    return;
  }
  await waitForFrame();
};

type PerformanceWithMemory = Performance & {
  memory?: {
    usedJSHeapSize?: unknown;
  };
};

const readHeap = (): number | null => {
  const raw = (window.performance as PerformanceWithMemory).memory?.usedJSHeapSize;
  return typeof raw === 'number' && Number.isFinite(raw) ? raw : null;
};

const supportsLongTasks = (): boolean => {
  if (typeof window.PerformanceObserver === 'undefined') {
    return false;
  }
  const supportedEntryTypes = (window.PerformanceObserver as typeof PerformanceObserver & {
    supportedEntryTypes?: string[];
  }).supportedEntryTypes;
  return Array.isArray(supportedEntryTypes) && supportedEntryTypes.includes('longtask');
};

const collectLongTasks = (): {
  metrics: PerfLongTaskMetrics;
  entries: () => PerfLongTaskEntry[];
  disconnect: () => void;
} => {
  if (!supportsLongTasks()) {
    return {
      metrics: { supported: false },
      entries: () => [],
      disconnect: () => undefined,
    };
  }

  const durations: number[] = [];
  const entries: PerfLongTaskEntry[] = [];
  try {
    const observer = new PerformanceObserver(list => {
      for (const entry of list.getEntries()) {
        if (Number.isFinite(entry.duration)) {
          durations.push(entry.duration);
          if (Number.isFinite(entry.startTime)) {
            entries.push({
              startTime: entry.startTime,
              duration: entry.duration,
            });
          }
        }
      }
    });
    // The collector starts after preflight, so buffered page-startup tasks would
    // contaminate the scenario total and could not be attributed to a phase.
    observer.observe({ type: 'longtask', buffered: false });

    return {
      metrics: {
        supported: true,
        get count() {
          return durations.length;
        },
        get longestMs() {
          return durations.length > 0 ? Math.max(...durations) : 0;
        },
        get totalMs() {
          return durations.reduce((total, duration) => total + duration, 0);
        },
      },
      entries: () => entries.slice(),
      disconnect: () => observer.disconnect(),
    };
  } catch {
    return {
      metrics: { supported: false },
      entries: () => [],
      disconnect: () => undefined,
    };
  }
};

type RafProbe = RafHealthProbe;

const probeRafCadence = async (phase: string): Promise<RafProbe> => {
  if (document.visibilityState !== 'visible') {
    const assessment = assessRafCadence([], phase);
    return {
      intervals: [],
      metrics: assessment.metrics,
      issues: [
        ...assessment.issues,
        `${phase} requires document.visibilityState=visible`,
      ],
    };
  }

  const intervals: number[] = [];
  let previousAt = await waitForFrame();
  while (previousAt !== null && intervals.length < PERF_RAF_HEALTH_SAMPLES) {
    if (document.visibilityState !== 'visible') {
      break;
    }
    const frameAt = await waitForFrame();
    if (frameAt === null) {
      break;
    }
    const delta = frameAt - previousAt;
    if (Number.isFinite(delta) && delta >= 0) {
      intervals.push(delta);
    }
    previousAt = frameAt;
  }

  const assessment = assessRafCadence(intervals, phase);
  return {
    intervals,
    metrics: assessment.metrics,
    issues: assessment.issues,
  };
};

interface ScrollMeasurement {
  metrics: ReturnType<typeof summarizeFrameDeltas>;
  deltas: number[];
}

const scrollAndMeasure = async (): Promise<ScrollMeasurement> => {
  const documentHeight = Math.max(
    document.documentElement.scrollHeight,
    document.body.scrollHeight,
  );
  const maxScroll = Math.max(0, documentHeight - window.innerHeight);
  const durationMs = 4_000;
  const deltas: number[] = [];
  window.scrollTo(0, 0);
  const initialFrame = await waitForFrame();
  if (initialFrame === null) {
    return { metrics: summarizeFrameDeltas(deltas), deltas };
  }

  const startedAt = performance.now();
  let previousAt = initialFrame;
  while (document.visibilityState === 'visible') {
    const frameAt = await waitForFrame();
    if (frameAt === null) {
      break;
    }
    const delta = frameAt - previousAt;
    if (Number.isFinite(delta) && delta >= 0) {
      deltas.push(delta);
    }
    previousAt = frameAt;

    const progress = Math.min(1, (frameAt - startedAt) / durationMs);
    const travel = progress <= 0.5 ? progress * 2 : (1 - progress) * 2;
    window.scrollTo(0, maxScroll * Math.max(0, travel));
    if (progress >= 1) {
      break;
    }
  }

  await waitForFrame();
  return { metrics: summarizeFrameDeltas(deltas), deltas };
};

const cleanup = () => {
  if (cleanedUp) {
    return observerInstrumentation.snapshot();
  }
  resetArticleViewTelemetryForTests();
  const snapshot = observerInstrumentation.snapshot();
  observerInstrumentation.restore();
  cleanedUp = true;
  return snapshot;
};

const telemetryNetworkRequestCount = (): number => (
  performance.getEntriesByType('resource')
    .filter(entry => entry.name.includes('article-view-events'))
    .length
);

const executionContext = (): PerfRawRun['executionContext'] => ({
  topLevel: window.parent === window,
  visibilityState: document.visibilityState,
  devicePixelRatio: window.devicePixelRatio || 1,
  userAgent: navigator.userAgent,
});

const rawScenario = (): PerfRawScenario => ({
  viewport: config.viewport,
  width: config.width,
  height: config.height,
  innerWidth: window.innerWidth,
  innerHeight: window.innerHeight,
  count: config.count,
  trackView: config.trackView,
  fixture: config.fixture,
  runType: config.runType,
});

const executionValidationIssues = (): string[] => {
  const issues: string[] = [];
  const requirement = perfViewportRequirement(config.viewport);
  if (window.parent !== window) {
    issues.push('Performance scenario must execute as a top-level document.');
  }
  if (!isViewportWithinTolerance(
    window.innerWidth,
    window.innerHeight,
    requirement.width,
    requirement.height,
  )) {
    issues.push(
      `viewport ${window.innerWidth}x${window.innerHeight} does not match `
      + `${config.viewport} target ${requirement.width}x${requirement.height} ±8px`,
    );
  }
  return issues;
};

const persistPending = (pending: PerfPendingScenarioEnvelope): void => {
  try {
    window.sessionStorage.setItem(
      PERF_PENDING_STORAGE_KEY,
      JSON.stringify(pending),
    );
    window.location.replace('/?autorun=1&resume=1');
  } catch (error) {
    fatalError.value = `SESSION_STORAGE_UNAVAILABLE: ${error instanceof Error ? error.message : String(error)}`;
  }
};

const sendResult = (result: PerfRawRun): void => {
  if (completed) return;
  completed = true;
  persistPending({
    schemaVersion: PERF_SESSION_SCHEMA_VERSION,
    suiteId: config.suiteId,
    runId: config.runId,
    type: 'result',
    result,
  });
};

const sendError = (error: unknown): void => {
  if (completed) return;
  completed = true;
  cleanup();
  persistPending({
    schemaVersion: PERF_SESSION_SCHEMA_VERSION,
    suiteId: config.suiteId,
    runId: config.runId,
    type: 'error',
    error: error instanceof Error ? error.message : String(error),
  });
};

const runScenario = async () => {
  let longTaskCollector: ReturnType<typeof collectLongTasks> | null = null;
  const phaseWindows: Partial<Record<PerfLongTaskPhase, LongTaskTimingWindow>> = {};
  let visibilityLost = document.visibilityState !== 'visible';
  const handleVisibilityChange = () => {
    if (document.visibilityState !== 'visible') {
      visibilityLost = true;
    }
  };

  document.addEventListener('visibilitychange', handleVisibilityChange);
  try {
    const preflight = await probeRafCadence('preflight');
    if (document.visibilityState !== 'visible') {
      visibilityLost = true;
    }

    if (preflight.issues.length > 0 || visibilityLost) {
      const postScroll = await probeRafCadence('postflight after scroll');
      const targetsBeforeCleanup = observerInstrumentation.snapshot().currentTargets;
      const observerAfterCleanup = cleanup();
      const postCleanup = await probeRafCadence('postflight after PostCard cleanup');
      const timingHealth = assessTimingHealth(
        preflight,
        postScroll,
        postCleanup,
        [],
        visibilityLost,
      );
      const result: PerfRawRun = {
        scenario: rawScenario(),
        executionContext: executionContext(),
        render: {
          requestedPosts: config.count,
          postCards: 0,
          domElements: 0,
          mountMs: 0,
        },
        append: {
          measured: false,
          from: 0,
          to: 0,
          durationMs: 0,
        },
        scroll: summarizeFrameDeltas([]),
        longTasks: { supported: false },
        memory: { supported: false },
        observer: {
          ...observerAfterCleanup,
          targetsBeforeCleanup,
        },
        timingHealth,
        validation: {
          valid: false,
          issues: Array.from(new Set([
            ...timingHealth.issues,
            ...executionValidationIssues(),
          ])),
          telemetryNetworkRequests: 0,
        },
      };
      sendResult(result);
      return;
    }

    longTaskCollector = collectLongTasks();
    const memoryBeforeMount = readHeap();
    const mountStartedAt = performance.now();
    posts.value = createPerfPosts(config.count, config.fixture);
    await nextTick();
    await waitForTwoFrames();
    const mountEndedAt = performance.now();
    phaseWindows.mount = { startTime: mountStartedAt, endTime: mountEndedAt };
    const mountMs = mountEndedAt - mountStartedAt;
    const renderedPostCards = listRoot.value?.querySelectorAll('.post-card').length ?? 0;
    const domElements = listRoot.value?.querySelectorAll('*').length ?? 0;
    const memoryAfterMount = readHeap();

    const appendFrom = config.appendFrom;
    let append = {
      measured: false,
      from: 0,
      to: 0,
      durationMs: 0,
    };
    if (typeof appendFrom === 'number') {
      if (posts.value.length !== appendFrom || appendFrom + 20 > 300) {
        throw new Error(`invalid append base ${appendFrom}`);
      }
      const appendStartedAt = performance.now();
      posts.value = [
        ...posts.value,
        ...createPerfPosts(20, config.fixture, appendFrom + 1),
      ];
      await nextTick();
      await waitForTwoFrames();
      const appendEndedAt = performance.now();
      phaseWindows.append = { startTime: appendStartedAt, endTime: appendEndedAt };
      append = {
        measured: true,
        from: appendFrom,
        to: appendFrom + 20,
        durationMs: appendEndedAt - appendStartedAt,
      };
    }

    const scrollStartedAt = performance.now();
    const scrollMeasurement = await scrollAndMeasure();
    const scrollEndedAt = performance.now();
    phaseWindows.scroll = { startTime: scrollStartedAt, endTime: scrollEndedAt };
    await waitForFrame();
    const longTaskEntries = longTaskCollector.entries();
    longTaskCollector.disconnect();
    const longTasks: PerfLongTaskMetrics = longTaskCollector.metrics.supported
      ? {
          supported: true,
          count: longTaskCollector.metrics.count,
          longestMs: longTaskCollector.metrics.longestMs,
          totalMs: longTaskCollector.metrics.totalMs,
          phases: attributeLongTasksByPhase(longTaskEntries, phaseWindows),
        }
      : { supported: false };
    const scrollTiming = assessScrollTiming(scrollMeasurement.deltas, longTasks);
    const postScroll = await probeRafCadence('postflight after scroll');
    const memoryAfterScroll = readHeap();
    const targetsBeforeCleanup = observerInstrumentation.snapshot().currentTargets;
    const observerAfterCleanup = cleanup();
    const postCleanup = await probeRafCadence('postflight after PostCard cleanup');
    const timingHealth = assessTimingHealth(
      preflight,
      postScroll,
      postCleanup,
      scrollTiming.issues,
      visibilityLost,
    );
    const memorySupported = memoryBeforeMount !== null
      && memoryAfterMount !== null
      && memoryAfterScroll !== null;
    const finalPostCards = config.count + (append.measured ? 20 : 0);
    const issues: string[] = [];
    issues.push(...executionValidationIssues());
    if (renderedPostCards !== config.count) {
      issues.push(`requested ${config.count} cards but rendered ${renderedPostCards}`);
    }
    if (!observerAfterCleanup.supported && config.trackView) {
      issues.push('native IntersectionObserver is unavailable');
    }
    if (config.trackView && targetsBeforeCleanup < finalPostCards) {
      issues.push(`tracked mode observed ${targetsBeforeCleanup}/${finalPostCards} cards`);
    }
    if (!config.trackView && observerAfterCleanup.observeCalls !== 0) {
      issues.push('untracked mode observed PostCard targets');
    }
    if (observerAfterCleanup.currentTargets !== 0) {
      issues.push(`observer cleanup left ${observerAfterCleanup.currentTargets} targets`);
    }
    const telemetryNetworkRequests = telemetryNetworkRequestCount();
    if (telemetryNetworkRequests !== 0) {
      issues.push('article-view-events network traffic was observed');
    }
    const memory: PerfMemoryMetrics = memorySupported
      ? {
          supported: true,
          beforeMount: memoryBeforeMount ?? undefined,
          afterMount: memoryAfterMount ?? undefined,
          afterScroll: memoryAfterScroll ?? undefined,
        }
      : { supported: false };
    const result: PerfRawRun = {
      scenario: rawScenario(),
      executionContext: executionContext(),
      render: {
        requestedPosts: config.count,
        postCards: renderedPostCards,
        domElements,
        mountMs,
      },
      append,
      scroll: scrollMeasurement.metrics,
      longTasks,
      memory,
      observer: {
        ...observerAfterCleanup,
        targetsBeforeCleanup,
      },
      timingHealth,
      validation: {
        valid: issues.length === 0 && timingHealth.valid,
        issues: Array.from(new Set([...issues, ...timingHealth.issues])),
        telemetryNetworkRequests,
      },
    };
    sendResult(result);
  } catch (error) {
    longTaskCollector?.disconnect();
    sendError(error);
  } finally {
    document.removeEventListener('visibilitychange', handleVisibilityChange);
  }
};

onMounted(() => {
  if (!hasPerfOrchestrationIds(config)) {
    fatalError.value = 'Missing suiteId/runId; this scenario was not launched by the top-level performance runner.';
    return;
  }
  void runScenario();
});

onBeforeUnmount(() => {
  cleanup();
});
</script>

<style scoped>
.perf-scenario {
  min-height: 100vh;
  background: var(--color-bg);
}

.perf-scenario__error {
  position: fixed;
  z-index: 1;
  inset: 16px;
  height: fit-content;
  margin: 0 auto;
  padding: 16px;
  border: 1px solid var(--color-danger);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  color: var(--color-danger);
}

.perf-scenario__list {
  width: min(100%, var(--shell-main-width));
  min-height: 100vh;
  margin: 0 auto;
  background: var(--color-surface);
}
</style>
