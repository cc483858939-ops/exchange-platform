// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RecommendationTelemetryClient,
  type RecommendationReadEndPayload,
} from './recommendationTelemetry';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  fetch: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

const tracking = {
  request_id: 'request-42',
  position: 1,
  scene: 'recommendation_page',
  ranker_version: 'rules_v3',
  ranker_config_hash: 'config-hash',
  strategy_id: 'cold_start_rules_v3',
  token: 'v2.token.signature',
  expires_at: '2099-08-15T00:00:00.000Z',
};

const readPayload: RecommendationReadEndPayload = {
  foreground_time_ms: 5000,
  scroll_progress_percent: 25,
  exit_type: 'route_leave',
};
const recommendationStorageKey = 'recommendation_telemetry_snapshot_v3';
const savedRecommendationSnapshot = () => JSON.parse(
  sessionStorage.getItem(recommendationStorageKey) ?? '{"events":[]}',
);

const responseFor = (events: Array<{ event_id: string }>) => ({
  accepted: events.length,
  duplicates: 0,
  rejected: 0,
  results: events.map(event => ({
    event_id: event.event_id,
    status: 'accepted' as const,
  })),
});

const eventsFromRequest = (request: RequestInit | undefined) =>
  JSON.parse(String(request?.body ?? '{}')).events as Array<{
    event_id: string;
    event_type: string;
    exit_type?: string;
  }>;

const eventsFromPost = (callIndex: number) =>
  (mocks.post.mock.calls[callIndex][1] as { events: Array<{ event_id: string; event_type: string }> }).events;

const flushMicrotasks = async () => {
  for (let index = 0; index < 5; index += 1) {
    await Promise.resolve();
  }
};

