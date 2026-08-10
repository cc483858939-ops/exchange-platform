import type { PublicAuthor } from './User';

export type FeedTab = 'for-you' | 'latest';

export type FeedLikeStatus = 'unknown' | 'ready' | 'unavailable';

export interface FeedPost {
  id: number;
  author: PublicAuthor;
  title: string;
  excerpt: string;
  coverImageUrl: string;
  createdAt: string;
  likeCount: number;
  commentCount: number;
  liked: boolean;
  likeStatus: FeedLikeStatus;
}

export interface FeedLikeStateUpdate {
  articleId: number;
  likes: number;
  liked: boolean;
  status: FeedLikeStatus;
}