import type { Article } from '../types/Article';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FollowingTimelineItem } from '../services/articleService';
import type { FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';

const safeLikeCount = (likes: number, fallback = 0) =>
  Number.isFinite(likes) ? Math.max(0, likes) : Math.max(0, fallback);

const safeRepostCount = (reposts: number, fallback = 0) => {
  const count = Number(reposts);
  if (!Number.isFinite(count) || !Number.isInteger(count) || count < 0) {
    return Math.max(0, Math.floor(Number(fallback) || 0));
  }
  return count;
};

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
    repostCount: 0,
    reposted: false,
    repostStatus: 'unknown',
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
    repostCount: 0,
    reposted: false,
    repostStatus: 'unknown',
  };
}

export function followingTimelineItemToFeedPost(item: FollowingTimelineItem): FeedPost {
  const post = articleToFeedPost(item.article);
  if (item.activity_type === 'repost') {
    post.repostContext = { actor: item.actor };
  }
  return post;
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

export function setFeedPostRepostReady(post: FeedPost, reposts: number, reposted: boolean): FeedPost {
  post.repostCount = safeRepostCount(reposts, post.repostCount);
  post.reposted = reposted;
  post.repostStatus = 'ready';
  return post;
}

export function setFeedPostRepostUnavailable(post: FeedPost): FeedPost {
  post.repostStatus = 'unavailable';
  return post;
}

export function applyFeedRepostStateUpdate(post: FeedPost, update: FeedRepostStateUpdate): boolean {
  if (post.id !== update.articleId) {
    return false;
  }

  if (update.status === 'ready') {
    setFeedPostRepostReady(post, update.reposts, update.reposted);
  } else if (update.status === 'unavailable') {
    setFeedPostRepostUnavailable(post);
  } else {
    post.repostStatus = 'unknown';
  }
  return true;
}
