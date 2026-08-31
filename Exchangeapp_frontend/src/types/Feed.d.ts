import type { PublicAuthor } from './User';
import type { Post, PostArticle, PostReference } from './Post';

export type FeedTab = 'for-you' | 'following';

export type FeedLikeStatus = 'unknown' | 'ready' | 'unavailable';

export type FeedRepostStatus = 'unknown' | 'ready' | 'unavailable';

export interface FeedRepostContext {
  actor: PublicAuthor;
}

export interface FeedPost {
  id: number;
  content?: Post['content'];
  article?: PostArticle | null;
  quotePost?: PostReference | null;
  replyToPost?: PostReference | null;
  author: PublicAuthor;
  title: string;
  excerpt: string;
  coverImageUrl: string;
  createdAt: string;
  likeCount: number;
  replyCount: number;
  viewCount: number;
  liked: boolean;
  likeStatus: FeedLikeStatus;
  repostCount: number;
  reposted: boolean;
  repostStatus: FeedRepostStatus;
  repostContext?: FeedRepostContext;
}

export interface FeedLikeStateUpdate {
  postId: number;
  likes: number;
  liked: boolean;
  status: FeedLikeStatus;
}

export interface FeedRepostStateUpdate {
  postId: number;
  reposts: number;
  reposted: boolean;
  status: FeedRepostStatus;
}
