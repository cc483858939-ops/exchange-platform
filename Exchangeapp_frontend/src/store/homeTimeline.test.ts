import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost } from '../types/Feed';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
    token: string | null;
  } | null,
  feedStore: null as {
    viewerID: number | null;
    recentlyPublishedPosts: FeedPost[];
    isArticleDeleted: ReturnType<typeof vi.fn>;
    markArticleDeleted: ReturnType<typeof vi.fn>;
    replaceAuthorIdentity: ReturnType<typeof vi.fn>;
    applyLikeStateUpdate: ReturnType<typeof vi.fn>;
  } | null,
  getArticleRecommendations: vi.fn(),
  getFollowingTimeline: vi.fn(),
  getArticleLikeStates: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  deleteArticle: vi.fn(),
}));

vi.mock('./auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('./feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/recommendationService', () => ({
  getArticleRecommendations: mocks.getArticleRecommendations,
}));

vi.mock('../services/articleService', () => ({
  getFollowingTimeline: mocks.getFollowingTimeline,
  deleteArticle: mocks.deleteArticle,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

import { useHomeTimelineStore } from './homeTimeline';

const author = (id = 7) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
});

const recommendation = (id: number) => ({
  id,
  author: author(),
  title: `Recommendation ${id}`,
  content: `Body ${id}`,
  preview: `Preview ${id}`,
  cover_image_url: '',
  like_count: 0,
  comment_count: 0,
  view_count: 0,
  created_at: '2026-08-24T00:00:00.000Z',
  score: 1,
});

const article = (id: number, authorID = 7) => ({
  ID: id,
  CreatedAt: '2026-08-24T00:00:00.000Z',
  UpdatedAt: '2026-08-24T00:00:00.000Z',
  title: `Post ${id}`,
  content: `Body ${id}`,
  preview: `Preview ${id}`,
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-24T00:00:00.000Z',
  expired_at: null,
  like_count: 0,
  comment_count: 0,
  view_count: 0,
  like_sync_version: 0,
  author: author(authorID),
});

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

describe('home timeline session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7 },
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      viewerID: 7,
      recentlyPublishedPosts: [],
      isArticleDeleted: vi.fn().mockReturnValue(false),
      markArticleDeleted: vi.fn().mockReturnValue(true),
      replaceAuthorIdentity: vi.fn(),
      applyLikeStateUpdate: vi.fn(),
    });
    mocks.getArticleRecommendations.mockReset();
    mocks.getFollowingTimeline.mockReset();
    mocks.getArticleLikeStates.mockReset().mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.likeArticle.mockReset();
    mocks.unlikeArticle.mockReset();
    mocks.deleteArticle.mockReset().mockResolvedValue(undefined);
  });

  it('does not refetch a loaded tab after clean Home re-entry', async () => {
    mocks.getArticleRecommendations.mockResolvedValue([recommendation(1)]);
    mocks.getFollowingTimeline.mockResolvedValue({ items: [article(2)], next_cursor: null });
    const store = useHomeTimelineStore();

    await store.loadForYou();
    await store.loadForYou();
    store.setActiveTab('following');
    await store.loadFollowing();
    await store.loadFollowing();
    await settle();

    expect(mocks.getArticleRecommendations).toHaveBeenCalledTimes(1);
    expect(mocks.getFollowingTimeline).toHaveBeenCalledTimes(1);
    expect(store.forYou.items).toHaveLength(1);
    expect(store.following.items).toHaveLength(1);
  });

  it('drops a late request when the authenticated viewer changes', async () => {
    let resolveRecommendations!: (items: ReturnType<typeof recommendation>[]) => void;
    const pending = new Promise<ReturnType<typeof recommendation>[]>(resolve => {
      resolveRecommendations = resolve;
    });
    mocks.getArticleRecommendations.mockReturnValue(pending);
    const store = useHomeTimelineStore();
    const request = store.loadForYou();

    store.setViewer(8);
    resolveRecommendations([recommendation(9)]);
    await request;

    expect(store.viewerID).toBe(8);
    expect(store.forYou.items).toHaveLength(0);
    expect(store.forYou.loaded).toBe(false);
  });

  it('keeps tab and scroll state in memory, then clears both for a new viewer', () => {
    const store = useHomeTimelineStore();

    store.setActiveTab('following');
    store.setScrollY('following', 742);
    expect(store.activeTab).toBe('following');
    expect(store.scrollY.following).toBe(742);

    store.setViewer(8);
    expect(store.activeTab).toBe('for-you');
    expect(store.scrollY.following).toBe(0);
  });

  it('dismisses a recommendation without removing the same article from Following', () => {
    const store = useHomeTimelineStore();
    const followingPost: FeedPost = {
      id: 4,
      author: author(),
      title: 'Following',
      excerpt: 'Following',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      commentCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    };
    store.forYou.items = [{ article: recommendation(4), post: { ...followingPost } }];
    store.following.items = [followingPost];

    expect(store.dismissRecommendation(4)).toBe(true);
    expect(store.forYou.items).toHaveLength(0);
    expect(store.following.items).toHaveLength(1);
    expect(store.following.items[0].id).toBe(4);
    expect(mocks.feedStore!.markArticleDeleted).not.toHaveBeenCalled();
  });

  it('applies a like update to every Home surface and removes deleted articles', () => {
    const store = useHomeTimelineStore();
    mocks.feedStore!.recentlyPublishedPosts = [{
      id: 4,
      author: author(),
      title: 'Recent',
      excerpt: 'Recent',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 3,
      commentCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    }];
    store.following.items = [{
      id: 4,
      author: author(),
      title: 'Following',
      excerpt: 'Following',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 3,
      commentCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    }];
    store.forYou.items = [
      { article: recommendation(4), post: { ...store.following.items[0] } },
    ];

    store.applyLikeStateUpdate({ articleId: 4, likes: 4, liked: true, status: 'ready' });
    expect(mocks.feedStore!.recentlyPublishedPosts[0].liked).toBe(true);
    expect(store.following.items[0].likeCount).toBe(4);
    expect(store.forYou.items[0].post.likeCount).toBe(4);

    expect(store.removeArticle(4, 7)).toBe(true);
    expect(store.following.items).toHaveLength(0);
    expect(store.forYou.items).toHaveLength(0);
    expect(mocks.feedStore!.markArticleDeleted).toHaveBeenCalledWith(4, 7);
  });

  it('external like state invalidates an older local like mutation', async () => {
    let resolveLike!: (value: { likes: number; liked: boolean }) => void;
    const pendingLike = new Promise<{ likes: number; liked: boolean }>((resolve) => {
      resolveLike = resolve;
    });
    mocks.likeArticle.mockReturnValue(pendingLike);
    const store = useHomeTimelineStore();
    store.following.items = [{
      id: 4,
      author: author(),
      title: 'Following',
      excerpt: 'Following',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 2,
      commentCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    }];

    const localMutation = store.toggleLike(4);
    expect(store.likePendingArticleIds.has(4)).toBe(true);

    store.applyExternalLikeStateLocal({
      articleId: 4,
      likes: 8,
      liked: true,
      status: 'ready',
    });
    expect(store.likePendingArticleIds.has(4)).toBe(false);
    expect(store.following.items[0].likeCount).toBe(8);
    expect(store.following.items[0].liked).toBe(true);

    resolveLike({ likes: 3, liked: true });
    await localMutation;
    expect(store.following.items[0].likeCount).toBe(8);
  });

  it('updates comment counts across recently published, Following, and For You copies', () => {
    const store = useHomeTimelineStore();
    const post: FeedPost = {
      id: 4,
      author: author(),
      title: 'Post',
      excerpt: 'Post',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 0,
      commentCount: 1,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    };
    mocks.feedStore!.recentlyPublishedPosts = [{ ...post }];
    store.following.items = [{ ...post }];
    store.forYou.items = [{ article: recommendation(4), post: { ...post } }];

    expect(store.applyCommentCountUpdateLocal({ articleId: 4, commentCount: 7 })).toBe(true);
    expect(mocks.feedStore!.recentlyPublishedPosts[0].commentCount).toBe(7);
    expect(store.following.items[0].commentCount).toBe(7);
    expect(store.forYou.items[0].post.commentCount).toBe(7);
  });

  it('reconciles an unfollow by removing only Following posts and marking it stale', () => {
    const store = useHomeTimelineStore();
    store.following.items = [
      { ...articleToPost(4, 8) },
      { ...articleToPost(5, 7) },
    ];
    store.forYou.items = [{ article: recommendation(4), post: articleToPost(4, 8) }];

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
    store.following.items = [articleToPost(1, 7)];
    store.following.loaded = true;
    store.following.nextCursor = 'old-cursor';
    store.following.stale = true;
    mocks.getFollowingTimeline.mockResolvedValue({
      items: [article(2), article(2), article(3)],
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

  it('preserves cached Following when background revalidation fails', async () => {
    const store = useHomeTimelineStore();
    store.following.items = [articleToPost(1, 7)];
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
    store.following.items = [articleToPost(1, 8)];
    store.following.loaded = true;
    store.following.nextCursor = 'cursor-1';
    let resolvePage!: (value: { items: ReturnType<typeof article>[]; next_cursor: string | null }) => void;
    const pendingPage = new Promise<{ items: ReturnType<typeof article>[]; next_cursor: string | null }>((resolve) => {
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
    resolvePage({ items: [article(9, 8)], next_cursor: null });
    await request;

    expect(store.following.loadingMore).toBe(false);
    expect(store.following.items).toHaveLength(0);
    expect(store.following.stale).toBe(true);
  });
});

const articleToPost = (id: number, authorID: number): FeedPost => ({
  id,
  author: author(authorID),
  title: `Post ${id}`,
  excerpt: `Post ${id}`,
  coverImageUrl: '',
  createdAt: '2026-08-24T00:00:00.000Z',
  likeCount: 0,
  commentCount: 0,
  viewCount: 0,
  liked: false,
  likeStatus: 'ready',
});
