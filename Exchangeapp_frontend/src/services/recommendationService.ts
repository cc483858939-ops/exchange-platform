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
