// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  releaseEngagementMutationLease,
  tryBeginEngagementMutationLease,
} from './engagementMutationLease';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  authStore: null as { isAuthenticated: boolean; currentIdentity: { id: number } | null } | null,
  getPostQuotes: vi.fn(),
  getPostEngagementStates: vi.fn(),
  createOptimisticLikeUpdate: vi.fn(),
  createOptimisticRepostUpdate: vi.fn(),
  createOptimisticBookmarkUpdate: vi.fn(),
  executeLikeToggle: vi.fn(),
  executeRepostToggle: vi.fn(),
  executeBookmarkToggle: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
  registerQuotesSessionSync: vi.fn(),
  syncQuotesLikeState: vi.fn(),
  syncQuotesRepostState: vi.fn(),
  syncQuotesBookmarkState: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/quoteService', () => ({ getPostQuotes: mocks.getPostQuotes }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.getPostEngagementStates }));
vi.mock('./engagementOperations', () => ({
  createOptimisticLikeUpdate: mocks.createOptimisticLikeUpdate,
  createOptimisticRepostUpdate: mocks.createOptimisticRepostUpdate,
  createOptimisticBookmarkUpdate: mocks.createOptimisticBookmarkUpdate,
  executeLikeToggle: mocks.executeLikeToggle,
  executeRepostToggle: mocks.executeRepostToggle,
  executeBookmarkToggle: mocks.executeBookmarkToggle,
}));
vi.mock('./sessionSync', () => ({
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
  registerQuotesSessionSync: mocks.registerQuotesSessionSync,
  syncQuotesLikeState: mocks.syncQuotesLikeState,
  syncQuotesRepostState: mocks.syncQuotesRepostState,
  syncQuotesBookmarkState: mocks.syncQuotesBookmarkState,
}));

import { useQuotesSessionStore } from './quotesSession';

const post = (id: number, overrides: Partial<Post> = {}): Post => ({
  id,
  created_at: '2026-09-20T12:00:00.000Z',
  updated_at: '2026-09-20T12:00:00.000Z',
  published_at: '2026-09-20T12:00:00.000Z',
  author: { id: 9, username: 'author', display_name: 'Author', avatar_url: '' },
  content: `Body ${id}`,
  language: 'und',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: 42,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  repost_count: 4,
  reply_count: 1,
  quote_count: 2,
  view_count: 8,
  deleted: false,
  ...overrides,
});

const createStore = (authenticated = false) => {
  mocks.authStore = reactive({
    isAuthenticated: authenticated,
    currentIdentity: authenticated ? { id: 7 } : null,
  });
  setActivePinia(createPinia());
  return useQuotesSessionStore();
};

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const readyEngagement = (
  postIDs: number[],
  overrides: {
    likes?: number;
    liked?: boolean;
    reposts?: number;
    reposted?: boolean;
    bookmarked?: boolean;
  } = {},
) => ({
  items: postIDs.map(postID => ({
    post_id: postID,
    like: { status: 'ready' as const, likes: overrides.likes ?? 13, liked: overrides.liked ?? true },
    repost: { status: 'ready' as const, reposts: overrides.reposts ?? 9, reposted: overrides.reposted ?? true },
    bookmark: { status: 'ready' as const, bookmarked: overrides.bookmarked ?? true },
  })),
});

const hydrateAllUnengaged = () => {
  mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => Promise.resolve(readyEngagement(postIDs, {
    likes: 3,
    liked: false,
    reposts: 4,
    reposted: false,
    bookmarked: false,
  })));
};

