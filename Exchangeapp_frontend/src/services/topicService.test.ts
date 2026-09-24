import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getTopicPosts, getTopics } from './topicService';

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('../axios', () => ({ default: { get: mocks.get } }));

describe('topicService', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.get.mockResolvedValue({ data: { items: [] } });
  });

  it('loads the public topic summaries', async () => {
    const data = { items: [{ slug: 'japan', label: 'Japan', description: 'Life in Japan' }] };
    mocks.get.mockResolvedValueOnce({ data });

    await expect(getTopics()).resolves.toEqual(data);
    expect(mocks.get).toHaveBeenCalledWith('/topics');
  });

  it('uses the topic posts route and forwards pagination parameters', async () => {
    const data = { topic: { slug: 'ai', label: 'AI', description: 'Models' }, items: [], next_cursor: 'next' };
    mocks.get.mockResolvedValueOnce({ data });

    await expect(getTopicPosts('ai', { limit: 20, cursor: 'next-token' })).resolves.toEqual(data);
    expect(mocks.get).toHaveBeenCalledWith('/topics/ai/posts', {
      params: { limit: 20, cursor: 'next-token' },
    });
  });

  it('encodes the slug as a path segment', async () => {
    await getTopicPosts('japan/extra');

    expect(mocks.get).toHaveBeenCalledWith('/topics/japan%2Fextra/posts', { params: {} });
  });
});
