// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ArticleViewTelemetryClient,
  resetArticleViewTelemetryForTests,
} from './articleViewTelemetry';

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
  article_id: index,
  occurred_at: '2026-08-16T00:00:' + String(index % 60).padStart(2, '0') + '.000Z',
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

describe('ArticleViewTelemetryClient', () => {
  let client: ArticleViewTelemetryClient;
  let currentUserID = 7;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllTimers();
    vi.resetAllMocks();
    sessionStorage.clear();
    client = new ArticleViewTelemetryClient(() => currentUserID);
    client.start();
  });

  afterEach(() => {
    client.stop();
    resetArticleViewTelemetryForTests();
    sessionStorage.clear();
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('retains network failures and schedules the first retry', async () => {
    mocks.post.mockRejectedValueOnce(new Error('offline'));

    client.enqueue(42, event(1).event_id, event(1).occurred_at);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toHaveLength(1);
  });

  it('preserves event identity and occurred_at on retry', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    await vi.advanceTimersByTimeAsync(1000);
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1]).toEqual({
      events: [{
        event_id: first.event_id,
        article_id: first.article_id,
        occurred_at: first.occurred_at,
      }],
    });
  });

  it('follows 1s, 2s, 5s, 10s, 30s, then 60s retry delays', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
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
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
    await settle();

    mocks.post.mockResolvedValueOnce(successResponse([first]));
    await vi.advanceTimersByTimeAsync(1000);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toEqual([]);

    const second = event(2);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(second.article_id, second.event_id, second.occurred_at);
    await settle();
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.post).toHaveBeenCalledTimes(3);
  });

  it('does not retry a 429 before Retry-After seconds', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 429, headers: { 'retry-after': '60' } },
    });
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
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
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
    await settle();

    const second = event(2);
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    await vi.advanceTimersByTimeAsync(1000);
    await settle();
    mocks.post.mockRejectedValueOnce({ response: { status: 400 } });
    client.enqueue(second.article_id, second.event_id, second.occurred_at);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toEqual([]);
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
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toEqual([]);
  });

  it('drops a 422 batch with no usable results', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce({
      response: { status: 422, data: { accepted: 0, rejected: 0, results: [] } },
    });
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
    await settle();

    expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toEqual([]);
  });

  it('drops permanent 4xx responses without retrying forever', async () => {
    for (const status of [400, 401, 403, 404, 413, 499]) {
      const item = event(status);
      mocks.post.mockRejectedValueOnce({ response: { status } });
      client.enqueue(item.article_id, item.event_id, item.occurred_at);
      await settle();
      expect(JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]')).toEqual([]);
    }
  });
  it('cancels a retry timer and flushes immediately when online', async () => {
    const first = event(1);
    mocks.post.mockRejectedValueOnce(new Error('offline'));
    client.enqueue(first.article_id, first.event_id, first.occurred_at);
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

    client.enqueue(first.article_id, first.event_id, first.occurred_at);
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
    sessionStorage.setItem('article_view_telemetry_queue_v1', JSON.stringify(pending));
    const restored = new ArticleViewTelemetryClient(() => currentUserID);
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
    sessionStorage.setItem('article_view_telemetry_queue_v1', JSON.stringify([queued]));
    currentUserID = 8;
    const otherClient = new ArticleViewTelemetryClient(() => currentUserID);
    otherClient.start();
    await settle();
    expect(mocks.post).not.toHaveBeenCalled();

    otherClient.stop();
    currentUserID = 7;
    const matchingClient = new ArticleViewTelemetryClient(() => currentUserID);
    mocks.post.mockResolvedValueOnce(successResponse([queued]));
    matchingClient.start();
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][1].events[0]).toEqual({
      event_id: queued.event_id,
      article_id: queued.article_id,
      occurred_at: queued.occurred_at,
    });
    expect(mocks.post.mock.calls[0][1].events[0]).not.toHaveProperty('owner_user_id');
    matchingClient.stop();
  });

  it('drops malformed persisted records and caps the restored queue at 200', async () => {
    const persisted = [
      { bad: true },
      ...Array.from({ length: 205 }, (_, index) => event(index + 1)),
    ];
    sessionStorage.setItem('article_view_telemetry_queue_v1', JSON.stringify(persisted));
    const restored = new ArticleViewTelemetryClient(() => null);
    restored.start();
    await settle();

    const saved = JSON.parse(sessionStorage.getItem('article_view_telemetry_queue_v1') || '[]');
    expect(saved).toHaveLength(200);
    expect(saved.filter((item: { owner_user_id?: number }) => item.owner_user_id === 7)).toHaveLength(200);
    restored.stop();
  });
});