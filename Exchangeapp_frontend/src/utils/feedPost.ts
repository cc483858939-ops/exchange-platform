import type { Post, PostReference } from '../types/Post';
import type { PublicAuthor } from '../types/User';
import type {
  FeedBookmarkStateUpdate,
  FeedLikeStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
} from '../types/Feed';

const safeLikeCount = (likes: number, fallback = 0) =>
  Number.isFinite(likes) ? Math.max(0, likes) : Math.max(0, fallback);

const safeAggregateCount = (value: number, fallback = 0) => {
  const count = Number(value);
  if (!Number.isFinite(count) || !Number.isInteger(count) || count < 0) {
    return Math.max(0, Math.floor(Number(fallback) || 0));
  }
  return count;
};

export function postToFeedPost(
  post: Post,
  context: { repostActor?: PublicAuthor } = {},
  isPostDeleted: (postID: number) => boolean = () => false,
): FeedPost {
  return {
    id: post.id,
    content: post.content,
    language: post.language,
    media: post.media.map(item => ({ ...item })),
    quotePost: normalizePostReference(post.quote_post, isPostDeleted),
    replyToPost: normalizePostReference(post.reply_to_post, isPostDeleted),
    author: post.author,
    createdAt: post.published_at || post.created_at,
    likeCount: post.like_count ?? 0,
    replyCount: post.reply_count ?? 0,
    quoteCount: safeAggregateCount(post.quote_count),
    viewCount: Math.max(0, post.view_count),
    liked: false,
    likeStatus: 'unknown',
    repostCount: safeAggregateCount(post.repost_count),
    reposted: false,
    repostStatus: 'unknown',
    bookmarked: false,
    bookmarkStatus: 'unknown',
    ...(context.repostActor ? { repostContext: { actor: context.repostActor } } : {}),
  };
}

export function initializeGuestInteractionStates(posts: FeedPost[]): void {
  posts.forEach((post) => {
    post.liked = false;
    post.likeStatus = 'ready';
    post.reposted = false;
    post.repostStatus = 'ready';
    post.bookmarked = false;
    post.bookmarkStatus = 'ready';
  });
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
  post.repostCount = safeAggregateCount(reposts, post.repostCount);
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

export function normalizePostReference(
  reference: PostReference | null | undefined,
  isPostDeleted: (postID: number) => boolean,
): PostReference | null | undefined {
  return reference && (reference.deleted || isPostDeleted(reference.id))
    ? { id: reference.id, deleted: true }
    : reference;
}

export function invalidateFeedPostReferences(post: FeedPost, deletedPostID: number): void {
  if (post.quotePost?.id === deletedPostID) {
    post.quotePost = { id: deletedPostID, deleted: true };
  }
  if (post.replyToPost?.id === deletedPostID) {
    post.replyToPost = { id: deletedPostID, deleted: true };
  }
}

export function normalizePostReferences(post: Post, isPostDeleted: (postID: number) => boolean): Post {
  post.quote_post = normalizePostReference(post.quote_post, isPostDeleted) ?? null;
  post.reply_to_post = normalizePostReference(post.reply_to_post, isPostDeleted) ?? null;
  return post;
}

export function setFeedPostBookmarkReady(
  post: FeedPost,
  bookmarked: boolean,
): FeedPost {
  post.bookmarked = bookmarked;
  post.bookmarkStatus = 'ready';
  return post;
}

export function setFeedPostBookmarkUnavailable(post: FeedPost): FeedPost {
  post.bookmarkStatus = 'unavailable';
  return post;
}

export function applyFeedBookmarkStateUpdate(
  post: FeedPost,
  update: FeedBookmarkStateUpdate,
): boolean {
  if (post.id !== update.postId) {
    return false;
  }

  if (update.status === 'ready') {
    setFeedPostBookmarkReady(post, update.bookmarked);
  } else if (update.status === 'unavailable') {
    setFeedPostBookmarkUnavailable(post);
  } else {
    post.bookmarkStatus = 'unknown';
  }
  return true;
}
