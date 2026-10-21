import axiosClient from '../axios';
import { createTelemetryPersistence } from './telemetryPersistence';
import type { RecommendationTracking } from '../types/Recommendation';
import {
  TelemetryRetryGate,
  createTelemetryAuthFingerprint,
  type TelemetryRequestAuthContext,
  type TelemetrySnapshot,
  getTelemetryHTTPFailure,
  readTelemetryHeader,
} from './telemetryRetryGate';

export type RecommendationEventType = 'impression' | 'click' | 'read_end' | 'feed_dwell' | 'not_interested';

export type RecommendationReadEndPayload = {
  foreground_time_ms: number;
  scroll_progress_percent: number;
  exit_type: string;
};

export type RecommendationFeedDwellPayload = {
  feed_visible_time_ms: number;
};

type QueuedRecommendationEvent = {
  event_id: string;
  event_type: RecommendationEventType;
  tracking_token: string;
  occurred_at: string;
  foreground_time_ms?: number;
  scroll_progress_percent?: number;
  exit_type?: string;
  feed_visible_time_ms?: number;
};

type RecommendationEventResult = {
  event_id: string;
  status: 'accepted' | 'duplicate' | 'rejected';
  reason?: string;
};

type RecommendationEventBatchResponse = {
  accepted: number;
  duplicates: number;
  rejected: number;
  results: RecommendationEventResult[];
};
type RecommendationSnapshot = TelemetrySnapshot<QueuedRecommendationEvent>;
type RecommendationResponse = {
  status: number;
  data: RecommendationEventBatchResponse | undefined;
  headers?: unknown;
};

type ObservedRecommendation = {
  postID: number;
  tracking: RecommendationTracking;
};

type FeedDwellState = {
  key: string;
  postID: number;
  tracking: RecommendationTracking;
  element: Element | null;
  accumulatedVisibleMS: number;
  activeStartedAt: number | null;
  intersecting: boolean;
  finalized: boolean;
};

const storageKey = 'recommendation_telemetry_snapshot_v3';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const maxRequestBodyBytes = 60 * 1024;
const flushThreshold = 10;
const flushDelayMs = 1000;
const impressionDelayMs = 1000;
const maxFeedVisibleTimeMS = 6 * 60 * 60 * 1000;

let sharedClient: RecommendationTelemetryClient | null = null;

const clamp = (value: number, minimum: number, maximum: number) =>
  Math.min(maximum, Math.max(minimum, value));
const isValidUserID = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
const isValidRecommendationEventType = (value: unknown): value is RecommendationEventType =>
  value === 'impression'
  || value === 'click'
  || value === 'read_end'
  || value === 'feed_dwell'
  || value === 'not_interested';
const isValidQueuedRecommendationEvent = (value: unknown): value is QueuedRecommendationEvent => {
  if (!value || typeof value !== 'object') return false;
  const event = value as Partial<QueuedRecommendationEvent>;
  return typeof event.event_id === 'string'
    && event.event_id.trim().length > 0
    && isValidRecommendationEventType(event.event_type)
    && typeof event.tracking_token === 'string'
    && event.tracking_token.trim().length > 0
    && typeof event.occurred_at === 'string'
    && event.occurred_at.trim().length > 0;
};

export function calculateRectVisibility(rect: DOMRect): number {
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;
  const cardWidth = Number.isFinite(rect.width) && rect.width > 0
    ? rect.width
    : rect.right - rect.left;
  const cardHeight = Number.isFinite(rect.height) && rect.height > 0
    ? rect.height
    : rect.bottom - rect.top;

  if (
    !Number.isFinite(viewportWidth)
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
    || cardHeight <= 0
  ) {
    return 0;
  }

  const visibleWidth = Math.max(
    0,
    Math.min(rect.right, viewportWidth) - Math.max(rect.left, 0),
  );
  const visibleHeight = Math.max(
    0,
    Math.min(rect.bottom, viewportHeight) - Math.max(rect.top, 0),
  );
  const referenceWidth = Math.min(cardWidth, viewportWidth);
  const referenceHeight = Math.min(cardHeight, viewportHeight);
  const denominator = referenceWidth * referenceHeight;

  if (!Number.isFinite(denominator) || denominator <= 0) {
    return 0;
  }

  return clamp((visibleWidth * visibleHeight) / denominator, 0, 1);
}
export const getRecommendationTelemetry = (
  getAccessToken: () => string | null,
  getCurrentUserID: () => number | null,
  getAuthSessionVersion: () => number | null = () => null,
) => {
  if (!sharedClient) {
    sharedClient = new RecommendationTelemetryClient(getAccessToken, getCurrentUserID, getAuthSessionVersion);
    sharedClient.start();
  }
  return sharedClient;
};

