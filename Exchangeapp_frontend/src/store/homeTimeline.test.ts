import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost } from '../types/Feed';
import { engagementResponseFromBatchMocks } from '../test-utils/engagementServiceMock';
import {
  captureBookmarkStateSyncVersion,
  syncHydratedPostBookmarkState,
} from './sessionSync';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
    token: string | null;
  } | null,
  feedStore: null as {
    viewerID: number | null;
    recentlyPublishedPosts: FeedPost[];
    isPostDeleted: ReturnType<typeof vi.fn>;
    markPostDeleted: ReturnType<typeof vi.fn>;
    replaceAuthorIdentity: ReturnType<typeof vi.fn>;
    applyLikeStateUpdate: ReturnType<typeof vi.fn>;
  } | null,
  getPostRecommendations: vi.fn(),
  getPublicPostRecommendations: vi.fn(),
  getFollowingTimeline: vi.fn(),
  getPostLikeStates: vi.fn(),
  getPostEngagementStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  getUser: vi.fn(),
  getUserTimeline: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  deletePost: vi.fn(),
}));

vi.mock('./auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('./feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/recommendationService', () => ({
  getPostRecommendations: mocks.getPostRecommendations,
  getPublicPostRecommendations: mocks.getPublicPostRecommendations,
}));

vi.mock('../services/postService', () => ({
  getFollowingTimeline: mocks.getFollowingTimeline,
  deletePost: mocks.deletePost,
}));

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

vi.mock('../services/engagementService', () => ({
  getPostEngagementStates: mocks.getPostEngagementStates,
}));

vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserTimeline: mocks.getUserTimeline,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

import {
  useHomeTimelineStore,
} from './homeTimeline';
import { useProfileSessionStore } from './profileSession';
import {
  isEngagementMutationLeased,
} from './engagementMutationLease';
import { GUEST_RECOMMENDATION_SESSION_STORAGE_KEY } from '../utils/guestRecommendationSession';

const guestSessionID = '4ca3706b-197e-4f63-8f51-f99176f8b61c';

const author = (id = 7) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
});
const post = (id: number, authorID = 7, repostCount = 0) => ({
  id,
  created_at: '2026-08-24T00:00:00.000Z',
  updated_at: '2026-08-24T00:00:00.000Z',
  published_at: '2026-08-24T00:00:00.000Z',
  author: author(authorID),
  content: `Body ${id}`,
  language: 'und' as const,
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public' as const,
  media: [],
  like_count: 0,
  repost_count: repostCount,
  reply_count: 0,
  view_count: 0,
  deleted: false as const,
});

const recommendation = (id: number, repostCount = 0) => ({
  post: post(id, 7, repostCount),
  score: 1,
});

const recommendationPage = (
  items: ReturnType<typeof recommendation>[],
  depleted = false,
) => ({
  items,
  request_id: 'request-id',
  depleted,
});

const followingActivity = (
  id: number,
  postAuthorID = 7,
  actorID = postAuthorID,
  activityType: 'post' | 'repost' = 'post',
) => ({
  activity_type: activityType,
  activity_at: '2026-08-24T00:00:00.000Z',
  source_id: id,
  actor: author(actorID),
  post: post(id, postAuthorID),
});

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

const createSessionStorage = (): Storage => {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
    clear: () => values.clear(),
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    get length() {
      return values.size;
    },
  } as Storage;
};

