import type { PublicAuthor } from './User';

export interface RecommendationTracking {
  request_id: string;
  position: number;
  scene: string;
  ranker_version: string;
  ranker_config_hash: string;
  strategy_id: string;
  token: string;
  expires_at: string;
}

export interface RecommendedArticle {
  author: PublicAuthor;
  id: number;
  title: string;
  content: string;
  preview: string;  cover_image_url: string;  like_count: number;
  comment_count: number;
  view_count: number;
  created_at: string;
  score: number;
  tracking?: RecommendationTracking;
}
