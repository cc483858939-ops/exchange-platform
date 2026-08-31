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
        <button type="button" :disabled="runnerState.status === 'running'" @click="resetAndStart">Reset / Start New Suite</button>
        <span class="perf-runner__hint">20 / 50 / 100 / 200 / 300 · tracked / untracked</span>
      </div>
    </header>

    <section class="perf-runner__progress" aria-live="polite">
      <div class="perf-runner__progress-head">
        <strong>{{ currentScenario }}</strong>
        <span>{{ completedExecutions }} / {{ totalExecutions }} logical slots</span>
      </div>
      <div
        class="perf-runner__progress-track"
        role="progressbar"
        :aria-valuenow="progressPercent"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <span :style="{ width: `${progressPercent}%` }"></span>
      </div>
      <p v-if="running" class="perf-runner__progress-note">
        Each plan runs as a top-level document with one warm-up and three timing-valid recorded runs.
        Timing-invalid recorded slots retry through persisted cursor transitions; retries do not add logical slots.
      </p>
    </section>

    <section v-if="runnerState.status === 'waiting-for-viewport'" class="perf-runner__notice" aria-live="polite">
      <p class="perf-runner__eyebrow">Viewport required</p>
      <h2>Waiting for {{ runnerState.viewportRequirement?.viewport }} viewport</h2>
      <p>
        Resize the current browser CSS viewport to approximately
        {{ runnerState.viewportRequirement?.width }} × {{ runnerState.viewportRequirement?.height }} CSS px.
        The runner will resume automatically after the resize settles.
      </p>
    </section>

    <section v-if="runnerState.status === 'failed'" class="perf-runner__notice perf-runner__notice--error" aria-live="assertive">
      <p class="perf-runner__eyebrow">Benchmark stopped</p>
      <h2>{{ runnerState.fatalCode || 'RUNNER_FAILED' }}</h2>
      <p>{{ runnerState.fatalError || 'The performance harness could not continue.' }}</p>
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
        <div>
          <span>Rejected timing attempts</span>
          <strong>{{ suiteResult.rejectedTimingAttempts }}</strong>
        </div>
      </div>

      <div class="perf-runner__exports" aria-label="Export benchmark results">
        <button type="button" @click="copyFullJson">Copy full baseline JSON</button>
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
              <th>Median P95 frame</th>
              <th>Worst run &gt;50ms %</th>
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
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import {
  aggregatePerfRuns,
  classifyPerfBaseline,
  serializePerfMarkdown,
} from './metrics';
import {
  createPerfScenarioPlans,
  PERF_EXECUTIONS_PER_SCENARIO,
  scenarioPlanLabel,
  type PerfScenarioPlan,
} from './runnerPlan';
import {
  applyPendingExecution,
  buildPerfSuiteResult,
  completeSuite,
  createSuiteSession,
  failSuite,
  getNextExecution,
  isPerfTopLevelSuiteSession,
  isViewportWithinTolerance,
  markViewportWaiting,
  parsePendingScenarioEnvelope,
  parseSuiteSession,
  PERF_PENDING_STORAGE_KEY,
  PERF_SESSION_STORAGE_KEY,
  PERF_STORAGE_PROBE_KEY,
  perfViewportRequirement,
  prepareNextExecution,
  serializeSuiteSession,
  type PerfPendingApplyResult,
  type PerfScenarioExecution,
  type PerfTopLevelSuiteSession,
} from './topLevelSuiteSession';
import {
  getPerfGitHead,
  type PerfEnvironment,
  type PerfRunnerState,
  type PerfScenarioConfig,
  type PerfSuiteResult,
} from './types';

const plans = createPerfScenarioPlans('mixed');
const totalExecutions = plans.length * PERF_EXECUTIONS_PER_SCENARIO;
const session = ref<PerfTopLevelSuiteSession | null>(null);
const suiteResult = ref<PerfSuiteResult | null>(null);
const copyStatus = ref('');
const runnerState = ref<PerfRunnerState>({
  status: 'running',
  acceptedRuns: 0,
  completedSlots: 0,
});
let resizeTimer: number | undefined;
let transitionInProgress = false;

const running = computed(() => (
  session.value?.status === 'running' || session.value?.status === 'waiting-for-viewport'
));

const completedExecutions = computed(() => (
  Math.min(totalExecutions, session.value?.completedSlots ?? 0)
));

const progressPercent = computed(() => (
  totalExecutions > 0 ? Math.round((completedExecutions.value / totalExecutions) * 100) : 0
));

