import { isAxiosError } from 'axios';
import axiosClient from '../axios';
const storageKey = 'recommendation_telemetry_queue_v1';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const flushThreshold = 10;
const flushDelayMs = 1000;
const impressionDelayMs = 1000;
const maxRetryDelayMs = 30_000;
export class RecommendationTelemetryClient {
    getAccessToken;
    queue = [];
    observer = null;
    observed = new WeakMap();
    qualifyingElements = new Set();
    visibilityTimers = new Map();
    seenImpressions = new Set();
    seenClicks = new Set();
    flushTimer = null;
    retryTimer = null;
    retryDelayMs = 1000;
    inFlight = false;
    pendingFlush = false;
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
        if (this.queue.length > 0) {
            this.scheduleFlush(0);
        }
    }
    stop() {
        if (this.stopped) {
            return;
        }
        void this.flush(true);
        this.stopped = true;
        document.removeEventListener('visibilitychange', this.handleVisibilityChange);
        window.removeEventListener('pagehide', this.handlePageHide);
        this.resetObservedCards();
        this.clearScheduledFlush();
        this.clearRetry();
    }
    resetObservedCards() {
        this.observer?.disconnect();
        this.qualifyingElements.clear();
        this.visibilityTimers.forEach(timer => window.clearTimeout(timer));
        this.visibilityTimers.clear();
        this.observed = new WeakMap();
    }
    clearSession() {
        this.resetObservedCards();
        this.queue = [];
        this.seenImpressions.clear();
        this.seenClicks.clear();
        this.persistQueue();
        this.clearScheduledFlush();
        this.clearRetry();
    }
    observeCard(element, articleID, tracking) {
        if (!tracking?.token) {
            return;
        }
        this.ensureObserver();
        this.observed.set(element, { articleID, tracking });
        this.observer?.observe(element);
    }
    recordClick(articleID, tracking) {
        if (!tracking?.token) {
            return;
        }
        const key = this.businessKey(articleID, tracking);
        if (this.seenClicks.has(key)) {
            return;
        }
        this.seenClicks.add(key);
        this.enqueue('click', tracking);
        void this.flush(false);
    }
    async flush(keepalive = false) {
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
            const response = keepalive
                ? await this.sendKeepalive(batch, accessToken)
                : await axiosClient.post('/recommendation-events', { events: batch });
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
    ensureObserver() {
        if (this.observer) {
            return;
        }
        this.observer = new IntersectionObserver(this.handleIntersections, { threshold: [0, 0.5, 1] });
    }
    handleIntersections = (entries) => {
        entries.forEach(entry => {
            if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
                this.qualifyingElements.add(entry.target);
                this.startVisibilityTimer(entry.target);
            }
            else {
                this.qualifyingElements.delete(entry.target);
                this.clearVisibilityTimer(entry.target);
            }
        });
    };
    startVisibilityTimer(element) {
        if (document.visibilityState !== 'visible' || this.visibilityTimers.has(element)) {
            return;
        }
        const item = this.observed.get(element);
        if (!item || this.seenImpressions.has(this.businessKey(item.articleID, item.tracking))) {
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
            const key = this.businessKey(current.articleID, current.tracking);
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
            void this.flush(true);
            return;
        }
        this.qualifyingElements.forEach(element => this.startVisibilityTimer(element));
    };
    handlePageHide = () => {
        void this.flush(true);
    };
    enqueue(eventType, tracking) {
        this.queue.push({
            event_id: crypto.randomUUID(),
            event_type: eventType,
            tracking_token: tracking.token,
            occurred_at: new Date().toISOString(),
        });
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
            throw new Error(`recommendation telemetry request failed: ${response.status}`);
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
    businessKey(articleID, tracking) {
        return `${tracking.request_id}:${articleID}`;
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
