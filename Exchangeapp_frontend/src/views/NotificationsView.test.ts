// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import NotificationsView from './NotificationsView.vue';
import { useNotificationStore } from '../store/notification';
import type { Notification } from '../types/Notification';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  router: { push: vi.fn() },
  routeLeaveGuard: null as (() => void) | null,
  getNotifications: vi.fn(),
  getUnreadNotificationCount: vi.fn(),
  markNotificationRead: vi.fn(),
  markAllNotificationsRead: vi.fn(),
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../services/notificationService', () => ({
  getNotifications: mocks.getNotifications,
  getUnreadNotificationCount: mocks.getUnreadNotificationCount,
  markNotificationRead: mocks.markNotificationRead,
  markAllNotificationsRead: mocks.markAllNotificationsRead,
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: (guard: () => void) => {
    mocks.routeLeaveGuard = guard;
  },
  useRouter: () => mocks.router,
}));

const notification = (id: number, read = false, avatarURL = ''): Notification => ({
  id,
  type: 'post_liked',
  actor: { id: 9, username: 'alice', display_name: 'Alice', avatar_url: avatarURL },
  post_id: 42,
  conversation_id: 42,
  activity_at: '2026-08-22T12:00:00.000Z',
  read,
});

const followedNotification = (id: number, actorID = 9, read = false): Notification => ({
  id,
  type: 'user_followed',
  actor: { id: actorID, username: 'alice', display_name: 'Alice', avatar_url: '' },
  post_id: null,
  conversation_id: null,
  activity_at: '2026-08-22T12:00:00.000Z',
  read,
});

const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
  });
};

const setNotificationViewer = (id: number | null) => {
  useNotificationStore().setViewer(id);
};

const mountView = () => mount(NotificationsView, {
  global: {
    stubs: {
      RouterLink: { template: '<a class="router-link-stub"><slot /></a>' },
    },
  },
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

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

const mountKeepAliveView = () => {
  const state = reactive({ showNotifications: true });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { max: 1 }, {
        default: () => (state.showNotifications ? h(NotificationsView) : null),
      });
    },
  });
  return {
    state,
    wrapper: mount(Host, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    }),
  };
};

