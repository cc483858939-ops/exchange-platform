import apiClient from '../axios';
import type { ArticleComment, CommentPage } from '../types/Comment';
import { normalizeResourceID } from './resourceId';

export type CommentQuery = {
  limit?: number;
  cursor?: string;
};

export async function getArticleComments(
  articleId: number | string,
  options: CommentQuery = {},
): Promise<CommentPage> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.get<CommentPage>(`/articles/${id}/comments`, { params: options });
  return response.data;
}

export async function createArticleComment(
  articleId: number | string,
  content: string,
): Promise<ArticleComment> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.post<ArticleComment>(`/articles/${id}/comments`, { content });
  return response.data;
}

export async function deleteComment(commentId: number | string): Promise<void> {
  const id = normalizeResourceID(commentId, 'comment');
  await apiClient.delete(`/comments/${id}`);
}
