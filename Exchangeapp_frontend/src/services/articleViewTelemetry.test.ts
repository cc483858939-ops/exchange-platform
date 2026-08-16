import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { enqueueArticleView, flushArticleViewEvents } from './articleViewTelemetry';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

describe('article view telemetry queue', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(async () => {
    mocks.post.mockResolvedValue({ data: { accepted: 1, rejected: 0, results: [] } });
    await flushArticleViewEvents();
  });

  it('deduplicates concurrent enqueue calls by event ID', async () => {
    mocks.post.mockResolvedValue({ data: { accepted: 1, rejected: 0, results: [] } });

    await Promise.all([
      enqueueArticleView(42, '00000000-0000-4000-8000-000000000042', '2026-08-16T00:00:00.000Z'),
      enqueueArticleView(42, '00000000-0000-4000-8000-000000000042', '2026-08-16T00:01:00.000Z'),
    ]);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith('/article-view-events', {
      events: [{
        event_id: '00000000-0000-4000-8000-000000000042',
        article_id: 42,
        occurred_at: '2026-08-16T00:00:00.000Z',
      }],
    });
  });

  it('keeps the original event for a retry after publish failure', async () => {
    mocks.post.mockRejectedValueOnce(new Error('Kafka unavailable'));

    await enqueueArticleView(
      42,
      '00000000-0000-4000-8000-000000000042',
      '2026-08-16T00:00:00.000Z',
    );

    mocks.post.mockResolvedValueOnce({ data: { accepted: 1, rejected: 0, results: [] } });
    await enqueueArticleView(
      42,
      '00000000-0000-4000-8000-000000000042',
      '2026-08-16T00:02:00.000Z',
    );

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1][1]).toEqual({
      events: [{
        event_id: '00000000-0000-4000-8000-000000000042',
        article_id: 42,
        occurred_at: '2026-08-16T00:00:00.000Z',
      }],
    });
  });
});