const currentScenario = computed(() => {
  const current = session.value;
  if (!current) return 'Ready to run';
  if (current.status === 'waiting-for-viewport') {
    const plan = plans[current.planIndex];
    return plan ? `Waiting · ${scenarioPlanLabel(plan)}` : 'Waiting for viewport';
  }
  if (current.status === 'failed') return current.fatalCode || 'Benchmark stopped';
  if (current.status === 'completed') return 'Completed';
  const plan = plans[current.planIndex];
  if (!plan) return 'Finalizing';
  return `${scenarioPlanLabel(plan)} · ${current.phase}${current.phase === 'recorded' ? ` ${current.recordedIndex + 1}/3` : ''}`;
});

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

const publishRunnerState = (
  current: PerfTopLevelSuiteSession | null,
  viewportRequirement?: PerfRunnerState['viewportRequirement'],
): void => {
  const effectiveViewportRequirement = current?.status === 'waiting-for-viewport'
    ? viewportRequirement
      ?? (plans[current.planIndex] ? perfViewportRequirement(plans[current.planIndex].viewport) : undefined)
    : viewportRequirement;
  const nextState: PerfRunnerState = current
    ? {
        status: current.status,
        ...(current.fatalCode ? { fatalCode: current.fatalCode } : {}),
        ...(current.fatalError ? { fatalError: current.fatalError } : {}),
        acceptedRuns: current.rawRuns.length,
        completedSlots: Math.min(totalExecutions, current.completedSlots),
        ...(effectiveViewportRequirement ? { viewportRequirement: effectiveViewportRequirement } : {}),
      }
    : {
        status: runnerState.value.status,
        ...(runnerState.value.fatalCode ? { fatalCode: runnerState.value.fatalCode } : {}),
        ...(runnerState.value.fatalError ? { fatalError: runnerState.value.fatalError } : {}),
        acceptedRuns: 0,
        completedSlots: 0,
      };
  runnerState.value = nextState;
  window.__EXCHANGE_PERF_RUNNER_STATE__ = nextState;
  if (effectiveViewportRequirement) {
    window.__EXCHANGE_PERF_VIEWPORT_REQUIREMENT__ = effectiveViewportRequirement;
  } else {
    window.__EXCHANGE_PERF_VIEWPORT_REQUIREMENT__ = undefined;
  }
};

const setRunnerFailure = (code: string, error: string, persist = true): void => {
  suiteResult.value = null;
  window.__EXCHANGE_PERF_RESULT__ = undefined;
  if (session.value) {
    const failed = failSuite(session.value, code, error);
    session.value = failed;
    if (persist) {
      try {
        window.sessionStorage.setItem(PERF_SESSION_STORAGE_KEY, serializeSuiteSession(failed));
      } catch {
        // The machine-readable in-memory state remains available when persistence fails.
      }
    }
    publishRunnerState(failed);
    return;
  }
  runnerState.value = {
    status: 'failed',
    fatalCode: code,
    fatalError: error,
    acceptedRuns: 0,
    completedSlots: 0,
  };
  publishRunnerState(null);
};

const probeSessionStorage = (): boolean => {
  try {
    const probeValue = `${Date.now()}-${Math.random()}`;
    window.sessionStorage.setItem(PERF_STORAGE_PROBE_KEY, probeValue);
    const available = window.sessionStorage.getItem(PERF_STORAGE_PROBE_KEY) === probeValue;
    window.sessionStorage.removeItem(PERF_STORAGE_PROBE_KEY);
    return available;
  } catch {
    return false;
  }
};

const removeOwnedStorage = (): boolean => {
  try {
    window.sessionStorage.removeItem(PERF_SESSION_STORAGE_KEY);
    window.sessionStorage.removeItem(PERF_PENDING_STORAGE_KEY);
    return true;
  } catch {
    return false;
  }
};

const persistSession = (current: PerfTopLevelSuiteSession): boolean => {
  try {
    window.sessionStorage.setItem(PERF_SESSION_STORAGE_KEY, serializeSuiteSession(current));
    return true;
  } catch {
    setRunnerFailure(
      'SESSION_STORAGE_UNAVAILABLE',
      'The performance suite could not persist its top-level navigation state.',
      false,
    );
    return false;
  }
};

const readStoredSession = (): PerfTopLevelSuiteSession | null => {
  try {
    return parseSuiteSession(window.sessionStorage.getItem(PERF_SESSION_STORAGE_KEY));
  } catch {
    return null;
  }
};

const removePending = (): boolean => {
  try {
    window.sessionStorage.removeItem(PERF_PENDING_STORAGE_KEY);
    return true;
  } catch {
    return false;
  }
};

