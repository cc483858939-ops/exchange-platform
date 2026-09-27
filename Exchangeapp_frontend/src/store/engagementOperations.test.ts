import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
}));

vi.mock('../services/likeService', () => ({
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));
vi.mock('../services/repostService', () => ({
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));
vi.mock('../services/bookmarkService', () => ({
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));

import {
  createOptimisticBookmarkUpdate,
  createOptimisticLikeUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';

describe('engagement operations', () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('creates pure optimistic Like updates in both directions', () => {
    const unliked = { id: 1, liked: false, likeCount: 3 };
    const liked = { id: 2, liked: true, likeCount: 0 };

    expect(createOptimisticLikeUpdate(unliked)).toEqual({
      postId: 1, likes: 4, liked: true, status: 'ready',
    });
    expect(createOptimisticLikeUpdate(liked)).toEqual({
      postId: 2, likes: 0, liked: false, status: 'ready',
    });
    expect(unliked).toEqual({ id: 1, liked: false, likeCount: 3 });
    expect(liked).toEqual({ id: 2, liked: true, likeCount: 0 });
  });

  it('creates pure optimistic Repost updates without negative counts', () => {
    const notReposted = { id: 3, reposted: false, repostCount: 2 };
    const reposted = { id: 4, reposted: true, repostCount: 0 };

    expect(createOptimisticRepostUpdate(notReposted)).toEqual({
      postId: 3, reposts: 3, reposted: true, status: 'ready',
    });
    expect(createOptimisticRepostUpdate(reposted)).toEqual({
      postId: 4, reposts: 0, reposted: false, status: 'ready',
    });
    expect(notReposted).toEqual({ id: 3, reposted: false, repostCount: 2 });
    expect(reposted).toEqual({ id: 4, reposted: true, repostCount: 0 });
  });

  it('creates a pure optimistic Bookmark update', () => {
    const post = { id: 5, bookmarked: false };

    expect(createOptimisticBookmarkUpdate(post)).toEqual({
      postId: 5, bookmarked: true, status: 'ready',
    });
    expect(post).toEqual({ id: 5, bookmarked: false });
  });

  it('selects Like and Unlike endpoints and returns service results unchanged', async () => {
    const likeResult = { likes: 7, liked: true };
    const unlikeResult = { likes: 6, liked: false };
    mocks.likePost.mockResolvedValue(likeResult);
    mocks.unlikePost.mockResolvedValue(unlikeResult);

    await expect(executeLikeToggle(11, false)).resolves.toBe(likeResult);
    await expect(executeLikeToggle(12, true)).resolves.toBe(unlikeResult);

    expect(mocks.likePost).toHaveBeenCalledExactlyOnceWith(11);
    expect(mocks.unlikePost).toHaveBeenCalledExactlyOnceWith(12);
  });

  it('selects Repost and Undo Repost endpoints and returns service results unchanged', async () => {
    const repostResult = { reposts: 8, reposted: true };
    const undoResult = { reposts: 7, reposted: false };
    mocks.repostPost.mockResolvedValue(repostResult);
    mocks.undoRepostPost.mockResolvedValue(undoResult);

    await expect(executeRepostToggle(21, false)).resolves.toBe(repostResult);
    await expect(executeRepostToggle(22, true)).resolves.toBe(undoResult);

    expect(mocks.repostPost).toHaveBeenCalledExactlyOnceWith(21);
    expect(mocks.undoRepostPost).toHaveBeenCalledExactlyOnceWith(22);
  });

  it('selects Bookmark and Unbookmark endpoints and returns service results unchanged', async () => {
    const bookmarkResult = { post_id: 31, bookmarked: true };
    const unbookmarkResult = { post_id: 32, bookmarked: false };
    mocks.bookmarkPost.mockResolvedValue(bookmarkResult);
    mocks.unbookmarkPost.mockResolvedValue(unbookmarkResult);

    await expect(executeBookmarkToggle(31, false)).resolves.toBe(bookmarkResult);
    await expect(executeBookmarkToggle(32, true)).resolves.toBe(unbookmarkResult);

    expect(mocks.bookmarkPost).toHaveBeenCalledExactlyOnceWith(31);
    expect(mocks.unbookmarkPost).toHaveBeenCalledExactlyOnceWith(32);
  });
});
