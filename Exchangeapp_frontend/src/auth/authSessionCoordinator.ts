import { decodeAuthTokenMetadata } from '../utils/authIdentity';
import { AuthRequestError } from '../utils/authError';

export const AUTH_SESSION_STORAGE_KEY = 'exchange_auth_session_v2';
export const AUTH_USER_STORAGE_KEY = 'auth_user';
export const AUTH_MUTATION_LOCK_NAME = 'exchange-auth-session-mutation';
export const AUTH_MUTATION_LOCK_WAIT_TIMEOUT_MS = 15_000;

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

export class AuthRefreshCoordinationUnavailableError extends Error {
  constructor() {
    super('Secure cross-tab refresh coordination is unavailable');
    this.name = 'AuthRefreshCoordinationUnavailableError';
  }
}

const storage = (): Storage | null => {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
};

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

export const readPersistedAuthSession = (): PersistedAuthSession | null => {
  const store = storage();
  if (!store) {
    return null;
  }
  try {
    const raw = store.getItem(AUTH_SESSION_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    return validateSession(JSON.parse(raw));
  } catch {
    return null;
  }
};

export const writePersistedAuthSession = (session: PersistedAuthSession): void => {
  const store = storage();
  if (!store) {
    throw new Error('Auth session storage is unavailable');
  }
  const validated = validateSession(session);
  if (!validated) {
    throw new Error('Invalid persisted auth session');
  }
  store.setItem(AUTH_SESSION_STORAGE_KEY, JSON.stringify(validated));
};

export const clearPersistedAuthSession = (): void => {
  const store = storage();
  if (!store) {
    return;
  }
  store.removeItem(AUTH_SESSION_STORAGE_KEY);
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

export const subscribeToPersistedAuthChanges = (callback: () => void): (() => void) => {
  if (typeof window === 'undefined') {
    return () => undefined;
  }
  const handleStorage = (event: StorageEvent) => {
    if (
      event.key === AUTH_SESSION_STORAGE_KEY
      || event.key === AUTH_USER_STORAGE_KEY
      || event.key === null
    ) {
      callback();
    }
  };
  window.addEventListener('storage', handleStorage);
  return () => window.removeEventListener('storage', handleStorage);
};
