import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';
const batchLikeStateLimit = 100;
export async function getPostLikeState(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.get(`/posts/${id}/like`);
    return response.data;
}
export async function likePost(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.put(`/posts/${id}/like`);
    return response.data;
}
export async function unlikePost(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.delete(`/posts/${id}/like`);
    return response.data;
}
export async function getPostLikeStates(postIDs) {
    const uniqueIds = Array.from(new Set(postIDs));
    if (uniqueIds.length === 0) {
        return {
            items: [],
            unavailable_post_ids: [],
        };
    }
    const result = {
        items: [],
        unavailable_post_ids: [],
    };
    for (let offset = 0; offset < uniqueIds.length; offset += batchLikeStateLimit) {
        const chunk = uniqueIds.slice(offset, offset + batchLikeStateLimit);
        const response = await apiClient.post('/posts/like-states', { post_ids: chunk });
        result.items.push(...(response.data.items ?? []));
        result.unavailable_post_ids.push(...(response.data.unavailable_post_ids ?? []));
    }
    return result;
}
