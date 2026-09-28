// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';
import { engagementResponseFromBatchMocks } from '../test-utils/engagementServiceMock';

const mocks = vi.hoisted(() => ({
  authStore: null as { isAuthenticated: boolean; currentIdentity: { id: number } | null } | null,
  getBookmarks: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  getPostLikeStates: vi.fn(),
  getPostEngagementStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/bookmarkService', () => ({
  bookmarkPost: mocks.bookmarkPost,
  getBookmarks: mocks.getBookmarks,
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  unbookmarkPost: mocks.unbookmarkPost,
}));
vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));
vi.mock('../services/engagementService', () => ({
  getPostEngagementStates: mocks.getPostEngagementStates,
}));
vi.mock('../services/repostService', () => ({
  getPostRepostStates: mocks.getPostRepostStates,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));
vi.mock('./sessionSync', () => ({
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
  registerBookmarksSessionSync: vi.fn(),
  syncExternalPostBookmarkState: vi.fn(),
  syncExternalPostLikeState: vi.fn(),
  syncExternalPostRepostState: vi.fn(),
}));

import { useBookmarksSessionStore } from './bookmarksSession';

const post = (id: number): Post => ({
  id,
  created_at: '2026-08-17T00:00:00.000Z',
  updated_at: '2026-08-17T00:00:00.000Z',
  published_at: '2026-08-17T00:00:00.000Z',
  author: {
    id: 9,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
  content: `Body ${id}`,
  language: 'und',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  repost_count: 0,
  reply_count: 1,
  view_count: 8,
  deleted: false,
});

const createStore = () => {
  mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
  setActivePinia(createPinia());
  return useBookmarksSessionStore();
};

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(resolvePromise => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('bookmarksSession store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getBookmarks.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostBookmarkStates.mockImplementation(async (postIDs: number[]) => ({
      items: postIDs.map(post_id => ({ post_id, bookmarked: true })),
      unavailable_post_ids: [],
    }));
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => engagementResponseFromBatchMocks(postIDs, mocks));
    mocks.bookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: true });
    mocks.unbookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: false });
  });

  it('loads canonical bookmarks with positive bookmark state and paginates independently', async () => {
    mocks.getBookmarks
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [post(2)], next_cursor: null });
    mocks.getPostBookmarkStates.mockResolvedValue({
      items: [{ post_id: 1, bookmarked: true }, { post_id: 2, bookmarked: true }],
      unavailable_post_ids: [],
    });
    const store = createStore();

    await store.loadInitial();
    await settle();
    expect(store.items.map(item => item.id)).toEqual([1]);
    expect(store.items[0].bookmarked).toBe(true);
    expect(store.items[0].bookmarkStatus).toBe('ready');
    expect(store.nextCursor).toBe('cursor-1');

    await store.loadMore();
    await settle();
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(mocks.getBookmarks).toHaveBeenNthCalledWith(2, { limit: 20, cursor: 'cursor-1' });
  });

  it('removes a bookmark optimistically and restores it after a failed mutation', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: null });
    const store = createStore();
    await store.loadInitial();

    mocks.unbookmarkPost.mockRejectedValueOnce(new Error('offline'));
    const request = store.toggleBookmark(1);
    expect(store.items.map(item => item.id)).toEqual([2]);

    await expect(request).resolves.toBe(false);
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(store.items[0].bookmarked).toBe(true);
    expect(store.mutationErrors.get(1)).toBe('Could not update bookmark.');
  });

  it('keeps an optimistic removal after a successful unbookmark', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.unbookmarkPost.mockResolvedValueOnce({ post_id: 1, bookmarked: false });
    const store = createStore();
    await store.loadInitial();

    const request = store.toggleBookmark(1);
    expect(store.items).toHaveLength(0);
    await expect(request).resolves.toBe(true);
    expect(store.items).toHaveLength(0);
  });

  it('applies external presentation, identity, and removal updates to cached bookmarks', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.loadInitial();

    expect(store.applyExternalLikeStateLocal({
      postId: 1,
      likes: 8,
      liked: true,
      status: 'ready',
    })).toBe(true);
    expect(store.items[0].likeCount).toBe(8);
    expect(store.items[0].liked).toBe(true);

    expect(store.applyExternalRepostStateLocal({
      postId: 1,
      reposts: 2,
      reposted: true,
      status: 'ready',
    })).toBe(true);
    expect(store.items[0].repostCount).toBe(2);
    expect(store.items[0].reposted).toBe(true);

    expect(store.replaceAuthorIdentityLocal({
      id: 9,
      username: 'updated-author',
      display_name: 'Updated Author',
      avatar_url: '/avatar-updated.jpg',
    })).toBe(true);
    expect(store.items[0].author.display_name).toBe('Updated Author');

    expect(store.removePostLocal(1)).toBe(true);
    expect(store.items).toHaveLength(0);
  });

  it('optimistically toggles Like and Repost and rolls each one back on failure', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({
      items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [],
    });
    mocks.getPostRepostStates.mockResolvedValueOnce({
      items: [{ post_id: 1, reposts: 0, reposted: false }], unavailable_post_ids: [],
    });
    const store = createStore();
    await store.loadInitial();
    await settle();
    mocks.likePost.mockRejectedValueOnce(new Error('like failed'));
    mocks.repostPost.mockRejectedValueOnce(new Error('repost failed'));

    const likeRequest = store.toggleLike(1);
    const repostRequest = store.toggleRepost(1);
    expect(store.items[0]).toMatchObject({
      likeCount: 4, liked: true, repostCount: 1, reposted: true,
    });
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    expect(store.repostPendingPostIDs.has(1)).toBe(true);

    await expect(Promise.all([likeRequest, repostRequest])).resolves.toEqual([false, false]);
    expect(store.items[0]).toMatchObject({
      likeCount: 3, liked: false, repostCount: 0, reposted: false,
    });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
    expect(store.repostPendingPostIDs.has(1)).toBe(false);
  });

  it('ignores pending Like and Repost results after external state updates', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({
      items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [],
    });
    mocks.getPostRepostStates.mockResolvedValueOnce({
      items: [{ post_id: 1, reposts: 0, reposted: false }], unavailable_post_ids: [],
    });
    const store = createStore();
    await store.loadInitial();
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    const repostResult = deferred<{ reposts: number; reposted: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);
    mocks.repostPost.mockReturnValueOnce(repostResult.promise);

    const likeMutation = store.toggleLike(1);
    const repostMutation = store.toggleRepost(1);
    store.applyExternalLikeStateLocal({ postId: 1, likes: 8, liked: true, status: 'ready' });
    store.applyExternalRepostStateLocal({ postId: 1, reposts: 7, reposted: true, status: 'ready' });
    likeResult.resolve({ likes: 4, liked: true });
    repostResult.resolve({ reposts: 2, reposted: true });

    await expect(Promise.all([likeMutation, repostMutation])).resolves.toEqual([false, false]);
    expect(store.items[0]).toMatchObject({ likeCount: 8, liked: true, repostCount: 7, reposted: true });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
    expect(store.repostPendingPostIDs.has(1)).toBe(false);
  });

  it('keeps newer local Like state when an older hydration response arrives', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const hydration = deferred<{
      items: { post_id: number; likes: number; liked: boolean }[];
      unavailable_post_ids: number[];
    }>();
    mocks.getPostLikeStates.mockReturnValueOnce(hydration.promise);
    mocks.likePost.mockResolvedValueOnce({ likes: 1, liked: true });
    const store = createStore();

    await store.loadInitial();
    store.items[0].likeStatus = 'ready';
    const mutation = store.toggleLike(1);
    await expect(mutation).resolves.toBe(true);
    hydration.resolve({
      items: [{ post_id: 1, likes: 99, liked: false }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(store.items[0]).toMatchObject({ likeCount: 1, liked: true, likeStatus: 'ready' });
  });

  it('removes a pending bookmark when external state says it is no longer saved', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.loadInitial();
    const unbookmarkResult = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.unbookmarkPost.mockReturnValueOnce(unbookmarkResult.promise);

    const mutation = store.toggleBookmark(1);
    expect(store.items).toHaveLength(0);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(true);
    expect(store.applyExternalBookmarkStateLocal({
      postId: 1, bookmarked: false, status: 'ready',
    })).toBe(true);
    unbookmarkResult.resolve({ post_id: 1, bookmarked: false });

    await expect(mutation).resolves.toBe(false);
    expect(store.items).toHaveLength(0);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(false);
    expect(store.mutationErrors.has(1)).toBe(false);
  });

  it('marks an absent externally bookmarked Post stale without inserting it', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' });
    const store = createStore();
    await store.loadInitial();
    const pagingVersion = store.pagingVersion;

    expect(store.applyExternalBookmarkStateLocal({
      postId: 2, bookmarked: true, status: 'ready',
    })).toBe(false);

    expect(store.items.map(item => item.id)).toEqual([1]);
    expect(store.stale).toBe(true);
    expect(store.pagingVersion).toBeGreaterThan(pagingVersion);
  });

  it('invalidates a pending mutation when the Bookmarks viewer changes', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({
      items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [],
    });
    const store = createStore();
    await store.loadInitial();
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);

    const mutation = store.toggleLike(1);
    store.setViewer(8);
    likeResult.resolve({ likes: 4, liked: true });

    await expect(mutation).resolves.toBe(false);
    expect(store.items).toEqual([]);
    expect(store.likePendingPostIDs.has(1)).toBe(false);
  });

  it('begins the shared bookmark fence before optimistic removal settles', async () => {
    mocks.getBookmarks.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({
      items: [{ post_id: 1, bookmarked: true }],
      unavailable_post_ids: [],
    });
    const store = createStore();
    await store.loadInitial();
    await settle();

    const pending = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.unbookmarkPost.mockReturnValueOnce(pending.promise);
    const request = store.toggleBookmark(1);

    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledTimes(1);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(1);
    expect(store.items).toHaveLength(0);

    pending.resolve({ post_id: 1, bookmarked: false });
    expect(await request).toBe(true);
  });
});
