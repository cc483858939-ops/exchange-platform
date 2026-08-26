// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UserConnectionsView from './UserConnectionsView.vue';

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
});
