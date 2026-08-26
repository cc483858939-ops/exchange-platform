// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { useConnectionsSessionStore } from './connectionsSession';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  getSession: vi.fn(),
  getUser: vi.fn(),
  getUserFollowers: vi.fn(),
  getUserFollowing: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  connectionsSync: null as any,
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('./profileSession', () => ({
  useProfileSessionStore: () => ({ getSession: mocks.getSession }),
}));
vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserFollowers: mocks.getUserFollowers,
  getUserFollowing: mocks.getUserFollowing,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));
vi.mock('./sessionSync', () => ({
  registerConnectionsSessionSync: vi.fn((sync: any) => { mocks.connectionsSync = sync; }),
  syncExternalFollowState: vi.fn((state: any) => {
    mocks.connectionsSync?.applyExternalFollowStateLocal(state);
  }),
}));

const user = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: `Bio ${id}`,
  created_at: '2026-08-15T00:00:00.000Z',
});

const page = (ids: number[], hasMore = false) => ({
  items: ids.map(id => ({ user: user(id), following: false })),
  has_more: hasMore,
});

const followResponse = (id: number, following: boolean) => ({
  user_id: id,
  following,
  follower_count: following ? 1 : 0,
  following_count: 0,
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

const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
  });
};

const createStore = (id = 7) => {
  setAuth(id);
  setActivePinia(createPinia());
  return useConnectionsSessionStore();
};

