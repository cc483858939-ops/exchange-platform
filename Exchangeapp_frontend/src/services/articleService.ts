import apiClient from '../axios';
import type { Article } from '../types/Article';
import { normalizeResourceID } from './resourceId';

export type CreateArticlePayload = {
  title: string;
  preview: string;
  content: string;
  cover_image_url?: string;
};

type UploadArticleCoverResponse = {
  cover_image_url: string;
};

export type FollowingTimelineQuery = {
  limit?: number;
  cursor?: string;
};

export type FollowingTimelineResponse = {
  items: Article[];
  next_cursor: string | null;
};

export async function getArticles(): Promise<Article[]> {
  const response = await apiClient.get<Article[]>('/articles');
  return response.data;
}

export async function getFollowingTimeline(
  options: FollowingTimelineQuery = {},
): Promise<FollowingTimelineResponse> {
  const response = await apiClient.get<FollowingTimelineResponse>(
    '/feed/following',
    { params: options },
  );
  return response.data;
}

export async function getArticleById(articleId: number | string): Promise<Article> {
  const id = normalizeResourceID(articleId, 'article');
  const response = await apiClient.get<Article>('/articles/' + id);
  return response.data;
}

export async function uploadArticleCover(file: File): Promise<string> {
  const formData = new FormData();
  formData.append('image', file);
  const response = await apiClient.post<UploadArticleCoverResponse>(
    '/uploads/article-cover',
    formData,
  );
  return response.data.cover_image_url;
}

export async function createArticle(payload: CreateArticlePayload): Promise<Article> {
  const response = await apiClient.post<Article>('/articles', payload);
  return response.data;
}