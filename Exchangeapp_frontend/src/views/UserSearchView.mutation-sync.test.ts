// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UserSearchView from './UserSearchView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  router: { push: vi.fn() },
  searchUsers: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  externalFollow: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));
vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/userService', () => ({
  searchUsers: mocks.searchUsers,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));
vi.mock('../store/sessionSync', () => ({
  syncExternalFollowState: mocks.externalFollow,
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

const mountSearch = () => mount(UserSearchView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      RouterLink: { template: '<a><slot /></a>' },
      UserRow: {
        props: ['item', 'pending', 'error'],
        emits: ['toggle-follow'],
        template: '<button class="test-follow" :data-following="item.following" @click="$emit(\'toggle-follow\', item.user.id)">{{ item.user.username }}</button>',
      },
    },
  },
});

describe('UserSearchView mutation synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({ query: { q: 'alice' } });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer token-a',
      currentIdentity: { id: 7, username: 'viewer' },
    });
    mocks.searchUsers.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    mocks.followUser.mockResolvedValue(followResponse(8, true));
    mocks.unfollowUser.mockResolvedValue(followResponse(8, false));
  });

  it('syncs a successful follow and unfollow exactly once', async () => {
    const wrapper = mountSearch();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();
    expect(mocks.externalFollow).toHaveBeenCalledWith(followResponse(8, true));

    mocks.externalFollow.mockClear();
    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();
    expect(mocks.externalFollow).toHaveBeenCalledWith(followResponse(8, false));
  });

  it('rolls back a failed follow without external synchronization', async () => {
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));
    const wrapper = mountSearch();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();

    expect(wrapper.find('.test-follow').attributes('data-following')).toBe('false');
    expect(mocks.externalFollow).not.toHaveBeenCalled();
  });

  it('keeps the same viewer request valid across an access-token refresh', async () => {
    let resolvePage!: (value: { items: { user: ReturnType<typeof user>; following: boolean }[]; has_more: boolean }) => void;
    const pending = new Promise<{ items: { user: ReturnType<typeof user>; following: boolean }[]; has_more: boolean }>((resolve) => {
      resolvePage = resolve;
    });
    mocks.searchUsers.mockReturnValueOnce(pending);
    const wrapper = mountSearch();
    expect(mocks.searchUsers).toHaveBeenCalledTimes(1);

    mocks.authStore.token = 'Bearer token-b';
    await flushPromises();
    expect(mocks.searchUsers).toHaveBeenCalledTimes(1);

    resolvePage({ items: [{ user: user(8), following: false }], has_more: false });
    await flushPromises();
    expect(wrapper.find('.test-follow').exists()).toBe(true);
  });
});
