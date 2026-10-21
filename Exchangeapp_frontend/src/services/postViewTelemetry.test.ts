// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  PostViewTelemetryClient,
  resetPostViewTelemetryForTests,
} from './postViewTelemetry';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

const event = (index = 1, owner = 7) => ({
  owner_user_id: owner,
  event_id: '00000000-0000-4000-8000-' + String(index).padStart(12, '0'),
  post_id: index,
  occurred_at: '2026-08-16T00:00:' + String(index % 60).padStart(2, '0') + '.000Z',
  source: 'post_detail' as const,
});

const successResponse = (events: Array<{ event_id: string }>) => ({
  status: 202,
  data: {
    accepted: events.length,
    rejected: 0,
    results: events.map(item => ({ event_id: item.event_id, status: 'accepted' })),
  },
});
const postViewStorageKey = 'post_view_telemetry_snapshot_v2';
const savedPostViewSnapshot = () => JSON.parse(sessionStorage.getItem(postViewStorageKey) || '{"events":[]}');
const savedPostViewEvents = () => savedPostViewSnapshot().events;
const writePostViewSnapshot = (
  events: unknown[],
  ownerUserId: number | null = 7,
  retry = { retryNotBeforeEpochMs: 0, retryAttempt: 0 },
) => sessionStorage.setItem(postViewStorageKey, JSON.stringify({ events, retry, ownerUserId }));

const settle = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

