<template>
  <main class="perf-runner">
    <header class="perf-runner__hero">
      <p class="perf-runner__eyebrow">Pre-launch diagnostic</p>
      <h1>Long-list browser baseline</h1>
      <p class="perf-runner__lede">
        Measures the current mounted PostCard architecture at desktop and mobile sizes.
        It does not change the production application or apply an optimization.
      </p>
      <div class="perf-runner__toolbar">
        <button class="perf-runner__run" type="button" :disabled="running" @click="runSuite">
          {{ running ? 'Running matrix…' : suiteResult ? 'Run again' : 'Run full matrix' }}
        </button>
        <span class="perf-runner__hint">20 / 50 / 100 / 200 / 300 · tracked / untracked</span>
      </div>
    </header>

    <section class="perf-runner__progress" aria-live="polite">
      <div class="perf-runner__progress-head">
        <strong>{{ currentScenario }}</strong>
        <span>{{ completedExecutions }} / {{ totalExecutions }} executions</span>
      </div>
      <div class="perf-runner__progress-track" role="progressbar" :aria-valuenow="progressPercent" aria-valuemin="0" aria-valuemax="100">
        <span :style="{ width: `${progressPercent}%` }"></span>
      </div>
      <p v-if="running" class="perf-runner__progress-note">
        Each scenario gets one warm-up and three recorded runs in a fresh iframe.
      </p>
    </section>

    <section v-if="suiteResult" class="perf-runner__results">
      <div class="perf-runner__decision" :class="`perf-runner__decision--${suiteResult.classification.toLowerCase().replaceAll(' ', '-')}`">
        <div>
          <p class="perf-runner__eyebrow">Measured result</p>
          <h2>{{ suiteResult.classification }}</h2>
        </div>
        <p>{{ suiteResult.recommendation }}</p>
      </div>

      <div class="perf-runner__facts">
        <div>
          <span>Bottleneck</span>
          <strong>{{ suiteResult.bottleneck }}</strong>
        </div>
        <div>
          <span>Recorded runs</span>
          <strong>{{ suiteResult.rawRuns.length }}</strong>
        </div>
        <div>
          <span>Failed executions</span>
          <strong>{{ suiteResult.failures.length }}</strong>
        </div>
      </div>

      <div class="perf-runner__exports" aria-label="Export benchmark results">
        <button type="button" @click="copyRawJson">Copy raw JSON</button>
        <button type="button" @click="copySummaryJson">Copy summary JSON</button>
        <button type="button" @click="copyMarkdown">Copy Markdown report</button>
        <span v-if="copyStatus" role="status">{{ copyStatus }}</span>
      </div>

      <div class="perf-runner__table-wrap">
        <table class="perf-runner__table">
          <caption>Aggregated recorded scenarios</caption>
          <thead>
            <tr>
              <th>Viewport</th>
              <th>Count</th>
              <th>Mode</th>
              <th>Run</th>
              <th>Mount</th>
              <th>Append</th>
              <th>P95 frame</th>
              <th>&gt;50 ms</th>
              <th>DOM</th>
              <th>Peak targets</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in suiteResult.summary.rows" :key="row.key">
              <td>{{ row.viewport }}</td>
              <td>{{ row.count }}</td>
              <td>{{ row.trackView ? 'tracked' : 'untracked' }}</td>
              <td>{{ row.runType }}</td>
              <td>{{ formatMs(row.medianMountMs) }}</td>
              <td>{{ formatMs(row.medianAppendMs) }}</td>
              <td>{{ formatMs(row.medianP95FrameMs) }}</td>
              <td>{{ formatPercent(row.worstPercentOver50Ms) }}</td>
              <td>{{ formatNumber(row.medianDomElements) }}</td>
              <td>{{ formatNumber(row.medianPeakObservedTargets) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <details v-if="suiteResult.failures.length > 0" class="perf-runner__failures">
        <summary>Failed executions</summary>
        <ul>
          <li v-for="failure in suiteResult.failures" :key="`${failure.phase}-${failure.scenario}-${failure.error}`">
            {{ failure.phase }} · {{ failure.scenario }} — {{ failure.error }}
          </li>
        </ul>
      </details>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import {
  aggregatePerfRuns,
  classifyPerfBaseline,
  serializePerfMarkdown,
} from './metrics';
import {
  createPerfScenarioPlans,
  PERF_EXECUTIONS_PER_SCENARIO,
  PERF_RECORDED_RUNS,
  scenarioPlanLabel,
  type PerfScenarioPlan,
} from './runnerPlan';
import {
  isPerfScenarioMessage,
  getPerfGitHead,
  type PerfEnvironment,
  type PerfRawRun,
  type PerfScenarioConfig,
  type PerfSuiteResult,
} from './types';

const plans = createPerfScenarioPlans('mixed');
const totalExecutions = plans.length * PERF_EXECUTIONS_PER_SCENARIO;
const running = ref(false);
const completedExecutions = ref(0);
const currentScenario = ref('Ready to run');
const rawRuns = ref<PerfRawRun[]>([]);
const failures = ref<PerfSuiteResult['failures']>([]);
const suiteResult = ref<PerfSuiteResult | null>(null);
const copyStatus = ref('');

const progressPercent = computed(() => (
  totalExecutions > 0 ? Math.round((completedExecutions.value / totalExecutions) * 100) : 0
));

const formatNumber = (value: number | null | undefined): string => (
  typeof value === 'number' && Number.isFinite(value) ? String(Math.round(value)) : 'n/a'
);

const formatMs = (value: number | null | undefined): string => (
  typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)} ms` : 'n/a'
);

const formatPercent = (value: number | null | undefined): string => (
  typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}%` : 'n/a'
);

