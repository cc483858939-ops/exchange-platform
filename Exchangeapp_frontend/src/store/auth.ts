import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import axios from 'axios';
import { apiBaseUrl } from '../api';
import { decodeAuthIdentity } from '../utils/authIdentity';
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
  user: AuthIdentity;
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
    const parsed = JSON.parse(raw) as Partial<AuthIdentity>;
    if (!Number.isSafeInteger(parsed.id) || Number(parsed.id) <= 0 || typeof parsed.username !== 'string') {
      return null;
    }
    return { id: Number(parsed.id), username: parsed.username };
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

  const setTokens = (response: AuthResponse) => {
    const rawAccessToken = response.access_token.trim().replace(/^Bearer\s+/i, '');
    if (!rawAccessToken || !response.refresh_token || response.token_type !== 'Bearer' || !response.user?.id) {
      throw new Error('Invalid authentication response');
    }
    token.value = asAuthorizationHeader(rawAccessToken);
    refreshToken.value = response.refresh_token;
    identity.value = response.user;
    localStorage.setItem(accessTokenKey, rawAccessToken);
    localStorage.setItem(refreshTokenKey, response.refresh_token);
    localStorage.setItem(authUserKey, JSON.stringify(response.user));
  };

  const clearAuth = () => {
    token.value = null;
    refreshToken.value = null;
    identity.value = null;
    localStorage.removeItem(accessTokenKey);
    localStorage.removeItem(refreshTokenKey);
    localStorage.removeItem(authUserKey);
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
    clearAuth,
    logout,
  };
});
