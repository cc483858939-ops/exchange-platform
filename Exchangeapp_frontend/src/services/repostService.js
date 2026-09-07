import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';
const batchRepostStateLimit = 100;
export async function getPostRepostState(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.get(`/posts/${id}/repost`);
    return response.data;
}
export async function repostPost(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.put(`/posts/${id}/repost`);
    return response.data;
}
export async function undoRepostPost(postID) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.delete(`/posts/${id}/repost`);
    return response.data;
}
export async function getPostRepostStates(postIDs) {
    const uniqueIds = Array.from(new Set(postIDs));
    if (uniqueIds.length === 0) {
        return { items: [], unavailable_post_ids: [] };
    }
    const result = {
        items: [],
        unavailable_post_ids: [],
    };
    for (let offset = 0; offset < uniqueIds.length; offset += batchRepostStateLimit) {
        const chunk = uniqueIds.slice(offset, offset + batchRepostStateLimit);
        const response = await apiClient.post('/posts/repost-states', { post_ids: chunk });
        result.items.push(...(response.data.items ?? []));
        result.unavailable_post_ids.push(...(response.data.unavailable_post_ids ?? []));
    }
    return result;
}
