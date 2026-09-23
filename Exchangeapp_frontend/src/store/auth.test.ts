// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { AuthRequestError } from '../utils/authError';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => ({ post: mocks.post })),
  },
}));

import { AuthSessionChangedError, useAuthStore } from './auth';

const fullIdentity = {
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  avatar_url: '/api/files/profile-avatars/7/test.webp',
};

const otherIdentity = {
  id: 8,
  username: 'bob',
  display_name: 'Bob',
  avatar_url: '/api/files/profile-avatars/8/bob.webp',
};

const authResponse = (
  identity = fullIdentity,
  accessToken = 'access-token',
  refreshToken = 'refresh-token',
) => ({
  access_token: accessToken,
  refresh_token: refreshToken,
  token_type: 'Bearer' as const,
  expires_in: 900,
  refresh_expires_in: 604800,
  user: identity,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;

  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });

  return { promise, resolve, reject };
};

describe('auth store identity persistence', () => {
  beforeEach(() => {
    localStorage.clear();
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('restores legacy storage without logging out', () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 7, username: 'alice' }));

    const store = useAuthStore();

    expect(store.currentIdentity).toEqual({
      id: 7,
      username: 'alice',
      display_name: '',
      avatar_url: '',
    });
    expect(localStorage.getItem('auth_user')).not.toBeNull();
  });

  it('restores a complete stored identity without dropping fields', () => {
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));

    expect(useAuthStore().currentIdentity).toEqual(fullIdentity);
  });

  it('persists the canonical identity returned by login', async () => {
    mocks.post.mockResolvedValueOnce({ data: authResponse() });
    const store = useAuthStore();

    await store.login('alice', 'secret123');

    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(fullIdentity);
  });

  it('lets the latest initiated login attempt win', async () => {
    const firstResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    const secondResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(firstResponse.promise).mockReturnValueOnce(secondResponse.promise);
    const store = useAuthStore();

    const firstLogin = store.login('alice', 'first-password');
    const secondLogin = store.login('bob', 'second-password');
    secondResponse.resolve({ data: authResponse(otherIdentity, 'bob-access', 'bob-refresh') });
    await secondLogin;
    firstResponse.resolve({ data: authResponse(fullIdentity, 'alice-access', 'alice-refresh') });

    await expect(firstLogin).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe('Bearer bob-access');
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(localStorage.getItem('token')).toBe('bob-access');
    expect(localStorage.getItem('refresh_token')).toBe('bob-refresh');
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(otherIdentity);
  });

  it('does not commit a register response after a newer auth transition', async () => {
    const registerResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(registerResponse.promise).mockResolvedValueOnce({
      data: authResponse(otherIdentity, 'bob-access', 'bob-refresh'),
    });
    const store = useAuthStore();

    const registration = store.register('alice', 'first-password');
    await store.login('bob', 'second-password');
    registerResponse.resolve({ data: authResponse(fullIdentity, 'alice-access', 'alice-refresh') });

    await expect(registration).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe('Bearer bob-access');
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
  });

  it('preserves the structured username-unavailable error from register', async () => {
    mocks.post.mockRejectedValueOnce({
      response: {
        data: {
          code: 'AUTH_USERNAME_UNAVAILABLE',
          message: 'Username is unavailable',
        },
      },
    });
    const store = useAuthStore();

    const error = await store.register('alice', 'secret123').catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(AuthRequestError);
    expect((error as AuthRequestError).code).toBe('AUTH_USERNAME_UNAVAILABLE');
    expect((error as AuthRequestError).message).toBe('Username is unavailable');
  });

  it('uses an AuthRequestError with a fallback for register network failures', async () => {
    mocks.post.mockRejectedValueOnce(new Error('Network Error'));
    const store = useAuthStore();

    const error = await store.register('alice', 'secret123').catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(AuthRequestError);
    expect((error as AuthRequestError).code).toBeNull();
    expect((error as AuthRequestError).message).toBe('注册失败，请稍后重试');
  });

  it('replaces identity state and storage with refresh metadata', async () => {
    localStorage.setItem('token', 'old-access');
    localStorage.setItem('refresh_token', 'old-refresh');
    localStorage.setItem('auth_user', JSON.stringify({
      ...fullIdentity,
      display_name: 'Old',
      avatar_url: '/old.webp',
    }));
    const store = useAuthStore();
    const refreshedIdentity = {
      ...fullIdentity,
      display_name: 'New',
      avatar_url: '/new.webp',
    };
    mocks.post.mockResolvedValueOnce({ data: authResponse(refreshedIdentity) });

    await store.refreshAccessToken();

    expect(store.sessionVersion).toBe(0);
    expect(store.currentIdentity).toEqual(refreshedIdentity);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(refreshedIdentity);
  });

  it('does not restore authentication after a refresh completes following logout', async () => {
    localStorage.setItem('token', 'alice-access');
    localStorage.setItem('refresh_token', 'alice-refresh');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise);
    const store = useAuthStore();
    const pendingRefresh = store.refreshAccessToken();

    store.logout();
    refreshResponse.resolve({ data: authResponse(fullIdentity, 'stale-access', 'stale-refresh') });

    await expect(pendingRefresh).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(localStorage.getItem('auth_user')).toBeNull();
  });

  it('does not let a stale refresh success overwrite the next account', async () => {
    localStorage.setItem('token', 'alice-access');
    localStorage.setItem('refresh_token', 'alice-refresh');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise).mockResolvedValueOnce({
      data: authResponse(otherIdentity, 'bob-access', 'bob-refresh'),
    });
    const store = useAuthStore();
    const pendingRefresh = store.refreshAccessToken();

    store.logout();
    await store.login('bob', 'password');
    refreshResponse.resolve({ data: authResponse(fullIdentity, 'stale-access', 'stale-refresh') });

    await expect(pendingRefresh).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe('Bearer bob-access');
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(localStorage.getItem('token')).toBe('bob-access');
    expect(localStorage.getItem('refresh_token')).toBe('bob-refresh');
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(otherIdentity);
  });

  it('does not let a stale refresh failure clear the next account', async () => {
    localStorage.setItem('token', 'alice-access');
    localStorage.setItem('refresh_token', 'alice-refresh');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise).mockResolvedValueOnce({
      data: authResponse(otherIdentity, 'bob-access', 'bob-refresh'),
    });
    const store = useAuthStore();
    const pendingRefresh = store.refreshAccessToken();

    store.logout();
    await store.login('bob', 'password');
    refreshResponse.reject(new Error('stale refresh failed'));

    await expect(pendingRefresh).rejects.toThrow('stale refresh failed');
    expect(store.token).toBe('Bearer bob-access');
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(localStorage.getItem('token')).toBe('bob-access');
    expect(localStorage.getItem('refresh_token')).toBe('bob-refresh');
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(otherIdentity);
  });

  it('synchronizes an own-profile update without touching tokens', () => {
    localStorage.setItem('token', 'access-token');
    localStorage.setItem('refresh_token', 'refresh-token');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();
    const updated = {
      ...fullIdentity,
      display_name: 'Alice Updated',
      avatar_url: '/new.webp',
    };

    expect(store.syncCurrentIdentityProfile(updated)).toBe(true);
    expect(store.currentIdentity).toEqual(updated);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(updated);
    expect(store.token).toBe('Bearer access-token');
    expect(store.refreshToken).toBe('refresh-token');
    expect(localStorage.getItem('token')).toBe('access-token');
    expect(localStorage.getItem('refresh_token')).toBe('refresh-token');
  });

  it('does not let another user replace current identity', () => {
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();
    const storedBefore = localStorage.getItem('auth_user');

    expect(store.syncCurrentIdentityProfile({ ...fullIdentity, id: 8, username: 'bob' })).toBe(false);
    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(localStorage.getItem('auth_user')).toBe(storedBefore);
  });

  it('clears tokens and identity on logout', () => {
    localStorage.setItem('token', 'access-token');
    localStorage.setItem('refresh_token', 'refresh-token');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();
    const versionBeforeLogout = store.sessionVersion;

    store.logout();

    expect(store.sessionVersion).toBe(versionBeforeLogout + 1);
    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(localStorage.getItem('auth_user')).toBeNull();
  });

  it('increments the session generation exactly once when clearAuth is called', () => {
    const store = useAuthStore();
    const versionBeforeClear = store.sessionVersion;

    store.clearAuth();

    expect(store.sessionVersion).toBe(versionBeforeClear + 1);
  });
});
