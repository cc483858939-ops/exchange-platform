import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { getTopicPosts, type TopicSummary } from '../services/topicService';
import { bookmarkPost, getPostBookmarkStates, unbookmarkPost } from '../services/bookmarkService';
import { getPostLikeStates, likePost, unlikePost } from '../services/likeService';
import { getPostRepostStates, repostPost, undoRepostPost } from '../services/repostService';
import type { Post } from '../types/Post';
import type { FeedBookmarkStateUpdate, FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
import {
  applyFeedBookmarkStateUpdate,
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  setFeedPostBookmarkUnavailable,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import {
  syncExternalPostBookmarkState,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
} from './sessionSync';

const pageSize = 20;
type MutationResult = 'succeeded' | 'failed' | 'ignored';
type MutationKind = 'like' | 'repost' | 'bookmark';

const normalizeID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const normalizeCount = (value: unknown, fallback: number) => {
  const count = Number(value);
  return Number.isSafeInteger(count) && count >= 0 ? count : fallback;
};

export const useTopicSessionStore = defineStore('topicSession', () => {
  const authStore = useAuthStore();
  const activeSlug = ref<string | null>(null);
  const topic = ref<TopicSummary | null>(null);
  const items = ref<FeedPost[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextCursor = ref<string | null>(null);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const likePendingPostIDs = reactive(new Set<number>());
  const repostPendingPostIDs = reactive(new Set<number>());
  const bookmarkPendingPostIDs = reactive(new Set<number>());
  const mutationErrors = reactive(new Map<number, string>());

  const loadedPostIDs = new Set<number>();
  const mutationVersions: Record<MutationKind, Map<number, number>> = {
    like: new Map(),
    repost: new Map(),
    bookmark: new Map(),
  };

  const pendingFor = (kind: MutationKind) => (
    kind === 'like' ? likePendingPostIDs
      : kind === 'repost' ? repostPendingPostIDs
        : bookmarkPendingPostIDs
  );

  const versionFor = (kind: MutationKind, postID: number) => mutationVersions[kind].get(postID) ?? 0;
  const bumpVersion = (kind: MutationKind, postID: number) => {
    const version = versionFor(kind, postID) + 1;
    mutationVersions[kind].set(postID, version);
    return version;
  };

  const findPost = (postID: number) => items.value.find(post => post.id === postID);
  const isCurrentRequest = (version: number, slug: string) => (
    requestVersion.value === version && activeSlug.value === slug
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
      if (loadedPostIDs.has(post.id)) return;
      loadedPostIDs.add(post.id);
      additions.push(postToFeedPost(post));
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

  const hydrateOneState = async <T extends { post_id: number }>(options: {
    kind: MutationKind;
    slug: string;
    request: number;
    capturedViewerID: number;
    generation: number;
    posts: FeedPost[];
    fetch: (ids: number[]) => Promise<{ items: T[]; unavailable_post_ids: number[] }>;
    apply: (post: FeedPost, item: T) => void;
    markUnavailable: (post: FeedPost) => void;
  }) => {
    const { kind, slug, request, capturedViewerID, generation, posts, fetch, apply, markUnavailable } = options;
    const postIDs = Array.from(new Set(posts.map(post => post.id)));
    if (postIDs.length === 0) return;
    const versions = new Map(postIDs.map(postID => [postID, versionFor(kind, postID)]));
    const current = () => canApplyHydration(slug, request, capturedViewerID, generation);
    try {
      const response = await fetch(postIDs);
      if (!current()) return;
      const updated = new Set<number>();
      response.items.forEach((item) => {
        if (versions.get(item.post_id) !== versionFor(kind, item.post_id)) return;
        const post = findPost(item.post_id);
        if (!post) return;
        apply(post, item);
        updated.add(item.post_id);
      });
      response.unavailable_post_ids.forEach((postID) => {
        if (versions.get(postID) !== versionFor(kind, postID)) return;
        const post = findPost(postID);
        if (post) markUnavailable(post);
        updated.add(postID);
      });
      postIDs.forEach((postID) => {
        if (updated.has(postID) || versions.get(postID) !== versionFor(kind, postID)) return;
        const post = findPost(postID);
        if (post) markUnavailable(post);
      });
    } catch {
      if (!current()) return;
      postIDs.forEach((postID) => {
        if (versions.get(postID) !== versionFor(kind, postID)) return;
        const post = findPost(postID);
        if (post) markUnavailable(post);
      });
    }
  };

  const hydrateEngagement = (posts: FeedPost[], slug: string, request: number) => {
    const capturedViewerID = viewerID.value;
    if (capturedViewerID === null || !authStore.isAuthenticated) {
      initializeGuestInteractionStates(posts);
      return;
    }
    const generation = viewerGeneration.value;
    void Promise.all([
      hydrateOneState({
        kind: 'like', slug, request, capturedViewerID, generation, posts,
        fetch: getPostLikeStates,
        apply: (post, item) => {
          const state = item as { post_id: number; likes: number; liked: boolean };
          applyFeedLikeStateUpdate(post, { postId: state.post_id, likes: state.likes, liked: state.liked, status: 'ready' });
        },
        markUnavailable: setFeedPostLikeUnavailable,
      }),
      hydrateOneState({
        kind: 'repost', slug, request, capturedViewerID, generation, posts,
        fetch: getPostRepostStates,
        apply: (post, item) => {
          const state = item as { post_id: number; reposts: number; reposted: boolean };
          applyFeedRepostStateUpdate(post, { postId: state.post_id, reposts: state.reposts, reposted: state.reposted, status: 'ready' });
        },
        markUnavailable: setFeedPostRepostUnavailable,
      }),
      hydrateOneState({
        kind: 'bookmark', slug, request, capturedViewerID, generation, posts,
        fetch: getPostBookmarkStates,
        apply: (post, item) => {
          const state = item as { post_id: number; bookmarked: boolean };
          applyFeedBookmarkStateUpdate(post, { postId: state.post_id, bookmarked: state.bookmarked, status: 'ready' });
        },
        markUnavailable: setFeedPostBookmarkUnavailable,
      }),
    ]);
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
    loadingMore.value = false;
    loadMoreError.value = '';
    loadedPostIDs.clear();
    likePendingPostIDs.clear();
    repostPendingPostIDs.clear();
    bookmarkPendingPostIDs.clear();
    mutationErrors.clear();
    Object.values(mutationVersions).forEach(versions => versions.clear());
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
    likePendingPostIDs.clear();
    repostPendingPostIDs.clear();
    bookmarkPendingPostIDs.clear();
    mutationErrors.clear();
    Object.values(mutationVersions).forEach(versions => versions.clear());
    if (nextViewerID === null) {
      initializeGuestInteractionStates(items.value);
    } else if (activeSlug.value) {
      hydrateEngagement(items.value, activeSlug.value, requestVersion.value);
    }
  };

  watch(
    () => [authStore.isAuthenticated, authStore.currentIdentity?.id] as const,
    ([, id]) => setViewer(id),
    { immediate: true },
  );

  const isCurrentMutation = (kind: MutationKind, postID: number, version: number, slug: string, request: number, viewer: number, generation: number) => (
    isCurrentRequest(request, slug)
    && viewerID.value === viewer
    && viewerGeneration.value === generation
    && authStore.isAuthenticated
    && versionFor(kind, postID) === version
    && pendingFor(kind).has(postID)
  );

  const toggleLike = async (postID: number): Promise<MutationResult> => {
    const post = findPost(postID);
    if (!post || viewerID.value === null || post.likeStatus !== 'ready' || likePendingPostIDs.has(postID)) return 'ignored';
    const previous = { liked: post.liked, count: post.likeCount };
    const expectedLiked = !previous.liked;
    const expectedCount = expectedLiked ? previous.count + 1 : Math.max(0, previous.count - 1);
    const version = bumpVersion('like', postID);
    const slug = activeSlug.value;
    const request = requestVersion.value;
    const viewer = viewerID.value;
    const generation = viewerGeneration.value;
    likePendingPostIDs.add(postID);
    mutationErrors.delete(postID);
    applyFeedLikeStateUpdate(post, { postId: postID, likes: expectedCount, liked: expectedLiked, status: 'ready' });
    try {
      const result = previous.liked ? await unlikePost(postID) : await likePost(postID);
      if (!slug || !isCurrentMutation('like', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('like', postID);
      const update: FeedLikeStateUpdate = {
        postId: postID,
        likes: normalizeCount(result.likes, expectedCount),
        liked: typeof result.liked === 'boolean' ? result.liked : expectedLiked,
        status: 'ready',
      };
      applyFeedLikeStateUpdate(post, update);
      likePendingPostIDs.delete(postID);
      syncExternalPostLikeState(update);
      return 'succeeded';
    } catch {
      if (!slug || !isCurrentMutation('like', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('like', postID);
      applyFeedLikeStateUpdate(post, { postId: postID, likes: previous.count, liked: previous.liked, status: 'ready' });
      likePendingPostIDs.delete(postID);
      mutationErrors.set(postID, 'Could not update like.');
      return 'failed';
    }
  };

  const toggleRepost = async (postID: number): Promise<MutationResult> => {
    const post = findPost(postID);
    if (!post || viewerID.value === null || post.repostStatus !== 'ready' || repostPendingPostIDs.has(postID)) return 'ignored';
    const previous = { reposted: post.reposted, count: post.repostCount };
    const expectedReposted = !previous.reposted;
    const expectedCount = expectedReposted ? previous.count + 1 : Math.max(0, previous.count - 1);
    const version = bumpVersion('repost', postID);
    const slug = activeSlug.value;
    const request = requestVersion.value;
    const viewer = viewerID.value;
    const generation = viewerGeneration.value;
    repostPendingPostIDs.add(postID);
    mutationErrors.delete(postID);
    applyFeedRepostStateUpdate(post, { postId: postID, reposts: expectedCount, reposted: expectedReposted, status: 'ready' });
    try {
      const result = previous.reposted ? await undoRepostPost(postID) : await repostPost(postID);
      if (!slug || !isCurrentMutation('repost', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('repost', postID);
      const update: FeedRepostStateUpdate = {
        postId: postID,
        reposts: normalizeCount(result.reposts, expectedCount),
        reposted: typeof result.reposted === 'boolean' ? result.reposted : expectedReposted,
        status: 'ready',
      };
      applyFeedRepostStateUpdate(post, update);
      repostPendingPostIDs.delete(postID);
      syncExternalPostRepostState(update);
      return 'succeeded';
    } catch {
      if (!slug || !isCurrentMutation('repost', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('repost', postID);
      applyFeedRepostStateUpdate(post, { postId: postID, reposts: previous.count, reposted: previous.reposted, status: 'ready' });
      repostPendingPostIDs.delete(postID);
      mutationErrors.set(postID, 'Could not update repost.');
      return 'failed';
    }
  };

  const toggleBookmark = async (postID: number): Promise<MutationResult> => {
    const post = findPost(postID);
    if (!post || viewerID.value === null || post.bookmarkStatus !== 'ready' || bookmarkPendingPostIDs.has(postID)) return 'ignored';
    const previous = post.bookmarked;
    const expectedBookmarked = !previous;
    const version = bumpVersion('bookmark', postID);
    const slug = activeSlug.value;
    const request = requestVersion.value;
    const viewer = viewerID.value;
    const generation = viewerGeneration.value;
    bookmarkPendingPostIDs.add(postID);
    mutationErrors.delete(postID);
    applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: expectedBookmarked, status: 'ready' });
    try {
      const result = previous ? await unbookmarkPost(postID) : await bookmarkPost(postID);
      if (!slug || !isCurrentMutation('bookmark', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('bookmark', postID);
      const update: FeedBookmarkStateUpdate = {
        postId: postID,
        bookmarked: typeof result.bookmarked === 'boolean' ? result.bookmarked : expectedBookmarked,
        status: 'ready',
      };
      applyFeedBookmarkStateUpdate(post, update);
      bookmarkPendingPostIDs.delete(postID);
      syncExternalPostBookmarkState(update);
      return 'succeeded';
    } catch {
      if (!slug || !isCurrentMutation('bookmark', postID, version, slug, request, viewer, generation)) return 'ignored';
      bumpVersion('bookmark', postID);
      applyFeedBookmarkStateUpdate(post, { postId: postID, bookmarked: previous, status: 'ready' });
      bookmarkPendingPostIDs.delete(postID);
      mutationErrors.set(postID, 'Could not update bookmark.');
      return 'failed';
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
    loadingMore,
    loadMoreError,
    viewerID,
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
  };
});
