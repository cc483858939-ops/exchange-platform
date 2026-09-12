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
  routeLeaveGuard: null as (() => void) | null,
  searchUsers: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
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
  readonly root: Element | Document | null;
  readonly rootMargin: string;
  private readonly callback: IntersectionObserverCallback;

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.callback = callback;
    this.root = options?.root ?? null;
    this.rootMargin = options?.rootMargin ?? '';
    TestIntersectionObserver.instances.push(this);
  }

  trigger(isIntersecting = true) {
    this.callback(
      [{ isIntersecting } as IntersectionObserverEntry],
      this as unknown as IntersectionObserver,
    );
  }
}

const setWindowScrollY = (value: number) => {
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
    mocks.routeLeaveGuard = null;
    TestIntersectionObserver.instances = [];
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    setWindowScrollY(0);
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
    const searchSession = useSearchSessionStore();
    const viewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    await nextTick();
    viewport.scrollTop = 640;
    mocks.routeLeaveGuard?.();

    state.showSearch = false;
    await nextTick();
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    resolveSearch({ items: [], has_more: false });
    await settle();

    expect(searchSession.scrollTop).toBe(640);
    expect(viewport.scrollTop).toBe(640);
    wrapper.unmount();
  });

  it('preserves the same-query viewport across UserProfile and ignores global scroll', async () => {
    const { state, wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    const originalViewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    originalViewport.scrollTop = 1400;
    setWindowScrollY(777);
    mocks.routeLeaveGuard?.();
    expect(searchSession.scrollTop).toBe(1400);

    state.showSearch = false;
    await nextTick();
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    await nextTick();

    setWindowScrollY(1200);
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    mocks.route.name = 'UserSearch';
    mocks.route.query = { q: 'alice' };
    state.showSearch = true;
    await settle();

    const restoredViewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    expect(restoredViewport).toBe(originalViewport);
    expect(restoredViewport.scrollTop).toBe(1400);
    expect(scrollTo).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('starts a new query session at the top without restoring the previous query scroll', async () => {
    const { state, wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    const viewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 1400;
    mocks.routeLeaveGuard?.();
    state.showSearch = false;
    await nextTick();
    mocks.route.name = 'UserProfile';
    mocks.route.query = {};
    await nextTick();

    mocks.searchUsers.mockResolvedValueOnce({ items: [], has_more: false });
    mocks.route.name = 'UserSearch';
    mocks.route.query = { q: 'bob' };
    state.showSearch = true;
    await settle();

    expect(searchSession.query).toBe('bob');
    expect(searchSession.scrollTop).toBe(0);
    expect((wrapper.get('.search-scroll-viewport').element as HTMLElement).scrollTop).toBe(0);
    expect(mocks.searchUsers).toHaveBeenLastCalledWith({ q: 'bob', limit: 20, offset: 0 });
    wrapper.unmount();
  });

  it('uses the internal viewport as the pagination observer root', async () => {
    mocks.searchUsers.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: true,
    });
    const { wrapper } = mountKeepAliveSearch();
    await settle();

    const viewport = wrapper.get('.search-scroll-viewport').element;
    const observer = TestIntersectionObserver.instances[0];
    expect(observer.root).toBe(viewport);
    expect(observer.rootMargin).toBe('240px 0px');
    wrapper.unmount();
  });

  it('does not rewind the viewport when pagination state changes', async () => {
    mocks.searchUsers.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: true,
    });
    const { wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();

    const viewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    searchSession.saveScrollTop(500);
    viewport.scrollTop = 500;
    viewport.scrollTop = 820;
    searchSession.items = [...searchSession.items, { user: user(9), following: false }];
    searchSession.hasMore = true;
    searchSession.loadingMore = true;
    await nextTick();
    searchSession.loadingMore = false;
    await nextTick();

    expect(viewport.scrollTop).toBe(820);
    wrapper.unmount();
  });

  it('resets the viewport and session when the search is cleared', async () => {
    const { wrapper } = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    const viewport = wrapper.get('.search-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 900;
    searchSession.saveScrollTop(900);

    await wrapper.get('.search-view__clear').trigger('click');
    mocks.route.query = {};
    await nextTick();

    expect(searchSession.query).toBe('');
    expect(searchSession.scrollTop).toBe(0);
    expect(viewport.scrollTop).toBe(0);
    expect(wrapper.text()).toContain('Search for people by name or @username.');
    wrapper.unmount();
  });

  it('restores scrollTop from the store after a view remount', async () => {
    const first = mountKeepAliveSearch();
    const searchSession = useSearchSessionStore();
    await settle();
    const firstViewport = first.wrapper.get('.search-scroll-viewport').element as HTMLElement;
    firstViewport.scrollTop = 640;
    mocks.routeLeaveGuard?.();
    first.wrapper.unmount();

    const second = mountKeepAliveSearch();
    await settle();

    expect(second.wrapper.get('.search-scroll-viewport').element).not.toBe(firstViewport);
    expect((second.wrapper.get('.search-scroll-viewport').element as HTMLElement).scrollTop).toBe(640);
    expect(searchSession.scrollTop).toBe(640);
    second.wrapper.unmount();
  });
});
