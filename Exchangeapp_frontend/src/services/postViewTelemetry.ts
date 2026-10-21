import apiClient from '../axios';
import { createTelemetryPersistence } from './telemetryPersistence';
import { createClientOperationID } from '../utils/clientOperationId';
import {
  TelemetryRetryGate,
  createTelemetryAuthFingerprint,
  type TelemetryRequestAuthContext,
  type TelemetrySnapshot,
  getTelemetryHTTPFailure,
} from './telemetryRetryGate';

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
type AuthStateGetter = () => string | number | null;
type AuthSessionVersionGetter = () => number | null;
type PostViewSnapshot = TelemetrySnapshot<QueuedPostViewEvent>;

type FeedObservation = {
  postID: number;
  intersectionRatio: number;
  timer: number | null;
  emitted: boolean;
  viewSessionKey?: string;
};

const storageKey = 'post_view_telemetry_snapshot_v2';
const maxBufferedEvents = 200;
const maxBatchEvents = 50;
const feedQualificationThreshold = 0.5;
const feedQualificationDurationMS = 1000;

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

export const createPostViewEventID = createClientOperationID;

export class PostViewTelemetryClient {
  private queue: QueuedPostViewEvent[] = [];
  private inFlight = false;
  private stopped = true;
  private sessionGeneration = 0;
  private activeOwnerID: number | null = null;
  private ownerInitialized = false;
  private ownerUnavailable = false;
  private authPausedFingerprint: string | null = null;
  private feedObserver: IntersectionObserver | null = null;
  private feedObservations = new Map<HTMLElement, FeedObservation>();
  private emittedFeedPostsBySession = new Map<string, Set<number>>();
  private queuePersistence = createTelemetryPersistence(() => this.saveQueue());
  private retryGate: TelemetryRetryGate;

  private readonly handlePageHide = () => {
    this.queuePersistence.flush();
    // Include events produced by pagehide handlers registered after this client.
    void Promise.resolve().then(() => this.queuePersistence.flush());
  };

  private readonly handleOnline = () => {
    this.scheduleFlush(0);
  };

  private readonly handleVisibilityChange = () => {
    if (document.visibilityState === 'hidden') {
      this.queuePersistence.flush();
      this.cancelFeedTimers();
      return;
    }
    this.resumeVisibleFeedObservations();
  };

  constructor(
    private readonly getCurrentUserID: CurrentUserIDGetter,
    private readonly getAuthStateKey: AuthStateGetter = () => 'authenticated',
    private readonly getAuthSessionVersion: AuthSessionVersionGetter = () => null,
  ) {
    this.retryGate = new TelemetryRetryGate(
      () => { if (!this.stopped) void this.flush(); },
      () => this.persistQueue(),
    );
    const snapshot = this.loadSnapshot();
    this.queue = snapshot.events;
    this.retryGate.restore(snapshot.retry);
    if (isValidUserID(snapshot.ownerUserId)) {
      this.activeOwnerID = snapshot.ownerUserId;
      this.ownerInitialized = true;
    }
    this.syncOwner();
    this.persistQueue();
    this.queuePersistence.flush();
  }

  start(): void {
    if (!this.stopped) {
      return;
    }
    this.stopped = false;
    window.addEventListener('online', this.handleOnline);
    window.addEventListener('pagehide', this.handlePageHide);
    document.addEventListener('visibilitychange', this.handleVisibilityChange);
    this.startFeedObserver();
    void this.flush();
  }

  stop(): void {
    this.queuePersistence.flush();
    if (!this.stopped) {
      window.removeEventListener('online', this.handleOnline);
      window.removeEventListener('pagehide', this.handlePageHide);
      document.removeEventListener('visibilitychange', this.handleVisibilityChange);
      this.retryGate.cancelScheduled();
      this.teardownFeedObserver();
      this.stopped = true;
    }
    this.emittedFeedPostsBySession.clear();
  }

  refreshAuthState(): void {
    this.scheduleFlush(0);
  }

