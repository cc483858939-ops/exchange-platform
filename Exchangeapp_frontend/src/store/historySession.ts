import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { getLikedHistory } from '../services/historyService';
import { getArticleLikeStates, unlikeArticle } from '../services/likeService';
import {
  getArticleRepostStates,
  repostArticle,
  undoRepostArticle,
} from '../services/repostService';
import type { Article } from '../types/Article';
import type { FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  articleToFeedPost,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import { useAuthStore } from './auth';
import {
  registerHistorySessionSync,
  syncExternalArticleLikeState,
  syncExternalArticleRepostState,
} from './sessionSync';
import type { ArticleCommentCountUpdate } from './sessionSync';

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
  const scrollY = ref(0);
  const requestVersion = ref(0);
  const pagingVersion = ref(0);
  const likeHydrationGeneration = ref(0);
  const repostHydrationGeneration = ref(0);
  const pendingUnlikeArticleIDs = ref(new Set<number>());
  const repostPendingArticleIDs = ref(new Set<number>());
  const mutationErrors = ref(new Map<number, string>());

  const loadedArticleIDs = new Set<number>();
  const removedSnapshots = new Map<number, RemovedSnapshot>();
  const deletedArticleIDs = new Set<number>();
  const likeMutationVersions = reactive(new Map<number, number>());
  const repostMutationVersions = reactive(new Map<number, number>());
  let freshnessVersion = 0;
  let repostGeneration = 0;

  const getLikeMutationVersion = (articleID: number) =>
    likeMutationVersions.get(articleID) ?? 0;

  const bumpLikeMutationVersion = (articleID: number) => {
    const version = getLikeMutationVersion(articleID) + 1;
    likeMutationVersions.set(articleID, version);
    return version;
  };

  const getRepostMutationVersion = (articleID: number) =>
    repostMutationVersions.get(articleID) ?? 0;

  const bumpRepostMutationVersion = (articleID: number) => {
    const version = getRepostMutationVersion(articleID) + 1;
    repostMutationVersions.set(articleID, version);
    return version;
  };

  const clearMutationState = () => {
    pendingUnlikeArticleIDs.value.clear();
    mutationErrors.value.clear();
    likeMutationVersions.clear();
    repostPendingArticleIDs.value.clear();
    repostMutationVersions.clear();
    repostGeneration += 1;
  };

  const clearPageState = () => {
    requestVersion.value += 1;
    pagingVersion.value += 1;
    likeHydrationGeneration.value += 1;
    repostHydrationGeneration.value += 1;
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
    scrollY.value = 0;
    loadedArticleIDs.clear();
    removedSnapshots.clear();
    deletedArticleIDs.clear();
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

  const findPost = (articleID: number) =>
    items.value.find(post => post.id === articleID);

  const updateSnapshotLikeState = (articleID: number, update: FeedLikeStateUpdate) => {
    const snapshot = removedSnapshots.get(articleID);
    if (!snapshot) {
      return false;
    }
    applyFeedLikeStateUpdate(snapshot.post, update);
    return true;
  };

  const updateSnapshotRepostState = (articleID: number, update: FeedRepostStateUpdate) => {
    const snapshot = removedSnapshots.get(articleID);
    if (!snapshot) {
      return false;
    }
    applyFeedRepostStateUpdate(snapshot.post, update);
    return true;
  };

  const removePostWithSnapshot = (articleID: number) => {
    const index = items.value.findIndex(post => post.id === articleID);
    if (index < 0) {
      return false;
    }
    const post = items.value[index];
    removedSnapshots.set(articleID, { post: { ...post }, originalIndex: index });
    items.value = items.value.filter(candidate => candidate.id !== articleID);
    return true;
  };

  const restoreSnapshot = (articleID: number, update?: FeedLikeStateUpdate) => {
    if (deletedArticleIDs.has(articleID)) {
      removedSnapshots.delete(articleID);
      return false;
    }
    const snapshot = removedSnapshots.get(articleID);
    if (!snapshot || findPost(articleID)) {
      return false;
    }

    const post = { ...snapshot.post };
    if (update) {
      applyFeedLikeStateUpdate(post, update);
    }
    const nextItems = [...items.value];
    nextItems.splice(Math.min(snapshot.originalIndex, nextItems.length), 0, post);
    items.value = nextItems;
    loadedArticleIDs.add(articleID);
    removedSnapshots.delete(articleID);
    return true;
  };

  const appendHistoryArticles = (articles: Article[]) => {
    const additions: FeedPost[] = [];
    articles.forEach((article) => {
      if (
        deletedArticleIDs.has(article.ID)
        || loadedArticleIDs.has(article.ID)
        || removedSnapshots.has(article.ID)
      ) {
        return;
      }
      loadedArticleIDs.add(article.ID);
      additions.push(articleToFeedPost(article));
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
  ) => {
    const articleIDs = Array.from(new Set(posts.map(post => post.id)));
    if (articleIDs.length === 0) {
      return;
    }

    const hydrationGeneration = likeHydrationGeneration.value;
    const capturedMutationVersions = new Map(
      articleIDs.map(articleID => [articleID, getLikeMutationVersion(articleID)]),
    );
    const current = () => (
      isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === likeHydrationGeneration.value
    );

    try {
      const response = await getArticleLikeStates(articleIDs);
      if (!current()) {
        return;
      }

      const readyArticleIDs = new Set<number>();
      (response.items ?? []).forEach((item) => {
        if (deletedArticleIDs.has(item.article_id)) {
          return;
        }
        const capturedVersion = capturedMutationVersions.get(item.article_id);
        if (
          capturedVersion === undefined
          || getLikeMutationVersion(item.article_id) !== capturedVersion
          || !findPost(item.article_id)
        ) {
          return;
        }

        if (!item.liked) {
          removePostWithSnapshot(item.article_id);
          return;
        }

        readyArticleIDs.add(item.article_id);
        const post = findPost(item.article_id);
        if (post) {
          applyFeedLikeStateUpdate(post, {
            articleId: item.article_id,
            likes: item.likes,
            liked: true,
            status: 'ready',
          });
        }
      });

      (response.unavailable_article_ids ?? []).forEach((articleID) => {
        if (deletedArticleIDs.has(articleID) || readyArticleIDs.has(articleID)) {
          return;
        }
        const capturedVersion = capturedMutationVersions.get(articleID);
        const post = findPost(articleID);
        if (
          capturedVersion !== undefined
          && getLikeMutationVersion(articleID) === capturedVersion
          && post
        ) {
          setFeedPostLikeUnavailable(post);
        }
      });
    } catch {
      if (!current()) {
        return;
      }
      articleIDs.forEach((articleID) => {
        if (deletedArticleIDs.has(articleID)) {
          return;
        }
        const capturedVersion = capturedMutationVersions.get(articleID);
        const post = findPost(articleID);
        if (
          capturedVersion !== undefined
          && getLikeMutationVersion(articleID) === capturedVersion
          && post
        ) {
          setFeedPostLikeUnavailable(post);
        }
      });
    }
  };

  const markRepostUnavailableLocal = (posts: FeedPost[], versions: Map<number, number>) => {
    posts.forEach((post) => {
      const capturedVersion = versions.get(post.id);
      if (
        capturedVersion === undefined
        || getRepostMutationVersion(post.id) !== capturedVersion
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
  ) => {
    const articleIDs = Array.from(new Set(posts.map(post => post.id)));
    if (articleIDs.length === 0) return;

    const hydrationGeneration = repostHydrationGeneration.value;
    const capturedRepostGeneration = repostGeneration;
    const capturedMutationVersions = new Map(
      articleIDs.map(articleID => [articleID, getRepostMutationVersion(articleID)]),
    );
    const current = () => (
      isCurrentRequest(capturedRequestVersion, capturedViewerID, capturedGeneration)
      && hydrationGeneration === repostHydrationGeneration.value
      && repostGeneration === capturedRepostGeneration
    );

    try {
      const response = await getArticleRepostStates(articleIDs);
      if (!current()) return;
      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = capturedMutationVersions.get(item.article_id);
        const post = findPost(item.article_id);
        if (
          capturedVersion === undefined
          || getRepostMutationVersion(item.article_id) !== capturedVersion
          || !post
        ) return;
        readyIDs.add(item.article_id);
        applyFeedRepostStateUpdate(post, {
          articleId: item.article_id,
          reposts: item.reposts,
          reposted: item.reposted,
          status: 'ready',
        });
      });
      response.unavailable_article_ids.forEach((articleID) => {
        const capturedVersion = capturedMutationVersions.get(articleID);
        const post = findPost(articleID);
        if (
          readyIDs.has(articleID)
          || capturedVersion === undefined
          || getRepostMutationVersion(articleID) !== capturedVersion
          || !post
        ) return;
        applyFeedRepostStateUpdate(post, {
          articleId: articleID,
          reposts: 0,
          reposted: false,
          status: 'unavailable',
        });
      });
    } catch {
      if (current()) markRepostUnavailableLocal(posts, capturedMutationVersions);
    }
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
      const newPosts = appendHistoryArticles(response.items ?? []);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      if (capturedFreshnessVersion === freshnessVersion) {
        stale.value = false;
      }
      revalidateError.value = '';
      void hydrateLikeStates(
        newPosts,
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
      void hydrateRepostStates(
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
      const newPosts = appendHistoryArticles(response.items ?? []);
      nextCursor.value = response.next_cursor;
      void hydrateLikeStates(
        newPosts,
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
      void hydrateRepostStates(
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
      (response.items ?? []).forEach((article) => {
        if (freshIDs.has(article.ID)) {
          return;
        }
        freshIDs.add(article.ID);
        if (deletedArticleIDs.has(article.ID) || removedSnapshots.has(article.ID)) {
          return;
        }
        const freshPost = articleToFeedPost(article);
        const oldPost = oldByID.get(article.ID);
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
          }
          : freshPost);
      });

      const cachedTail = oldItems.filter(post => (
        !deletedArticleIDs.has(post.id) && !freshIDs.has(post.id)
      ));
      items.value = [...freshPosts, ...cachedTail];
      loadedArticleIDs.clear();
      items.value.forEach(post => loadedArticleIDs.add(post.id));
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      if (capturedFreshnessVersion === freshnessVersion) {
        stale.value = false;
      }
      const freshForHydration = freshPosts.filter(post => post.likeStatus === 'unknown');
      void hydrateLikeStates(
        freshForHydration,
        capturedRequestVersion,
        capturedViewerID,
        capturedGeneration,
      );
      void hydrateRepostStates(
        freshPosts.filter(post => post.repostStatus === 'unknown'),
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

  const toggleUnlike = async (articleID: number) => {
    const index = items.value.findIndex(post => post.id === articleID);
    const post = index >= 0 ? items.value[index] : undefined;
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.likeStatus !== 'ready'
      || !post.liked
      || capturedViewerID === null
      || pendingUnlikeArticleIDs.value.has(articleID)
    ) {
      return;
    }

    const capturedGeneration = viewerGeneration.value;
    const mutationVersion = bumpLikeMutationVersion(articleID);
    removedSnapshots.set(articleID, { post: { ...post }, originalIndex: index });
    items.value = items.value.filter(candidate => candidate.id !== articleID);
    pendingUnlikeArticleIDs.value.add(articleID);
    mutationErrors.value.delete(articleID);
    const isCurrentMutation = () => (
      isCurrentViewer(capturedViewerID, capturedGeneration)
      && getLikeMutationVersion(articleID) === mutationVersion
      && pendingUnlikeArticleIDs.value.has(articleID)
    );

    try {
      const result = await unlikeArticle(articleID);
      if (!isCurrentMutation()) {
        return;
      }
      const likes = normalizeCount(result.likes) ?? removedSnapshots.get(articleID)?.post.likeCount ?? 0;
      syncExternalArticleLikeState({
        articleId: articleID,
        likes,
        liked: result.liked,
        status: 'ready',
      });
    } catch (error) {
      if (!isCurrentMutation()) {
        return;
      }
      const snapshot = removedSnapshots.get(articleID);
      if (snapshot) {
        const restored = {
          ...snapshot.post,
          likeStatus: getErrorStatus(error) === 503 ? 'unavailable' as const : 'ready' as const,
        };
        const nextItems = [...items.value];
        nextItems.splice(Math.min(snapshot.originalIndex, nextItems.length), 0, restored);
        items.value = nextItems;
        removedSnapshots.delete(articleID);
      }
      pendingUnlikeArticleIDs.value.delete(articleID);
      mutationErrors.value.set(
        articleID,
        getErrorStatus(error) === 503
          ? 'Likes are temporarily unavailable.'
          : 'Could not remove this like.',
      );
    }
  };

  const toggleRepost = async (articleID: number) => {
    const post = findPost(articleID);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.repostStatus !== 'ready'
      || capturedViewerID === null
      || !authStore.isAuthenticated
      || repostPendingArticleIDs.value.has(articleID)
    ) return false;

    const previousReposted = post.reposted;
    const previousReposts = post.repostCount;
    const mutationVersion = bumpRepostMutationVersion(articleID);
    const capturedGeneration = repostGeneration;
    const capturedViewerGeneration = viewerGeneration.value;
    repostPendingArticleIDs.value.add(articleID);
    applyFeedRepostStateUpdate(post, {
      articleId: articleID,
      reposts: previousReposted ? Math.max(0, previousReposts - 1) : previousReposts + 1,
      reposted: !previousReposted,
      status: 'ready',
    });

    const isCurrentMutation = () => (
      isCurrentViewer(capturedViewerID, capturedViewerGeneration)
      && repostGeneration === capturedGeneration
      && getRepostMutationVersion(articleID) === mutationVersion
      && repostPendingArticleIDs.value.has(articleID)
    );

    try {
      const response = previousReposted
        ? await undoRepostArticle(articleID)
        : await repostArticle(articleID);
      if (!isCurrentMutation()) return false;
      repostMutationVersions.set(articleID, mutationVersion + 1);
      syncExternalArticleRepostState({
        articleId: articleID,
        reposts: response.reposts,
        reposted: response.reposted,
        status: 'ready',
      });
      repostPendingArticleIDs.value.delete(articleID);
      return true;
    } catch {
      if (!isCurrentMutation()) return false;
      repostMutationVersions.set(articleID, mutationVersion + 1);
      applyFeedRepostStateUpdate(post, {
        articleId: articleID,
        reposts: previousReposts,
        reposted: previousReposted,
        status: 'ready',
      });
      repostPendingArticleIDs.value.delete(articleID);
      return false;
    }
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    if (deletedArticleIDs.has(update.articleId)) {
      removedSnapshots.delete(update.articleId);
      return false;
    }
    bumpLikeMutationVersion(update.articleId);
    pendingUnlikeArticleIDs.value.delete(update.articleId);
    mutationErrors.value.delete(update.articleId);

    const post = findPost(update.articleId);
    const snapshot = removedSnapshots.get(update.articleId);
    if (update.status !== 'ready') {
      const applied = post
        ? applyFeedLikeStateUpdate(post, update)
        : updateSnapshotLikeState(update.articleId, update);
      return applied;
    }

    if (!update.liked) {
      if (post) {
        removePostWithSnapshot(update.articleId);
        return true;
      }
      return Boolean(snapshot);
    }

    if (post) {
      return applyFeedLikeStateUpdate(post, update);
    }
    if (snapshot) {
      return restoreSnapshot(update.articleId, update);
    }

    stale.value = true;
    freshnessVersion += 1;
    pagingVersion.value += 1;
    loadingMore.value = false;
    revalidating.value = false;
    return false;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    if (deletedArticleIDs.has(update.articleId)) {
      return false;
    }
    bumpRepostMutationVersion(update.articleId);
    repostPendingArticleIDs.value.delete(update.articleId);
    const post = findPost(update.articleId);
    const snapshot = removedSnapshots.get(update.articleId);
    if (post) return applyFeedRepostStateUpdate(post, update);
    if (snapshot) return applyFeedRepostStateUpdate(snapshot.post, update);
    return false;
  };

  const applyCommentCountUpdateLocal = (update: ArticleCommentCountUpdate) => {
    const commentCount = normalizeCount(update.commentCount);
    if (commentCount === null) {
      return false;
    }
    let applied = false;
    const post = findPost(update.articleId);
    if (post) {
      post.commentCount = commentCount;
      applied = true;
    }
    const snapshot = removedSnapshots.get(update.articleId);
    if (snapshot) {
      snapshot.post.commentCount = commentCount;
      applied = true;
    }
    return applied;
  };

  const removeArticleLocal = (articleID: number) => {
    const hadItem = Boolean(findPost(articleID) || removedSnapshots.has(articleID));
    deletedArticleIDs.add(articleID);
    items.value = items.value.filter(post => post.id !== articleID);
    loadedArticleIDs.delete(articleID);
    removedSnapshots.delete(articleID);
    pendingUnlikeArticleIDs.value.delete(articleID);
    repostPendingArticleIDs.value.delete(articleID);
    mutationErrors.value.delete(articleID);
    bumpLikeMutationVersion(articleID);
    bumpRepostMutationVersion(articleID);
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

  const saveScroll = (value: number) => {
    if (Number.isFinite(value) && value >= 0) {
      scrollY.value = value;
    }
  };

  registerHistorySessionSync({
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyCommentCountUpdateLocal,
    removeArticleLocal,
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
    scrollY,
    requestVersion,
    pagingVersion,
    likeHydrationGeneration,
    repostHydrationGeneration,
    pendingUnlikeArticleIDs,
    repostPendingArticleIDs,
    likeMutationVersions,
    repostMutationVersions,
    mutationErrors,
    setViewer,
    loadInitial,
    loadMore,
    retryInitial,
    retryLoadMore,
    revalidateHistory,
    toggleUnlike,
    toggleRepost,
    applyExternalLikeStateLocal,
    applyExternalRepostStateLocal,
    applyCommentCountUpdateLocal,
    removeArticleLocal,
    replaceAuthorIdentityLocal,
    saveScroll,
  };
});
