import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getPostEngagementStates } from './engagementService';

const apiClient = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('../axios', () => ({ default: apiClient }));

describe('engagementService', () => {
  beforeEach(() => apiClient.post.mockReset());

  it('does not send a request for an empty input', async () => {
    await expect(getPostEngagementStates([])).resolves.toEqual({ items: [] });
    expect(apiClient.post).not.toHaveBeenCalled();
  });

  it('deduplicates IDs while preserving their first occurrence order', async () => {
    const item = {
      post_id: 1,
      like: { status: 'ready', likes: 2, liked: true },
      repost: { status: 'ready', reposts: 1, reposted: false },
      bookmark: { status: 'unavailable' },
    };
    apiClient.post.mockResolvedValue({ data: { items: [item] } });

    await expect(getPostEngagementStates([1, 1, 2])).resolves.toEqual({ items: [item] });
    expect(apiClient.post).toHaveBeenCalledTimes(1);
    expect(apiClient.post).toHaveBeenCalledWith('/posts/engagement-states', { post_ids: [1, 2] });
  });

  it('uses one request for up to 100 unique IDs', async () => {
    apiClient.post.mockResolvedValue({ data: { items: [] } });

    await getPostEngagementStates(Array.from({ length: 100 }, (_, index) => index + 1));

    expect(apiClient.post).toHaveBeenCalledTimes(1);
    expect(apiClient.post.mock.calls[0][0]).toBe('/posts/engagement-states');
    expect(apiClient.post.mock.calls[0][1].post_ids).toHaveLength(100);
  });

  it('chunks 101 unique IDs into two engagement requests', async () => {
    apiClient.post
      .mockResolvedValueOnce({ data: { items: [{ post_id: 1, like: { status: 'unavailable' }, repost: { status: 'unavailable' }, bookmark: { status: 'unavailable' } }] } })
      .mockResolvedValueOnce({ data: { items: [{ post_id: 101, like: { status: 'ready', likes: 0, liked: false }, repost: { status: 'ready', reposts: 0, reposted: false }, bookmark: { status: 'ready', bookmarked: false } }] } });

    await expect(getPostEngagementStates(Array.from({ length: 101 }, (_, index) => index + 1))).resolves.toEqual({
      items: [
        { post_id: 1, like: { status: 'unavailable' }, repost: { status: 'unavailable' }, bookmark: { status: 'unavailable' } },
        { post_id: 101, like: { status: 'ready', likes: 0, liked: false }, repost: { status: 'ready', reposts: 0, reposted: false }, bookmark: { status: 'ready', bookmarked: false } },
      ],
    });
    expect(apiClient.post).toHaveBeenCalledTimes(2);
    expect(apiClient.post.mock.calls[0][1].post_ids).toHaveLength(100);
    expect(apiClient.post.mock.calls[1][1].post_ids).toEqual([101]);
  });
});