const captureEnvironment = (): PerfEnvironment => {
  const navigation = navigator as Navigator & { deviceMemory?: number };
  const perf = performance as Performance & {
    memory?: { usedJSHeapSize?: unknown };
  };
  const supportedEntryTypes = typeof PerformanceObserver === 'undefined'
    ? []
    : (PerformanceObserver as typeof globalThis.PerformanceObserver & {
        supportedEntryTypes?: string[];
      }).supportedEntryTypes ?? [];
  const memorySupported = typeof perf.memory?.usedJSHeapSize === 'number';

  return {
    gitHead: getPerfGitHead(),
    timestamp: new Date().toISOString(),
    userAgent: navigator.userAgent,
    platform: navigator.platform || undefined,
    devicePixelRatio: window.devicePixelRatio || 1,
    hardwareConcurrency: navigator.hardwareConcurrency || undefined,
    deviceMemory: typeof navigation.deviceMemory === 'number' ? navigation.deviceMemory : undefined,
    longTaskSupported: supportedEntryTypes.includes('longtask'),
    memorySupported,
  };
};

const configFor = (plan: PerfScenarioPlan, runId: string): PerfScenarioConfig => ({
  ...plan,
  runId,
});

const runScenarioInFreshFrame = (config: PerfScenarioConfig): Promise<PerfRawRun> => (
  new Promise((resolve, reject) => {
    const frame = document.createElement('iframe');
    const scenarioURL = new URL(window.location.href);
    scenarioURL.search = '';
    scenarioURL.hash = '';
    scenarioURL.searchParams.set('scenario', '1');
    scenarioURL.searchParams.set('runId', config.runId);
    scenarioURL.searchParams.set('viewport', config.viewport);
    scenarioURL.searchParams.set('width', String(config.width));
    scenarioURL.searchParams.set('height', String(config.height));
    scenarioURL.searchParams.set('count', String(config.count));
    scenarioURL.searchParams.set('trackView', String(config.trackView));
    scenarioURL.searchParams.set('fixture', config.fixture);
    scenarioURL.searchParams.set('runType', config.runType);
    if (typeof config.appendFrom === 'number') {
      scenarioURL.searchParams.set('appendFrom', String(config.appendFrom));
    }

    frame.className = 'perf-runner__scenario-frame';
    frame.title = 'Long-list performance scenario';
    frame.style.position = 'fixed';
    frame.style.left = '0';
    frame.style.top = '0';
    frame.style.width = `${config.width}px`;
    frame.style.height = `${config.height}px`;
    frame.style.border = '0';
    // Keep the child renderable so Chromium does not throttle requestAnimationFrame
    // as an occluded frame. It remains visually negligible and non-interactive.
    frame.style.opacity = '0.01';
    frame.style.pointerEvents = 'none';
    frame.style.zIndex = '1';

    let settled = false;
    const timeoutID = window.setTimeout(() => {
      settleReject(new Error('scenario timed out after 60 seconds'));
    }, 60_000);

    const cleanup = () => {
      window.clearTimeout(timeoutID);
      window.removeEventListener('message', handleMessage);
      frame.remove();
    };
    const settleResolve = (result: PerfRawRun) => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(result);
    };
    const settleReject = (error: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(error);
    };
    const handleMessage = (event: MessageEvent<unknown>) => {
      if (event.source !== frame.contentWindow || event.origin !== window.location.origin) {
        return;
      }
      if (!isPerfScenarioMessage(event.data) || event.data.runId !== config.runId) {
        return;
      }
      if (event.data.type === 'scenario-error') {
        settleReject(new Error(event.data.error));
        return;
      }
      settleResolve(event.data.result);
    };

    window.addEventListener('message', handleMessage);
    frame.src = scenarioURL.toString();
    document.body.appendChild(frame);
  })
);

