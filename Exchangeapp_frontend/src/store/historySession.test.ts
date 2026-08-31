// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import type { Post } from '../types/Post';
import { useHistorySessionStore } from './historySession';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  getLikedHistory: vi.fn(),
  getPostLikeStates: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  historySync: null as any,
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
  registerHistorySessionSync: vi.fn((sync: any) => { mocks.historySync = sync; }),
  syncExternalPostLikeState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalLikeStateLocal(update);
  }),
  syncExternalPostRepostState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalRepostStateLocal(update);
  }),
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
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  article: {
    title: `Post ${id}`,
    preview: `Preview ${id}`,
    cover_image_url: '',
    publication_state: 'published',
    published_at: '2026-08-17T00:00:00.000Z',
    expired_at: null,
  },
  like_count: 3,
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
    mocks.unlikePost.mockResolvedValue({ likes: 3, liked: false });
    mocks.repostPost.mockReset();
    mocks.undoRepostPost.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
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

  it('keeps unrelated pending hydration alive when an uncached article is deleted', async () => {
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

  it('tombstones a deleted article while hydrating the other IDs in the batch', async () => {
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

  it('does not invalidate an initial page when its response contains a deleted article', async () => {
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

  it('does not invalidate a pending page when its response contains a deleted article', async () => {
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
});
