import apiClient from '../axios';
import type { PostLikeState } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export interface PostBatchLikeStateItem {
  post_id: number;
  likes: number;
  liked: boolean;
}

export interface PostBatchLikeStatesResponse {
  items: PostBatchLikeStateItem[];
  unavailable_post_ids: number[];
}

const batchLikeStateLimit = 100;

export async function getPostLikeState(postID: number | string): Promise<PostLikeState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.get<PostLikeState>(`/posts/${id}/like`);
  return response.data;
}

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

export async function getPostLikeStates(postIDs: number[]): Promise<PostBatchLikeStatesResponse> {
  const uniqueIds = Array.from(new Set(postIDs));
  if (uniqueIds.length === 0) {
    return {
      items: [],
      unavailable_post_ids: [],
    };
  }

  const result: PostBatchLikeStatesResponse = {
    items: [],
    unavailable_post_ids: [],
  };

  for (let offset = 0; offset < uniqueIds.length; offset += batchLikeStateLimit) {
    const chunk = uniqueIds.slice(offset, offset + batchLikeStateLimit);
    const response = await apiClient.post<PostBatchLikeStatesResponse>(
      '/posts/like-states',
      { post_ids: chunk },
    );
    result.items.push(...(response.data.items ?? []));
    result.unavailable_post_ids.push(...(response.data.unavailable_post_ids ?? []));
  }

  return result;
}
