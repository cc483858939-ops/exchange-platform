import apiClient from '../axios';
import type { Post } from '../types/Post';

export type PostSearchQuery = {
  q: string;
  author_id?: number;
  from?: string;
  to?: string;
  sort?: 'latest';
  limit?: number;
  cursor?: string;
};

export type PostSearchPage = {
  items: Post[];
  next_cursor: string | null;
};

export async function searchPosts(options: PostSearchQuery): Promise<PostSearchPage> {
  const response = await apiClient.get<PostSearchPage>('/posts/search', { params: options });
  return response.data;
}
