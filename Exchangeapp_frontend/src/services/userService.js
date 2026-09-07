import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';
export async function getUser(userId) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.get(`/users/${id}`);
    return response.data;
}
export async function getUserPosts(userId, options = {}) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.get(`/users/${id}/posts`, { params: options });
    return response.data;
}
export async function updateUserProfile(userId, payload) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.patch('/users/' + id, payload);
    return response.data;
}
export async function uploadProfileAvatar(file) {
    const data = new FormData();
    data.append('image', file);
    const response = await apiClient.post('/uploads/profile-avatar', data);
    return response.data.avatar_url;
}
export async function getUserFollowState(userId) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.get(`/users/${id}/follow`);
    return response.data;
}
export async function getUserFollowers(userId, options = {}) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.get(`/users/${id}/followers`, { params: options });
    return response.data;
}
export async function getUserFollowing(userId, options = {}) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.get(`/users/${id}/following`, { params: options });
    return response.data;
}
export async function searchUsers(options) {
    const response = await apiClient.get('/users/search', { params: options });
    return response.data;
}
export async function followUser(userId) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.put(`/users/${id}/follow`);
    return response.data;
}
export async function unfollowUser(userId) {
    const id = normalizeResourceID(userId, 'user');
    const response = await apiClient.delete(`/users/${id}/follow`);
    return response.data;
}