let testSessionStorage: Storage;

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(resolvePromise => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('home timeline session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    testSessionStorage = createSessionStorage();
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        sessionStorage: testSessionStorage,
        crypto: { randomUUID: vi.fn(() => guestSessionID) },
      },
    });
    testSessionStorage.clear();
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7 },
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      viewerID: 7,
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
      markPostDeleted: vi.fn().mockReturnValue(true),
      replaceAuthorIdentity: vi.fn(),
      applyLikeStateUpdate: vi.fn(),
    });
    mocks.getPostRecommendations.mockReset();
    mocks.getPublicPostRecommendations.mockReset();
    mocks.getFollowingTimeline.mockReset();
    mocks.getPostLikeStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.likePost.mockReset();
    mocks.unlikePost.mockReset();
    mocks.getPostRepostStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.repostPost.mockReset();
    mocks.undoRepostPost.mockReset();
    mocks.getPostBookmarkStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.bookmarkPost.mockReset();
    mocks.unbookmarkPost.mockReset();
    mocks.getUser.mockReset();
    mocks.getUserTimeline.mockReset();
    mocks.getUserFollowState.mockReset();
    mocks.followUser.mockReset();
    mocks.unfollowUser.mockReset();
    mocks.deletePost.mockReset().mockResolvedValue(undefined);
    mocks.getPostEngagementStates.mockReset().mockImplementation((postIDs: number[]) => engagementResponseFromBatchMocks(postIDs, mocks));
  });

  it('does not refetch a loaded tab after clean Home re-entry', async () => {
    mocks.getPostRecommendations.mockResolvedValue(recommendationPage([recommendation(1)]));
    mocks.getFollowingTimeline.mockResolvedValue({
      items: [followingActivity(2)],
      next_cursor: null,
    });
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadForYou();
    store.setActiveTab('following');
    await store.loadFollowing();
    await store.loadFollowing();
    await settle();

    expect(mocks.getPostRecommendations).toHaveBeenCalledTimes(1);
    expect(mocks.getPostRecommendations).toHaveBeenCalledWith(20);
    expect(mocks.getFollowingTimeline).toHaveBeenCalledTimes(1);
    expect(store.forYou.items).toHaveLength(1);
    expect(store.following.items).toHaveLength(1);
  });

  it('appends multiple For You pages without replacing earlier posts', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1), recommendation(2), recommendation(3)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(4), recommendation(5), recommendation(6)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();

    expect(mocks.getPostRecommendations).toHaveBeenNthCalledWith(1, 20);
    expect(mocks.getPostRecommendations).toHaveBeenNthCalledWith(2, 20);
    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 2, 3, 4, 5, 6]);
    expect(store.forYou.depleted).toBe(false);
  });

  it('deduplicates page responses and skips locally deleted posts', async () => {
    mocks.feedStore!.isPostDeleted.mockImplementation((id: number) => id === 2);
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1), recommendation(2), recommendation(3)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(3), recommendation(4), recommendation(2)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();
    await settle();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 3, 4]);
    expect(mocks.getPostLikeStates).toHaveBeenLastCalledWith([4]);
    expect(mocks.getPostRepostStates).toHaveBeenLastCalledWith([4]);
  });

  it('suppresses concurrent For You paging calls', async () => {
    const pending = deferred<ReturnType<typeof recommendationPage>>();
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockReturnValueOnce(pending.promise);
    const store = useHomeTimelineStore();

    await store.loadForYou();
    const first = store.loadMoreForYou();
    const second = store.loadMoreForYou();

    expect(mocks.getPostRecommendations).toHaveBeenCalledTimes(2);
    expect(store.forYou.loadingMore).toBe(true);
    pending.resolve(recommendationPage([recommendation(2)]));
    await Promise.all([first, second]);

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 2]);
    expect(store.forYou.loadingMore).toBe(false);
  });

  it('keeps the existing feed when paging fails and retries separately', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(recommendationPage([recommendation(2)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1]);
    expect(store.forYou.loadMoreError).toBe(true);
    expect(store.forYou.error).toBe(false);
    expect(store.forYou.depleted).toBe(false);

    store.retryForYouLoadMore();
    await settle();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 2]);
    expect(store.forYou.loadMoreError).toBe(false);
  });

  it('stops paging after a depleted response', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockResolvedValueOnce(recommendationPage([], true));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();
    await store.loadMoreForYou();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1]);
    expect(store.forYou.depleted).toBe(true);
    expect(mocks.getPostRecommendations).toHaveBeenCalledTimes(2);
  });

  it('replaces the accumulated For You feed on force refresh', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(2)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(9)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();
    await store.loadForYou(true);

    expect(store.forYou.items.map(item => item.post.id)).toEqual([9]);
    expect(store.forYou.loaded).toBe(true);
    expect(store.forYou.loadingMore).toBe(false);
    expect(store.forYou.loadMoreError).toBe(false);
  });

  it('stops after two successful no-progress pages', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();
    expect(store.forYou.depleted).toBe(false);
    await store.loadMoreForYou();
    await store.loadMoreForYou();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1]);
    expect(store.forYou.depleted).toBe(true);
    expect(mocks.getPostRecommendations).toHaveBeenCalledTimes(3);
  });

  it('hydrates only newly appended posts for later pages', async () => {
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1), recommendation(2)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(2), recommendation(3)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await settle();
    mocks.getPostLikeStates.mockClear();
    mocks.getPostRepostStates.mockClear();

    await store.loadMoreForYou();
    await settle();

    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([3]);
    expect(mocks.getPostRepostStates).toHaveBeenCalledWith([3]);
  });

  it('keeps later-page Like and Repost hydration valid when the next page starts', async () => {
    const page3Pending = deferred<ReturnType<typeof recommendationPage>>();
    const page2LikePending = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const page2RepostPending = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(2)]))
      .mockReturnValueOnce(page3Pending.promise);
    mocks.getPostLikeStates
      .mockResolvedValueOnce({ items: [], unavailable_post_ids: [] })
      .mockReturnValueOnce(page2LikePending.promise);
    mocks.getPostRepostStates
      .mockResolvedValueOnce({ items: [], unavailable_post_ids: [] })
      .mockReturnValueOnce(page2RepostPending.promise);
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await settle();
    await store.loadMoreForYou();

    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 2]);
    expect(store.forYou.items[1].post).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });

    const page3Request = store.loadMoreForYou();
    expect(mocks.getPostRecommendations).toHaveBeenCalledTimes(3);
    expect(store.forYou.loadingMore).toBe(true);

    page2LikePending.resolve({
      items: [{ post_id: 2, likes: 7, liked: true }],
      unavailable_post_ids: [],
    });
    page2RepostPending.resolve({
      items: [{ post_id: 2, reposts: 9, reposted: true }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(store.forYou.items[1].post).toMatchObject({
      likeCount: 7,
      liked: true,
      likeStatus: 'ready',
      repostCount: 9,
      reposted: true,
      repostStatus: 'ready',
    });

    page3Pending.resolve(recommendationPage([], true));
    await page3Request;
  });

  it('does not let older Like hydration overwrite a newer mutation', async () => {
    const hydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getFollowingTimeline.mockResolvedValue({
      items: [followingActivity(31)],
      next_cursor: null,
    });
    mocks.getPostLikeStates.mockReturnValueOnce(hydration.promise);
    mocks.likePost.mockResolvedValueOnce({ likes: 1, liked: true });
    const store = useHomeTimelineStore();

    await store.loadFollowing();
    const post = store.following.items[0];
    expect(post.likeStatus).toBe('unknown');

    // Make the row actionable while the earlier hydration request is still pending.
    post.likeStatus = 'ready';
    await expect(store.toggleLike(post.id)).resolves.toBe('succeeded');

    hydration.resolve({
      items: [{ post_id: post.id, likes: 99, liked: false }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(post).toMatchObject({ likeCount: 1, liked: true, likeStatus: 'ready' });
  });

  it('drops a late request when the authenticated viewer changes', async () => {
    let resolveRecommendations!: (response: ReturnType<typeof recommendationPage>) => void;
    const pending = new Promise<ReturnType<typeof recommendationPage>>(resolve => {
      resolveRecommendations = resolve;
    });
    mocks.getPostRecommendations.mockReturnValue(pending);
    const store = useHomeTimelineStore();
    const request = store.loadForYou();

    store.setViewer(8);
    resolveRecommendations(recommendationPage([recommendation(9)]));
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.forYou.items).toHaveLength(0);
    expect(store.forYou.loaded).toBe(false);
  });

  it('drops a late guest response when authentication begins', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    mocks.feedStore!.viewerID = null;
    const pending = deferred<ReturnType<typeof recommendationPage>>();
    mocks.getPublicPostRecommendations.mockReturnValue(pending.promise);
    const store = useHomeTimelineStore();
    const request = store.loadForYou();

    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = { id: 8 };
    await settle();

    pending.resolve(recommendationPage([recommendation(99)]));
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.forYou.items).toHaveLength(0);
    expect(store.forYou.loaded).toBe(false);
    expect(testSessionStorage.getItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY)).toBe(guestSessionID);
  });

  it('drops a late authenticated response after logout begins', async () => {
    const pending = deferred<ReturnType<typeof recommendationPage>>();
    mocks.getPostRecommendations.mockReturnValue(pending.promise);
    const store = useHomeTimelineStore();
    const request = store.loadForYou();

    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    mocks.feedStore!.viewerID = null;
    await settle();

    mocks.getPublicPostRecommendations.mockResolvedValue(recommendationPage([recommendation(77)]));
    const guestRequest = store.loadForYou();
    pending.resolve(recommendationPage([recommendation(88)]));
    await Promise.all([request, guestRequest]);

    expect(store.viewerID).toBe(null);
    expect(store.forYou.items.map(item => item.post.id)).toEqual([77]);
    expect(mocks.getPublicPostRecommendations).toHaveBeenCalledWith({
      limit: 20,
      guestSessionId: guestSessionID,
    });
  });

  it('keeps independent tab scrollTop values, then clears both for a new viewer', () => {
    const store = useHomeTimelineStore();

    store.setActiveTab('following');
    store.setScrollTop('for-you', 2400);
    store.setScrollTop('following', 900);
    expect(store.activeTab).toBe('following');
    expect(store.scrollTop['for-you']).toBe(2400);
    expect(store.scrollTop.following).toBe(900);

    store.setViewer(8);
    expect(store.activeTab).toBe('for-you');
    expect(store.scrollTop['for-you']).toBe(0);
    expect(store.scrollTop.following).toBe(0);
  });

  it('loads guest For You pages without engagement hydration and uses one tab session', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    mocks.feedStore!.viewerID = null;
    mocks.getPublicPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1, 8)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(2)], true));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadMoreForYou();
    await settle();

    expect(mocks.getPublicPostRecommendations).toHaveBeenNthCalledWith(1, {
      limit: 20,
      guestSessionId: guestSessionID,
    });
    expect(mocks.getPublicPostRecommendations).toHaveBeenNthCalledWith(2, {
      limit: 20,
      guestSessionId: guestSessionID,
    });
    expect(mocks.getPostLikeStates).not.toHaveBeenCalled();
    expect(mocks.getPostRepostStates).not.toHaveBeenCalled();
    expect(mocks.getPostBookmarkStates).not.toHaveBeenCalled();
    expect(store.forYou.items[0].post).toMatchObject({
      repostCount: 8,
      reposted: false,
      likeStatus: 'ready',
      repostStatus: 'ready',
      bookmarkStatus: 'ready',
    });
  });

  it('preserves the guest tab session across force refresh', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    mocks.feedStore!.viewerID = null;
    mocks.getPublicPostRecommendations
      .mockResolvedValueOnce(recommendationPage([recommendation(1), recommendation(2), recommendation(3)]))
      .mockResolvedValueOnce(recommendationPage([recommendation(4), recommendation(5), recommendation(6)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadForYou(true);

    expect(mocks.getPublicPostRecommendations).toHaveBeenNthCalledWith(2, {
      limit: 20,
      guestSessionId: guestSessionID,
    });
    expect(window.sessionStorage.getItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY))
      .toBe(guestSessionID);
  });

  it('continues guest serving when session storage cannot create a UUID', async () => {
    const setItem = vi.spyOn(testSessionStorage, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    mocks.feedStore!.viewerID = null;
    mocks.getPublicPostRecommendations.mockResolvedValue(recommendationPage([recommendation(4)]));
    const store = useHomeTimelineStore();

    try {
      await store.loadForYou();

      expect(store.forYou.items.map(item => item.post.id)).toEqual([4]);
      expect(mocks.getPublicPostRecommendations).toHaveBeenCalledWith({
        limit: 20,
        guestSessionId: null,
      });
      expect(setItem).toHaveBeenCalled();
    } finally {
      setItem.mockRestore();
    }
  });

  it('keeps authenticated recommendations independent from guest storage', async () => {
    testSessionStorage.setItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY, guestSessionID);
    mocks.getPostRecommendations.mockResolvedValue(recommendationPage([recommendation(9)]));
    const store = useHomeTimelineStore();

    await store.loadForYou();

    expect(mocks.getPostRecommendations).toHaveBeenCalledWith(20);
    expect(mocks.getPublicPostRecommendations).not.toHaveBeenCalled();
    expect(testSessionStorage.getItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY)).toBe(guestSessionID);
  });

  it('increments the Home reselect intent without resetting it for a new viewer', () => {
    const store = useHomeTimelineStore();
    const before = store.homeReselectVersion;

    store.requestHomeReselect();

    expect(store.homeReselectVersion).toBe(before + 1);
    store.setViewer(8);
    expect(store.homeReselectVersion).toBe(before + 1);
  });

  it('normalizes invalid scrollTop values to zero', () => {
    const store = useHomeTimelineStore();
    store.setScrollTop('for-you', 2400);

    store.setScrollTop('for-you', -100);
    expect(store.scrollTop['for-you']).toBe(0);

    store.setScrollTop('for-you', Number.NaN);
    expect(store.scrollTop['for-you']).toBe(0);

    store.setScrollTop('for-you', Number.POSITIVE_INFINITY);
    expect(store.scrollTop['for-you']).toBe(0);
  });

  it('dismisses a recommendation without removing the same Post from Following', () => {
    const store = useHomeTimelineStore();
    const followingPost: FeedPost = {
      id: 4,
      author: author(),
      content: 'Following',
      language: 'und',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    };
    store.forYou.items = [{ recommendation: recommendation(4), post: { ...followingPost } }];
    store.following.items = [followingPost];

    expect(store.dismissRecommendation(4)).toBe(true);
    expect(store.forYou.items).toHaveLength(0);
    expect(store.following.items).toHaveLength(1);
    expect(store.following.items[0].id).toBe(4);
    expect(mocks.feedStore!.markPostDeleted).not.toHaveBeenCalled();
  });

  it('applies a like update to every Home surface and removes deleted Posts', () => {
    const store = useHomeTimelineStore();
    mocks.feedStore!.recentlyPublishedPosts = [{
      id: 4,
      author: author(),
      content: 'Recent',
      language: 'und',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 3,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    }];
    store.following.items = [{
      id: 4,
      author: author(),
      content: 'Following',
      language: 'und',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 3,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    }];
    store.forYou.items = [
      { recommendation: recommendation(4), post: { ...store.following.items[0] } },
    ];

    store.applyLikeStateUpdate({ postId: 4, likes: 4, liked: true, status: 'ready' });
    expect(mocks.feedStore!.recentlyPublishedPosts[0].liked).toBe(true);
    expect(store.following.items[0].likeCount).toBe(4);
    expect(store.forYou.items[0].post.likeCount).toBe(4);

    expect(store.removePost(4, 7)).toBe(true);
    expect(store.following.items).toHaveLength(0);
    expect(store.forYou.items).toHaveLength(0);
    expect(mocks.feedStore!.markPostDeleted).toHaveBeenCalledWith(4, 7);
  });

  it('optimistically toggles Like and settles a successful mutation', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.likeCount = 8;
    store.following.items = [post];
    mocks.likePost.mockResolvedValue({ likes: 9, liked: true });

    const request = store.toggleLike(4);
    expect(post.likeCount).toBe(9);
    expect(post.liked).toBe(true);
    expect(store.likePendingPostIds.has(4)).toBe(true);
    expect(await request).toBe('succeeded');
    expect(post.likeCount).toBe(9);
    expect(post.liked).toBe(true);
    expect(store.likePendingPostIds.has(4)).toBe(false);
  });

  it('blocks a Profile Unlike while Home Like is pending, then permits it after Home settles', async () => {
    const homeStore = useHomeTimelineStore();
    const profileStore = useProfileSessionStore();
    const homePost = feedPostFixture(42, 8);
    const profilePost = feedPostFixture(42, 8);
    homeStore.following.items = [homePost];
    const profileSession = profileStore.ensureSession(7)!;
    profileSession.timelineItems = [{
      activityType: 'post',
      activityAt: profilePost.createdAt,
      sourceId: 42,
      actor: profilePost.author,
      post: profilePost,
    }];

    const pendingLike = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(pendingLike.promise);
    const homeMutation = homeStore.toggleLike(42);

    expect(mocks.likePost).toHaveBeenCalledTimes(1);
    expect(homePost).toMatchObject({ liked: true, likeCount: 1 });
    expect(profilePost).toMatchObject({ liked: true, likeCount: 1 });
    expect(isEngagementMutationLeased(7, 'like', 42)).toBe(true);

    await expect(profileStore.toggleLike(42, 7)).resolves.toBe('ignored');
    expect(mocks.likePost).toHaveBeenCalledTimes(1);
    expect(mocks.unlikePost).not.toHaveBeenCalled();
    expect(profilePost).toMatchObject({ liked: true, likeCount: 1 });

    pendingLike.resolve({ likes: 1, liked: true });
    await expect(homeMutation).resolves.toBe('succeeded');
    expect(isEngagementMutationLeased(7, 'like', 42)).toBe(false);

    mocks.unlikePost.mockResolvedValueOnce({ likes: 0, liked: false });
    await expect(profileStore.toggleLike(42, 7)).resolves.toBe('succeeded');
    expect(mocks.unlikePost).toHaveBeenCalledTimes(1);
    expect(homePost).toMatchObject({ liked: false, likeCount: 0 });
    expect(profilePost).toMatchObject({ liked: false, likeCount: 0 });
  });

  it('rolls back a failed Like mutation and reports failed', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.likeCount = 8;
    store.following.items = [post];
    mocks.likePost.mockRejectedValue(new Error('like failed'));

    const request = store.toggleLike(4);
    expect(post.likeCount).toBe(9);
    expect(post.liked).toBe(true);
    expect(await request).toBe('failed');
    expect(post.likeCount).toBe(8);
    expect(post.liked).toBe(false);
    expect(post.likeStatus).toBe('ready');
    expect(store.likePendingPostIds.has(4)).toBe(false);
  });

  it('rolls back a failed Unlike mutation to the previous liked state and count', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.likeCount = 8;
    post.liked = true;
    store.following.items = [post];
    mocks.unlikePost.mockRejectedValue(new Error('unlike failed'));

    const request = store.toggleLike(4);
    expect(post.likeCount).toBe(7);
    expect(post.liked).toBe(false);
    expect(await request).toBe('failed');
    expect(post.likeCount).toBe(8);
    expect(post.liked).toBe(true);
    expect(store.likePendingPostIds.has(4)).toBe(false);
  });

  it('preserves Like 503 unavailable semantics while reporting failed', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.likeCount = 8;
    store.following.items = [post];
    mocks.likePost.mockRejectedValue({ response: { status: 503 } });

    expect(await store.toggleLike(4)).toBe('failed');
    expect(post.likeCount).toBe(8);
    expect(post.liked).toBe(false);
    expect(post.likeStatus).toBe('unavailable');
    expect(store.likePendingPostIds.has(4)).toBe(false);
  });

  it('ignores a Like mutation that is not ready', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.likeStatus = 'unknown';
    store.following.items = [post];

    expect(await store.toggleLike(4)).toBe('ignored');
    expect(mocks.likePost).not.toHaveBeenCalled();
  });

  it('ignores a duplicate Like while the current request is pending', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    store.following.items = [post];
    const pending = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(pending.promise);

    const first = store.toggleLike(4);
    await expect(store.toggleLike(4)).resolves.toBe('ignored');
    expect(mocks.likePost).toHaveBeenCalledTimes(1);
    expect(store.likePendingPostIds.has(4)).toBe(true);

    pending.resolve({ likes: 1, liked: true });
    await expect(first).resolves.toBe('succeeded');
    expect(store.likePendingPostIds.has(4)).toBe(false);
  });

  it('batch-hydrates Repost state without changing For You membership', async () => {
    mocks.getPostRecommendations.mockResolvedValue(recommendationPage([recommendation(1), recommendation(2)]));
    mocks.getPostRepostStates.mockResolvedValue({
      items: [{ post_id: 1, reposts: 5, reposted: true }],
      unavailable_post_ids: [2],
    });
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await settle();

    expect(mocks.getPostRepostStates).toHaveBeenCalledWith([1, 2]);
    expect(store.forYou.items.map(item => item.post.id)).toEqual([1, 2]);
    expect(store.forYou.items[0].post).toMatchObject({
      repostCount: 5,
      reposted: true,
      repostStatus: 'ready',
    });
    expect(store.forYou.items[1].post.repostStatus).toBe('unavailable');
  });

  it('optimistically toggles Repost and settles from server authority', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.repostCount = 8;
    post.repostStatus = 'ready';
    store.following.items = [post];
    mocks.repostPost.mockResolvedValue({ reposts: 9, reposted: true });

    const request = store.toggleRepost(4);
    expect(post.repostCount).toBe(9);
    expect(post.reposted).toBe(true);
    expect(store.repostPendingPostIds.has(4)).toBe(true);
    expect(await request).toBe('succeeded');
    expect(post.repostCount).toBe(9);
    expect(post.reposted).toBe(true);
    expect(store.repostPendingPostIds.has(4)).toBe(false);
  });

  it('rolls back a failed Repost mutation and reports failed', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.repostCount = 8;
    store.following.items = [post];
    mocks.repostPost.mockRejectedValue(new Error('repost failed'));

    const request = store.toggleRepost(4);
    expect(post.repostCount).toBe(9);
    expect(post.reposted).toBe(true);
    expect(await request).toBe('failed');
    expect(post.repostCount).toBe(8);
    expect(post.reposted).toBe(false);
    expect(store.repostPendingPostIds.has(4)).toBe(false);
  });

  it('rolls back a failed Undo Repost mutation to the previous reposted state and count', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.repostCount = 8;
    post.reposted = true;
    store.following.items = [post];
    mocks.undoRepostPost.mockRejectedValue(new Error('undo repost failed'));

    const request = store.toggleRepost(4);
    expect(post.repostCount).toBe(7);
    expect(post.reposted).toBe(false);
    expect(await request).toBe('failed');
    expect(post.repostCount).toBe(8);
    expect(post.reposted).toBe(true);
    expect(store.repostPendingPostIds.has(4)).toBe(false);
  });

  it('ignores a stale Repost response after external state wins', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(4, 7);
    post.repostCount = 8;
    post.repostStatus = 'ready';
    store.following.items = [post];
    const pending = deferred<{ reposts: number; reposted: boolean }>();
    mocks.repostPost.mockReturnValue(pending.promise);

    const request = store.toggleRepost(4);
    expect(post.reposted).toBe(true);
    store.applyExternalRepostStateLocal({
      postId: 4,
      reposts: 12,
      reposted: true,
      status: 'ready',
    });
    pending.resolve({ reposts: 9, reposted: true });
    expect(await request).toBe('ignored');
    expect(post.repostCount).toBe(12);
    expect(post.reposted).toBe(true);
    expect(store.repostPendingPostIds.has(4)).toBe(false);
  });

  it('filters Following by activity actor while preserving a followed reposter card', () => {
    const store = useHomeTimelineStore();
    const repostedPost = feedPostFixture(4, 9);
    repostedPost.repostContext = { actor: author(8) };
    const directPost = feedPostFixture(5, 9);
    store.following.items = [repostedPost, directPost];

    store.reconcileFollowStateLocal({
      user_id: 8,
      following: false,
      follower_count: 0,
      following_count: 0,
    });

    expect(store.following.items.map(post => post.id)).toEqual([5]);
    expect(store.following.items[0].author.id).toBe(9);
    expect(store.following.stale).toBe(true);
  });

  it('external like state invalidates an older local like mutation', async () => {
    let resolveLike!: (value: { likes: number; liked: boolean }) => void;
    const pendingLike = new Promise<{ likes: number; liked: boolean }>((resolve) => {
      resolveLike = resolve;
    });
    mocks.likePost.mockReturnValue(pendingLike);
    const store = useHomeTimelineStore();
    store.following.items = [{
      id: 4,
      author: author(),
      content: 'Following',
      language: 'und',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 2,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    }];

    const localMutation = store.toggleLike(4);
    expect(store.likePendingPostIds.has(4)).toBe(true);

    store.applyExternalLikeStateLocal({
      postId: 4,
      likes: 8,
      liked: true,
      status: 'ready',
    });
    expect(store.likePendingPostIds.has(4)).toBe(false);
    expect(store.following.items[0].likeCount).toBe(8);
    expect(store.following.items[0].liked).toBe(true);

    resolveLike({ likes: 3, liked: true });
    expect(await localMutation).toBe('ignored');
    expect(store.following.items[0].likeCount).toBe(8);
  });

  it('blocks a newer Home Like until a stale in-flight request releases its lease', async () => {
    const first = deferred<{ likes: number; liked: boolean }>();
    const second = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const store = useHomeTimelineStore();
    const post = feedPostFixture(44, 7);
    store.following.items = [post];

    const firstMutation = store.toggleLike(44);
    store.applyExternalLikeStateLocal({
      postId: 44,
      likes: 6,
      liked: false,
      status: 'ready',
    });
    const secondMutation = store.toggleLike(44);
    await expect(secondMutation).resolves.toBe('ignored');
    expect(store.likePendingPostIds.has(44)).toBe(false);
    expect(post).toMatchObject({ likeCount: 6, liked: false });
    expect(mocks.likePost).toHaveBeenCalledTimes(1);

    first.resolve({ likes: 90, liked: true });
    await expect(firstMutation).resolves.toBe('ignored');
    expect(store.likePendingPostIds.has(44)).toBe(false);
    expect(post).toMatchObject({ likeCount: 6, liked: false });

    const nextMutation = store.toggleLike(44);
    expect(store.likePendingPostIds.has(44)).toBe(true);
    expect(post).toMatchObject({ likeCount: 7, liked: true });
    second.resolve({ likes: 7, liked: true });
    await expect(nextMutation).resolves.toBe('succeeded');
    expect(store.likePendingPostIds.has(44)).toBe(false);
    expect(post).toMatchObject({ likeCount: 7, liked: true });
  });

  it('ignores an in-flight Like after the authenticated viewer changes', async () => {
    const pending = deferred<{ likes: number; liked: boolean }>();
    mocks.likePost.mockReturnValueOnce(pending.promise);
    const store = useHomeTimelineStore();
    const post = feedPostFixture(45, 7);
    store.following.items = [post];

    const mutation = store.toggleLike(45);
    store.setViewer(8);
    pending.resolve({ likes: 1, liked: true });

    await expect(mutation).resolves.toBe('ignored');
    expect(store.likePendingPostIds.has(45)).toBe(false);
    expect(post).toMatchObject({ likeCount: 1, liked: true });
  });

  it('ignores an in-flight Bookmark after an external state update wins', async () => {
    const pending = deferred<{ bookmarked: boolean }>();
    mocks.bookmarkPost.mockReturnValueOnce(pending.promise);
    const store = useHomeTimelineStore();
    const post = feedPostFixture(46, 7);
    store.following.items = [post];

    const mutation = store.toggleBookmark(46);
    expect(post.bookmarked).toBe(true);
    store.applyExternalBookmarkStateLocal({
      postId: 46,
      bookmarked: false,
      status: 'ready',
    });
    pending.resolve({ bookmarked: true });

    await expect(mutation).resolves.toBe('ignored');
    expect(store.bookmarkPendingPostIds.has(46)).toBe(false);
    expect(post.bookmarked).toBe(false);
  });

  it('rolls back a failed Home Bookmark mutation and settles pending state', async () => {
    const store = useHomeTimelineStore();
    const post = feedPostFixture(47, 7);
    store.following.items = [post];
    mocks.bookmarkPost.mockRejectedValueOnce(new Error('bookmark failed'));

    const mutation = store.toggleBookmark(47);
    expect(post.bookmarked).toBe(true);
    expect(store.bookmarkPendingPostIds.has(47)).toBe(true);

    await expect(mutation).resolves.toBe('failed');
    expect(post.bookmarked).toBe(false);
    expect(store.bookmarkPendingPostIds.has(47)).toBe(false);
  });

  it('updates comment counts across recently published, Following, and For You copies', () => {
    const store = useHomeTimelineStore();
    const post: FeedPost = {
      id: 4,
      author: author(),
      content: 'Post',
      language: 'und',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 1,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
      bookmarked: false,
      bookmarkStatus: 'ready',
    };
    mocks.feedStore!.recentlyPublishedPosts = [{ ...post }];
    store.following.items = [{ ...post }];
    store.forYou.items = [{ recommendation: recommendation(4), post: { ...post } }];

    expect(store.applyReplyCountUpdateLocal({ postId: 4, replyCount: 7 })).toBe(true);
    expect(mocks.feedStore!.recentlyPublishedPosts[0].replyCount).toBe(7);
    expect(store.following.items[0].replyCount).toBe(7);
    expect(store.forYou.items[0].post.replyCount).toBe(7);
  });

  it('reconciles an unfollow by removing only Following posts and marking it stale', () => {
    const store = useHomeTimelineStore();
    store.following.items = [
      { ...feedPostFixture(4, 8) },
      { ...feedPostFixture(5, 7) },
    ];
    store.forYou.items = [{ recommendation: recommendation(4), post: feedPostFixture(4, 8) }];

    store.reconcileFollowStateLocal({
      user_id: 8,
      following: false,
      follower_count: 0,
      following_count: 0,
    });

    expect(store.following.items.map(post => post.id)).toEqual([5]);
    expect(store.forYou.items.map(item => item.post.id)).toEqual([4]);
    expect(store.following.stale).toBe(true);
  });

  it('marks Follow stale without synthesizing posts', () => {
    const store = useHomeTimelineStore();

    store.reconcileFollowStateLocal({
      user_id: 8,
      following: true,
      follower_count: 1,
      following_count: 2,
    });

    expect(store.following.items).toHaveLength(0);
    expect(store.following.stale).toBe(true);
  });

  it('replaces cached Following atomically on successful background revalidation', async () => {
    const store = useHomeTimelineStore();
    store.following.items = [feedPostFixture(1, 7)];
    store.following.loaded = true;
    store.following.nextCursor = 'old-cursor';
    store.following.stale = true;
    mocks.getFollowingTimeline.mockResolvedValue({
      items: [followingActivity(2), followingActivity(2), followingActivity(3)],
      next_cursor: 'fresh-cursor',
    });

    const refresh = store.revalidateFollowing();
    expect(store.following.items.map(post => post.id)).toEqual([1]);
    expect(store.following.loading).toBe(false);
    expect(store.following.revalidating).toBe(true);
    await refresh;

    expect(store.following.items.map(post => post.id)).toEqual([2, 3]);
    expect(store.following.nextCursor).toBe('fresh-cursor');
    expect(store.following.stale).toBe(false);
    expect(store.following.revalidating).toBe(false);
  });

  it('keeps Following revalidation hydration valid when pagination starts afterward', async () => {
    const pagePending = deferred<{
      items: ReturnType<typeof followingActivity>[];
      next_cursor: string | null;
    }>();
    const freshLikePending = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const freshRepostPending = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const store = useHomeTimelineStore();
    store.following.items = [feedPostFixture(1, 7)];
    store.following.loaded = true;
    store.following.stale = true;
    store.following.nextCursor = 'old-cursor';
    mocks.getFollowingTimeline
      .mockResolvedValueOnce({
        items: [followingActivity(2)],
        next_cursor: 'cursor-2',
      })
      .mockReturnValueOnce(pagePending.promise);
    mocks.getPostLikeStates.mockReturnValueOnce(freshLikePending.promise);
    mocks.getPostRepostStates.mockReturnValueOnce(freshRepostPending.promise);

    const refresh = store.revalidateFollowing();
    await refresh;

    expect(store.following.items.map(post => post.id)).toEqual([2]);
    expect(store.following.items[0]).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });
    expect(store.following.revalidating).toBe(false);

    const pageRequest = store.loadMoreFollowing();
    expect(mocks.getFollowingTimeline).toHaveBeenNthCalledWith(2, {
      limit: 20,
      cursor: 'cursor-2',
    });
    expect(store.following.loadingMore).toBe(true);

    freshLikePending.resolve({
      items: [{ post_id: 2, likes: 6, liked: true }],
      unavailable_post_ids: [],
    });
    freshRepostPending.resolve({
      items: [{ post_id: 2, reposts: 4, reposted: true }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(store.following.items[0]).toMatchObject({
      likeCount: 6,
      liked: true,
      likeStatus: 'ready',
      repostCount: 4,
      reposted: true,
      repostStatus: 'ready',
    });

    pagePending.resolve({ items: [], next_cursor: null });
    await pageRequest;
  });

  it('preserves cached Following when background revalidation fails', async () => {
    const store = useHomeTimelineStore();
    store.following.items = [feedPostFixture(1, 7)];
    store.following.loaded = true;
    store.following.stale = true;
    mocks.getFollowingTimeline.mockRejectedValue(new Error('offline'));

    await store.revalidateFollowing();

    expect(store.following.items.map(post => post.id)).toEqual([1]);
    expect(store.following.stale).toBe(true);
    expect(store.following.revalidating).toBe(false);
    expect(store.following.revalidateError).toBe(true);
  });

  it('invalidates an old Following page when an unfollow changes the relationship', async () => {
    const store = useHomeTimelineStore();
    store.following.items = [feedPostFixture(1, 8)];
    store.following.loaded = true;
    store.following.nextCursor = 'cursor-1';
    let resolvePage!: (value: { items: ReturnType<typeof followingActivity>[]; next_cursor: string | null }) => void;
    const pendingPage = new Promise<{ items: ReturnType<typeof followingActivity>[]; next_cursor: string | null }>((resolve) => {
      resolvePage = resolve;
    });
    mocks.getFollowingTimeline.mockReturnValue(pendingPage);

    const request = store.loadMoreFollowing();
    expect(store.following.loadingMore).toBe(true);
    store.reconcileFollowStateLocal({
      user_id: 8,
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    resolvePage({ items: [followingActivity(9, 8, 8)], next_cursor: null });
    await request;

    expect(store.following.loadingMore).toBe(false);
    expect(store.following.items).toHaveLength(0);
    expect(store.following.stale).toBe(true);
  });

  it('advances the shared bookmark fence before a Home mutation settles', async () => {
    const store = useHomeTimelineStore();
    const feedPost = feedPostFixture(4001, 7);
    store.following.items = [feedPost];
    const capturedVersion = captureBookmarkStateSyncVersion(4001);
    const pending = deferred<{ bookmarked: boolean }>();
    mocks.bookmarkPost.mockReturnValueOnce(pending.promise);

    const request = store.toggleBookmark(4001);

    expect(mocks.bookmarkPost).toHaveBeenCalledWith(4001);
    expect(feedPost.bookmarked).toBe(true);
    expect(syncHydratedPostBookmarkState({
      postId: 4001,
      bookmarked: false,
      status: 'ready',
    }, capturedVersion)).toBe(false);

    pending.resolve({ bookmarked: true });
    expect(await request).toBe('succeeded');
  });
});

const feedPostFixture = (id: number, authorID: number): FeedPost => ({
  id,
  author: author(authorID),
  content: `Post ${id}`,
  language: 'und',
  media: [],
  createdAt: '2026-08-24T00:00:00.000Z',
  likeCount: 0,
  replyCount: 0,
  viewCount: 0,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
  bookmarked: false,
  bookmarkStatus: 'ready',
});