const buildScenarioURL = (
  currentSession: PerfTopLevelSuiteSession,
  execution: PerfScenarioExecution,
): string => {
  const scenarioURL = new URL(window.location.href);
  scenarioURL.search = '';
  scenarioURL.hash = '';
  scenarioURL.searchParams.set('scenario', '1');
  scenarioURL.searchParams.set('suiteId', currentSession.suiteId);
  scenarioURL.searchParams.set('runId', execution.runId);
  scenarioURL.searchParams.set('viewport', execution.plan.viewport);
  scenarioURL.searchParams.set('width', String(execution.plan.width));
  scenarioURL.searchParams.set('height', String(execution.plan.height));
  scenarioURL.searchParams.set('count', String(execution.plan.count));
  scenarioURL.searchParams.set('trackView', String(execution.plan.trackView));
  scenarioURL.searchParams.set('fixture', execution.plan.fixture);
  scenarioURL.searchParams.set('runType', execution.plan.runType);
  if (typeof execution.plan.appendFrom === 'number') {
    scenarioURL.searchParams.set('appendFrom', String(execution.plan.appendFrom));
  }
  return scenarioURL.toString();
};

const completeCurrentSuite = (current: PerfTopLevelSuiteSession): void => {
  const summary = aggregatePerfRuns(current.rawRuns);
  const decision = classifyPerfBaseline(current.rawRuns, summary);
  const result = buildPerfSuiteResult(current, summary, decision);
  const completed = completeSuite(current);
  if (!persistSession(completed)) return;
  session.value = completed;
  suiteResult.value = result;
  window.__EXCHANGE_PERF_RESULT__ = result;
  publishRunnerState(completed);
};

const continueSuite = (): void => {
  if (transitionInProgress || !session.value) return;
  if (session.value.status === 'failed' || session.value.status === 'completed') return;
  transitionInProgress = true;
  try {
    const current = session.value;
    const runnable = current.status === 'waiting-for-viewport'
      ? { ...current, status: 'running' as const }
      : current;
    const next = getNextExecution(runnable, plans);
    if (!next) {
      completeCurrentSuite(runnable);
      return;
    }

    const requirement = perfViewportRequirement(next.plan.viewport);
    if (!isViewportWithinTolerance(
      window.innerWidth,
      window.innerHeight,
      requirement.width,
      requirement.height,
      requirement.tolerance,
    )) {
      const waiting = markViewportWaiting(current, requirement);
      if (!persistSession(waiting)) return;
      session.value = waiting;
      publishRunnerState(waiting, requirement);
      return;
    }

    const prepared = prepareNextExecution(runnable, plans);
    if (!prepared.execution) {
      completeCurrentSuite(runnable);
      return;
    }
    if (!persistSession(prepared.session)) return;
    session.value = prepared.session;
    publishRunnerState(prepared.session);
    window.location.replace(buildScenarioURL(prepared.session, prepared.execution));
  } finally {
    transitionInProgress = false;
  }
};

const consumePendingAndContinue = (): void => {
  const current = session.value;
  if (!current) return;
  let pendingRaw: string | null;
  try {
    pendingRaw = window.sessionStorage.getItem(PERF_PENDING_STORAGE_KEY);
  } catch {
    setRunnerFailure(
      'SESSION_STORAGE_UNAVAILABLE',
      'The performance suite could not read its top-level navigation state.',
      false,
    );
    return;
  }

  if (pendingRaw !== null) {
    const pending = parsePendingScenarioEnvelope(pendingRaw);
    if (!pending) {
      removePending();
      setRunnerFailure('INVALID_PENDING_STATE', 'The pending scenario envelope is invalid or corrupt.');
      return;
    }
    if (current.processedRunIds.includes(pending.runId)) {
      if (!removePending()) {
        setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The duplicate pending scenario could not be removed.');
        return;
      }
    } else {
      const applied: PerfPendingApplyResult = applyPendingExecution(current, pending, plans);
      if (applied.rejected) {
        removePending();
        setRunnerFailure('PENDING_CORRELATION_MISMATCH', applied.error || 'Pending scenario was rejected.');
        return;
      }
      session.value = applied.session;
      publishRunnerState(applied.session);
      if (!persistSession(applied.session)) return;
      if (!removePending()) {
        setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The pending scenario could not be removed after persistence.');
        return;
      }
    }
  }

  const afterPending = session.value;
  if (!afterPending) return;
  if (afterPending.status === 'failed') {
    publishRunnerState(afterPending);
    return;
  }
  if (afterPending.status === 'completed') {
    restoreCompletedResult(afterPending);
    return;
  }
  continueSuite();
};