const runSuite = async () => {
  if (running.value) return;
  running.value = true;
  completedExecutions.value = 0;
  currentScenario.value = 'Starting matrix';
  rawRuns.value = [];
  failures.value = [];
  suiteResult.value = null;
  copyStatus.value = '';
  window.__EXCHANGE_PERF_RESULT__ = undefined;
  const environment = captureEnvironment();

  for (let planIndex = 0; planIndex < plans.length; planIndex += 1) {
    const plan = plans[planIndex];
    currentScenario.value = scenarioPlanLabel(plan);
    for (let executionIndex = 0; executionIndex < PERF_EXECUTIONS_PER_SCENARIO; executionIndex += 1) {
      const phase = executionIndex === 0 ? 'warm-up' : 'recorded';
      const runNumber = executionIndex === 0 ? 0 : executionIndex;
      const runId = `scenario-${planIndex + 1}-run-${runNumber}`;
      try {
        const result = await runScenarioInFreshFrame(configFor(plan, runId));
        if (executionIndex > 0) {
          rawRuns.value.push(result);
        }
      } catch (error) {
        failures.value.push({
          scenario: scenarioPlanLabel(plan),
          phase,
          error: error instanceof Error ? error.message : String(error),
        });
      } finally {
        completedExecutions.value += 1;
      }
    }
  }

  const summary = aggregatePerfRuns(rawRuns.value);
  const decision = classifyPerfBaseline(rawRuns.value, summary);
  const result: PerfSuiteResult = {
    status: 'completed',
    environment,
    rawRuns: rawRuns.value,
    summary,
    classification: decision.classification,
    bottleneck: decision.bottleneck,
    recommendation: decision.recommendation,
    failures: failures.value,
  };
  suiteResult.value = result;
  window.__EXCHANGE_PERF_RESULT__ = result;
  currentScenario.value = failures.value.length > 0 ? 'Completed with failures' : 'Completed';
  running.value = false;
};

const copyText = async (label: string, value: string) => {
  try {
    await navigator.clipboard.writeText(value);
    copyStatus.value = `${label} copied`;
  } catch {
    copyStatus.value = 'Clipboard unavailable in this browser context';
  }
};

const copyRawJson = () => {
  if (suiteResult.value) {
    void copyText('Raw JSON', JSON.stringify(suiteResult.value.rawRuns, null, 2));
  }
};

const copySummaryJson = () => {
  if (suiteResult.value) {
    void copyText('Summary JSON', JSON.stringify(suiteResult.value.summary, null, 2));
  }
};

const copyMarkdown = () => {
  if (suiteResult.value) {
    void copyText('Markdown report', serializePerfMarkdown(suiteResult.value));
  }
};

onMounted(() => {
  if (new URLSearchParams(window.location.search).get('autorun') === '1') {
    void runSuite();
  }
});
</script>

<style scoped>
.perf-runner {
  width: min(100% - 32px, 1180px);
  margin: 0 auto;
  padding: 56px 0 80px;
}

.perf-runner__hero {
  max-width: 760px;
}

