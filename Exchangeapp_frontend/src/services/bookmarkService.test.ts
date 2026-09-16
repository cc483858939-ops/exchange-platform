import { describe, expect, it, vi } from 'vitest';
import {
  bookmarkPost,
  getBookmarks,
  getPostBookmarkStates,
  unbookmarkPost,
} from './bookmarkService';

const apiClient = vi.hoisted(() => ({
  put: vi.fn(),
  delete: vi.fn(),
  post: vi.fn(),
  get: vi.fn(),
}));

vi.mock('../axios', () => ({ default: apiClient }));

describe('bookmarkService', () => {
  it('uses the canonical mutation endpoints', async () => {
    apiClient.put.mockResolvedValue({ data: { post_id: 42, bookmarked: true } });
    apiClient.delete.mockResolvedValue({ data: { post_id: 42, bookmarked: false } });

    await expect(bookmarkPost(42)).resolves.toEqual({ post_id: 42, bookmarked: true });
    await expect(unbookmarkPost(42)).resolves.toEqual({ post_id: 42, bookmarked: false });

    expect(apiClient.put).toHaveBeenCalledWith('/posts/42/bookmark');
    expect(apiClient.delete).toHaveBeenCalledWith('/posts/42/bookmark');
  });

  it('deduplicates IDs and chunks batch hydration at the backend limit', async () => {
    const ids = Array.from({ length: 101 }, (_, index) => index + 1);
    ids.push(1);
    apiClient.post
      .mockResolvedValueOnce({ data: { items: [{ post_id: 1, bookmarked: true }], unavailable_post_ids: [] } })
      .mockResolvedValueOnce({ data: { items: [{ post_id: 101, bookmarked: false }], unavailable_post_ids: [100] } });

    await expect(getPostBookmarkStates(ids)).resolves.toEqual({
      items: [
        { post_id: 1, bookmarked: true },
        { post_id: 101, bookmarked: false },
      ],
      unavailable_post_ids: [100],
    });
    expect(apiClient.post).toHaveBeenNthCalledWith(1, '/posts/bookmark-states', {
      post_ids: Array.from({ length: 100 }, (_, index) => index + 1),
    });
    expect(apiClient.post).toHaveBeenNthCalledWith(2, '/posts/bookmark-states', {
      post_ids: [101],
    });
  });

  it('passes history pagination through the authenticated API client', async () => {
    apiClient.get.mockResolvedValue({ data: { items: [], next_cursor: 'next' } });

    await expect(getBookmarks({ limit: 20, cursor: 'cursor' })).resolves.toEqual({
      items: [],
      next_cursor: 'next',
    });
    expect(apiClient.get).toHaveBeenCalledWith('/me/bookmarks', {
      params: { limit: 20, cursor: 'cursor' },
    });
  });
});
