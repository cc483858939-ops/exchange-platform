import apiClient from '../axios';
import type { ArticleLikeState } from '../types/Article';
import { normalizeResourceID } from './resourceId';

export interface ArticleBatchLikeStateItem {
  article_id: number;
  likes: number;
  liked: boolean;
}

export interface ArticleBatchLikeStatesResponse {
  items: ArticleBatchLikeStateItem[];
  unavailable_article_ids: number[];
}

const batchLikeStateLimit = 100;

export async function getArticleLikeState(articleId: number | string): Promise<ArticleLikeState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.get<ArticleLikeState>(`/articles/${id}/like`);
  return response.data;
}

export async function likeArticle(articleId: number | string): Promise<ArticleLikeState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.put<ArticleLikeState>(`/articles/${id}/like`);
  return response.data;
}

export async function unlikeArticle(articleId: number | string): Promise<ArticleLikeState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.delete<ArticleLikeState>(`/articles/${id}/like`);
  return response.data;
}

export async function getArticleLikeStates(articleIds: number[]): Promise<ArticleBatchLikeStatesResponse> {
  const uniqueIds = Array.from(new Set(articleIds));
  if (uniqueIds.length === 0) {
    return {
      items: [],
      unavailable_article_ids: [],
    };
  }

  const result: ArticleBatchLikeStatesResponse = {
    items: [],
    unavailable_article_ids: [],
  };

  for (let offset = 0; offset < uniqueIds.length; offset += batchLikeStateLimit) {
    const chunk = uniqueIds.slice(offset, offset + batchLikeStateLimit);
    const response = await apiClient.post<ArticleBatchLikeStatesResponse>(
      '/articles/like-states',
      { article_ids: chunk },
    );
    result.items.push(...(response.data.items ?? []));
    result.unavailable_article_ids.push(...(response.data.unavailable_article_ids ?? []));
  }

  return result;
}