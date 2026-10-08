import { getPostEngagementStates } from '../services/engagementService';
import type { FeedPost } from '../types/Feed';
import {
  applyFeedBookmarkStateUpdate, applyFeedLikeStateUpdate, applyFeedRepostStateUpdate,
  setFeedPostBookmarkUnavailable, setFeedPostLikeUnavailable, setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import type { createEngagementMutationCoordinator } from './engagementMutationCoordinator';

type HydrationMutations = Pick<ReturnType<typeof createEngagementMutationCoordinator>, 'captureRevision' | 'isRevisionCurrent'>;

export const loadPostEngagementIndex = async (postIDs: number[]) => {
  const response = await getPostEngagementStates(postIDs);
  return new Map(response.items.map(item => [item.post_id, item]));
};

// Each session owns its identity/request predicate and per-Post revisions.
export const hydratePostEngagement = (
  posts: FeedPost[],
  mutations: HydrationMutations,
  current: () => boolean,
  findPost: (postID: number) => FeedPost | undefined,
) => {
  const postIDs = Array.from(new Set(posts.map(post => post.id)));
  if (postIDs.length === 0) return;

  const revisions = {
    like: new Map(postIDs.map(postID => [postID, mutations.captureRevision('like', postID)])),
    repost: new Map(postIDs.map(postID => [postID, mutations.captureRevision('repost', postID)])),
    bookmark: new Map(postIDs.map(postID => [postID, mutations.captureRevision('bookmark', postID)])),
  };
  void loadPostEngagementIndex(postIDs).then((states) => {
    if (!current()) return;
    postIDs.forEach((postID) => {
      const post = findPost(postID);
      if (!post) return;
      const state = states.get(postID);
      const likeRevision = revisions.like.get(postID);
      if (likeRevision && mutations.isRevisionCurrent('like', postID, likeRevision)) {
        const like = state?.like;
        if (like?.status === 'ready') {
          applyFeedLikeStateUpdate(post, { postId: postID, likes: like.likes, liked: like.liked, status: 'ready' });
        } else {
          setFeedPostLikeUnavailable(post);
        }
      }
      const repostRevision = revisions.repost.get(postID);
      if (repostRevision && mutations.isRevisionCurrent('repost', postID, repostRevision)) {
        const repost = state?.repost;
        if (repost?.status === 'ready') {
          applyFeedRepostStateUpdate(post, { postId: postID, reposts: repost.reposts, reposted: repost.reposted, status: 'ready' });
        } else {
          setFeedPostRepostUnavailable(post);
        }
      }
      const bookmarkRevision = revisions.bookmark.get(postID);
      if (bookmarkRevision && mutations.isRevisionCurrent('bookmark', postID, bookmarkRevision)) {
        const bookmark = state?.bookmark;
        if (bookmark?.status === 'ready') {
          applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: bookmark.bookmarked, status: 'ready' });
        } else {
          setFeedPostBookmarkUnavailable(post);
        }
      }
    });
  }).catch(() => {
    if (!current()) return;
    postIDs.forEach((postID) => {
      const post = findPost(postID);
      if (!post) return;
      const likeRevision = revisions.like.get(postID);
      if (likeRevision && mutations.isRevisionCurrent('like', postID, likeRevision)) setFeedPostLikeUnavailable(post);
      const repostRevision = revisions.repost.get(postID);
      if (repostRevision && mutations.isRevisionCurrent('repost', postID, repostRevision)) setFeedPostRepostUnavailable(post);
      const bookmarkRevision = revisions.bookmark.get(postID);
      if (bookmarkRevision && mutations.isRevisionCurrent('bookmark', postID, bookmarkRevision)) setFeedPostBookmarkUnavailable(post);
    });
  });
};
