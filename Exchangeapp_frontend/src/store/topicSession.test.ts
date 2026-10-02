// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';
import { engagementResponseFromBatchMocks } from '../test-utils/engagementServiceMock';

const mocks = vi.hoisted(() => ({
  authStore: null as { isAuthenticated: boolean; currentIdentity: { id: number } | null } | null,
  getTopicPosts: vi.fn(),
  getPostLikeStates: vi.fn(),
  getPostEngagementStates: vi.fn(),
  getPostRepostStates: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  registerTopicSessionSync: vi.fn(),
  syncExternalPostLikeState: vi.fn(),
  syncExternalPostRepostState: vi.fn(),
  syncExternalPostBookmarkState: vi.fn(),
  syncTopicLikeState: vi.fn(),
  syncTopicRepostState: vi.fn(),
  syncTopicBookmarkState: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/topicService', () => ({ getTopicPosts: mocks.getTopicPosts }));
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
vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));
vi.mock('./sessionSync', () => ({
  registerTopicSessionSync: mocks.registerTopicSessionSync,
  syncExternalPostLikeState: mocks.syncExternalPostLikeState,
  syncExternalPostRepostState: mocks.syncExternalPostRepostState,
  syncExternalPostBookmarkState: mocks.syncExternalPostBookmarkState,
  syncTopicLikeState: mocks.syncTopicLikeState,
  syncTopicRepostState: mocks.syncTopicRepostState,
  syncTopicBookmarkState: mocks.syncTopicBookmarkState,
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
}));

import { useTopicSessionStore } from './topicSession';
import {
  releaseEngagementMutationLease,
  tryBeginEngagementMutationLease,
} from './engagementMutationLease';

const topic = (slug: string) => ({ slug, label: slug.toUpperCase(), description: `${slug} description` });

const post = (id: number): Post => ({
  id,
  created_at: '2026-09-20T12:00:00.000Z',
  updated_at: '2026-09-20T12:00:00.000Z',
  published_at: '2026-09-20T12:00:00.000Z',
  author: { id: 9, username: 'author', display_name: 'Author', avatar_url: '' },
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
  repost_count: 4,
  reply_count: 1,
  quote_count: 0,
  view_count: 8,
  deleted: false,
});

const createStore = (authenticated = false) => {
  mocks.authStore = reactive({
    isAuthenticated: authenticated,
    currentIdentity: authenticated ? { id: 7 } : null,
  });
  setActivePinia(createPinia());
  return useTopicSessionStore();
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

describe('topicSession store', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.getTopicPosts.mockImplementation(async (slug: string) => ({
      topic: topic(slug), items: [], next_cursor: null,
    }));
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostEngagementStates.mockImplementation((postIDs: number[]) => engagementResponseFromBatchMocks(postIDs, mocks));
    mocks.likePost.mockResolvedValue({ likes: 4, liked: true });
    mocks.unlikePost.mockResolvedValue({ likes: 2, liked: false });
    mocks.repostPost.mockResolvedValue({ reposts: 5, reposted: true });
    mocks.undoRepostPost.mockResolvedValue({ reposts: 3, reposted: false });
    mocks.bookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: true });
    mocks.unbookmarkPost.mockResolvedValue({ post_id: 1, bookmarked: false });
  });

  it('loads a public topic and converts Posts to ready guest FeedPosts', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    const store = createStore();

    await store.setTopic(' Japan ');

    expect(mocks.getTopicPosts).toHaveBeenCalledWith('japan', { limit: 20 });
    expect(store.topic).toEqual(topic('japan'));
    expect(store.items[0]).toMatchObject({
      id: 1, content: 'Body 1', createdAt: post(1).published_at,
      likeCount: 3, repostCount: 4, replyCount: 1, viewCount: 8,
      liked: false, likeStatus: 'ready', reposted: false, repostStatus: 'ready',
      bookmarked: false, bookmarkStatus: 'ready',
    });
    expect(mocks.getPostLikeStates).not.toHaveBeenCalled();
  });

  it('batch hydrates authenticated engagement and leaves posts visible if a batch fails', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('ai'), items: [post(1), post(2)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({
      items: [{ post_id: 1, likes: 9, liked: true }], unavailable_post_ids: [],
    });
    mocks.getPostRepostStates.mockRejectedValueOnce(new Error('offline'));
    mocks.getPostBookmarkStates.mockResolvedValueOnce({
      items: [{ post_id: 1, bookmarked: true }, { post_id: 2, bookmarked: false }], unavailable_post_ids: [],
    });
    const store = createStore(true);

    await store.setTopic('ai');
    await settle();

    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([1, 2]);
    expect(mocks.getPostRepostStates).toHaveBeenCalledWith([1, 2]);
    expect(mocks.getPostBookmarkStates).toHaveBeenCalledWith([1, 2]);
    expect(store.items).toHaveLength(2);
    expect(store.items[0].likeCount).toBe(9);
    expect(store.items[0].liked).toBe(true);
    expect(store.items[0].repostStatus).toBe('unavailable');
    expect(store.items[1].repostStatus).toBe('unavailable');
    expect(store.items[0].bookmarked).toBe(true);
  });

  it('appends cursor pages without duplicate post IDs', async () => {
    mocks.getTopicPosts
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(1), post(2)], next_cursor: null });
    const store = createStore();

    await store.setTopic('ai');
    await store.loadMore();

    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(mocks.getTopicPosts).toHaveBeenNthCalledWith(2, 'ai', { limit: 20, cursor: 'cursor-1' });
  });

  it('keeps loaded pages and the next cursor when re-entering the same Topic', async () => {
    mocks.getTopicPosts
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(2)], next_cursor: 'cursor-2' })
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(3)], next_cursor: null });
    const store = createStore();

    await store.setTopic('ai');
    await store.loadMore();
    store.saveScrollTop(1200);
    await store.setTopic(' AI ');
    await store.loadMore();

    expect(mocks.getTopicPosts).toHaveBeenCalledTimes(3);
    expect(mocks.getTopicPosts).toHaveBeenNthCalledWith(3, 'ai', { limit: 20, cursor: 'cursor-2' });
    expect(store.items.map(item => item.id)).toEqual([1, 2, 3]);
    expect(store.scrollTop).toBe(1200);
  });

  it('ignores a stale response after the active slug changes', async () => {
    const japan = deferred<{ topic: ReturnType<typeof topic>; items: Post[]; next_cursor: null }>();
    const ai = deferred<{ topic: ReturnType<typeof topic>; items: Post[]; next_cursor: null }>();
    mocks.getTopicPosts.mockImplementation((slug: string) => slug === 'japan' ? japan.promise : ai.promise);
    const store = createStore();

    const oldRequest = store.setTopic('japan');
    const newRequest = store.setTopic('ai');
    ai.resolve({ topic: topic('ai'), items: [post(2)], next_cursor: null });
    await newRequest;
    japan.resolve({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    await oldRequest;

    expect(store.activeSlug).toBe('ai');
    expect(store.topic).toEqual(topic('ai'));
    expect(store.items.map(item => item.id)).toEqual([2]);
  });

  it('clears the prior page on a slug change and supports initial and load-more retries', async () => {
    mocks.getTopicPosts
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: 'cursor-1' })
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(2)], next_cursor: null });
    const store = createStore();

    await store.setTopic('japan');
    expect(store.initialError).toBeTruthy();
    await store.retryInitial();
    expect(store.items.map(item => item.id)).toEqual([1]);
    await store.loadMore();
    expect(store.loadMoreError).toBeTruthy();
    await store.retryLoadMore();
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    await store.setTopic('space');
    expect(store.topic).toEqual(topic('space'));
    expect(store.items).toEqual([]);
    expect(store.nextCursor).toBeNull();
  });

  it('guards duplicate mutations, applies server counts, and synchronizes successful changes', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();

    const likeResult = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);
    const mutation = store.toggleLike(1);
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    await expect(store.toggleLike(1)).resolves.toBe('ignored');
    likeResult.resolve({ likes: 11, liked: true });
    await expect(mutation).resolves.toBe('succeeded');
    expect(store.items[0].likeCount).toBe(11);
    expect(mocks.syncTopicLikeState).toHaveBeenCalledWith({ postId: 1, likes: 11, liked: true, status: 'ready' });

    await expect(store.toggleRepost(1)).resolves.toBe('succeeded');
    expect(store.items[0].repostCount).toBe(5);
    expect(mocks.syncTopicRepostState).toHaveBeenCalledWith({ postId: 1, reposts: 5, reposted: true, status: 'ready' });
    await expect(store.toggleBookmark(1)).resolves.toBe('succeeded');
    expect(store.items[0].bookmarked).toBe(true);
    expect(mocks.syncTopicBookmarkState).toHaveBeenCalledWith({ postId: 1, bookmarked: true, status: 'ready' });
    expect(store.likePendingPostIDs.size + store.repostPendingPostIDs.size + store.bookmarkPendingPostIDs.size).toBe(0);
  });

  it('leaves Topic state and mutation errors untouched when another surface owns the Like lease', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    store.mutationErrors.set(1, 'existing error');
    const lease = tryBeginEngagementMutationLease(7, 'like', 1)!;

    try {
      await expect(store.toggleLike(1)).resolves.toBe('ignored');
      expect(store.items[0]).toMatchObject({ liked: false, likeCount: 3 });
      expect(store.likePendingPostIDs.has(1)).toBe(false);
      expect(store.mutationErrors.get(1)).toBe('existing error');
      expect(mocks.likePost).not.toHaveBeenCalled();
      expect(mocks.unlikePost).not.toHaveBeenCalled();
    } finally {
      releaseEngagementMutationLease(lease);
    }
  });

  it('registers the Topic adapter and applies external post state locally', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.setTopic('japan');

    expect(mocks.registerTopicSessionSync).toHaveBeenCalledOnce();
    expect(mocks.registerTopicSessionSync).toHaveBeenCalledWith(expect.objectContaining({
      applyExternalLikeStateLocal: expect.any(Function),
      applyExternalRepostStateLocal: expect.any(Function),
      applyExternalBookmarkStateLocal: expect.any(Function),
      applyReplyCountUpdateLocal: expect.any(Function),
      applyQuoteCountUpdateLocal: expect.any(Function),
      removePostLocal: expect.any(Function),
      replaceAuthorIdentityLocal: expect.any(Function),
    }));

    expect(store.applyExternalLikeStateLocal({ postId: 1, likes: 4, liked: true, status: 'ready' })).toBe(true);
    expect(store.applyExternalRepostStateLocal({ postId: 1, reposts: 5, reposted: true, status: 'ready' })).toBe(true);
    expect(store.applyExternalBookmarkStateLocal({ postId: 1, bookmarked: true, status: 'ready' })).toBe(true);
    expect(store.applyReplyCountUpdateLocal({ postId: 1, replyCount: 7 })).toBe(true);
    expect(store.applyQuoteCountUpdateLocal({ postId: 1, quoteCount: 6 })).toBe(true);
    expect(store.applyQuoteCountUpdateLocal({ postId: 1, quoteCount: -1 })).toBe(false);
    expect(store.applyQuoteCountUpdateLocal({ postId: 0, quoteCount: 10 })).toBe(false);
    expect(store.items[0]).toMatchObject({
      likeCount: 4,
      liked: true,
      repostCount: 5,
      reposted: true,
      bookmarked: true,
      replyCount: 7,
      quoteCount: 6,
    });
    expect(store.applyReplyCountUpdateLocal({ postId: 1, replyCount: -1 })).toBe(false);
  });

  it('invalidates pending local mutations when external state arrives', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);

    const mutation = store.toggleLike(1);
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    expect(mocks.likePost).toHaveBeenCalledOnce();
    store.applyExternalLikeStateLocal({ postId: 1, likes: 9, liked: true, status: 'ready' });
    likeResult.resolve({ likes: 4, liked: true });

    await expect(mutation).resolves.toBe('ignored');
    expect(store.items[0]).toMatchObject({ likeCount: 9, liked: true });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
  });

  it('ignores a pending Topic mutation after changing slugs', async () => {
    mocks.getTopicPosts
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null })
      .mockResolvedValueOnce({ topic: topic('ai'), items: [post(2)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);

    const mutation = store.toggleLike(1);
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    await store.setTopic('ai');
    likeResult.resolve({ likes: 99, liked: true });

    await expect(mutation).resolves.toBe('ignored');
    expect(store.activeSlug).toBe('ai');
    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.items[0]).toMatchObject({ likeCount: 3, liked: false });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
  });

  it('ignores an old viewer mutation after a different viewer becomes active', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);

    const mutation = store.toggleLike(1);
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 8, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    mocks.authStore!.currentIdentity = { id: 8 };
    store.setViewer(8);
    await settle();
    expect(store.viewerID).toBe(8);
    likeResult.resolve({ likes: 99, liked: true });

    await expect(mutation).resolves.toBe('ignored');
    expect(store.viewerID).toBe(8);
    expect(store.items[0]).toMatchObject({ likeCount: 8, liked: false });
    expect(store.likePendingPostIDs.has(1)).toBe(false);
  });

  it('invalidates all engagement mutations when removing a post and blocks a cursor duplicate', async () => {
    mocks.getTopicPosts
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(1), post(2)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({ items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValueOnce({ items: [{ post_id: 1, reposts: 4, reposted: false }], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValueOnce({ items: [{ post_id: 1, bookmarked: false }], unavailable_post_ids: [] });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    const likeResult = deferred<{ likes: number; liked: boolean }>();
    const repostResult = deferred<{ reposts: number; reposted: boolean }>();
    const bookmarkResult = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.likePost.mockReturnValueOnce(likeResult.promise);
    mocks.repostPost.mockReturnValueOnce(repostResult.promise);
    mocks.bookmarkPost.mockReturnValueOnce(bookmarkResult.promise);

    const likeMutation = store.toggleLike(1);
    const repostMutation = store.toggleRepost(1);
    const bookmarkMutation = store.toggleBookmark(1);
    expect(store.likePendingPostIDs.has(1)).toBe(true);
    expect(store.repostPendingPostIDs.has(1)).toBe(true);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(true);

    expect(store.removePostLocal(1)).toBe(true);
    expect(store.items).toEqual([]);
    expect(store.likePendingPostIDs.has(1)).toBe(false);
    expect(store.repostPendingPostIDs.has(1)).toBe(false);
    expect(store.bookmarkPendingPostIDs.has(1)).toBe(false);

    likeResult.resolve({ likes: 5, liked: true });
    repostResult.resolve({ reposts: 6, reposted: true });
    bookmarkResult.resolve({ post_id: 1, bookmarked: true });
    await expect(Promise.all([likeMutation, repostMutation, bookmarkMutation])).resolves.toEqual([
      'ignored', 'ignored', 'ignored',
    ]);
    await store.loadMore();

    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(mocks.syncTopicLikeState).not.toHaveBeenCalled();
    expect(mocks.syncTopicRepostState).not.toHaveBeenCalled();
    expect(mocks.syncTopicBookmarkState).not.toHaveBeenCalled();
  });

  it('removes posts, blocks later cursor duplicates, and updates author identities', async () => {
    mocks.getTopicPosts
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ topic: topic('japan'), items: [post(99), post(2)], next_cursor: null });
    const store = createStore();
    await store.setTopic('japan');
    store.items[0].repostContext = { actor: post(1).author };

    expect(store.replaceAuthorIdentityLocal({ id: 9, username: 'new-author', display_name: 'New Author', avatar_url: '' })).toBe(true);
    expect(store.items[0].author.username).toBe('new-author');
    expect(store.items[0].repostContext?.actor.username).toBe('new-author');
    expect(store.removePostLocal(99)).toBe(false);
    await store.loadMore();
    expect(store.items.map(item => item.id)).toEqual([1, 2]);

    expect(store.removePostLocal(1)).toBe(true);
    expect(store.items.map(item => item.id)).toEqual([2]);
  });

  it('normalizes saved scroll and resets it when Topic page state is cleared', async () => {
    const store = createStore();
    store.saveScrollTop(1200);
    expect(store.scrollTop).toBe(1200);
    store.saveScrollTop(Number.NaN);
    expect(store.scrollTop).toBe(0);
    store.saveScrollTop(42);

    await store.setTopic('japan');
    await store.setTopic('ai');
    expect(store.scrollTop).toBe(0);
  });

  it('does not let old hydration overwrite a newer mutation', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    const store = createStore();
    await store.setTopic('japan');
    const staleLikes = deferred<{ items: { post_id: number; likes: number; liked: boolean }[]; unavailable_post_ids: number[] }>();
    mocks.getPostLikeStates.mockReturnValueOnce(staleLikes.promise);
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = { id: 7 };
    await settle();

    await expect(store.toggleLike(1)).resolves.toBe('succeeded');
    expect(store.items[0].likeCount).toBe(4);
    staleLikes.resolve({ items: [{ post_id: 1, likes: 1, liked: false }], unavailable_post_ids: [] });
    await settle();
    expect(store.items[0].liked).toBe(true);
    expect(store.items[0].likeCount).toBe(4);
  });

  it('rolls back failed mutations and clears pending state', async () => {
    mocks.getTopicPosts.mockResolvedValueOnce({ topic: topic('japan'), items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValueOnce({
      items: [{ post_id: 1, likes: 3, liked: false }], unavailable_post_ids: [],
    });
    const store = createStore(true);
    await store.setTopic('japan');
    await settle();
    mocks.likePost.mockRejectedValueOnce(new Error('offline'));

    await expect(store.toggleLike(1)).resolves.toBe('failed');
    expect(store.items[0].liked).toBe(false);
    expect(store.items[0].likeCount).toBe(3);
    expect(store.likePendingPostIDs.has(1)).toBe(false);
    expect(store.mutationErrors.get(1)).toBe('Could not update like.');
  });
});
