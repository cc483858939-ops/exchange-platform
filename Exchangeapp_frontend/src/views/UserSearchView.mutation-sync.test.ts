// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import UserSearchView from './UserSearchView.vue';
import { useSearchSessionStore } from '../store/searchSession';

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
  registerSearchSessionSync: vi.fn(),
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
        props: ['item', 'pending', 'error', 'isSelf'],
        emits: ['toggle-follow'],
        template: '<button class="test-follow" :data-following="item.following" @click="$emit(\'toggle-follow\', item.user.id)">{{ item.user.username }}</button>',
      },
    },
  },
});

describe('UserSearchView mutation synchronization', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
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
    wrapper.unmount();
  });

  it('rolls back a failed follow without external synchronization', async () => {
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));
    const wrapper = mountSearch();
    await flushPromises();

    await wrapper.find('.test-follow').trigger('click');
    await flushPromises();

    expect(wrapper.find('.test-follow').attributes('data-following')).toBe('false');
    expect(mocks.externalFollow).not.toHaveBeenCalled();
    wrapper.unmount();
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
    wrapper.unmount();
  });

  describe('loading and empty-state copy', () => {
    it('renders the corrected initial loading copy', async () => {
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
      mocks.searchUsers.mockReturnValueOnce(pending);
      const wrapper = mountSearch();
      await nextTick();

      expect(wrapper.text()).toContain('Searching people…');
      expect(wrapper.text()).not.toContain('Searching people?');
      resolvePage({ items: [], has_more: false });
      await flushPromises();
      wrapper.unmount();
    });

    it('renders the quoted empty-result query as text', async () => {
      mocks.searchUsers.mockResolvedValueOnce({ items: [], has_more: false });
      const wrapper = mountSearch();
      await flushPromises();

      expect(wrapper.text()).toContain('No people found for “alice”.');
      expect(wrapper.text()).not.toContain('No people found for ?alice?.');
      wrapper.unmount();
    });

    it('renders the corrected pagination loading copy', async () => {
      mocks.searchUsers.mockResolvedValueOnce({
        items: [{ user: user(8), following: false }],
        has_more: true,
      });
      const wrapper = mountSearch();
      await flushPromises();

      const searchSession = useSearchSessionStore();
      searchSession.loadingMore = true;
      await nextTick();

      expect(wrapper.text()).toContain('Loading more…');
      expect(wrapper.text()).not.toContain('Loading more?');
      wrapper.unmount();
    });
  });
});