describe('PostViewTelemetryClient', () => {
  let client: PostViewTelemetryClient;
  let currentUserID: number | null = 7;
  let authStateKey: string | null = 'token-v1';

  beforeEach(() => {
    currentUserID = 7;
    authStateKey = 'token-v1';
    vi.useFakeTimers();
    vi.clearAllTimers();
    vi.resetAllMocks();
    vi.spyOn(Math, 'random').mockReturnValue(0);
    sessionStorage.clear();
    client = new PostViewTelemetryClient(() => currentUserID, () => authStateKey);
    client.start();
  });

  afterEach(() => {
    client.stop();
    resetPostViewTelemetryForTests();
    sessionStorage.clear();
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('returns queue acceptance for valid and invalid events', async () => {
    const first = event(1);
    mocks.post.mockRejectedValue(new Error('offline'));

    expect(client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at)).toBe(true);
    await settle();

    expect(client.enqueue(0, event(2).event_id, 'post_detail')).toBe(false);
    expect(client.enqueue(42, '', 'post_detail')).toBe(false);
    currentUserID = null;
    expect(client.enqueue(42, event(3).event_id, 'post_detail')).toBe(false);
    currentUserID = 7;
    expect(savedPostViewEvents()).toHaveLength(1);
  });

  it('deduplicates within an owner and clears old-owner events after account switch', async () => {
    const first = event(1);
    mocks.post.mockRejectedValue(new Error('offline'));

    expect(client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at)).toBe(true);
    await settle();
    const saved = savedPostViewEvents();

    expect(client.enqueue(first.post_id, first.event_id, 'post_detail', '2026-08-17T00:00:00.000Z')).toBe(true);
    expect(client.enqueue(first.post_id + 1, first.event_id, 'post_detail')).toBe(false);
    expect(client.enqueue(first.post_id, first.event_id, 'feed')).toBe(false);
    currentUserID = 8;
    expect(client.enqueue(first.post_id, first.event_id, 'post_detail')).toBe(true);
    await settle();
    expect(savedPostViewEvents()).not.toEqual(saved);
    expect(savedPostViewEvents()).toEqual([
      expect.objectContaining({ event_id: first.event_id, owner_user_id: 8 }),
    ]);
  });

  it('retains network failures and schedules the first retry', async () => {
    mocks.post.mockRejectedValueOnce(new Error('offline'));

    client.enqueue(42, event(1).event_id, 'post_detail', event(1).occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedPostViewEvents()).toHaveLength(1);
  });

  it('preserves event identity and occurred_at on retry', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    await vi.advanceTimersByTimeAsync(1000);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1]).toEqual({
      events: [{
        event_id: first.event_id,
        post_id: first.post_id,
        occurred_at: first.occurred_at,
        source: 'post_detail',
      }],
    });
  });

  it('uses bounded equal-jitter exponential retry windows', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    const delays = [1000, 2000, 4000, 8000, 16000, 32000];
    for (const delay of delays) {
      mocks.post.mockRejectedValueOnce(new Error('offline'));
      await vi.advanceTimersByTimeAsync(delay);
      await settle();
    }

    expect(mocks.post).toHaveBeenCalledTimes(7);
    await vi.advanceTimersByTimeAsync(64000);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(8);
  });

  it('resets retry state and removes the queue entry after success', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    await vi.advanceTimersByTimeAsync(1000);
    await settle();

    expect(savedPostViewEvents()).toEqual([]);

    const second = event(2);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).toHaveBeenCalledTimes(3);
  });

  it('does not retry a 429 before Retry-After seconds', async () => {
    const first = event(1);
    const second = event(2);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first, second]));
    expect(client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at)).toBe(true);
    window.dispatchEvent(new Event('online'));
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    window.dispatchEvent(new Event('pagehide'));
    expect(savedPostViewSnapshot().events).toHaveLength(2);
    expect(savedPostViewSnapshot().retry.retryNotBeforeEpochMs).toBeGreaterThan(Date.now());

    client.stop();
    client = new PostViewTelemetryClient(() => currentUserID, () => authStateKey);
    client.start();
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(59999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('does not carry an account cooldown or queued events into the next owner', async () => {
    const ownerAEvent = event(71, 7);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(ownerAEvent.post_id, ownerAEvent.event_id, 'post_detail', ownerAEvent.occurred_at);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    currentUserID = 8;
    authStateKey = 'token-owner-b';
    const ownerBEvent = event(72, 8);
    mocks.post.mockResolvedValueOnce(successResponse([ownerBEvent]));
    client.enqueue(ownerBEvent.post_id, ownerBEvent.event_id, 'post_detail', ownerBEvent.occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([ownerBEvent.event_id]);
    expect(savedPostViewSnapshot().ownerUserId).toBe(8);
    expect(savedPostViewEvents().some((item: { owner_user_id: number }) => item.owner_user_id === 7)).toBe(false);
  });

  it('preserves Retry-After when the same owner refreshes the token', async () => {
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    const first = event(75);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    authStateKey = 'token-v2';
    client.refreshAuthState();
    const second = event(76);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(59_999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([first.event_id, second.event_id]);
  });

  it('preserves Retry-After when identity is temporarily unavailable and returns to the same owner', async () => {
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    const first = event(77);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    currentUserID = null;
    client.refreshAuthState();
    await vi.advanceTimersByTimeAsync(30_000);
    currentUserID = 7;
    client.refreshAuthState();
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    await vi.advanceTimersByTimeAsync(29_999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events[0].event_id).toBe(first.event_id);
  });

  it('does not inherit an owner cooldown after A becomes unavailable and B signs in', async () => {
    const first = event(78);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    currentUserID = null;
    client.refreshAuthState();
    currentUserID = 8;
    authStateKey = 'token-owner-b';
    client.refreshAuthState();
    const second = event(79, 8);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([second.event_id]);
    expect(savedPostViewSnapshot().ownerUserId).toBe(8);
  });

  it('discards another owner snapshot and cooldown before sending for the current owner', async () => {
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    client.stop();
    const ownerAEvent = event(80, 7);
    writePostViewSnapshot([ownerAEvent], 7, {
      retryNotBeforeEpochMs: Date.now() + 60_000,
      retryAttempt: 3,
    });
    currentUserID = 8;
    authStateKey = 'token-owner-b';
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client = new PostViewTelemetryClient(() => currentUserID, () => authStateKey);
    client.start();
    const ownerBEvent = event(81, 8);
    client.enqueue(ownerBEvent.post_id, ownerBEvent.event_id, 'post_detail', ownerBEvent.occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([ownerBEvent.event_id]);
    expect(savedPostViewSnapshot()).toMatchObject({ ownerUserId: 8, retry: { retryNotBeforeEpochMs: 0, retryAttempt: 0 } });
    expect(savedPostViewEvents()).toEqual([]);
  });

  it('restores the same owner cooldown and sends only after its deadline', async () => {
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    client.stop();
    const restored = event(82, 7);
    writePostViewSnapshot([restored], 7, {
      retryNotBeforeEpochMs: Date.now() + 60_000,
      retryAttempt: 3,
    });
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client = new PostViewTelemetryClient(() => currentUserID, () => authStateKey);
    client.start();
    await settle();
    expect(mocks.post).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(59_999);
    expect(mocks.post).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events[0].event_id).toBe(restored.event_id);
  });

  it('retries only current-owner events when an old request fails after an account switch', async () => {
    let rejectOldRequest: ((reason: unknown) => void) | undefined;
    const ownerAEvent = event(83, 7);
    mocks.post.mockImplementationOnce(() => new Promise((_resolve, reject) => {
      rejectOldRequest = reject;
    }));
    client.enqueue(ownerAEvent.post_id, ownerAEvent.event_id, 'post_detail', ownerAEvent.occurred_at);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    currentUserID = 8;
    authStateKey = 'token-owner-b';
    client.refreshAuthState();
    const ownerBEvent = event(84, 8);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.enqueue(ownerBEvent.post_id, ownerBEvent.event_id, 'post_detail', ownerBEvent.occurred_at);
    rejectOldRequest?.({
      response: { status: 401 },
      config: { headers: { Authorization: 'token-v1' }, _authSessionVersion: 0 },
    });
    await settle();
    await vi.advanceTimersByTimeAsync(0);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([ownerBEvent.event_id]);
    expect(savedPostViewSnapshot().ownerUserId).toBe(8);
    expect(savedPostViewEvents()).toEqual([]);
    expect(savedPostViewSnapshot().retry.retryNotBeforeEpochMs).toBe(0);
  });

  it('does not send newly enqueued events while stopped, even when flush is called directly', async () => {
    client.stop();
    const queued = event(73);

    expect(client.enqueue(queued.post_id, queued.event_id, 'post_detail', queued.occurred_at)).toBe(true);
    await client.flush();
    client.refreshAuthState();
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(300_000);

    expect(mocks.post).not.toHaveBeenCalled();
    expect(savedPostViewEvents().map((item: { event_id: string }) => item.event_id)).toEqual([queued.event_id]);
  });

  it('resumes delivery after stop and start', async () => {
    client.stop();
    const queued = event(85);
    client.enqueue(queued.post_id, queued.event_id, 'post_detail', queued.occurred_at);
    await settle();
    expect(mocks.post).not.toHaveBeenCalled();

    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.start();
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events[0].event_id).toBe(queued.event_id);
  });

  it('does not drain a second batch after stop while the first request is in flight', async () => {
    let resolveFirst: ((value: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise(resolve => {
      resolveFirst = resolve;
    }));
    const first = event(88);
    const second = event(89);
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    client.stop();
    resolveFirst?.(successResponse([first]));
    await settle();
    await vi.advanceTimersByTimeAsync(300_000);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedPostViewEvents().map((item: { event_id: string }) => item.event_id)).toEqual([second.event_id]);

    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.start();
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events[0].event_id).toBe(second.event_id);
  });

  it('does not let stop and start bypass an unexpired cooldown', async () => {
    vi.setSystemTime(new Date('2026-10-21T00:00:00.000Z'));
    const first = event(86);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();
    client.stop();

    const second = event(87);
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    client.start();
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(59_999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events.map((item: { event_id: string }) => item.event_id))
      .toEqual([first.event_id, second.event_id]);
  });

  it.each([401, 403])('does not pause a refreshed token after an old token returns %i', async status => {
    let rejectOldRequest: ((reason: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise((_resolve, reject) => {
      rejectOldRequest = reject;
    }));
    client.enqueue(74, event(74).event_id, 'post_detail');
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    authStateKey = 'token-v2';
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    rejectOldRequest?.({
      response: { status },
      config: { headers: { Authorization: 'token-v1' }, _authSessionVersion: 0 },
    });
    await settle();
    await vi.advanceTimersByTimeAsync(0);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1].events[0].event_id).toBe(event(74).event_id);
  });

  it('uses normal retry for 503 and drops permanent 4xx batches', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 503, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    const second = event(2);
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    await vi.advanceTimersByTimeAsync(59999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();

    expect(savedPostViewEvents()).toEqual([]);
  });

  it('does not let 100 queued events or a 2xx response without progress bypass cooldown', async () => {
    const first = event(1);
    mocks.post.mockResolvedValueOnce({
      status: 202,
      data: { accepted: 0, rejected: 0, results: [{ event_id: 'not-in-batch', status: 'accepted' }] },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedPostViewSnapshot().retry.retryNotBeforeEpochMs).toBeGreaterThan(Date.now());

    for (let index = 2; index <= 101; index += 1) {
      const next = event(index);
      client.enqueue(next.post_id, next.event_id, 'post_detail', next.occurred_at);
    }
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedPostViewEvents()).toHaveLength(101);
  });

  it('removes 422 result IDs without retrying rejected events', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: {
        status: 422,
        data: {
          accepted: 0,
          rejected: 1,
          results: [{ event_id: first.event_id, status: 'rejected' }],
        },
      },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    expect(savedPostViewEvents()).toEqual([]);
  });

  it('drops a 422 batch with no usable results', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 422, data: { accepted: 0, rejected: 0, results: [] } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    expect(savedPostViewEvents()).toEqual([]);
  });

  it('drops permanent 4xx responses without retrying forever', async () => {
    for (const status of [400, 404, 413, 499]) {
      const item = event(status);
      mocks.post.mockRejectedValueOnce({ response: { status } });
      client.enqueue(item.post_id, item.event_id, 'post_detail', item.occurred_at);
      await settle();
      expect(savedPostViewEvents()).toEqual([]);
    }
  });

  it.each([401, 403])('pauses a %i response until the authentication state changes', async status => {
    const first = event(status);
    mocks.post.mockRejectedValueOnce({ response: { status } });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(savedPostViewEvents()).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(300_000);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    authStateKey = 'token-after-auth-change';
    const second = event(status + 1);
    mocks.post.mockResolvedValueOnce(successResponse([first, second]));
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(savedPostViewEvents()).toEqual([]);
  });
  it('does not let online bypass a Retry-After cooldown', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    window.dispatchEvent(new Event('online'));
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(59999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('keeps only one HTTP request in flight for concurrent flush calls', async () => {
    const first = event(1);
    let resolveRequest: (value: unknown) => void = () => undefined;
    mocks.post.mockReturnValueOnce(new Promise(resolve => {
      resolveRequest = resolve;
    }));

    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();
    void client.flush();
    void client.flush();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    resolveRequest(successResponse([first]));
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);
  });

  it('splits more than 50 current-user events without mixing owners', async () => {
    const pending = Array.from({ length: 51 }, (_, index) => event(index + 1));
    writePostViewSnapshot(pending);
    const restored = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    restored.start();
    await settle();
    await vi.advanceTimersByTimeAsync(0);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[0][1].events).toHaveLength(50);
    expect(mocks.post.mock.calls[1][1].events).toHaveLength(1);
    expect(mocks.post.mock.calls[0][1].events[0]).not.toHaveProperty('owner_user_id');
    expect(mocks.post.mock.calls[1][1].events[0]).not.toHaveProperty('owner_user_id');
    restored.stop();
  });
  it('drops a snapshot from another owner instead of later sending it under the old owner', async () => {
    const queued = event(1, 7);
    writePostViewSnapshot([queued]);
    currentUserID = 8;
    const otherClient = new PostViewTelemetryClient(() => currentUserID);
    otherClient.start();
    await settle();
    expect(mocks.post).not.toHaveBeenCalled();
    expect(savedPostViewSnapshot()).toMatchObject({ ownerUserId: 8, events: [] });

    otherClient.stop();
    currentUserID = 7;
    const matchingClient = new PostViewTelemetryClient(() => currentUserID);
    matchingClient.start();
    await settle();

    expect(mocks.post).not.toHaveBeenCalled();
    expect(savedPostViewSnapshot()).toMatchObject({ ownerUserId: 7, events: [] });
    matchingClient.stop();
  });

  it('drops malformed persisted records and caps the restored queue at 200', async () => {
    const persisted = [
      { bad: true },
      ...Array.from({ length: 205 }, (_, index) => event(index + 1)),
    ];
    writePostViewSnapshot(persisted);
    const restored = new PostViewTelemetryClient(() => null);
    restored.start();
    await settle();

    const saved = savedPostViewEvents();
    expect(saved).toHaveLength(200);
    expect(saved.filter((item: { owner_user_id?: number }) => item.owner_user_id === 7)).toHaveLength(200);
    restored.stop();
  });
  it('qualifies a visible feed card once and preserves source', async () => {
    const originalObserver = globalThis.IntersectionObserver;
    const observers: FakeIntersectionObserver[] = [];
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
      emit(target: HTMLElement, ratio: number): void {
        this.callback(
          [{ target, intersectionRatio: ratio } as unknown as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }
    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: FakeIntersectionObserver,
    });

    const feedClient = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    feedClient.start();
    const card = document.createElement('article');
    feedClient.observeFeedCard(card, 42);
    observers[0].emit(card, 0.49);
    await vi.advanceTimersByTimeAsync(1500);
    expect(mocks.post).not.toHaveBeenCalled();

    observers[0].emit(card, 0.5);
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events[0]).toMatchObject({
      post_id: 42,
      source: 'feed',
    });

    observers[0].emit(card, 0.9);
    await vi.advanceTimersByTimeAsync(1000);
    expect(mocks.post).toHaveBeenCalledTimes(1);

    feedClient.stop();
    if (originalObserver) {
      Object.defineProperty(globalThis, 'IntersectionObserver', {
        configurable: true,
        writable: true,
        value: originalObserver,
      });
    } else {
      delete (globalThis as Partial<typeof globalThis>).IntersectionObserver;
    }
  });

  it('deduplicates virtual remounts within a Home session and permits explicit new sessions', async () => {
    const originalObserver = globalThis.IntersectionObserver;
    const observers: FakeIntersectionObserver[] = [];
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
      emit(target: HTMLElement, ratio: number): void {
        this.callback(
          [{ target, intersectionRatio: ratio } as unknown as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }

    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: FakeIntersectionObserver,
    });

    const feedClient = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    feedClient.start();

    const qualify = async (card: HTMLElement, sessionKey?: string) => {
      feedClient.observeFeedCard(card, 42, sessionKey);
      observers[0].emit(card, 0.8);
      await vi.advanceTimersByTimeAsync(1000);
      await settle();
    };

    try {
      const firstSession = 'home:7:for-you:1';
      const firstCard = document.createElement('article');
      await qualify(firstCard, firstSession);
      expect(mocks.post).toHaveBeenCalledTimes(1);

      feedClient.unobserveFeedCard(firstCard);
      const remountedCard = document.createElement('article');
      await qualify(remountedCard, firstSession);
      expect(mocks.post).toHaveBeenCalledTimes(1);

      const refreshedSession = 'home:7:for-you:2';
      const refreshedCard = document.createElement('article');
      await qualify(refreshedCard, refreshedSession);
      expect(mocks.post).toHaveBeenCalledTimes(2);

      feedClient.unobserveFeedCard(refreshedCard);
      feedClient.releaseFeedViewSession(refreshedSession);
      const releasedCard = document.createElement('article');
      await qualify(releasedCard, refreshedSession);
      expect(mocks.post).toHaveBeenCalledTimes(3);

      feedClient.unobserveFeedCard(releasedCard);
      const legacyCard = document.createElement('article');
      await qualify(legacyCard);
      expect(mocks.post).toHaveBeenCalledTimes(4);
    } finally {
      feedClient.stop();
      if (originalObserver) {
        Object.defineProperty(globalThis, 'IntersectionObserver', {
          configurable: true,
          writable: true,
          value: originalObserver,
        });
      } else {
        delete (globalThis as Partial<typeof globalThis>).IntersectionObserver;
      }
    }
  });

  it('cancels hidden-tab timers and starts a fresh interval when visible', async () => {
    const originalObserver = globalThis.IntersectionObserver;
    const observers: FakeIntersectionObserver[] = [];
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
      emit(target: HTMLElement, ratio: number): void {
        this.callback(
          [{ target, intersectionRatio: ratio } as unknown as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }
    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: FakeIntersectionObserver,
    });
    const visibility = vi.spyOn(document, 'visibilityState', 'get');
    visibility.mockReturnValue('visible');
    const feedClient = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    feedClient.start();
    const card = document.createElement('article');
    feedClient.observeFeedCard(card, 43);
    observers[0].emit(card, 0.8);
    await vi.advanceTimersByTimeAsync(500);

    visibility.mockReturnValue('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(1000);
    expect(mocks.post).not.toHaveBeenCalled();

    visibility.mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(1);

    visibility.mockRestore();
    feedClient.stop();
    if (originalObserver) {
      Object.defineProperty(globalThis, 'IntersectionObserver', {
        configurable: true,
        writable: true,
        value: originalObserver,
      });
    } else {
      delete (globalThis as Partial<typeof globalThis>).IntersectionObserver;
    }
  });

  it('does not poison a feed observation after unauthenticated qualification', async () => {
    const originalObserver = globalThis.IntersectionObserver;
    const observers: FakeIntersectionObserver[] = [];
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
      emit(target: HTMLElement, ratio: number): void {
        this.callback(
          [{ target, intersectionRatio: ratio } as unknown as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }
    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: FakeIntersectionObserver,
    });
    const visibility = vi.spyOn(document, 'visibilityState', 'get');
    visibility.mockReturnValue('visible');
    let feedClient: PostViewTelemetryClient | null = null;

    try {
      currentUserID = null;
      feedClient = new PostViewTelemetryClient(() => currentUserID);
      mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
        successResponse(body.events));
      feedClient.start();
      const card = document.createElement('article');
      feedClient.observeFeedCard(card, 47, 'home:7:for-you:1');
      observers[0].emit(card, 0.8);
      await vi.advanceTimersByTimeAsync(1000);
      await settle();

      expect(mocks.post).not.toHaveBeenCalled();
      expect(savedPostViewEvents()).toEqual([]);

      currentUserID = 7;
      observers[0].emit(card, 0.49);
      observers[0].emit(card, 0.8);
      await vi.advanceTimersByTimeAsync(1000);
      await settle();

      expect(mocks.post).toHaveBeenCalledTimes(1);
      expect(mocks.post.mock.calls[0][1].events[0]).toMatchObject({
        post_id: 47,
        source: 'feed',
      });
    } finally {
      feedClient?.stop();
      visibility.mockRestore();
      currentUserID = 7;
      if (originalObserver) {
        Object.defineProperty(globalThis, 'IntersectionObserver', {
          configurable: true,
          writable: true,
          value: originalObserver,
        });
      } else {
        delete (globalThis as Partial<typeof globalThis>).IntersectionObserver;
      }
    }
  });

  it('fails closed for unavailable observers, unauthenticated users, and unobserved cards', async () => {
    const unavailable = new PostViewTelemetryClient(() => currentUserID);
    const card = document.createElement('article');
    unavailable.start();
    unavailable.observeFeedCard(card, 44);
    await vi.advanceTimersByTimeAsync(1500);
    expect(mocks.post).not.toHaveBeenCalled();
    unavailable.stop();

    const originalObserver = globalThis.IntersectionObserver;
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
      emit(target: HTMLElement, ratio: number): void {
        this.callback(
          [{ target, intersectionRatio: ratio } as unknown as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }
    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: FakeIntersectionObserver,
    });
    currentUserID = null;
    const unauthenticated = new PostViewTelemetryClient(() => currentUserID);
    unauthenticated.start();
    const observerCard = document.createElement('article');
    unauthenticated.observeFeedCard(observerCard, 45);
    unauthenticated.stop();

    currentUserID = 7;
    const unobserved = new PostViewTelemetryClient(() => currentUserID);
    unobserved.start();
    const removedCard = document.createElement('article');
    unobserved.observeFeedCard(removedCard, 46);
    unobserved.unobserveFeedCard(removedCard);
    await vi.advanceTimersByTimeAsync(1500);
    expect(mocks.post).not.toHaveBeenCalled();
    unobserved.stop();

    if (originalObserver) {
      Object.defineProperty(globalThis, 'IntersectionObserver', {
        configurable: true,
        writable: true,
        value: originalObserver,
      });
    } else {
      delete (globalThis as Partial<typeof globalThis>).IntersectionObserver;
    }
  });

});
