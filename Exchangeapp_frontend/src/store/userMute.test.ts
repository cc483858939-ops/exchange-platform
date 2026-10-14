import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
  } | null,
  homeTimeline: {
    setViewer: vi.fn(),
    applyMutedAuthorStateLocal: vi.fn(),
  },
  notifications: {
    setViewer: vi.fn(),
    applyMutedActorStateLocal: vi.fn(),
  },
  getMyMutedUsers: vi.fn(),
  muteUser: vi.fn(),
  unmuteUser: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('./homeTimeline', () => ({ useHomeTimelineStore: () => mocks.homeTimeline }));
vi.mock('./notification', () => ({ useNotificationStore: () => mocks.notifications }));
vi.mock('../services/userMuteService', () => ({
  getMyMutedUsers: mocks.getMyMutedUsers,
  muteUser: mocks.muteUser,
  unmuteUser: mocks.unmuteUser,
}));

import { useUserMuteStore } from './userMute';

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
};

describe('user mute store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.homeTimeline.setViewer.mockReset();
    mocks.homeTimeline.applyMutedAuthorStateLocal.mockReset();
    mocks.notifications.setViewer.mockReset();
    mocks.notifications.applyMutedActorStateLocal.mockReset();
    mocks.getMyMutedUsers.mockReset().mockResolvedValue({ items: [], next_cursor: null });
    mocks.muteUser.mockReset().mockResolvedValue(undefined);
    mocks.unmuteUser.mockReset().mockResolvedValue(undefined);
  });

  it('applies mute state to related surfaces only after the server accepts it', async () => {
    const store = useUserMuteStore();
    expect(store.viewerID).toBe(7);
    expect(await store.muteAuthor(8)).toBe('updated');

    expect(store.isMuted(8)).toBe(true);
    expect(mocks.muteUser).toHaveBeenCalledExactlyOnceWith(8);
    expect(mocks.homeTimeline.setViewer).toHaveBeenCalledExactlyOnceWith(7);
    expect(mocks.homeTimeline.applyMutedAuthorStateLocal).toHaveBeenCalledExactlyOnceWith(8, true);
    expect(mocks.notifications.setViewer).toHaveBeenCalledExactlyOnceWith(7);
    expect(mocks.notifications.applyMutedActorStateLocal).toHaveBeenCalledExactlyOnceWith(8, true);
  });

  it('does not pollute local state when mute persistence fails', async () => {
    mocks.muteUser.mockRejectedValueOnce(new Error('offline'));
    const store = useUserMuteStore();

    expect(await store.muteAuthor(8)).toBe('failed');
    expect(store.isMuted(8)).toBe(false);
    expect(mocks.homeTimeline.applyMutedAuthorStateLocal).not.toHaveBeenCalled();
    expect(mocks.notifications.applyMutedActorStateLocal).not.toHaveBeenCalled();
  });

  it('discards a late mutation and list response after switching accounts', async () => {
    const mutation = deferred<void>();
    const page = deferred<{ items: []; next_cursor: string | null }>();
    mocks.muteUser.mockReturnValueOnce(mutation.promise);
    mocks.getMyMutedUsers.mockReturnValueOnce(page.promise);
    const store = useUserMuteStore();

    const pendingMutation = store.muteAuthor(8);
    const pendingList = store.loadMutedAccounts();
    mocks.authStore!.currentIdentity = { id: 9 };
    await nextTick();

    mutation.resolve(undefined);
    page.resolve({ items: [], next_cursor: null });
    await Promise.all([pendingMutation, pendingList, flushPromises()]);

    expect(store.viewerID).toBe(9);
    expect(store.isMuted(8)).toBe(false);
    expect(store.items).toEqual([]);
    expect(store.pendingAuthorIDs.size).toBe(0);
  });
});
