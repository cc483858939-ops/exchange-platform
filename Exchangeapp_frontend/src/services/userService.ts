import apiClient from '../axios';
import type { Article } from '../types/Article';
import type { PublicUser } from '../types/User';
import { normalizeResourceID } from './resourceId';

export type UserArticleQuery = {
  limit?: number;
  offset?: number;
};

export async function getUser(userId: number | string): Promise<PublicUser> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.get<PublicUser>(`/users/${id}`);
  return response.data;
}

export async function getUserArticles(
  userId: number | string,
  options: UserArticleQuery = {},
): Promise<Article[]> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.get<Article[]>(`/users/${id}/articles`, { params: options });
  return response.data;
}

export type UserFollowState = {
  user_id: number;
  following: boolean;
  follower_count: number;
  following_count: number;
};

export async function getUserFollowState(userId: number | string): Promise<UserFollowState> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.get<UserFollowState>(`/users/${id}/follow`);
  return response.data;
}

export async function followUser(userId: number | string): Promise<UserFollowState> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.put<UserFollowState>(`/users/${id}/follow`);
  return response.data;
}

export async function unfollowUser(userId: number | string): Promise<UserFollowState> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.delete<UserFollowState>(`/users/${id}/follow`);
  return response.data;
}