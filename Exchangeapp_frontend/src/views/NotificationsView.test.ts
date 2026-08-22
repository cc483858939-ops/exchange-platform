// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import NotificationsView from './NotificationsView.vue';
import type { Notification } from '../types/Notification';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  notificationStore: null as any,
  router: { push: vi.fn() },
  getNotifications: vi.fn(),
  markNotificationRead: vi.fn(),
  markAllNotificationsRead: vi.fn(),
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/notification', () => ({
  useNotificationStore: () => mocks.notificationStore,
}));

vi.mock('../services/notificationService', () => ({
  getNotifications: mocks.getNotifications,
  markNotificationRead: mocks.markNotificationRead,
  markAllNotificationsRead: mocks.markAllNotificationsRead,
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

const notification = (id: number, read = false): Notification => ({
  id,
  type: 'post_liked',
  actor: { id: 9, username: 'alice', display_name: 'Alice', avatar_url: '' },
  article_id: 42,
  comment_id: null,
  activity_at: '2026-08-22T12:00:00.000Z',
  read,
});

const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
  });
};

const setNotificationStore = (id: number | null) => {
  const state = reactive({
    viewerID: id,
    unreadCount: id === null ? 0 : 1,
  });
  const store = {
    ...state,
    captureViewer: vi.fn(() => state.viewerID === null ? null : { viewerID: state.viewerID, generation: 1 }),
    isCurrentViewer: vi.fn((capture: { viewerID: number } | null) => capture?.viewerID === state.viewerID),
    setViewer: vi.fn((nextID: number | null) => { state.viewerID = nextID; }),
    decrementUnread: vi.fn(() => { state.unreadCount = Math.max(0, state.unreadCount - 1); }),
    incrementUnread: vi.fn(() => { state.unreadCount += 1; }),
    setUnreadCount: vi.fn((count: number) => { state.unreadCount = Math.max(0, count); }),
    refreshUnreadCount: vi.fn().mockResolvedValue(0),
  };
  Object.defineProperty(store, 'unreadCount', { get: () => state.unreadCount, set: (value) => { state.unreadCount = value; } });
  mocks.notificationStore = store;
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

describe('NotificationsView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setAuth(null);
    setNotificationStore(null);
    mocks.getNotifications.mockResolvedValue({ items: [], next_cursor: null });
    mocks.markNotificationRead.mockResolvedValue(undefined);
    mocks.markAllNotificationsRead.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('does not request notifications while unauthenticated', async () => {
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.text()).toContain('Log in to view your notifications.');
    expect(mocks.getNotifications).not.toHaveBeenCalled();
  });

  it('loads a page and deduplicates IDs across cursor pages', async () => {
    setAuth(7);
    setNotificationStore(7);
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
  });

  it('navigates without waiting for mark-read and rolls back a failed optimistic read', async () => {
    setAuth(7);
    setNotificationStore(7);
    mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: null });
    const pending = deferred<void>();
    mocks.markNotificationRead.mockReturnValueOnce(pending.promise);
    const wrapper = mountView();
    await flushPromises();

    await wrapper.find('.notification-card__open').trigger('click');
    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'NewsDetail', params: { id: '42' } });
    expect(mocks.notificationStore.decrementUnread).toHaveBeenCalledTimes(1);
    expect(wrapper.find('.notification-card--unread').exists()).toBe(false);

    pending.reject(new Error('read failed'));
    await flushPromises();
    expect(wrapper.find('.notification-card--unread').exists()).toBe(true);
    expect(mocks.notificationStore.refreshUnreadCount).toHaveBeenCalled();
  });
});
