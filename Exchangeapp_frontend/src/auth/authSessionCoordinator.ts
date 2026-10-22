import { decodeAuthTokenMetadata } from '../utils/authIdentity';
import { AuthRequestError } from '../utils/authError';

export const AUTH_SESSION_STORAGE_KEY = 'exchange_auth_session_v2';
export const AUTH_USER_STORAGE_KEY = 'auth_user';
export const AUTH_MUTATION_LOCK_NAME = 'exchange-auth-session-mutation';
export const AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS = 15_000;
const AUTH_INVALIDATION_CHANNEL_NAME = 'exchange-auth-session-invalidation-v1';

export type AuthSessionInvalidation = { sessionId: string; userId: number };

let invalidationChannel: BroadcastChannel | null | undefined;
let invalidationSubscribers = 0;
const getInvalidationChannel = (): BroadcastChannel | null => {
  if (invalidationChannel !== undefined) return invalidationChannel;
  if (typeof BroadcastChannel === 'undefined') {
    invalidationChannel = null;
    return null;
  }
  try {
    invalidationChannel = new BroadcastChannel(AUTH_INVALIDATION_CHANNEL_NAME);
  } catch {
    invalidationChannel = null;
  }
  return invalidationChannel;
};

export const notifyAuthSessionInvalidated = (session: AuthSessionInvalidation): void => {
  if (!session.sessionId || !Number.isSafeInteger(session.userId) || session.userId <= 0) return;
  const channel = getInvalidationChannel();
  try { channel?.postMessage({ type: 'session-invalidated', ...session }); } catch { /* Storage events remain the fallback. */ }
  if (invalidationSubscribers === 0 && channel) {
    channel.close();
    invalidationChannel = undefined;
  }
};

export const subscribeToAuthSessionInvalidations = (
  callback: (session: AuthSessionInvalidation) => void,
): (() => void) => {
  const channel = getInvalidationChannel();
  if (!channel) return () => undefined;
  invalidationSubscribers += 1;
  const handler = (event: MessageEvent<unknown>) => {
    const value = event.data as Partial<{ type: string; sessionId: string; userId: number }> | null;
    if (
      value?.type === 'session-invalidated'
      && typeof value.sessionId === 'string'
      && value.sessionId.length > 0
      && Number.isSafeInteger(value.userId)
      && Number(value.userId) > 0
    ) callback({ sessionId: value.sessionId, userId: Number(value.userId) });
  };
  channel.addEventListener('message', handler);
  return () => {
    channel.removeEventListener('message', handler);
    invalidationSubscribers = Math.max(0, invalidationSubscribers - 1);
    if (invalidationSubscribers === 0) {
      channel.close();
      invalidationChannel = undefined;
    }
  };
};

export class AuthMutationLockTimeoutError extends AuthRequestError {
  constructor() {
    super('Authentication coordination timed out. Please try again.', 'AUTH_COORDINATION_TIMEOUT');
    this.name = 'AuthMutationLockTimeoutError';
  }
}

export type PersistedAuthSession = {
  schemaVersion: 2;
  tokenRevision: string;
  sessionId: string;
  userId: number;
  accessToken: string;
  refreshToken: string;
  refreshRequestID?: string;
};

export type AuthSessionReadResult =
  | { status: 'valid'; session: PersistedAuthSession }
  | { status: 'missing' }
  | { status: 'invalid' }
  | { status: 'unavailable' };

export class AuthStorageUnavailableError extends AuthRequestError {
  readonly cause: unknown;

  constructor(cause?: unknown) {
    super('Authentication storage is unavailable. Enable browser storage and try again.', 'AUTH_STORAGE_UNAVAILABLE');
    this.name = 'AuthStorageUnavailableError';
    this.cause = cause;
  }
}

export class AuthRefreshCoordinationUnavailableError extends Error {
  constructor() {
    super('Secure cross-tab refresh coordination is unavailable');
    this.name = 'AuthRefreshCoordinationUnavailableError';
  }
}

const storage = (): Storage | null => {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
};

const storageUnavailableError = (cause?: unknown): AuthStorageUnavailableError => (
  cause instanceof AuthStorageUnavailableError ? cause : new AuthStorageUnavailableError(cause)
);

const isNonEmptyString = (value: unknown): value is string => (
  typeof value === 'string' && value.trim().length > 0
);

