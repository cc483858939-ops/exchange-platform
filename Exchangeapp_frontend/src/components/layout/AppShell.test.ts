// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { reactive } from 'vue';
import AppShell from './AppShell.vue';

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

const mountShell = () => mount(AppShell, {
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

describe('AppShell mobile structure', () => {
  beforeEach(() => {
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
      revalidateNotifications: vi.fn().mockResolvedValue(undefined),
    };
    mocks.searchSession = { setViewer: vi.fn() };
    mocks.route = reactive({ name: 'Home' });
  });

  it('mounts the shared shell pieces and forwards the unread badge', () => {
    const wrapper = mountShell();

    expect(wrapper.find('.test-left-sidebar').exists()).toBe(true);
    expect(wrapper.find('.test-right-rail').exists()).toBe(true);
    expect(wrapper.find('.test-bottom-nav').attributes('data-badge')).toBe('4');
    expect(wrapper.find('.test-content').text()).toBe('Content');
    expect(wrapper.find('.app-layout__mobile-nav').exists()).toBe(false);
    expect(wrapper.find('.app-layout__mobile-account').exists()).toBe(false);
    expect(wrapper.find('.app-layout__mobile-links').exists()).toBe(false);
    expect(mocks.searchSession.setViewer).toHaveBeenCalledWith(7);
    wrapper.unmount();
  });

  it('refreshes unread state when the document becomes visible', async () => {
    const wrapper = mountShell();
    mocks.notificationStore.refreshUnreadCount.mockClear();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    expect(mocks.notificationStore.refreshUnreadCount).not.toHaveBeenCalled();

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    document.dispatchEvent(new Event('visibilitychange'));
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it('revalidates a stale notification list after the Notifications route refresh', async () => {
    mocks.notificationStore.listStale = true;
    mocks.route.name = 'Notifications';
    const wrapper = mountShell();
    await Promise.resolve();
    expect(mocks.notificationStore.revalidateNotifications).toHaveBeenCalled();
    wrapper.unmount();
  });
});
