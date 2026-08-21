import apiClient from '../axios';
import type { Article } from '../types/Article';

export type LikedHistoryQuery = {
  limit?: number;
  cursor?: string;
};

export type LikedHistoryResponse = {
  items: Article[];
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
