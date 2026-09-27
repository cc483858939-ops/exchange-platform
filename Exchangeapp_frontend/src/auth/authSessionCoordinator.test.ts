// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTH_MUTATION_LOCK_NAME,
  AUTH_SESSION_STORAGE_KEY,
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  clearPersistedAuthSession,
  createTokenRevision,
  migrateLegacyAuthSession,
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

  it('migrates a valid legacy pair once and keeps auth_user separate', () => {
    localStorage.setItem('token', tokenFor(7, 'sid-7'));
    localStorage.setItem('refresh_token', 'legacy-refresh');
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify({ id: 7, username: 'alice' }));

    const migrated = migrateLegacyAuthSession();

    expect(migrated).toMatchObject({
      schemaVersion: 2,
      sessionId: 'sid-7',
      userId: 7,
      accessToken: tokenFor(7, 'sid-7'),
      refreshToken: 'legacy-refresh',
    });
    expect(migrated?.tokenRevision).toBeTruthy();
    expect(localStorage.getItem(AUTH_SESSION_STORAGE_KEY)).not.toBeNull();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBe(JSON.stringify({ id: 7, username: 'alice' }));
  });

  it('clears incomplete legacy credentials and incompatible profile state', () => {
    localStorage.setItem('token', tokenFor(7, 'sid-7'));
    localStorage.setItem(AUTH_USER_STORAGE_KEY, JSON.stringify({ id: 7, username: 'alice' }));

    expect(migrateLegacyAuthSession()).toBeNull();
    expect(localStorage.getItem(AUTH_SESSION_STORAGE_KEY)).toBeNull();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(localStorage.getItem(AUTH_USER_STORAGE_KEY)).toBeNull();
  });

  it('does not overwrite an existing v2 snapshot during legacy migration', () => {
    const current = sessionFor();
    localStorage.setItem(AUTH_SESSION_STORAGE_KEY, JSON.stringify(current));
    localStorage.setItem('token', 'stale-legacy-access');
    localStorage.setItem('refresh_token', 'stale-legacy-refresh');

    expect(migrateLegacyAuthSession()).toEqual(current);
    expect(localStorage.getItem('token')).toBe('stale-legacy-access');
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
