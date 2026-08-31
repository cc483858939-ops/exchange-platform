import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import {
  deletePost as deletePostRequest,
  getFollowingTimeline,
  type FollowingTimelineItem,
} from '../services/postService';
import { getPostRecommendations } from '../services/recommendationService';
import { getPostLikeStates, likePost, unlikePost } from '../services/likeService';
import {
  getPostRepostStates,
  repostPost,
  undoRepostPost,
} from '../services/repostService';
import type { UserFollowState } from '../services/userService';
import type { RecommendedPost } from '../types/Recommendation';
import type {
  FeedLikeStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
  FeedTab,
} from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import {
  registerHomeTimelineSync,
  syncHomePostRemoval,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncHomeRepostState,
} from './sessionSync';
import type { PostReplyCountUpdate } from './sessionSync';

export type HomeRecommendationItem = {
  recommendation: RecommendedPost;
  post: FeedPost;
};

export type HomeFeedState<T> = {
  items: T[];
  loading: boolean;
  error: boolean;
  loaded: boolean;
};

export type HomeFollowingState = HomeFeedState<FeedPost> & {
  nextCursor: string | null;
  loadingMore: boolean;
  loadMoreError: boolean;
  stale: boolean;
  revalidating: boolean;
  revalidateError: boolean;
};

const normalizeID = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    return null;
  }
  return value;
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const normalizeReplyCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) && Number.isInteger(count) && count >= 0 ? count : null;
};

