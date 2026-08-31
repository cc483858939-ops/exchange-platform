import apiClient from '../axios';
import type { PublicAuthor } from '../types/User';
import type { Post, PostArticle, PostPageResponse } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export type CreatePostPayload = {
  content: string;
  reply_to_post_id?: number;
  quote_post_id?: number;
  article?: {
    title: string;
    preview: string;
    cover_image_url?: string;
  };
};

export type PostPageQuery = {
  limit?: number;
  cursor?: string;
};

export type FollowingActivityType = 'post' | 'repost';

export interface FollowingTimelineItem {
  activity_type: FollowingActivityType;
  activity_at: string;
  source_id: number;
  actor: PublicAuthor;
  post: Post;
}

export interface FollowingTimelineResponse {
  items: FollowingTimelineItem[];
  next_cursor: string | null;
}

export async function getFollowingTimeline(
  options: PostPageQuery = {},
): Promise<FollowingTimelineResponse> {
  const response = await apiClient.get<FollowingTimelineResponse>(
    '/feed/following',
    { params: options },
  );
  return response.data;
}

export async function getPostById(postID: number | string): Promise<Post> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.get<Post>('/posts/' + id);
  return response.data;
}

export async function deletePost(postID: number | string): Promise<void> {
  const id = normalizeResourceID(postID, 'post');
  await apiClient.delete('/posts/' + id);
}

export async function createPost(payload: CreatePostPayload): Promise<Post> {
  const response = await apiClient.post<Post>('/posts', payload);
  return response.data;
}

export async function uploadArticleCover(file: File): Promise<string> {
  const formData = new FormData();
  formData.append('image', file);
  const response = await apiClient.post<{ cover_image_url: string }>(
    '/uploads/article-cover',
    formData,
  );
  return response.data.cover_image_url;
}

export type { PostArticle, PostPageResponse };
