import { defineStore } from 'pinia';
import { computed, onScopeDispose, ref } from 'vue';
import axios from 'axios';
import { apiBaseUrl } from '../api';
import {
  AUTH_USER_STORAGE_KEY,
  AuthRefreshCoordinationUnavailableError,
  clearPersistedAuthSession,
  createTokenRevision,
  removeLegacyAuthCredentials,
  readPersistedAuthSession,
  runWithAuthMutationLock,
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

type AuthErrorResponse = {
  code?: string;
  error?: string;
  message?: string;
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

  const data = (error as { response?: { data?: AuthErrorResponse } }).response?.data;
  const code = typeof data?.code === 'string' && data.code.trim() ? data.code.trim() : null;
  const message = typeof data?.message === 'string' && data.message.trim()
    ? data.message.trim()
    : typeof data?.error === 'string' && data.error.trim()
      ? data.error.trim()
      : fallback;
  return new AuthRequestError(message, code);
};

const shouldClearAuthAfterRefreshFailure = (error: unknown): boolean => {
  if (
    isRequestTimeoutError(error)
    || !axios.isAxiosError(error)
    || error.response?.status !== 401
  ) {
    return false;
  }

  const data = error.response.data as AuthErrorResponse | undefined;
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
    const raw = localStorage.getItem(authUserKey);
    if (!raw) return null;
    const normalized = normalizeAuthIdentity(JSON.parse(raw));
    return normalized?.id === expectedUserId ? normalized : null;
  } catch {
    return null;
  }
};

const initializePersistedSession = (): PersistedAuthSession | null => {
  const session = readPersistedAuthSession();
  if (session) {
    removeLegacyAuthCredentials();
  } else {
    clearPersistedAuthSession();
    localStorage.removeItem(authUserKey);
  }
  return session;
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
    options: { external?: boolean } = {},
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
    identity.value = readStoredIdentity(next.userId) ?? minimalIdentity(next.userId);
  };

  const reconcilePersistedAuthState = () => {
    const next = readPersistedAuthSession();
    if (pendingLocalLogoutSessionId && next?.sessionId === pendingLocalLogoutSessionId) {
      return;
    }

    const credentialStateChanged = sessionId.value !== (next?.sessionId ?? null)
      || sessionUserId.value !== (next?.userId ?? null)
      || tokenRevision.value !== (next?.tokenRevision ?? null)
      || refreshToken.value !== (next?.refreshToken ?? null)
      || token.value !== asAuthorizationHeader(next?.accessToken ?? null);
    if (credentialStateChanged) {
      applyPersistedSession(next, { external: true });
      return;
    }
    identity.value = next
      ? readStoredIdentity(next.userId) ?? minimalIdentity(next.userId)
      : null;
  };

  const persistIdentity = (next: AuthIdentity | null) => {
    identity.value = next;
    if (next) {
      localStorage.setItem(authUserKey, JSON.stringify(next));
    } else {
      localStorage.removeItem(authUserKey);
    }
  };

  const commitAuthResponse = async (
    response: AuthResponse,
    operationVersion: number,
  ) => {
    if (sessionVersion.value !== operationVersion) throw new AuthSessionChangedError();
    await runWithSessionMutationLock(() => {
      reconcilePersistedAuthState();
      if (sessionVersion.value !== operationVersion) {
        throw new AuthSessionChangedError();
      }
      const validated = validateAuthResponse(response);
      writePersistedAuthSession(validated.session);
      persistIdentity(validated.identity);
      applyPersistedSession(validated.session);
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
    const latest = readPersistedAuthSession();
    if (latest?.sessionId !== sessionIdAtStart) {
      reconcilePersistedAuthState();
      throw new AuthSessionChangedError();
    }

    clearPersistedAuthSession();
    localStorage.removeItem(authUserKey);
    if (sessionVersion.value === versionAtStart) {
      advanceSessionVersion();
    }
    applyPersistedSession(null);
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

      const latest = readPersistedAuthSession();
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

        const persistedBeforeCommit = readPersistedAuthSession();
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
        persistIdentity(validated.identity);
        applyPersistedSession(validated.session);
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
      const latest = readPersistedAuthSession();
      if (!latest || latest.sessionId === loggedOutSessionId) {
        clearPersistedAuthSession();
        localStorage.removeItem(authUserKey);
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
    const pendingSession = readPersistedAuthSession();
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
        'Signed out on this browser, but server sign-out could not be confirmed. The session may still be active.',
        'AUTH_LOGOUT_UNCONFIRMED',
      );
    }
    if (cleanupFailed) throw new AuthRequestError(
      'Signed out locally, but saved credentials could not be cleared. Please retry sign-out.',
      'AUTH_LOGOUT_CLEANUP_PENDING',
    );
  };

  const syncCurrentIdentityProfile = (candidate: AuthIdentity): boolean => {
    const normalized = normalizeAuthIdentity(candidate);
    if (!normalized || !sessionUserId.value || normalized.id !== sessionUserId.value) {
      return false;
    }
    persistIdentity(normalized);
    return true;
  };

  applyPersistedSession(initialSession);

  const unsubscribe = subscribeToPersistedAuthChanges(reconcilePersistedAuthState);
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
    refreshAccessToken,
    syncCurrentIdentityProfile,
    reconcilePersistedAuthState,
    clearAuth,
    logout,
  };
});
