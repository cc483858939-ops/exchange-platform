// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import AppShell from './AppShell.vue';

const NOTIFICATION_POLL_INTERVAL_MS = 60_000;

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  notificationStore: null as any,
  searchSession: null as any,
  route: null as any,
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../../store/notification', () => ({
  useNotificationStore: () => mocks.notificationStore,
}));

vi.mock('../../store/searchSession', () => ({
  useSearchSessionStore: () => mocks.searchSession,
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

const mountedWrappers: Array<{ unmount: () => void }> = [];

const mountShell = () => {
  const wrapper = mount(AppShell, {
    slots: { default: '<p class="test-content">Content</p>' },
    global: {
      stubs: {
        LeftSidebar: {
          props: ['notificationBadge'],
          template: '<aside class="test-left-sidebar" :data-badge="notificationBadge" />',
        },
        RightRail: { template: '<aside class="test-right-rail" />' },
        MobileBottomNav: {
          props: ['notificationBadge'],
          template: '<nav class="test-bottom-nav" :data-badge="notificationBadge" />',
        },
      },
    },
  });
  mountedWrappers.push(wrapper);
  return wrapper;
};

const unmountShell = (wrapper: { unmount: () => void }) => {
  wrapper.unmount();
  const index = mountedWrappers.indexOf(wrapper);
  if (index >= 0) {
    mountedWrappers.splice(index, 1);
  }
};

const setVisibility = (value: 'visible' | 'hidden') => {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value });
};

const flushAsync = async () => {
  for (let index = 0; index < 6; index += 1) {
    await Promise.resolve();
    await nextTick();
  }
};

const mountAfterInitialRefresh = async () => {
  const wrapper = mountShell();
  await flushAsync();
  mocks.notificationStore.refreshUnreadCount.mockClear();
  mocks.notificationStore.revalidateNotifications.mockClear();
  return wrapper;
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('AppShell mobile structure and notification freshness', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setVisibility('visible');
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7 },
    });
    mocks.notificationStore = {
      unreadBadge: '4',
      listStale: false,
      setViewer: vi.fn(),
      captureViewer: vi.fn(() => ({ viewerID: 7, generation: 1 })),
      refreshUnreadCount: vi.fn().mockResolvedValue(undefined),
      revalidateNotifications: vi.fn(async () => {
        mocks.notificationStore.listStale = false;
      }),
    };
    mocks.searchSession = { setViewer: vi.fn() };
    mocks.route = reactive({ name: 'Home' });
  });

  afterEach(() => {
    mountedWrappers.splice(0).forEach(wrapper => wrapper.unmount());
    vi.clearAllTimers();
    vi.useRealTimers();
    setVisibility('visible');
  });

  it('mounts the shared shell pieces, forwards the badge, and refreshes immediately', async () => {
    const wrapper = mountShell();
    await flushAsync();

    expect(wrapper.find('.test-left-sidebar').exists()).toBe(true);
    expect(wrapper.find('.test-right-rail').exists()).toBe(true);
    expect(wrapper.find('.test-bottom-nav').attributes('data-badge')).toBe('4');
    expect(wrapper.find('.test-content').text()).toBe('Content');
    expect(wrapper.find('.app-layout__mobile-nav').exists()).toBe(false);
    expect(wrapper.find('.app-layout__mobile-account').exists()).toBe(false);
    expect(wrapper.find('.app-layout__mobile-links').exists()).toBe(false);
    expect(mocks.searchSession.setViewer).toHaveBeenCalledWith(7);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
  });

  it('does not refresh or schedule polling for a guest', async () => {
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    const wrapper = mountShell();
    await flushAsync();

    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS * 3);
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();
    unmountShell(wrapper);
  });

  it('polls every 60 seconds after the previous refresh settles', async () => {
    const wrapper = await mountAfterInitialRefresh();

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS - 1);
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    unmountShell(wrapper);
  });

  it('does not overlap scheduled refreshes while a request is pending', async () => {
    const wrapper = await mountAfterInitialRefresh();
    const pending = deferred<void>();
    mocks.notificationStore.refreshUnreadCount.mockImplementationOnce(() => pending.promise);

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS * 3);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);

    pending.resolve(undefined);
    await flushAsync();
    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS - 1);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    unmountShell(wrapper);
  });

  it('stops polling while hidden and refreshes immediately when visible again', async () => {
    const wrapper = await mountAfterInitialRefresh();

    setVisibility('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('online'));
    window.dispatchEvent(new Event('pageshow'));
    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS * 3);
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();

    setVisibility('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await flushAsync();
    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    unmountShell(wrapper);
  });

  it('restarts the full polling interval after returning to the foreground', async () => {
    const wrapper = await mountAfterInitialRefresh();
    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS / 2);

    setVisibility('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    setVisibility('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await flushAsync();

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS / 2);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync((NOTIFICATION_POLL_INTERVAL_MS / 2) - 1);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    unmountShell(wrapper);
  });

  it('refreshes immediately on online and pageshow events while visible', async () => {
    const wrapper = await mountAfterInitialRefresh();

    window.dispatchEvent(new Event('online'));
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    await flushAsync();
    window.dispatchEvent(new Event('pageshow'));
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    await flushAsync();

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(3);
    unmountShell(wrapper);
  });

  it('clears polling after logout', async () => {
    const wrapper = await mountAfterInitialRefresh();
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    await nextTick();
    await flushAsync();
    mocks.notificationStore.refreshUnreadCount.mockClear();

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS * 3);
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();
    unmountShell(wrapper);
  });

  it('refreshes on Notifications entry and revalidates only a stale list', async () => {
    const wrapper = await mountAfterInitialRefresh();
    mocks.route.name = 'Profile';
    await nextTick();
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();

    mocks.notificationStore.listStale = true;
    mocks.route.name = 'Notifications';
    await nextTick();
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    expect(mocks.notificationStore.revalidateNotifications).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    expect(mocks.notificationStore.revalidateNotifications).toHaveBeenCalledTimes(1);
    unmountShell(wrapper);
  });

  it('continues polling after a refresh failure settles', async () => {
    const wrapper = await mountAfterInitialRefresh();
    mocks.notificationStore.refreshUnreadCount.mockRejectedValueOnce(new Error('network error'));

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS);
    await flushAsync();
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(2);
    unmountShell(wrapper);
  });

  it('cleans up the timer and event listeners on unmount', async () => {
    const wrapper = await mountAfterInitialRefresh();
    unmountShell(wrapper);
    mocks.notificationStore.refreshUnreadCount.mockClear();

    await vi.advanceTimersByTimeAsync(NOTIFICATION_POLL_INTERVAL_MS * 2);
    setVisibility('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    setVisibility('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('online'));
    window.dispatchEvent(new Event('pageshow'));

    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();
  });
});