const restoreCompletedResult = (current: PerfTopLevelSuiteSession): void => {
  const summary = aggregatePerfRuns(current.rawRuns);
  const decision = classifyPerfBaseline(current.rawRuns, summary);
  const result = buildPerfSuiteResult(current, summary, decision);
  suiteResult.value = result;
  window.__EXCHANGE_PERF_RESULT__ = result;
  publishRunnerState(current);
};

const startNewSuite = (): void => {
  if (running.value) return;
  suiteResult.value = null;
  copyStatus.value = '';
  window.__EXCHANGE_PERF_RESULT__ = undefined;
  if (!probeSessionStorage()) {
    setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'sessionStorage is unavailable for the top-level performance suite.', false);
    return;
  }
  if (!removeOwnedStorage()) {
    setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The performance suite could not reset its owned session state.', false);
    return;
  }
  const suiteId = typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const fresh = createSuiteSession({
    suiteId,
    harnessHead: getPerfGitHead(),
    environment: captureEnvironment(),
  });
  session.value = fresh;
  publishRunnerState(fresh);
  if (!persistSession(fresh)) return;
  continueSuite();
};

const resumeSuite = (): void => {
  if (!probeSessionStorage()) {
    setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'sessionStorage is unavailable for the top-level performance suite.', false);
    return;
  }
  const stored = readStoredSession();
  if (!stored || !isPerfTopLevelSuiteSession(stored)) {
    setRunnerFailure('NO_RESUMABLE_SESSION', 'No valid v2 performance suite session is available to resume.', false);
    return;
  }
  session.value = stored;
  suiteResult.value = null;
  window.__EXCHANGE_PERF_RESULT__ = undefined;
  publishRunnerState(stored);
  consumePendingAndContinue();
};

const runSuite = (): void => {
  startNewSuite();
};

const resetAndStart = (): void => {
  if (resizeTimer !== undefined) {
    window.clearTimeout(resizeTimer);
    resizeTimer = undefined;
  }
  session.value = null;
  suiteResult.value = null;
  copyStatus.value = '';
  window.__EXCHANGE_PERF_RESULT__ = undefined;
  if (!removeOwnedStorage()) {
    setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The performance suite could not remove its owned session state.', false);
    return;
  }
  runnerState.value = { status: 'running', acceptedRuns: 0, completedSlots: 0 };
  publishRunnerState(null);
  startNewSuite();
};

const copyText = async (label: string, value: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(value);
    copyStatus.value = `${label} copied`;
  } catch {
    copyStatus.value = 'Clipboard unavailable in this browser context';
  }
};

const copyFullJson = (): void => {
  if (suiteResult.value) void copyText('Full baseline JSON', JSON.stringify(suiteResult.value, null, 2));
};

const copyRawJson = (): void => {
  if (suiteResult.value) void copyText('Raw JSON', JSON.stringify(suiteResult.value.rawRuns, null, 2));
};

const copySummaryJson = (): void => {
  if (suiteResult.value) void copyText('Summary JSON', JSON.stringify(suiteResult.value.summary, null, 2));
};

const copyMarkdown = (): void => {
  if (suiteResult.value) void copyText('Markdown report', serializePerfMarkdown(suiteResult.value));
};

const handleResize = (): void => {
  if (session.value?.status !== 'waiting-for-viewport') return;
  if (resizeTimer !== undefined) window.clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    resizeTimer = undefined;
    if (!session.value || session.value.status !== 'waiting-for-viewport') return;
    continueSuite();
  }, 250);
};

onMounted(() => {
  window.addEventListener('resize', handleResize);
  publishRunnerState(null);
  const params = new URLSearchParams(window.location.search);
  if (params.get('autorun') !== '1') {
    const stored = readStoredSession();
    if (stored?.status === 'completed') {
      session.value = stored;
      restoreCompletedResult(stored);
    }
    return;
  }
  if (params.get('resume') === '1') {
    resumeSuite();
  } else {
    startNewSuite();
  }
});

onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize);
  if (resizeTimer !== undefined) window.clearTimeout(resizeTimer);
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

.perf-runner__notice {
  margin-top: 40px;
  padding: var(--space-6);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-surface-subtle);
}

.perf-runner__notice p:last-child {
  margin-bottom: 0;
  color: var(--color-text-secondary);
  line-height: 1.55;
}

.perf-runner__notice--error {
  border-color: color-mix(in srgb, var(--color-danger) 45%, var(--color-border));
}

.perf-runner__notice--error .perf-runner__eyebrow,
.perf-runner__notice--error h2 {
  color: var(--color-danger);
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
  grid-template-columns: repeat(4, minmax(0, 1fr));
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
