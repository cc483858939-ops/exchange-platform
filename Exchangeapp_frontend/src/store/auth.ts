import { defineStore } from 'pinia';
import { computed, onScopeDispose, ref } from 'vue';
import axios from 'axios';
import { apiBaseUrl } from '../api';
import type { ApiErrorResponse } from '../api';
import {
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  AuthStorageUnavailableError,
  clearPersistedAuthSession,
  createTokenRevision,
  notifyAuthSessionInvalidated,
  readPersistedAuthSession,
  runWithAuthMutationLock,
  subscribeToAuthSessionInvalidations,
  subscribeToPersistedAuthChanges,
  writePersistedAuthSession,
} from '../auth/authSessionCoordinator';
import type { PersistedAuthSession } from '../auth/authSessionCoordinator';
import { decodeAuthTokenMetadata, normalizeAuthIdentity } from '../utils/authIdentity';
import type { AuthIdentity } from '../utils/authIdentity';
import { AuthRequestError } from '../utils/authError';
import { AUTH_REQUEST_TIMEOUT_MS, isRequestTimeoutError } from '../utils/requestTimeout';
import type { AuthRequestBinding } from '../auth/authRequestBinding';

const authClient = axios.create({
  baseURL: apiBaseUrl,
  timeout: AUTH_REQUEST_TIMEOUT_MS,
});

const authUserKey = AUTH_USER_STORAGE_KEY;

type AuthResponse = {
  access_token: string;
  refresh_token: string;
  token_type: 'Bearer';
  expires_in: number;
  refresh_expires_in: number;
  user: unknown;
};

type ValidatedAuthResponse = {
  session: PersistedAuthSession;
  identity: AuthIdentity;
};

export class AuthSessionChangedError extends Error {
  constructor() {
    super('Authentication session changed');
    this.name = 'AuthSessionChangedError';
  }
}

const toAuthRequestError = (error: unknown, fallback: string): AuthRequestError => {
  if (error instanceof AuthRequestError) return error;
  if (isRequestTimeoutError(error)) {
    return new AuthRequestError(
      'Request timed out. Check your connection and try again.',
      'AUTH_REQUEST_TIMEOUT',
    );
  }

  const data = (error as { response?: { data?: Partial<ApiErrorResponse> } }).response?.data;
  const code = typeof data?.code === 'string' && data.code.trim() ? data.code.trim() : null;
  const message = code ? authErrorMessages[code] : undefined;
  return new AuthRequestError(message ?? fallback, message ? code : null);
};

type AuthReconcileMode = 'best-effort' | 'require-readable';

const authErrorMessages: Record<string, string> = {
  AUTH_TOKEN_MISSING: 'Authentication required.',
  AUTH_TOKEN_INVALID: 'Authentication failed.',
  AUTH_CREDENTIALS_INVALID: 'Invalid username or password.',
  AUTH_REQUEST_INVALID: 'Invalid request data.',
  AUTH_PASSWORD_TOO_LONG: 'Password must be 72 bytes or fewer.',
  AUTH_REQUEST_TOO_LARGE: 'Authentication request is too large.',
  AUTH_RATE_LIMITED: 'Too many authentication attempts.',
  AUTH_RATE_LIMIT_UNAVAILABLE: 'Authentication is temporarily unavailable.',
  AUTH_USERNAME_UNAVAILABLE: 'Username is unavailable.',
  AUTH_REFRESH_INVALID: 'Authentication failed.',
  AUTH_REFRESH_EXPIRED: 'Authentication failed.',
  AUTH_REFRESH_REUSED: 'Authentication failed.',
  AUTH_INTERNAL: 'Authentication failed. Please try again.',
  AUTH_LIKE_STATE_UNAVAILABLE: 'Registration is temporarily unavailable.',
  AUTH_ONBOARDING_INIT_FAILED: 'Registration could not be completed.',
  AUTH_TOKEN_ISSUE_FAILED: 'Authentication failed. Please try again.',
  AUTH_LOGOUT_UNAVAILABLE: 'Sign out could not be confirmed. Please try again.',
  AUTH_CURRENT_PASSWORD_INVALID: 'Current password is incorrect.',
  AUTH_PASSWORD_UNCHANGED: 'Choose a password different from your current password.',
  AUTH_VERSION_UNAVAILABLE: 'Authentication is temporarily unavailable. Please try again.',
  AUTH_PASSWORD_CHANGED_CLEANUP_FAILED: 'Password changed, but saved credentials could not be cleared. Close other tabs and sign in again.',
};

