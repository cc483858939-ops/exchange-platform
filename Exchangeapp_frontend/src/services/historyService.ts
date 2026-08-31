import apiClient from '../axios';
import type { Post } from '../types/Post';

export type LikedHistoryQuery = {
  limit?: number;
  cursor?: string;
};

export type LikedHistoryResponse = {
  items: Post[];
  next_cursor: string | null;
};

export async function getLikedHistory(
  query: LikedHistoryQuery = {},
): Promise<LikedHistoryResponse> {
  const response = await apiClient.get<LikedHistoryResponse>(
    '/me/history/likes',
    { params: query },
  );
  return response.data;
}
