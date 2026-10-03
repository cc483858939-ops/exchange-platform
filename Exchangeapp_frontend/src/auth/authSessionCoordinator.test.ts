// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTH_MUTATION_LOCK_NAME,
  AUTH_SESSION_STORAGE_KEY,
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  clearPersistedAuthSession,
  createTokenRevision,
  readPersistedAuthSession,
  runWithAuthMutationLock,
  subscribeToPersistedAuthChanges,
  writePersistedAuthSession,
} from './authSessionCoordinator';
import type { PersistedAuthSession } from './authSessionCoordinator';

const tokenFor = (userId: number, sessionId: string): string => {
  const payload = btoa(JSON.stringify({ sub: String(userId), sid: sessionId }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `header.${payload}.signature`;
};

const sessionFor = (
  overrides: Partial<PersistedAuthSession> = {},
): PersistedAuthSession => ({
  schemaVersion: 2,
  tokenRevision: 'revision-1',
  sessionId: 'sid-7',
  userId: 7,
  accessToken: tokenFor(7, 'sid-7'),
  refreshToken: 'refresh-1',
  ...overrides,
});

const originalLocksDescriptor = Object.getOwnPropertyDescriptor(navigator, 'locks');

const setLocks = (locks: LockManager | undefined) => {
  if (locks) {
    Object.defineProperty(navigator, 'locks', { configurable: true, value: locks });
  } else {
    Reflect.deleteProperty(navigator, 'locks');
  }
};

afterEach(() => {
  if (originalLocksDescriptor) {
    Object.defineProperty(navigator, 'locks', originalLocksDescriptor);
  } else {
    Reflect.deleteProperty(navigator, 'locks');
  }
  vi.restoreAllMocks();
});

beforeEach(() => {
  localStorage.clear();
});

describe('auth session persistence', () => {
  it('preserves a valid pending refresh request in the credential snapshot', () => {
    const session = sessionFor({ refreshRequestID: '550e8400-e29b-41d4-a716-446655440000' });
    writePersistedAuthSession(session);
    expect(readPersistedAuthSession()).toEqual(session);
  });

  it('reads an atomically persisted credential pair with matching JWT metadata', () => {
    const session = sessionFor();
    localStorage.setItem(AUTH_SESSION_STORAGE_KEY, JSON.stringify(session));

    expect(readPersistedAuthSession()).toEqual(session);
  });

  it.each([
    ['invalid JSON', '{'],
    ['wrong schema', JSON.stringify({ ...sessionFor(), schemaVersion: 1 })],
    ['missing refresh token', JSON.stringify(sessionFor({ refreshToken: '' }))],
    ['sid mismatch', JSON.stringify(sessionFor({ sessionId: 'sid-other' }))],
    ['sub mismatch', JSON.stringify(sessionFor({ userId: 8 }))],
    ['missing revision', JSON.stringify(sessionFor({ tokenRevision: '' }))],
    ['invalid refresh request', JSON.stringify(sessionFor({ refreshRequestID: 'not-a-request-id' }))],
  ])('rejects persisted state with %s', (_name, raw) => {
    localStorage.setItem(AUTH_SESSION_STORAGE_KEY, raw);

    expect(readPersistedAuthSession()).toBeNull();
  });

  it('writes the credential pair as one snapshot and removes legacy token keys', () => {
    localStorage.setItem('token', 'legacy-access');
    localStorage.setItem('refresh_token', 'legacy-refresh');
    const spy = vi.spyOn(Storage.prototype, 'setItem');
    const session = sessionFor();

    writePersistedAuthSession(session);

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(AUTH_SESSION_STORAGE_KEY, JSON.stringify(session));
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(readPersistedAuthSession()).toEqual(session);
  });

  it('does not treat legacy credentials as a persisted auth session', () => {
    localStorage.setItem('token', tokenFor(7, 'sid-7'));
    localStorage.setItem('refresh_token', 'legacy-refresh');
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify({ id: 7, username: 'alice' }));

    expect(readPersistedAuthSession()).toBeNull();
    expect(localStorage.getItem(AUTH_SESSION_STORAGE_KEY)).toBeNull();
  });

  it('creates opaque secure token revisions', () => {
    const first = createTokenRevision();
    const second = createTokenRevision();

    expect(first).toBeTruthy();
    expect(second).toBeTruthy();
    expect(second).not.toBe(first);
  });

  it('clears only credential persistence', () => {
    localStorage.setItem(AUTH_SESSION_STORAGE_KEY, JSON.stringify(sessionFor()));
    localStorage.setItem(AUTH_USER_STORAGE_KEY, 'profile');

    clearPersistedAuthSession();

    expect(localStorage.getItem(AUTH_SESSION_STORAGE_KEY)).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBe('profile');
  });
});

describe('cross-tab mutation lock', () => {
  it('runs the operation under one exclusive origin-wide Web Lock', async () => {
    const request = vi.fn(async (
      name: string,
      options: LockOptions,
      callback: (lock: Lock | null) => Promise<string>,
    ) => callback({ name, mode: options.mode } as Lock));
    setLocks({ request } as unknown as LockManager);

    await expect(runWithAuthMutationLock(async () => 'done', { requireLock: true })).resolves.toBe('done');

    expect(request).toHaveBeenCalledTimes(1);
    expect(request.mock.calls[0][0]).toBe(AUTH_MUTATION_LOCK_NAME);
    expect(request.mock.calls[0][1]).toEqual({ mode: 'exclusive' });
  });

  it('fails closed for refresh when Web Locks are unavailable', async () => {
    setLocks(undefined);
    const operation = vi.fn(async () => 'unsafe');

    await expect(runWithAuthMutationLock(operation, { requireLock: true }))
      .rejects.toBeInstanceOf(AuthRefreshCoordinationUnavailableError);
    expect(operation).not.toHaveBeenCalled();
  });

  it('supports non-refresh persistence when Web Locks are unavailable', async () => {
    setLocks(undefined);

    await expect(runWithAuthMutationLock(() => 'local commit')).resolves.toBe('local commit');
  });

  it('subscribes only to auth keys and global storage clear', () => {
    const callback = vi.fn();
    const unsubscribe = subscribeToPersistedAuthChanges(callback);
    window.dispatchEvent(new StorageEvent('storage', { key: 'unrelated' }));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_USER_STORAGE_KEY }));
    window.dispatchEvent(new StorageEvent('storage', { key: null }));
    unsubscribe();
    window.dispatchEvent(new StorageEvent('storage', { key: AUTH_SESSION_STORAGE_KEY }));

    expect(callback).toHaveBeenCalledTimes(3);
  });
});
