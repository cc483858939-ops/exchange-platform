import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import { getPostQuotes } from '../services/quoteService';
import { getPostEngagementStates } from '../services/engagementService';
import type { Post } from '../types/Post';
import type { FeedBookmarkStateUpdate, FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
import {
  createOptimisticBookmarkUpdate,
  createOptimisticLikeUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';
import {
  applyFeedBookmarkStateUpdate,
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  invalidateFeedPostReferences,
  setFeedPostBookmarkUnavailable,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import { normalizePostQuoteCountUpdate } from '../utils/quoteCount';
import {
  beginBookmarkStateMutation,
  registerQuotesSessionSync,
  syncQuotesBookmarkState,
  syncQuotesLikeState,
  syncQuotesRepostState,
  type PostQuoteCountUpdate,
} from './sessionSync';
import {
  createEngagementMutationCoordinator,
  type EngagementMutationResult,
  type EngagementMutationToken,
} from './engagementMutationCoordinator';
import {
  releaseEngagementMutationLease,
  tryBeginEngagementMutationLease,
} from './engagementMutationLease';

const pageSize = 20;

const normalizeID = (value: unknown): number | null => {
  const raw = Array.isArray(value) ? value[0] : value;
  const text = typeof raw === 'number' ? String(raw) : typeof raw === 'string' ? raw.trim() : '';
  const id = Number(text);
  return text !== '' && Number.isSafeInteger(id) && id > 0 ? id : null;
};

const normalizeCount = (value: unknown, fallback: number) => {
  const count = Number(value);
  return Number.isSafeInteger(count) && count >= 0 ? count : fallback;
};

const isNotFound = (error: unknown) => (
  typeof error === 'object'
  && error !== null
  && 'response' in error
  && typeof error.response === 'object'
  && error.response !== null
  && 'status' in error.response
  && error.response.status === 404
);

export const useQuotesSessionStore = defineStore('quotesSession', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const targetPostID = ref<number | null>(null);
  const items = ref<FeedPost[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextCursor = ref<string | null>(null);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const targetUnavailable = ref(false);
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const engagementMutations = createEngagementMutationCoordinator();
  const { likePendingPostIDs, repostPendingPostIDs, bookmarkPendingPostIDs } = engagementMutations;
  const mutationErrors = reactive(new Map<number, string>());
  const loadedPostIDs = new Set<number>();
  const deletedPostIDs = new Set<number>();

  const findPost = (postID: number) => items.value.find(post => post.id === postID);
  const isCurrentRequest = (request: number, target: number) => (
    requestVersion.value === request && targetPostID.value === target
  );

  const initializeGuestInteractionStates = (posts: FeedPost[]) => {
    posts.forEach((post) => {
      post.liked = false;
      post.likeStatus = 'ready';
      post.reposted = false;
      post.repostStatus = 'ready';
      post.bookmarked = false;
      post.bookmarkStatus = 'ready';
    });
  };

  const appendPosts = (posts: Post[]) => {
    const additions: FeedPost[] = [];
    posts.forEach((post) => {
      if (!Number.isSafeInteger(post.id) || post.id <= 0 || loadedPostIDs.has(post.id)
        || deletedPostIDs.has(post.id) || feedStore.isPostDeleted(post.id)) return;
      loadedPostIDs.add(post.id);
      additions.push(postToFeedPost(post, {}, id => deletedPostIDs.has(id) || feedStore.isPostDeleted(id)));
    });
    if (viewerID.value === null) initializeGuestInteractionStates(additions);
    if (additions.length > 0) items.value = [...items.value, ...additions];
    return additions;
  };

  const canApplyHydration = (
    target: number,
    request: number,
    capturedViewerID: number,
    generation: number,
  ) => isCurrentRequest(request, target)
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === generation
    && authStore.isAuthenticated;

  const hydrateEngagement = (posts: FeedPost[], target: number, request: number) => {
    const capturedViewerID = viewerID.value;
    if (capturedViewerID === null || !authStore.isAuthenticated) {
      initializeGuestInteractionStates(posts);
      return;
    }
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;

    const generation = viewerGeneration.value;
    const revisions = {
      like: new Map(postIDs.map(postID => [postID, engagementMutations.captureRevision('like', postID)])),
      repost: new Map(postIDs.map(postID => [postID, engagementMutations.captureRevision('repost', postID)])),
      bookmark: new Map(postIDs.map(postID => [postID, engagementMutations.captureRevision('bookmark', postID)])),
    };
    const current = () => canApplyHydration(target, request, capturedViewerID, generation);

    void getPostEngagementStates(postIDs).then((response) => {
      if (!current()) return;
      const states = new Map((response.items ?? []).map(item => [item.post_id, item]));
      postIDs.forEach((postID) => {
        const post = findPost(postID);
        if (!post) return;
        const state = states.get(postID);
        const likeRevision = revisions.like.get(postID);
        if (likeRevision && engagementMutations.isRevisionCurrent('like', postID, likeRevision)) {
          const like = state?.like;
          if (like?.status === 'ready') {
            applyFeedLikeStateUpdate(post, { postId: postID, likes: like.likes, liked: like.liked, status: 'ready' });
          } else {
            setFeedPostLikeUnavailable(post);
          }
        }
        const repostRevision = revisions.repost.get(postID);
        if (repostRevision && engagementMutations.isRevisionCurrent('repost', postID, repostRevision)) {
          const repost = state?.repost;
          if (repost?.status === 'ready') {
            applyFeedRepostStateUpdate(post, { postId: postID, reposts: repost.reposts, reposted: repost.reposted, status: 'ready' });
          } else {
            setFeedPostRepostUnavailable(post);
          }
        }
        const bookmarkRevision = revisions.bookmark.get(postID);
        if (bookmarkRevision && engagementMutations.isRevisionCurrent('bookmark', postID, bookmarkRevision)) {
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
        if (likeRevision && engagementMutations.isRevisionCurrent('like', postID, likeRevision)) setFeedPostLikeUnavailable(post);
        const repostRevision = revisions.repost.get(postID);
        if (repostRevision && engagementMutations.isRevisionCurrent('repost', postID, repostRevision)) setFeedPostRepostUnavailable(post);
        const bookmarkRevision = revisions.bookmark.get(postID);
        if (bookmarkRevision && engagementMutations.isRevisionCurrent('bookmark', postID, bookmarkRevision)) setFeedPostBookmarkUnavailable(post);
      });
    });
  };

  const clearPageState = () => {
    requestVersion.value += 1;
    pagingVersion.value += 1;
    items.value = [];
    loaded.value = false;
    initialLoading.value = false;
    initialError.value = '';
    nextCursor.value = null;
    loadingMore.value = false;
    loadMoreError.value = '';
    targetUnavailable.value = false;
    loadedPostIDs.clear();
    engagementMutations.resetAll();
    mutationErrors.clear();
  };

  const markTargetUnavailable = () => {
    requestVersion.value += 1;
    pagingVersion.value += 1;
    items.value = [];
    loaded.value = false;
    initialLoading.value = false;
    initialError.value = '';
    nextCursor.value = null;
    loadingMore.value = false;
    loadMoreError.value = '';
    targetUnavailable.value = true;
    loadedPostIDs.clear();
    engagementMutations.resetAll();
    mutationErrors.clear();
  };

  const loadInitial = async (force = false) => {
    if (force) clearPageState();
    const target = targetPostID.value;
    if (target === null || deletedPostIDs.has(target) || feedStore.isPostDeleted(target)) {
      markTargetUnavailable();
      return;
    }
    if (initialLoading.value || (loaded.value && !force)) return;
    const request = requestVersion.value;
    initialLoading.value = true;
    initialError.value = '';
    loadMoreError.value = '';
    pagingVersion.value += 1;
    try {
      const response = await getPostQuotes(target, { limit: pageSize });
      if (!isCurrentRequest(request, target)) return;
      const additions = appendPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      hydrateEngagement(additions, target, request);
    } catch (error) {
      if (!isCurrentRequest(request, target)) return;
      if (isNotFound(error)) {
        markTargetUnavailable();
      } else {
        initialError.value = 'Quotes could not be loaded.';
      }
    } finally {
      if (isCurrentRequest(request, target)) initialLoading.value = false;
    }
  };

  const loadMore = async () => {
    const target = targetPostID.value;
    const cursor = nextCursor.value;
    if (target === null || !loaded.value || !cursor || initialLoading.value || loadingMore.value || loadMoreError.value) return;
    const request = requestVersion.value;
    const paging = ++pagingVersion.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const response = await getPostQuotes(target, { limit: pageSize, cursor });
      if (!isCurrentRequest(request, target) || paging !== pagingVersion.value || nextCursor.value !== cursor) return;
      const additions = appendPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      hydrateEngagement(additions, target, request);
    } catch (error) {
      if (!isCurrentRequest(request, target) || paging !== pagingVersion.value) return;
      if (isNotFound(error)) {
        markTargetUnavailable();
      } else {
        loadMoreError.value = 'Could not load more quotes.';
      }
    } finally {
      if (isCurrentRequest(request, target) && paging === pagingVersion.value) loadingMore.value = false;
    }
  };

  const setTarget = async (rawTargetPostID: unknown) => {
    targetPostID.value = normalizeID(rawTargetPostID);
    clearPageState();
    if (targetPostID.value === null) {
      targetUnavailable.value = true;
      loaded.value = true;
      return;
    }
    await loadInitial();
  };

  const retryInitial = () => loadInitial(true);
  const retryLoadMore = () => {
    loadMoreError.value = '';
    return loadMore();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = authStore.isAuthenticated ? normalizeID(rawViewerID) : null;
    if (nextViewerID === viewerID.value) return;
    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    deletedPostIDs.clear();
    engagementMutations.resetAll();
    mutationErrors.clear();
    if (nextViewerID === null) {
      initializeGuestInteractionStates(items.value);
    } else if (targetPostID.value !== null) {
      hydrateEngagement(items.value, targetPostID.value, requestVersion.value);
    }
  };

  watch(
    () => [authStore.isAuthenticated, authStore.currentIdentity?.id] as const,
    ([, id]) => setViewer(id),
    { immediate: true },
  );

  const isCurrentMutation = (
    token: EngagementMutationToken,
    target: number,
    request: number,
    viewer: number,
    generation: number,
  ) => engagementMutations.isCurrent(token)
    && isCurrentRequest(request, target)
    && viewerID.value === viewer
    && viewerGeneration.value === generation
    && authStore.isAuthenticated;

  const toggleLike = async (postID: number): Promise<EngagementMutationResult> => {
    const post = findPost(postID);
    const viewer = viewerID.value;
    const target = targetPostID.value;
    if (!post || viewer === null || target === null || !authStore.isAuthenticated || post.likeStatus !== 'ready' || likePendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'like', postID);
    if (!lease) return 'ignored';
    try {
      const previous = { liked: post.liked, count: post.likeCount };
      const request = requestVersion.value;
      const generation = viewerGeneration.value;
      const token = engagementMutations.begin('like', postID);
      const optimistic = createOptimisticLikeUpdate(post);
      mutationErrors.delete(postID);
      applyFeedLikeStateUpdate(post, optimistic);
      try {
        const result = await executeLikeToggle(postID, previous.liked);
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        const update: FeedLikeStateUpdate = {
          postId: postID,
          likes: normalizeCount(result.likes, optimistic.likes),
          liked: typeof result.liked === 'boolean' ? result.liked : optimistic.liked,
          status: 'ready',
        };
        applyFeedLikeStateUpdate(post, update);
        engagementMutations.settle(token);
        syncQuotesLikeState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        applyFeedLikeStateUpdate(post, { postId: postID, likes: previous.count, liked: previous.liked, status: 'ready' });
        engagementMutations.settle(token);
        mutationErrors.set(postID, 'Could not update like.');
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const toggleRepost = async (postID: number): Promise<EngagementMutationResult> => {
    const post = findPost(postID);
    const viewer = viewerID.value;
    const target = targetPostID.value;
    if (!post || viewer === null || target === null || !authStore.isAuthenticated || post.repostStatus !== 'ready' || repostPendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'repost', postID);
    if (!lease) return 'ignored';
    try {
      const previous = { reposted: post.reposted, count: post.repostCount };
      const request = requestVersion.value;
      const generation = viewerGeneration.value;
      const token = engagementMutations.begin('repost', postID);
      const optimistic = createOptimisticRepostUpdate(post);
      mutationErrors.delete(postID);
      applyFeedRepostStateUpdate(post, optimistic);
      try {
        const result = await executeRepostToggle(postID, previous.reposted);
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        const update: FeedRepostStateUpdate = {
          postId: postID,
          reposts: normalizeCount(result.reposts, optimistic.reposts),
          reposted: typeof result.reposted === 'boolean' ? result.reposted : optimistic.reposted,
          status: 'ready',
        };
        applyFeedRepostStateUpdate(post, update);
        engagementMutations.settle(token);
        syncQuotesRepostState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        applyFeedRepostStateUpdate(post, { postId: postID, reposts: previous.count, reposted: previous.reposted, status: 'ready' });
        engagementMutations.settle(token);
        mutationErrors.set(postID, 'Could not update repost.');
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const toggleBookmark = async (postID: number): Promise<EngagementMutationResult> => {
    const post = findPost(postID);
    const viewer = viewerID.value;
    const target = targetPostID.value;
    if (!post || viewer === null || target === null || !authStore.isAuthenticated || post.bookmarkStatus !== 'ready' || bookmarkPendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'bookmark', postID);
    if (!lease) return 'ignored';
    try {
      const previous = post.bookmarked;
      const request = requestVersion.value;
      const generation = viewerGeneration.value;
      beginBookmarkStateMutation(postID);
      const token = engagementMutations.begin('bookmark', postID);
      const optimistic = createOptimisticBookmarkUpdate(post);
      mutationErrors.delete(postID);
      applyFeedBookmarkStateUpdate(post, optimistic);
      try {
        const result = await executeBookmarkToggle(postID, previous);
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        const update: FeedBookmarkStateUpdate = {
          postId: postID,
          bookmarked: typeof result.bookmarked === 'boolean' ? result.bookmarked : optimistic.bookmarked,
          status: 'ready',
        };
        applyFeedBookmarkStateUpdate(post, update);
        engagementMutations.settle(token);
        syncQuotesBookmarkState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, target, request, viewer, generation)) return 'ignored';
        applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: previous, status: 'ready' });
        engagementMutations.settle(token);
        mutationErrors.set(postID, 'Could not update bookmark.');
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  const reset = () => {
    targetPostID.value = null;
    clearPageState();
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    const post = findPost(update.postId);
    if (!post) return false;

    engagementMutations.invalidate('like', post.id);
    mutationErrors.delete(post.id);

    return applyFeedLikeStateUpdate(post, update);
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    const post = findPost(update.postId);
    if (!post) return false;

    engagementMutations.invalidate('repost', post.id);
    mutationErrors.delete(post.id);

    return applyFeedRepostStateUpdate(post, update);
  };

  const applyExternalBookmarkStateLocal = (update: FeedBookmarkStateUpdate) => {
    const post = findPost(update.postId);
    if (!post) return false;

    engagementMutations.invalidate('bookmark', post.id);
    mutationErrors.delete(post.id);

    return applyFeedBookmarkStateUpdate(post, update);
  };

  const applyQuoteCountUpdateLocal = (update: PostQuoteCountUpdate) => {
    const normalized = normalizePostQuoteCountUpdate(update);
    if (!normalized) return false;
    const post = findPost(normalized.postId);
    if (!post) return false;
    post.quoteCount = normalized.quoteCount;
    return true;
  };

  const removePostLocal = (postID: number) => {
    deletedPostIDs.add(postID);
    if (targetPostID.value === postID) {
      markTargetUnavailable();
      return;
    }
    items.value = items.value.filter(post => post.id !== postID);
    items.value.forEach(post => invalidateFeedPostReferences(post, postID));
    loadedPostIDs.delete(postID);
    engagementMutations.invalidatePost(postID);
    mutationErrors.delete(postID);
  };

  registerQuotesSessionSync({
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyQuoteCountUpdateLocal,
    removePostLocal,
  });

  return {
    targetPostID,
    items,
    loaded,
    initialLoading,
    initialError,
    nextCursor,
    loadingMore,
    loadMoreError,
    targetUnavailable,
    viewerID,
    viewerGeneration,
    requestVersion,
    pagingVersion,
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
    mutationErrors,
    setTarget,
    loadInitial,
    retryInitial,
    loadMore,
    retryLoadMore,
    setViewer,
    toggleLike,
    toggleRepost,
    toggleBookmark,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyQuoteCountUpdateLocal,
    removePostLocal,
    reset,
  };
});
