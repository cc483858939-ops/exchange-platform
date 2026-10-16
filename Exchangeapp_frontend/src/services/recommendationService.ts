import apiClient from '../axios';
import type { RecommendedPost } from '../types/Recommendation';

export interface RecommendationPageResponse {
  items: RecommendedPost[];
  request_id: string;
  next_cursor: string | null;
  depleted: boolean;
}

export async function getPostRecommendations(limit?: number, cursor?: string | null): Promise<RecommendationPageResponse> {
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const params: Record<string, number | string> = {};
  if (limit !== undefined) params.limit = limit;
  if (cursor) params.cursor = cursor;
  const response = await apiClient.get<RecommendationPageResponse>('/recommendations/posts', {
    params: Object.keys(params).length > 0 ? params : undefined,
  });
  return response.data;
}

export async function getPublicPostRecommendations(options: {
  limit?: number;
  cursor?: string | null;
  guestSessionId?: string | null;
} = {}): Promise<RecommendationPageResponse> {
  const { limit, cursor, guestSessionId } = options;
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const params: Record<string, number | string> = {};
  if (limit !== undefined) params.limit = limit;
  if (cursor) params.cursor = cursor;
  const requestConfig: {
    params?: Record<string, number | string>;
    headers?: Record<string, string>;
  } = {
    params: Object.keys(params).length > 0 ? params : undefined,
  };
  if (guestSessionId) {
    requestConfig.headers = {
      'X-Guest-Recommendation-Session': guestSessionId,
    };
  }

  const response = await apiClient.get<RecommendationPageResponse>(
    '/public/recommendations/posts',
    requestConfig,
  );
  return response.data;
}
