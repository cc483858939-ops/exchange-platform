// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import UserConnectionsView from './UserConnectionsView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  getUser: vi.fn(),
  getUserFollowers: vi.fn(),
  getUserFollowing: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  getProfileSession: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/profileSession', () => ({
  useProfileSessionStore: () => ({ getSession: mocks.getProfileSession }),
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserFollowers: mocks.getUserFollowers,
  getUserFollowing: mocks.getUserFollowing,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

vi.mock('../store/sessionSync', () => ({
  registerConnectionsSessionSync: vi.fn(),
}));

const RouterLinkStub = {
  props: ['to'],
  template: '<a v-bind="$attrs"><slot /></a>',
};

const user = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const mountConnections = () => mount(UserConnectionsView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      RouterLink: RouterLinkStub,
      UserRow: {
        props: ['item'],
        template: '<div class="user-row">{{ item.user.display_name }}</div>',
      },
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
    currentIdentity: id === null ? null : { ...user(id) },
  });
};

describe('UserConnectionsView auth-required state', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    mocks.route = reactive({
      name: 'UserFollowers',
      params: { id: '7' },
      fullPath: '/users/7/followers',
    });
    setAuth(false, null);
    mocks.getUser.mockResolvedValue(user(7));
    mocks.getUserFollowers.mockResolvedValue({ items: [], has_more: false });
    mocks.getUserFollowing.mockResolvedValue({ items: [], has_more: false });
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.restoreAllMocks();
    document.title = 'Exchange';
  });

  it('renders anonymous Followers without profile or connections requests', async () => {
    wrapper = mountConnections();
    await settle();

    expect(wrapper.get('.connections-header__copy strong').text()).toBe('Followers');
    expect(wrapper.get('.auth-required-state h1').text()).toBe('Log in to view followers.');
    expect(wrapper.get('.auth-required-state p').text()).toBe(
      "Sign in to view this user's connections.",
    );
    expect(wrapper.text()).not.toContain('Profile could not be loaded.');
    expect(wrapper.text()).not.toContain('Connections could not be loaded.');
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(mocks.getUserFollowers).not.toHaveBeenCalled();
    expect(mocks.getUserFollowing).not.toHaveBeenCalled();
    expect(document.title).toBe('Followers — Exchange');
  });

  it('renders anonymous Following and preserves its exact deep link for Login', async () => {
    mocks.route.name = 'UserFollowing';
    mocks.route.fullPath = '/users/7/following?source=share#top';
    const target = '/users/7/following?source=share#top';
    wrapper = mountConnections();
    await settle();

    expect(wrapper.get('.connections-header__copy strong').text()).toBe('Following');
    expect(wrapper.get('.auth-required-state h1').text()).toBe('Log in to view following.');
    expect(wrapper.find('.auth-required-state__action').exists()).toBe(true);
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({
      name: 'UserProfile',
      params: { id: 7 },
    });
    expect(wrapper.findAllComponents(RouterLinkStub).at(-1)?.props('to')).toEqual({
      name: 'Login',
      query: { returnTo: target },
    });
    expect(document.title).toBe('Following — Exchange');
  });

  it('does not turn a malformed connections URL into an auth CTA', async () => {
    mocks.route.params.id = 'abc';
    mocks.route.fullPath = '/users/abc/followers';
    wrapper = mountConnections();
    await settle();

    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
    expect(wrapper.get('.connections-header__error').text()).toBe('Profile could not be loaded.');
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(0);
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(mocks.getUserFollowers).not.toHaveBeenCalled();
  });

  it('clears stale connections content when the session expires', async () => {
    setAuth(true, 7);
    mocks.getUser.mockResolvedValue(user(7));
    mocks.getUserFollowers.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    wrapper = mountConnections();
    await settle();

    expect(wrapper.text()).toContain('User 8');
    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowers).toHaveBeenCalledTimes(1);

    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    await settle();

    expect(wrapper.get('.auth-required-state h1').text()).toBe('Log in to view followers.');
    expect(wrapper.text()).not.toContain('User 8');
    expect(wrapper.text()).not.toContain('Connections could not be loaded.');
    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowers).toHaveBeenCalledTimes(1);
  });

  it('reactivates the current connections route once after authentication returns', async () => {
    wrapper = mountConnections();
    await settle();
    expect(mocks.getUser).not.toHaveBeenCalled();

    mocks.authStore.currentIdentity = { ...user(7) };
    mocks.authStore.isAuthenticated = true;
    await settle();

    expect(mocks.getUser).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowers).toHaveBeenCalledTimes(1);
    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
  });

  it('keeps an authenticated connections request failure distinct from auth required', async () => {
    setAuth(true, 7);
    mocks.getUser.mockResolvedValue(user(7));
    mocks.getUserFollowers.mockRejectedValue(new Error('offline'));
    wrapper = mountConnections();
    await settle();

    expect(wrapper.text()).toContain('Connections could not be loaded.');
    expect(wrapper.text()).toContain('Retry');
    expect(wrapper.find('.auth-required-state').exists()).toBe(false);
  });
});
