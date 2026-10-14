import apiClient from '../axios';
import type { PublicAuthor } from '../types/User';
import { normalizeResourceID } from './resourceId';

export type UserMuteItem = {
  user: Pick<PublicAuthor, 'id' | 'username' | 'display_name' | 'avatar_url'>;
  created_at: string;
};

export type UserMutePage = {
  items: UserMuteItem[];
  next_cursor: string | null;
};

export type UserMuteQuery = {
  limit?: number;
  cursor?: string;
};

export async function muteUser(userID: number | string): Promise<void> {
  const id = normalizeResourceID(userID, 'user');
  await apiClient.put(`/users/${id}/mute`);
}

export async function unmuteUser(userID: number | string): Promise<void> {
  const id = normalizeResourceID(userID, 'user');
  await apiClient.delete(`/users/${id}/mute`);
}

export async function getMyMutedUsers(query: UserMuteQuery = {}): Promise<UserMutePage> {
  const response = await apiClient.get<UserMutePage>('/me/mutes', { params: query });
  return response.data;
}
