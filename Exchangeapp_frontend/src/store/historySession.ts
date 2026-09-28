import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { getLikedHistory } from '../services/historyService';
import { getPostEngagementStates } from '../services/engagementService';
import type { Post } from '../types/Post';
import type {
  FeedBookmarkStateUpdate,
  FeedLikeStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
} from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  applyFeedBookmarkStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  setFeedPostLikeUnavailable,
  setFeedPostBookmarkUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import { useAuthStore } from './auth';
import {
  registerHistorySessionSync,
  markOwnProfileTimelineStale,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  syncHistoryBookmarkState,
  beginBookmarkStateMutation,
} from './sessionSync';
import type { PostReplyCountUpdate } from './sessionSync';
import {
  createEngagementMutationCoordinator,
  type EngagementMutationRevision,
} from './engagementMutationCoordinator';
import {
  createOptimisticBookmarkUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';

const pageSize = 20;

type RemovedSnapshot = {
  post: FeedPost;
  originalIndex: number;
};

const normalizeID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const normalizeCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) && Number.isInteger(count) && count >= 0 ? count : null;
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

export const useHistorySessionStore = defineStore('historySession', () => {
  const authStore = useAuthStore();

  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const items = ref<FeedPost[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextCursor = ref<string | null>(null);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const stale = ref(false);
  const revalidating = ref(false);
  const revalidateError = ref('');
  const scrollTop = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const likeHydrationGeneration = ref(0);
  const repostHydrationGeneration = ref(0);
  const bookmarkHydrationGeneration = ref(0);
  const engagementMutations = createEngagementMutationCoordinator();
  const pendingUnlikePostIDs = engagementMutations.likePendingPostIDs;
  const repostPendingPostIDs = engagementMutations.repostPendingPostIDs;
  const bookmarkPendingPostIDs = engagementMutations.bookmarkPendingPostIDs;
  const mutationErrors = ref(new Map<number, string>());

  const loadedPostIDs = new Set<number>();
  const removedSnapshots = new Map<number, RemovedSnapshot>();
  const deletedPostIDs = new Set<number>();
  let freshnessVersion = 0;

  const clearMutationState = () => {
    mutationErrors.value.clear();
    engagementMutations.resetAll();
  };

  const clearPageState = () => {
    requestVersion.value += 1;
    pagingVersion.value += 1;
    likeHydrationGeneration.value += 1;
    repostHydrationGeneration.value += 1;
    bookmarkHydrationGeneration.value += 1;
    items.value = [];
    loaded.value = false;
    initialLoading.value = false;
    initialError.value = '';
    nextCursor.value = null;
    loadingMore.value = false;
    loadMoreError.value = '';
    stale.value = false;
    revalidating.value = false;
    revalidateError.value = '';
    scrollTop.value = 0;
    loadedPostIDs.clear();
    removedSnapshots.clear();
    deletedPostIDs.clear();
    freshnessVersion += 1;
    clearMutationState();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = normalizeID(rawViewerID);
    if (nextViewerID === viewerID.value) {
      return false;
    }

    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    clearPageState();
    return true;
  };

  const isCurrentViewer = (capturedViewerID: number, capturedGeneration: number) => (
    authStore.isAuthenticated
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === capturedGeneration
  );

  const isCurrentRequest = (
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
  ) => (
    requestVersion.value === capturedRequestVersion
    && isCurrentViewer(capturedViewerID, capturedGeneration)
  );

  const findPost = (postID: number) =>
    items.value.find(post => post.id === postID);

  const updateSnapshotLikeState = (postID: number, update: FeedLikeStateUpdate) => {
    const snapshot = removedSnapshots.get(postID);
    if (!snapshot) {
      return false;
    }
    applyFeedLikeStateUpdate(snapshot.post, update);
    return true;
  };

  const updateSnapshotRepostState = (postID: number, update: FeedRepostStateUpdate) => {
    const snapshot = removedSnapshots.get(postID);
    if (!snapshot) {
      return false;
    }
    applyFeedRepostStateUpdate(snapshot.post, update);
    return true;
  };

  const removePostWithSnapshot = (postID: number) => {
    const index = items.value.findIndex(post => post.id === postID);
    if (index < 0) {
      return false;
    }
    const post = items.value[index];
    removedSnapshots.set(postID, { post: { ...post }, originalIndex: index });
    items.value = items.value.filter(candidate => candidate.id !== postID);
    return true;
  };

  const restoreSnapshot = (postID: number, update?: FeedLikeStateUpdate) => {
    if (deletedPostIDs.has(postID)) {
      removedSnapshots.delete(postID);
      return false;
    }
    const snapshot = removedSnapshots.get(postID);
    if (!snapshot || findPost(postID)) {
      return false;
    }

    const post = { ...snapshot.post };
    if (update) {
      applyFeedLikeStateUpdate(post, update);
    }
    const nextItems = [...items.value];
    nextItems.splice(Math.min(snapshot.originalIndex, nextItems.length), 0, post);
    items.value = nextItems;
    loadedPostIDs.add(postID);
    removedSnapshots.delete(postID);
    return true;
  };

  const appendHistoryPosts = (posts: Post[]) => {
    const additions: FeedPost[] = [];
    posts.forEach((post) => {
      if (
        deletedPostIDs.has(post.id)
        || loadedPostIDs.has(post.id)
        || removedSnapshots.has(post.id)
      ) {
        return;
      }
      loadedPostIDs.add(post.id);
      additions.push(postToFeedPost(post));
    });
    if (additions.length > 0) {
      items.value = [...items.value, ...additions];
    }
    return additions;
  };

  const hydrateLikeStates = async (
    posts: FeedPost[],
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
    responsePromise: ReturnType<typeof getPostEngagementStates>,
  ) => {
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) {
      return;
    }

    const hydrationGeneration = likeHydrationGeneration.value;
    const revisions = new Map(postIDs.map(postID => [
      postID,
      engagementMutations.captureRevision('like', postID),
    ]));
    const current = () => (
      isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === likeHydrationGeneration.value
    );

    try {
      const response = await responsePromise;
      if (!current()) {
        return;
      }

      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        if (deletedPostIDs.has(postID)) {
          return;
        }
        const revision = revisions.get(postID);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('like', postID, revision)
          || !findPost(postID)
        ) {
          return;
        }

        const like = states.get(postID)?.like;
        if (like?.status === 'ready' && !like.liked) {
          removePostWithSnapshot(postID);
          return;
        }

        const post = findPost(postID);
        if (post && like?.status === 'ready') {
          applyFeedLikeStateUpdate(post, {
            postId: postID,
            likes: like.likes,
            liked: true,
            status: 'ready',
          });
        } else if (post) {
          setFeedPostLikeUnavailable(post);
        }
      });
    } catch {
      if (!current()) {
        return;
      }
      postIDs.forEach((postID) => {
        if (deletedPostIDs.has(postID)) {
          return;
        }
        const revision = revisions.get(postID);
        const post = findPost(postID);
        if (
          revision
          && engagementMutations.isRevisionCurrent('like', postID, revision)
          && post
        ) {
          setFeedPostLikeUnavailable(post);
        }
      });
    }
  };

  const markRepostUnavailableLocal = (
    posts: FeedPost[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    posts.forEach((post) => {
      const revision = revisions.get(post.id);
      if (
        !revision
        || !engagementMutations.isRevisionCurrent('repost', post.id, revision)
      ) return;
      const currentPost = findPost(post.id);
      if (currentPost && currentPost.repostStatus === 'unknown') {
        setFeedPostRepostUnavailable(currentPost);
      }
    });
  };

  const hydrateRepostStates = async (
    posts: FeedPost[],
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
    responsePromise: ReturnType<typeof getPostEngagementStates>,
  ) => {
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;

    const hydrationGeneration = repostHydrationGeneration.value;
    const revisions = new Map(postIDs.map(postID => [
      postID,
      engagementMutations.captureRevision('repost', postID),
    ]));
    const current = () => (
      isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === repostHydrationGeneration.value
    );

    try {
      const response = await responsePromise;
      if (!current()) return;
      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const revision = revisions.get(postID);
        const post = findPost(postID);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('repost', postID, revision)
          || !post
        ) return;
        const repost = states.get(postID)?.repost;
        applyFeedRepostStateUpdate(post, {
          postId: postID,
          reposts: repost?.status === 'ready' ? repost.reposts : 0,
          reposted: repost?.status === 'ready' ? repost.reposted : false,
          status: repost?.status === 'ready' ? 'ready' : 'unavailable',
        });
      });
    } catch {
      if (current()) markRepostUnavailableLocal(posts, revisions);
    }
  };

  const markBookmarkUnavailableLocal = (
    posts: FeedPost[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    posts.forEach((post) => {
      const revision = revisions.get(post.id);
      if (
        !revision
        || !engagementMutations.isRevisionCurrent('bookmark', post.id, revision)
      ) return;
      const currentPost = findPost(post.id);
      if (currentPost && currentPost.bookmarkStatus === 'unknown') {
        setFeedPostBookmarkUnavailable(currentPost);
      }
    });
  };

  const hydrateBookmarkStates = async (
    posts: FeedPost[],
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
    responsePromise: ReturnType<typeof getPostEngagementStates>,
  ) => {
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;

    const hydrationGeneration = bookmarkHydrationGeneration.value;
    const revisions = new Map(postIDs.map(postID => [
      postID,
      engagementMutations.captureRevision('bookmark', postID),
    ]));
    const current = () => (
      isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === bookmarkHydrationGeneration.value
    );

    try {
      const response = await responsePromise;
      if (!current()) return;
      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const revision = revisions.get(postID);
        const post = findPost(postID);
        if (
          !revision
          || !engagementMutations.isRevisionCurrent('bookmark', postID, revision)
          || !post
        ) return;
        const bookmark = states.get(postID)?.bookmark;
        applyFeedBookmarkStateUpdate(post, {
          postId: postID,
          bookmarked: bookmark?.status === 'ready' ? bookmark.bookmarked : false,
          status: bookmark?.status === 'ready' ? 'ready' : 'unavailable',
        });
      });
    } catch {
      if (current()) markBookmarkUnavailableLocal(posts, revisions);
    }
  };

  const hydrateEngagementStates = (
    posts: FeedPost[],
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
  ) => {
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;
    const responsePromise = getPostEngagementStates(postIDs);
    void hydrateLikeStates(posts, capturedRequestVersion, capturedViewerID, capturedGeneration, responsePromise);
    void hydrateRepostStates(posts, capturedRequestVersion, capturedViewerID, capturedGeneration, responsePromise);
    void hydrateBookmarkStates(posts, capturedRequestVersion, capturedViewerID, capturedGeneration, responsePromise);
  };

  const loadInitial = async (force = false) => {
    const capturedViewerID = viewerID.value;
    if (capturedViewerID === null || !authStore.isAuthenticated) {
      return;
    }
    if (initialLoading.value) {
      return;
    }
    if (loaded.value && !force) {
      if (stale.value) void revalidateHistory();
      return;
    }
    if (force) {
      clearPageState();
    }

    const capturedGeneration = viewerGeneration.value;
    const capturedFreshnessVersion = freshnessVersion;
    const capturedRequestVersion = ++requestVersion.value;
    pagingVersion.value += 1;
    initialLoading.value = true;
    initialError.value = '';
    loadMoreError.value = '';
    try {
      const response = await getLikedHistory({ limit: pageSize });
      if (!isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        return;
      }
      const newPosts = appendHistoryPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      if (capturedFreshnessVersion === freshnessVersion) {
        stale.value = false;
      }
      revalidateError.value = '';
      void hydrateEngagementStates(
        newPosts,
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
    } catch {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        initialError.value = 'History could not be loaded.';
      }
    } finally {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        initialLoading.value = false;
      }
    }
  };

  const loadMore = async () => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !authStore.isAuthenticated
      || !loaded.value
      || !nextCursor.value
      || initialLoading.value
      || loadingMore.value
      || loadMoreError.value
      || stale.value
      || revalidating.value
    ) {
      return;
    }

    const requestedCursor = nextCursor.value;
    const capturedGeneration = viewerGeneration.value;
    const capturedRequestVersion = requestVersion.value;
    const capturedPagingVersion = ++pagingVersion.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const response = await getLikedHistory({ limit: pageSize, cursor: requestedCursor });
      if (
        !isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        || capturedPagingVersion !== pagingVersion.value
        || nextCursor.value !== requestedCursor
      ) {
        return;
      }
      const newPosts = appendHistoryPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      void hydrateEngagementStates(
        newPosts,
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
    } catch {
      if (
        isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value
      ) {
        loadMoreError.value = 'Could not load more posts.';
      }
    } finally {
      if (
        isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value
      ) {
        loadingMore.value = false;
      }
    }
  };

  const revalidateHistory = async () => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !authStore.isAuthenticated
      || !loaded.value
      || !stale.value
      || revalidating.value
    ) {
      return;
    }

    const capturedGeneration = viewerGeneration.value;
    const capturedRequestVersion = ++requestVersion.value;
    const capturedPagingVersion = ++pagingVersion.value;
    const capturedFreshnessVersion = freshnessVersion;
    revalidating.value = true;
    revalidateError.value = '';
    loadingMore.value = false;
    try {
      const response = await getLikedHistory({ limit: pageSize });
      if (!isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        return;
      }

      const oldItems = [...items.value];
      const oldByID = new Map(oldItems.map(post => [post.id, post]));
      const freshPosts: FeedPost[] = [];
      const freshIDs = new Set<number>();
      (response.items ?? []).forEach((post) => {
        if (freshIDs.has(post.id)) {
          return;
        }
        freshIDs.add(post.id);
        if (deletedPostIDs.has(post.id) || removedSnapshots.has(post.id)) {
          return;
        }
        const freshPost = postToFeedPost(post);
        const oldPost = oldByID.get(post.id);
        freshPosts.push(oldPost
          ? {
            ...freshPost,
            ...(oldPost.likeStatus !== 'unknown'
              ? { liked: oldPost.liked, likeCount: oldPost.likeCount, likeStatus: oldPost.likeStatus }
              : {}),
            ...(oldPost.repostStatus !== 'unknown'
              ? {
                reposted: oldPost.reposted,
                repostCount: oldPost.repostCount,
                repostStatus: oldPost.repostStatus,
              }
              : {}),
            ...(oldPost.bookmarkStatus !== 'unknown'
              ? {
                bookmarked: oldPost.bookmarked,
                bookmarkStatus: oldPost.bookmarkStatus,
              }
              : {}),
          }
          : freshPost);
      });

      const cachedTail = oldItems.filter(post => (
        !deletedPostIDs.has(post.id) && !freshIDs.has(post.id)
      ));
      items.value = [...freshPosts, ...cachedTail];
      loadedPostIDs.clear();
      items.value.forEach(post => loadedPostIDs.add(post.id));
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      if (capturedFreshnessVersion === freshnessVersion) {
        stale.value = false;
      }
      void hydrateEngagementStates(
        freshPosts.filter(post => (
          post.likeStatus === 'unknown'
          || post.repostStatus === 'unknown'
          || post.bookmarkStatus === 'unknown'
        )),
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
    } catch {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        stale.value = true;
        revalidateError.value = 'History could not be refreshed.';
      }
    } finally {
      if (
        isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value
      ) {
        revalidating.value = false;
      }
    }
  };

  const retryInitial = () => {
    void loadInitial(true);
  };

  const retryLoadMore = () => {
    if (!nextCursor.value) {
      return;
    }
    loadMoreError.value = '';
    void loadMore();
  };

  const toggleUnlike = async (postID: number) => {
    const index = items.value.findIndex(post => post.id === postID);
    const post = index >= 0 ? items.value[index] : undefined;
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.likeStatus !== 'ready'
      || !post.liked
      || capturedViewerID === null
      || !authStore.isAuthenticated
      || pendingUnlikePostIDs.has(postID)
    ) {
      return;
    }

    const capturedGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('like', postID);
    removedSnapshots.set(postID, { post: { ...post }, originalIndex: index });
    items.value = items.value.filter(candidate => candidate.id !== postID);
    mutationErrors.value.delete(postID);
    const isCurrentMutation = () => (
      isCurrentViewer(capturedViewerID, capturedGeneration)
      && engagementMutations.isCurrent(token)
    );

    try {
      const result = await executeLikeToggle(postID, true);
      if (!isCurrentMutation()) {
        return;
      }
      const likes = normalizeCount(result.likes) ?? removedSnapshots.get(postID)?.post.likeCount ?? 0;
      engagementMutations.settle(token);
      syncExternalPostLikeState({
        postId: postID,
        likes,
        liked: result.liked,
        status: 'ready',
      });
    } catch (error) {
      if (!isCurrentMutation()) {
        return;
      }
      const snapshot = removedSnapshots.get(postID);
      if (snapshot) {
        const restored = {
          ...snapshot.post,
          likeStatus: getErrorStatus(error) === 503 ? 'unavailable' as const : 'ready' as const,
        };
        const nextItems = [...items.value];
        nextItems.splice(Math.min(snapshot.originalIndex, nextItems.length), 0, restored);
        items.value = nextItems;
        removedSnapshots.delete(postID);
      }
      engagementMutations.settle(token);
      mutationErrors.value.set(
        postID,
        getErrorStatus(error) === 503
          ? 'Likes are temporarily unavailable.'
          : 'Could not remove this like.',
      );
    }
  };

  const toggleRepost = async (postID: number) => {
    const post = findPost(postID);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.repostStatus !== 'ready'
      || capturedViewerID === null
      || !authStore.isAuthenticated
      || repostPendingPostIDs.has(postID)
    ) return false;

    const previousReposted = post.reposted;
    const previousReposts = post.repostCount;
    const capturedViewerGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('repost', postID);
    applyFeedRepostStateUpdate(post, createOptimisticRepostUpdate(post));

    const isCurrentMutation = () => (
      isCurrentViewer(capturedViewerID, capturedViewerGeneration)
      && engagementMutations.isCurrent(token)
    );

    try {
      const response = await executeRepostToggle(postID, previousReposted);
      if (!isCurrentMutation()) return false;
      syncExternalPostRepostState({
        postId: postID,
        reposts: response.reposts,
        reposted: response.reposted,
        status: 'ready',
      });
      engagementMutations.settle(token);
      markOwnProfileTimelineStale();
      return true;
    } catch {
      if (!isCurrentMutation()) return false;
      applyFeedRepostStateUpdate(post, {
        postId: postID,
        reposts: previousReposts,
        reposted: previousReposted,
        status: 'ready',
      });
      engagementMutations.settle(token);
      return false;
    }
  };

  const toggleBookmark = async (postID: number) => {
    const post = findPost(postID);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.bookmarkStatus !== 'ready'
      || capturedViewerID === null
      || !authStore.isAuthenticated
      || bookmarkPendingPostIDs.has(postID)
    ) return false;

    const previousBookmarked = post.bookmarked;
    beginBookmarkStateMutation(postID);
    const capturedViewerGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('bookmark', postID);
    mutationErrors.value.delete(postID);
    applyFeedBookmarkStateUpdate(post, createOptimisticBookmarkUpdate(post));

    const isCurrentMutation = () => (
      isCurrentViewer(capturedViewerID, capturedViewerGeneration)
      && engagementMutations.isCurrent(token)
    );

    try {
      const response = await executeBookmarkToggle(postID, previousBookmarked);
      if (!isCurrentMutation()) return false;
      applyFeedBookmarkStateUpdate(post, {
        postId: postID,
        bookmarked: response.bookmarked,
        status: 'ready',
      });
      engagementMutations.settle(token);
      mutationErrors.value.delete(postID);
      syncHistoryBookmarkState({
        postId: postID,
        bookmarked: response.bookmarked,
        status: 'ready',
      });
      return true;
    } catch {
      if (!isCurrentMutation()) return false;
      applyFeedBookmarkStateUpdate(post, {
        postId: postID,
        bookmarked: previousBookmarked,
        status: 'ready',
      });
      engagementMutations.settle(token);
      mutationErrors.value.set(postID, 'Could not update bookmark.');
      return false;
    }
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    if (deletedPostIDs.has(update.postId)) {
      removedSnapshots.delete(update.postId);
      return false;
    }
    engagementMutations.invalidate('like', update.postId);
    mutationErrors.value.delete(update.postId);

    const post = findPost(update.postId);
    const snapshot = removedSnapshots.get(update.postId);
    if (update.status !== 'ready') {
      const applied = post
        ? applyFeedLikeStateUpdate(post, update)
        : updateSnapshotLikeState(update.postId, update);
      return applied;
    }

    if (!update.liked) {
      if (post) {
        removePostWithSnapshot(update.postId);
        return true;
      }
      return Boolean(snapshot);
    }

    if (post) {
      return applyFeedLikeStateUpdate(post, update);
    }
    if (snapshot) {
      return restoreSnapshot(update.postId, update);
    }

    stale.value = true;
    freshnessVersion += 1;
    pagingVersion.value += 1;
    loadingMore.value = false;
    revalidating.value = false;
    return false;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    if (deletedPostIDs.has(update.postId)) {
      return false;
    }
    engagementMutations.invalidate('repost', update.postId);
    const post = findPost(update.postId);
    const snapshot = removedSnapshots.get(update.postId);
    if (post) return applyFeedRepostStateUpdate(post, update);
    if (snapshot) return applyFeedRepostStateUpdate(snapshot.post, update);
    return false;
  };

  const applyExternalBookmarkStateLocal = (update: FeedBookmarkStateUpdate) => {
    if (deletedPostIDs.has(update.postId)) return false;
    engagementMutations.invalidate('bookmark', update.postId);
    const post = findPost(update.postId);
    if (post) return applyFeedBookmarkStateUpdate(post, update);
    const snapshot = removedSnapshots.get(update.postId);
    if (snapshot) return applyFeedBookmarkStateUpdate(snapshot.post, update);
    return false;
  };

  const applyReplyCountUpdateLocal = (update: PostReplyCountUpdate) => {
    const replyCount = normalizeCount(update.replyCount);
    if (replyCount === null) {
      return false;
    }
    let applied = false;
    const post = findPost(update.postId);
    if (post) {
      post.replyCount = replyCount;
      applied = true;
    }
    const snapshot = removedSnapshots.get(update.postId);
    if (snapshot) {
      snapshot.post.replyCount = replyCount;
      applied = true;
    }
    return applied;
  };

  const removePostLocal = (postID: number) => {
    const hadItem = Boolean(findPost(postID) || removedSnapshots.has(postID));
    deletedPostIDs.add(postID);
    items.value = items.value.filter(post => post.id !== postID);
    loadedPostIDs.delete(postID);
    removedSnapshots.delete(postID);
    engagementMutations.invalidatePost(postID);
    mutationErrors.value.delete(postID);
    return hadItem;
  };

  const replaceAuthorIdentityLocal = (author: PublicAuthor) => {
    let applied = false;
    items.value = items.value.map((post) => {
      const canonicalMatches = post.author.id === author.id;
      const actorMatches = post.repostContext?.actor.id === author.id;
      if (!canonicalMatches && !actorMatches) {
        return post;
      }
      applied = true;
      return {
        ...post,
        author: canonicalMatches ? author : post.author,
        repostContext: actorMatches ? { actor: author } : post.repostContext,
      };
    });
    removedSnapshots.forEach((snapshot) => {
      const canonicalMatches = snapshot.post.author.id === author.id;
      const actorMatches = snapshot.post.repostContext?.actor.id === author.id;
      if (!canonicalMatches && !actorMatches) return;
      snapshot.post = {
        ...snapshot.post,
        author: canonicalMatches ? author : snapshot.post.author,
        repostContext: actorMatches ? { actor: author } : snapshot.post.repostContext,
      };
      applied = true;
    });
    return applied;
  };

  const saveScrollTop = (value: number) => {
    scrollTop.value = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  registerHistorySessionSync({
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
  });

  watch(
    () => authStore.isAuthenticated ? authStore.currentIdentity?.id : null,
    nextViewerID => {
      setViewer(nextViewerID ?? null);
    },
    { immediate: true },
  );

  return {
    viewerID,
    viewerGeneration,
    items,
    loaded,
    initialLoading,
    initialError,
    nextCursor,
    loadingMore,
    loadMoreError,
    stale,
    revalidating,
    revalidateError,
    scrollTop,
    requestVersion,
    pagingVersion,
    likeHydrationGeneration,
    repostHydrationGeneration,
    bookmarkHydrationGeneration,
    pendingUnlikePostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
    mutationErrors,
    setViewer,
    loadInitial,
    loadMore,
    retryInitial,
    retryLoadMore,
    revalidateHistory,
    toggleUnlike,
    toggleRepost,
    toggleBookmark,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
    saveScrollTop,
  };
});
