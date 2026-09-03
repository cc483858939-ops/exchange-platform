import type { PublicAuthor } from './User';

export interface PostMedia {
  type: 'image';
  url: string;
  position: number;
}

export type PostReference =
  | {
      id: number;
      deleted: true;
    }
  | {
      id: number;
      deleted: false;
      author: PublicAuthor;
      content: string;
      published_at: string;
      media: PostMedia[];
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
  media: PostMedia[];
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
