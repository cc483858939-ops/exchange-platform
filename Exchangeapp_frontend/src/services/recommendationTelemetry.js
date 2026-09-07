import { isAxiosError } from 'axios';
import axiosClient from '../axios';
const storageKey = 'recommendation_telemetry_queue_v2';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const flushThreshold = 10;
const flushDelayMs = 1000;
const impressionDelayMs = 1000;
const maxRetryDelayMs = 30_000;
const maxFeedVisibleTimeMS = 6 * 60 * 60 * 1000;
let sharedClient = null;
const clamp = (value, minimum, maximum) => Math.min(maximum, Math.max(minimum, value));
export function calculateViewportVisibility(element) {
    const rect = element.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const cardWidth = Number.isFinite(rect.width) && rect.width > 0
        ? rect.width
        : rect.right - rect.left;
    const cardHeight = Number.isFinite(rect.height) && rect.height > 0
        ? rect.height
        : rect.bottom - rect.top;
    if (!Number.isFinite(viewportWidth)
        || !Number.isFinite(viewportHeight)
        || viewportWidth <= 0
        || viewportHeight <= 0
        || !Number.isFinite(rect.left)
        || !Number.isFinite(rect.right)
        || !Number.isFinite(rect.top)
        || !Number.isFinite(rect.bottom)
        || !Number.isFinite(cardWidth)
        || !Number.isFinite(cardHeight)
        || cardWidth <= 0
        || cardHeight <= 0) {
        return 0;
    }
    const visibleWidth = Math.max(0, Math.min(rect.right, viewportWidth) - Math.max(rect.left, 0));
    const visibleHeight = Math.max(0, Math.min(rect.bottom, viewportHeight) - Math.max(rect.top, 0));
    const referenceWidth = Math.min(cardWidth, viewportWidth);
    const referenceHeight = Math.min(cardHeight, viewportHeight);
    const denominator = referenceWidth * referenceHeight;
    if (!Number.isFinite(denominator) || denominator <= 0) {
        return 0;
    }
    return clamp((visibleWidth * visibleHeight) / denominator, 0, 1);
}
export const getRecommendationTelemetry = (getAccessToken) => {
    if (!sharedClient) {
        sharedClient = new RecommendationTelemetryClient(getAccessToken);
        sharedClient.start();
    }
    return sharedClient;
};
export class RecommendationTelemetryClient {
    getAccessToken;
    queue = [];
    observer = null;
    observed = new WeakMap();
    feedDwellByElement = new WeakMap();
    feedDwellStates = new Map();
    activeFeedDwellKey = null;
    qualifyingElements = new Set();
    visibilityTimers = new Map();
    seenImpressions = new Set();
    seenClicks = new Set();
    seenReadEnds = new Set();
    seenNotInterested = new Set();
    seenFeedDwells = new Set();
    flushTimer = null;
    retryTimer = null;
    retryDelayMs = 1000;
    inFlight = false;
    keepaliveInFlight = false;
    pendingFlush = false;
    reconciliationFrame = null;
    reconciliationFrameUsesRAF = false;
    stopped = true;
    constructor(getAccessToken) {
        this.getAccessToken = getAccessToken;
        this.queue = this.loadQueue();
    }
    start() {
        if (!this.stopped) {
            return;
        }
        this.stopped = false;
        document.addEventListener('visibilitychange', this.handleVisibilityChange);
        window.addEventListener('pagehide', this.handlePageHide);
        window.addEventListener('scroll', this.handleViewportChange, { passive: true });
        window.addEventListener('resize', this.handleViewportChange);
        if (this.queue.length > 0) {
            this.scheduleFlush(0);
        }
    }
    stop() {
        if (this.stopped) {
            return;
        }
        this.resetObservedCards(true);
        void this.flush(true);
        document.removeEventListener('visibilitychange', this.handleVisibilityChange);
        window.removeEventListener('pagehide', this.handlePageHide);
        window.removeEventListener('scroll', this.handleViewportChange);
        window.removeEventListener('resize', this.handleViewportChange);
        this.clearScheduledFlush();
        this.clearRetry();
        this.cancelReconciliation();
        this.stopped = true;
    }
    resetObservedCards(finalize = true) {
        if (finalize) {
            this.finalizeAllFeedDwells();
        }
        this.observer?.disconnect();
        this.qualifyingElements.clear();
        this.visibilityTimers.forEach(timer => window.clearTimeout(timer));
        this.visibilityTimers.clear();
        this.cancelReconciliation();
        this.observed = new WeakMap();
        this.feedDwellByElement = new WeakMap();
        this.feedDwellStates.clear();
        this.activeFeedDwellKey = null;
    }
    clearSession() {
        this.resetObservedCards(false);
        this.queue = [];
        this.seenImpressions.clear();
        this.seenClicks.clear();
        this.seenReadEnds.clear();
        this.seenNotInterested.clear();
        this.seenFeedDwells.clear();
        this.persistQueue();
        this.clearScheduledFlush();
        this.clearRetry();
    }
    observeCard(element, postID, tracking) {
        if (!tracking?.token) {
            return;
        }
        this.ensureObserver();
        this.observed.set(element, { postID, tracking });
        this.observer?.observe(element);
    }
    observeFeedCard(element, postID, tracking) {
        if (!tracking?.token) {
            return;
        }
        this.ensureObserver();
        const key = this.businessKey(postID, tracking);
        let state = this.feedDwellStates.get(key);
        if (!state) {
            state = {
                key,
                postID,
                tracking,
                element,
                accumulatedVisibleMS: 0,
                activeStartedAt: null,
                intersecting: false,
                finalized: this.seenFeedDwells.has(key),
            };
            this.feedDwellStates.set(key, state);
        }
        else {
            if (state.element && state.element !== element) {
                if (this.activeFeedDwellKey === key) {
                    this.settleFeedDwellState(state);
                    this.activeFeedDwellKey = null;
                }
                const oldElement = state.element;
                this.forgetObservedElement(oldElement);
                state.intersecting = false;
            }
            state.postID = postID;
            state.tracking = tracking;
            state.element = element;
            state.finalized = state.finalized || this.seenFeedDwells.has(key);
        }
        this.observed.set(element, { postID, tracking });
        this.feedDwellByElement.set(element, key);
        this.observer?.observe(element);
        this.scheduleFeedDwellReconciliation();
    }
    detachFeedCard(postID, tracking) {
        if (!tracking?.token) {
            return;
        }
        const key = this.businessKey(postID, tracking);
        const state = this.feedDwellStates.get(key);
        if (!state) {
            return;
        }
        this.detachFeedDwellState(state);
        this.scheduleFeedDwellReconciliation();
    }
    unobserveFeedCard(postID, tracking) {
        if (!tracking?.token) {
            return;
        }
        this.finalizeFeedDwell(postID, tracking);
        this.detachFeedCard(postID, tracking);
    }
    finalizeFeedDwell(postID, tracking) {
        if (!tracking?.token) {
            return false;
        }
        const key = this.businessKey(postID, tracking);
        const state = this.feedDwellStates.get(key);
        if (!state || state.finalized) {
            return false;
        }
        if (this.activeFeedDwellKey === key) {
            this.settleFeedDwellState(state);
            this.activeFeedDwellKey = null;
        }
        state.finalized = true;
        this.scheduleFeedDwellReconciliation();
        const accumulated = Number.isFinite(state.accumulatedVisibleMS)
            ? Math.max(0, state.accumulatedVisibleMS)
            : 0;
        const duration = Math.min(maxFeedVisibleTimeMS, Math.round(accumulated));
        if (duration <= 0 || this.seenFeedDwells.has(key)) {
            return false;
        }
        this.enqueue('feed_dwell', state.tracking, { feed_visible_time_ms: duration });
        this.seenFeedDwells.add(key);
        return true;
    }
    recordClick(postID, tracking) {
        if (!tracking?.token) {
            return;
        }
        this.finalizeFeedDwell(postID, tracking);
        const key = this.businessKey(postID, tracking);
        if (this.seenClicks.has(key)) {
            return;
        }
        this.seenClicks.add(key);
        this.enqueue('click', tracking);
        void this.flush(false);
    }
    recordReadEnd(postID, tracking, payload) {
        if (!tracking?.token)
            return false;
        const key = this.businessKey(postID, tracking);
        if (this.seenReadEnds.has(key))
            return false;
        this.seenReadEnds.add(key);
        this.enqueue('read_end', tracking, payload);
        return true;
    }
    recordNotInterested(postID, tracking) {
        if (!tracking?.token)
            return;
        this.finalizeFeedDwell(postID, tracking);
        const key = this.businessKey(postID, tracking);
        if (this.seenNotInterested.has(key))
            return;
        this.seenNotInterested.add(key);
        this.enqueue('not_interested', tracking);
        void this.flush(false);
    }
    async flush(keepalive = false) {
        if (keepalive) {
            await this.flushKeepalive();
            return;
        }
        this.clearScheduledFlush();
        if (this.inFlight) {
            this.pendingFlush = true;
            return;
        }
        if (this.queue.length === 0) {
            return;
        }
        const accessToken = this.getAccessToken();
        if (!accessToken) {
            return;
        }
        const batch = this.queue.slice(0, maxBatchEvents);
        this.inFlight = true;
        try {
            const response = await axiosClient.post('/recommendation-events', { events: batch });
            this.applyBatchResponse(response.data);
            this.retryDelayMs = 1000;
            this.clearRetry();
        }
        catch (error) {
            if (isAxiosError(error) && error.response?.data?.results) {
                this.applyBatchResponse(error.response.data);
            }
            else if (isAxiosError(error) && [400, 404, 413].includes(error.response?.status ?? 0)) {
                this.dropBatch(batch);
            }
            else {
                this.scheduleRetry();
            }
        }
        finally {
            this.inFlight = false;
            if (this.pendingFlush || this.queue.length >= flushThreshold) {
                this.pendingFlush = false;
                this.scheduleFlush(0);
            }
            else if (this.queue.length > 0 && this.retryTimer === null) {
                this.scheduleFlush(flushDelayMs);
            }
        }
    }
    async flushKeepalive() {
        if (this.keepaliveInFlight || this.queue.length === 0) {
            return;
        }
        const accessToken = this.getAccessToken();
        if (!accessToken) {
            return;
        }
        this.clearScheduledFlush();
        const batch = this.queue.slice(0, maxBatchEvents);
        this.keepaliveInFlight = true;
        try {
            const response = await this.sendKeepalive(batch, accessToken);
            this.applyBatchResponse(response.data);
            this.retryDelayMs = 1000;
            this.clearRetry();
        }
        catch (error) {
            if (isAxiosError(error) && error.response?.data?.results) {
                this.applyBatchResponse(error.response.data);
            }
            else if (isAxiosError(error) && [400, 404, 413].includes(error.response?.status ?? 0)) {
                this.dropBatch(batch);
            }
            else {
                this.scheduleRetry();
            }
        }
        finally {
            this.keepaliveInFlight = false;
            if (this.queue.length > 0 && this.retryTimer === null) {
                this.scheduleFlush(flushDelayMs);
            }
        }
    }
    ensureObserver() {
        if (this.observer || typeof IntersectionObserver === 'undefined') {
            return;
        }
        this.observer = new IntersectionObserver(this.handleIntersections, { threshold: [0, 0.5, 1] });
    }
    handleIntersections = (entries) => {
        entries.forEach(entry => {
            const observed = this.observed.get(entry.target);
            if (!observed) {
                return;
            }
            if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
                this.qualifyingElements.add(entry.target);
                this.startVisibilityTimer(entry.target);
            }
            else {
                this.qualifyingElements.delete(entry.target);
                this.clearVisibilityTimer(entry.target);
            }
            const key = this.feedDwellByElement.get(entry.target);
            if (!key) {
                return;
            }
            const state = this.feedDwellStates.get(key);
            if (state?.element === entry.target) {
                state.intersecting = entry.isIntersecting;
            }
        });
        this.reconcileActiveFeedDwell();
    };
    startVisibilityTimer(element) {
        if (document.visibilityState !== 'visible' || this.visibilityTimers.has(element)) {
            return;
        }
        const item = this.observed.get(element);
        if (!item || this.seenImpressions.has(this.businessKey(item.postID, item.tracking))) {
            return;
        }
        const timer = window.setTimeout(() => {
            this.visibilityTimers.delete(element);
            if (document.visibilityState !== 'visible' || !this.qualifyingElements.has(element)) {
                return;
            }
            const current = this.observed.get(element);
            if (!current) {
                return;
            }
            const key = this.businessKey(current.postID, current.tracking);
            if (this.seenImpressions.has(key)) {
                return;
            }
            this.seenImpressions.add(key);
            this.enqueue('impression', current.tracking);
        }, impressionDelayMs);
        this.visibilityTimers.set(element, timer);
    }
    clearVisibilityTimer(element) {
        const timer = this.visibilityTimers.get(element);
        if (timer !== undefined) {
            window.clearTimeout(timer);
            this.visibilityTimers.delete(element);
        }
    }
    handleVisibilityChange = () => {
        if (document.visibilityState === 'hidden') {
            this.visibilityTimers.forEach(timer => window.clearTimeout(timer));
            this.visibilityTimers.clear();
            this.reconcileActiveFeedDwell();
            void this.flush(true);
            return;
        }
        this.qualifyingElements.forEach(element => this.startVisibilityTimer(element));
        this.reconcileActiveFeedDwell();
    };
    handleViewportChange = () => {
        this.scheduleFeedDwellReconciliation();
    };
    handlePageHide = () => {
        this.finalizeAllFeedDwells();
        // The detail page registers after this shared client. Defer the global
        // flush until all synchronous pagehide producers have enqueued their
        // final read_end events.
        void Promise.resolve().then(() => this.flush(true));
    };
    finalizeAllFeedDwells() {
        Array.from(this.feedDwellStates.values()).forEach(state => {
            this.finalizeFeedDwell(state.postID, state.tracking);
        });
    }
    settleFeedDwellState(state, now = performance.now()) {
        if (state.activeStartedAt === null) {
            return;
        }
        state.accumulatedVisibleMS += Math.max(0, now - state.activeStartedAt);
        state.activeStartedAt = null;
    }
    detachFeedDwellState(state) {
        if (this.activeFeedDwellKey === state.key) {
            this.settleFeedDwellState(state);
            this.activeFeedDwellKey = null;
        }
        if (state.element) {
            const element = state.element;
            this.forgetObservedElement(element);
        }
        state.element = null;
        state.intersecting = false;
    }
    forgetObservedElement(element) {
        this.observer?.unobserve(element);
        this.clearVisibilityTimer(element);
        this.qualifyingElements.delete(element);
        this.observed.delete(element);
        this.feedDwellByElement.delete(element);
    }
    reconcileActiveFeedDwell() {
        const candidates = document.visibilityState === 'visible'
            ? Array.from(this.feedDwellStates.values())
                .filter(state => !state.finalized
                && state.element !== null
                && state.intersecting)
                .map(state => ({
                state,
                visibility: calculateViewportVisibility(state.element),
                distance: Math.abs((state.element.getBoundingClientRect().top
                    + state.element.getBoundingClientRect().bottom) / 2
                    - window.innerHeight / 2),
            }))
                .filter(candidate => candidate.visibility >= 0.5)
                .sort((left, right) => {
                if (left.visibility !== right.visibility) {
                    return right.visibility - left.visibility;
                }
                if (left.distance !== right.distance) {
                    return left.distance - right.distance;
                }
                return left.state.key < right.state.key ? -1 : left.state.key > right.state.key ? 1 : 0;
            })
            : [];
        const nextKey = candidates[0]?.state.key ?? null;
        if (this.activeFeedDwellKey === nextKey) {
            if (nextKey !== null) {
                const state = this.feedDwellStates.get(nextKey);
                if (state && state.activeStartedAt === null) {
                    state.activeStartedAt = performance.now();
                }
            }
            return;
        }
        if (this.activeFeedDwellKey !== null) {
            const previous = this.feedDwellStates.get(this.activeFeedDwellKey);
            if (previous) {
                this.settleFeedDwellState(previous);
            }
        }
        this.activeFeedDwellKey = nextKey;
        if (nextKey !== null) {
            const next = this.feedDwellStates.get(nextKey);
            if (next) {
                next.activeStartedAt = performance.now();
            }
        }
    }
    scheduleFeedDwellReconciliation() {
        if (this.reconciliationFrame !== null || this.stopped) {
            return;
        }
        const callback = () => {
            this.reconciliationFrame = null;
            this.reconciliationFrameUsesRAF = false;
            this.reconcileActiveFeedDwell();
        };
        if (typeof window.requestAnimationFrame === 'function') {
            this.reconciliationFrameUsesRAF = true;
            this.reconciliationFrame = window.requestAnimationFrame(callback);
            return;
        }
        this.reconciliationFrame = window.setTimeout(callback, 0);
    }
    cancelReconciliation() {
        if (this.reconciliationFrame === null) {
            return;
        }
        if (this.reconciliationFrameUsesRAF && typeof window.cancelAnimationFrame === 'function') {
            window.cancelAnimationFrame(this.reconciliationFrame);
        }
        else {
            window.clearTimeout(this.reconciliationFrame);
        }
        this.reconciliationFrame = null;
        this.reconciliationFrameUsesRAF = false;
    }
    enqueue(eventType, tracking, payload) {
        const queued = {
            event_id: crypto.randomUUID(),
            event_type: eventType,
            tracking_token: tracking.token,
            occurred_at: new Date().toISOString(),
        };
        if (eventType === 'read_end' && payload) {
            Object.assign(queued, payload);
        }
        else if (eventType === 'feed_dwell' && payload) {
            Object.assign(queued, payload);
        }
        this.queue.push(queued);
        if (this.queue.length > maxBufferedEvents) {
            this.queue.splice(0, this.queue.length - maxBufferedEvents);
        }
        this.persistQueue();
        this.scheduleFlush(this.queue.length >= flushThreshold ? 0 : flushDelayMs);
    }
    async sendKeepalive(events, accessToken) {
        const response = await fetch('/api/recommendation-events', {
            method: 'POST',
            headers: {
                Authorization: accessToken,
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ events }),
            credentials: 'same-origin',
            keepalive: true,
        });
        const data = (await response.json());
        if (!response.ok && response.status !== 422) {
            throw new Error('recommendation telemetry request failed: ' + response.status);
        }
        return { data };
    }
    applyBatchResponse(response) {
        const terminalIDs = new Set(response.results.map(result => result.event_id));
        if (terminalIDs.size === 0) {
            return;
        }
        this.queue = this.queue.filter(event => !terminalIDs.has(event.event_id));
        this.persistQueue();
    }
    dropBatch(batch) {
        const eventIDs = new Set(batch.map(event => event.event_id));
        this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
        this.persistQueue();
    }
    scheduleFlush(delayMs) {
        if (this.stopped || this.flushTimer !== null) {
            return;
        }
        this.flushTimer = window.setTimeout(() => {
            this.flushTimer = null;
            void this.flush(false);
        }, delayMs);
    }
    scheduleRetry() {
        if (this.stopped || this.retryTimer !== null) {
            return;
        }
        const jitter = Math.floor(Math.random() * 250);
        this.retryTimer = window.setTimeout(() => {
            this.retryTimer = null;
            void this.flush(false);
        }, this.retryDelayMs + jitter);
        this.retryDelayMs = Math.min(this.retryDelayMs * 2, maxRetryDelayMs);
    }
    clearScheduledFlush() {
        if (this.flushTimer !== null) {
            window.clearTimeout(this.flushTimer);
            this.flushTimer = null;
        }
    }
    clearRetry() {
        if (this.retryTimer !== null) {
            window.clearTimeout(this.retryTimer);
            this.retryTimer = null;
        }
    }
    businessKey(postID, tracking) {
        return tracking.request_id + ':' + postID;
    }
    loadQueue() {
        try {
            const value = sessionStorage.getItem(storageKey);
            if (!value) {
                return [];
            }
            const parsed = JSON.parse(value);
            return Array.isArray(parsed) ? parsed.slice(-maxBufferedEvents) : [];
        }
        catch {
            return [];
        }
    }
    persistQueue() {
        try {
            sessionStorage.setItem(storageKey, JSON.stringify(this.queue));
        }
        catch {
            // Storage can be unavailable in privacy modes; server-side idempotency
            // still protects any events that are delivered.
        }
    }
}
