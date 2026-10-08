// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { Pinia } from 'pinia';
import { apiBaseUrl } from '../api';
import {
  AUTH_SESSION_STORAGE_KEY,
  AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS,
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  AuthMutationLockTimeoutError,
  runWithAuthMutationLock,
  writePersistedAuthSession,
} from '../auth/authSessionCoordinator';
import type { PersistedAuthSession } from '../auth/authSessionCoordinator';
import { AuthRequestError } from '../utils/authError';
import { AUTH_REQUEST_TIMEOUT_MS } from '../utils/requestTimeout';

const mocks = vi.hoisted(() => {
  const client = { post: vi.fn() };
  return {
    client,
    post: client.post,
    create: vi.fn(() => client),
  };
});

vi.mock('axios', () => ({
  default: {
    create: mocks.create,
    isAxiosError: (error: unknown) => Boolean(
      error
      && typeof error === 'object'
      && 'isAxiosError' in error
      && error.isAxiosError === true,
    ),
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

const tokenFor = (label: string, userId = 7, sessionId = `sid-${userId}`) => {
  const payload = btoa(JSON.stringify({ sub: String(userId), sid: sessionId, jti: label }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `header.${payload}.signature`;
};

const bearerFor = (label: string, identity = fullIdentity, sessionId = `sid-${identity.id}`) => (
  `Bearer ${tokenFor(label, identity.id, sessionId)}`
);

const authResponse = (
  identity = fullIdentity,
  accessLabel = 'access-token',
  refreshToken = 'refresh-token',
  sessionId = `sid-${identity.id}`,
  tokenUserId = identity.id,
) => ({
  access_token: tokenFor(accessLabel, tokenUserId, sessionId),
  refresh_token: refreshToken,
  token_type: 'Bearer' as const,
  expires_in: 900,
  refresh_expires_in: 604800,
  user: identity,
});

const persistedSession = (
  identity = fullIdentity,
  accessLabel = 'alice-access',
  refreshToken = 'alice-refresh',
  sessionId = `sid-${identity.id}`,
  tokenRevision = 'revision-1',
): PersistedAuthSession => ({
  schemaVersion: 2,
  tokenRevision,
  sessionId,
  userId: identity.id,
  accessToken: tokenFor(accessLabel, identity.id, sessionId),
  refreshToken,
});

const seedV2Auth = (
  identity = fullIdentity,
  accessLabel = 'alice-access',
  refreshToken = 'alice-refresh',
  sessionId = `sid-${identity.id}`,
  tokenRevision = 'revision-1',
) => {
  writePersistedAuthSession(persistedSession(identity, accessLabel, refreshToken, sessionId, tokenRevision));
  localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(identity));
};

const readStoredSession = (): PersistedAuthSession | null => {
  const raw = localStorage.getItem(AUTH_SESSION_STORAGE_KEY);
  return raw ? JSON.parse(raw) as PersistedAuthSession : null;
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

const originalLocksDescriptor = Object.getOwnPropertyDescriptor(navigator, 'locks');
let stores: Array<ReturnType<typeof useAuthStore>> = [];
let lockRequest: ReturnType<typeof vi.fn>;

const setLocks = (locks: LockManager | undefined) => {
  if (locks) {
    Object.defineProperty(navigator, 'locks', { configurable: true, value: locks });
  } else {
    Reflect.deleteProperty(navigator, 'locks');
  }
};

const installSerializedLock = () => {
  let tail = Promise.resolve();
  const request = vi.fn(async (
    name: string,
    options: LockOptions,
    callback: (lock: Lock | null) => unknown,
  ) => {
    const previous = tail;
    let release!: () => void;
    tail = new Promise<void>(resolve => { release = resolve; });
    return new Promise((resolve, reject) => {
      const abort = () => reject(options.signal?.reason);
      options.signal?.addEventListener('abort', abort, { once: true });
      void previous.then(async () => {
        options.signal?.removeEventListener('abort', abort);
        try {
          if (options.signal?.aborted) { reject(options.signal.reason); return; }
          resolve(await callback({ name, mode: options.mode } as Lock));
        } catch (error) { reject(error); } finally { release(); }
      });
    });
  });
  setLocks({ request } as unknown as LockManager);
  return request;
};

const createAuthStore = (pinia?: Pinia) => {
  const store = useAuthStore(pinia);
  stores.push(store);
  return store;
};

const holdAuthMutationLock = async () => {
  const release = deferred<void>();
  const started = deferred<void>();
  const promise = runWithAuthMutationLock(() => { started.resolve(); return release.promise; });
  await started.promise;
  return { release: () => release.resolve(), promise };
};

beforeEach(() => {
  localStorage.clear();
  stores = [];
  setActivePinia(createPinia());
  mocks.post.mockReset();
  mocks.post.mockResolvedValue({ status: 204 });
  lockRequest = installSerializedLock();
});

afterEach(() => {
  vi.useRealTimers();
  stores.forEach(store => store.$dispose());
  if (originalLocksDescriptor) {
    Object.defineProperty(navigator, 'locks', originalLocksDescriptor);
  } else {
    Reflect.deleteProperty(navigator, 'locks');
  }
});

describe('auth store cross-tab session coordination', () => {
  it('bounds refresh queue time without HTTP or credential mutation, then allows retry', async () => {
    vi.useFakeTimers();
    seedV2Auth();
    const store = createAuthStore();
    const saved = readStoredSession();
    const holder = await holdAuthMutationLock();
    try {
      const pending = store.refreshAccessToken().catch(error => error);
      await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
      expect(await pending).toBeInstanceOf(AuthMutationLockTimeoutError);
      expect(mocks.post).not.toHaveBeenCalled();
      expect(readStoredSession()).toEqual(saved);
      expect(store.isAuthenticated).toBe(true);
    } finally { holder.release(); await holder.promise; }
    mocks.post.mockResolvedValueOnce({ data: authResponse(fullIdentity, 'retry-access', 'retry-refresh') });
    await expect(store.refreshAccessToken()).resolves.toBe(bearerFor('retry-access'));
    expect(readStoredSession()?.refreshToken).toBe('retry-refresh');
  });

  it('supports caller cancellation while refresh is queued', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const controller = new AbortController();
      const pending = store.refreshAccessToken({ signal: controller.signal }).catch(error => error);
      controller.abort();
      expect(await pending).toMatchObject({ name: 'AbortError' });
      expect(mocks.post).not.toHaveBeenCalled();
      expect(store.isAuthenticated).toBe(true);
      expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
    } finally { holder.release(); await holder.promise; }
  });

  it('cancels a queued refresh immediately on logout before the holder releases', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    const pendingRefresh = store.refreshAccessToken().catch(error => error);
    const logout = store.logout();
    try {
      expect(await pendingRefresh).toBeInstanceOf(AuthSessionChangedError);
      expect(mocks.post).not.toHaveBeenCalled();
      expect(store.isAuthenticated).toBe(false);
      window.dispatchEvent(new Event('pageshow'));
      expect(store.isAuthenticated).toBe(false);
    } finally { holder.release(); await holder.promise; await logout; }
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith('/auth/logout', { refresh_token: 'alice-refresh' });
    expect(readStoredSession()).toBeNull();
  });

  it('cancels queued refresh when another tab switches the account', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const pending = store.refreshAccessToken().catch(error => error);
      writePersistedAuthSession(persistedSession(otherIdentity, 'bob-access', 'bob-refresh'));
      localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
      window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));
      expect(await pending).toBeInstanceOf(AuthSessionChangedError);
      expect(mocks.post).not.toHaveBeenCalled();
      expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    } finally { holder.release(); await holder.promise; }
    expect(readStoredSession()?.refreshToken).toBe('bob-refresh');
  });

  it('retains the logout guard after cleanup timeout, still revokes and can retry cleanup', async () => {
    vi.useFakeTimers();
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const pending = store.logout().catch(error => error);
      expect(store.isAuthenticated).toBe(false);
      await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
      expect(await pending).toMatchObject({ code: 'AUTH_LOGOUT_CLEANUP_PENDING' });
      expect(mocks.post).toHaveBeenCalledWith('/auth/logout', { refresh_token: 'alice-refresh' });
      expect(readStoredSession()?.sessionId).toBe('sid-7');
      window.dispatchEvent(new Event('pageshow'));
      window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));
      store.reconcilePersistedAuthState();
      expect(store.isAuthenticated).toBe(false);
    } finally { holder.release(); await holder.promise; }
    await store.logout();
    expect(readStoredSession()).toBeNull();
    expect(store.isAuthenticated).toBe(false);
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post.mock.calls[1]).toEqual(['/auth/logout', { refresh_token: 'alice-refresh' }]);
  });

  it('keeps newer credentials when retrying a timed-out logout cleanup', async () => {
    vi.useFakeTimers();
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const pending = store.clearAuth().catch(error => error);
      await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
      expect(await pending).toBeInstanceOf(AuthMutationLockTimeoutError);
    } finally { holder.release(); await holder.promise; }
    writePersistedAuthSession(persistedSession(otherIdentity, 'bob-access', 'bob-refresh'));
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
    await store.logout();
    expect(readStoredSession()?.refreshToken).toBe('bob-refresh');
    expect(mocks.post).not.toHaveBeenCalled();
    store.reconcilePersistedAuthState();
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
  });

  it('retains logout protection after canceled or failed storage cleanup', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const controller = new AbortController();
      const pending = store.clearAuth({ signal: controller.signal }).catch(error => error);
      controller.abort();
      expect(await pending).toMatchObject({ name: 'AbortError' });
      store.reconcilePersistedAuthState();
      expect(store.isAuthenticated).toBe(false);
    } finally { holder.release(); await holder.promise; }
    const remove = vi.spyOn(Storage.prototype, 'removeItem').mockImplementationOnce(() => { throw new Error('Storage unavailable'); });
    await expect(store.clearAuth()).rejects.toThrow('Storage unavailable');
    store.reconcilePersistedAuthState();
    expect(store.isAuthenticated).toBe(false);
    remove.mockRestore();
    await store.clearAuth();
    expect(readStoredSession()).toBeNull();
  });

  it.each(['login', 'register'] as const)('bounds a queued %s commit and preserves the coordination error code', async method => {
    vi.useFakeTimers();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      mocks.post.mockResolvedValueOnce({ data: authResponse() });
      const pending = store[method]('alice', 'secret123').catch(error => error);
      await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
      expect(await pending).toMatchObject({ code: 'AUTH_COORDINATION_TIMEOUT' });
      expect(store.isAuthenticated).toBe(false);
      expect(readStoredSession()).toBeNull();
    } finally { holder.release(); await holder.promise; }
    expect(readStoredSession()).toBeNull();
  });

  it('cancels a queued refresh on store disposal without sending HTTP', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const holder = await holdAuthMutationLock();
    try {
      const pending = store.refreshAccessToken().catch(error => error);
      store.$dispose();
      expect(await pending).toBeInstanceOf(AuthSessionChangedError);
      expect(mocks.post).not.toHaveBeenCalled();
      expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
    } finally { holder.release(); await holder.promise; }
  });

  it('configures the auth client with its bounded timeout', () => {
    expect(mocks.create).toHaveBeenCalledWith({
      baseURL: apiBaseUrl,
      timeout: AUTH_REQUEST_TIMEOUT_MS,
    });
  });

  it('requires login and clears cached profile data when there is no credential snapshot', () => {
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(fullIdentity));
    const store = createAuthStore();

    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(store.isAuthenticated).toBe(false);
    expect(readStoredSession()).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('does not restore profile data for another user than the credential snapshot', () => {
    seedV2Auth();
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));

    const store = createAuthStore();

    expect(store.currentIdentity).toEqual({
      id: 7, username: '', display_name: '', avatar_url: '',
    });
  });

  it('restores the current credential snapshot', () => {
    seedV2Auth();

    const store = createAuthStore();

    expect(store.token).toBe(bearerFor('alice-access'));
    expect(store.refreshToken).toBe('alice-refresh');
  });

  it('rejects a malformed credential snapshot and clears cached profile data', () => {
    localStorage.setItem(AUTH_SESSION_STORAGE_KEY, '{');
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(fullIdentity));

    const store = createAuthStore();

    expect(store.isAuthenticated).toBe(false);
    expect(store.token).toBeNull();
    expect(readStoredSession()).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('commits a validated login as one credential snapshot', async () => {
    mocks.post.mockResolvedValueOnce({ data: authResponse() });
    const store = createAuthStore();

    await store.login('alice', 'secret123');

    expect(store.token).toBe(bearerFor('access-token'));
    expect(store.refreshToken).toBe('refresh-token');
    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(readStoredSession()).toMatchObject({
      sessionId: 'sid-7', userId: 7, accessToken: tokenFor('access-token'), refreshToken: 'refresh-token',
    });
    expect(JSON.parse(localStorage.getItem(AUTH_USER_STORAGE_KEY) || 'null')).toEqual(fullIdentity);
    expect(lockRequest).toHaveBeenCalledWith(expect.any(String), { mode: 'exclusive', signal: expect.any(AbortSignal) }, expect.any(Function));
  });

  it('rejects a login response whose JWT subject does not match its profile', async () => {
    mocks.post.mockResolvedValueOnce({ data: authResponse(fullIdentity, 'bad-sub', 'bad-refresh', 'sid-7', 8) });
    const store = createAuthStore();

    await expect(store.login('alice', 'secret123')).rejects.toBeInstanceOf(AuthRequestError);
    expect(readStoredSession()).toBeNull();
    expect(store.isAuthenticated).toBe(false);
  });

  it('lets the latest initiated login attempt win', async () => {
    const firstResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    const secondResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(firstResponse.promise).mockReturnValueOnce(secondResponse.promise);
    const store = createAuthStore();

    const firstLogin = store.login('alice', 'first-password');
    const secondLogin = store.login('bob', 'second-password');
    secondResponse.resolve({ data: authResponse(otherIdentity, 'bob-access', 'bob-refresh') });
    await secondLogin;
    firstResponse.resolve({ data: authResponse(fullIdentity, 'alice-access', 'alice-refresh') });

    await expect(firstLogin).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(readStoredSession()).toMatchObject({
      sessionId: 'sid-8', userId: 8, accessToken: tokenFor('bob-access', 8), refreshToken: 'bob-refresh',
    });
  });

  it('does not commit a register response after a newer auth transition', async () => {
    const registerResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(registerResponse.promise).mockResolvedValueOnce({
      data: authResponse(otherIdentity, 'bob-access', 'bob-refresh'),
    });
    const store = createAuthStore();

    const registration = store.register('alice', 'first-password');
    await store.login('bob', 'second-password');
    registerResponse.resolve({ data: authResponse(fullIdentity, 'alice-access', 'alice-refresh') });

    await expect(registration).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
  });

  it('preserves structured register errors and maps network and timeout failures', async () => {
    const store = createAuthStore();
    mocks.post.mockRejectedValueOnce({ response: { data: {
      code: 'AUTH_USERNAME_UNAVAILABLE', message: 'Username is unavailable',
    } } });
    const structured = await store.register('alice', 'secret123').catch((error: unknown) => error);
    expect(structured).toBeInstanceOf(AuthRequestError);
    expect(structured).toMatchObject({ code: 'AUTH_USERNAME_UNAVAILABLE', message: 'Username is unavailable' });

    mocks.post.mockRejectedValueOnce(new Error('Network Error'));
    const network = await store.register('alice', 'secret123').catch((error: unknown) => error);
    expect(network).toBeInstanceOf(AuthRequestError);
    expect(network).toMatchObject({ code: null, message: '注册失败，请稍后重试' });

    mocks.post.mockRejectedValueOnce(Object.assign(new Error('timeout'), {
      isAxiosError: true, code: 'ECONNABORTED',
    }));
    const timeout = await store.login('alice', 'secret123').catch((error: unknown) => error);
    expect(timeout).toBeInstanceOf(AuthRequestError);
    expect(timeout).toMatchObject({
      code: 'AUTH_REQUEST_TIMEOUT',
      message: 'Request timed out. Check your connection and try again.',
    });
  });

  it('rotates credentials without advancing the authentication session version', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const binding = store.captureRequestAuthBinding();
    expect(binding).toEqual({ userID: 7, sessionID: 'sid-7', sessionVersion: 0 });
    expect(Object.isFrozen(binding)).toBe(true);
    const revisionBefore = readStoredSession()?.tokenRevision;
    const refreshedIdentity = { ...fullIdentity, display_name: 'New', avatar_url: '/new.webp' };
    mocks.post.mockResolvedValueOnce({
      data: authResponse(refreshedIdentity, 'alice-access-v2', 'alice-refresh-v2'),
    });

    await expect(store.refreshAccessToken()).resolves.toBe(bearerFor('alice-access-v2'));

    expect(store.sessionVersion).toBe(0);
    expect(store.token).toBe(bearerFor('alice-access-v2'));
    expect(store.matchesRequestAuthBinding(binding!)).toBe(true);
    expect(store.refreshToken).toBe('alice-refresh-v2');
    expect(store.currentIdentity).toEqual(refreshedIdentity);
    expect(readStoredSession()).toMatchObject({
      sessionId: 'sid-7', userId: 7, accessToken: tokenFor('alice-access-v2'), refreshToken: 'alice-refresh-v2',
    });
    expect(readStoredSession()?.tokenRevision).not.toBe(revisionBefore);
  });

  it('preserves credentials after timeout, network failure, and server failure', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const timeout = Object.assign(new Error('Request timed out'), {
      isAxiosError: true, code: 'ECONNABORTED',
    });
    const network = new Error('Network Error');
    const server = { isAxiosError: true, response: { status: 503, data: { code: 'AUTH_INTERNAL' } } };

    for (const error of [timeout, network, server]) {
      mocks.post.mockRejectedValueOnce(error);
      await expect(store.refreshAccessToken()).rejects.toBe(error);
      expect(store.token).toBe(bearerFor('alice-access'));
      expect(store.refreshToken).toBe('alice-refresh');
      expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
    }
  });

  it('persists the same refresh request across failures and store recreation', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const timeout = Object.assign(new Error('Response lost'), { isAxiosError: true, code: 'ECONNABORTED' });
    mocks.post.mockImplementationOnce(async (_url, body) => {
      expect(readStoredSession()?.refreshRequestID).toBe(body.request_id);
      throw timeout;
    });
    await expect(store.refreshAccessToken()).rejects.toBe(timeout);
    const pending = readStoredSession();
    expect(pending?.refreshRequestID).toMatch(/^[0-9a-f-]{36}$/);
    const recreated = createAuthStore();
    mocks.post.mockResolvedValueOnce({ data: authResponse(fullIdentity, 'recovered-access', 'recovered-refresh') });
    await expect(recreated.refreshAccessToken()).resolves.toBe(bearerFor('recovered-access'));
    expect(mocks.post.mock.calls[1]?.[1]).toEqual(mocks.post.mock.calls[0]?.[1]);
    expect(readStoredSession()?.refreshToken).toBe('recovered-refresh');
    expect(readStoredSession()?.refreshRequestID).toBeUndefined();
  });

  it('does not rotate when the durable request ID cannot be saved', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const saved = readStoredSession();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => { throw new Error('Quota exceeded'); });
    await expect(store.refreshAccessToken()).rejects.toThrow('Quota exceeded');
    expect(mocks.post).not.toHaveBeenCalled();
    expect(readStoredSession()).toEqual(saved);
    expect(store.isAuthenticated).toBe(true);
  });

  it('recovers with the durable request ID after saving the successor fails', async () => {
    seedV2Auth();
    const store = createAuthStore();
    mocks.post.mockImplementationOnce(async () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => { throw new Error('Storage unavailable'); });
      return { data: authResponse(fullIdentity, 'recovered-access', 'recovered-refresh') };
    });
    await expect(store.refreshAccessToken()).rejects.toThrow('Storage unavailable');
    const firstRequest = mocks.post.mock.calls[0]?.[1];
    expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
    expect(readStoredSession()?.refreshRequestID).toBe(firstRequest.request_id);
    mocks.post.mockResolvedValueOnce({ data: authResponse(fullIdentity, 'recovered-access', 'recovered-refresh') });
    await expect(store.refreshAccessToken()).resolves.toBe(bearerFor('recovered-access'));
    expect(mocks.post.mock.calls[1]?.[1]).toEqual(firstRequest);
    expect(readStoredSession()?.refreshRequestID).toBeUndefined();
  });

  it.each([
    'AUTH_REFRESH_INVALID',
    'AUTH_REFRESH_EXPIRED',
    'AUTH_REFRESH_REUSED',
  ])('clears the same session after fatal refresh rejection %s', async code => {
    seedV2Auth();
    const store = createAuthStore();
    mocks.post.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 401, data: { code } },
    });

    await expect(store.refreshAccessToken()).rejects.toMatchObject({ response: { status: 401, data: { code } } });

    expect(store.sessionVersion).toBe(1);
    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(readStoredSession()).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('rejects refresh without Web Locks and preserves authentication', async () => {
    seedV2Auth();
    const store = createAuthStore();
    setLocks(undefined);

    await expect(store.refreshAccessToken()).rejects.toBeInstanceOf(AuthRefreshCoordinationUnavailableError);

    expect(mocks.post).not.toHaveBeenCalled();
    expect(store.isAuthenticated).toBe(true);
    expect(store.refreshToken).toBe('alice-refresh');
    expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
  });

  it('does not commit a refresh response with a different sid or sub', async () => {
    seedV2Auth();
    const store = createAuthStore();
    mocks.post.mockResolvedValueOnce({ data: authResponse(fullIdentity, 'bad-session', 'bad-refresh', 'sid-8') });

    await expect(store.refreshAccessToken()).rejects.toThrow('Invalid authentication response');

    expect(readStoredSession()?.sessionId).toBe('sid-7');
    expect(readStoredSession()?.refreshToken).toBe('alice-refresh');
    expect(store.sessionVersion).toBe(0);
  });

  it('clears auth synchronously on logout and conditionally removes persisted credentials', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const versionBefore = store.sessionVersion;

    const logout = store.logout();
    expect(store.sessionVersion).toBe(versionBefore + 1);
    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    await logout;

    expect(readStoredSession()).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('revokes only the captured refresh credential after clearing local state', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const response = deferred<{ status: number }>();
    mocks.post.mockReturnValueOnce(response.promise);
    const logout = store.logout();
    expect(store.isAuthenticated).toBe(false);
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledWith('/auth/logout', {
      refresh_token: 'alice-refresh',
    }));
    expect(readStoredSession()).toBeNull();
    writePersistedAuthSession(persistedSession(otherIdentity, 'bob-access', 'bob-refresh', 'sid-8'));
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));
    response.resolve({ status: 204 });
    await logout;
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    expect(readStoredSession()?.refreshToken).toBe('bob-refresh');
    expect(mocks.post).toHaveBeenCalledTimes(1);
  });

  it.each(['network', 'timeout', 'server', 'unexpected response'])(
    'reports unconfirmed server logout on %s while keeping local credentials cleared', async (failure) => {
      seedV2Auth();
      const store = createAuthStore();
      if (failure === 'unexpected response') mocks.post.mockResolvedValueOnce({ status: 200 });
      else mocks.post.mockRejectedValueOnce(failure === 'timeout'
        ? { code: 'ECONNABORTED' }
        : { response: failure === 'server' ? { status: 503 } : undefined });
      await expect(store.logout()).rejects.toMatchObject({ code: 'AUTH_LOGOUT_UNCONFIRMED' });
      expect(store.isAuthenticated).toBe(false);
      expect(readStoredSession()).toBeNull();
      expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
    },
  );

  it('performs local-only invalidation and does not send empty logout credentials', async () => {
    seedV2Auth();
    const store = createAuthStore();
    await store.clearAuth();
    await store.logout();
    expect(mocks.post).not.toHaveBeenCalled();
    expect(readStoredSession()).toBeNull();
  });

  it('preserves a new login when an older queued logout runs', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const logout = store.logout();
    mocks.post.mockResolvedValueOnce({ data: authResponse(otherIdentity, 'bob-access', 'bob-refresh') });

    await store.login('bob', 'password');
    await logout;

    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(readStoredSession()?.sessionId).toBe('sid-8');
  });

  it('does not clear a different persisted session when logout was captured from an older sid', async () => {
    seedV2Auth();
    const store = createAuthStore();
    writePersistedAuthSession(persistedSession(otherIdentity, 'bob-access', 'bob-refresh', 'sid-8', 'revision-bob'));
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));

    await store.logout();

    expect(readStoredSession()?.sessionId).toBe('sid-8');
    expect(JSON.parse(localStorage.getItem(AUTH_USER_STORAGE_KEY) || 'null')).toEqual(otherIdentity);
    expect(store.token).toBeNull();
  });

  it('does not restore a session when refresh finishes after local logout', async () => {
    seedV2Auth();
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise);
    const store = createAuthStore();
    const pendingRefresh = store.refreshAccessToken();
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));

    const logout = store.logout();
    refreshResponse.resolve({ data: authResponse(fullIdentity, 'stale-access', 'stale-refresh') });
    await expect(pendingRefresh).rejects.toBeInstanceOf(AuthSessionChangedError);
    await logout;

    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(readStoredSession()).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('does not let a stale refresh overwrite a new login session', async () => {
    seedV2Auth();
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise).mockResolvedValueOnce({
      data: authResponse(otherIdentity, 'bob-access', 'bob-refresh'),
    });
    const store = createAuthStore();
    const pendingRefresh = store.refreshAccessToken();
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));

    const logout = store.logout();
    const login = store.login('bob', 'password');
    refreshResponse.resolve({ data: authResponse(fullIdentity, 'stale-access', 'stale-refresh') });
    await expect(pendingRefresh).rejects.toBeInstanceOf(AuthSessionChangedError);
    await Promise.all([logout, login]);

    expect(store.token).toBe(bearerFor('bob-access', otherIdentity));
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(readStoredSession()?.sessionId).toBe('sid-8');
  });

  it('does not let a stale fatal refresh error clear another session', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise);
    const pendingRefresh = store.refreshAccessToken();
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));

    const nextSession = persistedSession(otherIdentity, 'bob-access', 'bob-refresh', 'sid-8', 'revision-bob');
    writePersistedAuthSession(nextSession);
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));
    refreshResponse.reject({
      isAxiosError: true,
      response: { status: 401, data: { code: 'AUTH_REFRESH_REUSED' } },
    });

    await expect(pendingRefresh).rejects.toBeInstanceOf(AuthSessionChangedError);
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity, 'sid-8'));
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(readStoredSession()?.sessionId).toBe('sid-8');
  });

  it('clears a same-sid reused session even if its local generation advanced', async () => {
    seedV2Auth();
    const store = createAuthStore();
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise);
    const pendingRefresh = store.refreshAccessToken();
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));

    writePersistedAuthSession(persistedSession(fullIdentity, 'peer-access', 'peer-refresh', 'sid-7', 'revision-peer'));
    refreshResponse.reject({
      isAxiosError: true,
      response: { status: 401, data: { code: 'AUTH_REFRESH_REUSED' } },
    });

    await expect(pendingRefresh).rejects.toMatchObject({ response: { status: 401 } });
    expect(store.token).toBeNull();
    expect(readStoredSession()).toBeNull();
  });

  it('adopts a peer refresh from the persisted snapshot without a second HTTP call', async () => {
    seedV2Auth();
    const firstPinia = createPinia();
    const secondPinia = createPinia();
    const thirdPinia = createPinia();
    const first = createAuthStore(firstPinia);
    const second = createAuthStore(secondPinia);
    const third = createAuthStore(thirdPinia);
    const refreshResponse = deferred<{ data: ReturnType<typeof authResponse> }>();
    mocks.post.mockReturnValueOnce(refreshResponse.promise);

    const firstRefresh = first.refreshAccessToken();
    const secondRefresh = second.refreshAccessToken();
    const thirdRefresh = third.refreshAccessToken();
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));
    refreshResponse.resolve({ data: authResponse(fullIdentity, 'rotated-access', 'rotated-refresh') });

    const rotatedBearer = bearerFor('rotated-access');
    await expect(Promise.all([firstRefresh, secondRefresh, thirdRefresh])).resolves.toEqual([
      rotatedBearer, rotatedBearer, rotatedBearer,
    ]);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    for (const store of [first, second, third]) {
      expect(store.token).toBe(rotatedBearer);
      expect(store.refreshToken).toBe('rotated-refresh');
      expect(store.sessionVersion).toBe(0);
    }
    expect(readStoredSession()?.sessionId).toBe('sid-7');
  });

  it('synchronizes same-sid rotation without advancing sessionVersion', () => {
    seedV2Auth();
    const store = createAuthStore();
    const revisionBefore = readStoredSession()?.tokenRevision;
    const next = persistedSession(fullIdentity, 'peer-access', 'peer-refresh', 'sid-7', 'revision-peer');
    writePersistedAuthSession(next);
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY, newValue: 'untrusted' }));

    expect(store.token).toBe(bearerFor('peer-access'));
    expect(store.refreshToken).toBe('peer-refresh');
    expect(store.sessionVersion).toBe(0);
    expect(store.token).not.toBeNull();
    expect(readStoredSession()?.tokenRevision).not.toBe(revisionBefore);
  });

  it('increments sessionVersion and adopts a remote account switch', () => {
    seedV2Auth();
    const store = createAuthStore();
    const next = persistedSession(otherIdentity, 'bob-access', 'bob-refresh', 'sid-8', 'revision-bob');
    writePersistedAuthSession(next);
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));

    expect(store.sessionVersion).toBe(1);
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity, 'sid-8'));
    expect(store.refreshToken).toBe('bob-refresh');
    expect(store.currentIdentity).toEqual(otherIdentity);
  });

  it('propagates remote logout and invalidates old requests', () => {
    seedV2Auth();
    const store = createAuthStore();
    localStorage.removeItem(AUTH_SESSION_STORAGE_KEY);
    localStorage.removeItem(AUTH_USER_STORAGE_KEY);
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));

    expect(store.sessionVersion).toBe(1);
    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
  });

  it('synchronizes profile metadata without changing token credentials or session version', () => {
    seedV2Auth();
    const store = createAuthStore();
    const before = readStoredSession();
    const updated = { ...fullIdentity, display_name: 'Alice Updated', avatar_url: '/new.webp' };
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(updated));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_USER_STORAGE_KEY }));

    expect(store.currentIdentity).toEqual(updated);
    expect(store.sessionVersion).toBe(0);
    expect(readStoredSession()).toEqual(before);
  });

  it('reconciles suspended tabs when pageshow fires', () => {
    seedV2Auth();
    const store = createAuthStore();
    const next = persistedSession(otherIdentity, 'bob-access', 'bob-refresh', 'sid-8', 'revision-bob');
    writePersistedAuthSession(next);
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify(otherIdentity));
    window.dispatchEvent(new Event('pageshow'));

    expect(store.sessionVersion).toBe(1);
    expect(store.currentIdentity).toEqual(otherIdentity);
    expect(store.token).toBe(bearerFor('bob-access', otherIdentity, 'sid-8'));
  });

  it('keeps own-profile updates separate from token rotation', () => {
    seedV2Auth();
    const store = createAuthStore();
    const before = readStoredSession();
    const updated = { ...fullIdentity, display_name: 'Alice Updated', avatar_url: '/new.webp' };

    expect(store.syncCurrentIdentityProfile(updated)).toBe(true);
    expect(store.currentIdentity).toEqual(updated);
    expect(JSON.parse(localStorage.getItem(AUTH_USER_STORAGE_KEY) || 'null')).toEqual(updated);
    expect(store.token).toBe(bearerFor('alice-access'));
    expect(store.refreshToken).toBe('alice-refresh');
    expect(readStoredSession()).toEqual(before);
  });

  it('does not let another user replace current profile metadata', () => {
    seedV2Auth();
    const store = createAuthStore();
    const storedBefore = localStorage.getItem(AUTH_USER_STORAGE_KEY);

    expect(store.syncCurrentIdentityProfile({ ...fullIdentity, id: 8, username: 'bob' })).toBe(false);
    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBe(storedBefore);
  });
});
