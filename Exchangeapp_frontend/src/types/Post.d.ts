import type { PublicAuthor } from './User';

export interface PostArticle {
  title: string;
  preview: string;
  cover_image_url: string;
  publication_state: 'published';
  published_at: string;
  expired_at: string | null;
}

export type PostReference =
  | {
      id: number;
      deleted: true;
    }
  | {
      id: number;
      author: PublicAuthor;
      content: string;
      published_at: string;
      article: Pick<PostArticle, 'title' | 'preview' | 'cover_image_url'> | null;
      deleted: false;
    };

export interface Post {
  id: number;
  created_at: string;
  updated_at: string;
  published_at: string;
  author: PublicAuthor;
  content: string;
  conversation_id: number;
  reply_to_post_id: number | null;
  quote_post_id: number | null;
  reply_to_post: PostReference | null;
  quote_post: PostReference | null;
  visibility: 'public';
  article: PostArticle | null;
  like_count: number;
  reply_count: number;
  view_count: number;
  deleted: false;
}

export interface PostLikeState {
  likes: number;
  liked: boolean;
}

export interface PostPageResponse {
  items: Post[];
  next_cursor: string | null;
}

export interface PostReplyPageResponse {
  items: Post[];
  next_cursor: string | null;
}
