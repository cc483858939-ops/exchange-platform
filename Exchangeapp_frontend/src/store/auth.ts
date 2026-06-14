import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import axios from '../axios';

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
  const token = ref<string | null>(localStorage.getItem('token'));

  const isAuthenticated = computed(() => !!token.value);

  const login = async (username: string, password: string) => {
    try {
      const response = await axios.post<AuthResponse>('/auth/login', { username, password });
      if (!response.data.access_token) {
        throw new Error('Missing access token');
      }
      token.value = response.data.access_token;
      localStorage.setItem('token', token.value || '');
    } catch (error) {
      throw new Error(getAuthErrorMessage(error, '登录失败，请稍后重试'));
    }
  };

  const register = async (username: string, password: string) => {
    try {
      const response = await axios.post<AuthResponse>('/auth/register', { username, password });
      if (!response.data.access_token) {
        throw new Error('Missing access token');
      }
      token.value = response.data.access_token;
      localStorage.setItem('token', token.value || '');
    } catch (error) {
      throw new Error(getAuthErrorMessage(error, '注册失败，请稍后重试'));
    }
  };

  const logout = () => {
    token.value = null;
    localStorage.removeItem('token');
  };

  return {
    token,
    isAuthenticated,
    login,
    register,
    logout
  };
});
