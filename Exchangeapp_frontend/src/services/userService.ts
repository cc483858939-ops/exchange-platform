import apiClient from '../axios';
import type { Article } from '../types/Article';
import type { PublicUser } from '../types/User';
import { normalizeResourceID } from './resourceId';

export type UserArticleQuery = {
  limit?: number;
  offset?: number;
};
export type UserConnectionItem = {
	user: PublicUser;
	following: boolean;
};

export type UserConnectionPage = {
	items: UserConnectionItem[];
	has_more: boolean;
};

export type UserConnectionQuery = {
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

export type UpdateUserProfilePayload = {
  display_name?: string;
  bio?: string;
  avatar_url?: string;
};

export async function updateUserProfile(
  userId: number | string,
  payload: UpdateUserProfilePayload,
): Promise<PublicUser> {
  const id = normalizeResourceID(userId, 'user');
  const response = await apiClient.patch<PublicUser>('/users/' + id, payload);
  return response.data;
}

export async function uploadProfileAvatar(file: File): Promise<string> {
  const data = new FormData();
  data.append('image', file);
  const response = await apiClient.post<{ avatar_url: string }>('/uploads/profile-avatar', data);
  return response.data.avatar_url;
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

export async function getUserFollowers(userId: number | string, options: UserConnectionQuery = {}): Promise<UserConnectionPage> {
	const id = normalizeResourceID(userId, 'user');
	const response = await apiClient.get<UserConnectionPage>(`/users/${id}/followers`, { params: options });
	return response.data;
}

export async function getUserFollowing(userId: number | string, options: UserConnectionQuery = {}): Promise<UserConnectionPage> {
	const id = normalizeResourceID(userId, 'user');
	const response = await apiClient.get<UserConnectionPage>(`/users/${id}/following`, { params: options });
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