const shouldClearAuthAfterRefreshFailure = (error: unknown): boolean => {
  if (
    isRequestTimeoutError(error)
    || !axios.isAxiosError(error)
    || error.response?.status !== 401
  ) {
    return false;
  }

  const data = error.response.data as Partial<ApiErrorResponse> | undefined;
  return data?.code === 'AUTH_REFRESH_INVALID'
    || data?.code === 'AUTH_REFRESH_EXPIRED'
    || data?.code === 'AUTH_REFRESH_REUSED';
};

const asAuthorizationHeader = (rawToken: string | null): string | null => {
  const trimmed = rawToken?.trim();
  return trimmed ? `Bearer ${trimmed.replace(/^Bearer\s+/i, '')}` : null;
};

const minimalIdentity = (userId: number): AuthIdentity => ({
  id: userId,
  username: '',
  display_name: '',
  avatar_url: '',
});

const readStoredIdentity = (expectedUserId: number): AuthIdentity | null => {
  try {
    const raw = typeof window === 'undefined' ? null : window.localStorage.getItem(authUserKey);
    if (!raw) return null;
    const normalized = normalizeAuthIdentity(JSON.parse(raw));
    return normalized?.id === expectedUserId ? normalized : null;
  } catch {
    return null;
  }
};

const persistIdentityCacheBestEffort = (next: AuthIdentity | null): boolean => {
  try {
    if (typeof window === 'undefined') return false;
    const storage = window.localStorage;
    if (next) storage.setItem(authUserKey, JSON.stringify(next));
    else storage.removeItem(authUserKey);
    return true;
  } catch {
    return false;
  }
};

const initializePersistedSession = (): PersistedAuthSession | null => {
  const result = readPersistedAuthSession();
  if (result.status === 'valid') return result.session;
  if (result.status === 'missing') {
    persistIdentityCacheBestEffort(null);
  } else if (result.status === 'invalid') {
    try { clearPersistedAuthSession(); } catch { /* Startup cleanup is best-effort. */ }
    persistIdentityCacheBestEffort(null);
  }
  return null;
};

const validateAuthResponse = (
  response: AuthResponse,
  expectedSessionId?: string,
  expectedUserId?: number,
): ValidatedAuthResponse => {
  const rawAccessToken = typeof response?.access_token === 'string'
    ? response.access_token.trim().replace(/^Bearer\s+/i, '')
    : '';
  const refreshToken = typeof response?.refresh_token === 'string'
    ? response.refresh_token.trim()
    : '';
  const identity = normalizeAuthIdentity(response?.user);
  const metadata = decodeAuthTokenMetadata(rawAccessToken);
  if (
    !rawAccessToken
    || !refreshToken
    || response?.token_type !== 'Bearer'
    || !identity
    || !metadata
    || metadata.userId !== identity.id
    || (expectedSessionId !== undefined && metadata.sessionId !== expectedSessionId)
    || (expectedUserId !== undefined && metadata.userId !== expectedUserId)
  ) {
    throw new Error('Invalid authentication response');
  }

  return {
    session: {
      schemaVersion: 2,
      tokenRevision: createTokenRevision(),
      sessionId: metadata.sessionId,
      userId: metadata.userId,
      accessToken: rawAccessToken,
      refreshToken,
    },
    identity,
  };
};

const samePersistedGeneration = (
  session: PersistedAuthSession | null,
  sessionId: string,
  tokenRevision: string,
  refreshToken: string,
): boolean => Boolean(
  session
  && session.sessionId === sessionId
  && session.tokenRevision === tokenRevision
  && session.refreshToken === refreshToken,
);

