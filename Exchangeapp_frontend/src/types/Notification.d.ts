export type NotificationType = 'post_liked' | 'post_replied' | 'user_followed';

export interface NotificationActor {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
}

export interface Notification {
  id: number;
  type: NotificationType;
  actor: NotificationActor;
  article_id: number | null;
  comment_id: number | null;
  activity_at: string;
  read: boolean;
}

export interface NotificationPageResponse {
  items: Notification[];
  next_cursor: string | null;
}

export interface UnreadNotificationCountResponse {
  unread_count: number;
}
