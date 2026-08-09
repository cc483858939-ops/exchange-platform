import type { PublicAuthor } from './User';

export interface ArticleComment {
  id: number;
  article_id: number;
  content: string;
  created_at: string;
  author: PublicAuthor;
}

export interface CommentPage {
  items: ArticleComment[];
  next_cursor: string | null;
}
