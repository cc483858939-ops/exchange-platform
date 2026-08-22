import apiClient from '../axios';
import type {
  NotificationPageResponse,
  UnreadNotificationCountResponse,
} from '../types/Notification';

export type NotificationQuery = {
  limit?: number;
  cursor?: string;
};

export async function getNotifications(
  query: NotificationQuery = {},
): Promise<NotificationPageResponse> {
  const response = await apiClient.get<NotificationPageResponse>('/me/notifications', { params: query });
  return response.data;
}

export async function getUnreadNotificationCount(): Promise<number> {
  const response = await apiClient.get<UnreadNotificationCountResponse>('/me/notifications/unread-count');
  return response.data.unread_count;
}

export async function markNotificationRead(notificationID: number): Promise<void> {
  await apiClient.put(`/me/notifications/${notificationID}/read`);
}

export async function markAllNotificationsRead(): Promise<void> {
  await apiClient.put('/me/notifications/read-all');
}
