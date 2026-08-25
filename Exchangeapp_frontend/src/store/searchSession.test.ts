import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const mocks = vi.hoisted(() => ({
  searchUsers: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  syncExternalFollowState: vi.fn(),
}));

vi.mock('../services/userService', () => ({
  searchUsers: mocks.searchUsers,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

vi.mock('./sessionSync', () => ({
  syncExternalFollowState: mocks.syncExternalFollowState,
}));

import { useSearchSessionStore } from './searchSession';

const user = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const item = (id: number, following = false) => ({ user: user(id), following });

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
};

describe('search session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.searchUsers.mockResolvedValue({ items: [], has_more: false });
  });

  it('normalizes the route query and does not refetch the loaded same query', async () => {
    mocks.searchUsers.mockResolvedValueOnce({ items: [item(8)], has_more: false });
    const store = useSearchSessionStore();
    store.setViewer(7);

    store.activateQuery('  @alice  ');
    await vi.waitFor(() => expect(store.loaded).toBe(true));
    expect(store.query).toBe('alice');
    expect(store.inputValue).toBe('alice');
    expect(mocks.searchUsers).toHaveBeenCalledTimes(1);
    expect(mocks.searchUsers).toHaveBeenCalledWith({ q: 'alice', limit: 20, offset: 0 });

    store.activateQuery('alice');
    await store.loadInitial();
    expect(mocks.searchUsers).toHaveBeenCalledTimes(1);
  });

  it('ignores an old query response after a query change', async () => {
    const first = deferred<{ items: ReturnType<typeof item>[]; has_more: boolean }>();
    const second = deferred<{ items: ReturnType<typeof item>[]; has_more: boolean }>();
    mocks.searchUsers.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const store = useSearchSessionStore();
    store.setViewer(7);
    store.activateQuery('alice');
    store.activateQuery('bob');

    second.resolve({ items: [item(9)], has_more: false });
    await vi.waitFor(() => expect(store.items.map(entry => entry.user.id)).toEqual([9]));
    first.resolve({ items: [item(8)], has_more: false });
    await Promise.resolve();

    expect(store.query).toBe('bob');
    expect(store.items.map(entry => entry.user.id)).toEqual([9]);
  });

  it('clears the page and ignores a late response when the viewer changes', async () => {
    const pending = deferred<{ items: ReturnType<typeof item>[]; has_more: boolean }>();
    mocks.searchUsers.mockReturnValueOnce(pending.promise);
    const store = useSearchSessionStore();
    store.setViewer(7);
    store.activateQuery('alice');
    store.saveScroll(480);
    store.setViewer(8);
    pending.resolve({ items: [item(8)], has_more: false });
    await Promise.resolve();

    expect(store.viewerID).toBe(8);
    expect(store.items).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(store.scrollY).toBe(0);
  });

  it('deduplicates cursor pages and advances the offset by the response size', async () => {
    mocks.searchUsers
      .mockResolvedValueOnce({ items: [item(8), item(9)], has_more: true })
      .mockResolvedValueOnce({ items: [item(9), item(10)], has_more: false });
    const store = useSearchSessionStore();
    store.setViewer(7);
    store.activateQuery('alice');
    await vi.waitFor(() => expect(store.loaded).toBe(true));
    await store.loadMore();

    expect(mocks.searchUsers).toHaveBeenLastCalledWith({ q: 'alice', limit: 20, offset: 2 });
    expect(store.items.map(entry => entry.user.id)).toEqual([8, 9, 10]);
    expect(store.nextOffset).toBe(4);
    expect(store.hasMore).toBe(false);
  });

  it('keeps a pending follow mutation valid across a query change and syncs it once', async () => {
    const followPending = deferred<{ user_id: number; following: boolean; follower_count: number; following_count: number }>();
    mocks.searchUsers.mockResolvedValueOnce({ items: [item(8)], has_more: false });
    mocks.followUser.mockReturnValueOnce(followPending.promise);
    const response = { user_id: 8, following: true, follower_count: 1, following_count: 0 };
    const store = useSearchSessionStore();
    store.setViewer(7);
    store.activateQuery('alice');
    await vi.waitFor(() => expect(store.loaded).toBe(true));
    const mutation = store.toggleFollow(8);
    store.activateQuery('bob');
    followPending.resolve(response);
    await mutation;

    expect(mocks.syncExternalFollowState).toHaveBeenCalledTimes(1);
    expect(mocks.syncExternalFollowState).toHaveBeenCalledWith(response);
    expect(store.query).toBe('bob');
    expect(store.pendingMutationIDs.size).toBe(0);
  });
});
