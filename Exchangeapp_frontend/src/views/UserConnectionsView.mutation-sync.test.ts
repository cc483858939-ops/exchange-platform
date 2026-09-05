// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import UserConnectionsView from './UserConnectionsView.vue';
import { useConnectionsSessionStore } from '../store/connectionsSession';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  getUser: vi.fn(),
  getUserFollowers: vi.fn(),
  getUserFollowing: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  externalFollow: vi.fn(),
  connectionsSync: null as any,
  getProfileSession: vi.fn(),
}));

vi.mock('vue-router', () => ({ useRoute: () => mocks.route }));
vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
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
  registerConnectionsSessionSync: vi.fn((sync: any) => { mocks.connectionsSync = sync; }),
  syncExternalFollowState: vi.fn((state: any) => {
    mocks.externalFollow(state);
    mocks.connectionsSync?.applyExternalFollowStateLocal(state);
  }),
}));

const user = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const followResponse = (id: number, following: boolean) => ({
  user_id: id,
  following,
  follower_count: following ? 1 : 0,
  following_count: 0,
});

const mountConnections = () => mount(UserConnectionsView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      RouterLink: { template: '<a><slot /></a>' },
      UserRow: {
        props: ['item'],
        emits: ['toggle-follow'],
        template: '<button class="test-follow" :data-user="item.user.id" :data-following="item.following" @click="$emit(\'toggle-follow\', item.user.id)">{{ item.user.username }}</button>',
      },
    },
  },
});

const setWindowScrollY = (value: number) => {
  Object.defineProperty(window, 'scrollY', { configurable: true, value });
};

describe('UserConnectionsView mutation synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
    mocks.connectionsSync = null;
    mocks.route = reactive({
      name: 'UserFollowing',
      params: { id: '7' },
    });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer token',
      currentIdentity: { id: 7, username: 'viewer' },
    });
    mocks.getProfileSession.mockReturnValue(undefined);
    mocks.getUser.mockResolvedValue(user(7));
    mocks.getUserFollowers.mockResolvedValue({ items: [], has_more: false });
    mocks.getUserFollowing.mockResolvedValue({
      items: [{ user: user(8), following: true }],
      has_more: false,
    });
    mocks.followUser.mockResolvedValue(followResponse(8, true));
    mocks.unfollowUser.mockResolvedValue(followResponse(8, false));
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    setWindowScrollY(0);
  });

  afterEach(() => {
    setWindowScrollY(0);
    vi.restoreAllMocks();
  });

  it('syncs successful unfollow and preserves own Following removal', async () => {
    const wrapper = mountConnections();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();

    expect(wrapper.find('.test-follow').exists()).toBe(false);
    expect(mocks.externalFollow).toHaveBeenCalledWith(followResponse(8, false));
  });

  it('syncs successful follow from a connection row', async () => {
    mocks.getUserFollowing.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    const wrapper = mountConnections();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();

    expect(wrapper.find('.test-follow').attributes('data-following')).toBe('true');
    expect(mocks.externalFollow).toHaveBeenCalledWith(followResponse(8, true));
  });

  it('rolls back a failed connection follow without external synchronization', async () => {
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));
    mocks.getUserFollowing.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    const wrapper = mountConnections();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();

    expect(wrapper.find('.test-follow').attributes('data-following')).toBe('false');
    expect(mocks.externalFollow).not.toHaveBeenCalled();
  });

  it('restores scroll independently per connection mode and target, once per route entry', async () => {
    mocks.route.name = 'UserFollowing';
    mocks.route.params.id = '42';
    mocks.getUser.mockResolvedValue(user(42));
    const connectionsSession = useConnectionsSessionStore();
    const target42 = connectionsSession.activate(42, 'following')!;
    await flushPromises();
    connectionsSession.activate(42, 'followers');
    await flushPromises();
    const target99 = connectionsSession.activate(99, 'following')!;
    await flushPromises();

    target42.following.scrollY = 400;
    target42.followers.scrollY = 900;
    target99.following.scrollY = 1100;

    const scrollTo = window.scrollTo as ReturnType<typeof vi.fn>;
    const wrapper = mountConnections();
    await flushPromises();

    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 400, behavior: 'auto' });

    setWindowScrollY(500);
    mocks.route.name = 'UserFollowers';
    await flushPromises();
    expect(scrollTo).toHaveBeenCalledTimes(2);
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 900, behavior: 'auto' });

    setWindowScrollY(700);
    mocks.route.name = 'UserFollowing';
    await flushPromises();
    expect(scrollTo).toHaveBeenCalledTimes(3);
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 500, behavior: 'auto' });

    setWindowScrollY(750);
    mocks.route.params.id = '99';
    await flushPromises();
    expect(scrollTo).toHaveBeenCalledTimes(4);
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1100, behavior: 'auto' });

    setWindowScrollY(1200);
    mocks.route.params.id = '42';
    await flushPromises();
    expect(scrollTo).toHaveBeenCalledTimes(5);
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 750, behavior: 'auto' });

    scrollTo.mockClear();
    const activeFollowing = target42.following;
    activeFollowing.items = [...activeFollowing.items, { user: user(123), following: false }];
    activeFollowing.hasMore = true;
    activeFollowing.loadingMore = true;
    activeFollowing.loadingMore = false;
    activeFollowing.loadMoreError = 'temporary';
    activeFollowing.stale = true;
    activeFollowing.revalidating = true;
    connectionsSession.pendingMutationIDs.add(123);
    await flushPromises();

    expect(scrollTo).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  describe('loading copy', () => {
    it('renders Following initial loading copy with an ellipsis', async () => {
      let resolvePage!: (value: {
        items: { user: ReturnType<typeof user>; following: boolean }[];
        has_more: boolean;
      }) => void;
      const pending = new Promise<{
        items: { user: ReturnType<typeof user>; following: boolean }[];
        has_more: boolean;
      }>((resolve) => {
        resolvePage = resolve;
      });
      mocks.getUserFollowing.mockReturnValueOnce(pending);
      const wrapper = mountConnections();
      await flushPromises();

      expect(wrapper.text()).toContain('Loading following…');
      expect(wrapper.text()).not.toContain('Loading following?');
      resolvePage({ items: [], has_more: false });
      await flushPromises();
      wrapper.unmount();
    });

    it('renders Followers initial loading copy with an ellipsis', async () => {
      mocks.route.name = 'UserFollowers';
      let resolvePage!: (value: {
        items: { user: ReturnType<typeof user>; following: boolean }[];
        has_more: boolean;
      }) => void;
      const pending = new Promise<{
        items: { user: ReturnType<typeof user>; following: boolean }[];
        has_more: boolean;
      }>((resolve) => {
        resolvePage = resolve;
      });
      mocks.getUserFollowers.mockReturnValueOnce(pending);
      const wrapper = mountConnections();
      await flushPromises();

      expect(wrapper.text()).toContain('Loading followers…');
      expect(wrapper.text()).not.toContain('Loading followers?');
      resolvePage({ items: [], has_more: false });
      await flushPromises();
      wrapper.unmount();
    });

    it('renders the corrected pagination loading copy', async () => {
      const wrapper = mountConnections();
      await flushPromises();

      const connectionsSession = useConnectionsSessionStore();
      const followingSession = connectionsSession.getTargetSession(7)?.following;
      expect(followingSession).toBeDefined();
      followingSession!.loadingMore = true;
      await nextTick();

      expect(wrapper.text()).toContain('Loading more…');
      expect(wrapper.text()).not.toContain('Loading more?');
      wrapper.unmount();
    });
  });
});
