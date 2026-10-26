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
  type RecommendationPageResponse,
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
  nextCursor: string | null;
  needsCursorRebuild: boolean;
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
    nextCursor: null,
    needsCursorRebuild: false,
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
  const mutedAuthorIDs = reactive(new Set<number>());

  const followingLoadedPostIds = new Set<number>();
  const forYouLoadedPostIds = new Set<number>();
  let authGeneration = 0;
  let forYouRequestVersion = 0;
  let forYouPagingVersion = 0;
  let forYouGuestSessionID: string | null = null;
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
    forYou.nextCursor = null;
    forYou.needsCursorRebuild = false;
    forYouLoadedPostIds.clear();
    forYouGuestSessionID = null;
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
    mutedAuthorIDs.clear();
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

  const collectHomePostsForIDs = (postIDs: readonly number[]) => {
    const wanted = new Set(postIDs.filter(postID => !feedStore.isPostDeleted(postID)));
    const postsByID = new Map<number, FeedPost[]>();
    const collect = (post: FeedPost) => {
      if (!wanted.has(post.id)) return;
      const matches = postsByID.get(post.id);
      if (matches) matches.push(post);
      else postsByID.set(post.id, [post]);
    };

    feedStore.recentlyPublishedPosts.forEach(collect);
    following.items.forEach(collect);
    forYou.items.forEach(item => collect(item.post));
    return postsByID;
  };

  const isMutedFeedPost = (post: FeedPost) => (
    mutedAuthorIDs.has(post.author.id)
    || (post.repostContext !== undefined && mutedAuthorIDs.has(post.repostContext.actor.id))
  );

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

  const hydrateEngagementStates = (postIds: number[], isCurrent: () => boolean) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;

    const revisions = {
      like: new Map(uniqueIDs.map(id => [id, engagementMutations.captureRevision('like', id)])),
      repost: new Map(uniqueIDs.map(id => [id, engagementMutations.captureRevision('repost', id)])),
      bookmark: new Map(uniqueIDs.map(id => [id, engagementMutations.captureRevision('bookmark', id)])),
    };
    const responsePromise = loadPostEngagementIndex(uniqueIDs);
    void (async () => {
      let states: Awaited<typeof responsePromise>;
      try {
        states = await responsePromise;
      } catch {
        if (!isCurrent()) return;
        const postsByID = collectHomePostsForIDs(uniqueIDs);
        uniqueIDs.forEach((postId) => {
          const posts = postsByID.get(postId);
          if (!posts) return;

          const likeRevision = revisions.like.get(postId);
          if (likeRevision && engagementMutations.isRevisionCurrent('like', postId, likeRevision)) {
            posts.forEach((post) => {
              if (post.likeStatus === 'unknown') setFeedPostLikeUnavailable(post);
            });
          }

          const repostRevision = revisions.repost.get(postId);
          if (repostRevision && engagementMutations.isRevisionCurrent('repost', postId, repostRevision)) {
            posts.forEach((post) => {
              if (post.repostStatus === 'unknown') setFeedPostRepostUnavailable(post);
            });
          }

          const bookmarkRevision = revisions.bookmark.get(postId);
          if (bookmarkRevision && engagementMutations.isRevisionCurrent('bookmark', postId, bookmarkRevision)) {
            posts.forEach((post) => {
              if (post.bookmarkStatus === 'unknown') setFeedPostBookmarkUnavailable(post);
            });
          }
        });
        return;
      }

      if (!isCurrent()) return;
      const postsByID = collectHomePostsForIDs(uniqueIDs);

      uniqueIDs.forEach((postId) => {
        const revision = revisions.like.get(postId);
        const posts = postsByID.get(postId);
        if (
          !revision
          || !posts?.length
          || !engagementMutations.isRevisionCurrent('like', postId, revision)
        ) return;

        const like = states.get(postId)?.like;
        const update: FeedLikeStateUpdate = like?.status === 'ready'
          ? { postId, likes: like.likes, liked: like.liked, status: 'ready' }
          : { postId, likes: 0, liked: false, status: 'unavailable' };
        let applied = false;
        posts.forEach(post => {
          applied = applyFeedLikeStateUpdate(post, update) || applied;
        });
        if (applied) syncHomeLikeState(update);
      });

      uniqueIDs.forEach((postId) => {
        const revision = revisions.repost.get(postId);
        const posts = postsByID.get(postId);
        if (
          !revision
          || !posts?.length
          || !engagementMutations.isRevisionCurrent('repost', postId, revision)
        ) return;

        const repost = states.get(postId)?.repost;
        const update: FeedRepostStateUpdate = repost?.status === 'ready'
          ? { postId, reposts: repost.reposts, reposted: repost.reposted, status: 'ready' }
          : { postId, reposts: 0, reposted: false, status: 'unavailable' };
        let applied = false;
        posts.forEach(post => {
          applied = applyFeedRepostStateUpdate(post, update) || applied;
        });
        if (applied) syncHomeRepostState(update);
      });

      uniqueIDs.forEach((postId) => {
        const revision = revisions.bookmark.get(postId);
        const posts = postsByID.get(postId);
        if (
          !revision
          || !posts?.length
          || !engagementMutations.isRevisionCurrent('bookmark', postId, revision)
        ) return;

        const bookmark = states.get(postId)?.bookmark;
        const update: FeedBookmarkStateUpdate = {
          postId,
          bookmarked: bookmark?.status === 'ready' ? bookmark.bookmarked : false,
          status: bookmark?.status === 'ready' ? 'ready' : 'unavailable',
        };
        posts.forEach(post => applyFeedBookmarkStateUpdate(post, update));
      });
    })();
  };

  const appendFollowingPosts = (activities: TimelineItem[]) => {
    const newPosts = activities
      .filter((activity) => {
        const postID = activity.post.id;
        if (
          followingLoadedPostIds.has(postID)
          || feedStore.isPostDeleted(postID)
          || mutedAuthorIDs.has(activity.post.author.id)
          || mutedAuthorIDs.has(activity.actor.id)
        ) return false;
        followingLoadedPostIds.add(postID);
        return true;
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
        || mutedAuthorIDs.has(recommendation.post.author.id)
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

  const requestForYouPage = (
    capturedAudience: ForYouAudience,
    guestSessionID: string | null,
    cursor: string | null,
  ): Promise<RecommendationPageResponse> => {
    if (capturedAudience.authenticated) {
      return cursor
        ? getPostRecommendations(HOME_FOR_YOU_PAGE_SIZE, cursor)
        : getPostRecommendations(HOME_FOR_YOU_PAGE_SIZE);
    }

    return getPublicPostRecommendations({
      limit: HOME_FOR_YOU_PAGE_SIZE,
      ...(cursor ? { cursor } : {}),
      guestSessionId: guestSessionID,
    });
  };

  const applyForYouPage = (
    response: RecommendationPageResponse,
    previousCursor: string | null,
    capturedAudience: ForYouAudience,
    requestVersion: number,
    generation: number,
  ) => {
    if (response.depleted !== (response.continuation_status === 'exhausted')) {
      throw new Error('Invalid recommendation continuation status');
    }

    const nextCursor = response.depleted ? null : response.next_cursor || null;
    const newItems = appendForYouRecommendations(response.items);
    forYou.depleted = response.depleted;
    forYou.nextCursor = nextCursor;
    forYou.needsCursorRebuild = !response.depleted && !nextCursor;
    forYou.loadMoreError = !response.depleted && (
      forYou.needsCursorRebuild
      || newItems.length === 0
      || (previousCursor !== null && nextCursor === previousCursor)
    );

    if (capturedAudience.authenticated && newItems.length > 0) {
      void hydrateEngagementStates(
        newItems.map(({ post }) => post.id),
        () => currentForYouRequest(requestVersion, generation, capturedAudience),
      );
    }
  };

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
    forYou.needsCursorRebuild = false;

    try {
      const guestSessionID = capturedAudience.authenticated ? null : getGuestRecommendationSessionID();
      if (!capturedAudience.authenticated && !guestSessionID) {
        throw new Error('Guest recommendation session is unavailable');
      }
      forYouGuestSessionID = guestSessionID;
      const response = capturedAudience.authenticated
        ? await getPostRecommendations(HOME_FOR_YOU_PAGE_SIZE)
        : await getPublicPostRecommendations({
          limit: HOME_FOR_YOU_PAGE_SIZE,
          guestSessionId: guestSessionID,
        });
      if (!currentForYouRequest(version, generation, capturedAudience)) return;
      if (response.depleted !== (response.continuation_status === 'exhausted')) {
        throw new Error('Invalid recommendation continuation status');
      }
      const newItems = appendForYouRecommendations(response.items);
      forYou.loaded = true;
      forYou.depleted = response.depleted;
      forYou.nextCursor = response.depleted ? null : response.next_cursor || null;
      forYou.needsCursorRebuild = !response.depleted && !forYou.nextCursor;
      forYou.loadMoreError = forYou.needsCursorRebuild;
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

  const refreshForYouAfterOnboarding = (expectedViewerID: number) => {
    const expectedID = normalizeID(expectedViewerID);
    const authenticatedID = normalizeID(authStore.currentIdentity?.id);
    if (
      expectedID === null
      || expectedID !== authenticatedID
      || !authStore.isAuthenticated
      || activeTab.value !== 'for-you'
    ) return false;

    if (viewerID.value !== expectedID) setViewer(expectedID);
    resetForYou();
    void loadForYou();
    return true;
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

    const needsCursorRebuild = forYou.needsCursorRebuild || !forYou.nextCursor;
    if (needsCursorRebuild) {
      forYou.needsCursorRebuild = true;
      forYou.nextCursor = null;
    }
    const cursor = needsCursorRebuild ? null : forYou.nextCursor;
    const guestSessionID = capturedAudience.authenticated
      ? null
      : forYouGuestSessionID ?? getGuestRecommendationSessionID();
    if (!capturedAudience.authenticated && !guestSessionID) {
      forYou.loadMoreError = true;
      return;
    }
    forYouGuestSessionID = guestSessionID;

    const requestVersion = forYouRequestVersion;
    const generation = authGeneration;
    const pagingVersion = ++forYouPagingVersion;
    forYou.loadingMore = true;
    forYou.loadMoreError = false;

    try {
      const response = await requestForYouPage(capturedAudience, guestSessionID, cursor);
      if (!currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) return;
      applyForYouPage(response, cursor, capturedAudience, requestVersion, generation);
    } catch (error) {
      if (currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) {
        if (!needsCursorRebuild && cursor && getErrorStatus(error) === 410) {
          // Rebuild the cursor session once while preserving the loaded ID set.
          forYou.nextCursor = null;
          forYou.needsCursorRebuild = true;
          try {
            const response = await requestForYouPage(capturedAudience, guestSessionID, null);
            if (!currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) return;
            applyForYouPage(response, null, capturedAudience, requestVersion, generation);
          } catch {
            if (currentForYouPage(requestVersion, generation, pagingVersion, capturedAudience)) {
              forYou.nextCursor = null;
              forYou.needsCursorRebuild = true;
              forYou.loadMoreError = true;
            }
          }
        } else {
          if (needsCursorRebuild) {
            forYou.nextCursor = null;
            forYou.needsCursorRebuild = true;
          }
          forYou.loadMoreError = true;
        }
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
          || mutedAuthorIDs.has(post.author.id)
          || mutedAuthorIDs.has(activity.actor.id)
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

  const applyMutedAuthorStateLocal = (authorID: number, muted: boolean) => {
    if (!Number.isSafeInteger(authorID) || authorID <= 0) return false;
    const wasMuted = mutedAuthorIDs.has(authorID);
    if (wasMuted === muted) return false;
    if (muted) mutedAuthorIDs.add(authorID);
    else mutedAuthorIDs.delete(authorID);

    forYouRequestVersion += 1;
    forYouPagingVersion += 1;
    forYou.loading = false;
    forYou.loadingMore = false;
    forYou.loadMoreError = false;
    forYou.error = false;
    forYou.loaded = false;
    forYou.depleted = false;
    forYou.items = forYou.items.filter(({ post }) => !isMutedFeedPost(post));
    forYouLoadedPostIds.clear();
    forYou.items.forEach(({ post }) => forYouLoadedPostIds.add(post.id));

    followingRequestVersion += 1;
    followingPagingVersion += 1;
    following.loading = false;
    following.loadingMore = false;
    following.revalidating = false;
    following.error = false;
    following.loadMoreError = false;
    following.revalidateError = false;
    following.items = following.items.filter(post => !isMutedFeedPost(post));
    followingLoadedPostIds.clear();
    following.items.forEach(post => followingLoadedPostIds.add(post.id));
    following.stale = following.loaded;

    if (activeTab.value === 'for-you' && authStore.isAuthenticated) {
      void loadForYou();
    } else if (activeTab.value === 'following' && authStore.isAuthenticated) {
      void revalidateFollowing();
    }
    return true;
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
    refreshForYouAfterOnboarding,
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
    applyMutedAuthorStateLocal,
    dismissRecommendation,
    removePost,
    removePostLocal,
    deletePost,
    replaceAuthorIdentity,
    replaceAuthorIdentityLocal,
  };
});
