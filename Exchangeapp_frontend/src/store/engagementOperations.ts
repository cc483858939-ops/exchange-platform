import { bookmarkPost, unbookmarkPost } from '../services/bookmarkService';
import { likePost, unlikePost } from '../services/likeService';
import { repostPost, undoRepostPost } from '../services/repostService';
import type {
  FeedBookmarkStateUpdate,
  FeedLikeStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
} from '../types/Feed';

export const createOptimisticLikeUpdate = (
  post: Pick<FeedPost, 'id' | 'liked' | 'likeCount'>,
): FeedLikeStateUpdate => ({
  postId: post.id,
  likes: post.liked ? Math.max(0, post.likeCount - 1) : post.likeCount + 1,
  liked: !post.liked,
  status: 'ready',
});

export const createOptimisticRepostUpdate = (
  post: Pick<FeedPost, 'id' | 'reposted' | 'repostCount'>,
): FeedRepostStateUpdate => ({
  postId: post.id,
  reposts: post.reposted ? Math.max(0, post.repostCount - 1) : post.repostCount + 1,
  reposted: !post.reposted,
  status: 'ready',
});

export const createOptimisticBookmarkUpdate = (
  post: Pick<FeedPost, 'id' | 'bookmarked'>,
): FeedBookmarkStateUpdate => ({
  postId: post.id,
  bookmarked: !post.bookmarked,
  status: 'ready',
});

export const executeLikeToggle = (postId: number, currentlyLiked: boolean) => (
  currentlyLiked ? unlikePost(postId) : likePost(postId)
);

export const executeRepostToggle = (postId: number, currentlyReposted: boolean) => (
  currentlyReposted ? undoRepostPost(postId) : repostPost(postId)
);

export const executeBookmarkToggle = (postId: number, currentlyBookmarked: boolean) => (
  currentlyBookmarked ? unbookmarkPost(postId) : bookmarkPost(postId)
);
