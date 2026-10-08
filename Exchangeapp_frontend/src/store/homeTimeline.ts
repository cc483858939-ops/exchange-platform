import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import {
  deletePost as deletePostRequest,
  getFollowingTimeline,
  type TimelineItem,
} from '../services/postService';
import {
  getPostRecommendations,
  getPublicPostRecommendations,
} from '../services/recommendationService';
import { loadPostEngagementIndex } from './engagementHydration';
import type { UserFollowState } from '../services/userService';
import type { RecommendedPost } from '../types/Recommendation';
import type {
  FeedLikeStateUpdate,
  FeedBookmarkStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
  FeedTab,
} from '../types/Feed';
import { normalizePostQuoteCountUpdate } from '../utils/quoteCount';
import type { PublicAuthor } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  applyFeedBookmarkStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  invalidateFeedPostReferences,
  normalizePostReferences,
  setFeedPostBookmarkUnavailable,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import {
  registerHomeTimelineSync,
  syncHomePostRemoval,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncHomeRepostState,
  syncHomeBookmarkState,
  beginBookmarkStateMutation,
  markOwnProfileTimelineStale,
} from './sessionSync';
import type { PostQuoteCountUpdate, PostReplyCountUpdate } from './sessionSync';
import { refreshAndSyncPostQuoteCount } from './postQuoteCountReconciliation';
import { getGuestRecommendationSessionID } from '../utils/guestRecommendationSession';
import {
  createEngagementMutationCoordinator,
  type EngagementMutationResult,
  type EngagementMutationRevision,
} from './engagementMutationCoordinator';
import {
  releaseEngagementMutationLease,
  tryBeginEngagementMutationLease,
} from './engagementMutationLease';
import {
  createOptimisticBookmarkUpdate,
  createOptimisticLikeUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';

export type HomeRecommendationItem = {
  recommendation: RecommendedPost;
  post: FeedPost;
};

export type HomeEngagementMutationResult = EngagementMutationResult;

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

export type HomeForYouState = HomeFeedState<HomeRecommendationItem> & {
  loadingMore: boolean;
  loadMoreError: boolean;
  depleted: boolean;
};

const HOME_FOR_YOU_PAGE_SIZE = 20;

type ForYouAudience =
  | { authenticated: true; viewerID: number }
  | { authenticated: false; viewerID: null };

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
  const forYou = reactive<HomeForYouState>({
    items: [],
    loading: false,
    error: false,
    loaded: false,
    loadingMore: false,
    loadMoreError: false,
    depleted: false,
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
  const scrollTop = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 0,
  });
  const homeReselectVersion = ref(0);
  const engagementMutations = createEngagementMutationCoordinator();
  const likePendingPostIds = engagementMutations.likePendingPostIDs;
  const repostPendingPostIds = engagementMutations.repostPendingPostIDs;
  const bookmarkPendingPostIds = engagementMutations.bookmarkPendingPostIDs;
  const pendingDeletePostIds = reactive(new Set<number>());
  const deleteErrors = reactive(new Map<number, string>());

  const followingLoadedPostIds = new Set<number>();
  const forYouLoadedPostIds = new Set<number>();
  let authGeneration = 0;
  let forYouRequestVersion = 0;
  let forYouPagingVersion = 0;
  let forYouNoProgressPages = 0;
  let followingRequestVersion = 0;
  let followingPagingVersion = 0;
  const resetForYou = () => {
    forYouRequestVersion += 1;
    forYouPagingVersion += 1;
    forYou.items = [];
    forYou.loading = false;
    forYou.error = false;
    forYou.loaded = false;
    forYou.loadingMore = false;
    forYou.loadMoreError = false;
    forYou.depleted = false;
    forYouLoadedPostIds.clear();
    forYouNoProgressPages = 0;
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
    engagementMutations.resetAll();
    pendingDeletePostIds.clear();
    deleteErrors.clear();
    activeTab.value = 'for-you';
    scrollTop['for-you'] = 0;
    scrollTop.following = 0;
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

  const setScrollTop = (tab: FeedTab, value: number) => {
    scrollTop[tab] = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  const requestHomeReselect = () => {
    homeReselectVersion.value += 1;
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
      && engagementMutations.getVersion('like', update.postId) !== expectedVersion
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
    engagementMutations.invalidate('like', update.postId);
    return applyLikeStateUpdateLocal(update);
  };

  const applyRepostStateUpdateLocal = (
    update: FeedRepostStateUpdate,
    expectedVersion?: number,
  ) => {
    if (
      expectedVersion !== undefined
      && engagementMutations.getVersion('repost', update.postId) !== expectedVersion
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
    engagementMutations.invalidate('repost', update.postId);
    return applyRepostStateUpdateLocal(update);
  };

  const applyBookmarkStateUpdateLocal = (
    update: FeedBookmarkStateUpdate,
    expectedVersion?: number,
  ) => {
    if (
      expectedVersion !== undefined
      && engagementMutations.getVersion('bookmark', update.postId) !== expectedVersion
    ) {
      return false;
    }
    let applied = false;
    forEachHomePost(update.postId, (post) => {
      applied = applyFeedBookmarkStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalBookmarkStateLocal = (update: FeedBookmarkStateUpdate) => {
    engagementMutations.invalidate('bookmark', update.postId);
    return applyBookmarkStateUpdateLocal(update);
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

  const applyQuoteCountUpdateLocal = (update: PostQuoteCountUpdate) => {
    const normalized = normalizePostQuoteCountUpdate(update);
    if (!normalized) return false;
    let applied = false;
    forEachHomePost(normalized.postId, (post) => {
      post.quoteCount = normalized.quoteCount;
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

  const applyBookmarkStateUpdate = (
    update: FeedBookmarkStateUpdate,
    expectedVersion?: number,
  ) => {
    const applied = applyBookmarkStateUpdateLocal(update, expectedVersion);
    if (expectedVersion !== undefined && !applied) {
      return false;
    }
    syncHomeBookmarkState(update);
    return applied;
  };

  const markUnavailableLocal = (
    postIds: number[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    postIds.forEach((postId) => {
      const revision = revisions.get(postId);
      if (
        !revision
        || !engagementMutations.isRevisionCurrent('like', postId, revision)
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
    uniqueIDs: number[],
    isCurrent: () => boolean,
    responsePromise: ReturnType<typeof loadPostEngagementIndex>,
  ) => {
    const revisions = new Map(uniqueIDs.map((id) => [
      id,
      engagementMutations.captureRevision('like', id),
    ]));
    try {
      const states = await responsePromise;
      if (!isCurrent()) return;

      uniqueIDs.forEach((postId) => {
        const revision = revisions.get(postId);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('like', postId, revision)
          || !findPost(postId)
        ) {
          return;
        }
        const like = states.get(postId)?.like;
        if (like?.status === 'ready') {
          applyLikeStateUpdate({
            postId,
            likes: like.likes,
            liked: like.liked,
            status: 'ready',
          }, revision.version);
        } else {
          applyLikeStateUpdate({
            postId,
            likes: 0,
            liked: false,
            status: 'unavailable',
          }, revision.version);
        }
      });
    } catch {
      if (isCurrent()) {
        markUnavailableLocal(uniqueIDs, revisions);
      }
    }
  };

  const markRepostUnavailableLocal = (
    postIds: number[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    postIds.forEach((postId) => {
      const revision = revisions.get(postId);
      if (
        !revision
        || !engagementMutations.isRevisionCurrent('repost', postId, revision)
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
    uniqueIDs: number[],
    isCurrent: () => boolean,
    responsePromise: ReturnType<typeof loadPostEngagementIndex>,
  ) => {
    const revisions = new Map(uniqueIDs.map((id) => [
      id,
      engagementMutations.captureRevision('repost', id),
    ]));
    try {
      const states = await responsePromise;
      if (!isCurrent()) return;

      uniqueIDs.forEach((postId) => {
        const revision = revisions.get(postId);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('repost', postId, revision)
          || !findPost(postId)
        ) {
          return;
        }
        const repost = states.get(postId)?.repost;
        if (repost?.status === 'ready') {
          applyRepostStateUpdate({
            postId,
            reposts: repost.reposts,
            reposted: repost.reposted,
            status: 'ready',
          }, revision.version);
        } else {
          applyRepostStateUpdate({
            postId,
            reposts: 0,
            reposted: false,
            status: 'unavailable',
          }, revision.version);
        }
      });
    } catch {
      if (isCurrent()) {
        markRepostUnavailableLocal(uniqueIDs, revisions);
      }
    }
  };

  const markBookmarkUnavailableLocal = (
    postIds: number[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    postIds.forEach((postId) => {
      const revision = revisions.get(postId);
      if (
        !revision
        || !engagementMutations.isRevisionCurrent('bookmark', postId, revision)
      ) return;
      forEachHomePost(postId, (post) => {
        if (post.bookmarkStatus === 'unknown') setFeedPostBookmarkUnavailable(post);
      });
    });
  };

  const hydrateBookmarkStates = async (
    uniqueIDs: number[],
    isCurrent: () => boolean,
    responsePromise: ReturnType<typeof loadPostEngagementIndex>,
  ) => {
    const revisions = new Map(uniqueIDs.map((id) => [
      id,
      engagementMutations.captureRevision('bookmark', id),
    ]));
    try {
      const states = await responsePromise;
      if (!isCurrent()) return;
      uniqueIDs.forEach((postId) => {
        const revision = revisions.get(postId);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('bookmark', postId, revision)
          || !findPost(postId)
        ) return;
        const bookmark = states.get(postId)?.bookmark;
        applyBookmarkStateUpdateLocal({
          postId,
          bookmarked: bookmark?.status === 'ready' ? bookmark.bookmarked : false,
          status: bookmark?.status === 'ready' ? 'ready' : 'unavailable',
        }, revision.version);
      });
    } catch {
      if (isCurrent()) {
        markBookmarkUnavailableLocal(uniqueIDs, revisions);
      }
    }
  };

  const hydrateEngagementStates = (postIds: number[], isCurrent: () => boolean) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;
    const responsePromise = loadPostEngagementIndex(uniqueIDs);
    void hydrateLikeStates(uniqueIDs, isCurrent, responsePromise);
    void hydrateRepostStates(uniqueIDs, isCurrent, responsePromise);
    void hydrateBookmarkStates(uniqueIDs, isCurrent, responsePromise);
  };

  const appendFollowingPosts = (activities: TimelineItem[]) => {
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
        feedStore.isPostDeleted,
      ));
    if (newPosts.length > 0) {
      following.items = [...following.items, ...newPosts];
    }
    return newPosts;
  };

  const appendForYouRecommendations = (recommendations: RecommendedPost[]) => {
    const audience = currentForYouAudience();
    const appended: HomeRecommendationItem[] = [];

    recommendations.forEach((recommendation) => {
      const postID = normalizeID(recommendation?.post?.id);
      if (
        postID === null
        || forYouLoadedPostIds.has(postID)
        || feedStore.isPostDeleted(postID)
      ) {
        return;
      }

      forYouLoadedPostIds.add(postID);
      normalizePostReferences(recommendation.post, feedStore.isPostDeleted);
      const feedPost = postToFeedPost(recommendation.post, {}, feedStore.isPostDeleted);
      if (!audience.authenticated) {
        feedPost.likeStatus = 'ready';
        feedPost.repostStatus = 'ready';
        feedPost.bookmarkStatus = 'ready';
      }
      appended.push({
        recommendation,
        post: feedPost,
      });
    });

    if (appended.length > 0) {
      forYou.items = [...forYou.items, ...appended];
    }
    return appended;
  };

  const currentForYouAudience = (): ForYouAudience => (
    authStore.isAuthenticated && viewerID.value !== null
      ? { authenticated: true, viewerID: viewerID.value }
      : { authenticated: false, viewerID: null }
  );

  const isCurrentForYouAudience = (capturedAudience: ForYouAudience) => {
    const currentAudience = currentForYouAudience();
    return capturedAudience.authenticated
      ? currentAudience.authenticated && currentAudience.viewerID === capturedAudience.viewerID
      : !currentAudience.authenticated;
  };

  const currentForYouRequest = (version: number, generation: number, capturedAudience: ForYouAudience) =>
    version === forYouRequestVersion
    && generation === authGeneration
    && isCurrentForYouAudience(capturedAudience);

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

  const currentForYouPage = (
    requestVersion: number,
    generation: number,
    pagingVersion: number,
    capturedAudience: ForYouAudience,
  ) => currentForYouRequest(requestVersion, generation, capturedAudience)
    && pagingVersion === forYouPagingVersion;

  const loadForYou = async (force = false) => {
    const capturedAudience = currentForYouAudience();
    if (forYou.loading && !force) {
      return;
    }
    if (forYou.loaded && !force) {
      return;
    }
    if (force) {
      engagementMutations.resetKind('like');
      resetForYou();
    }

    const version = ++forYouRequestVersion;
    const generation = authGeneration;
    forYou.loading = true;
    forYou.error = false;
    forYou.loadMoreError = false;
    forYou.depleted = false;

    try {
      const response = capturedAudience.authenticated
        ? await getPostRecommendations(HOME_FOR_YOU_PAGE_SIZE)
        : await getPublicPostRecommendations({
          limit: HOME_FOR_YOU_PAGE_SIZE,
          guestSessionId: getGuestRecommendationSessionID(),
        });
      if (!currentForYouRequest(version, generation, capturedAudience)) return;
      const newItems = appendForYouRecommendations(response.items);
      forYou.loaded = true;
      forYou.depleted = response.depleted;
      if (capturedAudience.authenticated) {
        void hydrateEngagementStates(
          newItems.map(({ post }) => post.id),
          () => currentForYouRequest(version, generation, capturedAudience),
        );
      }
    } catch {
      if (currentForYouRequest(version, generation, capturedAudience)) {
        forYou.error = true;
      }
    } finally {
      if (version === forYouRequestVersion && generation === authGeneration) {
        forYou.loading = false;
      }
    }
  };

  const loadMoreForYou = async () => {
    const capturedAudience = currentForYouAudience();
    if (
      !forYou.loaded
      || forYou.loading
      || forYou.loadingMore
      || forYou.loadMoreError
      || forYou.depleted
    ) {
      return;
    }

    const requestVersion = forYouRequestVersion;
    const generation = authGeneration;
    const pagingVersion = ++forYouPagingVersion;
    forYou.loadingMore = true;
    forYou.loadMoreError = false;

    try {
      const response = capturedAudience.authenticated
        ? await getPostRecommendations(HOME_FOR_YOU_PAGE_SIZE)
        : await getPublicPostRecommendations({
          limit: HOME_FOR_YOU_PAGE_SIZE,
          guestSessionId: getGuestRecommendationSessionID(),
        });
      if (!currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) return;
      const newItems = appendForYouRecommendations(response.items);
      if (response.depleted) {
        forYou.depleted = true;
        forYouNoProgressPages = 0;
      } else if (newItems.length > 0) {
        forYouNoProgressPages = 0;
      } else if (response.items.length > 0) {
        forYouNoProgressPages += 1;
        if (forYouNoProgressPages >= 2) {
          forYou.depleted = true;
        }
      }

      if (capturedAudience.authenticated) {
        void hydrateEngagementStates(
          newItems.map(({ post }) => post.id),
          () => currentForYouRequest(requestVersion, generation, capturedAudience),
        );
      }
    } catch {
      if (currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) {
        forYou.loadMoreError = true;
      }
    } finally {
      if (currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) {
        forYou.loadingMore = false;
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
      void hydrateEngagementStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(version, generation, capturedViewerID),
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
      void hydrateEngagementStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(requestVersion, generation, capturedViewerID),
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
          feedStore.isPostDeleted,
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
      void hydrateEngagementStates(
        freshPosts.map(post => post.id),
        () => currentFollowingRequest(version, generation, capturedViewerID),
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

  const toggleLike = async (postId: number): Promise<HomeEngagementMutationResult> => {
    const post = findPost(postId);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.likeStatus !== 'ready'
      || likePendingPostIds.has(postId)
      || !isAuthenticatedForViewer(capturedViewerID)
    ) {
      return 'ignored';
    }

    const lease = tryBeginEngagementMutationLease(capturedViewerID!, 'like', postId);
    if (!lease) return 'ignored';

    try {
      const previousLiked = post.liked;
      const previousLikes = post.likeCount;
      const capturedAuthGeneration = authGeneration;
      const token = engagementMutations.begin('like', postId);
      applyLikeStateUpdate(createOptimisticLikeUpdate(post), token.version);

      const isCurrent = () =>
        isAuthenticatedForViewer(capturedViewerID)
        && authGeneration === capturedAuthGeneration
        && engagementMutations.isCurrent(token);

      try {
        const result = await executeLikeToggle(postId, previousLiked);
        if (!isCurrent()) return 'ignored';
        applyLikeStateUpdate({
          postId,
          likes: result.likes,
          liked: result.liked,
          status: 'ready',
        }, token.version);
        engagementMutations.settle(token);
        return 'succeeded';
      } catch (error) {
        if (!isCurrent()) return 'ignored';
        applyLikeStateUpdate({
          postId,
          likes: previousLikes,
          liked: previousLiked,
          status: 'ready',
        }, token.version);
        if (getErrorStatus(error) === 503) {
          applyLikeStateUpdate({
            postId,
            likes: previousLikes,
            liked: previousLiked,
            status: 'unavailable',
          }, token.version);
        }
        engagementMutations.settle(token);
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const toggleRepost = async (postId: number): Promise<HomeEngagementMutationResult> => {
    const post = findPost(postId);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.repostStatus !== 'ready'
      || repostPendingPostIds.has(postId)
      || !isAuthenticatedForViewer(capturedViewerID)
    ) {
      return 'ignored';
    }

    const lease = tryBeginEngagementMutationLease(capturedViewerID!, 'repost', postId);
    if (!lease) return 'ignored';

    try {
      const previousReposted = post.reposted;
      const previousReposts = post.repostCount;
      const capturedAuthGeneration = authGeneration;
      const token = engagementMutations.begin('repost', postId);
      applyRepostStateUpdate(createOptimisticRepostUpdate(post), token.version);

      const isCurrent = () =>
        isAuthenticatedForViewer(capturedViewerID)
        && authGeneration === capturedAuthGeneration
        && engagementMutations.isCurrent(token);

      try {
        const result = await executeRepostToggle(postId, previousReposted);
        if (!isCurrent()) return 'ignored';
        applyRepostStateUpdate({
          postId,
          reposts: result.reposts,
          reposted: result.reposted,
          status: 'ready',
        }, token.version);
        engagementMutations.settle(token);
        markOwnProfileTimelineStale();
        return 'succeeded';
      } catch {
        if (!isCurrent()) return 'ignored';
        applyRepostStateUpdate({
          postId,
          reposts: previousReposts,
          reposted: previousReposted,
          status: 'ready',
        }, token.version);
        engagementMutations.settle(token);
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const toggleBookmark = async (postId: number): Promise<HomeEngagementMutationResult> => {
    const post = findPost(postId);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.bookmarkStatus !== 'ready'
      || bookmarkPendingPostIds.has(postId)
      || !isAuthenticatedForViewer(capturedViewerID)
    ) {
      return 'ignored';
    }

    const lease = tryBeginEngagementMutationLease(capturedViewerID!, 'bookmark', postId);
    if (!lease) return 'ignored';

    try {
      const previousBookmarked = post.bookmarked;
      beginBookmarkStateMutation(postId);
      const capturedAuthGeneration = authGeneration;
      const token = engagementMutations.begin('bookmark', postId);
      applyBookmarkStateUpdateLocal(createOptimisticBookmarkUpdate(post), token.version);

      const isCurrent = () =>
        isAuthenticatedForViewer(capturedViewerID)
        && authGeneration === capturedAuthGeneration
        && engagementMutations.isCurrent(token);

      try {
        const result = await executeBookmarkToggle(postId, previousBookmarked);
        if (!isCurrent()) return 'ignored';
        applyBookmarkStateUpdate({
          postId,
          bookmarked: result.bookmarked,
          status: 'ready',
        }, token.version);
        engagementMutations.settle(token);
        return 'succeeded';
      } catch {
        if (!isCurrent()) return 'ignored';
        applyBookmarkStateUpdateLocal({
          postId,
          bookmarked: previousBookmarked,
          status: 'ready',
        }, token.version);
        engagementMutations.settle(token);
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const removePostLocal = (postId: number) => {
    following.items = following.items.filter((post) => post.id !== postId);
    forYou.items = forYou.items.filter((item) => item.post.id !== postId);
    following.items.forEach(post => invalidateFeedPostReferences(post, postId));
    forYou.items.forEach(item => {
      invalidateFeedPostReferences(item.post, postId);
      normalizePostReferences(item.recommendation.post, id => id === postId);
    });
    followingLoadedPostIds.add(postId);
    engagementMutations.invalidatePost(postId);
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

    const quoteTargetPostID = post.quotePost?.id ?? null;

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
      const removed = removePost(postId, ownerUserID);
      if (removed && quoteTargetPostID !== null) {
        void refreshAndSyncPostQuoteCount(quoteTargetPostID);
      }
      return removed;
    } catch (error) {
      if (!isCurrent()) return false;
      if (getErrorStatus(error) === 404) {
        const removed = removePost(postId, ownerUserID);
        if (removed && quoteTargetPostID !== null) {
          void refreshAndSyncPostQuoteCount(quoteTargetPostID);
        }
        return removed;
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

  const retryForYouLoadMore = () => {
    if (!forYou.loadMoreError) return;
    forYou.loadMoreError = false;
    void loadMoreForYou();
  };

  registerHomeTimelineSync({
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyBookmarkStateUpdateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    applyQuoteCountUpdateLocal,
    reconcileFollowStateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
  });

  watch(
    [() => authStore.currentIdentity?.id, () => authStore.isAuthenticated],
    ([nextViewerID, authenticated]) => {
      setViewer(authenticated ? nextViewerID : null);
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
      const repostIDs = feedStore.recentlyPublishedPosts
        .filter((post) => post.repostStatus === 'unknown')
        .map((post) => post.id);
      const bookmarkIDs = feedStore.recentlyPublishedPosts
        .filter((post) => post.bookmarkStatus === 'unknown')
        .map((post) => post.id);
      const engagementIDs = Array.from(new Set([...ids, ...repostIDs, ...bookmarkIDs]));
      void hydrateEngagementStates(engagementIDs, () =>
        isAuthenticatedForViewer(capturedViewerID)
        && authGeneration === capturedAuthGeneration,
      );
    },
    { immediate: true },
  );

  return {
    viewerID,
    activeTab,
    forYou,
    following,
    scrollTop,
    homeReselectVersion,
    likePendingPostIds,
    repostPendingPostIds,
    bookmarkPendingPostIds,
    pendingDeletePostIds,
    deleteErrors,
    setViewer,
    setActiveTab,
    setScrollTop,
    requestHomeReselect,
    loadForYou,
    loadMoreForYou,
    retryForYouLoadMore,
    loadFollowing,
    loadMoreFollowing,
    revalidateFollowing,
    retryFollowingLoadMore,
    toggleLike,
    toggleRepost,
    toggleBookmark,
    findPost,
    applyLikeStateUpdate,
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyBookmarkStateUpdateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    applyQuoteCountUpdateLocal,
    reconcileFollowStateLocal,
    dismissRecommendation,
    removePost,
    removePostLocal,
    deletePost,
    replaceAuthorIdentity,
    replaceAuthorIdentityLocal,
  };
});