describe('quotesSession store', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.getPostQuotes.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => Promise.resolve(readyEngagement(postIDs)));
    mocks.createOptimisticLikeUpdate.mockImplementation((current: { id: number; liked: boolean; likeCount: number }) => ({
      postId: current.id,
      likes: current.liked ? Math.max(0, current.likeCount - 1) : current.likeCount + 1,
      liked: !current.liked,
      status: 'ready',
    }));
    mocks.createOptimisticRepostUpdate.mockImplementation((current: { id: number; reposted: boolean; repostCount: number }) => ({
      postId: current.id,
      reposts: current.reposted ? Math.max(0, current.repostCount - 1) : current.repostCount + 1,
      reposted: !current.reposted,
      status: 'ready',
    }));
    mocks.createOptimisticBookmarkUpdate.mockImplementation((current: { id: number; bookmarked: boolean }) => ({
      postId: current.id,
      bookmarked: !current.bookmarked,
      status: 'ready',
    }));
    mocks.executeLikeToggle.mockResolvedValue({ likes: 4, liked: true });
    mocks.executeRepostToggle.mockResolvedValue({ reposts: 5, reposted: true });
    mocks.executeBookmarkToggle.mockResolvedValue({ post_id: 1, bookmarked: true });
  });

  it('registers its external engagement handlers as a terminal Quotes sink', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.setTarget(42);

    expect(mocks.registerQuotesSessionSync).toHaveBeenCalledOnce();
    expect(mocks.registerQuotesSessionSync).toHaveBeenCalledWith({
      applyExternalLikeStateLocal: expect.any(Function),
      applyExternalRepostStateLocal: expect.any(Function),
      applyExternalBookmarkStateLocal: expect.any(Function),
    });
  });

  it('loads fresh Posts for the target and initializes guest engagement as ready', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();

    await store.setTarget('42');

    expect(mocks.getPostQuotes).toHaveBeenCalledWith(42, { limit: 20 });
    expect(store.targetPostID).toBe(42);
    expect(store.loaded).toBe(true);
    expect(store.items[0]).toMatchObject({
      id: 1,
      content: 'Body 1',
      quotePost: null,
      liked: false,
      likeStatus: 'ready',
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    });
    expect(mocks.getPostEngagementStates).not.toHaveBeenCalled();
  });

  it('ignores Like, Repost, and Bookmark mutations for guests', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore(false);
    await store.setTarget(42);

    expect(await store.toggleLike(1)).toBe('ignored');
    expect(await store.toggleRepost(1)).toBe('ignored');
    expect(await store.toggleBookmark(1)).toBe('ignored');
    expect(store.items[0]).toMatchObject({
      liked: false,
      likeCount: 3,
      reposted: false,
      repostCount: 4,
      bookmarked: false,
    });
    expect(mocks.executeLikeToggle).not.toHaveBeenCalled();
    expect(mocks.executeRepostToggle).not.toHaveBeenCalled();
    expect(mocks.executeBookmarkToggle).not.toHaveBeenCalled();
  });

  it('stores the next cursor and suppresses duplicate IDs across pages', async () => {
    mocks.getPostQuotes
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: null });
    const store = createStore();

    await store.setTarget(42);
    expect(store.nextCursor).toBe('cursor-1');
    await store.loadMore();

    expect(mocks.getPostQuotes).toHaveBeenNthCalledWith(2, 42, { limit: 20, cursor: 'cursor-1' });
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(store.nextCursor).toBeNull();
  });

  it('ignores a late initial response after the target changes', async () => {
    const oldPage = deferred<{ items: Post[]; next_cursor: null }>();
    const newPage = deferred<{ items: Post[]; next_cursor: null }>();
    mocks.getPostQuotes.mockImplementation((id: number) => id === 42 ? oldPage.promise : newPage.promise);
    const store = createStore();

    const oldRequest = store.setTarget(42);
    const newRequest = store.setTarget(99);
    newPage.resolve({ items: [post(99)], next_cursor: null });
    await newRequest;
    oldPage.resolve({ items: [post(42)], next_cursor: null });
    await oldRequest;

    expect(store.targetPostID).toBe(99);
    expect(store.items.map(item => item.id)).toEqual([99]);
  });

  it('ignores a late load-more response from the previous target', async () => {
    const oldPage = deferred<{ items: Post[]; next_cursor: null }>();
    mocks.getPostQuotes
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: 'old-cursor' })
      .mockReturnValueOnce(oldPage.promise)
      .mockResolvedValueOnce({ items: [post(99)], next_cursor: null });
    const store = createStore();
    await store.setTarget(42);

    const oldRequest = store.loadMore();
    await store.setTarget(99);
    oldPage.resolve({ items: [post(2)], next_cursor: null });
    await oldRequest;

    expect(store.items.map(item => item.id)).toEqual([99]);
  });

  it('prevents repeated sentinel calls from requesting the same cursor twice', async () => {
    const page = deferred<{ items: Post[]; next_cursor: null }>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' }).mockReturnValueOnce(page.promise);
    const store = createStore();
    await store.setTarget(42);

    const first = store.loadMore();
    const second = store.loadMore();
    expect(mocks.getPostQuotes).toHaveBeenCalledTimes(2);
    page.resolve({ items: [post(2)], next_cursor: null });
    await Promise.all([first, second]);
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
  });

  it('shows the unavailable state for an initial target 404', async () => {
    mocks.getPostQuotes.mockRejectedValueOnce({ response: { status: 404 } });
    const store = createStore();

    await store.setTarget(42);

    expect(store.targetUnavailable).toBe(true);
    expect(store.initialError).toBe('');
    expect(store.items).toEqual([]);
  });

  it('clears loaded Quotes if the target disappears during pagination', async () => {
    mocks.getPostQuotes
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: 'cursor-1' })
      .mockRejectedValueOnce({ response: { status: 404 } });
    const store = createStore();
    await store.setTarget(42);

    await store.loadMore();

    expect(store.targetUnavailable).toBe(true);
    expect(store.items).toEqual([]);
    expect(store.nextCursor).toBeNull();
  });

  it('batch hydrates authenticated engagement states for each page', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: null });
    const store = createStore(true);

    await store.setTarget(42);
    await settle();

    expect(mocks.getPostEngagementStates).toHaveBeenCalledWith([1, 2]);
    expect(store.items[0]).toMatchObject({
      likeCount: 13, liked: true, likeStatus: 'ready',
      repostCount: 9, reposted: true, repostStatus: 'ready',
      bookmarked: true, bookmarkStatus: 'ready',
    });
  });

  it('applies incoming Like, Repost, and Bookmark state and clears stale mutation errors', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.setTarget(42);
    store.mutationErrors.set(1, 'Could not update like.');

    expect(store.applyExternalLikeStateLocal({ postId: 1, liked: true, likes: 5, status: 'ready' })).toBe(true);
    expect(store.applyExternalRepostStateLocal({ postId: 1, reposted: true, reposts: 3, status: 'ready' })).toBe(true);
    expect(store.applyExternalBookmarkStateLocal({ postId: 1, bookmarked: true, status: 'ready' })).toBe(true);

    expect(store.items[0]).toMatchObject({
      liked: true, likeCount: 5, likeStatus: 'ready',
      reposted: true, repostCount: 3, repostStatus: 'ready',
      bookmarked: true, bookmarkStatus: 'ready',
    });
    expect(store.mutationErrors.has(1)).toBe(false);
    expect(mocks.syncQuotesLikeState).not.toHaveBeenCalled();
    expect(mocks.syncQuotesRepostState).not.toHaveBeenCalled();
    expect(mocks.syncQuotesBookmarkState).not.toHaveBeenCalled();
  });

  it('ignores incoming engagement for a Post that Quotes does not contain', async () => {
    const store = createStore();
    await store.setTarget(42);

    expect(store.applyExternalLikeStateLocal({ postId: 1, liked: true, likes: 5, status: 'ready' })).toBe(false);
    expect(store.applyExternalRepostStateLocal({ postId: 1, reposted: true, reposts: 3, status: 'ready' })).toBe(false);
    expect(store.applyExternalBookmarkStateLocal({ postId: 1, bookmarked: true, status: 'ready' })).toBe(false);
    expect(store.items).toEqual([]);
  });

  it('keeps a newer external Like when older engagement hydration resolves later', async () => {
    const hydration = deferred<ReturnType<typeof readyEngagement>>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostEngagementStates.mockReturnValueOnce(hydration.promise);
    const store = createStore(true);
    await store.setTarget(42);

    expect(store.applyExternalLikeStateLocal({ postId: 1, liked: true, likes: 5, status: 'ready' })).toBe(true);
    hydration.resolve(readyEngagement([1], { likes: 4, liked: false }));
    await settle();

    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 5, likeStatus: 'ready' });
  });

  it('keeps a newer external Bookmark when older engagement hydration resolves later', async () => {
    const hydration = deferred<ReturnType<typeof readyEngagement>>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostEngagementStates.mockReturnValueOnce(hydration.promise);
    const store = createStore(true);
    await store.setTarget(42);

    expect(store.applyExternalBookmarkStateLocal({ postId: 1, bookmarked: true, status: 'ready' })).toBe(true);
    hydration.resolve(readyEngagement([1], { bookmarked: false }));
    await settle();

    expect(store.items[0]).toMatchObject({ bookmarked: true, bookmarkStatus: 'ready' });
  });

  it('ignores engagement hydration from an earlier viewer generation', async () => {
    const hydration = deferred<ReturnType<typeof readyEngagement>>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.getPostEngagementStates.mockReturnValueOnce(hydration.promise);
    const store = createStore(true);
    await store.setTarget(42);

    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    await nextTick();
    hydration.resolve(readyEngagement([1]));
    await settle();

    expect(store.viewerID).toBeNull();
    expect(store.items[0]).toMatchObject({ liked: false, likeStatus: 'ready', reposted: false, bookmarked: false });
  });

  it('does not let hydration overwrite a newer local like mutation', async () => {
    const hydration = deferred<ReturnType<typeof readyEngagement>>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.setTarget(42);
    mocks.getPostEngagementStates.mockReturnValueOnce(hydration.promise);
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = { id: 7 };
    await nextTick();

    expect(await store.toggleLike(1)).toBe('succeeded');
    hydration.resolve({ items: [{
      post_id: 1,
      like: { status: 'ready', likes: 2, liked: false },
      repost: { status: 'ready', reposts: 0, reposted: false },
      bookmark: { status: 'ready', bookmarked: false },
    }] });
    await settle();

    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 4 });
  });

  it('uses existing Like, Repost, and Bookmark operations and fans out successful updates', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleLike(1)).toBe('succeeded');
    expect(await store.toggleRepost(1)).toBe('succeeded');
    expect(await store.toggleBookmark(1)).toBe('succeeded');

    expect(mocks.createOptimisticLikeUpdate).toHaveBeenCalledWith(store.items[0]);
    expect(mocks.executeLikeToggle).toHaveBeenCalledWith(1, false);
    expect(mocks.createOptimisticRepostUpdate).toHaveBeenCalledWith(store.items[0]);
    expect(mocks.executeRepostToggle).toHaveBeenCalledWith(1, false);
    expect(mocks.createOptimisticBookmarkUpdate).toHaveBeenCalledWith(store.items[0]);
    expect(mocks.executeBookmarkToggle).toHaveBeenCalledWith(1, false);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(1);
    expect(mocks.syncQuotesLikeState).toHaveBeenCalledWith({ postId: 1, likes: 4, liked: true, status: 'ready' });
    expect(mocks.syncQuotesRepostState).toHaveBeenCalledWith({ postId: 1, reposts: 5, reposted: true, status: 'ready' });
    expect(mocks.syncQuotesBookmarkState).toHaveBeenCalledWith({ postId: 1, bookmarked: true, status: 'ready' });
    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 4, reposted: true, repostCount: 5, bookmarked: true });
  });

  it('passes true to shared executors for Unlike, Undo Repost, and Unbookmark', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    mocks.executeLikeToggle.mockResolvedValue({ likes: 12, liked: false });
    mocks.executeRepostToggle.mockResolvedValue({ reposts: 8, reposted: false });
    mocks.executeBookmarkToggle.mockResolvedValue({ post_id: 1, bookmarked: false });
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleLike(1)).toBe('succeeded');
    expect(await store.toggleRepost(1)).toBe('succeeded');
    expect(await store.toggleBookmark(1)).toBe('succeeded');

    expect(mocks.executeLikeToggle).toHaveBeenCalledWith(1, true);
    expect(mocks.executeRepostToggle).toHaveBeenCalledWith(1, true);
    expect(mocks.executeBookmarkToggle).toHaveBeenCalledWith(1, true);
    expect(store.items[0]).toMatchObject({
      liked: false,
      likeCount: 12,
      reposted: false,
      repostCount: 8,
      bookmarked: false,
    });
  });

  it('applies optimistic Like state and marks it pending before the shared executor resolves', async () => {
    const request = deferred<{ likes: number; liked: boolean }>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeLikeToggle.mockReturnValueOnce(request.promise);
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    const mutation = store.toggleLike(1);

    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 4 });
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    expect(mocks.createOptimisticLikeUpdate).toHaveBeenCalledWith(store.items[0]);
    expect(mocks.executeLikeToggle).toHaveBeenCalledWith(1, false);

    request.resolve({ likes: 10, liked: true });
    expect(await mutation).toBe('succeeded');
    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 10 });
  });

  it('lets an external Like invalidate a pending local response', async () => {
    const request = deferred<{ likes: number; liked: boolean }>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeLikeToggle.mockReturnValueOnce(request.promise);
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    const mutation = store.toggleLike(1);
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    store.applyExternalLikeStateLocal({ postId: 1, likes: 5, liked: true, status: 'ready' });
    request.resolve({ likes: 99, liked: false });

    expect(await mutation).toBe('ignored');
    expect(store.items[0]).toMatchObject({ liked: true, likeCount: 5 });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
    expect(mocks.syncQuotesLikeState).not.toHaveBeenCalled();
  });

  it('lets an external Bookmark invalidate a pending local response', async () => {
    const request = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeBookmarkToggle.mockReturnValueOnce(request.promise);
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    const mutation = store.toggleBookmark(1);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(true);
    store.applyExternalBookmarkStateLocal({ postId: 1, bookmarked: true, status: 'ready' });
    request.resolve({ post_id: 1, bookmarked: false });

    expect(await mutation).toBe('ignored');
    expect(store.items[0]).toMatchObject({ bookmarked: true });
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(false);
    expect(mocks.syncQuotesBookmarkState).not.toHaveBeenCalled();
  });

  it('rolls Like back and keeps its error when the shared executor fails', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeLikeToggle.mockRejectedValueOnce(new Error('failed'));
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleLike(1)).toBe('failed');

    expect(store.items[0]).toMatchObject({ liked: false, likeCount: 3, likeStatus: 'ready' });
    expect(store.mutationErrors.get(1)).toBe('Could not update like.');
    expect(mocks.syncQuotesLikeState).not.toHaveBeenCalled();
  });

  it('rolls Repost back and keeps its error when the shared executor fails', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeRepostToggle.mockRejectedValueOnce(new Error('failed'));
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleRepost(1)).toBe('failed');

    expect(store.items[0]).toMatchObject({ reposted: false, repostCount: 4, repostStatus: 'ready' });
    expect(store.mutationErrors.get(1)).toBe('Could not update repost.');
    expect(mocks.syncQuotesRepostState).not.toHaveBeenCalled();
  });

  it('rolls Bookmark back and keeps its error when the shared executor fails', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeBookmarkToggle.mockRejectedValueOnce(new Error('failed'));
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleBookmark(1)).toBe('failed');

    expect(store.items[0]).toMatchObject({ bookmarked: false, bookmarkStatus: 'ready' });
    expect(store.mutationErrors.get(1)).toBe('Could not update bookmark.');
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(1);
    expect(mocks.syncQuotesBookmarkState).not.toHaveBeenCalled();
  });

  it('ignores a successful mutation after the target changes and does not fan it out', async () => {
    const request = deferred<{ likes: number; liked: boolean }>();
    mocks.getPostQuotes
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: null })
      .mockResolvedValueOnce({ items: [post(99)], next_cursor: null });
    hydrateAllUnengaged();
    mocks.executeLikeToggle.mockReturnValueOnce(request.promise);
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    const mutation = store.toggleLike(1);
    await store.setTarget(99);
    request.resolve({ likes: 50, liked: true });

    expect(await mutation).toBe('ignored');
    expect(store.targetPostID).toBe(99);
    expect(store.items).toHaveLength(1);
    expect(store.items[0]).toMatchObject({ id: 99, liked: false, likeCount: 3 });
    expect(mocks.syncQuotesLikeState).not.toHaveBeenCalled();
  });

  it('rejects a Like when another surface already holds the mutation lease', async () => {
    mocks.getPostQuotes.mockResolvedValueOnce({ items: [post(1)], next_cursor: null });
    hydrateAllUnengaged();
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    const lease = tryBeginEngagementMutationLease(7, 'like', 1);
    expect(lease).not.toBeNull();

    expect(await store.toggleLike(1)).toBe('ignored');
    expect(mocks.executeLikeToggle).not.toHaveBeenCalled();

    if (lease) releaseEngagementMutationLease(lease);
  });
});
