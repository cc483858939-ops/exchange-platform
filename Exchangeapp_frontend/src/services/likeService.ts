import apiClient from '../axios';
import type { ArticleLikeState } from '../types/Article';
import { normalizeResourceID } from './resourceId';

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
