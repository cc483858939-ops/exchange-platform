import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import axios from 'axios';
import { apiBaseUrl } from '../api';

const authClient = axios.create({
  baseURL: apiBaseUrl,
});

const accessTokenKey = 'token';
const refreshTokenKey = 'refresh_token';

type AuthResponse = {
  access_token: string;
  refresh_token: string;
};

type AuthErrorResponse = {
  error?: string;
};

const getAuthErrorMessage = (error: unknown, fallback: string) => {
  const data = (error as { response?: { data?: AuthErrorResponse } }).response?.data;
  return data?.error || fallback;
};

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem(accessTokenKey));
  const refreshToken = ref<string | null>(localStorage.getItem(refreshTokenKey));

  const isAuthenticated = computed(() => !!token.value);

  const setTokens = (accessToken: string, nextRefreshToken: string) => {
    token.value = accessToken;
    refreshToken.value = nextRefreshToken;
    localStorage.setItem(accessTokenKey, accessToken);
    localStorage.setItem(refreshTokenKey, nextRefreshToken);
  };

  const clearAuth = () => {
    token.value = null;
    refreshToken.value = null;
    localStorage.removeItem(accessTokenKey);
    localStorage.removeItem(refreshTokenKey);
  };

  const login = async (username: string, password: string) => {
    try {
      const response = await authClient.post<AuthResponse>('/auth/login', { username, password });
      if (!response.data.access_token || !response.data.refresh_token) {
        throw new Error('Missing token');
      }
      setTokens(response.data.access_token, response.data.refresh_token);
    } catch (error) {
      throw new Error(getAuthErrorMessage(error, '登录失败，请稍后重试'));
    }
  };

  const register = async (username: string, password: string) => {
    try {
      const response = await authClient.post<AuthResponse>('/auth/register', { username, password });
      if (!response.data.access_token || !response.data.refresh_token) {
        throw new Error('Missing token');
      }
      setTokens(response.data.access_token, response.data.refresh_token);
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
      if (!response.data.access_token || !response.data.refresh_token) {
        throw new Error('Missing token');
      }
      setTokens(response.data.access_token, response.data.refresh_token);
      return response.data.access_token;
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
    login,
    register,
    refreshAccessToken,
    clearAuth,
    logout
  };
});
