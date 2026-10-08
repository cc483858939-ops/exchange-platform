// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTH_MUTATION_LOCK_NAME,
  AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS,
  AUTH_SESSION_STORAGE_KEY,
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  AuthMutationLockTimeoutError,
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

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};

// Model native exclusive ownership and abort removal from the pending queue.
// The held callback completes before another queued callback can run.
const installQueuedLocks = () => {
  type Pending = {
    signal?: AbortSignal;
    run: () => void;
    abort: () => void;
  };
  const queue: Pending[] = [];
  let held = false;
  const drain = () => {
    if (held) return;
    const next = queue.shift();
    if (!next) return;
    held = true;
    next.signal?.removeEventListener('abort', next.abort);
    next.run();
  };
  const request = vi.fn((name: string, options: LockOptions, callback: (lock: Lock) => unknown) => (
    new Promise((resolve, reject) => {
      const entry: Pending = {
        signal: options.signal,
        abort: () => {
          const index = queue.indexOf(entry);
          if (index >= 0) queue.splice(index, 1);
          reject(options.signal?.reason);
        },
        run: () => {
          void Promise.resolve().then(() => callback({ name, mode: options.mode } as Lock))
            .then(resolve, reject).finally(() => { held = false; drain(); });
        },
      };
      if (options.signal?.aborted) { reject(options.signal.reason); return; }
      options.signal?.addEventListener('abort', entry.abort, { once: true });
      queue.push(entry);
      drain();
    })
  ));
  setLocks({ request } as unknown as LockManager);
  return { request, queue };
};

afterEach(() => {
  vi.useRealTimers();
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

  it('writes the credential pair as one snapshot', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem');
    const session = sessionFor();

    writePersistedAuthSession(session);

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(AUTH_SESSION_STORAGE_KEY, JSON.stringify(session));
    expect(readPersistedAuthSession()).toEqual(session);
  });

  it('does not treat cached profile data as a persisted auth session', () => {
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
  it('times out acquisition, removes the queued request and permits a fresh retry', async () => {
    vi.useFakeTimers();
    const locks = installQueuedLocks();
    const release = deferred<string>();
    const started = deferred<void>();
    const holder = runWithAuthMutationLock(() => { started.resolve(); return release.promise; });
    await started.promise;
    const operation = vi.fn(() => 'must not run');
    const pending = runWithAuthMutationLock(operation).catch(error => error);
    expect(locks.queue).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
    expect(await pending).toBeInstanceOf(AuthMutationLockTimeoutError);
    expect(await pending).toMatchObject({ code: 'AUTH_COORDINATION_TIMEOUT' });
    expect(locks.queue).toHaveLength(0);
    expect(locks.request.mock.calls[1][1].signal?.aborted).toBe(true);
    release.resolve('holder done');
    await holder;
    await expect(runWithAuthMutationLock(() => 'retry')).resolves.toBe('retry');
    expect(operation).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('cancels a queued acquisition with the caller reason and cleans its listener', async () => {
    vi.useFakeTimers();
    const locks = installQueuedLocks();
    const release = deferred<void>();
    const started = deferred<void>();
    const holder = runWithAuthMutationLock(() => { started.resolve(); return release.promise; });
    await started.promise;
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, 'removeEventListener');
    const operation = vi.fn();
    const pending = runWithAuthMutationLock(operation, { signal: controller.signal }).catch(error => error);
    const reason = new Error('caller left');
    controller.abort(reason);
    expect(await pending).toBe(reason);
    expect(locks.queue).toHaveLength(0);
    expect(remove).toHaveBeenCalledWith('abort', expect.any(Function));
    release.resolve();
    await holder;
    expect(operation).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not cancel or release a granted operation when its acquisition budget expires', async () => {
    vi.useFakeTimers();
    const locks = installQueuedLocks();
    const release = deferred<void>();
    const started = deferred<void>();
    const controller = new AbortController();
    const holder = runWithAuthMutationLock(() => { started.resolve(); return release.promise; }, { signal: controller.signal });
    await started.promise;
    controller.abort();
    await vi.advanceTimersByTimeAsync(AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS * 2);
    expect(locks.request.mock.calls[0][1].signal?.aborted).toBe(false);
    const next = vi.fn(() => 'next');
    const pending = runWithAuthMutationLock(next);
    expect(next).not.toHaveBeenCalled();
    release.resolve();
    await holder;
    await expect(pending).resolves.toBe('next');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('rejects a grant delivered after the deadline before the timer has run', async () => {
    vi.useFakeTimers();
    const result = deferred<unknown>();
    let grant!: () => void;
    setLocks({ request: vi.fn((name: string, options: LockOptions, callback: (lock: Lock) => unknown) => {
      grant = () => {
        try { result.resolve(callback({ name, mode: options.mode } as Lock)); }
        catch (error) { result.reject(error); }
      };
      return result.promise;
    }) } as unknown as LockManager);
    const operation = vi.fn();
    const pending = runWithAuthMutationLock(operation).catch(error => error);
    vi.setSystemTime(Date.now() + AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS + 1);
    grant();
    expect(await pending).toBeInstanceOf(AuthMutationLockTimeoutError);
    expect(operation).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not enqueue or use an unlocked fallback for an already canceled caller', async () => {
    const request = vi.fn();
    setLocks({ request } as unknown as LockManager);
    const controller = new AbortController();
    controller.abort();
    const operation = vi.fn();
    await expect(runWithAuthMutationLock(operation, { signal: controller.signal })).rejects.toMatchObject({ name: 'AbortError' });
    setLocks(undefined);
    await expect(runWithAuthMutationLock(operation, { signal: controller.signal })).rejects.toMatchObject({ name: 'AbortError' });
    expect(operation).not.toHaveBeenCalled();
    expect(request).not.toHaveBeenCalled();
  });

  it('cleans acquisition resources when request or the operation fails', async () => {
    vi.useFakeTimers();
    const failure = new Error('lock rejected');
    setLocks({ request: vi.fn(() => { throw failure; }) } as unknown as LockManager);
    await expect(runWithAuthMutationLock(vi.fn())).rejects.toBe(failure);
    expect(vi.getTimerCount()).toBe(0);
    installQueuedLocks();
    await expect(runWithAuthMutationLock(() => { throw failure; })).rejects.toBe(failure);
    await expect(runWithAuthMutationLock(() => 'after failure')).resolves.toBe('after failure');
    expect(vi.getTimerCount()).toBe(0);
  });

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
    expect(request.mock.calls[0][1]).toEqual({ mode: 'exclusive', signal: expect.any(AbortSignal) });
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
