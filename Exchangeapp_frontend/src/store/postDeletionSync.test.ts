// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post, PostReference } from '../types/Post';
import { postToFeedPost } from '../utils/feedPost';

const mocks = vi.hoisted(() => ({
  auth: null as any,
  quotes: vi.fn(), search: vi.fn(), history: vi.fn(), bookmarks: vi.fn(),
  topic: vi.fn(), following: vi.fn(), timeline: vi.fn(), engagement: vi.fn(),
  recommendations: vi.fn(),
  like: vi.fn(), bookmark: vi.fn(),
}));
vi.mock('./auth', () => ({ useAuthStore: () => mocks.auth }));
vi.mock('../services/quoteService', () => ({ getPostQuotes: mocks.quotes }));
vi.mock('../services/postSearchService', () => ({ searchPosts: mocks.search }));
vi.mock('../services/historyService', () => ({ getLikedHistory: mocks.history }));
vi.mock('../services/topicService', () => ({ getTopicPosts: mocks.topic }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.engagement }));
vi.mock('../services/recommendationService', async (original) => ({
  ...await original<typeof import('../services/recommendationService')>(), getPostRecommendations: mocks.recommendations,
}));
vi.mock('../services/bookmarkService', async (original) => ({
  ...await original<typeof import('../services/bookmarkService')>(), getBookmarks: mocks.bookmarks,
}));
vi.mock('../services/postService', async (original) => ({
  ...await original<typeof import('../services/postService')>(), getFollowingTimeline: mocks.following,
}));
vi.mock('../services/userService', async (original) => ({
  ...await original<typeof import('../services/userService')>(), getUserTimeline: mocks.timeline,
}));
vi.mock('./engagementOperations', async (original) => ({
  ...await original<typeof import('./engagementOperations')>(),
  executeLikeToggle: mocks.like, executeBookmarkToggle: mocks.bookmark,
}));

import { useFeedStore } from './feed';
import { useHomeTimelineStore } from './homeTimeline';
import { useProfileSessionStore } from './profileSession';
import { usePostSearchSessionStore } from './postSearchSession';
import { useHistorySessionStore } from './historySession';
import { useBookmarksSessionStore } from './bookmarksSession';
import { useTopicSessionStore } from './topicSession';
import { useQuotesSessionStore } from './quotesSession';
import { syncExternalPostRemoval, syncHomePostRemoval, syncProfilePostRemoval } from './sessionSync';

const reference = (): PostReference => ({
  id: 99, deleted: false,
  author: { id: 7, username: 'viewer', display_name: 'Viewer', avatar_url: '' },
  content: 'Deleted reference body', published_at: '2026-10-03T00:00:00Z',
  media: [{ type: 'image', url: '/deleted.png', large_url: '/deleted-large.png', width: 10, height: 10, position: 0 }],
});
const post = (id = 101): Post => ({
  id, created_at: '2026-10-03T00:00:00Z', updated_at: '2026-10-03T00:00:00Z', published_at: '2026-10-03T00:00:00Z',
  author: { id: 7, username: 'viewer', display_name: 'Viewer', avatar_url: '' },
  content: `Parent ${id}`, language: 'en', conversation_id: id,
  reply_to_post_id: 99, quote_post_id: 99, reply_to_post: reference(), quote_post: reference(),
  visibility: 'public', media: [], like_count: 1, repost_count: 0, reply_count: 0, quote_count: 0, view_count: 0, deleted: false,
});
const page = (items: Post[], next_cursor: string | null = null) => ({ items, next_cursor });
const activity = (value: Post) => ({
  activity_type: 'post' as const, activity_at: value.published_at, source_id: value.id, actor: value.author, post: value,
});
const criteria = { query: 'parent', authorId: null, time: { kind: 'any' as const }, sort: 'latest' as const };
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const remove = (id: number, sync = syncExternalPostRemoval) => {
  expect(useFeedStore().markPostDeleted(id, 7)).toBe(true);
  sync(id);
};
const expectTombstones = (value: ReturnType<typeof postToFeedPost>) => {
  expect(value).toMatchObject({ content: `Parent ${value.id}`, quotePost: { id: 99, deleted: true }, replyToPost: { id: 99, deleted: true } });
  expect(value.quotePost).toEqual({ id: 99, deleted: true });
  expect(value.replyToPost).toEqual({ id: 99, deleted: true });
};

