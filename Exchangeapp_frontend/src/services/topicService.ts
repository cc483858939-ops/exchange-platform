import apiClient from '../axios';
import type { Post } from '../types/Post';

export interface TopicSummary {
  slug: string;
  label: string;
  description: string;
}

export interface TopicListResponse {
  items: TopicSummary[];
}

export interface TopicPostsResponse {
  topic: TopicSummary;
  items: Post[];
  next_cursor: string | null;
}

export interface TopicPostsQuery {
  limit?: number;
  cursor?: string;
}

export async function getTopics(): Promise<TopicListResponse> {
  const response = await apiClient.get<TopicListResponse>('/topics');
  return response.data;
}

export async function getTopicPosts(
  slug: string,
  query: TopicPostsQuery = {},
): Promise<TopicPostsResponse> {
  const response = await apiClient.get<TopicPostsResponse>(
    `/topics/${encodeURIComponent(slug)}/posts`,
    { params: query },
  );
  return response.data;
}
