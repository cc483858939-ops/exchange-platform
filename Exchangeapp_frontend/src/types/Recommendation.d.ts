export interface RecommendedArticle {
  id: number;
  title: string;
  preview: string;
  summary: string;
  tags: string[];
  category: string;
  like_count: number;
  created_at: string;
  score: number;
}
