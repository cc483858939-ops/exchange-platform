import { defineStore } from 'pinia';
import { computed, onScopeDispose, reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import { getTopicPosts, type TopicSummary } from '../services/topicService';
import { releasePostViewSession } from '../services/postViewTelemetry';
import { hydratePostEngagement } from './engagementHydration';
import {
  createOptimisticBookmarkUpdate,
  createOptimisticLikeUpdate,
  createOptimisticRepostUpdate,
  executeBookmarkToggle,
  executeLikeToggle,
  executeRepostToggle,
} from './engagementOperations';
import type { Post } from '../types/Post';
import type { FeedBookmarkStateUpdate, FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import { normalizePostQuoteCountUpdate } from '../utils/quoteCount';
import {
  applyFeedBookmarkStateUpdate,
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  initializeGuestInteractionStates,
  postToFeedPost,
  invalidateFeedPostReferences,
} from '../utils/feedPost';
import {
  registerTopicSessionSync,
  syncTopicBookmarkState,
  syncTopicLikeState,
  syncTopicRepostState,
  beginBookmarkStateMutation,
} from './sessionSync';
import type { PostQuoteCountUpdate, PostReplyCountUpdate } from './sessionSync';
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

const normalizeID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const normalizeCount = (value: unknown, fallback: number) => {
  const count = Number(value);
  return Number.isSafeInteger(count) && count >= 0 ? count : fallback;
};

export const useTopicSessionStore = defineStore('topicSession', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const activeSlug = ref<string | null>(null);
  const topic = ref<TopicSummary | null>(null);
  const items = ref<FeedPost[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextCursor = ref<string | null>(null);
  const scrollTop = ref(0);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const viewSessionKey = computed(() => activeSlug.value
    ? `topic:${viewerID.value ?? 'anonymous'}:${activeSlug.value}:${requestVersion.value}:${viewerGeneration.value}`
    : '');
  watch(viewSessionKey, (key, previousKey) => {
    if (previousKey && previousKey !== key) releasePostViewSession(previousKey);
  }, { flush: 'sync' });
  onScopeDispose(() => {
    if (viewSessionKey.value) releasePostViewSession(viewSessionKey.value);
  });
  const engagementMutations = createEngagementMutationCoordinator();
  const {
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
  } = engagementMutations;
  const mutationErrors = reactive(new Map<number, string>());

  const loadedPostIDs = new Set<number>();
  const deletedPostIDs = new Set<number>();

  const findPost = (postID: number) => items.value.find(post => post.id === postID);
  const isCurrentRequest = (version: number, slug: string) => (
    requestVersion.value === version && activeSlug.value === slug
  );

  const appendPosts = (posts: Post[]) => {
    const additions: FeedPost[] = [];
    posts.forEach((post) => {
      if (loadedPostIDs.has(post.id) || (deletedPostIDs.has(post.id) || feedStore.isPostDeleted(post.id))) return;
      loadedPostIDs.add(post.id);
      additions.push(postToFeedPost(post, {}, id => deletedPostIDs.has(id) || feedStore.isPostDeleted(id)));
    });
    if (viewerID.value === null) initializeGuestInteractionStates(additions);
    if (additions.length > 0) items.value = [...items.value, ...additions];
    return additions;
  };

  const canApplyHydration = (
    slug: string,
    request: number,
    capturedViewerID: number,
    generation: number,
  ) => isCurrentRequest(request, slug)
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === generation
    && authStore.isAuthenticated;

  const hydrateEngagement = (posts: FeedPost[], slug: string, request: number) => {
    const capturedViewerID = viewerID.value;
    if (capturedViewerID === null || !authStore.isAuthenticated) {
      initializeGuestInteractionStates(posts);
      return;
    }
    const generation = viewerGeneration.value;
    hydratePostEngagement(posts, engagementMutations,
      () => canApplyHydration(slug, request, capturedViewerID, generation),
      findPost);
  };

  const clearPageState = () => {
    requestVersion.value += 1;
    pagingVersion.value += 1;
    topic.value = null;
    items.value = [];
    loaded.value = false;
    initialLoading.value = false;
    initialError.value = '';
    nextCursor.value = null;
    scrollTop.value = 0;
    loadingMore.value = false;
    loadMoreError.value = '';
    loadedPostIDs.clear();
    deletedPostIDs.clear();
    engagementMutations.resetAll();
    mutationErrors.clear();
  };

  const loadInitial = async (force = false) => {
    const slug = activeSlug.value;
    if (!slug || initialLoading.value) return;
    if (loaded.value && !force) return;
    if (force) clearPageState();
    const request = requestVersion.value;
    const capturedSlug = activeSlug.value;
    if (!capturedSlug) return;
    initialLoading.value = true;
    initialError.value = '';
    loadMoreError.value = '';
    pagingVersion.value += 1;
    try {
      const response = await getTopicPosts(capturedSlug, { limit: pageSize });
      if (!isCurrentRequest(request, capturedSlug)) return;
      topic.value = response.topic;
      const additions = appendPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      hydrateEngagement(additions, capturedSlug, request);
    } catch {
      if (isCurrentRequest(request, capturedSlug)) initialError.value = 'Topic posts could not be loaded.';
    } finally {
      if (isCurrentRequest(request, capturedSlug)) initialLoading.value = false;
    }
  };

  const loadMore = async () => {
    const slug = activeSlug.value;
    const cursor = nextCursor.value;
    if (!slug || !loaded.value || !cursor || initialLoading.value || loadingMore.value || loadMoreError.value) return;
    const request = requestVersion.value;
    const paging = ++pagingVersion.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const response = await getTopicPosts(slug, { limit: pageSize, cursor });
      if (!isCurrentRequest(request, slug) || paging !== pagingVersion.value || nextCursor.value !== cursor) return;
      const additions = appendPosts(response.items ?? []);
      nextCursor.value = response.next_cursor;
      hydrateEngagement(additions, slug, request);
    } catch {
      if (isCurrentRequest(request, slug) && paging === pagingVersion.value) {
        loadMoreError.value = 'Could not load more posts.';
      }
    } finally {
      if (isCurrentRequest(request, slug) && paging === pagingVersion.value) loadingMore.value = false;
    }
  };

  const retryInitial = () => loadInitial(true);
  const retryLoadMore = () => {
    loadMoreError.value = '';
    return loadMore();
  };

  const setTopic = async (rawSlug: unknown) => {
    const slug = typeof rawSlug === 'string' ? rawSlug.trim().toLowerCase() : '';
    if (slug === activeSlug.value) {
      if (slug && !loaded.value && !initialLoading.value) return loadInitial();
      return;
    }
    activeSlug.value = slug || null;
    clearPageState();
    if (slug) await loadInitial();
  };

  const reset = () => {
    activeSlug.value = null;
    clearPageState();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = authStore.isAuthenticated ? normalizeID(rawViewerID) : null;
    if (nextViewerID === viewerID.value) return;
    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    engagementMutations.resetAll();
    mutationErrors.clear();
    if (nextViewerID === null) {
      initializeGuestInteractionStates(items.value);
    } else if (activeSlug.value) {
      hydrateEngagement(items.value, activeSlug.value, requestVersion.value);
    }
  };

  watch(
    () => [authStore.isAuthenticated, authStore.currentIdentity?.id] as const,
    ([, id]) => setViewer(id),
    { immediate: true, flush: 'sync' },
  );

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    engagementMutations.invalidate('like', update.postId);
    mutationErrors.delete(update.postId);
    const post = findPost(update.postId);
    return post ? applyFeedLikeStateUpdate(post, update) : false;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    engagementMutations.invalidate('repost', update.postId);
    mutationErrors.delete(update.postId);
    const post = findPost(update.postId);
    return post ? applyFeedRepostStateUpdate(post, update) : false;
  };

  const applyExternalBookmarkStateLocal = (update: FeedBookmarkStateUpdate) => {
    engagementMutations.invalidate('bookmark', update.postId);
    mutationErrors.delete(update.postId);
    const post = findPost(update.postId);
    return post ? applyFeedBookmarkStateUpdate(post, update) : false;
  };

  const applyReplyCountUpdateLocal = (update: PostReplyCountUpdate) => {
    const replyCount = Number(update.replyCount);
    if (!Number.isSafeInteger(replyCount) || replyCount < 0) return false;
    const post = findPost(update.postId);
    if (!post) return false;
    post.replyCount = replyCount;
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
    const existed = Boolean(findPost(postID));
    deletedPostIDs.add(postID);
    items.value = items.value.filter(post => post.id !== postID);
    items.value.forEach(post => invalidateFeedPostReferences(post, postID));
    mutationErrors.delete(postID);
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

  registerTopicSessionSync({
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
    slug: string,
    request: number,
    viewer: number,
    viewerGenerationSnapshot: number,
  ) => (
    engagementMutations.isCurrent(token)
    && isCurrentRequest(request, slug)
    && viewerID.value === viewer
    && viewerGeneration.value === viewerGenerationSnapshot
    && authStore.isAuthenticated
  );

  const toggleLike = async (postID: number): Promise<EngagementMutationResult> => {
    const post = findPost(postID);
    const viewer = viewerID.value;
    if (
      !post
      || viewer === null
      || !authStore.isAuthenticated
      || post.likeStatus !== 'ready'
      || likePendingPostIDs.has(postID)
    ) return 'ignored';

    const lease = tryBeginEngagementMutationLease(viewer, 'like', postID);
    if (!lease) return 'ignored';

    try {
      const previous = { liked: post.liked, count: post.likeCount };
      const optimistic = createOptimisticLikeUpdate(post);
      const slug = activeSlug.value;
      const request = requestVersion.value;
      const viewerGenerationSnapshot = viewerGeneration.value;
      const token = engagementMutations.begin('like', postID);
      mutationErrors.delete(postID);
      applyFeedLikeStateUpdate(post, optimistic);
      try {
        const result = await executeLikeToggle(postID, previous.liked);
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
        const update: FeedLikeStateUpdate = {
          postId: postID,
          likes: normalizeCount(result.likes, optimistic.likes),
          liked: typeof result.liked === 'boolean' ? result.liked : optimistic.liked,
          status: 'ready',
        };
        applyFeedLikeStateUpdate(post, update);
        engagementMutations.settle(token);
        syncTopicLikeState(update);
        return 'succeeded';
      } catch {
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
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
    if (
      !post
      || viewer === null
      || !authStore.isAuthenticated
      || post.repostStatus !== 'ready'
      || repostPendingPostIDs.has(postID)
    ) return 'ignored';

    const lease = tryBeginEngagementMutationLease(viewer, 'repost', postID);
    if (!lease) return 'ignored';

    try {
      const previous = { reposted: post.reposted, count: post.repostCount };
      const optimistic = createOptimisticRepostUpdate(post);
      const slug = activeSlug.value;
      const request = requestVersion.value;
      const viewerGenerationSnapshot = viewerGeneration.value;
      const token = engagementMutations.begin('repost', postID);
      mutationErrors.delete(postID);
      applyFeedRepostStateUpdate(post, optimistic);
      try {
        const result = await executeRepostToggle(postID, previous.reposted);
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
        const update: FeedRepostStateUpdate = {
          postId: postID,
          reposts: normalizeCount(result.reposts, optimistic.reposts),
          reposted: typeof result.reposted === 'boolean' ? result.reposted : optimistic.reposted,
          status: 'ready',
        };
        applyFeedRepostStateUpdate(post, update);
        engagementMutations.settle(token);
        syncTopicRepostState(update);
        return 'succeeded';
      } catch {
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
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
    if (
      !post
      || viewer === null
      || !authStore.isAuthenticated
      || post.bookmarkStatus !== 'ready'
      || bookmarkPendingPostIDs.has(postID)
    ) return 'ignored';

    const lease = tryBeginEngagementMutationLease(viewer, 'bookmark', postID);
    if (!lease) return 'ignored';

    try {
      const previous = post.bookmarked;
      const optimistic = createOptimisticBookmarkUpdate(post);
      const slug = activeSlug.value;
      const request = requestVersion.value;
      const viewerGenerationSnapshot = viewerGeneration.value;
      beginBookmarkStateMutation(postID);
      const token = engagementMutations.begin('bookmark', postID);
      mutationErrors.delete(postID);
      applyFeedBookmarkStateUpdate(post, optimistic);
      try {
        const result = await executeBookmarkToggle(postID, previous);
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
        const update: FeedBookmarkStateUpdate = {
          postId: postID,
          bookmarked: typeof result.bookmarked === 'boolean' ? result.bookmarked : optimistic.bookmarked,
          status: 'ready',
        };
        applyFeedBookmarkStateUpdate(post, update);
        engagementMutations.settle(token);
        syncTopicBookmarkState(update);
        return 'succeeded';
      } catch {
        if (!slug || !isCurrentMutation(token, slug, request, viewer, viewerGenerationSnapshot)) return 'ignored';
        applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: previous, status: 'ready' });
        engagementMutations.settle(token);
        mutationErrors.set(postID, 'Could not update bookmark.');
        return 'failed';
      }
    } finally {
      releaseEngagementMutationLease(lease);
    }
  };

  return {
    activeSlug,
    topic,
    items,
    loaded,
    initialLoading,
    initialError,
    nextCursor,
    scrollTop,
    loadingMore,
    loadMoreError,
    viewerID,
    viewSessionKey,
    likePendingPostIDs,
    repostPendingPostIDs,
    bookmarkPendingPostIDs,
    mutationErrors,
    setTopic,
    reset,
    loadInitial,
    loadMore,
    retryInitial,
    retryLoadMore,
    setViewer,
    toggleLike,
    toggleRepost,
    toggleBookmark,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyExternalBookmarkStateLocal,
    applyReplyCountUpdateLocal,
    applyQuoteCountUpdateLocal,
    removePostLocal,
    replaceAuthorIdentityLocal,
    saveScrollTop,
  };
});
