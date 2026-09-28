// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import type { Post } from '../types/Post';
import { engagementResponseFromBatchMocks } from '../test-utils/engagementServiceMock';
import { useHistorySessionStore } from './historySession';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  getLikedHistory: vi.fn(),
  getPostLikeStates: vi.fn(),
  getPostEngagementStates: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  historySync: null as any,
  beginBookmarkStateMutation: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/historyService', () => ({ getLikedHistory: mocks.getLikedHistory }));
vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  unlikePost: mocks.unlikePost,
}));
vi.mock('../services/repostService', () => ({
  getPostRepostStates: mocks.getPostRepostStates,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));
vi.mock('./sessionSync', () => ({
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
  registerHistorySessionSync: vi.fn((sync: any) => { mocks.historySync = sync; }),
  syncExternalPostLikeState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalLikeStateLocal(update);
  }),
  syncExternalPostRepostState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalRepostStateLocal(update);
  }),
  syncHistoryBookmarkState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalBookmarkStateLocal?.(update);
  }),
  markOwnProfileTimelineStale: vi.fn(),
}));
vi.mock('../services/engagementService', () => ({
  getPostEngagementStates: mocks.getPostEngagementStates,
}));
vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));

const post = (id: number, authorID = 9): Post => ({
  id,
  created_at: '2026-08-17T00:00:00.000Z',
  updated_at: '2026-08-17T00:00:00.000Z',
  published_at: '2026-08-17T00:00:00.000Z',
  author: {
    id: authorID,
    username: `author-${authorID}`,
    display_name: `Author ${authorID}`,
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

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const flushMicrotasks = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
  });
};

const createStore = (id = 7) => {
  setAuth(id);
  setActivePinia(createPinia());
  return useHistorySessionStore();
};

const loadReady = async (store: ReturnType<typeof useHistorySessionStore>, ids: number[]) => {
  const posts = ids.map(id => post(id));
  mocks.getLikedHistory.mockResolvedValueOnce({ items: posts, next_cursor: null });
  mocks.getPostLikeStates.mockResolvedValueOnce({
    items: ids.map(id => ({ post_id: id, likes: 4, liked: true })),
    unavailable_post_ids: [],
  });
  mocks.getPostRepostStates.mockResolvedValueOnce({
    items: ids.map(id => ({ post_id: id, reposts: 2, reposted: false })),
    unavailable_post_ids: [],
  });
  await store.loadInitial();
  await vi.waitFor(() => expect(store.items.length).toBe(ids.length));
  await vi.waitFor(() => expect(store.items.every(item => item.likeStatus === 'ready')).toBe(true));
};

