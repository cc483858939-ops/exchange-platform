import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import {
  deleteArticle,
  getFollowingTimeline,
} from '../services/articleService';
import { getArticleRecommendations } from '../services/recommendationService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import type { Article } from '../types/Article';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FeedLikeStateUpdate, FeedPost, FeedTab } from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  recommendationToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';
import {
  registerHomeTimelineSync,
  syncHomeArticleRemoval,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
} from './sessionSync';

export type HomeRecommendationItem = {
  article: RecommendedArticle;
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
};

const normalizeID = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    return null;
  }
  return value;
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

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
  });
  const scrollY = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 0,
  });
  const likePendingArticleIds = reactive(new Set<number>());
  const pendingDeleteArticleIds = reactive(new Set<number>());
  const deleteErrors = reactive(new Map<number, string>());

  const followingLoadedArticleIds = new Set<number>();
  const likeMutationVersions = new Map<number, number>();
  let authGeneration = 0;
  let forYouRequestVersion = 0;
  let followingRequestVersion = 0;
  let followingPagingVersion = 0;
  let likeGeneration = 0;

  const clearLikeWork = () => {
    likeGeneration += 1;
    likePendingArticleIds.clear();
    likeMutationVersions.clear();
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
    followingLoadedArticleIds.clear();
  };

  const clearForViewer = () => {
    authGeneration += 1;
    clearLikeWork();
    pendingDeleteArticleIds.clear();
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

  const getLikeMutationVersion = (articleId: number) =>
    likeMutationVersions.get(articleId) ?? 0;

  const bumpLikeMutationVersion = (articleId: number) => {
    const next = getLikeMutationVersion(articleId) + 1;
    likeMutationVersions.set(articleId, next);
    return next;
  };

  const forEachHomePost = (articleId: number, callback: (post: FeedPost) => void) => {
    if (feedStore.isArticleDeleted(articleId)) {
      return;
    }
    feedStore.recentlyPublishedPosts.forEach((post) => {
      if (post.id === articleId) callback(post);
    });
    following.items.forEach((post) => {
      if (post.id === articleId) callback(post);
    });
    forYou.items.forEach(({ post }) => {
      if (post.id === articleId) callback(post);
    });
  };

  const findPost = (articleId: number): FeedPost | undefined => {
    let found: FeedPost | undefined;
    forEachHomePost(articleId, (post) => {
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
      && getLikeMutationVersion(update.articleId) !== expectedVersion
    ) {
      return false;
    }
    let applied = false;
    forEachHomePost(update.articleId, (post) => {
      applied = applyFeedLikeStateUpdate(post, update) || applied;
    });
    return applied;
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

  const markUnavailableLocal = (articleIds: number[], versions: Map<number, number>) => {
    articleIds.forEach((articleId) => {
      const capturedVersion = versions.get(articleId);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(articleId) !== capturedVersion
      ) {
        return;
      }
      forEachHomePost(articleId, (post) => {
        if (post.likeStatus === 'unknown') {
          setFeedPostLikeUnavailable(post);
        }
      });
    });
  };

  const hydrateLikeStates = async (
    articleIds: number[],
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(articleIds));
    if (uniqueIDs.length === 0) return;

    const versions = new Map(uniqueIDs.map((id) => [id, getLikeMutationVersion(id)]));
    try {
      const response = await getArticleLikeStates(uniqueIDs);
      if (!isCurrent()) return;

      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.article_id);
        if (
          capturedVersion === undefined
          || getLikeMutationVersion(item.article_id) !== capturedVersion
          || !findPost(item.article_id)
        ) {
          return;
        }
        readyIDs.add(item.article_id);
        applyLikeStateUpdate({
          articleId: item.article_id,
          likes: item.likes,
          liked: item.liked,
          status: 'ready',
        }, capturedVersion);
      });
      response.unavailable_article_ids.forEach((articleId) => {
        const capturedVersion = versions.get(articleId);
        if (
          readyIDs.has(articleId)
          || capturedVersion === undefined
          || getLikeMutationVersion(articleId) !== capturedVersion
          || !findPost(articleId)
        ) {
          return;
        }
        applyLikeStateUpdate({
          articleId,
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

  const appendFollowingArticles = (articles: Article[]) => {
    const newPosts = articles
      .filter((article) => {
        if (followingLoadedArticleIds.has(article.ID)) return false;
        followingLoadedArticleIds.add(article.ID);
        return !feedStore.isArticleDeleted(article.ID);
      })
      .map(articleToFeedPost);
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
      const articles = await getArticleRecommendations(50);
      if (!currentForYouRequest(version, generation, capturedViewerID)) return;
      forYou.items = articles
        .filter((article) => !feedStore.isArticleDeleted(article.id))
        .map((article) => ({ article, post: recommendationToFeedPost(article) }));
      forYou.loaded = true;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        forYou.items.map(({ post }) => post.id),
        () => currentForYouRequest(version, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
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
      const newPosts = appendFollowingArticles(response.items);
      following.nextCursor = response.next_cursor;
      following.loaded = true;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(version, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
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
      const newPosts = appendFollowingArticles(response.items);
      following.nextCursor = response.next_cursor;
      const capturedLikeGeneration = likeGeneration;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        () => currentFollowingRequest(requestVersion, generation, capturedViewerID)
          && likeGeneration === capturedLikeGeneration,
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

  const toggleLike = async (articleId: number) => {
    const post = findPost(articleId);
    if (!post || post.likeStatus !== 'ready' || likePendingArticleIds.has(articleId)) {
      return;
    }

    const previousLiked = post.liked;
    const previousLikes = post.likeCount;
    const mutationVersion = bumpLikeMutationVersion(articleId);
    const capturedLikeGeneration = likeGeneration;
    const capturedAuthGeneration = authGeneration;
    const capturedViewerID = viewerID.value;
    likePendingArticleIds.add(articleId);
    applyLikeStateUpdate({
      articleId,
      likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
      liked: !previousLiked,
      status: 'ready',
    }, mutationVersion);

    const isCurrent = () =>
      isAuthenticatedForViewer(capturedViewerID)
      && authGeneration === capturedAuthGeneration
      && likeGeneration === capturedLikeGeneration
      && getLikeMutationVersion(articleId) === mutationVersion
      && likePendingArticleIds.has(articleId);

    try {
      const result = previousLiked
        ? await unlikeArticle(articleId)
        : await likeArticle(articleId);
      if (!isCurrent()) return;
      const settledVersion = bumpLikeMutationVersion(articleId);
      applyLikeStateUpdate({
        articleId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      }, settledVersion);
      likePendingArticleIds.delete(articleId);
    } catch (error) {
      if (!isCurrent()) return;
      const settledVersion = bumpLikeMutationVersion(articleId);
      applyLikeStateUpdate({
        articleId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'ready',
      }, settledVersion);
      if (getErrorStatus(error) === 503) {
        applyLikeStateUpdate({
          articleId,
          likes: previousLikes,
          liked: previousLiked,
          status: 'unavailable',
        }, settledVersion);
      }
      likePendingArticleIds.delete(articleId);
    }
  };

  const removeArticleLocal = (articleId: number) => {
    following.items = following.items.filter((post) => post.id !== articleId);
    forYou.items = forYou.items.filter((item) => item.post.id !== articleId);
    followingLoadedArticleIds.add(articleId);
    likePendingArticleIds.delete(articleId);
    likeMutationVersions.delete(articleId);
    pendingDeleteArticleIds.delete(articleId);
    deleteErrors.delete(articleId);
  };

  const dismissRecommendation = (articleId: number) => {
    const before = forYou.items.length;
    forYou.items = forYou.items.filter((item) => item.article.id !== articleId);
    return forYou.items.length !== before;
  };

  const removeArticle = (articleId: number, ownerUserID?: number) => {
    if (ownerUserID !== undefined) {
      if (!feedStore.markArticleDeleted(articleId, ownerUserID)) return false;
    }
    removeArticleLocal(articleId);
    if (ownerUserID !== undefined) {
      syncHomeArticleRemoval(articleId);
    }
    return true;
  };

  const deletePost = async (articleId: number) => {
    const ownerUserID = viewerID.value;
    const post = findPost(articleId);
    if (
      ownerUserID === null
      || !authStore.isAuthenticated
      || !post
      || post.author.id !== ownerUserID
      || pendingDeleteArticleIds.has(articleId)
    ) return false;

    const capturedAuthGeneration = authGeneration;
    const capturedViewerID = ownerUserID;
    pendingDeleteArticleIds.add(articleId);
    deleteErrors.delete(articleId);
    const isCurrent = () =>
      authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && authGeneration === capturedAuthGeneration
      && pendingDeleteArticleIds.has(articleId);

    try {
      await deleteArticle(articleId);
      if (!isCurrent()) return false;
      return removeArticle(articleId, ownerUserID);
    } catch (error) {
      if (!isCurrent()) return false;
      if (getErrorStatus(error) === 404) {
        return removeArticle(articleId, ownerUserID);
      }
      deleteErrors.set(
        articleId,
        getErrorStatus(error) === 403
          ? 'You can only delete your own posts.'
          : getErrorStatus(error) === 401
            ? 'Please log in again to delete this post.'
            : 'Could not delete post. Please try again.',
      );
      pendingDeleteArticleIds.delete(articleId);
      return false;
    }
  };

  const replaceAuthorIdentityLocal = (author: PublicAuthor) => {
    following.items = following.items.map((post) => (
      post.author.id === author.id ? { ...post, author } : post
    ));
    forYou.items = forYou.items.map((item) => (
      item.post.author.id === author.id
        ? { ...item, post: { ...item.post, author } }
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
    removeArticleLocal,
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
      .map((post) => `${post.id}:${post.likeStatus}`)
      .join(','),
    () => {
      const ids = feedStore.recentlyPublishedPosts
        .filter((post) => post.likeStatus === 'unknown')
        .map((post) => post.id);
      const capturedViewerID = viewerID.value;
      const capturedAuthGeneration = authGeneration;
      const capturedLikeGeneration = likeGeneration;
      if (!authStore.isAuthenticated || capturedViewerID === null || ids.length === 0) return;
      void hydrateLikeStates(ids, () =>
        isAuthenticatedForViewer(capturedViewerID)
        && authGeneration === capturedAuthGeneration
        && likeGeneration === capturedLikeGeneration,
      );
    },
    { immediate: true },
  );

  return {
    viewerID,
    activeTab,
    forYou,
    following,
    scrollY,
    likePendingArticleIds,
    pendingDeleteArticleIds,
    deleteErrors,
    setViewer,
    setActiveTab,
    setScrollY,
    loadForYou,
    loadFollowing,
    loadMoreFollowing,
    retryFollowingLoadMore,
    toggleLike,
    findPost,
    applyLikeStateUpdate,
    applyLikeStateUpdateLocal,
    dismissRecommendation,
    removeArticle,
    removeArticleLocal,
    deletePost,
    replaceAuthorIdentity,
    replaceAuthorIdentityLocal,
  };
});
