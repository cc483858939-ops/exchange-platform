// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import UserSearchView from './UserSearchView.vue';
import { useSearchSessionStore } from '../store/searchSession';

const mocks = vi.hoisted(() => ({
  route: null as any,
  routeLeaveGuard: null as (() => void) | null,
  authStore: null as any,
  router: { push: vi.fn() },
  searchUsers: vi.fn(),
  getSuggestedUsers: vi.fn(),
  getUser: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  externalFollow: vi.fn(),
  registerSuggestedUsersSessionSync: vi.fn(() => vi.fn()),
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: (guard: () => void) => {
    mocks.routeLeaveGuard = guard;
  },
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));
vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/userService', () => ({
  searchUsers: mocks.searchUsers,
  getSuggestedUsers: mocks.getSuggestedUsers,
  getUser: mocks.getUser,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));
vi.mock('../store/sessionSync', () => ({
  registerSearchSessionSync: vi.fn(),
  registerPostSearchSessionSync: vi.fn(),
  syncPostSearchLikeState: vi.fn(),
  syncPostSearchRepostState: vi.fn(),
  syncPostSearchBookmarkState: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
  syncExternalFollowState: mocks.externalFollow,
  registerSuggestedUsersSessionSync: mocks.registerSuggestedUsersSessionSync,
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
    mocks.routeLeaveGuard = null;
    mocks.route = reactive({ name: 'UserSearch', query: { tab: 'people', q: 'alice' } });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer token-a',
      sessionVersion: 0,
      currentIdentity: { id: 7, username: 'viewer' },
    });
    mocks.searchUsers.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    mocks.followUser.mockResolvedValue(followResponse(8, true));
    mocks.unfollowUser.mockResolvedValue(followResponse(8, false));
    mocks.getSuggestedUsers.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
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

  describe('People suggestions', () => {
    it('loads at most five real suggestions only in the empty People state', async () => {
      mocks.route.query = { tab: 'people' };
      mocks.getSuggestedUsers.mockResolvedValueOnce({
        items: [8, 9, 10, 11, 12, 13].map(id => ({ user: user(id), following: false })),
        has_more: false,
      });
      const wrapper = mountSearch();
      await flushPromises();

      expect(mocks.getSuggestedUsers).toHaveBeenCalledExactlyOnceWith(5);
      expect(wrapper.text()).toContain('Suggested people');
      expect(wrapper.findAll('.test-follow')).toHaveLength(5);
      expect(mocks.searchUsers).not.toHaveBeenCalled();
      wrapper.unmount();
    });

    it('hides suggestions for a valid draft without searching until submit, then restores them when cleared', async () => {
      mocks.route.query = { tab: 'people' };
      const wrapper = mountSearch();
      await flushPromises();
      expect(mocks.getSuggestedUsers).toHaveBeenCalledOnce();

      const input = wrapper.get('input[type="search"]');
      await input.setValue('   ');
      expect(wrapper.text()).toContain('Suggested people');
      expect(mocks.searchUsers).not.toHaveBeenCalled();

      await input.setValue('  alice  ');
      await nextTick();
      expect(wrapper.text()).not.toContain('Suggested people');
      expect(mocks.searchUsers).not.toHaveBeenCalled();
      expect(mocks.getSuggestedUsers).toHaveBeenCalledOnce();

      await wrapper.get('form[role="search"]').trigger('submit');
      expect(mocks.router.push).toHaveBeenCalled();
      mocks.route.query = { tab: 'people', q: 'alice' };
      await flushPromises();
      expect(mocks.searchUsers).toHaveBeenCalledWith({ q: 'alice', limit: 20, offset: 0 });
      expect(mocks.getSuggestedUsers).toHaveBeenCalledOnce();

      mocks.route.query = { tab: 'people' };
      await flushPromises();
      expect(mocks.getSuggestedUsers).toHaveBeenCalledTimes(2);
      expect(mocks.searchUsers).toHaveBeenCalledTimes(1);
      expect(wrapper.text()).toContain('Suggested people');
      wrapper.unmount();
    });

    it('falls back to the existing hint for empty or failed suggestions and never requests them for Posts or guests', async () => {
      mocks.route.query = { tab: 'people' };
      mocks.getSuggestedUsers.mockResolvedValueOnce({ items: [], has_more: false });
      const empty = mountSearch();
      await flushPromises();
      expect(empty.text()).toContain('Search for people by name or @username.');
      expect(empty.find('.search-view__state--error').exists()).toBe(false);
      empty.unmount();

      mocks.getSuggestedUsers.mockRejectedValueOnce(new Error('offline'));
      const failed = mountSearch();
      await flushPromises();
      expect(failed.text()).toContain('Search for people by name or @username.');
      expect(failed.text()).not.toContain('offline');
      failed.unmount();

      mocks.route.query = { tab: 'posts' };
      mocks.getSuggestedUsers.mockClear();
      const posts = mountSearch();
      await flushPromises();
      expect(mocks.getSuggestedUsers).not.toHaveBeenCalled();
      posts.unmount();

      mocks.route.query = { tab: 'people' };
      mocks.authStore.isAuthenticated = false;
      const guest = mountSearch();
      await flushPromises();
      expect(mocks.getSuggestedUsers).not.toHaveBeenCalled();
      expect(guest.text()).toContain('Log in to search posts and people.');
      guest.unmount();
    });

    it('uses the shared Follow state machine and syncs successful discovery follows', async () => {
      mocks.route.query = { tab: 'people' };
      const wrapper = mountSearch();
      await flushPromises();
      await wrapper.get('.test-follow').trigger('click');
      await flushPromises();

      expect(mocks.followUser).toHaveBeenCalledExactlyOnceWith(8);
      expect(mocks.externalFollow).toHaveBeenCalledExactlyOnceWith(followResponse(8, true));
      expect(wrapper.get('.test-follow').attributes('data-following')).toBe('true');
      wrapper.unmount();
    });
  });
});
