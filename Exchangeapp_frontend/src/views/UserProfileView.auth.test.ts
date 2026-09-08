// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UserProfileView from './UserProfileView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
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
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  feedStore: {
    isPostDeleted: vi.fn(),
    markPostDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
    applyLikeStateUpdate: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

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

vi.mock('../services/repostService', () => ({
  getPostRepostStates: mocks.getPostRepostStates,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));

vi.mock('../store/sessionSync', () => ({
  registerProfileSessionSync: vi.fn(),
  syncProfilePostRemoval: vi.fn(),
  syncProfileAuthorIdentity: vi.fn(),
  syncProfileLikeState: vi.fn(),
  syncProfileRepostState: vi.fn(),
  syncProfileFollowState: vi.fn(),
}));

const RouterLinkStub = {
  props: ['to'],
  template: '<a v-bind="$attrs"><slot /></a>',
};

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
  props: ['post'],
  template: '<article class="post-card">{{ post.content }}</article>',
};

const mountProfile = () => mount(UserProfileView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      MobileAccountMenu: { template: '<span />' },
      PostCard: PostCardStub,
      RouterLink: RouterLinkStub,
    },
  },
});

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const setAuth = (isAuthenticated: boolean, id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated,
    currentIdentity: id === null ? null : { ...profile(id) },
    syncCurrentIdentityProfile: vi.fn(),
  });
};

describe('UserProfileView auth-required state', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.route = reactive({
      params: { id: '7' },
      fullPath: '/users/7',
    });
    setAuth(false, null);
    mocks.getUser.mockResolvedValue(profile(7));
    mocks.getUserPosts.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
  });

  it('renders the anonymous profile state without making protected requests', async () => {
    const wrapper = mountProfile();
    await settle();

    expect(wrapper.get('.auth-required-state h1').text()).toBe('Log in to view this profile.');
    expect(wrapper.get('.auth-required-state p').text()).toBe(
      'Sign in to view this profile, posts, and connections.',
    );
    expect(wrapper.get('.auth-required-state__action').text()).toBe('Log in');
    expect(wrapper.text()).not.toContain('Profile could not be loaded.');
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(mocks.getUserPosts).not.toHaveBeenCalled();
    expect(mocks.getUserFollowState).not.toHaveBeenCalled();
    expect(document.title).toBe('Profile — Exchange');
    wrapper.unmount();
  });

  it('passes the full profile deep link, including query and hash, to Login', async () => {
    mocks.route.fullPath = '/users/7?source=share#bio';
    const wrapper = mountProfile();
    await settle();

    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({
      name: 'Login',
      query: {
        returnTo: '/users/7?source=share#bio',
      },
    });
    wrapper.unmount();
  });

  it('keeps malformed profile URLs separate from the auth-required state', async () => {
    mocks.route.params.id = 'abc';
    mocks.route.fullPath = '/users/abc';
    const wrapper = mountProfile();
    await settle();

    expect(wrapper.text()).toContain('This profile URL is not valid.');
    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
    expect(mocks.getUser).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('clears stale profile content immediately when the session expires', async () => {
    mocks.route.params.id = '8';
    mocks.route.fullPath = '/users/8';
    setAuth(true, 7);
    mocks.getUser.mockResolvedValue(profile(8));
    mocks.getUserPosts.mockResolvedValue({ items: [post(101, 8)], next_cursor: null });
    const wrapper = mountProfile();
    await settle();

    expect(wrapper.text()).toContain('User 8');
    expect(wrapper.text()).toContain('Body 101');
    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserPosts).toHaveBeenCalledTimes(1);

    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    await settle();

    expect(wrapper.get('.auth-required-state h1').text()).toBe('Log in to view this profile.');
    expect(wrapper.text()).not.toContain('User 8');
    expect(wrapper.text()).not.toContain('Body 101');
    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserPosts).toHaveBeenCalledTimes(1);
    expect(document.title).toBe('Profile — Exchange');
    wrapper.unmount();
  });

  it('loads the profile once when authentication returns without a route change', async () => {
    const wrapper = mountProfile();
    await settle();
    expect(mocks.getUser).not.toHaveBeenCalled();

    mocks.authStore.currentIdentity = { ...profile(7) };
    mocks.authStore.isAuthenticated = true;
    await settle();

    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUser).toHaveBeenCalledWith('7');
    expect(wrapper.text()).toContain('User 7');
    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
    wrapper.unmount();
  });

  it.each([
    ['not found', { response: { status: 404 } }, 'Profile not found.', 'This user does not exist.'],
    ['server failure', { response: { status: 500 } }, 'Profile could not be loaded.', 'Retry'],
  ])('preserves authenticated %s semantics', async (_name, error, heading, body) => {
    setAuth(true, 7);
    mocks.getUser.mockRejectedValue(error);
    const wrapper = mountProfile();
    await settle();

    expect(wrapper.text()).toContain(heading);
    expect(wrapper.text()).toContain(body);
    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});
