/* __placeholder__ */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { aggregatePerfRuns, classifyPerfBaseline, serializePerfMarkdown, } from './metrics';
import { createPerfScenarioPlans, PERF_EXECUTIONS_PER_SCENARIO, scenarioPlanLabel, } from './runnerPlan';
import { applyPendingExecution, buildPerfSuiteResult, completeSuite, createSuiteSession, failSuite, getNextExecution, isPerfTopLevelSuiteSession, isViewportWithinTolerance, markViewportWaiting, parsePendingScenarioEnvelope, parseSuiteSession, PERF_PENDING_STORAGE_KEY, PERF_SESSION_STORAGE_KEY, PERF_STORAGE_PROBE_KEY, perfViewportRequirement, prepareNextExecution, serializeSuiteSession, } from './topLevelSuiteSession';
import { getPerfGitHead, } from './types';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const plans = createPerfScenarioPlans('mixed');
const totalExecutions = plans.length * PERF_EXECUTIONS_PER_SCENARIO;
const session = ref(null);
const suiteResult = ref(null);
const copyStatus = ref('');
const runnerState = ref({
    status: 'running',
    acceptedRuns: 0,
    completedSlots: 0,
});
let resizeTimer;
let transitionInProgress = false;
const running = computed(() => (session.value?.status === 'running' || session.value?.status === 'waiting-for-viewport'));
const completedExecutions = computed(() => (Math.min(totalExecutions, session.value?.completedSlots ?? 0)));
const progressPercent = computed(() => (totalExecutions > 0 ? Math.round((completedExecutions.value / totalExecutions) * 100) : 0));
const currentScenario = computed(() => {
    const current = session.value;
    if (!current)
        return 'Ready to run';
    if (current.status === 'waiting-for-viewport') {
        const plan = plans[current.planIndex];
        return plan ? `Waiting · ${scenarioPlanLabel(plan)}` : 'Waiting for viewport';
    }
    if (current.status === 'failed')
        return current.fatalCode || 'Benchmark stopped';
    if (current.status === 'completed')
        return 'Completed';
    const plan = plans[current.planIndex];
    if (!plan)
        return 'Finalizing';
    return `${scenarioPlanLabel(plan)} · ${current.phase}${current.phase === 'recorded' ? ` ${current.recordedIndex + 1}/3` : ''}`;
});
const formatNumber = (value) => (typeof value === 'number' && Number.isFinite(value) ? String(Math.round(value)) : 'n/a');
const formatMs = (value) => (typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)} ms` : 'n/a');
const formatPercent = (value) => (typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}%` : 'n/a');
const captureEnvironment = () => {
    const navigation = navigator;
    const perf = performance;
    const supportedEntryTypes = typeof PerformanceObserver === 'undefined'
        ? []
        : PerformanceObserver.supportedEntryTypes ?? [];
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
const publishRunnerState = (current, viewportRequirement) => {
    const effectiveViewportRequirement = current?.status === 'waiting-for-viewport'
        ? viewportRequirement
            ?? (plans[current.planIndex] ? perfViewportRequirement(plans[current.planIndex].viewport) : undefined)
        : viewportRequirement;
    const nextState = current
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
    }
    else {
        window.__EXCHANGE_PERF_VIEWPORT_REQUIREMENT__ = undefined;
    }
};
const setRunnerFailure = (code, error, persist = true) => {
    suiteResult.value = null;
    window.__EXCHANGE_PERF_RESULT__ = undefined;
    if (session.value) {
        const failed = failSuite(session.value, code, error);
        session.value = failed;
        if (persist) {
            try {
                window.sessionStorage.setItem(PERF_SESSION_STORAGE_KEY, serializeSuiteSession(failed));
            }
            catch {
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
const probeSessionStorage = () => {
    try {
        const probeValue = `${Date.now()}-${Math.random()}`;
        window.sessionStorage.setItem(PERF_STORAGE_PROBE_KEY, probeValue);
        const available = window.sessionStorage.getItem(PERF_STORAGE_PROBE_KEY) === probeValue;
        window.sessionStorage.removeItem(PERF_STORAGE_PROBE_KEY);
        return available;
    }
    catch {
        return false;
    }
};
const removeOwnedStorage = () => {
    try {
        window.sessionStorage.removeItem(PERF_SESSION_STORAGE_KEY);
        window.sessionStorage.removeItem(PERF_PENDING_STORAGE_KEY);
        return true;
    }
    catch {
        return false;
    }
};
const persistSession = (current) => {
    try {
        window.sessionStorage.setItem(PERF_SESSION_STORAGE_KEY, serializeSuiteSession(current));
        return true;
    }
    catch {
        setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The performance suite could not persist its top-level navigation state.', false);
        return false;
    }
};
const readStoredSession = () => {
    try {
        return parseSuiteSession(window.sessionStorage.getItem(PERF_SESSION_STORAGE_KEY));
    }
    catch {
        return null;
    }
};
const removePending = () => {
    try {
        window.sessionStorage.removeItem(PERF_PENDING_STORAGE_KEY);
        return true;
    }
    catch {
        return false;
    }
};
const buildScenarioURL = (currentSession, execution) => {
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
const completeCurrentSuite = (current) => {
    const summary = aggregatePerfRuns(current.rawRuns);
    const decision = classifyPerfBaseline(current.rawRuns, summary);
    const result = buildPerfSuiteResult(current, summary, decision);
    const completed = completeSuite(current);
    if (!persistSession(completed))
        return;
    session.value = completed;
    suiteResult.value = result;
    window.__EXCHANGE_PERF_RESULT__ = result;
    publishRunnerState(completed);
};
const continueSuite = () => {
    if (transitionInProgress || !session.value)
        return;
    if (session.value.status === 'failed' || session.value.status === 'completed')
        return;
    transitionInProgress = true;
    try {
        const current = session.value;
        const runnable = current.status === 'waiting-for-viewport'
            ? { ...current, status: 'running' }
            : current;
        const next = getNextExecution(runnable, plans);
        if (!next) {
            completeCurrentSuite(runnable);
            return;
        }
        const requirement = perfViewportRequirement(next.plan.viewport);
        if (!isViewportWithinTolerance(window.innerWidth, window.innerHeight, requirement.width, requirement.height, requirement.tolerance)) {
            const waiting = markViewportWaiting(current, requirement);
            if (!persistSession(waiting))
                return;
            session.value = waiting;
            publishRunnerState(waiting, requirement);
            return;
        }
        const prepared = prepareNextExecution(runnable, plans);
        if (!prepared.execution) {
            completeCurrentSuite(runnable);
            return;
        }
        if (!persistSession(prepared.session))
            return;
        session.value = prepared.session;
        publishRunnerState(prepared.session);
        window.location.replace(buildScenarioURL(prepared.session, prepared.execution));
    }
    finally {
        transitionInProgress = false;
    }
};
const consumePendingAndContinue = () => {
    const current = session.value;
    if (!current)
        return;
    let pendingRaw;
    try {
        pendingRaw = window.sessionStorage.getItem(PERF_PENDING_STORAGE_KEY);
    }
    catch {
        setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The performance suite could not read its top-level navigation state.', false);
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
        }
        else {
            const applied = applyPendingExecution(current, pending, plans);
            if (applied.rejected) {
                removePending();
                setRunnerFailure('PENDING_CORRELATION_MISMATCH', applied.error || 'Pending scenario was rejected.');
                return;
            }
            session.value = applied.session;
            publishRunnerState(applied.session);
            if (!persistSession(applied.session))
                return;
            if (!removePending()) {
                setRunnerFailure('SESSION_STORAGE_UNAVAILABLE', 'The pending scenario could not be removed after persistence.');
                return;
            }
        }
    }
    const afterPending = session.value;
    if (!afterPending)
        return;
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
const restoreCompletedResult = (current) => {
    const summary = aggregatePerfRuns(current.rawRuns);
    const decision = classifyPerfBaseline(current.rawRuns, summary);
    const result = buildPerfSuiteResult(current, summary, decision);
    suiteResult.value = result;
    window.__EXCHANGE_PERF_RESULT__ = result;
    publishRunnerState(current);
};
const startNewSuite = () => {
    if (running.value)
        return;
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
    if (!persistSession(fresh))
        return;
    continueSuite();
};
const resumeSuite = () => {
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
const runSuite = () => {
    startNewSuite();
};
const resetAndStart = () => {
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
const copyText = async (label, value) => {
    try {
        await navigator.clipboard.writeText(value);
        copyStatus.value = `${label} copied`;
    }
    catch {
        copyStatus.value = 'Clipboard unavailable in this browser context';
    }
};
const copyFullJson = () => {
    if (suiteResult.value)
        void copyText('Full baseline JSON', JSON.stringify(suiteResult.value, null, 2));
};
const copyRawJson = () => {
    if (suiteResult.value)
        void copyText('Raw JSON', JSON.stringify(suiteResult.value.rawRuns, null, 2));
};
const copySummaryJson = () => {
    if (suiteResult.value)
        void copyText('Summary JSON', JSON.stringify(suiteResult.value.summary, null, 2));
};
const copyMarkdown = () => {
    if (suiteResult.value)
        void copyText('Markdown report', serializePerfMarkdown(suiteResult.value));
};
const handleResize = () => {
    if (session.value?.status !== 'waiting-for-viewport')
        return;
    if (resizeTimer !== undefined)
        window.clearTimeout(resizeTimer);
    resizeTimer = window.setTimeout(() => {
        resizeTimer = undefined;
        if (!session.value || session.value.status !== 'waiting-for-viewport')
            return;
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
    }
    else {
        startNewSuite();
    }
});
onBeforeUnmount(() => {
    window.removeEventListener('resize', handleResize);
    if (resizeTimer !== undefined)
        window.clearTimeout(resizeTimer);
});
const __VLS_fnComponent = (await import('vue')).defineComponent({});
let __VLS_functionalComponentProps;
let __VLS_modelEmitsType;
function __VLS_template() {
    let __VLS_ctx;
    /* Components */
    let __VLS_otherComponents;
    let __VLS_own;
    let __VLS_localComponents;
    let __VLS_components;
    let __VLS_styleScopedClasses;
    // CSS variable injection 
    // CSS variable injection end 
    let __VLS_resolvedLocalAndGlobalComponents;
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("perf-runner") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("perf-runner__hero") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__eyebrow") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__lede") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__toolbar") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.runSuite) }, ...{ class: ("perf-runner__run") }, type: ("button"), disabled: ((__VLS_ctx.running)), });
    (__VLS_ctx.running ? 'Running matrix…' : __VLS_ctx.suiteResult ? 'Run again' : 'Run full matrix');
    // @ts-ignore
    [runSuite, running, running, suiteResult,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.resetAndStart) }, type: ("button"), disabled: ((__VLS_ctx.runnerState.status === 'running')), });
    // @ts-ignore
    [resetAndStart, runnerState,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("perf-runner__hint") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("perf-runner__progress") }, "aria-live": ("polite"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__progress-head") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.currentScenario);
    // @ts-ignore
    [currentScenario,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.completedExecutions);
    (__VLS_ctx.totalExecutions);
    // @ts-ignore
    [completedExecutions, totalExecutions,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__progress-track") }, role: ("progressbar"), "aria-valuenow": ((__VLS_ctx.progressPercent)), "aria-valuemin": ("0"), "aria-valuemax": ("100"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ style: (({ width: `${__VLS_ctx.progressPercent}%` })) }, });
    // @ts-ignore
    [progressPercent, progressPercent,];
    if (__VLS_ctx.running) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__progress-note") }, });
        // @ts-ignore
        [running,];
    }
    if (__VLS_ctx.runnerState.status === 'waiting-for-viewport') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("perf-runner__notice") }, "aria-live": ("polite"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__eyebrow") }, });
        // @ts-ignore
        [runnerState,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        (__VLS_ctx.runnerState.viewportRequirement?.viewport);
        // @ts-ignore
        [runnerState,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.runnerState.viewportRequirement?.width);
        (__VLS_ctx.runnerState.viewportRequirement?.height);
        // @ts-ignore
        [runnerState, runnerState,];
    }
    if (__VLS_ctx.runnerState.status === 'failed') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("perf-runner__notice perf-runner__notice--error") }, "aria-live": ("assertive"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__eyebrow") }, });
        // @ts-ignore
        [runnerState,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        (__VLS_ctx.runnerState.fatalCode || 'RUNNER_FAILED');
        // @ts-ignore
        [runnerState,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.runnerState.fatalError || 'The performance harness could not continue.');
        // @ts-ignore
        [runnerState,];
    }
    if (__VLS_ctx.suiteResult) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("perf-runner__results") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__decision") }, ...{ class: ((`perf-runner__decision--${__VLS_ctx.suiteResult.classification.toLowerCase().replaceAll(' ', '-')}`)) }, });
        __VLS_styleScopedClasses = (`perf-runner__decision--${suiteResult.classification.toLowerCase().replaceAll(' ', '-')}`);
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-runner__eyebrow") }, });
        // @ts-ignore
        [suiteResult, suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        (__VLS_ctx.suiteResult.classification);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.suiteResult.recommendation);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__facts") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.suiteResult.bottleneck);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.suiteResult.rawRuns.length);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.suiteResult.failures.length);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.suiteResult.rejectedTimingAttempts);
        // @ts-ignore
        [suiteResult,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__exports") }, "aria-label": ("Export benchmark results"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.copyFullJson) }, type: ("button"), });
        // @ts-ignore
        [copyFullJson,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.copyRawJson) }, type: ("button"), });
        // @ts-ignore
        [copyRawJson,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.copySummaryJson) }, type: ("button"), });
        // @ts-ignore
        [copySummaryJson,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.copyMarkdown) }, type: ("button"), });
        // @ts-ignore
        [copyMarkdown,];
        if (__VLS_ctx.copyStatus) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ role: ("status"), });
            (__VLS_ctx.copyStatus);
            // @ts-ignore
            [copyStatus, copyStatus,];
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("perf-runner__table-wrap") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.table, __VLS_intrinsicElements.table)({ ...{ class: ("perf-runner__table") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.caption, __VLS_intrinsicElements.caption)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.thead, __VLS_intrinsicElements.thead)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.tr, __VLS_intrinsicElements.tr)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.th, __VLS_intrinsicElements.th)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.tbody, __VLS_intrinsicElements.tbody)({});
        for (const [row] of __VLS_getVForSourceType((__VLS_ctx.suiteResult.summary.rows))) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.tr, __VLS_intrinsicElements.tr)({ key: ((row.key)), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (row.viewport);
            // @ts-ignore
            [suiteResult,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (row.count);
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (row.trackView ? 'tracked' : 'untracked');
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (row.runType);
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatMs(row.medianMountMs));
            // @ts-ignore
            [formatMs,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatMs(row.medianAppendMs));
            // @ts-ignore
            [formatMs,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatMs(row.medianP95FrameMs));
            // @ts-ignore
            [formatMs,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatPercent(row.worstPercentOver50Ms));
            // @ts-ignore
            [formatPercent,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatNumber(row.medianDomElements));
            // @ts-ignore
            [formatNumber,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.td, __VLS_intrinsicElements.td)({});
            (__VLS_ctx.formatNumber(row.medianPeakObservedTargets));
            // @ts-ignore
            [formatNumber,];
        }
        if (__VLS_ctx.suiteResult.failures.length > 0) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.details, __VLS_intrinsicElements.details)({ ...{ class: ("perf-runner__failures") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.summary, __VLS_intrinsicElements.summary)({});
            // @ts-ignore
            [suiteResult,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.ul, __VLS_intrinsicElements.ul)({});
            for (const [failure] of __VLS_getVForSourceType((__VLS_ctx.suiteResult.failures))) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.li, __VLS_intrinsicElements.li)({ key: ((`${failure.phase}-${failure.scenario}-${failure.error}`)), });
                (failure.phase);
                (failure.scenario);
                (failure.error);
                // @ts-ignore
                [suiteResult,];
            }
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['perf-runner'];
        __VLS_styleScopedClasses['perf-runner__hero'];
        __VLS_styleScopedClasses['perf-runner__eyebrow'];
        __VLS_styleScopedClasses['perf-runner__lede'];
        __VLS_styleScopedClasses['perf-runner__toolbar'];
        __VLS_styleScopedClasses['perf-runner__run'];
        __VLS_styleScopedClasses['perf-runner__hint'];
        __VLS_styleScopedClasses['perf-runner__progress'];
        __VLS_styleScopedClasses['perf-runner__progress-head'];
        __VLS_styleScopedClasses['perf-runner__progress-track'];
        __VLS_styleScopedClasses['perf-runner__progress-note'];
        __VLS_styleScopedClasses['perf-runner__notice'];
        __VLS_styleScopedClasses['perf-runner__eyebrow'];
        __VLS_styleScopedClasses['perf-runner__notice'];
        __VLS_styleScopedClasses['perf-runner__notice--error'];
        __VLS_styleScopedClasses['perf-runner__eyebrow'];
        __VLS_styleScopedClasses['perf-runner__results'];
        __VLS_styleScopedClasses['perf-runner__decision'];
        __VLS_styleScopedClasses['perf-runner__eyebrow'];
        __VLS_styleScopedClasses['perf-runner__facts'];
        __VLS_styleScopedClasses['perf-runner__exports'];
        __VLS_styleScopedClasses['perf-runner__table-wrap'];
        __VLS_styleScopedClasses['perf-runner__table'];
        __VLS_styleScopedClasses['perf-runner__failures'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                totalExecutions: totalExecutions,
                suiteResult: suiteResult,
                copyStatus: copyStatus,
                runnerState: runnerState,
                running: running,
                completedExecutions: completedExecutions,
                progressPercent: progressPercent,
                currentScenario: currentScenario,
                formatNumber: formatNumber,
                formatMs: formatMs,
                formatPercent: formatPercent,
                runSuite: runSuite,
                resetAndStart: resetAndStart,
                copyFullJson: copyFullJson,
                copyRawJson: copyRawJson,
                copySummaryJson: copySummaryJson,
                copyMarkdown: copyMarkdown,
            };
        },
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
;
