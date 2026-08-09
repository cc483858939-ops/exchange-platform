import apiClient from '../axios';
import type { RecommendedArticle } from '../types/Recommendation';

export async function getArticleRecommendations(limit?: number): Promise<RecommendedArticle[]> {
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }

  const response = await apiClient.get<RecommendedArticle[]>('/recommendations/articles', {
    params: limit === undefined ? undefined : { limit },
  });
  return response.data;
}