  enqueue(
    postID: number,
    eventID: string,
    source: PostViewSource,
    occurredAt = new Date().toISOString(),
  ): boolean {
    const ownerUserID = this.syncOwner();
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

  observeFeedCard(element: HTMLElement, postID: number, viewSessionKey?: string): void {
    if (this.stopped || !this.feedObserver || !Number.isSafeInteger(postID) || postID <= 0) {
      return;
    }
    this.unobserveFeedCard(element);
    const normalizedSessionKey = typeof viewSessionKey === 'string' && viewSessionKey.trim().length > 0
      ? viewSessionKey.trim()
      : undefined;
    const observation: FeedObservation = {
      postID,
      intersectionRatio: 0,
      timer: null,
      emitted: normalizedSessionKey !== undefined
        && this.emittedFeedPostsBySession.get(normalizedSessionKey)?.has(postID) === true,
      viewSessionKey: normalizedSessionKey,
    };
    this.feedObservations.set(element, observation);
    this.feedObserver.observe(element);
  }

  releaseFeedViewSession(viewSessionKey: string): void {
    const normalizedSessionKey = viewSessionKey.trim();
    if (!normalizedSessionKey) {
      return;
    }
    this.emittedFeedPostsBySession.delete(normalizedSessionKey);
    for (const observation of this.feedObservations.values()) {
      if (observation.viewSessionKey === normalizedSessionKey) {
        observation.emitted = false;
      }
    }
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
    if (this.stopped || this.inFlight) {
      return;
    }
    const currentUserID = this.syncOwner();
    if (!isValidUserID(currentUserID) || !this.canSendForAuth(currentUserID)) return;
    if (!this.retryGate.canSend()) {
      this.scheduleFlush(0);
      return;
    }

    const batch = this.queue
      .filter(event => event.owner_user_id === currentUserID)
      .slice(0, maxBatchEvents);
    if (batch.length === 0) {
      return;
    }

    const requestAuthContext = this.captureRequestAuthContext(currentUserID);
    if (!requestAuthContext) return;
    const capturedGeneration = requestAuthContext.sessionGeneration;
    const capturedRetryEpoch = this.retryGate.captureEpoch();
    this.retryGate.cancelScheduled();
    this.inFlight = true;
    let shouldDrain = false;
    try {
      const response = await apiClient.post<PostViewBatchResponse>('/post-view-events', {
        events: batch.map(({ event_id, post_id, occurred_at, source }) => ({
          event_id,
          post_id,
          occurred_at,
          source,
        })),
      });
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      const outcome = this.removeTerminalResults(batch, response.data);
      if (outcome.complete) {
        this.retryGate.clearAfterSuccess(capturedRetryEpoch);
      } else {
        this.recordRetryFailure();
      }
      shouldDrain = outcome.progressed;
    } catch (error) {
      this.syncOwner();
      if (capturedGeneration !== this.sessionGeneration) return;
      const failure = getTelemetryHTTPFailure(error);
      const status = failure.status;
      if (status === 422) {
        const responseData = failure.data as PostViewBatchResponse | undefined;
        const outcome = this.removeTerminalResults(batch, responseData);
        if (outcome.progressed) {
          if (outcome.complete) {
            this.retryGate.clearAfterSuccess(capturedRetryEpoch);
          } else {
            this.recordRetryFailure(failure.retryAfter, failure.date);
          }
          shouldDrain = true;
        } else {
          this.dropBatch(batch);
          this.retryGate.clearAfterSuccess(capturedRetryEpoch);
          shouldDrain = true;
        }
      } else if (status === 401 || status === 403) {
        const failedFingerprint = this.failedRequestAuthFingerprint(requestAuthContext, failure);
        const currentFingerprint = this.authFingerprint(currentUserID);
        if (failedFingerprint !== null && failedFingerprint === currentFingerprint) {
          this.pauseForAuth(currentUserID, failedFingerprint);
        } else {
          shouldDrain = currentFingerprint !== null;
        }
      } else if (status !== 408 && status !== 425 && status !== 429 && !(status !== null && status >= 500 && status <= 599)) {
        if (status !== null && status >= 400 && status <= 499) {
          this.dropBatch(batch);
          this.retryGate.clearAfterSuccess(capturedRetryEpoch);
          shouldDrain = true;
        } else {
          this.recordRetryFailure(failure.retryAfter, failure.date);
        }
      } else {
        this.recordRetryFailure(failure.retryAfter, failure.date);
      }
    } finally {
      this.inFlight = false;
      if (this.stopped) {
        this.retryGate.cancelScheduled();
      } else if (this.hasCurrentUserEvents()) {
        if (capturedGeneration !== this.sessionGeneration || shouldDrain) {
          this.scheduleFlush(0);
        }
      }
    }
  }

  private scheduleFlush(delayMS: number): void {
    if (this.stopped || !this.hasCurrentUserEvents()) return;
    const ownerID = this.syncOwner();
    if (!isValidUserID(ownerID) || !this.canSendForAuth(ownerID)) return;
    this.retryGate.request(delayMS);
  }

  private syncOwner(): number | null {
    const ownerID = this.getCurrentUserID();
    if (!isValidUserID(ownerID)) {
      if (this.ownerInitialized && !this.ownerUnavailable) {
        this.sessionGeneration += 1;
        this.ownerUnavailable = true;
      }
      return null;
    }

    if (!this.ownerInitialized) {
      this.ownerInitialized = true;
      this.activeOwnerID = ownerID;
      this.persistQueue();
    } else if (this.activeOwnerID !== ownerID) {
      this.activeOwnerID = ownerID;
      this.sessionGeneration += 1;
      this.authPausedFingerprint = null;
      this.queue = this.queue.filter(event => event.owner_user_id === ownerID);
      this.retryGate.reset();
      this.persistQueue();
    } else if (this.ownerUnavailable) {
      this.sessionGeneration += 1;
    }
    this.ownerUnavailable = false;
    return ownerID;
  }

  private authFingerprint(ownerID: number): string | null {
    return createTelemetryAuthFingerprint(
      ownerID,
      this.getAuthStateKey(),
      this.normalizedAuthSessionVersion(),
    );
  }

  private captureRequestAuthContext(ownerID: number): TelemetryRequestAuthContext | null {
    const authStateKey = this.getAuthStateKey();
    const sessionVersion = this.normalizedAuthSessionVersion();
    const authFingerprint = createTelemetryAuthFingerprint(ownerID, authStateKey, sessionVersion);
    if (authFingerprint === null || authStateKey === null || authStateKey === '') return null;
    return {
      ownerID,
      sessionGeneration: this.sessionGeneration,
      authFingerprint,
      authStateKey,
      sessionVersion,
    };
  }

  private failedRequestAuthFingerprint(
    context: TelemetryRequestAuthContext,
    failure: ReturnType<typeof getTelemetryHTTPFailure>,
  ): string | null {
    const authorization = failure.requestAuthorization?.trim();
    const authStateKey = authorization || context.authStateKey;
    const sessionVersion = failure.requestSessionVersion ?? context.sessionVersion;
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

  private canSendForAuth(ownerID: number): boolean {
    const fingerprint = this.authFingerprint(ownerID);
    if (fingerprint === null) return false;
    if (this.authPausedFingerprint !== null) {
      if (this.authPausedFingerprint === fingerprint) return false;
      this.authPausedFingerprint = null;
    }
    return true;
  }

  private pauseForAuth(ownerID: number, failedFingerprint: string): void {
    if (this.authFingerprint(ownerID) !== failedFingerprint) return;
    this.authPausedFingerprint = failedFingerprint;
    this.retryGate.cancelScheduled();
    this.persistQueue();
    this.queuePersistence.flush();
  }

  private recordRetryFailure(retryAfter?: string | null, serverDate?: string | null): void {
    this.retryGate.recordFailure(retryAfter, serverDate);
    this.queuePersistence.flush();
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
        if (observation.viewSessionKey) {
          const emittedPosts = this.emittedFeedPostsBySession.get(observation.viewSessionKey)
            ?? new Set<number>();
          emittedPosts.add(observation.postID);
          this.emittedFeedPostsBySession.set(observation.viewSessionKey, emittedPosts);
        }
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
    response: PostViewBatchResponse | undefined,
  ): { progressed: boolean; complete: boolean } {
    const batchIDs = new Set(batch.map(event => event.event_id));
    const resultIDs = this.resultIDs(response, batchIDs);
    if (resultIDs.size > 0) {
      this.removeEventIDs(resultIDs);
    }
    return {
      progressed: resultIDs.size > 0,
      complete: batch.every(event => resultIDs.has(event.event_id)),
    };
  }

  private resultIDs(response: PostViewBatchResponse | undefined, batchIDs: Set<string>): Set<string> {
    const resultIDs = new Set<string>();
    if (!Array.isArray(response?.results)) {
      return resultIDs;
    }
    for (const result of response.results) {
      if (
        typeof result?.event_id === 'string'
        && batchIDs.has(result.event_id)
        && (result.status === 'accepted' || result.status === 'rejected')
      ) {
        resultIDs.add(result.event_id);
      }
    }
    return resultIDs;
  }

  private removeEventIDs(eventIDs: Set<string>): void {
    this.queue = this.queue.filter(event => !eventIDs.has(event.event_id));
    this.persistQueue();
    this.queuePersistence.flush();
  }

  private dropBatch(batch: QueuedPostViewEvent[]): void {
    const eventIDs = new Set(batch.map(event => event.event_id));
    this.removeEventIDs(eventIDs);
  }

  private hasCurrentUserEvents(): boolean {
    const currentUserID = this.syncOwner();
    return isValidUserID(currentUserID)
      && this.queue.some(event => event.owner_user_id === currentUserID);
  }

  private loadSnapshot(): PostViewSnapshot {
    try {
      const raw = sessionStorage.getItem(storageKey);
      if (!raw) {
        return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
      }
      const parsed: unknown = JSON.parse(raw);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
      }
      const snapshot = parsed as Partial<PostViewSnapshot>;
      const retry = snapshot.retry && typeof snapshot.retry === 'object'
        ? snapshot.retry
        : { retryNotBeforeEpochMs: 0, retryAttempt: 0 };
      return {
        events: Array.isArray(snapshot.events)
          ? snapshot.events.filter(isValidQueuedEvent).slice(-maxBufferedEvents)
          : [],
        retry,
        ownerUserId: isValidUserID(snapshot.ownerUserId) ? snapshot.ownerUserId : null,
      };
    } catch {
      return { events: [], retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 }, ownerUserId: null };
    }
  }

  private persistQueue(): void {
    this.queuePersistence.schedule();
  }

  private saveQueue(): void {
    try {
      const snapshot: PostViewSnapshot = {
        events: this.queue,
        retry: this.retryGate.snapshot(),
        ownerUserId: this.activeOwnerID,
      };
      sessionStorage.setItem(storageKey, JSON.stringify(snapshot));
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
  }
}

export function initializePostViewTelemetry(
  getCurrentUserID: CurrentUserIDGetter,
  getAuthStateKey?: AuthStateGetter,
  getAuthSessionVersion?: AuthSessionVersionGetter,
): PostViewTelemetryClient {
  if (!sharedClient) {
    sharedClient = new PostViewTelemetryClient(getCurrentUserID, getAuthStateKey, getAuthSessionVersion);
    sharedClient.start();
  }
  return sharedClient;
}

export function getPostViewTelemetry(): PostViewTelemetryClient {
  return sharedClient ?? initializePostViewTelemetry(() => null);
}

export function releasePostViewSession(viewSessionKey: string): void {
  sharedClient?.releaseFeedViewSession(viewSessionKey);
}

export function resetPostViewTelemetryForTests(): void {
  sharedClient?.stop();
  sharedClient = null;
}
