import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';
export async function getFollowingTimeline(options = {}) {
    const response = await apiClient.get('/feed/following', { params: options });
    return response.data;
}
export async function getPostById(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.get('/posts/' + id);
    return response.data;
}
export async function deletePost(postID) {
    const id = normalizeResourceID(postID, 'post');
    await apiClient.delete('/posts/' + id);
}
export async function createPost(payload) {
    const response = await apiClient.post('/posts', payload);
    return response.data;
}
export async function uploadPostMedia(file) {
    const formData = new FormData();
    formData.append('image', file);
    const response = await apiClient.post('/uploads/post-media', formData);
    return response.data.media_url;
}