describe('NotificationsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.routeLeaveGuard = null;
    setAuth(null);
    setNotificationViewer(null);
    mocks.getNotifications.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValue(0);
    mocks.markNotificationRead.mockResolvedValue(undefined);
    mocks.markAllNotificationsRead.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('does not request notifications while unauthenticated', async () => {
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.text()).toContain('Log in to view your notifications.');
    expect(mocks.getNotifications).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('loads a page and deduplicates IDs across cursor pages', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications
      .mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [notification(1), notification(2, true)], next_cursor: null });
    const wrapper = mountView();
    await flushPromises();
    expect(mocks.getNotifications).toHaveBeenCalledWith({ limit: 20 });
    expect(wrapper.findAll('.notification-card')).toHaveLength(1);

    await wrapper.find('.notifications-page__load-more').trigger('click');
    await flushPromises();
    expect(mocks.getNotifications).toHaveBeenLastCalledWith({ limit: 20, cursor: 'cursor-1' });
    expect(wrapper.findAll('.notification-card')).toHaveLength(2);
    wrapper.unmount();
  });

  it('navigates without waiting for mark-read and rolls back a failed optimistic read', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: null });
    const pending = deferred<void>();
    mocks.markNotificationRead.mockReturnValueOnce(pending.promise);
    const store = useNotificationStore();
    store.setUnreadCount(1);
    const wrapper = mountView();
    await flushPromises();

    await wrapper.find('.notification-card__open').trigger('click');
    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'PostDetail', params: { id: '42' } });
    expect(store.pendingReadIDs.has(1)).toBe(true);
    expect(wrapper.find('.notification-card--unread').exists()).toBe(false);

    pending.reject(new Error('read failed'));
    await flushPromises();
    expect(wrapper.find('.notification-card--unread').exists()).toBe(true);
    expect(mocks.getUnreadNotificationCount).toHaveBeenCalled();
    wrapper.unmount();
  });

  it('renders notification actors through the shared avatar component', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({
      items: [notification(1, false, '/actor.webp')],
      next_cursor: null,
    });
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.get('.notification-card__avatar .user-avatar__image').attributes('src'))
      .toBe('/actor.webp');
    expect(wrapper.get('.notification-card__avatar').attributes('aria-hidden')).toBe('true');
    wrapper.unmount();
  });

  it('keeps cached notifications while revalidating and blocks cursor loading', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-old' });
    const store = useNotificationStore();
    const wrapper = mountView();
    await flushPromises();
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(2);
    await store.refreshUnreadCount();
    expect(store.listStale).toBe(true);
    expect(wrapper.findAll('.notification-card')).toHaveLength(1);

    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
    const request = store.revalidateNotifications();
    expect(store.revalidating).toBe(true);
    expect(store.loading).toBe(false);
    await store.loadMore();
    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
    revalidation.resolve({ items: [notification(2)], next_cursor: 'cursor-new' });
    await request;
    expect(store.listStale).toBe(false);
    expect(store.items.map(item => item.id)).toEqual([2, 1]);
    wrapper.unmount();
  });

  it('saves viewport scrollTop on route leave and disconnects the observer on deactivation', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    TestIntersectionObserver.instances = [];
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: 'cursor-1' });
    const { state, wrapper } = mountKeepAliveView();
    const store = useNotificationStore();
    await flushPromises();
    await nextTick();
    expect(TestIntersectionObserver.instances).toHaveLength(1);
    expect(useNotificationStore().nextCursor).toBe('cursor-1');
    const initialObserver = TestIntersectionObserver.instances[0];
    const viewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;

    viewport.scrollTop = 1800;
    setWindowScrollY(777);
    mocks.routeLeaveGuard?.();
    expect(store.scrollTop).toBe(1800);

    state.showNotifications = false;
    await nextTick();

    expect(initialObserver.disconnect).toHaveBeenCalled();
    wrapper.unmount();
  });

  it('does not recreate the observer or restore scroll while hidden', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    TestIntersectionObserver.instances = [];
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: 'cursor-1' });
    const { state, wrapper } = mountKeepAliveView();
    const store = useNotificationStore();
    await flushPromises();
    await nextTick();
    const initialObserver = TestIntersectionObserver.instances[0];
    state.showNotifications = false;
    await nextTick();

    store.items = [...store.items, notification(2)];
    store.nextCursor = 'cursor-2';
    await nextTick();
    initialObserver.trigger();

    expect(TestIntersectionObserver.instances).toHaveLength(1);
    expect(mocks.getNotifications).toHaveBeenCalledTimes(1);
    expect(store.scrollTop).toBe(0);
    wrapper.unmount();
  });

  it('preserves the cached viewport and never restores the global window on activation', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    TestIntersectionObserver.instances = [];
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: 'cursor-1' });
    const { state, wrapper } = mountKeepAliveView();
    const store = useNotificationStore();
    await flushPromises();
    await nextTick();
    expect(TestIntersectionObserver.instances).toHaveLength(1);
    const originalViewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;
    originalViewport.scrollTop = 1800;
    mocks.routeLeaveGuard?.();
    expect(store.scrollTop).toBe(1800);

    state.showNotifications = false;
    await nextTick();
    setWindowScrollY(1200);
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);

    state.showNotifications = true;
    await flushPromises();
    await nextTick();
    await flushPromises();
    await nextTick();
    await nextTick();
    await nextTick();
    await nextTick();
    expect(useNotificationStore().nextCursor).toBe('cursor-1');
    expect(wrapper.find('.notifications-page__sentinel').exists()).toBe(true);
    expect(useNotificationStore().listStale).toBe(false);
    expect(useNotificationStore().revalidating).toBe(false);
    expect(useNotificationStore().loadingMore).toBe(false);
    expect(useNotificationStore().loadMoreError).toBeNull();

    const restoredViewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;
    expect(restoredViewport).toBe(originalViewport);
    expect(restoredViewport.scrollTop).toBe(1800);
    expect(scrollTo).not.toHaveBeenCalled();
    expect(TestIntersectionObserver.instances.length).toBeGreaterThanOrEqual(2);
    wrapper.unmount();
  });

  it('uses the internal viewport as the pagination observer root', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    TestIntersectionObserver.instances = [];
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: 'cursor-1' });
    const wrapper = mountView();
    await flushPromises();
    await nextTick();

    const viewport = wrapper.get('.notifications-scroll-viewport').element;
    const observer = TestIntersectionObserver.instances[0];
    expect(observer.root).toBe(viewport);
    expect(observer.rootMargin).toBe('240px 0px');
    wrapper.unmount();
  });

  it('keeps the internal scroll position when notification data revalidates', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-old' });
    const store = useNotificationStore();
    const wrapper = mountView();
    await flushPromises();
    const viewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 820;

    mocks.getUnreadNotificationCount.mockResolvedValueOnce(2);
    await store.refreshUnreadCount();
    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
    const request = store.revalidateNotifications();
    revalidation.resolve({ items: [notification(2)], next_cursor: 'cursor-new' });
    await request;
    await nextTick();

    expect(viewport.scrollTop).toBe(820);
    wrapper.unmount();
  });

  it('saves the post notification position before navigating to PostDetail', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: null });
    const store = useNotificationStore();
    const wrapper = mountView();
    await flushPromises();
    const viewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 1400;

    await wrapper.find('.notification-card__open').trigger('click');
    mocks.routeLeaveGuard?.();

    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'PostDetail', params: { id: '42' } });
    expect(store.scrollTop).toBe(1400);
    wrapper.unmount();
  });

  it('navigates user-followed notifications to UserProfile and saves the position', async () => {
    setAuth(7);
    setNotificationViewer(7);
    mocks.getNotifications.mockResolvedValue({
      items: [followedNotification(2, 9)],
      next_cursor: null,
    });
    const store = useNotificationStore();
    const wrapper = mountView();
    await flushPromises();
    const viewport = wrapper.get('.notifications-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 980;

    await wrapper.find('.notification-card__open').trigger('click');
    mocks.routeLeaveGuard?.();

    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'UserProfile', params: { id: '9' } });
    expect(store.scrollTop).toBe(980);
    wrapper.unmount();
  });
});
