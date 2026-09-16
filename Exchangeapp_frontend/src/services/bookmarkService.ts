import apiClient from '../axios';
import type { Post } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export interface PostBookmarkState {
  post_id: number;
  bookmarked: boolean;
}

export interface PostBatchBookmarkStateItem {
  post_id: number;
  bookmarked: boolean;
}

export interface PostBatchBookmarkStatesResponse {
  items: PostBatchBookmarkStateItem[];
  unavailable_post_ids: number[];
}

export type BookmarkHistoryQuery = {
  limit?: number;
  cursor?: string;
};

export type BookmarkHistoryResponse = {
  items: Post[];
  next_cursor: string | null;
};

const batchBookmarkStateLimit = 100;

export async function bookmarkPost(postID: number | string): Promise<PostBookmarkState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.put<PostBookmarkState>(`/posts/${id}/bookmark`);
  return response.data;
}

export async function unbookmarkPost(postID: number | string): Promise<PostBookmarkState> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.delete<PostBookmarkState>(`/posts/${id}/bookmark`);
  return response.data;
}

export async function getPostBookmarkStates(
  postIDs: number[],
): Promise<PostBatchBookmarkStatesResponse> {
  const uniqueIds = Array.from(new Set(postIDs));
  if (uniqueIds.length === 0) {
    return { items: [], unavailable_post_ids: [] };
  }

  const result: PostBatchBookmarkStatesResponse = {
    items: [],
    unavailable_post_ids: [],
  };
  for (let offset = 0; offset < uniqueIds.length; offset += batchBookmarkStateLimit) {
    const chunk = uniqueIds.slice(offset, offset + batchBookmarkStateLimit);
    const response = await apiClient.post<PostBatchBookmarkStatesResponse>(
      '/posts/bookmark-states',
      { post_ids: chunk },
    );
    result.items.push(...(response.data.items ?? []));
    result.unavailable_post_ids.push(...(response.data.unavailable_post_ids ?? []));
  }
  return result;
}

export async function getBookmarks(
  query: BookmarkHistoryQuery = {},
): Promise<BookmarkHistoryResponse> {
  const response = await apiClient.get<BookmarkHistoryResponse>('/me/bookmarks', {
    params: query,
  });
  return response.data;
}
