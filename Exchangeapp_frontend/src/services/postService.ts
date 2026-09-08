import apiClient from '../axios';
import type { PublicAuthor } from '../types/User';
import type { Post } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export type CreatePostMediaPayload = {
  type: 'image';
  url: string;
};

export type CreatePostPayload = {
  content: string;
  reply_to_post_id?: number;
  quote_post_id?: number;
  media?: CreatePostMediaPayload[];
};

export type TimelineQuery = {
  limit?: number;
  cursor?: string;
};

export type TimelineActivityType = 'post' | 'repost';

export interface TimelineItem {
  activity_type: TimelineActivityType;
  activity_at: string;
  source_id: number;
  actor: PublicAuthor;
  post: Post;
}

export interface TimelineResponse {
  items: TimelineItem[];
  next_cursor: string | null;
}

export async function getFollowingTimeline(
  options: TimelineQuery = {},
): Promise<TimelineResponse> {
  const response = await apiClient.get<TimelineResponse>(
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

export async function uploadPostMedia(file: File): Promise<string> {
  const formData = new FormData();
  formData.append('image', file);
  const response = await apiClient.post<{ media_url: string }>(
    '/uploads/post-media',
    formData,
  );
  return response.data.media_url;
}
