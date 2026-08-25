import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { Notification } from '../types/Notification';

const mocks = vi.hoisted(() => ({
  getNotifications: vi.fn(),
  getUnreadNotificationCount: vi.fn(),
  markNotificationRead: vi.fn(),
  markAllNotificationsRead: vi.fn(),
}));

vi.mock('../services/notificationService', () => ({
  getNotifications: mocks.getNotifications,
  getUnreadNotificationCount: mocks.getUnreadNotificationCount,
  markNotificationRead: mocks.markNotificationRead,
  markAllNotificationsRead: mocks.markAllNotificationsRead,
}));

import { useNotificationStore } from './notification';

const notification = (id: number, read = false): Notification => ({
  id,
  type: 'post_liked',
  actor: { id: 9, username: 'alice', display_name: 'Alice', avatar_url: '' },
  article_id: 42,
  comment_id: null,
  activity_at: '2026-08-22T12:00:00.000Z',
  read,
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

describe('notification store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.getNotifications.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValue(0);
    mocks.markNotificationRead.mockResolvedValue(undefined);
    mocks.markAllNotificationsRead.mockResolvedValue(undefined);
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

  it('preserves the full session when the same viewer refreshes a token', async () => {
    const store = useNotificationStore();
    store.setViewer(7);
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    await store.loadInitial();
    store.setUnreadCount(4);
    store.saveScroll(320);
    const generation = store.viewerGeneration;
    expect(store.setViewer(7)).toBe(false);
    expect(store.viewerGeneration).toBe(generation);
    expect(store.unreadCount).toBe(4);
    expect(store.items.map(item => item.id)).toEqual([1]);
    expect(store.scrollY).toBe(320);
  });

  it('clears the list and ignores late unread/list responses after a viewer switch', async () => {
    const unread = deferred<number>();
    const page = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getUnreadNotificationCount.mockReturnValueOnce(unread.promise);
    mocks.getNotifications.mockReturnValueOnce(page.promise);
    const store = useNotificationStore();
    store.setViewer(7);
    const unreadRequest = store.refreshUnreadCount();
    const listRequest = store.loadInitial();
    store.saveScroll(100);
    store.setViewer(8);
    unread.resolve(12);
    page.resolve({ items: [notification(1)], next_cursor: null });
    await Promise.all([unreadRequest, listRequest]);

    expect(store.viewerID).toBe(8);
    expect(store.unreadCount).toBe(0);
    expect(store.items).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(store.scrollY).toBe(0);
  });

  it('coalesces same-generation unread requests and marks a loaded list stale on increase', async () => {
    const unread = deferred<number>();
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-1' });
    mocks.getUnreadNotificationCount.mockReturnValueOnce(unread.promise);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    const first = store.refreshUnreadCount();
    const second = store.refreshUnreadCount();
    expect(mocks.getUnreadNotificationCount).toHaveBeenCalledTimes(1);
    unread.resolve(2);
    await Promise.all([first, second]);

    expect(store.unreadCount).toBe(2);
    expect(store.listStale).toBe(true);
    expect(store.loadingMore).toBe(false);
    await store.loadMore();
    expect(mocks.getNotifications).toHaveBeenCalledTimes(1);
  });

  it('revalidates the first page while preserving cached tail IDs', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: 'cursor-old' });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(3);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    await store.refreshUnreadCount();

    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
    const request = store.revalidateNotifications();
    expect(store.revalidating).toBe(true);
    expect(store.loading).toBe(false);
    await store.loadMore();
    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
    revalidation.resolve({ items: [notification(3), notification(1, true)], next_cursor: 'cursor-new' });
    await request;

    expect(store.items.map(item => item.id)).toEqual([3, 1, 2]);
    expect(store.items.find(item => item.id === 1)?.read).toBe(true);
    expect(store.nextCursor).toBe('cursor-new');
    expect(store.listStale).toBe(false);
    expect(store.revalidating).toBe(false);
  });

  it('keeps cached items and the stale marker when revalidation fails', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(2);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    await store.refreshUnreadCount();
    mocks.getNotifications.mockRejectedValueOnce(new Error('offline'));
    await store.revalidateNotifications();

    expect(store.items.map(item => item.id)).toEqual([1]);
    expect(store.listStale).toBe(true);
    expect(store.revalidateError).toBeInstanceOf(Error);
    expect(store.revalidating).toBe(false);
  });

  it('optimistically reads and rolls back failed mark-all mutations', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: null });
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    store.setUnreadCount(2);
    const readPending = deferred<void>();
    mocks.markNotificationRead.mockReturnValueOnce(readPending.promise);
    const readRequest = store.markNotificationRead(1);
    expect(store.pendingReadIDs.has(1)).toBe(true);
    expect(store.items.find(item => item.id === 1)?.read).toBe(true);
    readPending.resolve();
    await readRequest;
    expect(store.pendingReadIDs.has(1)).toBe(false);

    mocks.markAllNotificationsRead.mockRejectedValueOnce(new Error('write failed'));
    await expect(store.markAllRead()).rejects.toThrow('write failed');
    expect(store.markAllPending).toBe(false);
    expect(store.items.some(item => !item.read)).toBe(true);
  });
});
