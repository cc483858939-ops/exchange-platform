// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  authStore: null as { isAuthenticated: boolean; currentIdentity: { id: number } | null } | null,
  getPostQuotes: vi.fn(),
  getPostEngagementStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
  syncExternalPostLikeState: vi.fn(),
  syncExternalPostRepostState: vi.fn(),
  syncExternalPostBookmarkState: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/quoteService', () => ({ getPostQuotes: mocks.getPostQuotes }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.getPostEngagementStates }));
vi.mock('../services/likeService', () => ({ likePost: mocks.likePost, unlikePost: mocks.unlikePost }));
vi.mock('../services/repostService', () => ({ repostPost: mocks.repostPost, undoRepostPost: mocks.undoRepostPost }));
vi.mock('../services/bookmarkService', () => ({ bookmarkPost: mocks.bookmarkPost, unbookmarkPost: mocks.unbookmarkPost }));
vi.mock('./sessionSync', () => ({
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
  syncExternalPostLikeState: mocks.syncExternalPostLikeState,
  syncExternalPostRepostState: mocks.syncExternalPostRepostState,
  syncExternalPostBookmarkState: mocks.syncExternalPostBookmarkState,
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

const readyEngagement = (postIDs: number[]) => ({
  items: postIDs.map(postID => ({
    post_id: postID,
    like: { status: 'ready' as const, likes: 13, liked: true },
    repost: { status: 'ready' as const, reposts: 9, reposted: true },
    bookmark: { status: 'ready' as const, bookmarked: true },
  })),
});

describe('quotesSession store', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.getPostQuotes.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => Promise.resolve(readyEngagement(postIDs)));
    mocks.likePost.mockResolvedValue({ likes: 4, liked: true });
    mocks.unlikePost.mockResolvedValue({ likes: 2, liked: false });
    mocks.repostPost.mockResolvedValue({ reposts: 5, reposted: true });
    mocks.undoRepostPost.mockResolvedValue({ reposts: 3, reposted: false });
    mocks.bookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: true });
    mocks.unbookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: false });
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
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => Promise.resolve({
      items: postIDs.map(postID => ({
        post_id: postID,
        like: { status: 'ready' as const, likes: 3, liked: false },
        repost: { status: 'ready' as const, reposts: 4, reposted: false },
        bookmark: { status: 'ready' as const, bookmarked: false },
      })),
    }));
    const store = createStore(true);
    await store.setTarget(42);
    await settle();

    expect(await store.toggleLike(1)).toBe('succeeded');
    expect(await store.toggleRepost(1)).toBe('succeeded');
    expect(await store.toggleBookmark(1)).toBe('succeeded');

    expect(mocks.likePost).toHaveBeenCalledWith(1);
    expect(mocks.repostPost).toHaveBeenCalledWith(1);
    expect(mocks.bookmarkPost).toHaveBeenCalledWith(1);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(1);
    expect(mocks.syncExternalPostLikeState).toHaveBeenCalledWith({ postId: 1, likes: 4, liked: true, status: 'ready' });
    expect(mocks.syncExternalPostRepostState).toHaveBeenCalledWith({ postId: 1, reposts: 5, reposted: true, status: 'ready' });
    expect(mocks.syncExternalPostBookmarkState).toHaveBeenCalledWith({ postId: 1, bookmarked: true, status: 'ready' });
  });
});