export const useHomeTimelineStore = defineStore('homeTimeline', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();

  const viewerID = ref<number | null>(null);
  const activeTab = ref<FeedTab>('for-you');
  const forYou = reactive<HomeFeedState<HomeRecommendationItem>>({
    items: [],
    loading: false,
    error: false,
    loaded: false,
  });
  const following = reactive<HomeFollowingState>({
    items: [],
    loading: false,
    error: false,
    loaded: false,
    nextCursor: null,
    loadingMore: false,
    loadMoreError: false,
    stale: false,
    revalidating: false,
    revalidateError: false,
  });
  const scrollY = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 0,
  });
  const likePendingPostIds = reactive(new Set<number>());
  const repostPendingPostIds = reactive(new Set<number>());
  const pendingDeletePostIds = reactive(new Set<number>());
  const deleteErrors = reactive(new Map<number, string>());

  const followingLoadedPostIds = new Set<number>();
  const likeMutationVersions = new Map<number, number>();
  const repostMutationVersions = new Map<number, number>();
  let authGeneration = 0;
  let forYouRequestVersion = 0;
  let followingRequestVersion = 0;
  let followingPagingVersion = 0;
  let likeGeneration = 0;
  let repostGeneration = 0;

  const clearLikeWork = () => {
    likeGeneration += 1;
    likePendingPostIds.clear();
    likeMutationVersions.clear();
  };

  const clearRepostWork = () => {
    repostGeneration += 1;
    repostPendingPostIds.clear();
    repostMutationVersions.clear();
  };

  const resetForYou = () => {
    forYouRequestVersion += 1;
    forYou.items = [];
    forYou.loading = false;
    forYou.error = false;
    forYou.loaded = false;
  };

  const resetFollowing = () => {
    followingRequestVersion += 1;
    followingPagingVersion += 1;
    following.items = [];
    following.loading = false;
    following.error = false;
    following.loaded = false;
    following.nextCursor = null;
    following.loadingMore = false;
    following.loadMoreError = false;
    following.stale = false;
    following.revalidating = false;
    following.revalidateError = false;
    followingLoadedPostIds.clear();
  };

  const clearForViewer = () => {
    authGeneration += 1;
    clearLikeWork();
    clearRepostWork();
    pendingDeletePostIds.clear();
    deleteErrors.clear();
    activeTab.value = 'for-you';
    scrollY['for-you'] = 0;
    scrollY.following = 0;
    resetForYou();
    resetFollowing();
  };

  const setViewer = (nextViewerID: unknown) => {
    const normalized = normalizeID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }
    viewerID.value = normalized;
    clearForViewer();
    return true;
  };

  const isAuthenticatedForViewer = (capturedViewerID = viewerID.value) =>
    Boolean(authStore.isAuthenticated && capturedViewerID !== null && capturedViewerID === viewerID.value);

  const setActiveTab = (tab: FeedTab) => {
    activeTab.value = tab;
  };

  const setScrollY = (tab: FeedTab, value: number) => {
    scrollY[tab] = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  const getLikeMutationVersion = (postId: number) =>
    likeMutationVersions.get(postId) ?? 0;

  const bumpLikeMutationVersion = (postId: number) => {
    const next = getLikeMutationVersion(postId) + 1;
    likeMutationVersions.set(postId, next);
    return next;
  };

  const getRepostMutationVersion = (postId: number) =>
    repostMutationVersions.get(postId) ?? 0;

  const bumpRepostMutationVersion = (postId: number) => {
    const next = getRepostMutationVersion(postId) + 1;
    repostMutationVersions.set(postId, next);
    return next;
  };

  const forEachHomePost = (postId: number, callback: (post: FeedPost) => void) => {
    if (feedStore.isPostDeleted(postId)) {
      return;
    }
    feedStore.recentlyPublishedPosts.forEach((post) => {
      if (post.id === postId) callback(post);
    });
    following.items.forEach((post) => {
      if (post.id === postId) callback(post);
    });
    forYou.items.forEach(({ post }) => {
      if (post.id === postId) callback(post);
    });
  };

  const findPost = (postId: number): FeedPost | undefined => {
    let found: FeedPost | undefined;
    forEachHomePost(postId, (post) => {
      found ||= post;
    });
    return found;
  };

  const applyLikeStateUpdateLocal = (
    update: FeedLikeStateUpdate,
    expectedVersion?: number,
  ) => {
    if (
      expectedVersion !== undefined
      && getLikeMutationVersion(update.postId) !== expectedVersion
    ) {
      return false;
    }
    let applied = false;
    forEachHomePost(update.postId, (post) => {
      applied = applyFeedLikeStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    bumpLikeMutationVersion(update.postId);
    likePendingPostIds.delete(update.postId);
    return applyLikeStateUpdateLocal(update);
  };

  const applyRepostStateUpdateLocal = (
    update: FeedRepostStateUpdate,
    expectedVersion?: number,
  ) => {
    if (
      expectedVersion !== undefined
      && getRepostMutationVersion(update.postId) !== expectedVersion
    ) {
      return false;
    }
    let applied = false;
    forEachHomePost(update.postId, (post) => {
      applied = applyFeedRepostStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    bumpRepostMutationVersion(update.postId);
    repostPendingPostIds.delete(update.postId);
    return applyRepostStateUpdateLocal(update);
  };

  const applyReplyCountUpdateLocal = (update: PostReplyCountUpdate) => {
    const replyCount = normalizeReplyCount(update.replyCount);
    if (replyCount === null) return false;
    let applied = false;
    forEachHomePost(update.postId, (post) => {
      post.replyCount = replyCount;
      applied = true;
    });
    return applied;
  };

  const reconcileFollowStateLocal = (state: UserFollowState) => {
    if (!Number.isSafeInteger(state.user_id) || state.user_id <= 0) return false;

    followingRequestVersion += 1;
    followingPagingVersion += 1;
    following.loading = false;
    following.loadingMore = false;
    following.revalidating = false;
    following.error = false;
    following.loadMoreError = false;
    following.revalidateError = false;
    following.stale = true;

    if (!state.following) {
      following.items = following.items.filter((post) => (
        (post.repostContext?.actor.id ?? post.author.id) !== state.user_id
      ));
    }
    return true;
  };

  const applyLikeStateUpdate = (
    update: FeedLikeStateUpdate,
    expectedVersion?: number,
  ) => {
    const applied = applyLikeStateUpdateLocal(update, expectedVersion);
    if (expectedVersion !== undefined && !applied) {
      return false;
    }
    syncHomeLikeState(update);
    return applied;
  };

  const applyRepostStateUpdate = (
    update: FeedRepostStateUpdate,
    expectedVersion?: number,
  ) => {
    const applied = applyRepostStateUpdateLocal(update, expectedVersion);
    if (expectedVersion !== undefined && !applied) {
      return false;
    }
    syncHomeRepostState(update);
    return applied;
  };

  const markUnavailableLocal = (postIds: number[], versions: Map<number, number>) => {
    postIds.forEach((postId) => {
      const capturedVersion = versions.get(postId);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(postId) !== capturedVersion
      ) {
        return;
      }
      forEachHomePost(postId, (post) => {
        if (post.likeStatus === 'unknown') {
          setFeedPostLikeUnavailable(post);
        }
      });
    });
  };

  const hydrateLikeStates = async (
    postIds: number[],
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;

    const versions = new Map(uniqueIDs.map((id) => [id, getLikeMutationVersion(id)]));
    try {
      const response = await getPostLikeStates(uniqueIDs);
      if (!isCurrent()) return;

      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.post_id);
        if (
          capturedVersion === undefined
          || getLikeMutationVersion(item.post_id) !== capturedVersion
          || !findPost(item.post_id)
        ) {
          return;
        }
        readyIDs.add(item.post_id);
        applyLikeStateUpdate({
          postId: item.post_id,
          likes: item.likes,
          liked: item.liked,
          status: 'ready',
        }, capturedVersion);
      });
      response.unavailable_post_ids.forEach((postId) => {
        const capturedVersion = versions.get(postId);
        if (
          readyIDs.has(postId)
          || capturedVersion === undefined
          || getLikeMutationVersion(postId) !== capturedVersion
          || !findPost(postId)
        ) {
          return;
        }
        applyLikeStateUpdate({
          postId,
          likes: 0,
          liked: false,
          status: 'unavailable',
        }, capturedVersion);
      });
    } catch {
      if (isCurrent()) {
        markUnavailableLocal(uniqueIDs, versions);
      }
    }
  };

  const markRepostUnavailableLocal = (postIds: number[], versions: Map<number, number>) => {
    postIds.forEach((postId) => {
      const capturedVersion = versions.get(postId);
      if (
        capturedVersion === undefined
        || getRepostMutationVersion(postId) !== capturedVersion
      ) {
        return;
      }
      forEachHomePost(postId, (post) => {
        if (post.repostStatus === 'unknown') {
          setFeedPostRepostUnavailable(post);
        }
      });
    });
  };

  const hydrateRepostStates = async (
    postIds: number[],
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;

    const versions = new Map(uniqueIDs.map((id) => [id, getRepostMutationVersion(id)]));
    try {
      const response = await getPostRepostStates(uniqueIDs);
      if (!isCurrent()) return;

      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.post_id);
        if (
          capturedVersion === undefined
          || getRepostMutationVersion(item.post_id) !== capturedVersion
          || !findPost(item.post_id)
        ) {
          return;
        }
        readyIDs.add(item.post_id);
        applyRepostStateUpdate({
          postId: item.post_id,
          reposts: item.reposts,
          reposted: item.reposted,
          status: 'ready',
        }, capturedVersion);
      });
      response.unavailable_post_ids.forEach((postId) => {
        const capturedVersion = versions.get(postId);
        if (
          readyIDs.has(postId)
          || capturedVersion === undefined
          || getRepostMutationVersion(postId) !== capturedVersion
          || !findPost(postId)
        ) {
          return;
        }
        applyRepostStateUpdate({
          postId,
          reposts: 0,
          reposted: false,
          status: 'unavailable',
        }, capturedVersion);
      });
    } catch {
      if (isCurrent()) {
        markRepostUnavailableLocal(uniqueIDs, versions);
      }
    }
  };

  const appendFollowingPosts = (activities: FollowingTimelineItem[]) => {
    const newPosts = activities
      .filter((activity) => {
        const postID = activity.post.id;
        if (followingLoadedPostIds.has(postID)) return false;
        followingLoadedPostIds.add(postID);
        return !feedStore.isPostDeleted(postID);
      })
      .map((activity) => postToFeedPost(
        activity.post,
        activity.activity_type === 'repost' ? { repostActor: activity.actor } : {},
      ));
    if (newPosts.length > 0) {
      following.items = [...following.items, ...newPosts];
    }
    return newPosts;
  };

  const currentForYouRequest = (version: number, generation: number, capturedViewerID: number) =>
    version === forYouRequestVersion
    && generation === authGeneration
    && isAuthenticatedForViewer(capturedViewerID);

  const currentFollowingRequest = (version: number, generation: number, capturedViewerID: number) =>
    version === followingRequestVersion
    && generation === authGeneration
    && isAuthenticatedForViewer(capturedViewerID);

  const currentFollowingPage = (
    requestVersion: number,
    generation: number,
    pagingVersion: number,
    capturedViewerID: number,
  ) => currentFollowingRequest(requestVersion, generation, capturedViewerID)
    && pagingVersion === followingPagingVersion;

  const currentFollowingRefresh = (
    requestVersion: number,
    generation: number,
    pagingVersion: number,
    capturedViewerID: number,
  ) => currentFollowingRequest(requestVersion, generation, capturedViewerID)
    && pagingVersion === followingPagingVersion;

  const loadForYou = async (force = false) => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !isAuthenticatedForViewer(capturedViewerID)
      || (forYou.loading && !force)
    ) {
      return;
    }
    if (forYou.loaded && !force) {
      return;
    }
    if (force) {
      clearLikeWork();
      forYouRequestVersion += 1;
      forYou.items = [];
      forYou.loaded = false;
    }

    const version = ++forYouRequestVersion;
    const generation = authGeneration;
    forYou.loading = true;
    forYou.error = false;

    try {
      const recommendations = await getPostRecommendations(50);
      if (!currentForYouRequest(version, generation, capturedViewerID)) return;
      forYou.items = recommendations
        .filter((recommendation) => !feedStore.isPostDeleted(recommendation.post.id))
        .map((recommendation) => ({
          recommendation,
          post: postToFeedPost(recommendation.post),
        }));
      forYou.loaded = true;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        forYou.items.map(({ post }) => post.id),
        () => currentForYouRequest(version, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
      );
      const capturedRepostGeneration = repostGeneration;
      void hydrateRepostStates(
        forYou.items.map(({ post }) => post.id),
        () => currentForYouRequest(version, generation, capturedViewerID)
          && repostGeneration === capturedRepostGeneration,
      );
    } catch {
      if (currentForYouRequest(version, generation, capturedViewerID)) {
        forYou.error = true;
      }
    } finally {
      if (version === forYouRequestVersion && generation === authGeneration) {
        forYou.loading = false;
      }
    }
  };

  const loadFollowing = async (force = false) => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !isAuthenticatedForViewer(capturedViewerID)
      || (following.loading && !force)
    ) {
      return;
    }
    if (following.loaded && !force) return;
    if (force) {
      resetFollowing();
    }

    const version = ++followingRequestVersion;
    const generation = authGeneration;
    const pagingVersion = ++followingPagingVersion;
    following.loading = true;
    following.error = false;
    following.loadMoreError = false;

    try {
      const response = await getFollowingTimeline({ limit: 20 });
      if (!currentFollowingRequest(version, generation, capturedViewerID)) return;
      const newPosts = appendFollowingPosts(response.items);
      following.nextCursor = response.next_cursor;
      following.loaded = true;
      following.stale = false;
      following.revalidateError = false;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(version, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
      );
      const capturedRepostGeneration = repostGeneration;
      void hydrateRepostStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(version, generation, capturedViewerID)
          && repostGeneration === capturedRepostGeneration,
      );
    } catch {
      if (currentFollowingRequest(version, generation, capturedViewerID)) {
        following.error = true;
      }
    } finally {
      if (
        version === followingRequestVersion
        && generation === authGeneration
        && pagingVersion === followingPagingVersion
      ) {
        following.loading = false;
      }
    }
  };

  const loadMoreFollowing = async () => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !isAuthenticatedForViewer(capturedViewerID)
      || !following.loaded
      || !following.nextCursor
      || following.loading
      || following.loadingMore
      || following.stale
      || following.revalidating
      || following.loadMoreError
    ) {
      return;
    }

    const requestedCursor = following.nextCursor;
    const requestVersion = followingRequestVersion;
    const generation = authGeneration;
    const pagingVersion = ++followingPagingVersion;
    following.loadingMore = true;
    following.loadMoreError = false;

    try {
      const response = await getFollowingTimeline({ limit: 20, cursor: requestedCursor });
      if (
        !currentFollowingPage(requestVersion, generation, pagingVersion, capturedViewerID)
        || following.nextCursor !== requestedCursor
      ) return;
      const newPosts = appendFollowingPosts(response.items);
      following.nextCursor = response.next_cursor;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(requestVersion, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
      );
      const capturedRepostGeneration = repostGeneration;
      void hydrateRepostStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(requestVersion, generation, capturedViewerID)
          && repostGeneration === capturedRepostGeneration,
      );
    } catch {
      if (currentFollowingPage(requestVersion, generation, pagingVersion, capturedViewerID)) {
        following.loadMoreError = true;
      }
    } finally {
      if (currentFollowingPage(requestVersion, generation, pagingVersion, capturedViewerID)) {
        following.loadingMore = false;
      }
    }
  };

  const revalidateFollowing = async () => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !isAuthenticatedForViewer(capturedViewerID)
    ) {
      return;
    }
    if (!following.loaded) {
      await loadFollowing();
      return;
    }
    if (!following.stale || following.revalidating) return;

    const version = ++followingRequestVersion;
    const generation = authGeneration;
    const pagingVersion = ++followingPagingVersion;
    following.revalidating = true;
    following.revalidateError = false;
    following.loadingMore = false;
    following.loadMoreError = false;

    try {
      const response = await getFollowingTimeline({ limit: 20 });
      if (!currentFollowingRefresh(version, generation, pagingVersion, capturedViewerID)) return;

      const freshIDs = new Set<number>();
      const previousPostsByID = new Map(following.items.map(post => [post.id, post]));
      const freshPosts: FeedPost[] = [];
      response.items.forEach((activity) => {
        const post = activity.post;
        if (
          freshIDs.has(post.id)
          || feedStore.isPostDeleted(post.id)
        ) return;
        freshIDs.add(post.id);
        const freshPost = postToFeedPost(
          post,
          activity.activity_type === 'repost' ? { repostActor: activity.actor } : {},
        );
        const previousPost = previousPostsByID.get(freshPost.id);
        freshPosts.push(previousPost && previousPost.repostStatus !== 'unknown'
          ? {
            ...freshPost,
            repostCount: previousPost.repostCount,
            reposted: previousPost.reposted,
            repostStatus: previousPost.repostStatus,
          }
          : freshPost);
      });

      following.items = freshPosts;
      followingLoadedPostIds.clear();
      freshPosts.forEach(post => followingLoadedPostIds.add(post.id));
      following.nextCursor = response.next_cursor;
      following.loaded = true;
      following.stale = false;
      following.revalidating = false;
      following.revalidateError = false;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        freshPosts.map(post => post.id),
        () => currentFollowingRefresh(version, generation, pagingVersion, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
      );
      const capturedRepostGeneration = repostGeneration;
      void hydrateRepostStates(
        freshPosts.map(post => post.id),
        () => currentFollowingRefresh(version, generation, pagingVersion, capturedViewerID)
          && repostGeneration === capturedRepostGeneration,
      );
    } catch {
      if (currentFollowingRefresh(version, generation, pagingVersion, capturedViewerID)) {
        following.revalidating = false;
        following.revalidateError = true;
        following.stale = true;
      }
    } finally {
      if (currentFollowingRefresh(version, generation, pagingVersion, capturedViewerID)) {
        following.revalidating = false;
      }
    }
  };

  const toggleLike = async (postId: number) => {
    const post = findPost(postId);
    if (!post || post.likeStatus !== 'ready' || likePendingPostIds.has(postId)) {
      return;
    }

    const previousLiked = post.liked;
    const previousLikes = post.likeCount;
    const mutationVersion = bumpLikeMutationVersion(postId);
    const capturedLikeGeneration = likeGeneration;
    const capturedAuthGeneration = authGeneration;
    const capturedViewerID = viewerID.value;
    likePendingPostIds.add(postId);
    applyLikeStateUpdate({
      postId,
      likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
      liked: !previousLiked,
      status: 'ready',
    }, mutationVersion);

    const isCurrent = () =>
      isAuthenticatedForViewer(capturedViewerID)
      && authGeneration === capturedAuthGeneration
      && likeGeneration === capturedLikeGeneration
      && getLikeMutationVersion(postId) === mutationVersion
      && likePendingPostIds.has(postId);

    try {
      const result = previousLiked
        ? await unlikePost(postId)
        : await likePost(postId);
      if (!isCurrent()) return;
      const settledVersion = bumpLikeMutationVersion(postId);
      applyLikeStateUpdate({
        postId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      }, settledVersion);
      likePendingPostIds.delete(postId);
    } catch (error) {
      if (!isCurrent()) return;
      const settledVersion = bumpLikeMutationVersion(postId);
      applyLikeStateUpdate({
        postId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'ready',
      }, settledVersion);
      if (getErrorStatus(error) === 503) {
        applyLikeStateUpdate({
          postId,
          likes: previousLikes,
          liked: previousLiked,
          status: 'unavailable',
        }, settledVersion);
      }
      likePendingPostIds.delete(postId);
    }
  };

  const toggleRepost = async (postId: number) => {
    const post = findPost(postId);
    if (!post || post.repostStatus !== 'ready' || repostPendingPostIds.has(postId)) {
      return false;
    }

    const previousReposted = post.reposted;
    const previousReposts = post.repostCount;
    const mutationVersion = bumpRepostMutationVersion(postId);
    const capturedRepostGeneration = repostGeneration;
    const capturedAuthGeneration = authGeneration;
    const capturedViewerID = viewerID.value;
    repostPendingPostIds.add(postId);
    applyRepostStateUpdate({
      postId,
      reposts: previousReposted ? Math.max(0, previousReposts - 1) : previousReposts + 1,
      reposted: !previousReposted,
      status: 'ready',
    }, mutationVersion);

    const isCurrent = () =>
      isAuthenticatedForViewer(capturedViewerID)
      && authGeneration === capturedAuthGeneration
      && repostGeneration === capturedRepostGeneration
      && getRepostMutationVersion(postId) === mutationVersion
      && repostPendingPostIds.has(postId);

    try {
      const result = previousReposted
        ? await undoRepostPost(postId)
        : await repostPost(postId);
      if (!isCurrent()) return false;
      const settledVersion = bumpRepostMutationVersion(postId);
      applyRepostStateUpdate({
        postId,
        reposts: result.reposts,
        reposted: result.reposted,
        status: 'ready',
      }, settledVersion);
      repostPendingPostIds.delete(postId);
      return true;
    } catch {
      if (!isCurrent()) return false;
      const settledVersion = bumpRepostMutationVersion(postId);
      applyRepostStateUpdate({
        postId,
        reposts: previousReposts,
        reposted: previousReposted,
        status: 'ready',
      }, settledVersion);
      repostPendingPostIds.delete(postId);
      return false;
    }
  };

  const removePostLocal = (postId: number) => {
    following.items = following.items.filter((post) => post.id !== postId);
    forYou.items = forYou.items.filter((item) => item.post.id !== postId);
    followingLoadedPostIds.add(postId);
    likePendingPostIds.delete(postId);
    likeMutationVersions.delete(postId);
    repostPendingPostIds.delete(postId);
    repostMutationVersions.delete(postId);
    pendingDeletePostIds.delete(postId);
    deleteErrors.delete(postId);
  };

  const dismissRecommendation = (postId: number) => {
    const before = forYou.items.length;
    forYou.items = forYou.items.filter((item) => item.recommendation.post.id !== postId);
    return forYou.items.length !== before;
  };

  const removePost = (postId: number, ownerUserID?: number) => {
    if (ownerUserID !== undefined) {
      if (!feedStore.markPostDeleted(postId, ownerUserID)) return false;
    }
    removePostLocal(postId);
    if (ownerUserID !== undefined) {
      syncHomePostRemoval(postId);
    }
    return true;
  };

  const deletePost = async (postId: number) => {
    const ownerUserID = viewerID.value;
    const post = findPost(postId);
    if (
      ownerUserID === null
      || !authStore.isAuthenticated
      || !post
      || post.author.id !== ownerUserID
      || pendingDeletePostIds.has(postId)
    ) return false;

    const capturedAuthGeneration = authGeneration;
    const capturedViewerID = ownerUserID;
    pendingDeletePostIds.add(postId);
    deleteErrors.delete(postId);
    const isCurrent = () =>
      authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && authGeneration === capturedAuthGeneration
      && pendingDeletePostIds.has(postId);

    try {
      await deletePostRequest(postId);
      if (!isCurrent()) return false;
      return removePost(postId, ownerUserID);
    } catch (error) {
      if (!isCurrent()) return false;
      if (getErrorStatus(error) === 404) {
        return removePost(postId, ownerUserID);
      }
      deleteErrors.set(
        postId,
        getErrorStatus(error) === 403
          ? 'You can only delete your own posts.'
          : getErrorStatus(error) === 401
            ? 'Please log in again to delete this post.'
            : 'Could not delete post. Please try again.',
      );
      pendingDeletePostIds.delete(postId);
      return false;
    }
  };

  const replaceAuthorIdentityLocal = (author: PublicAuthor) => {
    following.items = following.items.map((post) => {
      const canonicalMatches = post.author.id === author.id;
      const actorMatches = post.repostContext?.actor.id === author.id;
      return canonicalMatches || actorMatches
        ? {
          ...post,
          author: canonicalMatches ? author : post.author,
          repostContext: actorMatches ? { actor: author } : post.repostContext,
        }
        : post;
    });
    forYou.items = forYou.items.map((item) => (
      item.post.author.id === author.id || item.post.repostContext?.actor.id === author.id
        ? {
          ...item,
          post: {
            ...item.post,
            author: item.post.author.id === author.id ? author : item.post.author,
            repostContext: item.post.repostContext?.actor.id === author.id
              ? { actor: author }
              : item.post.repostContext,
          },
        }
        : item
    ));
  };

  const replaceAuthorIdentity = (author: PublicAuthor) => {
    replaceAuthorIdentityLocal(author);
    feedStore.replaceAuthorIdentity(author);
    syncHomeAuthorIdentity(author);
  };

  const retryFollowingLoadMore = () => {
    following.loadMoreError = false;
    void loadMoreFollowing();
  };

  registerHomeTimelineSync({
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyReplyCountUpdateLocal,
    reconcileFollowStateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
  });

  watch(
    () => authStore.currentIdentity?.id,
    (nextViewerID) => {
      setViewer(nextViewerID ?? null);
    },
    { immediate: true },
  );

  watch(
    () => feedStore.recentlyPublishedPosts
      .map((post) => `${post.id}:${post.likeStatus}:${post.repostStatus}`)
      .join(','),
    () => {
      const ids = feedStore.recentlyPublishedPosts
        .filter((post) => post.likeStatus === 'unknown')
        .map((post) => post.id);
      const capturedViewerID = viewerID.value;
      const capturedAuthGeneration = authGeneration;
      if (!authStore.isAuthenticated || capturedViewerID === null) return;
      if (ids.length > 0) {
        const capturedLikeGeneration = likeGeneration;
        void hydrateLikeStates(ids, () =>
          isAuthenticatedForViewer(capturedViewerID)
          && authGeneration === capturedAuthGeneration
          && likeGeneration === capturedLikeGeneration,
        );
      }
      const repostIDs = feedStore.recentlyPublishedPosts
        .filter((post) => post.repostStatus === 'unknown')
        .map((post) => post.id);
      const capturedRepostGeneration = repostGeneration;
      if (repostIDs.length > 0) {
        void hydrateRepostStates(repostIDs, () =>
          isAuthenticatedForViewer(capturedViewerID)
          && authGeneration === capturedAuthGeneration
          && repostGeneration === capturedRepostGeneration,
        );
      }
    },
    { immediate: true },
  );

  return {
    viewerID,
    activeTab,
    forYou,
    following,
    scrollY,
    likePendingPostIds,
    repostPendingPostIds,
    pendingDeletePostIds,
    deleteErrors,
    setViewer,
    setActiveTab,
    setScrollY,
    loadForYou,
    loadFollowing,
    loadMoreFollowing,
    revalidateFollowing,
    retryFollowingLoadMore,
    toggleLike,
    toggleRepost,
    findPost,
    applyLikeStateUpdate,
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyReplyCountUpdateLocal,
    reconcileFollowStateLocal,
    dismissRecommendation,
    removePost,
    removePostLocal,
    deletePost,
    replaceAuthorIdentity,
    replaceAuthorIdentityLocal,
  };
});
