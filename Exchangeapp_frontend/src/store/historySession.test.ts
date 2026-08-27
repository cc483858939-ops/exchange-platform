// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import type { Article } from '../types/Article';
import { useHistorySessionStore } from './historySession';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  getLikedHistory: vi.fn(),
  getArticleLikeStates: vi.fn(),
  unlikeArticle: vi.fn(),
  getArticleRepostStates: vi.fn(),
  repostArticle: vi.fn(),
  undoRepostArticle: vi.fn(),
  historySync: null as any,
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/historyService', () => ({ getLikedHistory: mocks.getLikedHistory }));
vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  unlikeArticle: mocks.unlikeArticle,
}));
vi.mock('../services/repostService', () => ({
  getArticleRepostStates: mocks.getArticleRepostStates,
  repostArticle: mocks.repostArticle,
  undoRepostArticle: mocks.undoRepostArticle,
}));
vi.mock('./sessionSync', () => ({
  registerHistorySessionSync: vi.fn((sync: any) => { mocks.historySync = sync; }),
  syncExternalArticleLikeState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalLikeStateLocal(update);
  }),
  syncExternalArticleRepostState: vi.fn((update: any) => {
    mocks.historySync?.applyExternalRepostStateLocal(update);
  }),
}));

const article = (id: number, authorID = 9): Article => ({
  ID: id,
  CreatedAt: '2026-08-17T00:00:00.000Z',
  UpdatedAt: '2026-08-17T00:00:00.000Z',
  title: `Post ${id}`,
  content: `Body ${id}`,
  preview: `Preview ${id}`,
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-17T00:00:00.000Z',
  expired_at: null,
  like_count: 3,
  comment_count: 1,
  view_count: 8,
  like_sync_version: 1,
  author: {
    id: authorID,
    username: `author-${authorID}`,
    display_name: `Author ${authorID}`,
    avatar_url: '',
  },
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
  const articles = ids.map(id => article(id));
  mocks.getLikedHistory.mockResolvedValueOnce({ items: articles, next_cursor: null });
  mocks.getArticleLikeStates.mockResolvedValueOnce({
    items: ids.map(id => ({ article_id: id, likes: 4, liked: true })),
    unavailable_article_ids: [],
  });
  mocks.getArticleRepostStates.mockResolvedValueOnce({
    items: ids.map(id => ({ article_id: id, reposts: 2, reposted: false })),
    unavailable_article_ids: [],
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
    mocks.getArticleLikeStates.mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.getArticleRepostStates.mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.unlikeArticle.mockResolvedValue({ likes: 3, liked: false });
    mocks.repostArticle.mockReset();
    mocks.undoRepostArticle.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('keeps one loaded page across clean re-entry and token refresh', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(1)], next_cursor: null });

    await store.loadInitial();
    await store.loadInitial();
    mocks.authStore.currentIdentity = { id: 7, username: 'viewer-7-refreshed' };
    await store.loadInitial();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
    expect(store.items.map(item => item.id)).toEqual([1]);
  });

  it('batch-hydrates History Repost state and keeps the liked item', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(1)], next_cursor: null });
    mocks.getArticleRepostStates.mockResolvedValueOnce({
      items: [{ article_id: 1, reposts: 4, reposted: true }],
      unavailable_article_ids: [],
    });

    await store.loadInitial();
    await vi.waitFor(() => expect(store.items[0]?.repostStatus).toBe('ready'));

    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ repostCount: 4, reposted: true });
  });

  it('toggles History Repost without changing liked History membership', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    mocks.repostArticle.mockResolvedValue({ reposts: 3, reposted: true });

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
      articleId: 1,
      reposts: 5,
      reposted: true,
      status: 'ready',
    })).toBe(true);
    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ repostCount: 5, reposted: true });
  });

  it('lets a pending request finish after the view leaves without resetting the session', async () => {
    const store = createStore();
    const page = deferred<{ items: Article[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    expect(store.initialLoading).toBe(true);
    page.resolve({ items: [article(2)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.loaded).toBe(true);
  });

  it('invalidates page and hydration results when the viewer changes', async () => {
    const store = createStore(7);
    const page = deferred<{ items: Article[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    store.setViewer(8);
    page.resolve({ items: [article(3)], next_cursor: null });
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.items).toEqual([]);
    expect(store.loaded).toBe(false);
  });

  it('keeps unrelated pending hydration alive when an uncached article is deleted', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ article_id: number; likes: number; liked: boolean }>;
      unavailable_article_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({
      items: [article(1), article(2)],
      next_cursor: null,
    });
    mocks.getArticleLikeStates.mockImplementationOnce(() => hydration.promise);

    await store.loadInitial();
    await vi.waitFor(() => expect(mocks.getArticleLikeStates).toHaveBeenCalledWith([1, 2]));
    const requestVersion = store.requestVersion;
    const pagingVersion = store.pagingVersion;
    const hydrationGeneration = store.likeHydrationGeneration;

    expect(store.removeArticleLocal(99)).toBe(false);
    expect(store.requestVersion).toBe(requestVersion);
    expect(store.pagingVersion).toBe(pagingVersion);
    expect(store.likeHydrationGeneration).toBe(hydrationGeneration);

    hydration.resolve({
      items: [
        { article_id: 1, likes: 11, liked: true },
        { article_id: 2, likes: 12, liked: true },
      ],
      unavailable_article_ids: [],
    });
    await vi.waitFor(() => expect(store.items.every(item => item.likeStatus === 'ready')).toBe(true));

    expect(store.items.map(item => [item.id, item.likeCount])).toEqual([[1, 11], [2, 12]]);
  });

  it('tombstones a deleted article while hydrating the other IDs in the batch', async () => {
    const store = createStore();
    const hydration = deferred<{
      items: Array<{ article_id: number; likes: number; liked: boolean }>;
      unavailable_article_ids: number[];
    }>();
    mocks.getLikedHistory.mockResolvedValueOnce({
      items: [article(1), article(2)],
      next_cursor: null,
    });
    mocks.getArticleLikeStates.mockImplementationOnce(() => hydration.promise);

    await store.loadInitial();
    await vi.waitFor(() => expect(mocks.getArticleLikeStates).toHaveBeenCalledWith([1, 2]));
    expect(store.removeArticleLocal(1)).toBe(true);

    hydration.resolve({
      items: [
        { article_id: 1, likes: 11, liked: true },
        { article_id: 2, likes: 12, liked: true },
      ],
      unavailable_article_ids: [],
    });
    await vi.waitFor(() => expect(store.items[0]?.likeStatus).toBe('ready'));

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.items[0].likeCount).toBe(12);
  });

  it('does not invalidate an initial page when its response contains a deleted article', async () => {
    const store = createStore();
    const page = deferred<{ items: Article[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadInitial();

    expect(store.initialLoading).toBe(true);
    expect(store.removeArticleLocal(1)).toBe(false);
    page.resolve({ items: [article(1), article(2)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.loaded).toBe(true);
    expect(store.initialLoading).toBe(false);
  });

  it('does not invalidate a pending page when its response contains a deleted article', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(1)], next_cursor: 'cursor-1' });
    await store.loadInitial();

    const page = deferred<{ items: Article[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => page.promise);
    const request = store.loadMore();

    expect(store.loadingMore).toBe(true);
    expect(store.removeArticleLocal(3)).toBe(false);
    page.resolve({ items: [article(3), article(4)], next_cursor: null });
    await request;

    expect(store.items.map(item => item.id)).toEqual([1, 4]);
    expect(store.loadingMore).toBe(false);
    expect(store.nextCursor).toBe(null);
  });

  it('does not restore a deleted unlike snapshot from a later liked state', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    const unlike = deferred<{ likes: number; liked: boolean }>();
    mocks.unlikeArticle.mockImplementationOnce(() => unlike.promise);

    const request = store.toggleUnlike(1);
    expect(store.items).toEqual([]);
    expect(store.removeArticleLocal(1)).toBe(true);
    const pagingVersion = store.pagingVersion;
    expect(store.applyExternalLikeStateLocal({
      articleId: 1,
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

    mocks.unlikeArticle.mockResolvedValueOnce({ likes: 2, liked: false });
    await store.toggleUnlike(2);
    expect(store.items.map(item => item.id)).toEqual([1, 3]);

    const secondStore = createStore();
    await loadReady(secondStore, [1, 2]);
    mocks.unlikeArticle.mockResolvedValueOnce({ likes: 8, liked: true });
    await secondStore.toggleUnlike(1);
    expect(secondStore.items.map(item => item.id)).toEqual([1, 2]);
    expect(secondStore.items[0].likeCount).toBe(8);
    expect(secondStore.items[0].liked).toBe(true);

    const thirdStore = createStore();
    await loadReady(thirdStore, [1, 2, 3]);
    mocks.unlikeArticle.mockRejectedValueOnce(new Error('offline'));
    await thirdStore.toggleUnlike(2);
    expect(thirdStore.items.map(item => item.id)).toEqual([1, 2, 3]);
    expect(thirdStore.mutationErrors.get(2)).toContain('Could not remove');
  });

  it('allows a settled external like to win over an older unlike response', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    const unlike = deferred<{ likes: number; liked: boolean }>();
    mocks.unlikeArticle.mockImplementationOnce(() => unlike.promise);
    const request = store.toggleUnlike(1);

    store.applyExternalLikeStateLocal({ articleId: 1, likes: 12, liked: true, status: 'ready' });
    unlike.resolve({ likes: 0, liked: false });
    await request;

    expect(store.items[0].likeCount).toBe(12);
    expect(store.items[0].liked).toBe(true);
    expect(store.pendingUnlikeArticleIDs.has(1)).toBe(false);
  });

  it('does not remove membership for unavailable state and revalidates stale cache with a fresh cursor', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(1), article(2)], next_cursor: 'old' });
    await store.loadInitial();
    expect(store.items).toHaveLength(2);

    store.applyExternalLikeStateLocal({ articleId: 1, likes: 0, liked: false, status: 'unavailable' });
    expect(store.items.map(item => item.id)).toEqual([1, 2]);

    store.applyExternalLikeStateLocal({ articleId: 99, likes: 1, liked: true, status: 'ready' });
    expect(store.stale).toBe(true);
    await store.loadMore();
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);

    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(2), article(3)], next_cursor: 'fresh' });
    await store.revalidateHistory();

    expect(store.items.map(item => item.id)).toEqual([2, 3, 1]);
    expect(store.nextCursor).toBe('fresh');
    expect(store.stale).toBe(false);
  });

  it('keeps the cached History list on revalidation failure and removes deleted articles without refetching', async () => {
    const store = createStore();
    mocks.getLikedHistory.mockResolvedValueOnce({ items: [article(1), article(2)], next_cursor: null });
    await store.loadInitial();
    store.applyExternalLikeStateLocal({ articleId: 99, likes: 1, liked: true, status: 'ready' });
    mocks.getLikedHistory.mockRejectedValueOnce(new Error('offline'));
    await store.revalidateHistory();

    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(store.stale).toBe(true);
    expect(store.revalidating).toBe(false);
    expect(store.revalidateError).toContain('refreshed');

    store.removeArticleLocal(1);
    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);
  });

  it('updates comments and author identity in both visible and removed snapshots', async () => {
    const store = createStore();
    await loadReady(store, [1]);
    await store.toggleUnlike(1);
    expect(store.items).toEqual([]);

    expect(store.applyCommentCountUpdateLocal({ articleId: 1, commentCount: 9 })).toBe(true);
    expect(store.replaceAuthorIdentityLocal({
      id: 9,
      username: 'renamed',
      display_name: 'Renamed',
      avatar_url: '/avatar.png',
    })).toBe(true);

    mocks.unlikeArticle.mockResolvedValueOnce({ likes: 10, liked: true });
    store.applyExternalLikeStateLocal({ articleId: 1, likes: 10, liked: true, status: 'ready' });
    expect(store.items[0].commentCount).toBe(9);
    expect(store.items[0].author.username).toBe('renamed');
  });
});
