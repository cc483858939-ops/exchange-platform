import apiClient from '../axios';
import { normalizeResourceID } from './resourceId';

export interface ArticleRepostState {
  reposts: number;
  reposted: boolean;
}

export interface ArticleBatchRepostStateItem {
  article_id: number;
  reposts: number;
  reposted: boolean;
}

export interface ArticleBatchRepostStatesResponse {
  items: ArticleBatchRepostStateItem[];
  unavailable_article_ids: number[];
}

const batchRepostStateLimit = 100;

export async function getArticleRepostState(articleId: number | string): Promise<ArticleRepostState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.get<ArticleRepostState>(`/articles/${id}/repost`);
  return response.data;
}

export async function repostArticle(articleId: number | string): Promise<ArticleRepostState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.put<ArticleRepostState>(`/articles/${id}/repost`);
  return response.data;
}

export async function undoRepostArticle(articleId: number | string): Promise<ArticleRepostState> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.delete<ArticleRepostState>(`/articles/${id}/repost`);
  return response.data;
}

export async function getArticleRepostStates(articleIds: number[]): Promise<ArticleBatchRepostStatesResponse> {
  const uniqueIds = Array.from(new Set(articleIds));
  if (uniqueIds.length === 0) {
    return { items: [], unavailable_article_ids: [] };
  }

  const result: ArticleBatchRepostStatesResponse = {
    items: [],
    unavailable_article_ids: [],
  };
  for (let offset = 0; offset < uniqueIds.length; offset += batchRepostStateLimit) {
    const chunk = uniqueIds.slice(offset, offset + batchRepostStateLimit);
    const response = await apiClient.post<ArticleBatchRepostStatesResponse>(
      '/articles/repost-states',
      { article_ids: chunk },
    );
    result.items.push(...(response.data.items ?? []));
    result.unavailable_article_ids.push(...(response.data.unavailable_article_ids ?? []));
  }
  return result;
}
