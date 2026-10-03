import { defineStore } from 'pinia';
import { computed, reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import { getPostEngagementStates } from '../services/engagementService';
import { searchPosts } from '../services/postSearchService';
import type { Post } from '../types/Post';
import type { PublicAuthor } from '../types/User';
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
  registerPostSearchSessionSync,
  syncPostSearchBookmarkState,
  syncPostSearchLikeState,
  syncPostSearchRepostState,
  type PostQuoteCountUpdate,
  type PostReplyCountUpdate,
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

export type PostSearchCriteriaV1 = {
  query: string;
  authorId: number | null;
  time:
    | { kind: 'any' }
    | { kind: 'relative'; duration: '24h' | '7d' | '30d' }
    | { kind: 'custom'; from: string | null; to: string | null };
  sort: 'latest';
};

const defaultCriteria: PostSearchCriteriaV1 = {
  query: '',
  authorId: null,
  time: { kind: 'any' },
  sort: 'latest',
};

export const normalizePostSearchQuery = (value: string) => value.trim();

export const postSearchQueryError = (value: string) => {
  const length = Array.from(normalizePostSearchQuery(value)).length;
  if (length === 0) return '';
  if (length < 2) return 'Enter at least 2 characters to search posts.';
  if (length > 200) return 'Search text must be 200 characters or fewer.';
  return '';
};

const normalizeID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const normalizeCount = (value: number, fallback: number) => (
  Number.isSafeInteger(value) && value >= 0 ? value : fallback
);

const normalizeCustomTimestamp = (value: string | null) => {
  if (value === null || value.trim() === '') return null;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : undefined;
};

const resolveTime = (time: PostSearchCriteriaV1['time'], now: number) => {
  if (time.kind === 'any') return { from: null, to: null };
  if (time.kind === 'custom') {
    const from = normalizeCustomTimestamp(time.from);
    const to = normalizeCustomTimestamp(time.to);
    if (from === undefined || to === undefined) return null;
    return { from, to };
  }
  const durations = { '24h': 24 * 60 * 60_000, '7d': 7 * 24 * 60 * 60_000, '30d': 30 * 24 * 60 * 60_000 };
  return {
    from: new Date(now - durations[time.duration]).toISOString(),
    to: new Date(now).toISOString(),
  };
};

const normalizeCountOrZero = (value: number) => normalizeCount(value, 0);

export const usePostSearchSessionStore = defineStore('postSearchSession', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const criteria = ref<PostSearchCriteriaV1>({ ...defaultCriteria });
  const criteriaKey = ref('');
  const resolvedFrom = ref<string | null>(null);
  const resolvedTo = ref<string | null>(null);
  const items = ref<FeedPost[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextCursor = ref<string | null>(null);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const scrollTop = ref(0);
  const searchReselectVersion = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const engagementMutations = createEngagementMutationCoordinator();
  const { likePendingPostIDs, repostPendingPostIDs, bookmarkPendingPostIDs } = engagementMutations;
  const mutationErrors = reactive(new Map<number, string>());
  const loadedPostIDs = new Set<number>();
  const deletedPostIDs = new Set<number>();

  const findPost = (postID: number) => items.value.find(post => post.id === postID);
  const hasMore = computed(() => nextCursor.value !== null);

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

  const initializeUnknownInteractionStates = (posts: FeedPost[]) => {
    posts.forEach((post) => {
      post.likeStatus = 'unknown';
      post.repostStatus = 'unknown';
      post.bookmarkStatus = 'unknown';
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
    scrollTop.value = 0;
    loadedPostIDs.clear();
    deletedPostIDs.clear();
    engagementMutations.resetAll();
    mutationErrors.clear();
  };

  const appendPosts = (posts: Post[]) => {
    const additions: FeedPost[] = [];
    posts.forEach((post) => {
      if (!Number.isSafeInteger(post.id) || post.id <= 0 || loadedPostIDs.has(post.id) || (deletedPostIDs.has(post.id) || feedStore.isPostDeleted(post.id))) return;
      loadedPostIDs.add(post.id);
      additions.push(postToFeedPost(post, {}, id => deletedPostIDs.has(id) || feedStore.isPostDeleted(id)));
    });
    if (viewerID.value === null) initializeGuestInteractionStates(additions);
    if (additions.length > 0) items.value = [...items.value, ...additions];
    return additions;
  };

  const isCurrentRequest = (
    request: number,
    key: string,
    capturedViewerID: number,
    generation: number,
  ) => requestVersion.value === request
    && criteriaKey.value === key
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === generation;

  const hydrateEngagement = (posts: FeedPost[], request: number, key: string) => {
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
    const current = () => isCurrentRequest(request, key, capturedViewerID, generation)
      && authStore.isAuthenticated;

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

  const criteriaQueryValid = () => postSearchQueryError(criteria.value.query) === '' && criteria.value.query !== '';

  const requestOptions = (cursor?: string) => ({
    q: criteria.value.query,
    author_id: criteria.value.authorId ?? undefined,
    from: resolvedFrom.value ?? undefined,
    to: resolvedTo.value ?? undefined,
    sort: criteria.value.sort,
    limit: pageSize,
    ...(cursor ? { cursor } : {}),
  });

  const loadInitial = async () => {
    const capturedViewerID = viewerID.value;
    if (capturedViewerID === null || !criteriaQueryValid() || initialLoading.value) return;
    if (loaded.value) return;
    const capturedKey = criteriaKey.value;
    const request = requestVersion.value;
    const generation = viewerGeneration.value;
    initialLoading.value = true;
    initialError.value = '';
    loadMoreError.value = '';
    try {
      const page = await searchPosts(requestOptions());
      if (!isCurrentRequest(request, capturedKey, capturedViewerID, generation)) return;
      const additions = appendPosts(page.items ?? []);
      nextCursor.value = page.next_cursor;
      loaded.value = true;
      hydrateEngagement(additions, request, capturedKey);
    } catch {
      if (isCurrentRequest(request, capturedKey, capturedViewerID, generation)) {
        initialError.value = 'Could not search posts.';
      }
    } finally {
      if (isCurrentRequest(request, capturedKey, capturedViewerID, generation)) initialLoading.value = false;
    }
  };

  const activateCriteria = (nextCriteria: PostSearchCriteriaV1) => {
    const normalized: PostSearchCriteriaV1 = {
      query: normalizePostSearchQuery(nextCriteria.query),
      authorId: normalizeID(nextCriteria.authorId),
      time: nextCriteria.time.kind === 'custom'
        ? { kind: 'custom', from: nextCriteria.time.from, to: nextCriteria.time.to }
        : { ...nextCriteria.time },
      sort: 'latest',
    };
    const semanticCriteriaKey = JSON.stringify([
      normalized.query,
      normalized.authorId,
      normalized.time,
      normalized.sort,
    ]);
    const currentSemanticCriteriaKey = JSON.stringify([
      criteria.value.query,
      criteria.value.authorId,
      criteria.value.time,
      criteria.value.sort,
    ]);
    if (criteriaKey.value !== '' && semanticCriteriaKey === currentSemanticCriteriaKey) {
      criteria.value = normalized;
      return false;
    }
    const resolved = resolveTime(normalized.time, Date.now());
    const key = resolved === null
      ? JSON.stringify([normalized.query, normalized.authorId, normalized.time, normalized.sort])
      : JSON.stringify([normalized.query, normalized.authorId, resolved.from, resolved.to, normalized.sort]);
    if (key === criteriaKey.value) {
      criteria.value = normalized;
      return false;
    }

    criteria.value = normalized;
    criteriaKey.value = key;
    resolvedFrom.value = resolved?.from ?? null;
    resolvedTo.value = resolved?.to ?? null;
    clearPageState();
    if (viewerID.value !== null && resolved !== null && criteriaQueryValid()) void loadInitial();
    return true;
  };

  const reload = () => {
    const resolved = resolveTime(criteria.value.time, Date.now());
    if (resolved !== null) {
      resolvedFrom.value = resolved.from;
      resolvedTo.value = resolved.to;
      criteriaKey.value = JSON.stringify([
        criteria.value.query,
        criteria.value.authorId,
        resolved.from,
        resolved.to,
        criteria.value.sort,
      ]);
    }
    clearPageState();
    if (viewerID.value !== null && criteriaQueryValid()) void loadInitial();
  };

  const loadMore = async () => {
    const capturedViewerID = viewerID.value;
    const cursor = nextCursor.value;
    if (
      capturedViewerID === null
      || !loaded.value
      || !cursor
      || initialLoading.value
      || loadingMore.value
      || loadMoreError.value
    ) return;

    const capturedKey = criteriaKey.value;
    const request = requestVersion.value;
    const generation = viewerGeneration.value;
    const paging = ++pagingVersion.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const page = await searchPosts(requestOptions(cursor));
      if (
        !isCurrentRequest(request, capturedKey, capturedViewerID, generation)
        || paging !== pagingVersion.value
        || nextCursor.value !== cursor
      ) return;
      const additions = appendPosts(page.items ?? []);
      nextCursor.value = page.next_cursor;
      hydrateEngagement(additions, request, capturedKey);
    } catch {
      if (isCurrentRequest(request, capturedKey, capturedViewerID, generation) && paging === pagingVersion.value) {
        loadMoreError.value = 'Could not load more posts.';
      }
    } finally {
      if (isCurrentRequest(request, capturedKey, capturedViewerID, generation) && paging === pagingVersion.value) {
        loadingMore.value = false;
      }
    }
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = authStore.isAuthenticated ? normalizeID(rawViewerID) : null;
    if (nextViewerID === viewerID.value) return false;
    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    requestVersion.value += 1;
    pagingVersion.value += 1;
    initialLoading.value = false;
    loadingMore.value = false;
    loadMoreError.value = '';
    engagementMutations.resetAll();
    mutationErrors.clear();
    if (nextViewerID === null) {
      initializeGuestInteractionStates(items.value);
    } else if (items.value.length > 0) {
      initializeUnknownInteractionStates(items.value);
      hydrateEngagement(items.value, requestVersion.value, criteriaKey.value);
    } else if (criteriaQueryValid()) {
      loaded.value = false;
      void loadInitial();
    }
    return true;
  };

  const requestSearchReselect = () => { searchReselectVersion.value += 1; };
  const saveScrollTop = (value: number) => {
    scrollTop.value = Number.isFinite(value) && value >= 0 ? value : 0;
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

  const applyReplyCountUpdateLocal = (update: PostReplyCountUpdate) => {
    const post = findPost(update.postId);
    if (!post) return false;
    post.replyCount = normalizeCountOrZero(update.replyCount);
    return true;
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
    const existed = Boolean(findPost(postID));
    items.value = items.value.filter(post => post.id !== postID);
    items.value.forEach(post => invalidateFeedPostReferences(post, postID));
    loadedPostIDs.delete(postID);
    engagementMutations.invalidatePost(postID);
    mutationErrors.delete(postID);
    return existed;
  };

  const replaceAuthorIdentityLocal = (author: PublicAuthor) => {
    items.value.forEach((post) => {
      if (post.author.id === author.id) post.author = { ...author };
    });
  };

  registerPostSearchSessionSync({
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    applyQuoteCountUpdateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
  });

  const isCurrentMutation = (
    token: EngagementMutationToken,
    request: number,
    key: string,
    viewer: number,
    generation: number,
  ) => engagementMutations.isCurrent(token)
    && isCurrentRequest(request, key, viewer, generation)
    && authStore.isAuthenticated;

  const toggleLike = async (postID: number): Promise<EngagementMutationResult> => {
    const post = findPost(postID);
    const viewer = viewerID.value;
    if (!post || viewer === null || !authStore.isAuthenticated || post.likeStatus !== 'ready' || likePendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'like', postID);
    if (!lease) return 'ignored';
    try {
      const previous = { liked: post.liked, count: post.likeCount };
      const request = requestVersion.value;
      const key = criteriaKey.value;
      const generation = viewerGeneration.value;
      const token = engagementMutations.begin('like', postID);
      const optimistic = createOptimisticLikeUpdate(post);
      mutationErrors.delete(postID);
      applyFeedLikeStateUpdate(post, optimistic);
      try {
        const result = await executeLikeToggle(postID, previous.liked);
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
        const update: FeedLikeStateUpdate = {
          postId: postID,
          likes: normalizeCount(result.likes, optimistic.likes),
          liked: typeof result.liked === 'boolean' ? result.liked : optimistic.liked,
          status: 'ready',
        };
        applyFeedLikeStateUpdate(post, update);
        engagementMutations.settle(token);
        syncPostSearchLikeState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
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
    if (!post || viewer === null || !authStore.isAuthenticated || post.repostStatus !== 'ready' || repostPendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'repost', postID);
    if (!lease) return 'ignored';
    try {
      const previous = { reposted: post.reposted, count: post.repostCount };
      const request = requestVersion.value;
      const key = criteriaKey.value;
      const generation = viewerGeneration.value;
      const token = engagementMutations.begin('repost', postID);
      const optimistic = createOptimisticRepostUpdate(post);
      mutationErrors.delete(postID);
      applyFeedRepostStateUpdate(post, optimistic);
      try {
        const result = await executeRepostToggle(postID, previous.reposted);
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
        const update: FeedRepostStateUpdate = {
          postId: postID,
          reposts: normalizeCount(result.reposts, optimistic.reposts),
          reposted: typeof result.reposted === 'boolean' ? result.reposted : optimistic.reposted,
          status: 'ready',
        };
        applyFeedRepostStateUpdate(post, update);
        engagementMutations.settle(token);
        syncPostSearchRepostState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
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
    if (!post || viewer === null || !authStore.isAuthenticated || post.bookmarkStatus !== 'ready' || bookmarkPendingPostIDs.has(postID)) return 'ignored';
    const lease = tryBeginEngagementMutationLease(viewer, 'bookmark', postID);
    if (!lease) return 'ignored';
    try {
      const previous = post.bookmarked;
      const request = requestVersion.value;
      const key = criteriaKey.value;
      const generation = viewerGeneration.value;
      beginBookmarkStateMutation(postID);
      const token = engagementMutations.begin('bookmark', postID);
      const optimistic = createOptimisticBookmarkUpdate(post);
      mutationErrors.delete(postID);
      applyFeedBookmarkStateUpdate(post, optimistic);
      try {
        const result = await executeBookmarkToggle(postID, previous);
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
        const update: FeedBookmarkStateUpdate = {
          postId: postID,
          bookmarked: typeof result.bookmarked === 'boolean' ? result.bookmarked : optimistic.bookmarked,
          status: 'ready',
        };
        applyFeedBookmarkStateUpdate(post, update);
        engagementMutations.settle(token);
        syncPostSearchBookmarkState(update);
        return 'succeeded';
      } catch {
        if (!isCurrentMutation(token, request, key, viewer, generation)) return 'ignored';
        applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: previous, status: 'ready' });
        engagementMutations.settle(token);
        mutationErrors.set(postID, 'Could not update bookmark.');
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  watch(
    () => [authStore.isAuthenticated, authStore.currentIdentity?.id] as const,
    ([, id]) => setViewer(id),
    { immediate: true },
  );

  return {
    viewerID,
    viewerGeneration,
    criteria,
    criteriaKey,
    resolvedFrom,
    resolvedTo,
    items,
    loaded,
    initialLoading,
    initialError,
    nextCursor,
    hasMore,
    loadingMore,
    loadMoreError,
    scrollTop,
    searchReselectVersion,
    requestVersion,
    pagingVersion,
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
    mutationErrors,
    setViewer,
    activateCriteria,
    loadInitial,
    reload,
    loadMore,
    requestSearchReselect,
    saveScrollTop,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    applyQuoteCountUpdateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
    toggleLike,
    toggleRepost,
    toggleBookmark,
  };
});
