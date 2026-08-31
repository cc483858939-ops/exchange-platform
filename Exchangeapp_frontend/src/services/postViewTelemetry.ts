import { isAxiosError } from 'axios';
import apiClient from '../axios';

export type PostViewSource = 'post_detail' | 'feed';

export type PostViewEvent = {
  event_id: string;
  post_id: number;
  occurred_at: string;
  source: PostViewSource;
};

export type QueuedPostViewEvent = PostViewEvent & {
  owner_user_id: number;
};

type PostViewEventResult = {
  event_id: string;
  status: 'accepted' | 'rejected';
  reason?: string;
};

type PostViewBatchResponse = {
  accepted: number;
  rejected: number;
  results: PostViewEventResult[];
};

type CurrentUserIDGetter = () => number | null;

type FeedObservation = {
  postID: number;
  intersectionRatio: number;
  timer: number | null;
  emitted: boolean;
};

const storageKey = 'post_view_telemetry_queue_v1';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const feedQualificationThreshold = 0.5;
const feedQualificationDurationMS = 1000;
const retryDelaysMS = [1000, 2000, 5000, 10000, 30000, 60000];

let sharedClient: PostViewTelemetryClient | null = null;

const isValidUserID = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;

const isValidSource = (value: unknown): value is PostViewSource =>
  value === 'post_detail' || value === 'feed';

