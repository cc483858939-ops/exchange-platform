/* __placeholder__ */
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useAuthStore } from '../store/auth';
import PostCard from '../components/feed/PostCard.vue';
import { resetPostViewTelemetryForTests } from '../services/postViewTelemetry';
import { createPerfPosts } from './fixtures';
import { attributeLongTasksByPhase, assessRafCadence, assessScrollTiming, assessTimingHealth, PERF_RAF_HEALTH_SAMPLES, summarizeFrameDeltas, } from './metrics';
import { installIntersectionObserverInstrumentation } from './observerInstrumentation';
import { PERF_PENDING_STORAGE_KEY, PERF_SESSION_SCHEMA_VERSION, isViewportWithinTolerance, perfViewportRequirement, } from './topLevelSuiteSession';
import { hasPerfOrchestrationIds } from './scenarioConfig';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const config = props.config;
const posts = ref([]);
const listRoot = ref(null);
const observerInstrumentation = installIntersectionObserverInstrumentation();
const authStore = useAuthStore();
let cleanedUp = false;
let completed = false;
const fatalError = ref(null);
// The perf preview has its own origin. Clearing that origin guarantees that this
// isolated Pinia instance cannot accidentally use a valid viewer from the SPA.
authStore.clearAuth();
const RAF_PROBE_TIMEOUT_MS = 750;
const waitForFrame = () => new Promise(resolve => {
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
const waitForTwoFrames = async () => {
    const firstFrame = await waitForFrame();
    if (firstFrame === null) {
        return;
    }
    await waitForFrame();
};
const readHeap = () => {
    const raw = window.performance.memory?.usedJSHeapSize;
    return typeof raw === 'number' && Number.isFinite(raw) ? raw : null;
};
const supportsLongTasks = () => {
    if (typeof window.PerformanceObserver === 'undefined') {
        return false;
    }
    const supportedEntryTypes = window.PerformanceObserver.supportedEntryTypes;
    return Array.isArray(supportedEntryTypes) && supportedEntryTypes.includes('longtask');
};
const collectLongTasks = () => {
    if (!supportsLongTasks()) {
        return {
            metrics: { supported: false },
            entries: () => [],
            disconnect: () => undefined,
        };
    }
    const durations = [];
    const entries = [];
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
    }
    catch {
        return {
            metrics: { supported: false },
            entries: () => [],
            disconnect: () => undefined,
        };
    }
};
const probeRafCadence = async (phase) => {
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
    const intervals = [];
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
const scrollAndMeasure = async () => {
    const documentHeight = Math.max(document.documentElement.scrollHeight, document.body.scrollHeight);
    const maxScroll = Math.max(0, documentHeight - window.innerHeight);
    const durationMs = 4_000;
    const deltas = [];
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
    resetPostViewTelemetryForTests();
    const snapshot = observerInstrumentation.snapshot();
    observerInstrumentation.restore();
    cleanedUp = true;
    return snapshot;
};
const telemetryNetworkRequestCount = () => (performance.getEntriesByType('resource')
    .filter(entry => entry.name.includes('post-view-events'))
    .length);
const executionContext = () => ({
    topLevel: window.parent === window,
    visibilityState: document.visibilityState,
    devicePixelRatio: window.devicePixelRatio || 1,
    userAgent: navigator.userAgent,
});
const rawScenario = () => ({
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
const executionValidationIssues = () => {
    const issues = [];
    const requirement = perfViewportRequirement(config.viewport);
    if (window.parent !== window) {
        issues.push('Performance scenario must execute as a top-level document.');
    }
    if (!isViewportWithinTolerance(window.innerWidth, window.innerHeight, requirement.width, requirement.height)) {
        issues.push(`viewport ${window.innerWidth}x${window.innerHeight} does not match `
            + `${config.viewport} target ${requirement.width}x${requirement.height} ±8px`);
    }
    return issues;
};
const persistPending = (pending) => {
    try {
        window.sessionStorage.setItem(PERF_PENDING_STORAGE_KEY, JSON.stringify(pending));
        window.location.replace('/?autorun=1&resume=1');
    }
    catch (error) {
        fatalError.value = `SESSION_STORAGE_UNAVAILABLE: ${error instanceof Error ? error.message : String(error)}`;
    }
};
const sendResult = (result) => {
    if (completed)
        return;
    completed = true;
    persistPending({
        schemaVersion: PERF_SESSION_SCHEMA_VERSION,
        suiteId: config.suiteId,
        runId: config.runId,
        type: 'result',
        result,
    });
};
const sendError = (error) => {
    if (completed)
        return;
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
    let longTaskCollector = null;
    const phaseWindows = {};
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
            const timingHealth = assessTimingHealth(preflight, postScroll, postCleanup, [], visibilityLost);
            const result = {
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
        const longTasks = longTaskCollector.metrics.supported
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
        const timingHealth = assessTimingHealth(preflight, postScroll, postCleanup, scrollTiming.issues, visibilityLost);
        const memorySupported = memoryBeforeMount !== null
            && memoryAfterMount !== null
            && memoryAfterScroll !== null;
        const finalPostCards = config.count + (append.measured ? 20 : 0);
        const issues = [];
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
            issues.push('post-view-events network traffic was observed');
        }
        const memory = memorySupported
            ? {
                supported: true,
                beforeMount: memoryBeforeMount ?? undefined,
                afterMount: memoryAfterMount ?? undefined,
                afterScroll: memoryAfterScroll ?? undefined,
            }
            : { supported: false };
        const result = {
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
    }
    catch (error) {
        longTaskCollector?.disconnect();
        sendError(error);
    }
    finally {
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("perf-scenario") }, });
    if (__VLS_ctx.fatalError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("perf-scenario__error") }, role: ("alert"), });
        (__VLS_ctx.fatalError);
        // @ts-ignore
        [fatalError, fatalError,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ref: ("listRoot"), ...{ class: ("perf-scenario__list") }, "aria-label": ("Performance benchmark post list"), });
    // @ts-ignore
    (__VLS_ctx.listRoot);
    for (const [post] of __VLS_getVForSourceType((__VLS_ctx.posts))) {
        // @ts-ignore
        [PostCard,];
        const __VLS_0 = __VLS_asFunctionalComponent(PostCard, new PostCard({ key: ((post.id)), post: ((post)), trackView: ((__VLS_ctx.config.trackView)), }));
        const __VLS_1 = __VLS_0({ key: ((post.id)), post: ((post)), trackView: ((__VLS_ctx.config.trackView)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
        ({}({ key: ((post.id)), post: ((post)), trackView: ((__VLS_ctx.config.trackView)), }));
        // @ts-ignore
        [listRoot, posts, config,];
        const __VLS_4 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_1);
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['perf-scenario'];
        __VLS_styleScopedClasses['perf-scenario__error'];
        __VLS_styleScopedClasses['perf-scenario__list'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                PostCard: PostCard,
                config: config,
                posts: posts,
                listRoot: listRoot,
                fatalError: fatalError,
            };
        },
        props: {},
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    props: {},
});
;
