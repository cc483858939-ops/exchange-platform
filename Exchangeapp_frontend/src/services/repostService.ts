import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';

export interface PostRepostState {
  reposts: number;
  reposted: boolean;
}

export async function repostPost(postID: number | string): Promise<PostRepostState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.put<PostRepostState>(`/posts/${id}/repost`);
  return response.data;
}

export async function undoRepostPost(postID: number | string): Promise<PostRepostState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.delete<PostRepostState>(`/posts/${id}/repost`);
  return response.data;
}