const validateSession = (value: unknown): PersistedAuthSession | null => {
  if (typeof value !== 'object' || value === null) {
    return null;
  }
  const candidate = value as Record<string, unknown>;
  if (
    candidate.schemaVersion !== 2
    || !isNonEmptyString(candidate.tokenRevision)
    || !isNonEmptyString(candidate.sessionId)
    || typeof candidate.userId !== 'number'
    || !Number.isSafeInteger(candidate.userId)
    || candidate.userId <= 0
    || !isNonEmptyString(candidate.accessToken)
    || /^Bearer\s/i.test(candidate.accessToken.trim())
    || !isNonEmptyString(candidate.refreshToken)
    || (candidate.refreshRequestID !== undefined && (
      typeof candidate.refreshRequestID !== 'string'
      || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(candidate.refreshRequestID)
    ))
  ) {
    return null;
  }

  const metadata = decodeAuthTokenMetadata(candidate.accessToken);
  if (
    !metadata
    || metadata.sessionId !== candidate.sessionId.trim()
    || metadata.userId !== candidate.userId
  ) {
    return null;
  }

  return {
    schemaVersion: 2,
    tokenRevision: candidate.tokenRevision.trim(),
    sessionId: metadata.sessionId,
    userId: metadata.userId,
    accessToken: candidate.accessToken.trim(),
    refreshToken: candidate.refreshToken.trim(),
    ...(candidate.refreshRequestID !== undefined ? { refreshRequestID: candidate.refreshRequestID as string } : {}),
  };
};

export const createTokenRevision = (): string => {
  const cryptoApi = globalThis.crypto;
  if (typeof cryptoApi?.randomUUID === 'function') {
    return cryptoApi.randomUUID();
  }
  if (typeof cryptoApi?.getRandomValues === 'function') {
    const bytes = cryptoApi.getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  throw new Error('Secure randomness is unavailable for auth token revision');
};

export const readPersistedAuthSession = (): AuthSessionReadResult => {
  const store = storage();
  if (!store) {
    return { status: 'unavailable' };
  }

  let raw: string | null;
  try {
    raw = store.getItem(AUTH_SESSION_STORAGE_KEY);
  } catch {
    return { status: 'unavailable' };
  }

  if (raw === null) return { status: 'missing' };

  try {
    const session = validateSession(JSON.parse(raw));
    return session ? { status: 'valid', session } : { status: 'invalid' };
  } catch {
    return { status: 'invalid' };
  }
};

export const writePersistedAuthSession = (session: PersistedAuthSession): void => {
  const validated = validateSession(session);
  if (!validated) {
    throw new Error('Invalid persisted auth session');
  }
  const store = storage();
  if (!store) throw new AuthStorageUnavailableError();
  try {
    store.setItem(AUTH_SESSION_STORAGE_KEY, JSON.stringify(validated));
  } catch (error) {
    throw storageUnavailableError(error);
  }
};

export const clearPersistedAuthSession = (): void => {
  const store = storage();
  if (!store) throw new AuthStorageUnavailableError();
  try {
    store.removeItem(AUTH_SESSION_STORAGE_KEY);
  } catch (error) {
    throw storageUnavailableError(error);
  }
};

export const runWithAuthMutationLock = async <T>(
  operation: () => Promise<T> | T,
  options: { requireLock?: boolean; signal?: AbortSignal } = {},
): Promise<T> => {
  const cancellationReason = () => options.signal?.reason ?? new DOMException('Authentication coordination canceled', 'AbortError');
  if (options.signal?.aborted) throw cancellationReason();
  const lockManager = typeof navigator === 'undefined' ? undefined : navigator.locks;
  if (!lockManager || typeof lockManager.request !== 'function') {
    if (options.requireLock) {
      throw new AuthRefreshCoordinationUnavailableError();
    }
    return operation();
  }
  const controller = new AbortController();
  const deadline = Date.now() + AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS;
  let waiting = true;
  let waitError: unknown;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let abortWait: () => void;
  const finishWaiting = () => {
    waiting = false;
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', abortWait);
  };
  return new Promise<T>((resolve, reject) => {
    const cancelWait = (reason: unknown) => {
      if (!waiting) return;
      waitError = reason;
      finishWaiting();
      controller.abort(reason);
      reject(reason);
    };
    abortWait = () => cancelWait(cancellationReason());
    options.signal?.addEventListener('abort', abortWait, { once: true });
    timer = setTimeout(() => cancelWait(new AuthMutationLockTimeoutError()), AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS);
    try {
      const request = lockManager.request(
        AUTH_MUTATION_LOCK_NAME,
        { mode: 'exclusive', signal: controller.signal },
        () => {
          // A resumed tab may receive the grant before its overdue timer runs.
          // Also reject a late callback from a canceled request.
          if (waiting && Date.now() >= deadline) cancelWait(new AuthMutationLockTimeoutError());
          if (waitError !== undefined) throw waitError;
          finishWaiting();
          // The deadline only bounds acquisition. Keep holding the lock until
          // the entire operation (including HTTP and credential commit) ends.
          return operation();
        },
      );
      Promise.resolve(request).then(resolve, reject).finally(finishWaiting);
    } catch (error) {
      finishWaiting();
      reject(error);
    }
  });
};

export const subscribeToPersistedAuthChanges = (callback: (key: string | null) => void): (() => void) => {
  if (typeof window === 'undefined') {
    return () => undefined;
  }
  const handleStorage = (event: StorageEvent) => {
    if (
      event.key === AUTH_SESSION_STORAGE_KEY
      || event.key === AUTH_USER_STORAGE_KEY
      || event.key === null
    ) {
      callback(event.key);
    }
  };
  window.addEventListener('storage', handleStorage);
  return () => window.removeEventListener('storage', handleStorage);
};
