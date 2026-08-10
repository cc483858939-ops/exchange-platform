import type { PublicAuthor } from './User';

export type FeedTab = 'for-you' | 'latest';

export interface FeedPost {
  id: number;
  author: PublicAuthor;
  title: string;
  excerpt: string;
  coverImageUrl: string;
  createdAt: string;
  likeCount: number;
  commentCount: number;
}