describe('Post deletion across cached surfaces and delayed responses', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.auth = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    setActivePinia(createPinia());
    mocks.engagement.mockResolvedValue({ items: [] });
    mocks.quotes.mockResolvedValue(page([]));
    mocks.search.mockResolvedValue(page([]));
    mocks.history.mockResolvedValue(page([]));
    mocks.bookmarks.mockResolvedValue(page([]));
    mocks.following.mockResolvedValue(page([]));
    mocks.timeline.mockResolvedValue(page([]));
    mocks.recommendations.mockResolvedValue({ items: [], depleted: true });
    mocks.topic.mockResolvedValue({ ...page([]), topic: { slug: 'test', name: 'Test' } });
  });

  it.each([syncExternalPostRemoval, syncHomePostRemoval, syncProfilePostRemoval])(
    'purges reference data throughout all live caches through every deletion entry point', sync => {
      const feed = useFeedStore();
      const home = useHomeTimelineStore();
      const profile = useProfileSessionStore();
      const search = usePostSearchSessionStore();
      const history = useHistorySessionStore();
      const bookmarks = useBookmarksSessionStore();
      const topic = useTopicSessionStore();
      const quotes = useQuotesSessionStore();
      feed.registerPublishedPost(post(), 7);
      home.following.items = [postToFeedPost(post())];
      home.forYou.items = [{ recommendation: { post: post(), score: 1 }, post: postToFeedPost(post()) }];
      const cachedProfile = profile.ensureSession(7)!;
      cachedProfile.timelineItems = [{ activityType: 'post', activityAt: '', sourceId: 101, actor: post().author, post: postToFeedPost(post()) }];
      for (const store of [search, history, bookmarks, topic, quotes]) store.items = [postToFeedPost(post())];
      // Home/Profile callers also evict their own cache before broadcasting.
      if (sync === syncHomePostRemoval) home.removePostLocal(99);
      if (sync === syncProfilePostRemoval) profile.removePostEverywhereLocal(99);
      remove(99, sync);
      expect(home.forYou.items[0].recommendation.post.quote_post).toEqual({ id: 99, deleted: true });
      for (const value of [feed.recentlyPublishedPosts[0], home.following.items[0], home.forYou.items[0].post, cachedProfile.timelineItems[0].post,
        search.items[0], history.items[0], bookmarks.items[0], topic.items[0], quotes.items[0]]) expectTombstones(value);
    },
  );

  it('normalizes both the rendered card and the cached payload of a delayed recommendation', async () => {
    const response = deferred<any>();
    mocks.recommendations.mockReturnValueOnce(response.promise);
    const store = useHomeTimelineStore();
    const request = store.loadForYou();
    remove(99);
    response.resolve({ items: [{ post: post(), score: 1 }], depleted: true });
    await request;
    expect(store.forYou.items).toHaveLength(1);
    expectTombstones(store.forYou.items[0].post);
    expect(store.forYou.items[0].recommendation.post.quote_post).toEqual({ id: 99, deleted: true });
    expect(store.forYou.items[0].recommendation.post.reply_to_post).toEqual({ id: 99, deleted: true });
  });

  it.each(['search', 'history', 'bookmarks', 'topic', 'following', 'profile', 'quotes'] as const)(
    'normalizes stale %s response references using the shared known-deleted registry', async surface => {
      const response = deferred<any>();
      const endpoint = surface === 'profile' ? mocks.timeline : mocks[surface];
      endpoint.mockReturnValueOnce(response.promise);
      let result!: () => ReturnType<typeof postToFeedPost>[];
      let loading: Promise<unknown> | undefined;
      if (surface === 'search') {
        const store = usePostSearchSessionStore(); store.activateCriteria(criteria); result = () => store.items;
      } else if (surface === 'history') {
        const store = useHistorySessionStore(); loading = store.loadInitial(); result = () => store.items;
      } else if (surface === 'bookmarks') {
        const store = useBookmarksSessionStore(); loading = store.loadInitial(); result = () => store.items;
      } else if (surface === 'topic') {
        const store = useTopicSessionStore(); loading = store.setTopic('test'); result = () => store.items;
      } else if (surface === 'following') {
        const store = useHomeTimelineStore(); loading = store.loadFollowing(); result = () => store.following.items;
      } else if (surface === 'profile') {
        const store = useProfileSessionStore(); loading = store.loadTimeline(7); result = () => store.getSession(7)!.timelineItems.map(item => item.post);
      } else {
        const store = useQuotesSessionStore(); loading = store.setTarget(42); result = () => store.items;
      }
      remove(99);
      response.resolve(surface === 'following' || surface === 'profile' ? page([activity(post())] as any)
        : surface === 'topic' ? { ...page([post()]), topic: { slug: 'test', name: 'Test' } } : page([post()]));
      await loading;
      await flushPromises();
      expect(result()).toHaveLength(1);
      expectTombstones(result()[0]);
    },
  );

  it('filters deleted rows and references after search criteria reset', async () => {
    usePostSearchSessionStore();
    remove(99);
    mocks.search.mockResolvedValueOnce(page([post(99), post()]));
    const store = usePostSearchSessionStore(); store.activateCriteria({ ...criteria, query: 'new' });
    await flushPromises();
    expect(store.items.map(value => value.id)).toEqual([101]);
    expectTombstones(store.items[0]);
  });

  it.each(['history', 'bookmarks'] as const)('purges %s rollback snapshots without deleting the parent', async surface => {
    const request = deferred<any>();
    const store = surface === 'history' ? useHistorySessionStore() : useBookmarksSessionStore();
    const value = postToFeedPost(post()); value.liked = true; value.likeStatus = 'ready'; value.bookmarked = true; value.bookmarkStatus = 'ready';
    store.items = [value];
    const operation = surface === 'history'
      ? (mocks.like.mockReturnValueOnce(request.promise), useHistorySessionStore().toggleUnlike(101))
      : (mocks.bookmark.mockReturnValueOnce(request.promise), useBookmarksSessionStore().toggleBookmark(101));
    expect(store.items).toHaveLength(0);
    remove(99);
    request.reject(new Error('failed'));
    await operation;
    expect(store.items).toHaveLength(1);
    expectTombstones(store.items[0]);
  });

  it.each(['initial', 'paging'] as const)('marks a deleted Quotes target unavailable and ignores pending %s', async phase => {
    const response = deferred<any>();
    const store = useQuotesSessionStore();
    if (phase === 'paging') {
      mocks.quotes.mockResolvedValueOnce(page([post()], 'next'));
      await store.setTarget(42);
    }
    mocks.quotes.mockReturnValueOnce(response.promise);
    const request = phase === 'initial' ? store.setTarget(42) : store.loadMore();
    remove(42);
    expect(store.targetUnavailable).toBe(true);
    expect(store.initialLoading || store.loadingMore).toBe(false);
    response.resolve(page([post()], 'old'));
    await request;
    await store.retryInitial();
    expect(store.items).toEqual([]);
    expect(store.nextCursor).toBeNull();
    expect(store.targetUnavailable).toBe(true);
    expect(mocks.quotes).toHaveBeenCalledTimes(phase === 'initial' ? 1 : 2);
  });

  it('filters a deleted Quote row from pending pagination, refresh and hydration', async () => {
    const response = deferred<any>(); const hydration = deferred<any>();
    mocks.quotes.mockResolvedValueOnce(page([post()], 'next'));
    mocks.engagement.mockReturnValueOnce(hydration.promise);
    const store = useQuotesSessionStore(); await store.setTarget(42);
    mocks.quotes.mockReturnValueOnce(response.promise);
    const request = store.loadMore();
    remove(101);
    response.resolve(page([post(), post(102)]));
    hydration.resolve({ items: [{ post_id: 101, like: { status: 'ready', likes: 999, liked: true } }] });
    await request; await flushPromises();
    expect(store.items.map(value => value.id)).toEqual([102]);
    mocks.quotes.mockResolvedValueOnce(page([post(), post(102)]));
    await store.retryInitial();
    expect(store.items.map(value => value.id)).toEqual([102]);
  });

  it.each(['row', 'target'] as const)('invalidates a pending Quote mutation on %s deletion without fanout', async deleted => {
    const request = deferred<any>();
    mocks.quotes.mockResolvedValueOnce(page([post()]));
    const store = useQuotesSessionStore(); await store.setTarget(42); await flushPromises();
    store.items[0].likeStatus = 'ready';
    mocks.like.mockReturnValueOnce(request.promise);
    const operation = store.toggleLike(101);
    const search = usePostSearchSessionStore(); search.items = [postToFeedPost(post())];
    remove(deleted === 'row' ? 101 : 42);
    request.resolve({ likes: 999, liked: true });
    expect(await operation).toBe('ignored');
    expect(store.likePendingPostIDs.has(101)).toBe(false);
    if (deleted === 'target') expect(search.items[0].likeCount).toBe(1);
  });

  it('respects a target deleted before Quotes exists and keeps deletion knowledge across target switches', async () => {
    remove(42);
    const store = useQuotesSessionStore(); await store.setTarget(42);
    expect(store.targetUnavailable).toBe(true);
    expect(mocks.quotes).not.toHaveBeenCalled();
    await store.setTarget(43); await store.setTarget(42);
    expect(store.targetUnavailable).toBe(true);
    expect(mocks.quotes).toHaveBeenCalledOnce();
    mocks.auth.currentIdentity = { id: 8 };
    await nextTick();
    expect(useFeedStore().isPostDeleted(42)).toBe(false);
    await store.setTarget(42);
    expect(store.targetUnavailable).toBe(false);
  });
});