.perf-runner__eyebrow {
  margin: 0 0 var(--space-2);
  color: var(--color-accent);
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.perf-runner h1,
.perf-runner h2 {
  margin: 0;
  letter-spacing: -0.03em;
}

.perf-runner h1 {
  font-size: clamp(34px, 6vw, 64px);
  line-height: 0.98;
}

.perf-runner h2 {
  font-size: clamp(24px, 4vw, 36px);
}

.perf-runner__lede {
  max-width: 660px;
  margin: var(--space-5) 0 0;
  color: var(--color-text-secondary);
  font-size: 17px;
  line-height: 1.55;
}

.perf-runner__toolbar,
.perf-runner__exports,
.perf-runner__progress-head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.perf-runner__toolbar {
  flex-wrap: wrap;
  margin-top: var(--space-6);
}

.perf-runner button {
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: 10px 16px;
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font-weight: 700;
}

.perf-runner button:hover:not(:disabled),
.perf-runner button:focus-visible {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.perf-runner button:disabled {
  cursor: wait;
  opacity: 0.6;
}

.perf-runner__run {
  border-color: var(--color-accent) !important;
  background: var(--color-accent) !important;
  color: white !important;
}

.perf-runner__hint,
.perf-runner__progress-head span,
.perf-runner__progress-note {
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.perf-runner__progress {
  margin-top: 56px;
  padding: var(--space-4) 0;
  border-top: 1px solid var(--color-border);
  border-bottom: 1px solid var(--color-border);
}

.perf-runner__progress-head {
  justify-content: space-between;
}

.perf-runner__progress-track {
  height: 8px;
  margin-top: var(--space-3);
  overflow: hidden;
  border-radius: var(--radius-pill);
  background: var(--color-surface-subtle);
}

.perf-runner__progress-track span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--color-accent);
  transition: width 180ms ease;
}

.perf-runner__progress-note {
  margin: var(--space-2) 0 0;
}

.perf-runner__results {
  margin-top: 40px;
}

.perf-runner__decision {
  display: grid;
  grid-template-columns: minmax(220px, 0.7fr) minmax(0, 1.3fr);
  gap: var(--space-6);
  align-items: end;
  padding: var(--space-6);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-surface-subtle);
}

.perf-runner__decision p:last-child {
  margin: 0;
  color: var(--color-text-secondary);
  line-height: 1.55;
}

.perf-runner__decision--green {
  border-color: color-mix(in srgb, #16803c 38%, var(--color-border));
}

.perf-runner__decision--yellow {
  border-color: color-mix(in srgb, #9a6700 38%, var(--color-border));
}

.perf-runner__decision--red,
.perf-runner__decision--measurement-not-verified {
  border-color: color-mix(in srgb, var(--color-danger) 38%, var(--color-border));
}

.perf-runner__facts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  margin-top: var(--space-5);
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-border);
}

.perf-runner__facts > div {
  min-width: 0;
  padding: var(--space-4);
  background: var(--color-surface);
}

.perf-runner__facts span {
  display: block;
  margin-bottom: var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.perf-runner__facts strong {
  display: block;
  overflow-wrap: anywhere;
  font-size: 14px;
  line-height: 1.4;
}

.perf-runner__exports {
  flex-wrap: wrap;
  margin-top: var(--space-5);
}

.perf-runner__exports span {
  color: var(--color-accent);
  font-size: 13px;
}

.perf-runner__table-wrap {
  margin-top: var(--space-6);
  overflow-x: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

.perf-runner__table {
  width: 100%;
  min-width: 860px;
  border-collapse: collapse;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.perf-runner__table caption {
  padding: var(--space-4);
  color: var(--color-text);
  font-size: 15px;
  font-weight: 750;
  text-align: left;
}

.perf-runner__table th,
.perf-runner__table td {
  padding: 11px 12px;
  border-top: 1px solid var(--color-border);
  text-align: left;
  white-space: nowrap;
}

.perf-runner__table th {
  color: var(--color-text-tertiary);
  font-size: 11px;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.perf-runner__failures {
  margin-top: var(--space-5);
  color: var(--color-danger);
  font-size: 13px;
}

.perf-runner__failures ul {
  padding-left: 20px;
  line-height: 1.6;
}

@media (max-width: 700px) {
  .perf-runner {
    width: min(100% - 24px, 1180px);
    padding-top: 32px;
  }

  .perf-runner__decision,
  .perf-runner__facts {
    grid-template-columns: 1fr;
  }
}
</style>
