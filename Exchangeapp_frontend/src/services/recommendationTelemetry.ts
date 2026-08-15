import { isAxiosError } from 'axios';
import axiosClient from '../axios';
import type { RecommendationTracking } from '../types/Recommendation';

export type RecommendationEventType = 'impression' | 'click' | 'read_end' | 'not_interested';

export type RecommendationReadEndPayload = { foreground_time_ms: number; scroll_progress_percent: number; exit_type: string; };

type QueuedRecommendationEvent = {
  event_id: string;
  event_type: RecommendationEventType;
  tracking_token: string;
  occurred_at: string;
  foreground_time_ms?: number;
  scroll_progress_percent?: number;
  exit_type?: string;
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

type ObservedRecommendation = {
  articleID: number;
  tracking: RecommendationTracking;
};

const storageKey = 'recommendation_telemetry_queue_v2';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const flushThreshold = 10;
const flushDelayMs = 1000;
const impressionDelayMs = 1000;
const maxRetryDelayMs = 30_000;

let sharedClient: RecommendationTelemetryClient | null = null;

export const getRecommendationTelemetry = (getAccessToken: () => string | null) => {
  if (!sharedClient) {
    sharedClient = new RecommendationTelemetryClient(getAccessToken);
    sharedClient.start();
  }
  return sharedClient;
};

export class RecommendationTelemetryClient {
  private queue: QueuedRecommendationEvent[] = [];
  private observer: IntersectionObserver | null = null;
  private observed = new WeakMap<Element, ObservedRecommendation>();
  private qualifyingElements = new Set<Element>();
  private visibilityTimers = new Map<Element, number>();
  private seenImpressions = new Set<string>();
  private seenClicks = new Set<string>();
  private seenReadEnds = new Set<string>();
  private seenNotInterested = new Set<string>();
  private flushTimer: number | null = null;
  private retryTimer: number | null = null;
  private retryDelayMs = 1000;
  private inFlight = false;
  private keepaliveInFlight = false;
  private pendingFlush = false;
  private stopped = true;

  constructor(private readonly getAccessToken: () => string | null) {
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
    this.observed = new WeakMap<Element, ObservedRecommendation>();
  }

  clearSession() {
    this.resetObservedCards();
    this.queue = [];
    this.seenImpressions.clear();
    this.seenClicks.clear();
    this.seenReadEnds.clear();
    this.seenNotInterested.clear();
    this.persistQueue();
    this.clearScheduledFlush();
    this.clearRetry();
  }

  observeCard(element: HTMLElement, articleID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) {
      return;
    }
    this.ensureObserver();
    this.observed.set(element, { articleID, tracking });
    this.observer?.observe(element);
  }

  recordClick(articleID: number, tracking?: RecommendationTracking) {
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

  recordReadEnd(articleID: number, tracking: RecommendationTracking | undefined, payload: RecommendationReadEndPayload) {
    if (!tracking?.token) return false;
    const key = this.businessKey(articleID, tracking);
    if (this.seenReadEnds.has(key)) return false;
    this.seenReadEnds.add(key);
    this.enqueue('read_end', tracking, payload);
    return true;
  }

  recordNotInterested(articleID: number, tracking?: RecommendationTracking) {
    if (!tracking?.token) return;
    const key = this.businessKey(articleID, tracking);
    if (this.seenNotInterested.has(key)) return;
    this.seenNotInterested.add(key);
    this.enqueue('not_interested', tracking);
    void this.flush(false);
  }
  async flush(keepalive = false): Promise<void> {
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
      const response = await axiosClient.post<RecommendationEventBatchResponse>('/recommendation-events', { events: batch });
      this.applyBatchResponse(response.data);
      this.retryDelayMs = 1000;
      this.clearRetry();
    } catch (error) {
      if (isAxiosError<RecommendationEventBatchResponse>(error) && error.response?.data?.results) {
        this.applyBatchResponse(error.response.data);
      } else if (isAxiosError(error) && [400, 404, 413].includes(error.response?.status ?? 0)) {
        this.dropBatch(batch);
      } else {
        this.scheduleRetry();
      }
    } finally {
      this.inFlight = false;
      if (this.pendingFlush || this.queue.length >= flushThreshold) {
        this.pendingFlush = false;
        this.scheduleFlush(0);
      } else if (this.queue.length > 0 && this.retryTimer === null) {
        this.scheduleFlush(flushDelayMs);
      }
    }
  }

  private async flushKeepalive(): Promise<void> {
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
    } catch (error) {
      if (isAxiosError<RecommendationEventBatchResponse>(error) && error.response?.data?.results) {
        this.applyBatchResponse(error.response.data);
      } else if (isAxiosError(error) && [400, 404, 413].includes(error.response?.status ?? 0)) {
        this.dropBatch(batch);
      } else {
        this.scheduleRetry();
      }
    } finally {
      this.keepaliveInFlight = false;
      if (this.queue.length > 0 && this.retryTimer === null) {
        this.scheduleFlush(flushDelayMs);
      }
    }
  }

  private ensureObserver() {
    if (this.observer) {
      return;
    }
    this.observer = new IntersectionObserver(this.handleIntersections, { threshold: [0, 0.5, 1] });
  }

  private handleIntersections = (entries: IntersectionObserverEntry[]) => {
    entries.forEach(entry => {
      if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
        this.qualifyingElements.add(entry.target);
        this.startVisibilityTimer(entry.target);
      } else {
        this.qualifyingElements.delete(entry.target);
        this.clearVisibilityTimer(entry.target);
      }
    });
  };

  private startVisibilityTimer(element: Element) {
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
      void this.flush(true);
      return;
    }
    this.qualifyingElements.forEach(element => this.startVisibilityTimer(element));
  };

  private handlePageHide = () => {
    // The detail page registers after this shared client. Defer the global
    // flush until all synchronous pagehide producers have enqueued their
    // final read_end events.
    void Promise.resolve().then(() => this.flush(true));
  };

  private enqueue(eventType: RecommendationEventType, tracking: RecommendationTracking, readPayload?: RecommendationReadEndPayload) {
    this.queue.push({
      event_id: crypto.randomUUID(),
      event_type: eventType,
      tracking_token: tracking.token,
      occurred_at: new Date().toISOString(),
      ...readPayload,
    });
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
    const data = (await response.json()) as RecommendationEventBatchResponse;
    if (!response.ok && response.status !== 422) {
      throw new Error(`recommendation telemetry request failed: ${response.status}`);
    }
    return { data };
  }

  private applyBatchResponse(response: RecommendationEventBatchResponse) {
    const terminalIDs = new Set(response.results.map(result => result.event_id));
    if (terminalIDs.size === 0) {
      return;
    }
    this.queue = this.queue.filter(event => !terminalIDs.has(event.event_id));
    this.persistQueue();
  }

  private dropBatch(batch: QueuedRecommendationEvent[]) {
    const eventIDs = new Set(batch.map(event => event.event_id));
    this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
    this.persistQueue();
  }

  private scheduleFlush(delayMs: number) {
    if (this.stopped || this.flushTimer !== null) {
      return;
    }
    this.flushTimer = window.setTimeout(() => {
      this.flushTimer = null;
      void this.flush(false);
    }, delayMs);
  }

  private scheduleRetry() {
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

  private clearScheduledFlush() {
    if (this.flushTimer !== null) {
      window.clearTimeout(this.flushTimer);
      this.flushTimer = null;
    }
  }

  private clearRetry() {
    if (this.retryTimer !== null) {
      window.clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
  }

  private businessKey(articleID: number, tracking: RecommendationTracking) {
    return `${tracking.request_id}:${articleID}`;
  }

  private loadQueue(): QueuedRecommendationEvent[] {
    try {
      const value = sessionStorage.getItem(storageKey);
      if (!value) {
        return [];
      }
      const parsed = JSON.parse(value) as unknown;
      return Array.isArray(parsed) ? (parsed as QueuedRecommendationEvent[]).slice(-maxBufferedEvents) : [];
    } catch {
      return [];
    }
  }

  private persistQueue() {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(this.queue));
    } catch {
      // Storage can be unavailable in privacy modes; server-side idempotency
      // still protects any events that are delivered.
    }
  }
}
