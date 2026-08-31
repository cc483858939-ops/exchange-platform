import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';

export interface PostRepostState {
  reposts: number;
  reposted: boolean;
}

export interface PostBatchRepostStateItem {
  post_id: number;
  reposts: number;
  reposted: boolean;
}

export interface PostBatchRepostStatesResponse {
  items: PostBatchRepostStateItem[];
  unavailable_post_ids: number[];
}

const batchRepostStateLimit = 100;

export async function getPostRepostState(postID: number | string): Promise<PostRepostState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.get<PostRepostState>(`/posts/${id}/repost`);
  return response.data;
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

export async function getPostRepostStates(postIDs: number[]): Promise<PostBatchRepostStatesResponse> {
  const uniqueIds = Array.from(new Set(postIDs));
  if (uniqueIds.length === 0) {
    return { items: [], unavailable_post_ids: [] };
  }

  const result: PostBatchRepostStatesResponse = {
    items: [],
    unavailable_post_ids: [],
  };
  for (let offset = 0; offset < uniqueIds.length; offset += batchRepostStateLimit) {
    const chunk = uniqueIds.slice(offset, offset + batchRepostStateLimit);
    const response = await apiClient.post<PostBatchRepostStatesResponse>(
      '/posts/repost-states',
      { post_ids: chunk },
    );
    result.items.push(...(response.data.items ?? []));
    result.unavailable_post_ids.push(...(response.data.unavailable_post_ids ?? []));
  }
  return result;
}
