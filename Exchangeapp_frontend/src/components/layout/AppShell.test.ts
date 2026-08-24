// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { reactive } from 'vue';
import AppShell from './AppShell.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  notificationStore: null as any,
  route: null as any,
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../../store/notification', () => ({
  useNotificationStore: () => mocks.notificationStore,
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
      setViewer: vi.fn(),
      captureViewer: vi.fn(() => ({ viewerID: 7, generation: 1 })),
      refreshUnreadCount: vi.fn().mockResolvedValue(undefined),
    };
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
  });
});
