import { isAxiosError } from 'axios';
import apiClient from '../axios';
const storageKey = 'post_view_telemetry_queue_v1';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const feedQualificationThreshold = 0.5;
const feedQualificationDurationMS = 1000;
const retryDelaysMS = [1000, 2000, 5000, 10000, 30000, 60000];
let sharedClient = null;
const isValidUserID = (value) => typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
const isValidSource = (value) => value === 'post_detail' || value === 'feed';
const isValidQueuedEvent = (value) => {
    if (!value || typeof value !== 'object') {
        return false;
    }
    const event = value;
    return isValidUserID(event.owner_user_id)
        && typeof event.event_id === 'string'
        && event.event_id.trim().length > 0
        && typeof event.post_id === 'number'
        && Number.isSafeInteger(event.post_id)
        && event.post_id > 0
        && typeof event.occurred_at === 'string'
        && event.occurred_at.trim().length > 0
        && isValidSource(event.source);
};
const getErrorStatus = (error) => {
    if (isAxiosError(error)) {
        return error.response?.status ?? null;
    }
    const response = error?.response;
    return typeof response?.status === 'number' ? response.status : null;
};
const getRetryAfterMS = (error) => {
    const response = error?.response;
    const headers = response?.headers;
    const raw = typeof headers?.get === 'function'
        ? headers.get('Retry-After')
        : headers?.['retry-after'] ?? headers?.['Retry-After'];
    const seconds = typeof raw === 'number'
        ? raw
        : typeof raw === 'string' && /^\d+$/.test(raw.trim())
            ? Number(raw.trim())
            : Number.NaN;
    return Number.isFinite(seconds) && seconds > 0 ? seconds * 1000 : 0;
};
export function createPostViewEventID() {
    if (typeof globalThis.crypto?.randomUUID === 'function') {
        return globalThis.crypto.randomUUID();
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, character => {
        const random = Math.floor(Math.random() * 16);
        const value = character === 'x' ? random : (random & 0x3) | 0x8;
        return value.toString(16);
    });
}
export class PostViewTelemetryClient {
    getCurrentUserID;
    queue = [];
    inFlight = false;
    pendingFlush = false;
    retryTimer = null;
    retryAttempt = 0;
    stopped = true;
    feedObserver = null;
    feedObservations = new Map();
    handleOnline = () => {
        this.clearRetryTimer();
        void this.flush();
    };
    handleVisibilityChange = () => {
        if (document.visibilityState === 'hidden') {
            this.cancelFeedTimers();
            return;
        }
        this.resumeVisibleFeedObservations();
    };
    constructor(getCurrentUserID) {
        this.getCurrentUserID = getCurrentUserID;
        this.queue = this.loadQueue();
        this.persistQueue();
    }
    start() {
        if (!this.stopped) {
            return;
        }
        this.stopped = false;
        window.addEventListener('online', this.handleOnline);
        document.addEventListener('visibilitychange', this.handleVisibilityChange);
        this.startFeedObserver();
        if (this.hasCurrentUserEvents()) {
            void this.flush();
        }
    }
    stop() {
        if (this.stopped) {
            return;
        }
        window.removeEventListener('online', this.handleOnline);
        document.removeEventListener('visibilitychange', this.handleVisibilityChange);
        this.clearRetryTimer();
        this.pendingFlush = false;
        this.teardownFeedObserver();
        this.stopped = true;
    }
    enqueue(postID, eventID, source, occurredAt = new Date().toISOString()) {
        const ownerUserID = this.getCurrentUserID();
        const normalizedEventID = eventID.trim();
        if (!isValidUserID(ownerUserID)
            || !Number.isSafeInteger(postID)
            || postID <= 0
            || !normalizedEventID
            || !isValidSource(source)) {
            return false;
        }
        const existingEvent = this.queue.find(event => event.event_id === normalizedEventID);
        if (existingEvent) {
            const isSameLogicalEvent = existingEvent.owner_user_id === ownerUserID
                && existingEvent.post_id === postID
                && existingEvent.source === source;
            if (!isSameLogicalEvent) {
                return false;
            }
            void this.flush();
            return true;
        }
        this.queue.push({
            owner_user_id: ownerUserID,
            event_id: normalizedEventID,
            post_id: postID,
            occurred_at: occurredAt,
            source,
        });
        this.queue = this.queue.slice(-maxBufferedEvents);
        this.persistQueue();
        void this.flush();
        return true;
    }
    observeFeedCard(element, postID) {
        if (this.stopped || !this.feedObserver || !Number.isSafeInteger(postID) || postID <= 0) {
            return;
        }
        this.unobserveFeedCard(element);
        const observation = {
            postID,
            intersectionRatio: 0,
            timer: null,
            emitted: false,
        };
        this.feedObservations.set(element, observation);
        this.feedObserver.observe(element);
    }
    unobserveFeedCard(element) {
        this.feedObserver?.unobserve(element);
        const observation = this.feedObservations.get(element);
        if (observation) {
            this.cancelFeedTimer(observation);
            this.feedObservations.delete(element);
        }
    }
    async flush() {
        if (this.inFlight) {
            this.pendingFlush = true;
            return;
        }
        const currentUserID = this.getCurrentUserID();
        if (!isValidUserID(currentUserID)) {
            return;
        }
        const batch = this.queue
            .filter(event => event.owner_user_id === currentUserID)
            .slice(0, maxBatchEvents);
        if (batch.length === 0) {
            return;
        }
        this.inFlight = true;
        let terminal = false;
        try {
            const response = await apiClient.post('/post-view-events', {
                events: batch.map(({ event_id, post_id, occurred_at, source }) => ({
                    event_id,
                    post_id,
                    occurred_at,
                    source,
                })),
            });
            this.removeTerminalResults(batch, response.data, response.status);
            this.resetRetryState();
            terminal = true;
        }
        catch (error) {
            const status = getErrorStatus(error);
            if (status === 422) {
                const responseData = error?.response?.data;
                const resultIDs = this.resultIDs(responseData);
                if (resultIDs.size > 0) {
                    this.removeEventIDs(resultIDs);
                }
                else {
                    this.dropBatch(batch);
                }
                this.resetRetryState();
                terminal = true;
            }
            else if (status !== 408 && status !== 425 && status !== 429 && !(status !== null && status >= 500 && status <= 599)) {
                if (status !== null && status >= 400 && status <= 499) {
                    this.dropBatch(batch);
                    this.resetRetryState();
                    terminal = true;
                }
                else {
                    this.scheduleRetry();
                }
            }
            else {
                this.scheduleRetry(getRetryAfterMS(error));
            }
        }
        finally {
            this.inFlight = false;
            if (this.pendingFlush) {
                this.pendingFlush = false;
                if (!this.stopped && terminal) {
                    void this.flush();
                }
            }
            else if (!this.stopped && terminal && this.hasCurrentUserEvents()) {
                void this.flush();
            }
        }
    }
    startFeedObserver() {
        if (typeof IntersectionObserver === 'undefined') {
            return;
        }
        this.feedObserver = new IntersectionObserver(entries => entries.forEach(entry => this.handleFeedIntersection(entry)), { threshold: [0, feedQualificationThreshold, 1] });
    }
    handleFeedIntersection(entry) {
        const observation = this.feedObservations.get(entry.target);
        if (!observation) {
            return;
        }
        observation.intersectionRatio = entry.intersectionRatio;
        if (document.visibilityState !== 'visible' || observation.emitted) {
            this.cancelFeedTimer(observation);
            return;
        }
        if (observation.intersectionRatio >= feedQualificationThreshold) {
            this.startFeedTimer(entry.target, observation);
        }
        else {
            this.cancelFeedTimer(observation);
        }
    }
    startFeedTimer(element, observation) {
        if (observation.timer !== null || observation.emitted) {
            return;
        }
        observation.timer = window.setTimeout(() => {
            observation.timer = null;
            if (this.stopped
                || document.visibilityState !== 'visible'
                || observation.emitted
                || observation.intersectionRatio < feedQualificationThreshold
                || this.feedObservations.get(element) !== observation) {
                return;
            }
            const accepted = this.enqueue(observation.postID, createPostViewEventID(), 'feed');
            if (accepted) {
                observation.emitted = true;
            }
        }, feedQualificationDurationMS);
    }
    cancelFeedTimer(observation) {
        if (observation.timer !== null) {
            window.clearTimeout(observation.timer);
            observation.timer = null;
        }
    }
    cancelFeedTimers() {
        for (const observation of this.feedObservations.values()) {
            this.cancelFeedTimer(observation);
        }
    }
    resumeVisibleFeedObservations() {
        if (document.visibilityState !== 'visible') {
            return;
        }
        for (const [element, observation] of this.feedObservations) {
            if (!observation.emitted && observation.intersectionRatio >= feedQualificationThreshold) {
                this.startFeedTimer(element, observation);
            }
        }
    }
    teardownFeedObserver() {
        this.feedObserver?.disconnect();
        this.feedObserver = null;
        for (const observation of this.feedObservations.values()) {
            this.cancelFeedTimer(observation);
        }
        this.feedObservations.clear();
    }
    removeTerminalResults(batch, response, status) {
        const resultIDs = this.resultIDs(response);
        if (resultIDs.size > 0) {
            this.removeEventIDs(resultIDs);
            return;
        }
        if (status >= 200 && status < 300) {
            this.dropBatch(batch);
        }
    }
    resultIDs(response) {
        const resultIDs = new Set();
        if (!Array.isArray(response?.results)) {
            return resultIDs;
        }
        for (const result of response.results) {
            if (typeof result?.event_id === 'string' && result.event_id.trim()) {
                resultIDs.add(result.event_id);
            }
        }
        return resultIDs;
    }
    removeEventIDs(eventIDs) {
        this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
        this.persistQueue();
    }
    dropBatch(batch) {
        const eventIDs = new Set(batch.map(event => event.event_id));
        this.removeEventIDs(eventIDs);
    }
    scheduleRetry(minimumDelayMS = 0) {
        if (this.stopped || this.retryTimer !== null) {
            return;
        }
        const delay = Math.max(retryDelaysMS[Math.min(this.retryAttempt, retryDelaysMS.length - 1)], minimumDelayMS);
        this.retryAttempt = Math.min(this.retryAttempt + 1, retryDelaysMS.length - 1);
        this.retryTimer = window.setTimeout(() => {
            this.retryTimer = null;
            void this.flush();
        }, delay);
    }
    resetRetryState() {
        this.clearRetryTimer();
        this.retryAttempt = 0;
    }
    clearRetryTimer() {
        if (this.retryTimer !== null) {
            window.clearTimeout(this.retryTimer);
            this.retryTimer = null;
        }
    }
    hasCurrentUserEvents() {
        const currentUserID = this.getCurrentUserID();
        return isValidUserID(currentUserID)
            && this.queue.some(event => event.owner_user_id === currentUserID);
    }
    loadQueue() {
        try {
            const raw = sessionStorage.getItem(storageKey);
            if (!raw) {
                return [];
            }
            const parsed = JSON.parse(raw);
            if (!Array.isArray(parsed)) {
                return [];
            }
            return parsed.filter(isValidQueuedEvent).slice(-maxBufferedEvents);
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
            // Storage can be unavailable in privacy-restricted browser contexts.
        }
    }
}
export function initializePostViewTelemetry(getCurrentUserID) {
    if (!sharedClient) {
        sharedClient = new PostViewTelemetryClient(getCurrentUserID);
        sharedClient.start();
    }
    return sharedClient;
}
export function getPostViewTelemetry() {
    return sharedClient ?? initializePostViewTelemetry(() => null);
}
export function resetPostViewTelemetryForTests() {
    sharedClient?.stop();
    sharedClient = null;
}
