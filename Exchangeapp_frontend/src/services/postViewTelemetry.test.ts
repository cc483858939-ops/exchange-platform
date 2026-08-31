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

const settle = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

describe('PostViewTelemetryClient', () => {
  let client: PostViewTelemetryClient;
  let currentUserID: number | null = 7;

  beforeEach(() => {
    currentUserID = 7;
    vi.useFakeTimers();
    vi.clearAllTimers();
    vi.resetAllMocks();
    sessionStorage.clear();
    client = new PostViewTelemetryClient(() => currentUserID);
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
    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toHaveLength(1);
  });

  it('deduplicates compatible event IDs and rejects conflicting identities', async () => {
    const first = event(1);
    mocks.post.mockRejectedValue(new Error('offline'));

    expect(client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at)).toBe(true);
    await settle();
    const saved = JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]');

    expect(client.enqueue(first.post_id, first.event_id, 'post_detail', '2026-08-17T00:00:00.000Z')).toBe(true);
    expect(client.enqueue(first.post_id + 1, first.event_id, 'post_detail')).toBe(false);
    expect(client.enqueue(first.post_id, first.event_id, 'feed')).toBe(false);
    currentUserID = 8;
    expect(client.enqueue(first.post_id, first.event_id, 'post_detail')).toBe(false);

    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual(saved);
  });

  it('retains network failures and schedules the first retry', async () => {
    mocks.post.mockRejectedValueOnce(new Error('offline'));

    client.enqueue(42, event(1).event_id, 'post_detail', event(1).occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toHaveLength(1);
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

  it('follows 1s, 2s, 5s, 10s, 30s, then 60s retry delays', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    const delays = [1000, 2000, 5000, 10000, 30000, 60000];
    for (const delay of delays) {
      mocks.post.mockRejectedValueOnce(new Error('offline'));
      await vi.advanceTimersByTimeAsync(delay);
      await settle();
    }

    expect(mocks.post).toHaveBeenCalledTimes(7);
    await vi.advanceTimersByTimeAsync(60000);
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

    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);

    const second = event(2);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).toHaveBeenCalledTimes(3);
  });

  it('does not retry a 429 before Retry-After seconds', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    await vi.advanceTimersByTimeAsync(59999);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });

  it('uses normal retry for 503 and drops permanent 4xx batches', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({ response: { status: 503 } });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    const second = event(2);
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    await vi.advanceTimersByTimeAsync(1000);
    await settle();
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    client.enqueue(second.post_id, second.event_id, 'post_detail', second.occurred_at);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);
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

    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);
  });

  it('drops a 422 batch with no usable results', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 422, data: { accepted: 0, rejected: 0, results: [] } },
    });
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);
  });

  it('drops permanent 4xx responses without retrying forever', async () => {
    for (const status of [400, 401, 403, 404, 413, 499]) {
      const item = event(status);
      mocks.post.mockRejectedValueOnce({ response: { status } });
      client.enqueue(item.post_id, item.event_id, 'post_detail', item.occurred_at);
      await settle();
      expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);
    }
  });
  it('cancels a retry timer and flushes immediately when online', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.post_id, first.event_id, 'post_detail', first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    window.dispatchEvent(new Event('online'));
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
    sessionStorage.setItem('post_view_telemetry_queue_v1', JSON.stringify(pending));
    const restored = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) =>
      successResponse(body.events));
    restored.start();
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[0][1].events).toHaveLength(50);
    expect(mocks.post.mock.calls[1][1].events).toHaveLength(1);
    expect(mocks.post.mock.calls[0][1].events[0]).not.toHaveProperty('owner_user_id');
    expect(mocks.post.mock.calls[1][1].events[0]).not.toHaveProperty('owner_user_id');
    restored.stop();
  });
  it('never sends a queued event under another authenticated user', async () => {
    const queued = event(1, 7);
    sessionStorage.setItem('post_view_telemetry_queue_v1', JSON.stringify([queued]));
    currentUserID = 8;
    const otherClient = new PostViewTelemetryClient(() => currentUserID);
    otherClient.start();
    await settle();
    expect(mocks.post).not.toHaveBeenCalled();

    otherClient.stop();
    currentUserID = 7;
    const matchingClient = new PostViewTelemetryClient(() => currentUserID);
    mocks.post.mockResolvedValueOnce(successResponse([queued]));
    matchingClient.start();
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events[0]).toEqual({
      event_id: queued.event_id,
      post_id: queued.post_id,
      occurred_at: queued.occurred_at,
      source: 'post_detail',
    });
    expect(mocks.post.mock.calls[0][1].events[0]).not.toHaveProperty('owner_user_id');
    matchingClient.stop();
  });

  it('drops malformed persisted records and caps the restored queue at 200', async () => {
    const persisted = [
      { bad: true },
      ...Array.from({ length: 205 }, (_, index) => event(index + 1)),
    ];
    sessionStorage.setItem('post_view_telemetry_queue_v1', JSON.stringify(persisted));
    const restored = new PostViewTelemetryClient(() => null);
    restored.start();
    await settle();

    const saved = JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]');
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
      feedClient.observeFeedCard(card, 47);
      observers[0].emit(card, 0.8);
      await vi.advanceTimersByTimeAsync(1000);
      await settle();

      expect(mocks.post).not.toHaveBeenCalled();
      expect(JSON.parse(sessionStorage.getItem('post_view_telemetry_queue_v1') || '[]')).toEqual([]);

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

