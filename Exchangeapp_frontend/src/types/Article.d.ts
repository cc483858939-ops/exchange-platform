import type { PublicAuthor } from './User';
export interface Article {
  CreatedAt: string;
  author: PublicAuthor;
  ID: number;
  title: string;
  preview: string;
  content: string;
  cover_image_url?: string;
  expired_at?: string;
}

export interface Like {
  likes: number;
  liked: boolean;
}
