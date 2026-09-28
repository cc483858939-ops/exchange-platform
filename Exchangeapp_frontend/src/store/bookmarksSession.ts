import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import {
  getBookmarks,
} from '../services/bookmarkService';
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
  applyFeedBookmarkStateUpdate,
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  setFeedPostBookmarkUnavailable,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import { useAuthStore } from './auth';
import {
  registerBookmarksSessionSync,
  syncExternalPostBookmarkState,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  beginBookmarkStateMutation,
} from './sessionSync';
import {
  createEngagementMutationCoordinator,
  type EngagementMutationRevision,
} from './engagementMutationCoordinator';
import {
  createOptimisticBookmarkUpdate,
  createOptimisticLikeUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';

const pageSize = 20;

type RemovedBookmarkSnapshot = {
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

export const useBookmarksSessionStore = defineStore('bookmarksSession', () => {
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
  const {
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
  } = engagementMutations;
  const mutationErrors = reactive(new Map<number, string>());

  const loadedPostIDs = new Set<number>();
  const deletedPostIDs = new Set<number>();
  const removedBookmarkSnapshots = new Map<number, RemovedBookmarkSnapshot>();
  let freshnessVersion = 0;

  const clearMutationState = () => {
    mutationErrors.clear();
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
    deletedPostIDs.clear();
    removedBookmarkSnapshots.clear();
    freshnessVersion += 1;
    clearMutationState();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = normalizeID(rawViewerID);
    if (nextViewerID === viewerID.value) return false;
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
  ) => requestVersion.value === capturedRequestVersion
    && isCurrentViewer(capturedViewerID, capturedGeneration);

  const findPost = (postID: number) => items.value.find(post => post.id === postID);

  const appendBookmarkPosts = (posts: Post[]) => {
    const additions: FeedPost[] = [];
    posts.forEach((post) => {
      if (
        deletedPostIDs.has(post.id)
        || loadedPostIDs.has(post.id)
        || removedBookmarkSnapshots.has(post.id)
      ) return;
      loadedPostIDs.add(post.id);
      const feedPost = postToFeedPost(post);
      // Membership in this endpoint is the initial positive bookmark state.
      feedPost.bookmarked = true;
      feedPost.bookmarkStatus = 'ready';
      additions.push(feedPost);
    });
    if (additions.length > 0) items.value = [...items.value, ...additions];
    return additions;
  };

  const markLikeUnavailable = (
    posts: FeedPost[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    posts.forEach((post) => {
      const revision = revisions.get(post.id);
      if (!revision || !engagementMutations.isRevisionCurrent('like', post.id, revision)) return;
      const current = findPost(post.id);
      if (current && current.likeStatus === 'unknown') setFeedPostLikeUnavailable(current);
    });
  };

  const hydrateLikeStates = async (
    posts: FeedPost[],
    capturedRequestVersion: number,
    capturedViewerID: number,
    capturedGeneration: number,
    responsePromise: ReturnType<typeof getPostEngagementStates>,
  ) => {
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;
    const hydrationGeneration = likeHydrationGeneration.value;
    const revisions = new Map(postIDs.map(postID => [
      postID,
      engagementMutations.captureRevision('like', postID),
    ]));
    const current = () => isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === likeHydrationGeneration.value;
    try {
      const response = await responsePromise;
      if (!current()) return;
      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const revision = revisions.get(postID);
        if (!revision || !engagementMutations.isRevisionCurrent('like', postID, revision)) return;
        const post = findPost(postID);
        if (!post) return;
        const like = states.get(postID)?.like;
        applyFeedLikeStateUpdate(post, {
          postId: postID,
          likes: like?.status === 'ready' ? like.likes : 0,
          liked: like?.status === 'ready' ? like.liked : false,
          status: like?.status === 'ready' ? 'ready' : 'unavailable',
        });
      });
    } catch {
      if (current()) markLikeUnavailable(posts, revisions);
    }
  };

  const markRepostUnavailable = (
    posts: FeedPost[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    posts.forEach((post) => {
      const revision = revisions.get(post.id);
      if (!revision || !engagementMutations.isRevisionCurrent('repost', post.id, revision)) return;
      const current = findPost(post.id);
      if (current && current.repostStatus === 'unknown') setFeedPostRepostUnavailable(current);
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
    const current = () => isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === repostHydrationGeneration.value;
    try {
      const response = await responsePromise;
      if (!current()) return;
      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const revision = revisions.get(postID);
        if (!revision || !engagementMutations.isRevisionCurrent('repost', postID, revision)) return;
        const post = findPost(postID);
        if (!post) return;
        const repost = states.get(postID)?.repost;
        applyFeedRepostStateUpdate(post, {
          postId: postID,
          reposts: repost?.status === 'ready' ? repost.reposts : 0,
          reposted: repost?.status === 'ready' ? repost.reposted : false,
          status: repost?.status === 'ready' ? 'ready' : 'unavailable',
        });
      });
    } catch {
      if (current()) markRepostUnavailable(posts, revisions);
    }
  };

  const markBookmarkUnavailable = (
    posts: FeedPost[],
    revisions: Map<number, EngagementMutationRevision>,
  ) => {
    posts.forEach((post) => {
      const revision = revisions.get(post.id);
      if (!revision || !engagementMutations.isRevisionCurrent('bookmark', post.id, revision)) return;
      const current = findPost(post.id);
      if (current && current.bookmarkStatus === 'unknown') setFeedPostBookmarkUnavailable(current);
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
    const current = () => isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === bookmarkHydrationGeneration.value;
    try {
      const response = await responsePromise;
      if (!current()) return;
      const states = new Map(response.items.map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const revision = revisions.get(postID);
        if (!revision || !engagementMutations.isRevisionCurrent('bookmark', postID, revision)) return;
        const post = findPost(postID);
        if (!post) return;
        const bookmark = states.get(postID)?.bookmark;
        if (bookmark?.status !== 'ready') return;
        if (!bookmark.bookmarked) {
          items.value = items.value.filter(candidate => candidate.id !== postID);
          loadedPostIDs.delete(postID);
          return;
        }
        applyFeedBookmarkStateUpdate(post, {
          postId: postID,
          bookmarked: true,
          status: 'ready',
        });
      });
    } catch {
      if (current()) markBookmarkUnavailable(posts, revisions);
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
    if (capturedViewerID === null || !authStore.isAuthenticated || initialLoading.value) return;
    if (loaded.value && !force) {
      if (stale.value) void revalidateBookmarks();
      return;
    }
    if (force) clearPageState();
    const capturedGeneration = viewerGeneration.value;
    const capturedFreshnessVersion = freshnessVersion;
    const capturedRequestVersion = ++requestVersion.value;
    pagingVersion.value += 1;
    initialLoading.value = true;
    initialError.value = '';
    loadMoreError.value = '';
    try {
      const response = await getBookmarks({ limit: pageSize });
      if (!isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) return;
      const newPosts = appendBookmarkPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      if (capturedFreshnessVersion === freshnessVersion) stale.value = false;
      revalidateError.value = '';
      void hydrateEngagementStates(newPosts, capturedRequestVersion, capturedViewerID, capturedGeneration);
    } catch {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        initialError.value = 'Bookmarks could not be loaded.';
      }
    } finally {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) initialLoading.value = false;
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
    ) return;
    const requestedCursor = nextCursor.value;
    const capturedGeneration = viewerGeneration.value;
    const capturedRequestVersion = requestVersion.value;
    const capturedPagingVersion = ++pagingVersion.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const response = await getBookmarks({ limit: pageSize, cursor: requestedCursor });
      if (
        !isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        || capturedPagingVersion !== pagingVersion.value
        || nextCursor.value !== requestedCursor
      ) return;
      const newPosts = appendBookmarkPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      void hydrateEngagementStates(newPosts, capturedRequestVersion, capturedViewerID, capturedGeneration);
    } catch {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value) {
        loadMoreError.value = 'Could not load more bookmarks.';
      }
    } finally {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value) loadingMore.value = false;
    }
  };

  const revalidateBookmarks = async () => {
    const capturedViewerID = viewerID.value;
    if (
      capturedViewerID === null
      || !authStore.isAuthenticated
      || !loaded.value
      || !stale.value
      || revalidating.value
    ) return;
    const capturedGeneration = viewerGeneration.value;
    const capturedRequestVersion = ++requestVersion.value;
    const capturedPagingVersion = ++pagingVersion.value;
    const capturedFreshnessVersion = freshnessVersion;
    revalidating.value = true;
    revalidateError.value = '';
    try {
      const response = await getBookmarks({ limit: pageSize });
      if (!isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        || capturedPagingVersion !== pagingVersion.value) return;
      const oldByID = new Map(items.value.map(post => [post.id, post]));
      const freshIDs = new Set<number>();
      const freshPosts: FeedPost[] = [];
      (response.items ?? []).forEach((post) => {
        if (freshIDs.has(post.id) || deletedPostIDs.has(post.id) || removedBookmarkSnapshots.has(post.id)) return;
        freshIDs.add(post.id);
        const next = postToFeedPost(post);
        next.bookmarked = true;
        next.bookmarkStatus = 'ready';
        const old = oldByID.get(post.id);
        freshPosts.push(old
          ? {
            ...next,
            ...(old.likeStatus !== 'unknown'
              ? { liked: old.liked, likeCount: old.likeCount, likeStatus: old.likeStatus }
              : {}),
            ...(old.repostStatus !== 'unknown'
              ? { reposted: old.reposted, repostCount: old.repostCount, repostStatus: old.repostStatus }
              : {}),
          }
          : next);
      });
      const cachedTail = items.value.filter(post => !deletedPostIDs.has(post.id) && !freshIDs.has(post.id));
      items.value = [...freshPosts, ...cachedTail];
      loadedPostIDs.clear();
      items.value.forEach(post => loadedPostIDs.add(post.id));
      nextCursor.value = response.next_cursor;
      if (capturedFreshnessVersion === freshnessVersion) stale.value = false;
      revalidating.value = false;
      void hydrateEngagementStates(freshPosts, capturedRequestVersion, capturedViewerID, capturedGeneration);
    } catch {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)) {
        stale.value = true;
        revalidateError.value = 'Bookmarks could not be refreshed.';
      }
    } finally {
      if (isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
        && capturedPagingVersion === pagingVersion.value) revalidating.value = false;
    }
  };

  const retryInitial = () => { void loadInitial(true); };
  const retryLoadMore = () => {
    loadMoreError.value = '';
    void loadMore();
  };

  const toggleLike = async (postID: number) => {
    const post = findPost(postID);
    if (!post || post.likeStatus !== 'ready' || likePendingPostIDs.has(postID)) return false;
    const previousLiked = post.liked;
    const previousLikes = post.likeCount;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('like', postID);
    applyFeedLikeStateUpdate(post, createOptimisticLikeUpdate(post));
    const current = () => isCurrentViewer(capturedViewerID!, capturedViewerGeneration)
      && engagementMutations.isCurrent(token);
    try {
      const result = await executeLikeToggle(postID, previousLiked);
      if (!current()) return false;
      applyFeedLikeStateUpdate(post, { postId: postID, likes: result.likes, liked: result.liked, status: 'ready' });
      engagementMutations.settle(token);
      syncExternalPostLikeState({ postId: postID, likes: result.likes, liked: result.liked, status: 'ready' });
      return true;
    } catch {
      if (!current()) return false;
      applyFeedLikeStateUpdate(post, { postId: postID, likes: previousLikes, liked: previousLiked, status: 'ready' });
      engagementMutations.settle(token);
      return false;
    }
  };

  const toggleRepost = async (postID: number) => {
    const post = findPost(postID);
    if (!post || post.repostStatus !== 'ready' || repostPendingPostIDs.has(postID)) return false;
    const previousReposted = post.reposted;
    const previousReposts = post.repostCount;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('repost', postID);
    applyFeedRepostStateUpdate(post, createOptimisticRepostUpdate(post));
    const current = () => isCurrentViewer(capturedViewerID!, capturedViewerGeneration)
      && engagementMutations.isCurrent(token);
    try {
      const result = await executeRepostToggle(postID, previousReposted);
      if (!current()) return false;
      applyFeedRepostStateUpdate(post, { postId: postID, reposts: result.reposts, reposted: result.reposted, status: 'ready' });
      engagementMutations.settle(token);
      syncExternalPostRepostState({ postId: postID, reposts: result.reposts, reposted: result.reposted, status: 'ready' });
      return true;
    } catch {
      if (!current()) return false;
      applyFeedRepostStateUpdate(post, { postId: postID, reposts: previousReposts, reposted: previousReposted, status: 'ready' });
      engagementMutations.settle(token);
      return false;
    }
  };

  const toggleBookmark = async (postID: number) => {
    const post = findPost(postID);
    if (!post || post.bookmarkStatus !== 'ready' || bookmarkPendingPostIDs.has(postID)) return false;
    const previousBookmarked = post.bookmarked;
    beginBookmarkStateMutation(postID);
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    const token = engagementMutations.begin('bookmark', postID);
    const optimisticUpdate = createOptimisticBookmarkUpdate(post);
    const originalIndex = items.value.findIndex(candidate => candidate.id === postID);
    if (previousBookmarked && originalIndex >= 0) {
      removedBookmarkSnapshots.set(postID, { post: { ...post }, originalIndex });
      items.value = items.value.filter(candidate => candidate.id !== postID);
    } else {
      applyFeedBookmarkStateUpdate(post, optimisticUpdate);
    }
    const current = () => isCurrentViewer(capturedViewerID!, capturedViewerGeneration)
      && engagementMutations.isCurrent(token);
    try {
      const result = await executeBookmarkToggle(postID, previousBookmarked);
      if (!current()) return false;
      engagementMutations.settle(token);
      removedBookmarkSnapshots.delete(postID);
      syncExternalPostBookmarkState({ postId: postID, bookmarked: result.bookmarked, status: 'ready' });
      return true;
    } catch {
      if (!current()) return false;
      engagementMutations.settle(token);
      const snapshot = removedBookmarkSnapshots.get(postID);
      if (snapshot && !findPost(postID)) {
        const next = [...items.value];
        next.splice(Math.min(snapshot.originalIndex, next.length), 0, snapshot.post);
        items.value = next;
        loadedPostIDs.add(postID);
      } else if (findPost(postID)) {
        applyFeedBookmarkStateUpdate(findPost(postID)!, { postId: postID, bookmarked: previousBookmarked, status: 'ready' });
      }
      removedBookmarkSnapshots.delete(postID);
      mutationErrors.set(postID, 'Could not update bookmark.');
      return false;
    }
  };

  const applyExternalBookmarkStateLocal = (update: FeedBookmarkStateUpdate) => {
    if (deletedPostIDs.has(update.postId)) return false;
    engagementMutations.invalidate('bookmark', update.postId);
    if (update.status === 'ready' && !update.bookmarked) {
      const existed = Boolean(findPost(update.postId) || removedBookmarkSnapshots.has(update.postId));
      items.value = items.value.filter(post => post.id !== update.postId);
      loadedPostIDs.delete(update.postId);
      removedBookmarkSnapshots.delete(update.postId);
      return existed;
    }
    const post = findPost(update.postId);
    if (post) return applyFeedBookmarkStateUpdate(post, update);
    if (update.status === 'ready' && update.bookmarked) {
      stale.value = true;
      freshnessVersion += 1;
      pagingVersion.value += 1;
      loadingMore.value = false;
      revalidating.value = false;
    }
    return false;
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    engagementMutations.invalidate('like', update.postId);
    const post = findPost(update.postId);
    return post ? applyFeedLikeStateUpdate(post, update) : false;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    engagementMutations.invalidate('repost', update.postId);
    const post = findPost(update.postId);
    return post ? applyFeedRepostStateUpdate(post, update) : false;
  };

  const removePostLocal = (postID: number) => {
    const existed = Boolean(findPost(postID) || removedBookmarkSnapshots.has(postID));
    deletedPostIDs.add(postID);
    items.value = items.value.filter(post => post.id !== postID);
    loadedPostIDs.delete(postID);
    removedBookmarkSnapshots.delete(postID);
    engagementMutations.invalidatePost(postID);
    return existed;
  };

  const replaceAuthorIdentityLocal = (author: PublicAuthor) => {
    let applied = false;
    items.value = items.value.map((post) => {
      const canonicalMatches = post.author.id === author.id;
      const actorMatches = post.repostContext?.actor.id === author.id;
      if (!canonicalMatches && !actorMatches) return post;
      applied = true;
      return {
        ...post,
        author: canonicalMatches ? author : post.author,
        repostContext: actorMatches ? { actor: author } : post.repostContext,
      };
    });
    return applied;
  };

  const saveScrollTop = (value: number) => {
    scrollTop.value = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  registerBookmarksSessionSync({
    applyExternalBookmarkStateLocal,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
  });

  watch(
    () => authStore.isAuthenticated ? authStore.currentIdentity?.id : null,
    nextViewerID => setViewer(nextViewerID ?? null),
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
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
    mutationErrors,
    setViewer,
    loadInitial,
    loadMore,
    retryInitial,
    retryLoadMore,
    revalidateBookmarks,
    toggleLike,
    toggleRepost,
    toggleBookmark,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
    saveScrollTop,
  };
});
