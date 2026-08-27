import type { PublicAuthor } from './User';

export type FeedTab = 'for-you' | 'following';

export type FeedLikeStatus = 'unknown' | 'ready' | 'unavailable';

export type FeedRepostStatus = 'unknown' | 'ready' | 'unavailable';

export interface FeedRepostContext {
  actor: PublicAuthor;
}

export interface FeedPost {
  id: number;
  author: PublicAuthor;
  title: string;
  excerpt: string;
  coverImageUrl: string;
  createdAt: string;
  likeCount: number;
  commentCount: number;
  viewCount: number;
  liked: boolean;
  likeStatus: FeedLikeStatus;
  repostCount: number;
  reposted: boolean;
  repostStatus: FeedRepostStatus;
  repostContext?: FeedRepostContext;
}

export interface FeedLikeStateUpdate {
  articleId: number;
  likes: number;
  liked: boolean;
  status: FeedLikeStatus;
}

export interface FeedRepostStateUpdate {
  articleId: number;
  reposts: number;
  reposted: boolean;
  status: FeedRepostStatus;
}