describe('historySession store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.historySync = null;
    mocks.getLikedHistory.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => engagementResponseFromBatchMocks(postIDs, mocks));
    mocks.unlikePost.mockResolvedValue({ likes: 3, liked: false });
    mocks.repostPost.mockReset();
    mocks.undoRepostPost.mockReset();
    mocks.bookmarkPost.mockReset();
    mocks.unbookmarkPost.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('stores internal scrollTop and normalizes invalid values', () => {
    const store = createStore();

    store.saveScrollTop(640);
    expect(store.scrollTop).toBe(640);

    store.saveScrollTop(-1);
    expect(store.scrollTop).toBe(0);
    store.saveScrollTop(Number.NaN);
    expect(store.scrollTop).toBe(0);
    store.saveScrollTop(Number.POSITIVE_INFINITY);
    expect(store.scrollTop).toBe(0);
  });

  it('clears internal scrollTop when the viewer changes', () => {
    const store = createStore(7);

    store.saveScrollTop(640);
    store.setViewer(8);

    expect(store.scrollTop).toBe(0);
  });

  it('keeps one loaded page across clean re-entry and token refresh', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });

    await store.loadInitial();
    await store.loadInitial();
    mocks.authStore.currentIdentity = { id: 7, username: 'viewer-7-refreshed' };
    await store.loadInitial();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
    expect(store.items.map(item => item.id)).toEqual([1]);
  });

  it('batch-hydrates History Repost state and keeps the liked item', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostRepostStates.mockResolvedValueOnce({
      items: [{ post_id: 1, reposts: 4, reposted: true }],
      unavailable_post_ids: [],
    });

    await store.loadInitial();
    await vi.waitFor(() => expect(store.items[0]?.repostStatus).toBe('ready'));

    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ repostCount: 4, reposted: true });
  });

  it('toggles History Repost without changing liked History membership', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    mocks.repostPost.mockResolvedValue({ reposts: 3, reposted: true });

    const request = store.toggleRepost(1);
    expect(store.items).toHaveLength(1);
    expect(store.items[0].reposted).toBe(true);
    expect(await request).toBe(true);
    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ repostCount: 3, reposted: true });
  });

  it('applies an external Detail Repost update to History without removing the item', async () => {
    const store = createStore();
    await loadReady(store, [1]);

    expect(store.applyExternalRepostStateLocal({
      postId: 1,
      reposts: 5,
      reposted: true,
      status: 'ready',
    })).toBe(true);
    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ repostCount: 5, reposted: true });
  });

  it('lets a pending request finish after the view leaves without resetting the session', async () => {
    const store = createStore();
    const page = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    expect(store.initialLoading).toBe(true);
    page.resolve({ items: [post(2)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.loaded).toBe(true);
  });

  it('invalidates page and hydration results when the viewer changes', async () => {
    const store = createStore(7);
    const page = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    store.setViewer(8);
    page.resolve({ items: [post(3)], next_cursor: null });
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.items).toEqual([]);
    expect(store.loaded).toBe(false);
  });

  it('drops engagement hydration after a viewer reset', async () => {
    const store = createStore(7);
    const hydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockReturnValueOnce(hydration.promise);

    await store.loadInitial();
    expect(store.items.map(item => item.id)).toEqual([1]);
    store.setViewer(8);
    hydration.resolve({
      items: [{ post_id: 1, likes: 99, liked: true }],
      unavailable_post_ids: [],
    });
    await flushMicrotasks();

    expect(store.viewerID).toBe(8);
    expect(store.items).toEqual([]);
  });

  it('does not let an older Like hydration overwrite an external Like update', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockReturnValueOnce(hydration.promise);

    await store.loadInitial();
    store.applyExternalLikeStateLocal({
      postId: 1,
      likes: 12,
      liked: true,
      status: 'ready',
    });
    hydration.resolve({
      items: [{ post_id: 1, likes: 99, liked: true }],
      unavailable_post_ids: [],
    });
    await flushMicrotasks();

    expect(store.items[0]).toMatchObject({ likeCount: 12, liked: true, likeStatus: 'ready' });
  });

  it('does not let older Repost hydration overwrite a newer mutation', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const mutation = deferred<{ reposts: number; reposted: boolean }>();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostRepostStates.mockReturnValueOnce(hydration.promise);
    mocks.repostPost.mockReturnValueOnce(mutation.promise);

    await store.loadInitial();
    const item = store.items[0];
    item.repostStatus = 'ready';
    item.repostCount = 2;
    const request = store.toggleRepost(1);
    expect(item).toMatchObject({ repostCount: 3, reposted: true });
    expect(store.repostPendingPostIDs.has(1)).toBe(true);

    hydration.resolve({
      items: [{ post_id: 1, reposts: 90, reposted: false }],
      unavailable_post_ids: [],
    });
    await flushMicrotasks();
    expect(item).toMatchObject({ repostCount: 3, reposted: true });
    expect(store.repostPendingPostIDs.has(1)).toBe(true);

    mutation.resolve({ reposts: 4, reposted: true });
    await expect(request).resolves.toBe(true);
    expect(item).toMatchObject({ repostCount: 4, reposted: true });
    expect(store.repostPendingPostIDs.has(1)).toBe(false);
  });

  it('does not let older Bookmark hydration overwrite a newer mutation', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ post_id: number; bookmarked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const mutation = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostBookmarkStates.mockReturnValueOnce(hydration.promise);
    mocks.bookmarkPost.mockReturnValueOnce(mutation.promise);

    await store.loadInitial();
    const item = store.items[0];
    item.bookmarkStatus = 'ready';
    item.bookmarked = false;
    const request = store.toggleBookmark(1);
    expect(item.bookmarked).toBe(true);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(true);

    hydration.resolve({
      items: [{ post_id: 1, bookmarked: false }],
      unavailable_post_ids: [],
    });
    await flushMicrotasks();
    expect(item.bookmarked).toBe(true);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(true);

    mutation.resolve({ post_id: 1, bookmarked: true });
    await expect(request).resolves.toBe(true);
    expect(item.bookmarked).toBe(true);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(false);
  });

  it('keeps unrelated pending hydration alive when an uncached Post is deleted', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({
      items: [post(1), post(2)],
      next_cursor: null,
    });
    mocks.getPostLikeStates.mockImplementationOnce(() => hydration.promise);

    await store.loadInitial();
    await vi.waitFor(() => expect(mocks.getPostLikeStates).toHaveBeenCalledWith([1, 2]));
    const requestVersion = store.requestVersion;
    const pagingVersion = store.pagingVersion;
    const hydrationGeneration = store.likeHydrationGeneration;

    expect(store.removePostLocal(99)).toBe(false);
    expect(store.requestVersion).toBe(requestVersion);
    expect(store.pagingVersion).toBe(pagingVersion);
    expect(store.likeHydrationGeneration).toBe(hydrationGeneration);

    hydration.resolve({
      items: [
        { post_id: 1, likes: 11, liked: true },
        { post_id: 2, likes: 12, liked: true },
      ],
      unavailable_post_ids: [],
    });
    await vi.waitFor(() => expect(store.items.every(item => item.likeStatus === 'ready')).toBe(true));

    expect(store.items.map(item => [item.id, item.likeCount])).toEqual([[1, 11], [2, 12]]);
  });

  it('tombstones a deleted Post while hydrating the other IDs in the batch', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({
      items: [post(1), post(2)],
      next_cursor: null,
    });
    mocks.getPostLikeStates.mockImplementationOnce(() => hydration.promise);

    await store.loadInitial();
    await vi.waitFor(() => expect(mocks.getPostLikeStates).toHaveBeenCalledWith([1, 2]));
    expect(store.removePostLocal(1)).toBe(true);

    hydration.resolve({
      items: [
        { post_id: 1, likes: 11, liked: true },
        { post_id: 2, likes: 12, liked: true },
      ],
      unavailable_post_ids: [],
    });
    await vi.waitFor(() => expect(store.items[0]?.likeStatus).toBe('ready'));

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.items[0].likeCount).toBe(12);
  });

  it('does not invalidate an initial page when its response contains a deleted Post', async () => {
    const store = createStore();
    const page = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    expect(store.initialLoading).toBe(true);
    expect(store.removePostLocal(1)).toBe(false);
    page.resolve({ items: [post(1), post(2)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.loaded).toBe(true);
    expect(store.initialLoading).toBe(false);
  });

  it('does not invalidate a pending page when its response contains a deleted Post', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' });
    await store.loadInitial();

    const page = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadMore();

    expect(store.loadingMore).toBe(true);
    expect(store.removePostLocal(3)).toBe(false);
    page.resolve({ items: [post(3), post(4)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([1, 4]);
    expect(store.loadingMore).toBe(false);
    expect(store.nextCursor).toBe(null);
  });

  it('does not restore a deleted unlike snapshot from a later liked state', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    const unlike = deferred<{ likes: number; liked: boolean }>();
    mocks.unlikePost.mockImplementationOnce(() => unlike.promise);

    const request = store.toggleUnlike(1);
    expect(store.items).toEqual([]);
    expect(store.removePostLocal(1)).toBe(true);
    const pagingVersion = store.pagingVersion;
    expect(store.applyExternalLikeStateLocal({
      postId: 1,
      likes: 99,
      liked: true,
      status: 'ready',
    })).toBe(false);
    expect(store.items).toEqual([]);
    expect(store.stale).toBe(false);
    expect(store.pagingVersion).toBe(pagingVersion);

    unlike.resolve({ likes: 99, liked: true });
    await request;
    expect(store.items).toEqual([]);
  });

  it('preserves unlike success, unexpected like success, and failure rollback semantics', async () => {
    const store = createStore();
    await loadReady(store, [1, 2, 3]);

    mocks.unlikePost.mockResolvedValueOnce({ likes: 2, liked: false });
    await store.toggleUnlike(2);
    expect(store.items.map(item => item.id)).toEqual([1, 3]);

    const secondStore = createStore();
    await loadReady(secondStore, [1, 2]);
    mocks.unlikePost.mockResolvedValueOnce({ likes: 8, liked: true });
    await secondStore.toggleUnlike(1);
    expect(secondStore.items.map(item => item.id)).toEqual([1, 2]);
    expect(secondStore.items[0].likeCount).toBe(8);
    expect(secondStore.items[0].liked).toBe(true);

    const thirdStore = createStore();
    await loadReady(thirdStore, [1, 2, 3]);
    mocks.unlikePost.mockRejectedValueOnce(new Error('offline'));
    await thirdStore.toggleUnlike(2);
    expect(thirdStore.items.map(item => item.id)).toEqual([1, 2, 3]);
    expect(thirdStore.mutationErrors.get(2)).toContain('Could not remove');
  });

  it('allows a settled external like to win over an older unlike response', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    const unlike = deferred<{ likes: number; liked: boolean }>();
    mocks.unlikePost.mockImplementationOnce(() => unlike.promise);
    const request = store.toggleUnlike(1);

    store.applyExternalLikeStateLocal({ postId: 1, likes: 12, liked: true, status: 'ready' });
    unlike.resolve({ likes: 0, liked: false });
    await request;

    expect(store.items[0].likeCount).toBe(12);
    expect(store.items[0].liked).toBe(true);
    expect(store.pendingUnlikePostIDs.has(1)).toBe(false);
  });

  it('ignores duplicate Unlike and keeps the newer request pending after stale A resolves', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    const first = deferred<{ likes: number; liked: boolean }>();
    const second = deferred<{ likes: number; liked: boolean }>();
    mocks.unlikePost.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    const requestA = store.toggleUnlike(1);
    expect(store.items).toEqual([]);
    expect(store.pendingUnlikePostIDs.has(1)).toBe(true);
    await store.toggleUnlike(1);
    expect(mocks.unlikePost).toHaveBeenCalledTimes(1);

    store.applyExternalLikeStateLocal({
      postId: 1,
      likes: 12,
      liked: true,
      status: 'ready',
    });
    const requestB = store.toggleUnlike(1);
    expect(store.items).toEqual([]);
    expect(store.pendingUnlikePostIDs.has(1)).toBe(true);
    expect(mocks.unlikePost).toHaveBeenCalledTimes(2);

    first.resolve({ likes: 0, liked: false });
    await requestA;
    expect(store.pendingUnlikePostIDs.has(1)).toBe(true);
    expect(store.items).toEqual([]);

    second.resolve({ likes: 11, liked: false });
    await requestB;
    expect(store.pendingUnlikePostIDs.has(1)).toBe(false);
    expect(store.items).toEqual([]);
  });

  it('restores Unlike failure at its original index and preserves the 503 state', async () => {
    const store = createStore();
    await loadReady(store, [1, 2, 3]);
    mocks.unlikePost.mockRejectedValueOnce({ response: { status: 503 } });

    await store.toggleUnlike(2);

    expect(store.items.map(item => item.id)).toEqual([1, 2, 3]);
    expect(store.items[1]).toMatchObject({ liked: true, likeStatus: 'unavailable' });
    expect(store.pendingUnlikePostIDs.has(2)).toBe(false);
    expect(store.mutationErrors.get(2)).toBe('Likes are temporarily unavailable.');
  });

  it('does not remove membership for unavailable state and revalidates stale cache with a fresh cursor', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: 'old' });
    await store.loadInitial();
    expect(store.items).toHaveLength(2);

    store.applyExternalLikeStateLocal({ postId: 1, likes: 0, liked: false, status: 'unavailable' });
    expect(store.items.map(item => item.id)).toEqual([1, 2]);

    store.applyExternalLikeStateLocal({ postId: 99, likes: 1, liked: true, status: 'ready' });
    expect(store.stale).toBe(true);
    await store.loadMore();
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);

    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(2), post(3)], next_cursor: 'fresh' });
    await store.revalidateHistory();

    expect(store.items.map(item => item.id)).toEqual([2, 3, 1]);
    expect(store.nextCursor).toBe('fresh');
    expect(store.stale).toBe(false);
  });

  it('keeps the cached History list on revalidation failure and removes deleted articles without refetching', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: null });
    await store.loadInitial();
    store.applyExternalLikeStateLocal({ postId: 99, likes: 1, liked: true, status: 'ready' });
    mocks.getLikedHistory.mockRejectedValueOnce(new Error('offline'));
    await store.revalidateHistory();

    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(store.stale).toBe(true);
    expect(store.revalidating).toBe(false);
    expect(store.revalidateError).toContain('refreshed');

    store.removePostLocal(1);
    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);
  });

  it('updates replies and author identity in both visible and removed snapshots', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    await store.toggleUnlike(1);
    expect(store.items).toEqual([]);

    expect(store.applyReplyCountUpdateLocal({ postId: 1, replyCount: 9 })).toBe(true);
    expect(store.replaceAuthorIdentityLocal({
      id: 9,
      username: 'renamed',
      display_name: 'Renamed',
      avatar_url: '/avatar.png',
    })).toBe(true);

    mocks.unlikePost.mockResolvedValueOnce({ likes: 10, liked: true });
    store.applyExternalLikeStateLocal({ postId: 1, likes: 10, liked: true, status: 'ready' });
    expect(store.items[0].replyCount).toBe(9);
    expect(store.items[0].author.username).toBe('renamed');
  });

  it('begins the shared bookmark fence before a History mutation settles', async () => {
    const store = createStore();
    await loadReady(store, [4]);
    store.items[0].bookmarkStatus = 'ready';
    store.items[0].bookmarked = false;
    const pending = deferred<{ bookmarked: boolean }>();
    mocks.bookmarkPost.mockReturnValueOnce(pending.promise);

    const request = store.toggleBookmark(4);

    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledTimes(1);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(4);
    expect(store.items[0].bookmarked).toBe(true);

    pending.resolve({ bookmarked: true });
    expect(await request).toBe(true);
  });
});
