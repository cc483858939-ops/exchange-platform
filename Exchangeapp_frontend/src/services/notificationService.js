import apiClient from '../axios';
export async function getNotifications(query = {}) {
    const response = await apiClient.get('/me/notifications', { params: query });
    return response.data;
}
export async function getUnreadNotificationCount() {
    const response = await apiClient.get('/me/notifications/unread-count');
    return response.data.unread_count;
}
export async function markNotificationRead(notificationID) {
    await apiClient.put(`/me/notifications/${notificationID}/read`);
}
export async function markAllNotificationsRead() {
    await apiClient.put('/me/notifications/read-all');
}
