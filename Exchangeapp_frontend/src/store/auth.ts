import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import axios from 'axios';
import { apiBaseUrl } from '../api';
import { decodeAuthIdentity, normalizeAuthIdentity } from '../utils/authIdentity';
import type { AuthIdentity } from '../utils/authIdentity';

const authClient = axios.create({
  baseURL: apiBaseUrl,
});

const accessTokenKey = 'token';
const refreshTokenKey = 'refresh_token';
const authUserKey = 'auth_user';

type AuthResponse = {
  access_token: string;
  refresh_token: string;
  token_type: 'Bearer';
  expires_in: number;
  refresh_expires_in: number;
  user: unknown;
};

type AuthErrorResponse = {
  error?: string;
  message?: string;
};

const getAuthErrorMessage = (error: unknown, fallback: string) => {
  const data = (error as { response?: { data?: AuthErrorResponse } }).response?.data;
  return data?.message || data?.error || fallback;
};

const asAuthorizationHeader = (rawToken: string | null): string | null => {
  const trimmed = rawToken?.trim();
  return trimmed ? `Bearer ${trimmed.replace(/^Bearer\s+/i, '')}` : null;
};

const loadStoredIdentity = (): AuthIdentity | null => {
  try {
    const raw = localStorage.getItem(authUserKey);
    if (!raw) return null;
    return normalizeAuthIdentity(JSON.parse(raw));
  } catch {
    return null;
  }
};

export const useAuthStore = defineStore('auth', () => {
  const storedAccessToken = localStorage.getItem(accessTokenKey);
  const token = ref<string | null>(asAuthorizationHeader(storedAccessToken));
  const refreshToken = ref<string | null>(localStorage.getItem(refreshTokenKey));
  const identity = ref<AuthIdentity | null>(loadStoredIdentity() || decodeAuthIdentity(storedAccessToken));

  const isAuthenticated = computed(() => Boolean(token.value && refreshToken.value && identity.value));
  const currentIdentity = computed<AuthIdentity | null>(() => identity.value);

  const persistIdentity = (next: AuthIdentity | null) => {
    identity.value = next;
    if (next) {
      localStorage.setItem(authUserKey, JSON.stringify(next));
    } else {
      localStorage.removeItem(authUserKey);
    }
  };

  const setTokens = (response: AuthResponse) => {
    const rawAccessToken = typeof response.access_token === 'string'
      ? response.access_token.trim().replace(/^Bearer\s+/i, '')
      : '';
    const normalizedIdentity = normalizeAuthIdentity(response.user);
    if (!rawAccessToken || typeof response.refresh_token !== 'string' || !response.refresh_token || response.token_type !== 'Bearer' || !normalizedIdentity) {
      throw new Error('Invalid authentication response');
    }
    token.value = asAuthorizationHeader(rawAccessToken);
    refreshToken.value = response.refresh_token;
    localStorage.setItem(accessTokenKey, rawAccessToken);
    localStorage.setItem(refreshTokenKey, response.refresh_token);
    persistIdentity(normalizedIdentity);
  };

  const clearAuth = () => {
    token.value = null;
    refreshToken.value = null;
    localStorage.removeItem(accessTokenKey);
    localStorage.removeItem(refreshTokenKey);
    persistIdentity(null);
  };

  const syncCurrentIdentityProfile = (candidate: AuthIdentity): boolean => {
    const normalized = normalizeAuthIdentity(candidate);
    if (!normalized || !identity.value || normalized.id !== identity.value.id) {
      return false;
    }
    persistIdentity(normalized);
    return true;
  };

  const login = async (username: string, password: string) => {
    try {
      const response = await authClient.post<AuthResponse>('/auth/login', { username, password });
      setTokens(response.data);
    } catch (error) {
      throw new Error(getAuthErrorMessage(error, '登录失败，请稍后重试'));
    }
  };

  const register = async (username: string, password: string) => {
    try {
      const response = await authClient.post<AuthResponse>('/auth/register', { username, password });
      setTokens(response.data);
    } catch (error) {
      throw new Error(getAuthErrorMessage(error, '注册失败，请稍后重试'));
    }
  };

  const refreshAccessToken = async () => {
    if (!refreshToken.value) {
      clearAuth();
      throw new Error('Missing refresh token');
    }
    try {
      const response = await authClient.post<AuthResponse>('/auth/refresh', {
        refresh_token: refreshToken.value,
      });
      setTokens(response.data);
      if (!token.value) {
        throw new Error('Missing access token');
      }
      return token.value;
    } catch (error) {
      clearAuth();
      throw error;
    }
  };

  const logout = () => {
    clearAuth();
  };

  return {
    token,
    refreshToken,
    isAuthenticated,
    currentIdentity,
    login,
    register,
    refreshAccessToken,
    syncCurrentIdentityProfile,
    clearAuth,
    logout,
  };
});
