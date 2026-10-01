import axios from 'axios';
import type { AxiosError, InternalAxiosRequestConfig } from 'axios';
import { apiBaseUrl } from './api';
import { API_REQUEST_TIMEOUT_MS } from './utils/requestTimeout';
import { AuthSessionChangedError, useAuthStore } from './store/auth';
import type { AuthRequestBinding } from './auth/authRequestBinding';

type RetryableRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
  _authSessionVersion?: number;
  _authBinding?: AuthRequestBinding;
};

type RefreshState = {
  sessionVersion: number;
  promise: Promise<string>;
};

const instance = axios.create({
  baseURL: apiBaseUrl,
  timeout: API_REQUEST_TIMEOUT_MS,
});

let refreshState: RefreshState | null = null;

const authEndpointPaths = ['/auth/login', '/auth/register', '/auth/refresh'];

const isAuthEndpoint = (url?: string) => {
  if (!url) {
    return false;
  }
  return authEndpointPaths.some((path) => url.includes(path));
};

const readAuthorizationHeader = (config: RetryableRequestConfig): string | null => {
  const headers = config.headers;
  const value = typeof headers.get === 'function'
    ? headers.get('Authorization')
    : headers.Authorization;
  return typeof value === 'string' ? value.trim() : null;
};

instance.interceptors.request.use(config => {
  const authStore = useAuthStore();
  const authConfig = config as RetryableRequestConfig;

  const binding = authConfig._authBinding;
  if (binding) {
    if (
      !authStore.matchesRequestAuthBinding(binding)
      || (authConfig._authSessionVersion !== undefined
        && authConfig._authSessionVersion !== binding.sessionVersion)
    ) {
      return Promise.reject(new AuthSessionChangedError());
    }
    authConfig._authSessionVersion = binding.sessionVersion;
  } else if (authConfig._authSessionVersion === undefined) {
    authConfig._authSessionVersion = authStore.sessionVersion;
  } else if (authConfig._authSessionVersion !== authStore.sessionVersion) {
    return Promise.reject(new AuthSessionChangedError());
  }

  if (authStore.token) {
    config.headers.Authorization = authStore.token;
  }
  return config;
});

instance.interceptors.response.use(
  response => response,
  async (error: AxiosError) => {
    const authStore = useAuthStore();
    const originalRequest = error.config as RetryableRequestConfig | undefined;

    if (
      error.response?.status !== 401 ||
      !originalRequest ||
      originalRequest._retry ||
      isAuthEndpoint(originalRequest.url)
    ) {
      return Promise.reject(error);
    }

    authStore.reconcilePersistedAuthState();

    const requestVersion = originalRequest._authSessionVersion;
    if (requestVersion === undefined || requestVersion !== authStore.sessionVersion) {
      return Promise.reject(error);
    }
    if (originalRequest._authBinding && !authStore.matchesRequestAuthBinding(originalRequest._authBinding)) {
      return Promise.reject(new AuthSessionChangedError());
    }

    if (!authStore.refreshToken) {
      return Promise.reject(error);
    }

    originalRequest._retry = true;

    const currentAuthorization = authStore.token?.trim() ?? '';
    const requestAuthorization = readAuthorizationHeader(originalRequest) ?? '';
    if (currentAuthorization && currentAuthorization !== requestAuthorization) {
      originalRequest.headers.Authorization = currentAuthorization;
      return instance(originalRequest);
    }

    try {
      if (!refreshState || refreshState.sessionVersion !== requestVersion) {
        const state: RefreshState = {
          sessionVersion: requestVersion,
          promise: authStore.refreshAccessToken(),
        };
        refreshState = state;
        state.promise = state.promise.finally(() => {
          if (refreshState === state) {
            refreshState = null;
          }
        });
      }

      const activeRefreshState = refreshState;
      if (!activeRefreshState || activeRefreshState.sessionVersion !== requestVersion) {
        return Promise.reject(new AuthSessionChangedError());
      }

      const accessToken = await activeRefreshState.promise;
      if (
        activeRefreshState.sessionVersion !== requestVersion ||
        authStore.sessionVersion !== requestVersion ||
        (originalRequest._authBinding
          && !authStore.matchesRequestAuthBinding(originalRequest._authBinding))
      ) {
        return Promise.reject(new AuthSessionChangedError());
      }

      originalRequest.headers.Authorization = accessToken;
      return instance(originalRequest);
    } catch (refreshError) {
      return Promise.reject(refreshError);
    }
  }
);

export default instance;
