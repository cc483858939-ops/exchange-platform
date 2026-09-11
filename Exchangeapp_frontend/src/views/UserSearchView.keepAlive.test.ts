// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
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
  syncExternalFollowState: vi.fn(),
}));

const user = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

class TestIntersectionObserver {
  static instances: TestIntersectionObserver[] = [];
  readonly observe = vi.fn();
  readonly disconnect = vi.fn();
  private readonly callback: IntersectionObserverCallback;

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback;
    TestIntersectionObserver.instances.push(this);
  }

  trigger(isIntersecting = true) {
    this.callback(
      [{ isIntersecting } as IntersectionObserverEntry],
      this as unknown as IntersectionObserver,
    );
  }
}

const setScrollY = (value: number) => {
  Object.defineProperty(window, 'scrollY', { configurable: true, value });
};

const mountKeepAliveSearch = () => {
  const state = reactive({ showSearch: true });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { max: 1 }, {
        default: () => (state.showSearch ? h(UserSearchView) : null),
      });
    },
  });
  return { state, wrapper: mount(Host, {
    global: {
      stubs: {
        AppIcon: { template: '<span />' },
        RouterLink: { template: '<a><slot /></a>' },
        UserRow: {
          props: ['item'],
          template: '<button class="test-user">{{ item.user.username }}</button>',
        },
      },
    },
  }) };
};

const settle = async () => {
  await flushPromises();
  await nextTick();
};

describe('UserSearchView KeepAlive lifecycle', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    TestIntersectionObserver.instances = [];
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    setScrollY(0);
    mocks.route = reactive({ name: 'UserSearch', query: { q: 'alice' } });
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7, username: 'viewer-7' },
    });
    mocks.searchUsers.mockResolvedValue({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
  });

  it('does not clear the cached query when another route becomes active', async () => {
    const { state, wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    expect(searchSession.query).toBe('alice');
    const activateQuery = vi.spyOn(searchSession, 'activateQuery');
    activateQuery.mockClear();

    state.showSearch = false;
    await nextTick();
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    await nextTick();

    expect(searchSession.query).toBe('alice');
    expect(activateQuery).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('disconnects and does not recreate its observer while hidden', async () => {
    mocks.searchUsers.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: true,
    });
    const { state, wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    expect(TestIntersectionObserver.instances).toHaveLength(1);
    const initialObserver = TestIntersectionObserver.instances[0];

    state.showSearch = false;
    await nextTick();
    expect(initialObserver.disconnect).toHaveBeenCalled();

    searchSession.items = [...searchSession.items];
    searchSession.hasMore = true;
    searchSession.loadingMore = false;
    await nextTick();
    expect(TestIntersectionObserver.instances).toHaveLength(1);
    initialObserver.trigger();
    expect(mocks.searchUsers).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it('does not restore scroll when an async search completes while hidden', async () => {
    let resolveSearch!: (value: { items: { user: ReturnType<typeof user>; following: boolean }[]; has_more: boolean }) => void;
    const pending = new Promise<{ items: { user: ReturnType<typeof user>; following: boolean }[]; has_more: boolean }>((resolve) => {
      resolveSearch = resolve;
    });
    mocks.searchUsers.mockReturnValueOnce(pending);
    const { state, wrapper } = mountKeepAliveSearch();
    await nextTick();

    state.showSearch = false;
    await nextTick();
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    setScrollY(250);
    vi.mocked(window.scrollTo).mockClear();
    resolveSearch({ items: [], has_more: false });
    await settle();

    expect(window.scrollTo).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('restores the saved scroll position when reactivated for the same query', async () => {
    const { state, wrapper } = mountKeepAliveSearch();
    await settle();
    vi.mocked(window.scrollTo).mockClear();
    setScrollY(1400);
    state.showSearch = false;
    await nextTick();
    setScrollY(250);
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    await nextTick();

    mocks.route.name = 'UserSearch';
    mocks.route.query = { q: 'alice' };
    state.showSearch = true;
    await settle();

    expect(window.scrollTo).toHaveBeenCalledWith({ top: 1400, behavior: 'auto' });
    wrapper.unmount();
  });

  it('starts a new query session without restoring the previous query scroll', async () => {
    const { state, wrapper } = mountKeepAliveSearch();
    await settle();
    vi.mocked(window.scrollTo).mockClear();
    setScrollY(1400);
    state.showSearch = false;
    await nextTick();
    setScrollY(250);
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    await nextTick();

    mocks.searchUsers.mockResolvedValueOnce({ items: [], has_more: false });
    mocks.route.name = 'UserSearch';
    mocks.route.query = { q: 'bob' };
    state.showSearch = true;
    await settle();

    expect(useSearchSessionStore().query).toBe('bob');
    expect(mocks.searchUsers).toHaveBeenLastCalledWith({ q: 'bob', limit: 20, offset: 0 });
    expect(window.scrollTo).not.toHaveBeenCalledWith({ top: 1400, behavior: 'auto' });
    wrapper.unmount();
  });
});
