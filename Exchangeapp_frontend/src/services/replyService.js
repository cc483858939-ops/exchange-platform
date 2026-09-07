import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';
export async function getPostReplies(postID, options = {}) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.get(`/posts/${id}/replies`, { params: options });
    return response.data;
}
export async function createPostReply(postID, content) {
    const id = normalizeResourceID(postID, 'post');
    const response = await apiClient.post('/posts', {
        content,
        reply_to_post_id: Number(id),
    });
    return response.data;
}
export async function deletePostReply(replyID) {
    const id = normalizeResourceID(replyID, 'post');
    await apiClient.delete(`/posts/${id}`);
}