export const useAuthStore = defineStore('auth', () => {
  const initialSession = initializePersistedSession();
  const token = ref<string | null>(null);
  const refreshToken = ref<string | null>(null);
  const sessionId = ref<string | null>(null);
  const sessionUserId = ref<number | null>(null);
  const tokenRevision = ref<string | null>(null);
  const identity = ref<AuthIdentity | null>(null);
  const sessionVersion = ref(0);
  let pendingLocalLogoutSessionId: string | null = null;
  let sessionTransition = new AbortController();

  const runWithSessionMutationLock = <T>(
    operation: () => Promise<T> | T,
    options: { requireLock?: boolean; signal?: AbortSignal } = {},
  ): Promise<T> => {
    const controller = new AbortController();
    const signals = [sessionTransition.signal, options.signal].filter((signal): signal is AbortSignal => Boolean(signal));
    const cancel = (event: Event) => controller.abort((event.target as AbortSignal).reason);
    for (const signal of signals) {
      if (signal.aborted) controller.abort(signal.reason);
      else signal.addEventListener('abort', cancel, { once: true });
    }
    return runWithAuthMutationLock(operation, { requireLock: options.requireLock, signal: controller.signal })
      .finally(() => signals.forEach(signal => signal.removeEventListener('abort', cancel)));
  };

  const isAuthenticated = computed(() => Boolean(token.value && refreshToken.value && identity.value));
  const currentIdentity = computed<AuthIdentity | null>(() => identity.value);

  const captureRequestAuthBinding = (): AuthRequestBinding | null => {
    if (
      !isAuthenticated.value
      || !sessionId.value
      || !sessionUserId.value
      || identity.value?.id !== sessionUserId.value
    ) return null;
    return Object.freeze({
      userID: sessionUserId.value,
      sessionID: sessionId.value,
      sessionVersion: sessionVersion.value,
    });
  };

  const matchesRequestAuthBinding = (binding: AuthRequestBinding): boolean => (
    isAuthenticated.value
    && identity.value?.id === binding.userID
    && sessionUserId.value === binding.userID
    && sessionId.value === binding.sessionID
    && sessionVersion.value === binding.sessionVersion
  );

  const advanceSessionVersion = (): number => {
    sessionTransition.abort(new AuthSessionChangedError());
    sessionTransition = new AbortController();
    sessionVersion.value += 1;
    return sessionVersion.value;
  };

  const applyPersistedSession = (
    next: PersistedAuthSession | null,
    options: { external?: boolean; identity?: AuthIdentity } = {},
  ) => {
    const currentSessionId = sessionId.value;
    const currentUserId = sessionUserId.value;
    const nextSessionId = next?.sessionId ?? null;
    const nextUserId = next?.userId ?? null;
    const sameSession = currentSessionId === nextSessionId && currentUserId === nextUserId;
    if (options.external && !sameSession && (currentSessionId !== null || nextSessionId !== null)) {
      advanceSessionVersion();
    }

    if (!next) {
      token.value = null;
      refreshToken.value = null;
      sessionId.value = null;
      sessionUserId.value = null;
      tokenRevision.value = null;
      identity.value = null;
      return;
    }

    token.value = asAuthorizationHeader(next.accessToken);
    refreshToken.value = next.refreshToken;
    sessionId.value = next.sessionId;
    sessionUserId.value = next.userId;
    tokenRevision.value = next.tokenRevision;
    identity.value = options.identity?.id === next.userId
      ? options.identity
      : readStoredIdentity(next.userId) ?? minimalIdentity(next.userId);
  };

  const reconcilePersistedAuthState = (
    changedStorageKey?: string | null,
    mode: AuthReconcileMode = 'best-effort',
  ) => {
    const result = readPersistedAuthSession();
    if (result.status === 'unavailable') {
      if (mode === 'require-readable') throw new AuthStorageUnavailableError();
      return;
    }

    const next = result.status === 'valid' ? result.session : null;
    if (pendingLocalLogoutSessionId && next?.sessionId === pendingLocalLogoutSessionId) {
      return;
    }
    if (result.status === 'invalid') {
      try { clearPersistedAuthSession(); } catch { /* Invalid data must never block tab startup. */ }
    }
    if (!next) persistIdentityCacheBestEffort(null);

    const credentialStateChanged = sessionId.value !== (next?.sessionId ?? null)
      || sessionUserId.value !== (next?.userId ?? null)
      || tokenRevision.value !== (next?.tokenRevision ?? null)
      || refreshToken.value !== (next?.refreshToken ?? null)
      || token.value !== asAuthorizationHeader(next?.accessToken ?? null);
    if (credentialStateChanged) {
      applyPersistedSession(next, { external: true });
      return;
    }
    if (!next) {
      identity.value = null;
    } else if (changedStorageKey === authUserKey) {
      identity.value = readStoredIdentity(next.userId) ?? minimalIdentity(next.userId);
    } else if (!identity.value || identity.value.id !== next.userId) {
      identity.value = readStoredIdentity(next.userId) ?? minimalIdentity(next.userId);
    }
  };

  const commitAuthResponse = async (
    response: AuthResponse,
    operationVersion: number,
  ) => {
    if (sessionVersion.value !== operationVersion) throw new AuthSessionChangedError();
    await runWithSessionMutationLock(() => {
      reconcilePersistedAuthState(undefined, 'require-readable');
      if (sessionVersion.value !== operationVersion) {
        throw new AuthSessionChangedError();
      }
      const validated = validateAuthResponse(response);
      writePersistedAuthSession(validated.session);
      applyPersistedSession(validated.session, { identity: validated.identity });
      persistIdentityCacheBestEffort(validated.identity);
    });
  };

  const commitRefreshFailureIfCurrent = (
    error: unknown,
    versionAtStart: number,
    sessionIdAtStart: string,
  ): boolean => {
    if (!shouldClearAuthAfterRefreshFailure(error)) {
      return false;
    }
    const result = readPersistedAuthSession();
    if (result.status === 'unavailable') throw new AuthStorageUnavailableError();
    const latest = result.status === 'valid' ? result.session : null;
    if (latest?.sessionId !== sessionIdAtStart || latest.userId !== sessionUserId.value) {
      reconcilePersistedAuthState();
      throw new AuthSessionChangedError();
    }

    // The server has confirmed this session is no longer refreshable. Remove it
    // from memory immediately, and retain the guard if strict storage cleanup fails.
    pendingLocalLogoutSessionId = sessionIdAtStart;
    if (sessionVersion.value === versionAtStart) advanceSessionVersion();
    applyPersistedSession(null);
    clearPersistedAuthSession();
    persistIdentityCacheBestEffort(null);
    if (pendingLocalLogoutSessionId === sessionIdAtStart) pendingLocalLogoutSessionId = null;
    return true;
  };

  const refreshAccessToken = async (options: { signal?: AbortSignal } = {}): Promise<string> => {
    const versionAtStart = sessionVersion.value;
    const sessionIdAtStart = sessionId.value;
    const tokenRevisionAtStart = tokenRevision.value;
    const refreshTokenAtStart = refreshToken.value;
    const userIdAtStart = sessionUserId.value;

    if (!sessionIdAtStart || !tokenRevisionAtStart || !refreshTokenAtStart || !userIdAtStart) {
      void clearAuth().catch(() => undefined);
      throw new Error('Missing refresh session');
    }

    return runWithSessionMutationLock(async () => {
      if (
        sessionVersion.value !== versionAtStart
        || sessionId.value !== sessionIdAtStart
      ) {
        throw new AuthSessionChangedError();
      }

      const result = readPersistedAuthSession();
      if (result.status === 'unavailable') throw new AuthStorageUnavailableError();
      const latest = result.status === 'valid' ? result.session : null;
      if (!latest || latest.sessionId !== sessionIdAtStart || latest.userId !== userIdAtStart) {
        reconcilePersistedAuthState();
        throw new AuthSessionChangedError();
      }
      if (
        latest.tokenRevision !== tokenRevisionAtStart
        || latest.refreshToken !== refreshTokenAtStart
      ) {
        applyPersistedSession(latest);
        return asAuthorizationHeader(latest.accessToken) as string;
      }

      try {
        // Persist before sending so a lost response or a suspended tab retries
        // the same rotation, including after this store is recreated.
        const requestID = latest.refreshRequestID ?? createTokenRevision();
        if (!latest.refreshRequestID) {
          writePersistedAuthSession({ ...latest, refreshRequestID: requestID });
        }
        const response = await authClient.post<AuthResponse>('/auth/refresh', {
          refresh_token: refreshTokenAtStart,
          request_id: requestID,
        });

        if (
          sessionVersion.value !== versionAtStart
          || sessionId.value !== sessionIdAtStart
        ) {
          throw new AuthSessionChangedError();
        }

        const persistedResult = readPersistedAuthSession();
        if (persistedResult.status === 'unavailable') throw new AuthStorageUnavailableError();
        const persistedBeforeCommit = persistedResult.status === 'valid' ? persistedResult.session : null;
        if (
          !persistedBeforeCommit
          || persistedBeforeCommit.sessionId !== sessionIdAtStart
          || persistedBeforeCommit.userId !== userIdAtStart
        ) {
          reconcilePersistedAuthState();
          throw new AuthSessionChangedError();
        }
        if (!samePersistedGeneration(
          persistedBeforeCommit,
          sessionIdAtStart,
          tokenRevisionAtStart,
          refreshTokenAtStart,
        )) {
          applyPersistedSession(persistedBeforeCommit);
          return asAuthorizationHeader(persistedBeforeCommit.accessToken) as string;
        }

        const validated = validateAuthResponse(
          response.data,
          sessionIdAtStart,
          userIdAtStart,
        );
        writePersistedAuthSession(validated.session);
        applyPersistedSession(validated.session, { identity: validated.identity });
        persistIdentityCacheBestEffort(validated.identity);
        return asAuthorizationHeader(validated.session.accessToken) as string;
      } catch (error) {
        if (error instanceof AuthSessionChangedError) {
          throw error;
        }
        commitRefreshFailureIfCurrent(error, versionAtStart, sessionIdAtStart);
        throw error;
      }
    }, { requireLock: true, signal: options.signal });
  };

  const clearAuth = (options: { signal?: AbortSignal } = {}): Promise<void> => {
    const loggedOutSessionId = sessionId.value ?? pendingLocalLogoutSessionId;
    advanceSessionVersion();
    applyPersistedSession(null);
    if (loggedOutSessionId) {
      pendingLocalLogoutSessionId = loggedOutSessionId;
    }

    return runWithAuthMutationLock(() => {
      const result = readPersistedAuthSession();
      if (result.status === 'unavailable') throw new AuthStorageUnavailableError();
      if (result.status === 'invalid') {
        throw new AuthStorageUnavailableError(new Error('Cannot verify the stored session owner.'));
      }
      const latest = result.status === 'valid' ? result.session : null;
      if (!latest || latest.sessionId === loggedOutSessionId) {
        clearPersistedAuthSession();
        persistIdentityCacheBestEffort(null);
      }
    }, { signal: options.signal }).then(() => {
      // Keep the guard if acquisition or storage cleanup fails. A retry must
      // target this captured sid even though memory is already signed out.
      if (pendingLocalLogoutSessionId === loggedOutSessionId) {
        pendingLocalLogoutSessionId = null;
      }
    });
  };

  const logout = async (): Promise<void> => {
    // Capture before clearing memory. Never use a later login's credentials.
    // The server accepts this family's consumed secret if an in-flight refresh
    // already rotated it, and revocation cannot restore credentials locally.
    const pendingResult = readPersistedAuthSession();
    const pendingSession = pendingResult.status === 'valid' ? pendingResult.session : null;
    const loggedOutRefreshToken = refreshToken.value ?? (
      pendingLocalLogoutSessionId && pendingSession?.sessionId === pendingLocalLogoutSessionId
        ? pendingSession.refreshToken : null
    );
    let cleanupFailed = false;
    try { await clearAuth(); } catch { cleanupFailed = true; }
    // Revocation is safe with the captured credential even when local lock
    // acquisition failed. It must never use a later login's refresh token.
    if (loggedOutRefreshToken) try {
      const response = await authClient.post('/auth/logout', {
        refresh_token: loggedOutRefreshToken,
      });
      if (response.status !== 204) throw new Error('Server sign-out was not confirmed');
    } catch {
      throw new AuthRequestError(
        cleanupFailed
          ? 'Signed out in memory, but saved credentials could not be cleared and server sign-out could not be confirmed.'
          : 'Signed out on this browser, but server sign-out could not be confirmed. The session may still be active.',
        'AUTH_LOGOUT_UNCONFIRMED',
      );
    }
    if (cleanupFailed) throw new AuthRequestError(
      'Signed out locally, but saved credentials could not be cleared. Please retry sign-out.',
      'AUTH_LOGOUT_CLEANUP_PENDING',
    );
  };

  const clearChangedPasswordSession = async (binding: AuthRequestBinding): Promise<void> => {
    await runWithSessionMutationLock(() => {
      if (!matchesRequestAuthBinding(binding)) {
        if (sessionId.value === null && pendingLocalLogoutSessionId === binding.sessionID) return;
        throw new AuthSessionChangedError();
      }

      const persisted = readPersistedAuthSession();
      if (persisted.status === 'valid' && (
        persisted.session.sessionId !== binding.sessionID
        || persisted.session.userId !== binding.userID
      )) {
        reconcilePersistedAuthState();
        throw new AuthSessionChangedError();
      }

      pendingLocalLogoutSessionId = binding.sessionID;
      advanceSessionVersion();
      applyPersistedSession(null);
      notifyAuthSessionInvalidated({ sessionId: binding.sessionID, userId: binding.userID });
      if (persisted.status === 'unavailable') {
        persistIdentityCacheBestEffort(null);
        throw new AuthRequestError(
          'Password changed, but saved credentials could not be checked or cleared. Close other tabs and sign in again.',
          'AUTH_PASSWORD_CHANGED_CLEANUP_FAILED',
        );
      }
      try {
        clearPersistedAuthSession();
      } catch {
        persistIdentityCacheBestEffort(null);
        throw new AuthRequestError(
          'Password changed, but saved credentials could not be cleared. Close other tabs and sign in again.',
          'AUTH_PASSWORD_CHANGED_CLEANUP_FAILED',
        );
      }
      if (!persistIdentityCacheBestEffort(null)) {
        throw new AuthRequestError(
          'Password changed, but browser storage cleanup could not be completed. Close other tabs and sign in again.',
          'AUTH_PASSWORD_CHANGED_CLEANUP_FAILED',
        );
      }
      if (pendingLocalLogoutSessionId === binding.sessionID) pendingLocalLogoutSessionId = null;
    }, { requireLock: true });
  };

  const changePassword = async (
    currentPassword: string,
    newPassword: string,
    expectedBinding: AuthRequestBinding,
  ): Promise<void> => {
    reconcilePersistedAuthState(undefined, 'require-readable');
    if (!expectedBinding || !matchesRequestAuthBinding(expectedBinding)) {
      throw new AuthSessionChangedError();
    }
    const authorization = token.value;
    if (!authorization) throw new AuthSessionChangedError();

    try {
      const response = await authClient.put('/me/password', {
        current_password: currentPassword,
        new_password: newPassword,
      }, { headers: { Authorization: authorization } });
      if (response.status !== 204) throw new Error('Password change was not confirmed');
    } catch (error) {
      if (error instanceof AuthSessionChangedError) throw error;
      throw toAuthRequestError(error, 'Password could not be changed. Please try again.');
    }

    try {
      await clearChangedPasswordSession(expectedBinding);
    } catch (error) {
      if (error instanceof AuthSessionChangedError) throw error;
      if (error instanceof AuthRequestError && error.code === 'AUTH_PASSWORD_CHANGED_CLEANUP_FAILED') throw error;
      if (matchesRequestAuthBinding(expectedBinding)) {
        pendingLocalLogoutSessionId = expectedBinding.sessionID;
        advanceSessionVersion();
        applyPersistedSession(null);
        notifyAuthSessionInvalidated({ sessionId: expectedBinding.sessionID, userId: expectedBinding.userID });
      }
      throw new AuthRequestError(
        'Password changed, but browser session cleanup could not be confirmed. Close other tabs and sign in again.',
        'AUTH_PASSWORD_CHANGED_CLEANUP_FAILED',
      );
    }
  };

  const syncCurrentIdentityProfile = (candidate: AuthIdentity): boolean => {
    const normalized = normalizeAuthIdentity(candidate);
    if (!normalized || !sessionUserId.value || normalized.id !== sessionUserId.value) {
      return false;
    }
    identity.value = normalized;
    persistIdentityCacheBestEffort(normalized);
    return true;
  };

  applyPersistedSession(initialSession);

  const unsubscribe = subscribeToPersistedAuthChanges(reconcilePersistedAuthState);
  const unsubscribeInvalidation = subscribeToAuthSessionInvalidations((session) => {
    if (sessionId.value === session.sessionId && sessionUserId.value === session.userId) {
      pendingLocalLogoutSessionId = session.sessionId;
      advanceSessionVersion();
      applyPersistedSession(null);
    }
    void runWithSessionMutationLock(() => {
      const persisted = readPersistedAuthSession();
      if (persisted.status === 'unavailable') throw new AuthStorageUnavailableError();
      if (persisted.status === 'valid' && persisted.session.sessionId === session.sessionId
        && persisted.session.userId === session.userId) {
        clearPersistedAuthSession();
      }
      if (sessionId.value !== session.sessionId && pendingLocalLogoutSessionId === session.sessionId) {
        pendingLocalLogoutSessionId = null;
      }
    }).catch(() => undefined);
  });
  const handlePageShow = () => reconcilePersistedAuthState();
  const handleVisibilityChange = () => {
    if (document.visibilityState === 'visible') {
      reconcilePersistedAuthState();
    }
  };
  if (typeof window !== 'undefined') {
    window.addEventListener('pageshow', handlePageShow);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    onScopeDispose(() => {
      sessionTransition.abort(new AuthSessionChangedError());
      unsubscribe();
      unsubscribeInvalidation();
      window.removeEventListener('pageshow', handlePageShow);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    });
  }

  const login = async (username: string, password: string) => {
    const operationVersion = advanceSessionVersion();

    try {
      const response = await authClient.post<AuthResponse>('/auth/login', { username, password });
      await commitAuthResponse(response.data, operationVersion);
    } catch (error) {
      if (error instanceof AuthSessionChangedError) {
        throw error;
      }
      if (error instanceof AuthRefreshCoordinationUnavailableError) {
        throw error;
      }
      throw toAuthRequestError(error, '登录失败，请稍后重试');
    }
  };

  const register = async (username: string, password: string) => {
    const operationVersion = advanceSessionVersion();

    try {
      const response = await authClient.post<AuthResponse>('/auth/register', { username, password });
      await commitAuthResponse(response.data, operationVersion);
    } catch (error) {
      if (error instanceof AuthSessionChangedError) {
        throw error;
      }
      if (error instanceof AuthRefreshCoordinationUnavailableError) {
        throw error;
      }
      throw toAuthRequestError(error, '注册失败，请稍后重试');
    }
  };

  return {
    token,
    refreshToken,
    sessionVersion,
    isAuthenticated,
    currentIdentity,
    captureRequestAuthBinding,
    matchesRequestAuthBinding,
    login,
    register,
    changePassword,
    refreshAccessToken,
    syncCurrentIdentityProfile,
    reconcilePersistedAuthState,
    clearAuth,
    logout,
  };
});
