import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost } from '../types/Feed';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
  } | null,
  feedStore: null as {
    viewerID: number | null;
    recentlyPublishedPosts: FeedPost[];
    isArticleDeleted: ReturnType<typeof vi.fn>;
    markArticleDeleted: ReturnType<typeof vi.fn>;
    replaceAuthorIdentity: ReturnType<typeof vi.fn>;
    applyLikeStateUpdate: ReturnType<typeof vi.fn>;
  } | null,
  getUser: vi.fn(),
  getUserArticles: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
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

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserArticles: mocks.getUserArticles,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: mocks.deleteArticle,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
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

describe('profile session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.feedStore = reactive({
      viewerID: 7,
      recentlyPublishedPosts: [],
      isArticleDeleted: vi.fn().mockReturnValue(false),
      markArticleDeleted: vi.fn().mockReturnValue(true),
      replaceAuthorIdentity: vi.fn(),
      applyLikeStateUpdate: vi.fn(),
    });
    mocks.getUser.mockReset().mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserArticles.mockReset().mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockReset().mockResolvedValue({
      user_id: 7,
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getArticleLikeStates.mockReset().mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.followUser.mockReset();
    mocks.unfollowUser.mockReset();
    mocks.likeArticle.mockReset();
    mocks.unlikeArticle.mockReset();
    mocks.deleteArticle.mockReset().mockResolvedValue(undefined);
  });

  it('reuses profile, initial articles, and follow data on clean re-entry', async () => {
    mocks.getUserArticles.mockResolvedValue({ items: [article(1)], next_cursor: null });
    const store = useProfileSessionStore();

    await store.loadProfile(7);
    await settle();
    await store.loadProfile(7);
    await settle();

    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserArticles).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowState).toHaveBeenCalledTimes(1);
    expect(store.getSession(7)?.articles).toHaveLength(1);
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

  it('preserves cursor pagination and deduplicates article IDs in one session', async () => {
    mocks.getUserArticles
      .mockResolvedValueOnce({ items: [article(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [article(1), article(2)], next_cursor: null });
    const store = useProfileSessionStore();

    await store.loadProfile(7);
    await settle();
    await store.loadMoreArticles(7);
    await settle();

    const session = store.getSession(7)!;
    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(session.articles.map(post => post.id)).toEqual([1, 2]);
    expect(session.nextCursor).toBeNull();
    expect(session.hasMore).toBe(false);
  });

  it('synchronizes likes, deletes, identity edits, and newly published own posts across profile sessions', () => {
    const store = useProfileSessionStore();
    const first = store.ensureSession(7)!;
    const second = store.ensureSession(8)!;
    first.articles = [{
      id: 4,
      author: author(7),
      title: 'A',
      excerpt: 'A',
      coverImageUrl: '',
      createdAt: '2026-08-24T00:00:00.000Z',
      likeCount: 1,
      commentCount: 0,
      viewCount: 0,
      liked: false,
      likeStatus: 'ready',
    }];
    second.articles = [{ ...first.articles[0] }];
    store.applyLikeStateUpdateEverywhere({ articleId: 4, likes: 2, liked: true, status: 'ready' });
    expect(first.articles[0].liked).toBe(true);
    expect(second.articles[0].likeCount).toBe(2);

    store.replaceAuthorIdentityEverywhere({ ...author(7), display_name: 'Renamed' });
    expect(first.articles[0].author.display_name).toBe('Renamed');
    expect(mocks.feedStore!.replaceAuthorIdentity).toHaveBeenCalled();

    store.removeArticleEverywhere(4, 7);
    expect(first.articles).toHaveLength(0);
    expect(second.articles).toHaveLength(0);
    expect(mocks.feedStore!.markArticleDeleted).toHaveBeenCalledWith(4, 7);

    store.registerPublishedArticle(article(9, 7), 7);
    expect(store.getSession(7)?.articles[0].id).toBe(9);
  });
});
