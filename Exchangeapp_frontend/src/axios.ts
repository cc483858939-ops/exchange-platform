import axios from 'axios';
import type { AxiosError, InternalAxiosRequestConfig } from 'axios';
import { apiBaseUrl } from './api';
import { useAuthStore } from './store/auth';

type RetryableRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
};

const instance = axios.create({
  baseURL: apiBaseUrl,
});

let refreshPromise: Promise<string> | null = null;

const authEndpointPaths = ['/auth/login', '/auth/register', '/auth/refresh'];

const isAuthEndpoint = (url?: string) => {
  if (!url) {
    return false;
  }
  return authEndpointPaths.some((path) => url.includes(path));
};

instance.interceptors.request.use(config => {
  const authStore = useAuthStore();
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
      isAuthEndpoint(originalRequest.url) ||
      !authStore.refreshToken
    ) {
      return Promise.reject(error);
    }

    originalRequest._retry = true;

    try {
      if (!refreshPromise) {
        refreshPromise = authStore.refreshAccessToken().finally(() => {
          refreshPromise = null;
        });
      }
      const accessToken = await refreshPromise;
      originalRequest.headers.Authorization = accessToken;
      return instance(originalRequest);
    } catch (refreshError) {
      authStore.clearAuth();
      return Promise.reject(refreshError);
    }
  }
);

export default instance;
