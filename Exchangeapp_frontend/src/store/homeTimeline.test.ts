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

const article = (id: number) => ({
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
  author: author(),
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
});
