import axios from 'axios';
import { apiBaseUrl } from './api';
import { useAuthStore } from './store/auth';
const instance = axios.create({
    baseURL: apiBaseUrl,
});
let refreshPromise = null;
const authEndpointPaths = ['/auth/login', '/auth/register', '/auth/refresh'];
const isAuthEndpoint = (url) => {
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
instance.interceptors.response.use(response => response, async (error) => {
    const authStore = useAuthStore();
    const originalRequest = error.config;
    if (error.response?.status !== 401 ||
        !originalRequest ||
        originalRequest._retry ||
        isAuthEndpoint(originalRequest.url) ||
        !authStore.refreshToken) {
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
    }
    catch (refreshError) {
        authStore.clearAuth();
        return Promise.reject(refreshError);
    }
});
export default instance;
