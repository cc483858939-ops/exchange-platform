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
  excludePostIds?: number[];
} = {}): Promise<RecommendationPageResponse> {
  const { limit, excludePostIds } = options;
  if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
    throw new Error('Invalid recommendation limit');
  }
  if (excludePostIds !== undefined && (
    excludePostIds.length > 200
    || excludePostIds.some(id => !Number.isSafeInteger(id) || id <= 0)
  )) {
    throw new Error('Invalid recommendation exclusions');
  }

  const params: Record<string, number | string> = {};
  if (limit !== undefined) params.limit = limit;
  if (excludePostIds && excludePostIds.length > 0) {
    params.exclude_post_ids = Array.from(new Set(excludePostIds)).join(',');
  }
  const response = await apiClient.get<RecommendationPageResponse>('/public/recommendations/posts', {
    params: Object.keys(params).length > 0 ? params : undefined,
  });
  return response.data;
}
