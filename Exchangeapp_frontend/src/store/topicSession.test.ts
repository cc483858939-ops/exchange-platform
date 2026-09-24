// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  authStore: null as { isAuthenticated: boolean; currentIdentity: { id: number } | null } | null,
  getTopicPosts: vi.fn(),
  getPostLikeStates: vi.fn(),
  getPostRepostStates: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  syncExternalPostLikeState: vi.fn(),
  syncExternalPostRepostState: vi.fn(),
  syncExternalPostBookmarkState: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/topicService', () => ({ getTopicPosts: mocks.getTopicPosts }));
vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
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
  syncExternalPostLikeState: mocks.syncExternalPostLikeState,
  syncExternalPostRepostState: mocks.syncExternalPostRepostState,
  syncExternalPostBookmarkState: mocks.syncExternalPostBookmarkState,
}));

import { useTopicSessionStore } from './topicSession';

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
    vi.clearAllMocks();
    mocks.getTopicPosts.mockImplementation(async (slug: string) => ({
      topic: topic(slug), items: [], next_cursor: null,
    }));
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
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
    expect(mocks.syncExternalPostLikeState).toHaveBeenCalledWith({ postId: 1, likes: 11, liked: true, status: 'ready' });

    await expect(store.toggleRepost(1)).resolves.toBe('succeeded');
    expect(store.items[0].repostCount).toBe(5);
    expect(mocks.syncExternalPostRepostState).toHaveBeenCalledWith({ postId: 1, reposts: 5, reposted: true, status: 'ready' });
    await expect(store.toggleBookmark(1)).resolves.toBe('succeeded');
    expect(store.items[0].bookmarked).toBe(true);
    expect(mocks.syncExternalPostBookmarkState).toHaveBeenCalledWith({ postId: 1, bookmarked: true, status: 'ready' });
    expect(store.likePendingPostIDs.size + store.repostPendingPostIDs.size + store.bookmarkPendingPostIDs.size).toBe(0);
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
