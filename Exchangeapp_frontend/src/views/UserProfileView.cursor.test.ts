// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import UserProfileView from './UserProfileView.vue';

const mocks = vi.hoisted(() => ({
  route: { params: { id: '7' } },
  setRouteID: (_id: string) => {},
  getUser: vi.fn(),
  getUserPosts: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadProfileAvatar: vi.fn(),
  deletePost: vi.fn(),
  getPostLikeStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
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
    isPostDeleted: vi.fn(),
    markPostDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
    applyLikeStateUpdate: vi.fn(),
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
  getUserPosts: mocks.getUserPosts,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
}));

vi.mock('../services/postService', () => ({
  deletePost: mocks.deletePost,
}));

vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const post = (id: number, authorID: number) => ({
  id,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
  content: `Body ${id}`,
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
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.setRouteID('7');
    mocks.authStore.currentIdentity.id = 7;
    mocks.getUser.mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserPosts.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
  });

  it('stores the next cursor, loads with it, deduplicates IDs, and stops at null', async () => {
    mocks.getUserPosts
      .mockResolvedValueOnce({ items: [post(1, 7)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [post(1, 7), post(2, 7)], next_cursor: null });

    const mounted = mountProfile();
    await settle();

    expect(mocks.getUserPosts).toHaveBeenNthCalledWith(1, '7', { limit: 20 });
    expect(mounted.findAll('.post-card')).toHaveLength(1);

    await mounted.find('.profile-feed-sentinel .profile-action').trigger('click');
    await settle();

    expect(mocks.getUserPosts).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card')).toHaveLength(2);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);

    await mounted.vm.$nextTick();
    expect(mocks.getUserPosts).toHaveBeenCalledTimes(2);
    mounted.unmount();
  });

  it('drops stale initial and load-more responses after changing profile', async () => {
    let resolveA!: (value: ReturnType<typeof profile>) => void;
    const profileA = new Promise<ReturnType<typeof profile>>((resolve) => {
      resolveA = resolve;
    });
    let resolveMore!: (value: { items: ReturnType<typeof post>[]; next_cursor: string | null }) => void;
    const loadMoreA = new Promise<{ items: ReturnType<typeof post>[]; next_cursor: string | null }>((resolve) => {
      resolveMore = resolve;
    });

    mocks.getUser.mockImplementation((id: string) => id === '7' ? profileA : Promise.resolve(profile(8)));
    mocks.getUserPosts.mockImplementation((id: string, options?: { cursor?: string }) => {
      if (id === '7' && options?.cursor) {
        return loadMoreA;
      }
      if (id === '8') {
        return Promise.resolve({ items: [post(8, 8)], next_cursor: null });
      }
      return Promise.resolve({ items: [post(7, 7)], next_cursor: 'cursor-a' });
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
    resolveMore({ items: [post(2, 7)], next_cursor: null });
    await settle();

    expect(mountedWithMore.find('h1').text()).toBe('User 8');
    expect(mountedWithMore.find('.post-card__id').text()).toBe('8');
    expect(mountedWithMore.findAll('.post-card')).toHaveLength(1);
    mountedWithMore.unmount();
  });

  it('ignores a stale delete response after switching profile', async () => {
    let resolveDelete!: () => void;
    mocks.getUserPosts.mockImplementation((id: string) => id === '8'
      ? Promise.resolve({ items: [post(8, 8)], next_cursor: null })
      : Promise.resolve({ items: [post(1, 7)], next_cursor: 'cursor-1' }));
    mocks.deletePost.mockImplementation(() => new Promise<void>((resolve) => {
      resolveDelete = resolve;
    }));

    const mounted = mountProfile();
    await settle();
    await mounted.find('.post-card__delete').trigger('click');
    expect(mocks.deletePost).toHaveBeenCalledWith(1);

    mocks.setRouteID('8');
    await settle();
    resolveDelete();
    await settle();

    expect(mounted.find('h1').text()).toBe('User 8');
    expect(mounted.find('.post-card__id').text()).toBe('8');
    expect(mocks.feedStore.markPostDeleted).not.toHaveBeenCalled();
    mounted.unmount();
  });
});

