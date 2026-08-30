<template>
  <main class="perf-scenario">
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
import { summarizeFrameDeltas } from './metrics';
import { installIntersectionObserverInstrumentation } from './observerInstrumentation';
import {
  PERF_MESSAGE_NAMESPACE,
  PERF_SCENARIO_COMPLETE,
  PERF_SCENARIO_ERROR,
  type PerfLongTaskMetrics,
  type PerfMemoryMetrics,
  type PerfRawRun,
  type PerfScenarioConfig,
  type PerfScenarioMessage,
} from './types';

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

// The perf preview has its own origin. Clearing that origin guarantees that this
// isolated Pinia instance cannot accidentally use a valid viewer from the SPA.
authStore.clearAuth();

const waitForFrame = (): Promise<number> => new Promise(resolve => {
  window.requestAnimationFrame(resolve);
});

const waitForTwoFrames = async (): Promise<void> => {
  await waitForFrame();
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
  disconnect: () => void;
} => {
  if (!supportsLongTasks()) {
    return {
      metrics: { supported: false },
      disconnect: () => undefined,
    };
  }

  const durations: number[] = [];
  try {
    const observer = new PerformanceObserver(list => {
      for (const entry of list.getEntries()) {
        if (Number.isFinite(entry.duration)) {
          durations.push(entry.duration);
        }
      }
    });
    observer.observe({ type: 'longtask', buffered: true });

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
      disconnect: () => observer.disconnect(),
    };
  } catch {
    return {
      metrics: { supported: false },
      disconnect: () => undefined,
    };
  }
};

const scrollAndMeasure = async (): Promise<ReturnType<typeof summarizeFrameDeltas>> => {
  const documentHeight = Math.max(
    document.documentElement.scrollHeight,
    document.body.scrollHeight,
  );
  const maxScroll = Math.max(0, documentHeight - window.innerHeight);
  const durationMs = 4_000;
  const deltas: number[] = [];
  window.scrollTo(0, 0);
  await waitForFrame();

  const startedAt = performance.now();
  let previousAt = startedAt;
  while (true) {
    const frameAt = await waitForFrame();
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
  return summarizeFrameDeltas(deltas);
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

const sendMessage = (message: PerfScenarioMessage) => {
  if (window.parent !== window) {
    window.parent.postMessage(message, window.location.origin);
  }
};

const sendError = (error: unknown) => {
  if (completed) {
    return;
  }
  completed = true;
  cleanup();
  sendMessage({
    namespace: PERF_MESSAGE_NAMESPACE,
    type: PERF_SCENARIO_ERROR,
    runId: config.runId,
    error: error instanceof Error ? error.message : String(error),
  });
};

const runScenario = async () => {
  let longTaskCollector: ReturnType<typeof collectLongTasks> | null = null;
  try {
    longTaskCollector = collectLongTasks();
    const memoryBeforeMount = readHeap();
    const mountStartedAt = performance.now();
    posts.value = createPerfPosts(config.count, config.fixture);
    await nextTick();
    await waitForTwoFrames();
    const mountMs = performance.now() - mountStartedAt;
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
      append = {
        measured: true,
        from: appendFrom,
        to: appendFrom + 20,
        durationMs: performance.now() - appendStartedAt,
      };
    }

    const scroll = await scrollAndMeasure();
    await waitForFrame();
    longTaskCollector.disconnect();
    const memoryAfterScroll = readHeap();
    const targetsBeforeCleanup = observerInstrumentation.snapshot().currentTargets;
    const observerAfterCleanup = cleanup();
    const memorySupported = memoryBeforeMount !== null
      && memoryAfterMount !== null
      && memoryAfterScroll !== null;
    const finalPostCards = config.count + (append.measured ? 20 : 0);
    const issues: string[] = [];
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

    const longTasks: PerfLongTaskMetrics = longTaskCollector.metrics.supported
      ? {
          supported: true,
          count: longTaskCollector.metrics.count,
          longestMs: longTaskCollector.metrics.longestMs,
          totalMs: longTaskCollector.metrics.totalMs,
        }
      : { supported: false };
    const memory: PerfMemoryMetrics = memorySupported
      ? {
          supported: true,
          beforeMount: memoryBeforeMount ?? undefined,
          afterMount: memoryAfterMount ?? undefined,
          afterScroll: memoryAfterScroll ?? undefined,
        }
      : { supported: false };
    const result: PerfRawRun = {
      scenario: {
        viewport: config.viewport,
        width: config.width,
        height: config.height,
        innerWidth: window.innerWidth,
        innerHeight: window.innerHeight,
        count: config.count,
        trackView: config.trackView,
        fixture: config.fixture,
        runType: config.runType,
      },
      render: {
        requestedPosts: config.count,
        postCards: renderedPostCards,
        domElements,
        mountMs,
      },
      append,
      scroll,
      longTasks,
      memory,
      observer: {
        ...observerAfterCleanup,
        targetsBeforeCleanup,
      },
      validation: {
        valid: issues.length === 0,
        issues,
        telemetryNetworkRequests,
      },
    };
    completed = true;
    sendMessage({
      namespace: PERF_MESSAGE_NAMESPACE,
      type: PERF_SCENARIO_COMPLETE,
      runId: config.runId,
      result,
    });
  } catch (error) {
    longTaskCollector?.disconnect();
    sendError(error);
  }
};

onMounted(() => {
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

.perf-scenario__list {
  width: min(100%, var(--shell-main-width));
  min-height: 100vh;
  margin: 0 auto;
  background: var(--color-surface);
}
</style>
