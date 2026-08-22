import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const mocks = vi.hoisted(() => ({
  getUnreadNotificationCount: vi.fn(),
}));

vi.mock('../services/notificationService', () => ({
  getUnreadNotificationCount: mocks.getUnreadNotificationCount,
}));

import { useNotificationStore } from './notification';

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('notification store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.getUnreadNotificationCount.mockResolvedValue(0);
  });

  it('caps and hides the shared unread badge', () => {
    const store = useNotificationStore();
    expect(store.unreadBadge).toBeNull();
    store.setUnreadCount(7);
    expect(store.unreadBadge).toBe('7');
    store.setUnreadCount(99);
    expect(store.unreadBadge).toBe('99');
    store.setUnreadCount(100);
    expect(store.unreadBadge).toBe('99+');
    store.setUnreadCount(-20);
    expect(store.unreadBadge).toBeNull();
  });

  it('preserves state when the same viewer refreshes a token', () => {
    const store = useNotificationStore();
    expect(store.setViewer(7)).toBe(true);
    store.setUnreadCount(4);
    const generation = store.viewerGeneration;
    expect(store.setViewer(7)).toBe(false);
    expect(store.viewerGeneration).toBe(generation);
    expect(store.unreadCount).toBe(4);
  });

  it('ignores a late unread response after viewer switch', async () => {
    const pending = deferred<number>();
    mocks.getUnreadNotificationCount.mockReturnValueOnce(pending.promise);
    const store = useNotificationStore();
    store.setViewer(7);
    const capture = store.captureViewer();
    const request = store.refreshUnreadCount(capture);
    store.setViewer(8);
    pending.resolve(12);
    await request;
    expect(store.viewerID).toBe(8);
    expect(store.unreadCount).toBe(0);
  });

  it('ignores a late unread response after logout', async () => {
    const pending = deferred<number>();
    mocks.getUnreadNotificationCount.mockReturnValueOnce(pending.promise);
    const store = useNotificationStore();
    store.setViewer(7);
    const request = store.refreshUnreadCount();
    store.setViewer(null);
    pending.resolve(12);
    await request;
    expect(store.viewerID).toBeNull();
    expect(store.unreadCount).toBe(0);
  });
});
