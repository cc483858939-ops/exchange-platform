import type { PublicAuthor } from './User';

export interface Article {
  ID: number;

  CreatedAt: string;
  UpdatedAt: string;

  title: string;
  content: string;
  preview: string;
  cover_image_url: string;

  summary: string;
  tags: string[] | null;
  category: string;

  publication_state: string;
  analysis_state: string;
  analysis_version: string;

  published_at: string | null;
  expired_at: string | null;

  like_count: number;
  comment_count: number;
  view_count: number;
  like_sync_version: number;

  author: PublicAuthor;
}

export interface ArticleLikeState {
  likes: number;
  liked: boolean;
}

export type Like = ArticleLikeState;