const isValidQueuedEvent = (value: unknown): value is QueuedPostViewEvent => {
  if (!value || typeof value !== 'object') {
    return false;
  }
  const event = value as Partial<QueuedPostViewEvent>;
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

const getErrorStatus = (error: unknown): number | null => {
  if (isAxiosError(error)) {
    return error.response?.status ?? null;
  }
  const response = (error as { response?: { status?: unknown } } | null)?.response;
  return typeof response?.status === 'number' ? response.status : null;
};

const getRetryAfterMS = (error: unknown): number => {
  const response = (error as {
    response?: {
      headers?: {
        get?: (name: string) => unknown;
        [key: string]: unknown;
      };
    };
  } | null)?.response;
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

export function createPostViewEventID(): string {
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
  private queue: QueuedPostViewEvent[] = [];
  private inFlight = false;
  private pendingFlush = false;
  private retryTimer: number | null = null;
  private retryAttempt = 0;
  private stopped = true;
  private feedObserver: IntersectionObserver | null = null;
  private feedObservations = new Map<HTMLElement, FeedObservation>();

  private readonly handleOnline = () => {
    this.clearRetryTimer();
    void this.flush();
  };

  private readonly handleVisibilityChange = () => {
    if (document.visibilityState === 'hidden') {
      this.cancelFeedTimers();
      return;
    }
    this.resumeVisibleFeedObservations();
  };

  constructor(private readonly getCurrentUserID: CurrentUserIDGetter) {
    this.queue = this.loadQueue();
    this.persistQueue();
  }

  start(): void {
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

  stop(): void {
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

  enqueue(
    postID: number,
    eventID: string,
    source: PostViewSource,
    occurredAt = new Date().toISOString(),
  ): boolean {
    const ownerUserID = this.getCurrentUserID();
    const normalizedEventID = eventID.trim();
    if (
      !isValidUserID(ownerUserID)
      || !Number.isSafeInteger(postID)
      || postID <= 0
      || !normalizedEventID
      || !isValidSource(source)
    ) {
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

  observeFeedCard(element: HTMLElement, postID: number): void {
    if (this.stopped || !this.feedObserver || !Number.isSafeInteger(postID) || postID <= 0) {
      return;
    }
    this.unobserveFeedCard(element);
    const observation: FeedObservation = {
      postID,
      intersectionRatio: 0,
      timer: null,
      emitted: false,
    };
    this.feedObservations.set(element, observation);
    this.feedObserver.observe(element);
  }

  unobserveFeedCard(element: HTMLElement): void {
    this.feedObserver?.unobserve(element);
    const observation = this.feedObservations.get(element);
    if (observation) {
      this.cancelFeedTimer(observation);
      this.feedObservations.delete(element);
    }
  }

  async flush(): Promise<void> {
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
      const response = await apiClient.post<PostViewBatchResponse>('/post-view-events', {
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
    } catch (error) {
      const status = getErrorStatus(error);
      if (status === 422) {
        const responseData = (error as { response?: { data?: PostViewBatchResponse } } | null)?.response?.data;
        const resultIDs = this.resultIDs(responseData);
        if (resultIDs.size > 0) {
          this.removeEventIDs(resultIDs);
        } else {
          this.dropBatch(batch);
        }
        this.resetRetryState();
        terminal = true;
      } else if (status !== 408 && status !== 425 && status !== 429 && !(status !== null && status >= 500 && status <= 599)) {
        if (status !== null && status >= 400 && status <= 499) {
          this.dropBatch(batch);
          this.resetRetryState();
          terminal = true;
        } else {
          this.scheduleRetry();
        }
      } else {
        this.scheduleRetry(getRetryAfterMS(error));
      }
    } finally {
      this.inFlight = false;
      if (this.pendingFlush) {
        this.pendingFlush = false;
        if (!this.stopped && terminal) {
          void this.flush();
        }
      } else if (!this.stopped && terminal && this.hasCurrentUserEvents()) {
        void this.flush();
      }
    }
  }

  private startFeedObserver(): void {
    if (typeof IntersectionObserver === 'undefined') {
      return;
    }
    this.feedObserver = new IntersectionObserver(
      entries => entries.forEach(entry => this.handleFeedIntersection(entry)),
      { threshold: [0, feedQualificationThreshold, 1] },
    );
  }

  private handleFeedIntersection(entry: IntersectionObserverEntry): void {
    const observation = this.feedObservations.get(entry.target as HTMLElement);
    if (!observation) {
      return;
    }
    observation.intersectionRatio = entry.intersectionRatio;
    if (document.visibilityState !== 'visible' || observation.emitted) {
      this.cancelFeedTimer(observation);
      return;
    }
    if (observation.intersectionRatio >= feedQualificationThreshold) {
      this.startFeedTimer(entry.target as HTMLElement, observation);
    } else {
      this.cancelFeedTimer(observation);
    }
  }

  private startFeedTimer(element: HTMLElement, observation: FeedObservation): void {
    if (observation.timer !== null || observation.emitted) {
      return;
    }
    observation.timer = window.setTimeout(() => {
      observation.timer = null;
      if (
        this.stopped
        || document.visibilityState !== 'visible'
        || observation.emitted
        || observation.intersectionRatio < feedQualificationThreshold
        || this.feedObservations.get(element) !== observation
      ) {
        return;
      }
      const accepted = this.enqueue(
        observation.postID,
        createPostViewEventID(),
        'feed',
      );
      if (accepted) {
        observation.emitted = true;
      }
    }, feedQualificationDurationMS);
  }

  private cancelFeedTimer(observation: FeedObservation): void {
    if (observation.timer !== null) {
      window.clearTimeout(observation.timer);
      observation.timer = null;
    }
  }

  private cancelFeedTimers(): void {
    for (const observation of this.feedObservations.values()) {
      this.cancelFeedTimer(observation);
    }
  }

  private resumeVisibleFeedObservations(): void {
    if (document.visibilityState !== 'visible') {
      return;
    }
    for (const [element, observation] of this.feedObservations) {
      if (!observation.emitted && observation.intersectionRatio >= feedQualificationThreshold) {
        this.startFeedTimer(element, observation);
      }
    }
  }

  private teardownFeedObserver(): void {
    this.feedObserver?.disconnect();
    this.feedObserver = null;
    for (const observation of this.feedObservations.values()) {
      this.cancelFeedTimer(observation);
    }
    this.feedObservations.clear();
  }

  private removeTerminalResults(
    batch: QueuedPostViewEvent[],
    response: PostViewBatchResponse,
    status: number,
  ): void {
    const resultIDs = this.resultIDs(response);
    if (resultIDs.size > 0) {
      this.removeEventIDs(resultIDs);
      return;
    }
    if (status >= 200 && status < 300) {
      this.dropBatch(batch);
    }
  }

  private resultIDs(response: PostViewBatchResponse | undefined): Set<string> {
    const resultIDs = new Set<string>();
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

  private removeEventIDs(eventIDs: Set<string>): void {
    this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
    this.persistQueue();
  }

  private dropBatch(batch: QueuedPostViewEvent[]): void {
    const eventIDs = new Set(batch.map(event => event.event_id));
    this.removeEventIDs(eventIDs);
  }

  private scheduleRetry(minimumDelayMS = 0): void {
    if (this.stopped || this.retryTimer !== null) {
      return;
    }
    const delay = Math.max(
      retryDelaysMS[Math.min(this.retryAttempt, retryDelaysMS.length - 1)],
      minimumDelayMS,
    );
    this.retryAttempt = Math.min(this.retryAttempt + 1, retryDelaysMS.length - 1);
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = null;
      void this.flush();
    }, delay);
  }

  private resetRetryState(): void {
    this.clearRetryTimer();
    this.retryAttempt = 0;
  }

  private clearRetryTimer(): void {
    if (this.retryTimer !== null) {
      window.clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
  }

  private hasCurrentUserEvents(): boolean {
    const currentUserID = this.getCurrentUserID();
    return isValidUserID(currentUserID)
      && this.queue.some(event => event.owner_user_id === currentUserID);
  }

  private loadQueue(): QueuedPostViewEvent[] {
    try {
      const raw = sessionStorage.getItem(storageKey);
      if (!raw) {
        return [];
      }
      const parsed: unknown = JSON.parse(raw);
      if (!Array.isArray(parsed)) {
        return [];
      }
      return parsed.filter(isValidQueuedEvent).slice(-maxBufferedEvents);
    } catch {
      return [];
    }
  }

  private persistQueue(): void {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(this.queue));
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
  }
}

export function initializePostViewTelemetry(
  getCurrentUserID: CurrentUserIDGetter,
): PostViewTelemetryClient {
  if (!sharedClient) {
    sharedClient = new PostViewTelemetryClient(getCurrentUserID);
    sharedClient.start();
  }
  return sharedClient;
}

export function getPostViewTelemetry(): PostViewTelemetryClient {
  return sharedClient ?? initializePostViewTelemetry(() => null);
}

export function resetPostViewTelemetryForTests(): void {
  sharedClient?.stop();
  sharedClient = null;
}