export class RecommendationTelemetryClient {
  private queue: QueuedRecommendationEvent[] = [];
  private observer: IntersectionObserver | null = null;
  private observed = new WeakMap<Element, ObservedRecommendation>();
  private feedDwellByElement = new WeakMap<Element, string>();
  private feedDwellStates = new Map<string, FeedDwellState>();
  private intersectingFeedDwells = new Set<FeedDwellState>();
  private activeFeedDwellKey: string | null = null;
  private qualifyingElements = new Set<Element>();
  private visibilityTimers = new Map<Element, number>();
  private seenImpressions = new Set<string>();
  private seenClicks = new Set<string>();
  private seenReadEnds = new Set<string>();
  private seenNotInterested = new Set<string>();
  private seenFeedDwells = new Set<string>();
  private inFlight = false;
  private keepaliveInFlight = false;
  private reconciliationFrame: number | null = null;
  private reconciliationFrameUsesRAF = false;
  private stopped = true;
  private sessionGeneration = 0;
  private ownerUserId: number | null = null;
  private ownerInitialized = false;
  private ownerUnavailable = false;
  private authPausedFingerprint: string | null = null;
  private queuePersistence = createTelemetryPersistence(() => this.saveQueue());
  private retryGate: TelemetryRetryGate;

  constructor(
    private readonly getAccessToken: () => string | null,
    private readonly getCurrentUserID: () => number | null,
    private readonly getAuthSessionVersion: () => number | null = () => null,
  ) {
    this.retryGate = new TelemetryRetryGate(
      () => { if (!this.stopped) void this.flush(false); },
      () => this.persistQueue(),
    );
    const snapshot = this.loadSnapshot();
    this.queue = snapshot.events;
    this.retryGate.restore(snapshot.retry);
    if (isValidUserID(snapshot.ownerUserId)) {
      this.ownerUserId = snapshot.ownerUserId;
      this.ownerInitialized = true;
    }
    this.syncOwner();
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
    if (this.queue.length > 0) void this.flush(false);
  }

  stop() {
    if (this.stopped) {
      this.queuePersistence.flush();
      return;
    }
    this.resetObservedCards(true);
    this.queuePersistence.flush();
    void this.flush(true);
    document.removeEventListener('visibilitychange', this.handleVisibilityChange);
    window.removeEventListener('pagehide', this.handlePageHide);
    window.removeEventListener('scroll', this.handleViewportChange);
    window.removeEventListener('resize', this.handleViewportChange);
    this.retryGate.cancelScheduled();
    this.cancelReconciliation();
    this.stopped = true;
  }

  notifyViewportChange() {
    this.scheduleFeedDwellReconciliation();
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
    this.observed = new WeakMap<Element, ObservedRecommendation>();
    this.feedDwellByElement = new WeakMap<Element, string>();
    this.feedDwellStates.clear();
    this.intersectingFeedDwells.clear();
    this.activeFeedDwellKey = null;
  }

  clearSession() {
    this.sessionGeneration += 1;
    this.resetObservedCards(false);
    this.queue = [];
    this.seenImpressions.clear();
    this.seenClicks.clear();
    this.seenReadEnds.clear();
    this.seenNotInterested.clear();
    this.seenFeedDwells.clear();
    this.retryGate.reset();
    this.authPausedFingerprint = null;
    const currentOwnerID = this.getCurrentUserID();
    if (isValidUserID(currentOwnerID)) {
      this.ownerUserId = currentOwnerID;
      this.ownerInitialized = true;
      this.ownerUnavailable = false;
    }
    this.persistQueue();
    this.queuePersistence.flush();
  }

  refreshOwner() {
    const ownerID = this.syncOwner();
    if (isValidUserID(ownerID)) this.scheduleFlush(0);
  }

