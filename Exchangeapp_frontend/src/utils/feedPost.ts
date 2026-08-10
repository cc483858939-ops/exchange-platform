import type { Article } from '../types/Article';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FeedPost } from '../types/Feed';

export function articleToFeedPost(article: Article): FeedPost {
  return {
    id: article.ID,
    author: article.author,
    title: article.title,
    excerpt: article.preview || article.summary || '',
    coverImageUrl: article.cover_image_url || '',
    createdAt: article.CreatedAt,
    likeCount: article.like_count ?? 0,
    commentCount: article.comment_count ?? 0,
  };
}

export function recommendationToFeedPost(article: RecommendedArticle): FeedPost {
  return {
    id: article.id,
    author: article.author,
    title: article.title,
    excerpt: article.summary || article.preview || '',
    coverImageUrl: article.cover_image_url || '',
    createdAt: article.created_at,
    likeCount: article.like_count ?? 0,
    commentCount: article.comment_count ?? 0,
  };
}