describe('RecommendationTelemetryClient delivery', () => {
  let client: RecommendationTelemetryClient | null = null;

  beforeEach(() => {
    sessionStorage.clear();
    mocks.post.mockReset();
    mocks.fetch.mockReset();
    vi.stubGlobal('fetch', mocks.fetch);
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => 'event-' + Math.random()) });
  });

  afterEach(() => {
    client?.clearSession();
    client?.stop();
    client = null;
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it('captures a read_end enqueued by a later pagehide producer', async () => {
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromRequest(request)),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    window.addEventListener('pagehide', () => {
      client?.recordReadEnd(42, tracking, { ...readPayload, exit_type: 'page_hide' });
    }, { once: true });
    window.dispatchEvent(new Event('pagehide'));

    await Promise.resolve();
    await Promise.resolve();

    expect(mocks.fetch).toHaveBeenCalledTimes(1);
    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ event_type: 'read_end', exit_type: 'page_hide' });
  });

  it('does not start Axios when recordReadEnd only enqueues an event', () => {
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);

    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(true);
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.fetch).not.toHaveBeenCalled();
  });

  it('sends the final event with keepalive while normal Axios is in flight', async () => {
    let resolveNormal: ((value: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise(resolve => {
      resolveNormal = resolve;
    }));
    mocks.fetch.mockImplementation(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ ...responseFor([]), results: [] }),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    client.recordClick(7, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    client.recordReadEnd(42, tracking, readPayload);
    await client.flush(true);

    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events.some(event => event.event_type === 'read_end')).toBe(true);

    resolveNormal?.({ data: responseFor([]) });
    await Promise.resolve();
  });

  it('deduplicates repeated read_end calls by business key', async () => {
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromRequest(request)),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(true);
    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(false);
    await client.flush(true);

    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events.filter(event => event.event_type === 'read_end')).toHaveLength(1);
  });

  it('deduplicates clicks within a request but allows a new recommendation request immediately', async () => {
    let now = 1_000_000;
    const dateNow = vi.spyOn(Date, 'now').mockImplementation(() => now);
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `click-${++nextEventID}`) });
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    const firstEvent = eventsFromPost(0)[0];

    now += 60_000;
    client.recordClick(42, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    client.recordClick(42, { ...tracking, request_id: 'another-recommendation-request' });
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)[0].event_id).not.toBe(firstEvent.event_id);

    now += 5 * 60_000;
    client.recordClick(42, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(2);
    dateNow.mockRestore();
  });

  it('clears click deduplication state when the session is cleared', async () => {
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `session-click-${++nextEventID}`) });
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    client.clearSession();
    client.recordClick(42, tracking);
    await flushMicrotasks();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)[0].event_id).not.toBe(eventsFromPost(0)[0].event_id);
  });

  it('retries an already queued click with its original Event ID', async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(0);
    let attempts = 0;
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => {
      attempts += 1;
      if (attempts === 1) throw new Error('temporary failure');
      return { data: responseFor(body.events) };
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    const originalEventID = eventsFromPost(0)[0].event_id;

    await vi.advanceTimersByTimeAsync(1000);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)[0].event_id).toBe(originalEventID);
  });

  it('does not let a threshold flush bypass Retry-After after a failed send', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    client.stop();
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    for (let index = 0; index < 9; index += 1) {
      client.recordReadEnd(100 + index, {
        ...tracking,
        request_id: 'threshold-' + index,
        token: 'v2.threshold-' + index,
      }, readPayload);
    }
    await vi.advanceTimersByTimeAsync(0);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(59_999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    await vi.advanceTimersByTimeAsync(1);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('does not send keepalive during Retry-After cooldown', async () => {
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordClick(42, tracking);
    await flushMicrotasks();

    await client.flush(true);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.fetch).not.toHaveBeenCalled();
  });

  it('persists final events on hidden and pagehide without sending during cooldown', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    client.recordReadEnd(43, { ...tracking, request_id: 'hidden-read-end' }, readPayload);
    const visibilityDescriptor = Object.getOwnPropertyDescriptor(document, 'visibilityState');
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('pagehide'));
    await flushMicrotasks();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.fetch).not.toHaveBeenCalled();
    expect(savedRecommendationSnapshot().events).toHaveLength(2);
    expect(savedRecommendationSnapshot().retry.retryNotBeforeEpochMs).toBe(Date.now() + 60_000);
    if (visibilityDescriptor) {
      Object.defineProperty(document, 'visibilityState', visibilityDescriptor);
    } else {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    }
  });

  it('honors a Fetch 503 Retry-After before the next Axios retry', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    vi.spyOn(Math, 'random').mockReturnValue(0);
    mocks.fetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      headers: new Headers({ 'Retry-After': '60' }),
      json: async () => ({}),
    });
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordReadEnd(42, tracking, readPayload);
    await client.flush(true);

    expect(mocks.fetch).toHaveBeenCalledTimes(1);
    await client.flush(false);
    await client.flush(true);
    await vi.advanceTimersByTimeAsync(59_999);
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.fetch).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
  });

  it('backs off a 2xx response without in-batch terminal progress', async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(0);
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `no-progress-${++nextEventID}`) });
    mocks.post.mockResolvedValueOnce({ status: 202, data: { results: [] } });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordReadEnd(42, tracking, readPayload);
    await client.flush(false);
    await flushMicrotasks();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedRecommendationSnapshot().events).toHaveLength(1);
    client.recordReadEnd(43, { ...tracking, request_id: 'queued-behind-cooldown' }, readPayload);
    await client.flush(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.fetch).not.toHaveBeenCalled();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    await vi.advanceTimersByTimeAsync(1000);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('processes only current batch IDs for 422 and retries unconfirmed events later', async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(0);
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `partial-422-${++nextEventID}`) });
    mocks.post.mockRejectedValueOnce({
      response: {
        status: 422,
        data: {
          accepted: 1,
          duplicates: 0,
          rejected: 0,
          results: [
            { event_id: 'partial-422-1', status: 'accepted' },
            { event_id: 'foreign-event', status: 'accepted' },
          ],
        },
      },
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordReadEnd(42, tracking, readPayload);
    client.recordReadEnd(43, { ...tracking, request_id: 'second-422' }, readPayload);
    await client.flush(false);
    await flushMicrotasks();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedRecommendationSnapshot().events).toHaveLength(1);
    expect(savedRecommendationSnapshot().events[0].event_id).toBe('partial-422-2');
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1).map(event => event.event_id)).toEqual(['partial-422-2']);
  });

  it('keeps Recommendation batches to 50 events and keepalive bodies below 64 KiB', async () => {
    vi.useFakeTimers();
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `batch-${++nextEventID}`) });
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    for (let index = 1; index <= 51; index += 1) {
      client.recordReadEnd(index, { ...tracking, request_id: `batch-${index}` }, readPayload);
    }
    await client.flush(false);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(eventsFromPost(0)).toHaveLength(50);
    await vi.advanceTimersByTimeAsync(0);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)).toHaveLength(1);

    const largeTracking = { ...tracking, request_id: 'large-body', token: 'x'.repeat(40_000) };
    client.recordReadEnd(100, largeTracking, readPayload);
    client.recordReadEnd(101, { ...largeTracking, request_id: 'large-body-2' }, readPayload);
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => responseFor(eventsFromRequest(request)),
    }));
    await client.flush(true);
    const body = String(mocks.fetch.mock.calls[0][1].body);
    expect(new TextEncoder().encode(body).byteLength).toBeLessThan(64 * 1024);
    expect(eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit)).toHaveLength(1);
  });

  it.each([401, 403])('pauses %i until the token changes', async status => {
    vi.useFakeTimers();
    let token = 'Bearer before-auth-change';
    mocks.post.mockRejectedValueOnce({ response: { status } });
    client = new RecommendationTelemetryClient(() => token, () => 7);
    client.start();
    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(300_000);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    token = 'Bearer after-auth-change';
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client.refreshOwner();
    await vi.advanceTimersByTimeAsync(0);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('ignores late requests after clearSession and does not leak old account events', async () => {
    vi.useFakeTimers();
    let ownerID = 7;
    let resolveOldRequest: ((value: unknown) => void) | undefined;
    let nextEventID = 0;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `session-${++nextEventID}`) });
    mocks.post.mockImplementationOnce(() => new Promise(resolve => { resolveOldRequest = resolve; }));
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => ownerID);
    client.start();
    client.recordClick(42, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    client.clearSession();
    client.recordReadEnd(43, { ...tracking, request_id: 'new-session' }, readPayload);
    resolveOldRequest?.({ data: responseFor(eventsFromPost(0)) });
    await flushMicrotasks();
    await vi.advanceTimersByTimeAsync(0);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)).toHaveLength(1);
    expect(eventsFromPost(1)[0].event_type).toBe('read_end');

    ownerID = 8;
    client.refreshOwner();
    await flushMicrotasks();
    expect(savedRecommendationSnapshot().ownerUserId).toBe(8);
    expect(savedRecommendationSnapshot().events).toEqual([]);
  });

  it('drops old-owner events and ignores an in-flight response after account switch', async () => {
    vi.useFakeTimers();
    let ownerID = 7;
    let nextEventID = 0;
    let resolveOldRequest: ((value: unknown) => void) | undefined;
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => `owner-switch-${++nextEventID}`) });
    mocks.post.mockImplementationOnce(() => new Promise(resolve => { resolveOldRequest = resolve; }));
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => ownerID);
    client.start();
    client.recordClick(42, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    ownerID = 8;
    client.refreshOwner();
    client.recordReadEnd(43, { ...tracking, request_id: 'new-owner-event' }, readPayload);
    resolveOldRequest?.({ data: responseFor(eventsFromPost(0)) });
    await flushMicrotasks();
    await vi.advanceTimersByTimeAsync(0);
    await flushMicrotasks();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(eventsFromPost(1)).toHaveLength(1);
    expect(eventsFromPost(1)[0].event_type).toBe('read_end');
    expect(savedRecommendationSnapshot().ownerUserId).toBe(8);
  });

  it('does not let an older Axios success clear a newer keepalive failure cooldown', async () => {
    let resolveNormal: ((value: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise(resolve => {
      resolveNormal = resolve;
    }));
    mocks.fetch.mockResolvedValueOnce({
      ok: false,
      status: 429,
      headers: new Headers({ 'Retry-After': '60' }),
      json: async () => ({}),
    });
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();
    client.recordClick(42, tracking);
    await flushMicrotasks();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    client.recordReadEnd(43, {
      ...tracking,
      request_id: 'parallel-read-end',
      token: 'v2.parallel-read-end',
    }, readPayload);
    await client.flush(true);
    resolveNormal?.({ data: responseFor(eventsFromPost(0)) });
    await flushMicrotasks();
    await client.flush(false);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.fetch).toHaveBeenCalledTimes(1);
  });

  it('keeps route-leave delivery on the normal Axios flush path', async () => {
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token', () => 7);
    client.start();

    client.recordReadEnd(42, tracking, readPayload);
    expect(mocks.post).not.toHaveBeenCalled();
    await client.flush(false);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][0]).toBe('/recommendation-events');
    expect(mocks.fetch).not.toHaveBeenCalled();
  });
});