describe('connectionsSession store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.connectionsSync = null;
    mocks.getSession.mockReturnValue(undefined);
    mocks.getUser.mockImplementation((id: number) => Promise.resolve(user(Number(id))));
    mocks.getUserFollowers.mockResolvedValue(page([]));
    mocks.getUserFollowing.mockResolvedValue(page([]));
    mocks.followUser.mockResolvedValue(followResponse(8, true));
    mocks.unfollowUser.mockResolvedValue(followResponse(8, false));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('seeds profile from Profile cache and shares one target profile across modes', async () => {
    mocks.getSession.mockReturnValue({ user: user(7) });
    mocks.getUserFollowing.mockResolvedValueOnce(page([8]));
    mocks.getUserFollowers.mockResolvedValueOnce(page([9]));
    const store = createStore();

    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));
    store.activate(7, 'followers');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.followers.loaded).toBe(true));
    store.activate(7, 'following');

    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(mocks.getUserFollowing).toHaveBeenCalledTimes(1);
    expect(mocks.getUserFollowers).toHaveBeenCalledTimes(1);
    expect(store.getTargetSession(7)?.profile?.bio).toBe('Bio 7');
  });

  it('keeps pending list requests alive when another target is activated', async () => {
    const firstPage = deferred<ReturnType<typeof page>>();
    mocks.getUserFollowing.mockImplementationOnce(() => firstPage.promise);
    const store = createStore();

    store.activate(7, 'following');
    store.activate(8, 'following');
    firstPage.resolve(page([10]));
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.items).toHaveLength(1));

    expect(store.getTargetSession(7)?.following.items[0].user.id).toBe(10);
    expect(store.getTargetSession(8)).toBeDefined();
  });

  it('deduplicates rows while advancing the offset by raw backend page size', async () => {
    const store = createStore();
    mocks.getUserFollowing
      .mockResolvedValueOnce(page([8, 8], true))
      .mockResolvedValueOnce(page([8, 9], false));

    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));
    await store.loadMore(7, 'following');

    const mode = store.getTargetSession(7)!.following;
    expect(mode.items.map(item => item.user.id)).toEqual([8, 9]);
    expect(mode.nextOffset).toBe(4);
    expect(mocks.getUserFollowing).toHaveBeenNthCalledWith(2, 7, { limit: 20, offset: 2 });
  });

  it('optimistically updates every cached target and lets external state win a race', async () => {
    const store = createStore();
    mocks.getUserFollowing.mockResolvedValueOnce(page([8]));
    mocks.getUserFollowers.mockResolvedValueOnce(page([8]));
    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));
    store.activate(9, 'followers');
    await vi.waitFor(() => expect(store.getTargetSession(9)?.followers.loaded).toBe(true));

    const follow = deferred<ReturnType<typeof followResponse>>();
    mocks.followUser.mockImplementationOnce(() => follow.promise);
    const mutation = store.toggleFollow(7, 'following', 8);
    expect(store.getTargetSession(7)!.following.items[0].following).toBe(true);
    expect(store.getTargetSession(9)!.followers.items[0].following).toBe(true);

    store.applyExternalFollowStateLocal(followResponse(8, false));
    follow.resolve(followResponse(8, true));
    await mutation;

    expect(store.getTargetSession(7)!.following.items).toEqual([]);
    expect(store.getTargetSession(9)!.followers.items[0].following).toBe(false);
    expect(store.pendingMutationIDs.has(8)).toBe(false);
  });

  it('removes an external unfollow from the viewer Following list and marks unknown follows stale', async () => {
    const store = createStore();
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(8), following: true }],
      has_more: false,
    });
    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));

    store.applyExternalFollowStateLocal(followResponse(8, false));
    const mode = store.getTargetSession(7)!.following;
    expect(mode.items).toEqual([]);
    expect(mode.nextOffset).toBe(0);
    expect(mode.pageRequestVersion).toBeGreaterThan(1);

    store.applyExternalFollowStateLocal(followResponse(99, true));
    expect(mode.stale).toBe(true);
    expect(mode.freshnessVersion).toBeGreaterThan(0);
  });

  it('rolls back a failed follow across cached rows', async () => {
    const store = createStore();
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    mocks.getUserFollowers.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));
    store.activate(9, 'followers');
    await vi.waitFor(() => expect(store.getTargetSession(9)?.followers.loaded).toBe(true));
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));

    await store.toggleFollow(7, 'following', 8);

    expect(store.getTargetSession(7)!.following.items[0].following).toBe(false);
    expect(store.getTargetSession(9)!.followers.items[0].following).toBe(false);
    expect(store.mutationErrors.get(8)).toContain('Could not update');
  });

  it('reconciles stale offset pages without losing the cache or clearing newer stale state', async () => {
    const store = createStore();
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }, { user: user(9), following: false }],
      has_more: true,
    });
    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));
    const mode = store.getTargetSession(7)!.following;

    store.applyExternalFollowStateLocal(followResponse(99, true));
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(10), following: false }, { user: user(8), following: false }],
      has_more: true,
    });
    await store.revalidateMode(7, 'following');
    expect(mode.items.map(item => item.user.id)).toEqual([10, 8, 9]);
    expect(mode.nextOffset).toBe(2);
    expect(mode.stale).toBe(false);

    store.applyExternalFollowStateLocal(followResponse(100, true));
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(11), following: false }],
      has_more: false,
    });
    await store.revalidateMode(7, 'following');
    expect(mode.items.map(item => item.user.id)).toEqual([11]);
    expect(mode.nextOffset).toBe(1);

    store.applyExternalFollowStateLocal(followResponse(101, true));
    mocks.getUserFollowing.mockRejectedValueOnce(new Error('offline'));
    await store.revalidateMode(7, 'following');
    expect(mode.items.map(item => item.user.id)).toEqual([11]);
    expect(mode.stale).toBe(true);
    expect(mode.revalidating).toBe(false);
    expect(mode.revalidateError).toContain('refreshed');

    store.applyExternalFollowStateLocal(followResponse(102, true));
    const pendingRefresh = deferred<ReturnType<typeof page>>();
    mocks.getUserFollowing.mockImplementationOnce(() => pendingRefresh.promise);
    const refreshRequest = store.revalidateMode(7, 'following');
    store.applyExternalFollowStateLocal(followResponse(103, true));
    pendingRefresh.resolve(page([12], false));
    await refreshRequest;
    expect(mode.stale).toBe(true);
    expect(mode.revalidating).toBe(false);
  });

  it('keeps only eight target sessions and invalidates them on viewer change', async () => {
    const store = createStore();
    mocks.getSession.mockImplementation((id: number) => ({ user: user(Number(id)) }));
    for (let id = 1; id <= 9; id += 1) store.activate(id, 'followers');

    expect(store.sessions.size).toBe(8);
    expect(store.getTargetSession(1)).toBeUndefined();
    expect(store.getTargetSession(9)).toBeDefined();

    store.setViewer(22);
    expect(store.sessions.size).toBe(0);
    expect(store.pendingMutationIDs.size).toBe(0);
  });

  it('replaces identity fields without dropping PublicUser extras', async () => {
    const store = createStore();
    mocks.getUserFollowing.mockResolvedValueOnce({
      items: [{ user: user(8), following: false }],
      has_more: false,
    });
    store.activate(7, 'following');
    await vi.waitFor(() => expect(store.getTargetSession(7)?.following.loaded).toBe(true));

    store.replaceUserIdentityLocal({
      id: 8,
      username: 'renamed',
      display_name: 'Renamed',
      avatar_url: '/new.png',
    });
    const row = store.getTargetSession(7)!.following.items[0].user;
    expect(row.username).toBe('renamed');
    expect(row.avatar_url).toBe('/new.png');
    expect(row.bio).toBe('Bio 8');
  });
});
