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

import type { Post } from './Post';

export interface RecommendedPost {
  post: Post;
  score: number;
  tracking?: RecommendationTracking;
}
