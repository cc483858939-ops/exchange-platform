import apiClient from '../axios';
import type { Post, PostReplyPageResponse } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export type ReplyQuery = {
  limit?: number;
  cursor?: string;
};

export async function getPostReplies(
  postID: number | string,
  options: ReplyQuery = {},
): Promise<PostReplyPageResponse> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.get<PostReplyPageResponse>(`/posts/${id}/replies`, { params: options });
  return response.data;
}

export async function createPostReply(
  postID: number | string,
  content: string,
): Promise<Post> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.post<Post>('/posts', {
    content,
    reply_to_post_id: Number(id),
  });
  return response.data;
}

export async function deletePostReply(replyID: number | string): Promise<void> {
  const id = normalizeResourceID(replyID, 'post');
  await apiClient.delete(`/posts/${id}`);
}
