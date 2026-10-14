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
  post_id: 42,
  conversation_id: 42,
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
    store.saveScrollTop(320);
    const generation = store.viewerGeneration;
    expect(store.setViewer(7)).toBe(false);
    expect(store.viewerGeneration).toBe(generation);
    expect(store.unreadCount).toBe(4);
    expect(store.items.map(item => item.id)).toEqual([1]);
    expect(store.scrollTop).toBe(320);
  });

  it('saves valid scrollTop values and normalizes invalid values', () => {
    const store = useNotificationStore();

    store.saveScrollTop(640);
    expect(store.scrollTop).toBe(640);

    for (const value of [-1, NaN, Infinity]) {
      store.saveScrollTop(value);
      expect(store.scrollTop).toBe(0);
    }
  });

  it('increments the notification reselect intent without resetting it on viewer changes', () => {
    const store = useNotificationStore();

    expect(store.notificationReselectVersion).toBe(0);
    store.requestNotificationReselect();
    store.setViewer(7);
    store.requestNotificationReselect();
    store.setViewer(8);

    expect(store.notificationReselectVersion).toBe(2);
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
    store.saveScrollTop(100);
    store.setViewer(8);
    unread.resolve(12);
    page.resolve({ items: [notification(1)], next_cursor: null });
    await Promise.all([unreadRequest, listRequest]);

    expect(store.viewerID).toBe(8);
    expect(store.unreadCount).toBe(0);
    expect(store.items).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(store.scrollTop).toBe(0);
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

  it('marks a loaded list stale when the unread count decreases', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(3);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    store.setUnreadCount(4);

    await store.refreshUnreadCount();

    expect(store.unreadCount).toBe(3);
    expect(store.listStale).toBe(true);
  });

  it('keeps a loaded list fresh when the unread count is unchanged', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(4);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    store.setUnreadCount(4);

    await store.refreshUnreadCount();

    expect(store.unreadCount).toBe(4);
    expect(store.listStale).toBe(false);
  });

  it('replaces cached pages with the refreshed first page and uses its cursor', async () => {
    mocks.getNotifications
      .mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: 'cursor-old' })
      .mockResolvedValueOnce({ items: [notification(3), notification(4)], next_cursor: null });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(3);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    await store.loadMore();
    expect(store.items.map(item => item.id)).toEqual([1, 2, 3, 4]);
    await store.refreshUnreadCount();

    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
    const request = store.revalidateNotifications();
    expect(store.revalidating).toBe(true);
    expect(store.loading).toBe(false);
    await store.loadMore();
    expect(mocks.getNotifications).toHaveBeenCalledTimes(3);
    revalidation.resolve({ items: [notification(3), notification(1, true)], next_cursor: 'cursor-new' });
    await request;

    expect(store.items.map(item => item.id)).toEqual([3, 1]);
    expect(store.items.find(item => item.id === 1)?.read).toBe(true);
    expect(store.nextCursor).toBe('cursor-new');
    expect(store.listStale).toBe(false);
    expect(store.revalidating).toBe(false);

    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(5)], next_cursor: null });
    await store.loadMore();
    expect(mocks.getNotifications).toHaveBeenLastCalledWith({ limit: 20, cursor: 'cursor-new' });
    expect(store.items.map(item => item.id)).toEqual([3, 1, 5]);
  });

  it('force revalidates a loaded list even when it is not stale and preserves cached items while pending', async () => {
    mocks.getNotifications.mockResolvedValueOnce({
      items: [notification(1), notification(2)],
      next_cursor: 'cursor-old',
    });
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();

    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
    const request = store.revalidateNotifications(true);

    expect(store.revalidating).toBe(true);
    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);

    revalidation.resolve({ items: [notification(3), notification(1, true)], next_cursor: 'cursor-new' });
    await request;

    expect(store.items.map(item => item.id)).toEqual([3, 1]);
    expect(store.nextCursor).toBe('cursor-new');
    expect(store.listStale).toBe(false);
    expect(store.revalidating).toBe(false);
  });

  it('removes muted actors and ignores stale list and unread responses during revalidation', async () => {
    const stalePage = deferred<{ items: Notification[]; next_cursor: string | null }>();
    const staleUnread = deferred<number>();
    mocks.getNotifications
      .mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-1' })
      .mockReturnValueOnce(stalePage.promise);
    mocks.getUnreadNotificationCount
      .mockReturnValueOnce(staleUnread.promise)
      .mockResolvedValueOnce(1);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    const oldUnreadRequest = store.refreshUnreadCount();
    const oldPageRequest = store.loadMore();

    store.applyMutedActorStateLocal(9, true);
    expect(store.items).toEqual([]);
    expect(store.listStale).toBe(true);
    await vi.waitFor(() => expect(mocks.getUnreadNotificationCount).toHaveBeenCalledTimes(2));
    await Promise.resolve();
    expect(store.unreadCount).toBe(1);

    staleUnread.resolve(8);
    stalePage.resolve({ items: [notification(2)], next_cursor: null });
    await Promise.all([oldUnreadRequest, oldPageRequest]);
    await vi.waitFor(() => expect(mocks.getNotifications).toHaveBeenCalledTimes(3));
    await vi.waitFor(() => expect(store.revalidating).toBe(false));

    expect(store.unreadCount).toBe(1);
    expect(store.items.map(item => item.id)).toEqual([]);
    expect(store.listStale).toBe(false);
  });

  it('keeps cached items and cursor when revalidation fails, then replaces them on retry', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: 'cursor-old' });
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(2);
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    await store.refreshUnreadCount();
    mocks.getNotifications.mockRejectedValueOnce(new Error('offline'));
    await store.revalidateNotifications();

    expect(store.items.map(item => item.id)).toEqual([1, 2]);
    expect(store.nextCursor).toBe('cursor-old');
    expect(store.listStale).toBe(true);
    expect(store.revalidateError).toBeInstanceOf(Error);
    expect(store.revalidating).toBe(false);

    mocks.getNotifications.mockResolvedValueOnce({ items: [], next_cursor: null });
    await store.revalidateNotifications(true);
    expect(store.items).toEqual([]);
    expect(store.nextCursor).toBeNull();
    expect(store.listStale).toBe(false);
  });

  it('refreshes unread count before the first-page list and continues when unread refresh fails', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();

    const calls: string[] = [];
    mocks.getUnreadNotificationCount.mockImplementationOnce(async () => {
      calls.push('unread');
      throw new Error('badge offline');
    });
    mocks.getNotifications.mockImplementationOnce(async () => {
      calls.push('list');
      return { items: [notification(2)], next_cursor: 'cursor-new' };
    });

    await store.refreshNotifications();

    expect(calls).toEqual(['unread', 'list']);
    expect(store.items.map(item => item.id)).toEqual([2]);
    expect(store.nextCursor).toBe('cursor-new');
    expect(store.listStale).toBe(false);
  });

  it('coalesces concurrent explicit refreshes into one first-page request', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    const unread = deferred<number>();
    const revalidation = deferred<{ items: Notification[]; next_cursor: string | null }>();
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    mocks.getUnreadNotificationCount.mockReturnValueOnce(unread.promise);
    mocks.getNotifications.mockReturnValueOnce(revalidation.promise);

    const first = store.refreshNotifications();
    const second = store.refreshNotifications();
    expect(mocks.getUnreadNotificationCount).toHaveBeenCalledTimes(1);

    unread.resolve(0);
    await vi.waitFor(() => {
      expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
    });

    revalidation.resolve({ items: [notification(2)], next_cursor: null });
    await Promise.all([first, second]);

    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
    expect(store.items.map(item => item.id)).toEqual([2]);
  });

  it('defers a forced revalidation until an in-flight page load finishes', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-old' });
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();

    const oldPage = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(oldPage.promise);
    const paging = store.loadMore();
    await store.revalidateNotifications(true);

    expect(store.listStale).toBe(true);
    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);

    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(3)], next_cursor: null });
    oldPage.resolve({ items: [notification(2)], next_cursor: null });
    await paging;
    await vi.waitFor(() => expect(store.items.map(item => item.id)).toEqual([3]));

    expect(mocks.getNotifications).toHaveBeenCalledTimes(3);
    expect(store.listStale).toBe(false);
  });

  it('runs a refresh requested during initial loading after that request settles', async () => {
    const firstPage = deferred<{ items: Notification[]; next_cursor: string | null }>();
    mocks.getNotifications.mockReturnValueOnce(firstPage.promise);
    const store = useNotificationStore();
    store.setViewer(7);
    const initialLoad = store.loadInitial();
    await store.loadInitial();

    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(2)], next_cursor: null });
    firstPage.resolve({ items: [notification(1)], next_cursor: null });
    await initialLoad;
    await vi.waitFor(() => expect(store.items.map(item => item.id)).toEqual([2]));

    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
  });

  it('does not revalidate a new viewer after an explicit refresh started for the previous viewer', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: null });
    const unread = deferred<number>();
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    mocks.getUnreadNotificationCount.mockReturnValueOnce(unread.promise);

    const request = store.refreshNotifications();
    store.setViewer(8);
    unread.resolve(4);
    await request;

    expect(mocks.getNotifications).toHaveBeenCalledTimes(1);
    expect(store.viewerID).toBe(8);
    expect(store.items).toEqual([]);
  });

  it('optimistically reads and rolls back failed mark-all mutations', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: null });
    const store = useNotificationStore();
    store.setViewer(7);
    await store.loadInitial();
    mocks.getNotifications.mockResolvedValueOnce({
      items: [notification(1, true), notification(2)],
      next_cursor: null,
    });
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

  it('defers a forced revalidation until pending read mutations settle', async () => {
    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1), notification(2)], next_cursor: null });
    const store = useNotificationStore();
    store.setViewer(7);
    store.setUnreadCount(2);
    await store.loadInitial();

    const pendingRead = deferred<void>();
    mocks.markNotificationRead.mockReturnValueOnce(pendingRead.promise);
    const readRequest = store.markNotificationRead(1);
    mocks.getUnreadNotificationCount.mockResolvedValueOnce(1);
    const revalidation = store.revalidateNotifications(true);

    expect(store.listStale).toBe(true);
    expect(mocks.getNotifications).toHaveBeenCalledTimes(1);
    await revalidation;

    mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1, true)], next_cursor: null });
    pendingRead.resolve();
    await readRequest;
    await vi.waitFor(() => expect(store.items.map(item => item.id)).toEqual([1]));

    expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
    expect(store.listStale).toBe(false);
  });
});
