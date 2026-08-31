import apiClient from '../axios';
import type { RecommendedPost } from '../types/Recommendation';

export async function getPostRecommendations(limit?: number): Promise<RecommendedPost[]> {
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const response = await apiClient.get<RecommendedPost[]>('/recommendations/posts', {
    params: limit === undefined ? undefined : { limit },
  });
  return response.data;
}
