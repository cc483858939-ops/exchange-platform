import apiClient from '../axios';
import type { Article } from '../types/Article';
import { normalizeResourceID } from './resourceId';

export async function getArticles(): Promise<Article[]> {
  const response = await apiClient.get<Article[]>('/articles');
  return response.data;
}

export async function getArticleById(articleId: number | string): Promise<Article> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.get<Article>(`/articles/${id}`);
  return response.data;
}
