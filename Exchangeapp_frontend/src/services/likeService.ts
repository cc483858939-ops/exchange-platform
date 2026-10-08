import apiClient from '../axios';
import type { PostLikeState } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export async function likePost(postID: number | string): Promise<PostLikeState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.put<PostLikeState>(`/posts/${id}/like`);
  return response.data;
}

export async function unlikePost(postID: number | string): Promise<PostLikeState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.delete<PostLikeState>(`/posts/${id}/like`);
  return response.data;
}
