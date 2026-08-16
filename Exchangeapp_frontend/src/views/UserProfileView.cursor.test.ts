// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UserProfileView from './UserProfileView.vue';

const mocks = vi.hoisted(() => ({
  route: { params: { id: '7' } },
  setRouteID: (_id: string) => {},
  getUser: vi.fn(),
  getUserArticles: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadProfileAvatar: vi.fn(),
  deleteArticle: vi.fn(),
  getArticleLikeStates: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  authStore: {
    isAuthenticated: true,
    currentIdentity: {
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
    },
  },
  feedStore: {
    isArticleDeleted: vi.fn(),
    markArticleDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
  },
}));

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue');
  const route = reactive(mocks.route);
  mocks.setRouteID = (id: string) => {
    route.params.id = id;
  };
  return {
    useRoute: () => route,
    useRouter: () => mocks.router,
  };
});

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserArticles: mocks.getUserArticles,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: mocks.deleteArticle,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const article = (id: number, authorID: number) => ({
  ID: id,
  CreatedAt: '2026-08-15T00:00:00.000Z',
  UpdatedAt: '2026-08-15T00:00:00.000Z',
  title: `Post ${id}`,
  content: `Body ${id}`,
  preview: `Preview ${id}`,
  cover_image_url: '',
  summary: '',
  tags: [],
  category: 'News',
  publication_state: 'published',
  analysis_state: 'pending',
  analysis_version: 'v1',
  published_at: '2026-08-15T00:00:00.000Z',
  expired_at: null,
  like_count: 0,
  comment_count: 0,
  like_sync_version: 0,
  author: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
});

const PostCardStub = {
  props: ['post', 'showDelete'],
  template: `
    <article class="post-card">
      <span class="post-card__id">{{ post.id }}</span>
      <button v-if="showDelete" class="post-card__delete" type="button" @click="$emit('delete-post', post.id)">Delete</button>
    </article>
  `,
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const mountProfile = () => mount(UserProfileView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      PostCard: PostCardStub,
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('UserProfileView cursor pagination', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.setRouteID('7');
    mocks.authStore.currentIdentity.id = 7;
    mocks.getUser.mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserArticles.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getArticleLikeStates.mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.deleteArticle.mockResolvedValue(undefined);
    mocks.feedStore.isArticleDeleted.mockReturnValue(false);
    mocks.feedStore.markArticleDeleted.mockReturnValue(true);
  });

  it('stores the next cursor, loads with it, deduplicates IDs, and stops at null', async () => {
    mocks.getUserArticles
      .mockResolvedValueOnce({ items: [article(1, 7)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [article(1, 7), article(2, 7)], next_cursor: null });

    const mounted = mountProfile();
    await settle();

    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(1, '7', { limit: 20 });
    expect(mounted.findAll('.post-card')).toHaveLength(1);

    await mounted.find('.profile-feed-sentinel .profile-action').trigger('click');
    await settle();

    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card')).toHaveLength(2);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);

    await mounted.vm.$nextTick();
    expect(mocks.getUserArticles).toHaveBeenCalledTimes(2);
    mounted.unmount();
  });

  it('drops stale initial and load-more responses after changing profile', async () => {
    let resolveA!: (value: ReturnType<typeof profile>) => void;
    const profileA = new Promise<ReturnType<typeof profile>>((resolve) => {
      resolveA = resolve;
    });
    let resolveMore!: (value: { items: ReturnType<typeof article>[]; next_cursor: string | null }) => void;
    const loadMoreA = new Promise<{ items: ReturnType<typeof article>[]; next_cursor: string | null }>((resolve) => {
      resolveMore = resolve;
    });

    mocks.getUser.mockImplementation((id: string) => id === '7' ? profileA : Promise.resolve(profile(8)));
    mocks.getUserArticles.mockImplementation((id: string, options?: { cursor?: string }) => {
      if (id === '7' && options?.cursor) {
        return loadMoreA;
      }
      if (id === '8') {
        return Promise.resolve({ items: [article(8, 8)], next_cursor: null });
      }
      return Promise.resolve({ items: [article(7, 7)], next_cursor: 'cursor-a' });
    });

    const mounted = mountProfile();
    await flushPromises();
    mocks.setRouteID('8');
    await settle();

    expect(mounted.find('h1').text()).toBe('User 8');
    expect(mounted.findAll('.post-card')).toHaveLength(1);
    expect(mounted.find('.post-card__id').text()).toBe('8');

    resolveA(profile(7));
    await settle();
    expect(mounted.find('h1').text()).toBe('User 8');
    expect(mounted.find('.post-card__id').text()).toBe('8');

    mounted.unmount();

    mocks.setRouteID('7');
    const mountedWithMore = mountProfile();
    await settle();
    await mountedWithMore.find('.profile-feed-sentinel .profile-action').trigger('click');
    await flushPromises();
    mocks.setRouteID('8');
    await settle();
    resolveMore({ items: [article(2, 7)], next_cursor: null });
    await settle();

    expect(mountedWithMore.find('h1').text()).toBe('User 8');
    expect(mountedWithMore.find('.post-card__id').text()).toBe('8');
    expect(mountedWithMore.findAll('.post-card')).toHaveLength(1);
    mountedWithMore.unmount();
  });

  it('ignores a stale delete response after switching profile', async () => {
    let resolveDelete!: () => void;
    mocks.getUserArticles.mockImplementation((id: string) => id === '8'
      ? Promise.resolve({ items: [article(8, 8)], next_cursor: null })
      : Promise.resolve({ items: [article(1, 7)], next_cursor: 'cursor-1' }));
    mocks.deleteArticle.mockImplementation(() => new Promise<void>((resolve) => {
      resolveDelete = resolve;
    }));

    const mounted = mountProfile();
    await settle();
    await mounted.find('.post-card__delete').trigger('click');
    expect(mocks.deleteArticle).toHaveBeenCalledWith(1);

    mocks.setRouteID('8');
    await settle();
    resolveDelete();
    await settle();

    expect(mounted.find('h1').text()).toBe('User 8');
    expect(mounted.find('.post-card__id').text()).toBe('8');
    expect(mocks.feedStore.markArticleDeleted).not.toHaveBeenCalled();
    mounted.unmount();
  });
});