  observeCard(element: HTMLElement, postID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) {
      return;
    }
    this.ensureObserver();
    this.observed.set(element, { postID, tracking });
    this.observer?.observe(element);
  }

  observeFeedCard(element: HTMLElement, postID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) {
      return;
    }

    this.ensureObserver();
    const key = this.businessKey(postID, tracking);
    const previousKey = this.feedDwellByElement.get(element);
    if (previousKey !== undefined && previousKey !== key) {
      const previous = this.feedDwellStates.get(previousKey);
      if (previous) this.detachFeedDwellState(previous);
    }
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
    } else {
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

  detachFeedCard(postID: number, tracking?: RecommendationTracking) {
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

  unobserveFeedCard(postID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) {
      return;
    }
    this.finalizeFeedDwell(postID, tracking);
    this.detachFeedCard(postID, tracking);
  }

  finalizeFeedDwell(postID: number, tracking?: RecommendationTracking): boolean {
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
    this.intersectingFeedDwells.delete(state);
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

  recordClick(postID: number, tracking?: RecommendationTracking) {
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

  recordReadEnd(
    postID: number,
    tracking: RecommendationTracking | undefined,
    payload: RecommendationReadEndPayload,
  ) {
    if (!tracking?.token) return false;
    const key = this.businessKey(postID, tracking);
    if (this.seenReadEnds.has(key)) return false;
    this.seenReadEnds.add(key);
    this.enqueue('read_end', tracking, payload);
    return true;
  }

  recordNotInterested(postID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) return;
    this.finalizeFeedDwell(postID, tracking);
    const key = this.businessKey(postID, tracking);
    if (this.seenNotInterested.has(key)) return;
    this.seenNotInterested.add(key);
    this.enqueue('not_interested', tracking);
    void this.flush(false);
  }

  async flush(keepalive = false): Promise<void> {
    if (this.stopped) return;
    if (keepalive) {
      this.queuePersistence.flush();
      await this.flushKeepalive();
      return;
    }

    const context = this.getSendContext();
    if (!context || this.inFlight) return;
    const batch = this.selectRequestBatch(this.queue);
    if (batch.length === 0) return;
    this.retryGate.cancelScheduled();
    const capturedGeneration = context.authContext.sessionGeneration;
    const capturedRetryEpoch = this.retryGate.captureEpoch();
    this.inFlight = true;
    let shouldDrain = false;
    try {
      const response = await axiosClient.post<RecommendationEventBatchResponse>(
        '/recommendation-events',
        { events: batch },
      );
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      shouldDrain = this.handleResponse({
        status: typeof response.status === 'number' ? response.status : 200,
        data: response.data,
        headers: response.headers,
      }, batch, context.authContext, capturedRetryEpoch);
    } catch (error) {
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      const failure = getTelemetryHTTPFailure(error);
      shouldDrain = this.handleResponse({
        status: failure.status ?? 0,
        data: failure.data as RecommendationEventBatchResponse | undefined,
        headers: (error as { response?: { headers?: unknown } } | null)?.response?.headers,
      }, batch, context.authContext, capturedRetryEpoch, failure);
    } finally {
      this.inFlight = false;
      if (this.stopped) {
        this.retryGate.cancelScheduled();
      } else if (this.queue.length > 0) {
        if (capturedGeneration !== this.sessionGeneration || shouldDrain) {
          this.scheduleFlush(0);
        }
      }
    }
  }

  private async flushKeepalive(): Promise<void> {
    if (this.keepaliveInFlight || this.queue.length === 0) {
      return;
    }

    const context = this.getSendContext();
    if (!context) return;
    const batch = this.selectRequestBatch(this.queue);
    if (batch.length === 0) return;
    this.retryGate.cancelScheduled();
    const capturedGeneration = context.authContext.sessionGeneration;
    const capturedRetryEpoch = this.retryGate.captureEpoch();
    this.keepaliveInFlight = true;
    let shouldDrain = false;
    try {
      const response = await this.sendKeepalive(batch, context.accessToken);
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      shouldDrain = this.handleResponse(response, batch, context.authContext, capturedRetryEpoch);
    } catch (error) {
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      const failure = getTelemetryHTTPFailure(error);
      shouldDrain = this.handleResponse({
        status: failure.status ?? 0,
        data: failure.data as RecommendationEventBatchResponse | undefined,
        headers: (error as { response?: { headers?: unknown } } | null)?.response?.headers,
      }, batch, context.authContext, capturedRetryEpoch, failure);
    } finally {
      this.keepaliveInFlight = false;
      if (this.stopped) {
        this.retryGate.cancelScheduled();
      } else if (this.queue.length > 0 && (capturedGeneration !== this.sessionGeneration || shouldDrain)) {
        this.scheduleFlush(0);
      }
    }
  }

  private ensureObserver() {
    if (this.observer || typeof IntersectionObserver === 'undefined') {
      return;
    }
    this.observer = new IntersectionObserver(this.handleIntersections, { threshold: [0, 0.5, 1] });
  }

  private handleIntersections = (entries: IntersectionObserverEntry[]) => {
    entries.forEach(entry => {
      const observed = this.observed.get(entry.target);
      if (!observed) {
        return;
      }

      if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
        this.qualifyingElements.add(entry.target);
        this.startVisibilityTimer(entry.target);
      } else {
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
        if (state.intersecting && !state.finalized) {
          this.intersectingFeedDwells.add(state);
        } else {
          this.intersectingFeedDwells.delete(state);
        }
      }
    });
    this.reconcileActiveFeedDwell();
  };

  private startVisibilityTimer(element: Element) {
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

  private clearVisibilityTimer(element: Element) {
    const timer = this.visibilityTimers.get(element);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      this.visibilityTimers.delete(element);
    }
  }

  private handleVisibilityChange = () => {
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

  private handleViewportChange = () => {
    this.notifyViewportChange();
  };

  private handlePageHide = () => {
    this.finalizeAllFeedDwells();
    // The detail page registers after this shared client. Defer the global
    // flush until all synchronous pagehide producers have enqueued their
    // final read_end events.
    void Promise.resolve().then(() => this.flush(true));
  };

  private finalizeAllFeedDwells() {
    Array.from(this.feedDwellStates.values()).forEach(state => {
      this.finalizeFeedDwell(state.postID, state.tracking);
    });
  }

  private settleFeedDwellState(state: FeedDwellState, now = performance.now()) {
    if (state.activeStartedAt === null) {
      return;
    }
    state.accumulatedVisibleMS += Math.max(0, now - state.activeStartedAt);
    state.activeStartedAt = null;
  }

  private detachFeedDwellState(state: FeedDwellState) {
    this.intersectingFeedDwells.delete(state);
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

  private forgetObservedElement(element: Element) {
    const key = this.feedDwellByElement.get(element);
    const state = key === undefined ? undefined : this.feedDwellStates.get(key);
    if (state?.element === element) this.intersectingFeedDwells.delete(state);
    this.observer?.unobserve(element);
    this.clearVisibilityTimer(element);
    this.qualifyingElements.delete(element);
    this.observed.delete(element);
    this.feedDwellByElement.delete(element);
  }

  private reconcileActiveFeedDwell() {
    const candidates = document.visibilityState === 'visible'
      ? Array.from(this.intersectingFeedDwells)
        .filter(state =>
          !state.finalized
          && state.element !== null
          && state.intersecting
        )
        .map(state => {
          const rect = (state.element as Element).getBoundingClientRect();
          return {
            state,
            visibility: calculateRectVisibility(rect),
            distance: Math.abs((rect.top + rect.bottom) / 2 - window.innerHeight / 2),
          };
        })
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

  private scheduleFeedDwellReconciliation() {
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

  private cancelReconciliation() {
    if (this.reconciliationFrame === null) {
      return;
    }
    if (this.reconciliationFrameUsesRAF && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(this.reconciliationFrame);
    } else {
      window.clearTimeout(this.reconciliationFrame);
    }
    this.reconciliationFrame = null;
    this.reconciliationFrameUsesRAF = false;
  }

  private enqueue(
    eventType: RecommendationEventType,
    tracking: RecommendationTracking,
    payload?: RecommendationReadEndPayload | RecommendationFeedDwellPayload,
  ) {
    this.syncOwner();
    const queued: QueuedRecommendationEvent = {
      event_id: crypto.randomUUID(),
      event_type: eventType,
      tracking_token: tracking.token,
      occurred_at: new Date().toISOString(),
    };
    if (eventType === 'read_end' && payload) {
      Object.assign(queued, payload as RecommendationReadEndPayload);
    } else if (eventType === 'feed_dwell' && payload) {
      Object.assign(queued, payload as RecommendationFeedDwellPayload);
    }

    this.queue.push(queued);
    if (this.queue.length > maxBufferedEvents) {
      this.queue.splice(0, this.queue.length - maxBufferedEvents);
    }
    this.persistQueue();
    this.scheduleFlush(this.queue.length >= flushThreshold ? 0 : flushDelayMs);
  }

  private async sendKeepalive(events: QueuedRecommendationEvent[], accessToken: string) {
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
    let data: RecommendationEventBatchResponse | undefined;
    try {
      data = await response.json() as RecommendationEventBatchResponse;
    } catch {
      data = undefined;
    }
    return { status: response.status, data, headers: response.headers } satisfies RecommendationResponse;
  }

  private handleResponse(
    response: RecommendationResponse,
    batch: QueuedRecommendationEvent[],
    authContext: TelemetryRequestAuthContext,
    retryEpoch: number,
    failure?: ReturnType<typeof getTelemetryHTTPFailure>,
  ): boolean {
    const { status, data, headers } = response;
    const retryAfter = readTelemetryHeader(headers, 'Retry-After');
    const serverDate = readTelemetryHeader(headers, 'Date');

    if (status >= 200 && status < 300 || status === 422) {
      const outcome = this.applyBatchResponse(batch, data);
      if (status === 422 && !outcome.progressed) {
        this.dropBatch(batch);
        this.retryGate.clearAfterSuccess(retryEpoch);
        return true;
      }
      if (outcome.complete) {
        this.retryGate.clearAfterSuccess(retryEpoch);
      } else {
        this.recordRetryFailure(retryAfter, serverDate);
      }
      return outcome.progressed;
    }

    if (status === 401 || status === 403) {
      const failedFingerprint = this.failedRequestAuthFingerprint(authContext, failure);
      const currentFingerprint = this.authFingerprint(authContext.ownerID);
      if (failedFingerprint !== null && failedFingerprint === currentFingerprint) {
        this.pauseForAuth(authContext.ownerID, failedFingerprint);
        return false;
      }
      return currentFingerprint !== null;
    }
    if (
      status === 0
      || status === 408
      || status === 425
      || status === 429
      || status >= 500 && status <= 599
      || status >= 300 && status < 400
    ) {
      this.recordRetryFailure(retryAfter, serverDate);
      return false;
    }
    if (status >= 400 && status < 500) {
      this.dropBatch(batch);
      this.retryGate.clearAfterSuccess(retryEpoch);
      return true;
    }
    this.recordRetryFailure(retryAfter, serverDate);
    return false;
  }

  private applyBatchResponse(
    batch: QueuedRecommendationEvent[],
    response: RecommendationEventBatchResponse | undefined,
  ): { progressed: boolean; complete: boolean } {
    const batchIDs = new Set(batch.map(event => event.event_id));
    const terminalIDs = new Set<string>();
    if (Array.isArray(response?.results)) {
      for (const result of response.results) {
        if (
          typeof result?.event_id === 'string'
          && batchIDs.has(result.event_id)
          && (result.status === 'accepted' || result.status === 'duplicate' || result.status === 'rejected')
        ) {
          terminalIDs.add(result.event_id);
        }
      }
    }
    if (terminalIDs.size > 0) {
      this.queue = this.queue.filter(event => !terminalIDs.has(event.event_id));
      this.persistQueue();
      this.queuePersistence.flush();
    }
    return {
      progressed: terminalIDs.size > 0,
      complete: batch.every(event => terminalIDs.has(event.event_id)),
    };
  }

  private dropBatch(batch: QueuedRecommendationEvent[]) {
    const eventIDs = new Set(batch.map(event => event.event_id));
    this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
    this.persistQueue();
    this.queuePersistence.flush();
  }

  private scheduleFlush(delayMS: number) {
    if (this.stopped || this.queue.length === 0) return;
    const context = this.getSendContext();
    if (!context) return;
    this.retryGate.request(delayMS);
  }

  private getSendContext(): {
    ownerID: number;
    accessToken: string;
    authContext: TelemetryRequestAuthContext;
  } | null {
    const ownerID = this.syncOwner();
    if (!isValidUserID(ownerID) || this.queue.length === 0) return null;
    const accessToken = this.getAccessToken();
    if (!accessToken) return null;
    const sessionVersion = this.normalizedAuthSessionVersion();
    const fingerprint = createTelemetryAuthFingerprint(ownerID, accessToken, sessionVersion);
    if (fingerprint === null) return null;
    if (this.authPausedFingerprint !== null) {
      if (this.authPausedFingerprint === fingerprint) return null;
      this.authPausedFingerprint = null;
    }
    if (!this.retryGate.canSend()) {
      if (!this.stopped) this.retryGate.request(0);
      return null;
    }
    return {
      ownerID,
      accessToken,
      authContext: {
        ownerID,
        sessionGeneration: this.sessionGeneration,
        authFingerprint: fingerprint,
        authStateKey: accessToken,
        sessionVersion,
      },
    };
  }

  private recordRetryFailure(retryAfter?: string | null, serverDate?: string | null) {
    this.retryGate.recordFailure(retryAfter, serverDate);
    this.queuePersistence.flush();
  }

  private authFingerprint(ownerID: number): string | null {
    return createTelemetryAuthFingerprint(
      ownerID,
      this.getAccessToken(),
      this.normalizedAuthSessionVersion(),
    );
  }

  private failedRequestAuthFingerprint(
    context: TelemetryRequestAuthContext,
    failure?: ReturnType<typeof getTelemetryHTTPFailure>,
  ): string | null {
    const authorization = failure?.requestAuthorization?.trim();
    const authStateKey = authorization || context.authStateKey;
    const sessionVersion = failure?.requestSessionVersion ?? context.sessionVersion;
    if (authStateKey === context.authStateKey && sessionVersion === context.sessionVersion) {
      return context.authFingerprint;
    }
    return createTelemetryAuthFingerprint(
      context.ownerID,
      authStateKey,
      sessionVersion,
    );
  }

  private normalizedAuthSessionVersion(): number | null {
    const version = this.getAuthSessionVersion();
    return typeof version === 'number' && Number.isSafeInteger(version) && version >= 0
      ? version
      : null;
  }

  private pauseForAuth(ownerID: number, failedFingerprint: string) {
    if (this.authFingerprint(ownerID) !== failedFingerprint) return;
    this.authPausedFingerprint = failedFingerprint;
    this.retryGate.cancelScheduled();
    this.persistQueue();
    this.queuePersistence.flush();
  }

  private syncOwner(): number | null {
    const nextOwnerID = this.getCurrentUserID();
    if (!isValidUserID(nextOwnerID)) {
      if (this.ownerInitialized && !this.ownerUnavailable) {
        this.sessionGeneration += 1;
        this.ownerUnavailable = true;
      }
      return null;
    }

    if (!this.ownerInitialized) {
      this.ownerInitialized = true;
      this.ownerUserId = nextOwnerID;
      this.persistQueue();
    } else if (this.ownerUserId !== nextOwnerID) {
      this.sessionGeneration += 1;
      this.ownerUserId = nextOwnerID;
      this.authPausedFingerprint = null;
      this.resetObservedCards(false);
      this.clearSeenEvents();
      this.queue = [];
      this.retryGate.reset();
      this.persistQueue();
    } else if (this.ownerUnavailable) {
      this.sessionGeneration += 1;
    }
    this.ownerUnavailable = false;
    return nextOwnerID;
  }

  private clearSeenEvents() {
    this.seenImpressions.clear();
    this.seenClicks.clear();
    this.seenReadEnds.clear();
    this.seenNotInterested.clear();
    this.seenFeedDwells.clear();
  }

  private selectRequestBatch(events: QueuedRecommendationEvent[]): QueuedRecommendationEvent[] {
    const batch: QueuedRecommendationEvent[] = [];
    const encoder = new TextEncoder();
    for (const event of events) {
      if (batch.length >= maxBatchEvents) break;
      const candidate = [...batch, event];
      const bytes = encoder.encode(JSON.stringify({ events: candidate })).byteLength;
      if (bytes > maxRequestBodyBytes) {
        if (batch.length === 0) {
          this.dropBatch([event]);
          continue;
        }
        break;
      }
      batch.push(event);
    }
    return batch;
  }

  private businessKey(postID: number, tracking: RecommendationTracking) {
    return tracking.request_id + ':' + postID;
  }

  private loadSnapshot(): RecommendationSnapshot {
    try {
      const value = sessionStorage.getItem(storageKey);
      if (!value) {
        return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
      }
      const parsed = JSON.parse(value) as unknown;
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
      }
      const snapshot = parsed as Partial<RecommendationSnapshot>;
      return {
        events: Array.isArray(snapshot.events)
          ? snapshot.events.filter(isValidQueuedRecommendationEvent).slice(-maxBufferedEvents)
          : [],
        retry: snapshot.retry ?? { retryNotBeforeEpochMs: 0, retryAttempt: 0 },
        ownerUserId: isValidUserID(snapshot.ownerUserId) ? snapshot.ownerUserId : null,
      };
    } catch {
      return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
    }
  }

  private persistQueue() {
    this.queuePersistence.schedule();
  }

  private saveQueue() {
    try {
      const snapshot: RecommendationSnapshot = {
        events: this.queue,
        retry: this.retryGate.snapshot(),
        ownerUserId: this.ownerUserId,
      };
      sessionStorage.setItem(storageKey, JSON.stringify(snapshot));
    } catch {
      // Storage can be unavailable in privacy modes; server-side idempotency
      // still protects any events that are delivered.
    }
  }
}
