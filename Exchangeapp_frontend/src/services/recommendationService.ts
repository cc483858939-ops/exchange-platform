import apiClient from '../axios';
import type { RecommendedPost } from '../types/Recommendation';

export interface RecommendationPageResponse {
  items: RecommendedPost[];
  request_id: string;
  depleted: boolean;
}

export async function getPostRecommendations(limit?: number): Promise<RecommendationPageResponse> {
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const response = await apiClient.get<RecommendationPageResponse>('/recommendations/posts', {
    params: limit === undefined ? undefined : { limit },
  });
  return response.data;
}

export async function getPublicPostRecommendations(options: {
  limit?: number;
  guestSessionId?: string | null;
} = {}): Promise<RecommendationPageResponse> {
  const { limit, guestSessionId } = options;
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const params: Record<string, number | string> = {};
  if (limit !== undefined) params.limit = limit;
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
