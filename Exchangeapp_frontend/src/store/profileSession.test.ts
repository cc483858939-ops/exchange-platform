import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { TimelineItem } from '../services/postService';
import type { FeedPost } from '../types/Feed';
import type { ProfileTimelineItem } from './profileSession';
import { registerHomeTimelineSync } from './sessionSync';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
  } | null,
  feedStore: null as {
    viewerID: number | null;
    recentlyPublishedPosts: FeedPost[];
    isPostDeleted: ReturnType<typeof vi.fn>;
    markPostDeleted: ReturnType<typeof vi.fn>;
    replaceAuthorIdentity: ReturnType<typeof vi.fn>;
    applyLikeStateUpdate: ReturnType<typeof vi.fn>;
  } | null,
  getUser: vi.fn(),
  getUserTimeline: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  getPostLikeStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  deletePost: vi.fn(),
}));

vi.mock('./auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('./feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserTimeline: mocks.getUserTimeline,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

vi.mock('../services/postService', () => ({
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

import { useProfileSessionStore } from './profileSession';

const author = (id = 7) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
});
const profile = (id: number) => ({
  ...author(id),
  bio: '',
  created_at: '2026-08-24T00:00:00.000Z',
});

const followState = (id: number, following: boolean) => ({
  user_id: id,
  following,
  follower_count: following ? 1 : 0,
  following_count: 0,
});

const post = (id: number, authorID = 7) => ({
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
  reply_count: 0,
  view_count: 0,
  deleted: false as const,
});

const timelineItem = (
  sourceID: number,
  postID = sourceID,
  activityType: TimelineItem['activity_type'] = 'post',
  actorID = 7,
): TimelineItem => ({
  activity_type: activityType,
  activity_at: `2026-08-24T00:${String(sourceID).padStart(2, '0')}:00.000Z`,
  source_id: sourceID,
  actor: author(actorID),
  post: post(postID, activityType === 'repost' ? 8 : actorID),
});

const profileTimelineItem = (
  feedPost: FeedPost,
  activityType: ProfileTimelineItem['activityType'] = 'post',
  sourceID = feedPost.id,
  actorID = feedPost.author.id,
): ProfileTimelineItem => ({
  activityType,
  activityAt: feedPost.createdAt,
  sourceId: sourceID,
  actor: author(actorID),
  post: feedPost,
});

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

describe('profile session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.feedStore = reactive({
      viewerID: 7,
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
      markPostDeleted: vi.fn().mockReturnValue(true),
      replaceAuthorIdentity: vi.fn(),
      applyLikeStateUpdate: vi.fn(),
      applyRepostStateUpdate: vi.fn(),
    });
    mocks.getUser.mockReset().mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserTimeline.mockReset().mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockReset().mockResolvedValue({
      user_id: 7,
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.followUser.mockReset();
    mocks.unfollowUser.mockReset();
    mocks.likePost.mockReset();
    mocks.unlikePost.mockReset();
    mocks.repostPost.mockReset();
    mocks.undoRepostPost.mockReset();
    mocks.deletePost.mockReset().mockResolvedValue(undefined);
  });

  it('reuses profile, initial posts, and follow data on clean re-entry', async () => {
    mocks.getUserTimeline.mockResolvedValue({ items: [timelineItem(1)], next_cursor: null });
    const store = useProfileSessionStore();

    await store.loadProfile(7);
    await settle();
    await store.loadProfile(7);
    await settle();

    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserTimeline).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowState).toHaveBeenCalledTimes(1);
    expect(store.getSession(7)?.timelineItems).toHaveLength(1);
  });

  it('keeps up to eight sessions, prioritizes the own profile, and reuses 7 after 7 to 8 to 7', () => {
    const store = useProfileSessionStore();
    const own = store.ensureSession(7)!;
    own.user = profile(7);
    own.profileLoaded = true;
    store.setScrollY(7, 321);

    for (let id = 1; id <= 9; id += 1) {
      store.ensureSession(id);
    }

    expect(store.sessions.size).toBe(8);
    expect(store.sessions.has(7)).toBe(true);
    expect(store.sessions.has(1)).toBe(false);
    expect(store.ensureSession(7)?.scrollY).toBe(321);
  });

  it('clears viewer-owned sessions and ignores a late profile response', async () => {
    let resolveProfile!: (value: ReturnType<typeof profile>) => void;
    mocks.getUser.mockReturnValue(new Promise(resolve => { resolveProfile = resolve; }));
    const store = useProfileSessionStore();
    const request = store.loadProfile(7);

    store.setViewer(8);
    resolveProfile(profile(7));
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.sessions.size).toBe(0);
  });

  it('preserves cursor pagination and deduplicates Post IDs in one session', async () => {
    mocks.getUserTimeline
      .mockResolvedValueOnce({ items: [timelineItem(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [timelineItem(1), timelineItem(2)], next_cursor: null });
    const store = useProfileSessionStore();

    await store.loadProfile(7);
    await settle();
    await store.loadMoreTimeline(7);
    await settle();

    const session = store.getSession(7)!;
    expect(mocks.getUserTimeline).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(session.timelineItems.map(item => item.post.id)).toEqual([1, 2]);
    expect(session.nextCursor).toBeNull();
    expect(session.hasMore).toBe(false);
  });

  it('keeps previous Profile page interaction hydration valid when the next page starts', async () => {
    const page3Pending = deferred<{
      items: TimelineItem[];
      next_cursor: string | null;
    }>();
    const page2LikePending = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const page2RepostPending = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getUserTimeline
      .mockResolvedValueOnce({ items: [timelineItem(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [timelineItem(2)], next_cursor: 'cursor-2' })
      .mockReturnValueOnce(page3Pending.promise);
    mocks.getPostLikeStates
      .mockResolvedValueOnce({ items: [], unavailable_post_ids: [] })
      .mockReturnValueOnce(page2LikePending.promise);
    mocks.getPostRepostStates
      .mockResolvedValueOnce({ items: [], unavailable_post_ids: [] })
      .mockReturnValueOnce(page2RepostPending.promise);
    const store = useProfileSessionStore();

    await store.loadTimeline(7);
    await settle();
    await store.loadMoreTimeline(7);

    const session = store.getSession(7)!;
    expect(session.timelineItems.map(item => item.post.id)).toEqual([1, 2]);
    expect(session.timelineItems[1].post).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });
    const page2RequestVersion = session.timelineRequestVersion;

    const page3Request = store.loadMoreTimeline(7);
    expect(session.timelineRequestVersion).toBe(page2RequestVersion + 1);
    expect(mocks.getUserTimeline).toHaveBeenCalledTimes(3);
    expect(session.timelineLoadingMore).toBe(true);

    page2LikePending.resolve({
      items: [{ post_id: 2, likes: 7, liked: true }],
      unavailable_post_ids: [],
    });
    page2RepostPending.resolve({
      items: [{ post_id: 2, reposts: 9, reposted: true }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(session.timelineItems[1].post).toMatchObject({
      likeCount: 7,
      liked: true,
      likeStatus: 'ready',
      repostCount: 9,
      reposted: true,
      repostStatus: 'ready',
    });

    page3Pending.resolve({ items: [], next_cursor: null });
    await page3Request;
  });

  it('rejects old Profile interaction hydration after a forced posts refresh', async () => {
    const oldLikePending = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const oldRepostPending = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const newLikePending = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const newRepostPending = deferred<{
      items: Array<{ post_id: number; reposts: number; reposted: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getUserTimeline
      .mockResolvedValueOnce({ items: [timelineItem(2)], next_cursor: null })
      .mockResolvedValueOnce({ items: [timelineItem(2)], next_cursor: null });
    mocks.getPostLikeStates
      .mockReturnValueOnce(oldLikePending.promise)
      .mockReturnValueOnce(newLikePending.promise);
    mocks.getPostRepostStates
      .mockReturnValueOnce(oldRepostPending.promise)
      .mockReturnValueOnce(newRepostPending.promise);
    const store = useProfileSessionStore();

    await store.loadTimeline(7);
    const session = store.getSession(7)!;
    const oldTimelineGeneration = session.timelineGeneration;
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].post.id).toBe(2);
    expect(session.timelineItems[0].post).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });

    const refresh = store.loadTimeline(7, true);
    await refresh;

    expect(session.timelineGeneration).toBe(oldTimelineGeneration + 1);
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].post.id).toBe(2);
    expect(session.timelineItems[0].post).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });

    oldLikePending.resolve({
      items: [{ post_id: 2, likes: 99, liked: true }],
      unavailable_post_ids: [],
    });
    oldRepostPending.resolve({
      items: [{ post_id: 2, reposts: 88, reposted: true }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(session.timelineItems[0].post).toMatchObject({
      likeStatus: 'unknown',
      repostStatus: 'unknown',
    });

    newLikePending.resolve({
      items: [{ post_id: 2, likes: 3, liked: false }],
      unavailable_post_ids: [],
    });
    newRepostPending.resolve({
      items: [{ post_id: 2, reposts: 4, reposted: false }],
      unavailable_post_ids: [],
    });
    await settle();

    expect(session.timelineItems[0].post).toMatchObject({
      likeCount: 3,
      liked: false,
      likeStatus: 'ready',
      repostCount: 4,
      reposted: false,
      repostStatus: 'ready',
    });
  });

  it('keeps an unrelated pending initial Post request valid when another Post is removed', async () => {
    let resolvePosts!: (value: {
      items: TimelineItem[];
      next_cursor: string | null;
    }) => void;
    const pending = new Promise<{
      items: TimelineItem[];
      next_cursor: string | null;
    }>((resolve) => {
      resolvePosts = resolve;
    });
    mocks.getUserTimeline.mockReturnValue(pending);
    mocks.feedStore!.isPostDeleted.mockImplementation((postID: number) => postID === 42);
    const store = useProfileSessionStore();

    const request = store.loadTimeline(8);
    const session = store.getSession(8)!;
    const requestVersion = session.timelineRequestVersion;
    expect(session.timelineInitialLoading).toBe(true);

    expect(store.removePostEverywhere(42, 7)).toBe(true);
    expect(session.timelineRequestVersion).toBe(requestVersion);

    resolvePosts({ items: [timelineItem(42, 42, 'post', 8), timelineItem(43, 43, 'post', 8)], next_cursor: null });
    await request;

    expect(session.timelineInitialLoading).toBe(false);
    expect(session.timelineItems.map((item) => item.post.id)).toEqual([43]);
  });

  it('does not strand an unrelated pending load-more request after Post removal', async () => {
    let resolveTimeline!: (value: {
      items: TimelineItem[];
      next_cursor: string | null;
    }) => void;
    const pending = new Promise<{
      items: TimelineItem[];
      next_cursor: string | null;
    }>((resolve) => {
      resolveTimeline = resolve;
    });
    mocks.getUserTimeline.mockReturnValue(pending);
    mocks.feedStore!.isPostDeleted.mockImplementation((postID: number) => postID === 42);
    const store = useProfileSessionStore();
    const session = store.ensureSession(8)!;
    session.timelineLoaded = true;
    session.hasMore = true;
    session.nextCursor = 'cursor-1';

    const request = store.loadMoreTimeline(8);
    const requestVersion = session.timelineRequestVersion;
    expect(session.timelineLoadingMore).toBe(true);

    expect(store.removePostEverywhere(42, 7)).toBe(true);
    expect(session.timelineRequestVersion).toBe(requestVersion);

    resolveTimeline({
      items: [timelineItem(42, 42, 'post', 8), timelineItem(43, 43, 'post', 8)],
      next_cursor: null,
    });
    await request;

    expect(session.timelineLoadingMore).toBe(false);
    expect(session.timelineItems.map((item) => item.post.id)).toEqual([43]);
    expect(session.hasMore).toBe(false);
    expect(session.nextCursor).toBeNull();
  });

  it('keeps a removed canonical Post out of a late timeline response', async () => {
    let resolveTimeline!: (value: {
      items: TimelineItem[];
      next_cursor: string | null;
    }) => void;
    const pending = new Promise<{
      items: TimelineItem[];
      next_cursor: string | null;
    }>((resolve) => {
      resolveTimeline = resolve;
    });
    mocks.getUserTimeline.mockReturnValue(pending);
    const store = useProfileSessionStore();
    const session = store.ensureSession(8)!;
    session.timelineLoaded = true;
    session.hasMore = true;
    session.nextCursor = 'cursor-1';

    const request = store.loadMoreTimeline(8);
    store.removePostEverywhereLocal(42);
    resolveTimeline({
      items: [timelineItem(42, 42, 'post', 8), timelineItem(43, 43, 'post', 8)],
      next_cursor: null,
    });
    await request;

    expect(session.timelineItems.map((item) => item.post.id)).toEqual([43]);
  });

  it('external like state invalidates an older local Profile like mutation', async () => {
    let resolveLike!: (value: { likes: number; liked: boolean }) => void;
    const pendingLike = new Promise<{ likes: number; liked: boolean }>((resolve) => {
      resolveLike = resolve;
    });
    mocks.likePost.mockReturnValue(pendingLike);
    const store = useProfileSessionStore();
    const session = store.ensureSession(7)!;
    session.timelineItems = [profileTimelineItem({
      id: 4,
      author: author(7),
      content: 'Post 4',
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
    })];

    const localMutation = store.toggleLike(4, 7);
    expect(store.likePendingPostIds.has(4)).toBe(true);

    store.applyExternalLikeStateLocal({
      postId: 4,
      likes: 8,
      liked: true,
      status: 'ready',
    });
    expect(store.likePendingPostIds.has(4)).toBe(false);
    expect(session.timelineItems[0].post.likeCount).toBe(8);
    expect(session.timelineItems[0].post.liked).toBe(true);

    resolveLike({ likes: 3, liked: true });
    await localMutation;
    expect(session.timelineItems[0].post.likeCount).toBe(8);
  });

  it('batch-hydrates Profile Repost state without changing authored membership', async () => {
    mocks.getUserTimeline.mockResolvedValue({ items: [timelineItem(4)], next_cursor: null });
    mocks.getPostRepostStates.mockResolvedValue({
      items: [{ post_id: 4, reposts: 6, reposted: true }],
      unavailable_post_ids: [],
    });
    const store = useProfileSessionStore();

    await store.loadProfile(7);
    await settle();

    const session = store.getSession(7)!;
    expect(mocks.getPostRepostStates).toHaveBeenCalledWith([4]);
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].post).toMatchObject({
      id: 4,
      repostCount: 6,
      reposted: true,
      repostStatus: 'ready',
    });
  });

  it('keeps authored and repost activities distinct while hydrating each canonical post once', async () => {
    mocks.getUserTimeline.mockResolvedValue({
      items: [
        timelineItem(55, 4, 'repost', 7),
        timelineItem(4, 4, 'post', 7),
      ],
      next_cursor: null,
    });
    const store = useProfileSessionStore();

    await store.loadTimeline(7);
    await settle();

    const session = store.getSession(7)!;
    expect(session.timelineItems.map((item) => `${item.activityType}:${item.sourceId}`)).toEqual([
      'repost:55',
      'post:4',
    ]);
    expect(session.timelineItems.map((item) => item.post.id)).toEqual([4, 4]);
    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([4]);
    expect(mocks.getPostRepostStates).toHaveBeenCalledWith([4]);
  });

  it('optimistically toggles Profile Repost and rolls back on failure', async () => {
    const store = useProfileSessionStore();
    const session = store.ensureSession(7)!;
    const post: FeedPost = {
      id: 4,
      author: author(7),
      content: 'Post 4',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 8,
      reposted: false,
      repostStatus: 'ready',
    };
    session.timelineItems = [profileTimelineItem(post)];
    mocks.repostPost.mockRejectedValue(new Error('offline'));

    const request = store.toggleRepost(4, 7);
    expect(post.reposted).toBe(true);
    expect(post.repostCount).toBe(9);
    expect(await request).toBe(false);
    expect(post.reposted).toBe(false);
    expect(post.repostCount).toBe(8);
    expect(store.repostPendingPostIds.has(4)).toBe(false);
  });

  it('undoes only the repost activity when authored and repost activities share a canonical post', async () => {
    const store = useProfileSessionStore();
    const authored: FeedPost = {
      id: 4,
      author: author(7),
      content: 'Post 4',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 1,
      reposted: true,
      repostStatus: 'ready',
    };
    const reposted: FeedPost = {
      ...authored,
      author: author(8),
      repostContext: { actor: author(7) },
    };
    const session = store.ensureSession(7)!;
    session.timelineItems = [
      profileTimelineItem(authored, 'post', 4, 7),
      profileTimelineItem(reposted, 'repost', 55, 7),
    ];
    session.loadedActivityKeys.add('post:4');
    session.loadedActivityKeys.add('repost:55');
    mocks.undoRepostPost.mockResolvedValue({ reposts: 0, reposted: false });

    expect(await store.toggleRepost(4, 7)).toBe(true);
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].activityType).toBe('post');
    expect(session.timelineItems[0].sourceId).toBe(4);
    expect(session.timelineItems[0].post.reposted).toBe(false);
    expect(session.timelineItems[0].post.author.id).toBe(7);
  });

  it('marks the cached own Profile timeline stale after a successful repost without fabricating an activity', async () => {
    const store = useProfileSessionStore();
    const session = store.ensureSession(7)!;
    const feedPost: FeedPost = {
      id: 4,
      author: author(8),
      content: 'Post 4',
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
    };
    session.timelineItems = [profileTimelineItem(feedPost, 'post', 4, 8)];
    session.timelineLoaded = true;
    mocks.repostPost.mockResolvedValue({ reposts: 1, reposted: true });

    expect(await store.toggleRepost(4, 7)).toBe(true);
    expect(session.timelineStale).toBe(true);
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].activityType).toBe('post');
  });

  it('applies an external Detail Repost update without changing Profile membership', () => {
    const store = useProfileSessionStore();
    const session = store.ensureSession(7)!;
    session.timelineItems = [profileTimelineItem({
      id: 4,
      author: author(7),
      content: 'Post 4',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 8,
      reposted: false,
      repostStatus: 'ready',
    })];

    expect(store.applyExternalRepostStateLocal({
      postId: 4,
      reposts: 9,
      reposted: true,
      status: 'ready',
    })).toBe(true);
    expect(session.timelineItems).toHaveLength(1);
    expect(session.timelineItems[0].post.repostCount).toBe(9);
    expect(session.timelineItems[0].post.reposted).toBe(true);
  });

  it('external follow state invalidates an older Profile follow request', async () => {
    let resolveFollow!: (value: ReturnType<typeof followState>) => void;
    const pendingFollow = new Promise<ReturnType<typeof followState>>((resolve) => {
      resolveFollow = resolve;
    });
    mocks.followUser.mockReturnValue(pendingFollow);
    const store = useProfileSessionStore();
    const session = store.ensureSession(8)!;
    session.followState = followState(8, false);
    session.followLoaded = true;

    const localMutation = store.toggleFollow(8);
    expect(session.followPending).toBe(true);
    store.applyExternalFollowStateLocal(followState(8, true));
    expect(session.followPending).toBe(false);
    expect(session.followLoading).toBe(false);
    expect(session.followState?.following).toBe(true);

    resolveFollow(followState(8, true));
    await localMutation;
    expect(session.followState?.following).toBe(true);
  });

  it('sends a successful own Profile follow to Home reconciliation', async () => {
    const reconcileFollowStateLocal = vi.fn();
    registerHomeTimelineSync({
      applyLikeStateUpdateLocal: vi.fn().mockReturnValue(false),
      applyExternalLikeStateLocal: vi.fn().mockReturnValue(false),
      applyRepostStateUpdateLocal: vi.fn().mockReturnValue(false),
      applyExternalRepostStateLocal: vi.fn().mockReturnValue(false),
      applyReplyCountUpdateLocal: vi.fn().mockReturnValue(false),
      reconcileFollowStateLocal,
      removePostLocal: vi.fn(),
      replaceAuthorIdentityLocal: vi.fn(),
    });
    mocks.followUser.mockResolvedValue(followState(8, true));
    const store = useProfileSessionStore();
    const session = store.ensureSession(8)!;
    session.followState = followState(8, false);
    session.followLoaded = true;

    expect(await store.toggleFollow(8)).toBe(true);
    expect(reconcileFollowStateLocal).toHaveBeenCalledWith(followState(8, true));
  });

  it('updates reply counts in every cached matching Profile Post', () => {
    const store = useProfileSessionStore();
    const first = store.ensureSession(7)!;
    const second = store.ensureSession(8)!;
    const post = {
      id: 4,
      author: author(7),
      content: 'Post 4',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      replyCount: 1,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready' as const,
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready' as const,
    };
    first.timelineItems = [profileTimelineItem({ ...post })];
    second.timelineItems = [profileTimelineItem({ ...post })];

    expect(store.applyReplyCountUpdateEverywhereLocal({ postId: 4, replyCount: 6 })).toBe(true);
    expect(first.timelineItems[0].post.replyCount).toBe(6);
    expect(second.timelineItems[0].post.replyCount).toBe(6);
  });

  it('synchronizes likes, deletes, identity edits, and newly published own posts across profile sessions', () => {
    const store = useProfileSessionStore();
    const first = store.ensureSession(7)!;
    const second = store.ensureSession(8)!;
    first.timelineItems = [profileTimelineItem({
      id: 4,
      author: author(7),
      content: 'A',
      media: [],
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 1,
      replyCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
      repostCount: 0,
      reposted: false,
      repostStatus: 'ready',
    })];
    second.timelineItems = [profileTimelineItem({ ...first.timelineItems[0].post })];
    store.applyLikeStateUpdateEverywhere({ postId: 4, likes: 2, liked: true, status: 'ready' });
    expect(first.timelineItems[0].post.liked).toBe(true);
    expect(second.timelineItems[0].post.likeCount).toBe(2);

    store.replaceAuthorIdentityEverywhere({ ...author(7), display_name: 'Renamed' });
    expect(first.timelineItems[0].post.author.display_name).toBe('Renamed');
    expect(mocks.feedStore!.replaceAuthorIdentity).toHaveBeenCalled();

    store.removePostEverywhere(4, 7);
    expect(first.timelineItems).toHaveLength(0);
    expect(second.timelineItems).toHaveLength(0);
    expect(mocks.feedStore!.markPostDeleted).toHaveBeenCalledWith(4, 7);

    store.registerPublishedTimelinePost(post(9, 7), 7);
    expect(store.getSession(7)?.timelineItems[0].post.id).toBe(9);
    expect(store.getSession(7)?.timelineLoaded).toBe(false);
  });
});
