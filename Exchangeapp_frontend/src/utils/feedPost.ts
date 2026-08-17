import type { Article } from '../types/Article';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FeedLikeStateUpdate, FeedPost } from '../types/Feed';

const safeLikeCount = (likes: number, fallback = 0) =>
  Number.isFinite(likes) ? Math.max(0, likes) : Math.max(0, fallback);

export function articleToFeedPost(article: Article): FeedPost {
  return {
    id: article.ID,
    author: article.author,
    title: article.title,
    excerpt: article.content,
    coverImageUrl: article.cover_image_url || '',
    createdAt: article.CreatedAt,
    likeCount: article.like_count ?? 0,
    commentCount: article.comment_count ?? 0,
    viewCount: Math.max(0, article.view_count),
    liked: false,
    likeStatus: 'unknown',
  };
}

export function recommendationToFeedPost(article: RecommendedArticle): FeedPost {
  return {
    id: article.id,
    author: article.author,
    title: article.title,
    excerpt: article.content,
    coverImageUrl: article.cover_image_url || '',
    createdAt: article.created_at,
    likeCount: article.like_count ?? 0,
    commentCount: article.comment_count ?? 0,
    viewCount: Math.max(0, article.view_count),
    liked: false,
    likeStatus: 'unknown',
  };
}

export function setFeedPostLikeReady(post: FeedPost, likes: number, liked: boolean): FeedPost {
  post.likeCount = safeLikeCount(likes, post.likeCount);
  post.liked = liked;
  post.likeStatus = 'ready';
  return post;
}

export function setFeedPostLikeUnavailable(post: FeedPost): FeedPost {
  post.likeStatus = 'unavailable';
  return post;
}

export function applyFeedLikeStateUpdate(post: FeedPost, update: FeedLikeStateUpdate): boolean {
  if (post.id !== update.articleId) {
    return false;
  }

  if (update.status === 'ready') {
    setFeedPostLikeReady(post, update.likes, update.liked);
  } else if (update.status === 'unavailable') {
    setFeedPostLikeUnavailable(post);
  } else {
    post.likeStatus = 'unknown';
  }
  return true;
}