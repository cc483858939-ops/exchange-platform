import type { Post } from '../types/Post';
import type { PublicAuthor } from '../types/User';
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

export function postToFeedPost(
  post: Post,
  context: { repostActor?: PublicAuthor } = {},
): FeedPost {
  const article = post.article;
  return {
    id: post.id,
    content: post.content,
    article,
    quotePost: post.quote_post,
    replyToPost: post.reply_to_post,
    author: post.author,
    title: article?.title ?? '',
    excerpt: article?.preview ?? post.content,
    coverImageUrl: article?.cover_image_url || '',
    createdAt: post.published_at,
    likeCount: post.like_count ?? 0,
    replyCount: post.reply_count ?? 0,
    viewCount: Math.max(0, post.view_count),
    liked: false,
    likeStatus: 'unknown',
    repostCount: 0,
    reposted: false,
    repostStatus: 'unknown',
    ...(context.repostActor ? { repostContext: { actor: context.repostActor } } : {}),
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
  if (post.id !== update.postId) {
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
  if (post.id !== update.postId) {
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
