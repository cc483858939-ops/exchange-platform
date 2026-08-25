import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import {
  deleteArticle,
} from '../services/articleService';
import {
  followUser,
  getUser,
  getUserArticles,
  getUserFollowState,
  unfollowUser,
  type UserFollowState,
} from '../services/userService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import type { Article } from '../types/Article';
import type { FeedLikeStateUpdate, FeedPost } from '../types/Feed';
import type { PublicAuthor, PublicUser } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';
import {
  registerProfileSessionSync,
  syncProfileArticleRemoval,
  syncProfileAuthorIdentity,
  syncProfileLikeState,
} from './sessionSync';
import type { ArticleCommentCountUpdate } from './sessionSync';
import { syncProfileFollowState } from './sessionSync';

export type ProfileSessionEntry = {
  user: PublicUser | null;
  profileLoaded: boolean;
  profileLoading: boolean;
  profileError: string;
  profileNotFound: boolean;
  articles: FeedPost[];
  articlesLoaded: boolean;
  articlesInitialLoading: boolean;
  articlesLoadingMore: boolean;
  articlesInitialError: string;
  articlesLoadMoreError: string;
  nextCursor: string | null;
  hasMore: boolean;
  followState: UserFollowState | null;
  followLoaded: boolean;
  followLoading: boolean;
  followError: string;
  followPending: boolean;
  followActionError: string;
  scrollY: number;
  lastAccessedAt: number;
  loadedArticleIds: Set<number>;
  profileRequestVersion: number;
  articleRequestVersion: number;
  followRequestVersion: number;
  followMutationVersion: number;
};

export type ProfileSessionCapture = {
  userID: number;
  viewerID: number | null;
  viewerGeneration: number;
  profileRequestVersion: number;
};

const maxProfileSessions = 8;
const pageSize = 20;

const normalizeID = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    return null;
  }
  return value;
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const normalizeCommentCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) && Number.isInteger(count) && count >= 0 ? count : null;
};

export const useProfileSessionStore = defineStore('profileSession', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const sessions = reactive(new Map<number, ProfileSessionEntry>());
  const likePendingArticleIds = reactive(new Set<number>());
  const pendingDeleteArticleIds = reactive(new Set<number>());
  const deleteErrors = reactive(new Map<number, string>());
  const deleteTargetProfileIDs = new Map<number, number>();
  const deleteMutationVersions = new Map<number, number>();
  const likeMutationVersions = new Map<number, number>();
  let likeGeneration = 0;
  let accessClock = 0;

  const nextAccessTime = () => {
    accessClock = Math.max(accessClock + 1, Date.now());
    return accessClock;
  };

  const createSession = (): ProfileSessionEntry => ({
    user: null,
    profileLoaded: false,
    profileLoading: false,
    profileError: '',
    profileNotFound: false,
    articles: [],
    articlesLoaded: false,
    articlesInitialLoading: false,
    articlesLoadingMore: false,
    articlesInitialError: '',
    articlesLoadMoreError: '',
    nextCursor: null,
    hasMore: false,
    followState: null,
    followLoaded: false,
    followLoading: false,
    followError: '',
    followPending: false,
    followActionError: '',
    scrollY: 0,
    lastAccessedAt: nextAccessTime(),
    loadedArticleIds: new Set<number>(),
    profileRequestVersion: 0,
    articleRequestVersion: 0,
    followRequestVersion: 0,
    followMutationVersion: 0,
  });

  const enforceSessionLimit = () => {
    while (sessions.size > maxProfileSessions) {
      const candidates = Array.from(sessions.entries())
        .filter(([id]) => id !== viewerID.value)
        .sort(([, a], [, b]) => a.lastAccessedAt - b.lastAccessedAt);
      const candidate = candidates[0] || Array.from(sessions.entries())
        .sort(([, a], [, b]) => a.lastAccessedAt - b.lastAccessedAt)[0];
      if (!candidate) return;
      sessions.delete(candidate[0]);
    }
  };

  const touchSession = (userID: number) => {
    const session = sessions.get(userID);
    if (!session) return null;
    session.lastAccessedAt = nextAccessTime();
    return session;
  };

  const ensureSession = (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    if (userID === null) return null;
    let session: ProfileSessionEntry | null = sessions.get(userID) || null;
    if (!session) {
      session = createSession();
      sessions.set(userID, session);
      enforceSessionLimit();
      session = sessions.get(userID) || null;
    } else {
      session.lastAccessedAt = nextAccessTime();
    }
    return session;
  };

  const clearLikeWork = () => {
    likeGeneration += 1;
    likePendingArticleIds.clear();
    likeMutationVersions.clear();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = normalizeID(rawViewerID);
    if (nextViewerID === viewerID.value) return false;
    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    sessions.clear();
    clearLikeWork();
    pendingDeleteArticleIds.clear();
    deleteErrors.clear();
    deleteTargetProfileIDs.clear();
    deleteMutationVersions.clear();
    return true;
  };

  const getSession = (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    if (userID === null) return null;
    return sessions.get(userID) || null;
  };

  const isCurrentSessionCapture = (capture: ProfileSessionCapture) => {
    const session = sessions.get(capture.userID);
    return Boolean(
      session
      && session.profileRequestVersion === capture.profileRequestVersion
      && viewerID.value === capture.viewerID
      && viewerGeneration.value === capture.viewerGeneration,
    );
  };

  const captureSession = (rawUserID: unknown): ProfileSessionCapture | null => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (!userID || !session) return null;
    return {
      userID,
      viewerID: viewerID.value,
      viewerGeneration: viewerGeneration.value,
      profileRequestVersion: session.profileRequestVersion,
    };
  };

  const forEachProfilePost = (articleId: number, callback: (post: FeedPost) => void) => {
    if (feedStore.isArticleDeleted(articleId)) return;
    sessions.forEach((session) => {
      session.articles.forEach((post) => {
        if (post.id === articleId) callback(post);
      });
    });
  };

  const findPost = (articleId: number, rawUserID?: unknown) => {
    const preferred = normalizeID(rawUserID);
    const preferredSession = preferred === null ? null : sessions.get(preferred);
    const preferredPost = preferredSession?.articles.find((post) => post.id === articleId);
    if (preferredPost && !feedStore.isArticleDeleted(articleId)) return preferredPost;
    let found: FeedPost | undefined;
    forEachProfilePost(articleId, (post) => {
      found ||= post;
    });
    return found;
  };

  const applyLikeStateUpdateLocal = (update: FeedLikeStateUpdate) => {
    let applied = false;
    forEachProfilePost(update.articleId, (post) => {
      applied = applyFeedLikeStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    likeMutationVersions.set(
      update.articleId,
      (likeMutationVersions.get(update.articleId) ?? 0) + 1,
    );
    likePendingArticleIds.delete(update.articleId);
    return applyLikeStateUpdateLocal(update);
  };

  const applyCommentCountUpdateEverywhereLocal = (update: ArticleCommentCountUpdate) => {
    const commentCount = normalizeCommentCount(update.commentCount);
    if (commentCount === null) return false;
    let applied = false;
    sessions.forEach((session) => {
      session.articles.forEach((post) => {
        if (post.id !== update.articleId) return;
        post.commentCount = commentCount;
        applied = true;
      });
    });
    return applied;
  };

  const applyExternalFollowStateLocal = (state: UserFollowState) => {
    const session = sessions.get(state.user_id);
    if (!session) return false;
    session.followRequestVersion += 1;
    session.followMutationVersion += 1;
    session.followPending = false;
    session.followLoading = false;
    session.followActionError = '';
    session.followError = '';
    session.followState = state;
    session.followLoaded = true;
    return true;
  };

  const applyLikeStateUpdateEverywhere = (update: FeedLikeStateUpdate) => {
    const applied = applyLikeStateUpdateLocal(update);
    feedStore.applyLikeStateUpdate(update);
    syncProfileLikeState(update);
    return applied;
  };

  const markUnavailableLocal = (articleIds: number[], versions: Map<number, number>) => {
    articleIds.forEach((articleId) => {
      const capturedVersion = versions.get(articleId);
      if (
        capturedVersion === undefined
        || (likeMutationVersions.get(articleId) ?? 0) !== capturedVersion
      ) return;
      forEachProfilePost(articleId, (post) => {
        if (post.likeStatus === 'unknown') setFeedPostLikeUnavailable(post);
      });
    });
  };

  const hydrateLikeStates = async (
    articleIds: number[],
    capturedViewerGeneration: number,
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(articleIds));
    if (uniqueIDs.length === 0) return;
    const versions = new Map(uniqueIDs.map((id) => [id, likeMutationVersions.get(id) ?? 0]));
    const capturedLikeGeneration = likeGeneration;
    try {
      const response = await getArticleLikeStates(uniqueIDs);
      if (
        !isCurrent()
        || capturedViewerGeneration !== viewerGeneration.value
        || capturedLikeGeneration !== likeGeneration
      ) return;
      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.article_id);
        if (
          capturedVersion === undefined
          || (likeMutationVersions.get(item.article_id) ?? 0) !== capturedVersion
          || !findPost(item.article_id)
        ) return;
        readyIDs.add(item.article_id);
        applyLikeStateUpdateEverywhere({
          articleId: item.article_id,
          likes: item.likes,
          liked: item.liked,
          status: 'ready',
        });
      });
      response.unavailable_article_ids.forEach((articleId) => {
        const capturedVersion = versions.get(articleId);
        if (
          readyIDs.has(articleId)
          || capturedVersion === undefined
          || (likeMutationVersions.get(articleId) ?? 0) !== capturedVersion
          || !findPost(articleId)
        ) return;
        applyLikeStateUpdateEverywhere({
          articleId,
          likes: 0,
          liked: false,
          status: 'unavailable',
        });
      });
    } catch {
      if (isCurrent() && capturedViewerGeneration === viewerGeneration.value) {
        markUnavailableLocal(uniqueIDs, versions);
      }
    }
  };

  const appendArticles = (session: ProfileSessionEntry, rawArticles: Article[]) => {
    const newPosts = rawArticles
      .filter((article) => {
        if (session.loadedArticleIds.has(article.ID)) return false;
        session.loadedArticleIds.add(article.ID);
        return !feedStore.isArticleDeleted(article.ID);
      })
      .map(articleToFeedPost);
    if (newPosts.length > 0) {
      session.articles = [...session.articles, ...newPosts];
    }
    return newPosts;
  };

  const currentRequest = (
    userID: number,
    session: ProfileSessionEntry,
    version: number,
    capturedViewerID: number | null,
    capturedViewerGeneration: number,
  ) => sessions.get(userID) === session
    && session.articleRequestVersion === version
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === capturedViewerGeneration;

  const loadArticles = async (rawUserID: unknown, force = false) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (!userID || !session || (session.articlesInitialLoading && !force)) return session;
    if (session.articlesLoaded && !force) return session;

    if (force) {
      if (session.articlesLoadingMore) {
        session.articleRequestVersion += 1;
      }
      session.articles = [];
      session.loadedArticleIds.clear();
      session.nextCursor = null;
      session.hasMore = false;
      session.articlesLoaded = false;
    }

    const requestVersion = ++session.articleRequestVersion;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    session.articlesInitialLoading = true;
    session.articlesInitialError = '';
    session.articlesLoadMoreError = '';
    try {
      const page = await getUserArticles(String(userID), { limit: pageSize });
      if (!currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) return session;
      const newPosts = appendArticles(session, page.items);
      session.nextCursor = page.next_cursor;
      session.hasMore = page.next_cursor !== null;
      session.articlesLoaded = true;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        capturedViewerGeneration,
        () => currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration),
      );
    } catch (error) {
      if (currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.articlesInitialError = getErrorStatus(error) === 404
          ? 'The user posts could not be found.'
          : "Try again to load this user's posts.";
      }
    } finally {
      if (currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.articlesInitialLoading = false;
      }
    }
    return session;
  };

  const loadMoreArticles = async (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (
      !userID
      || !session
      || !session.articlesLoaded
      || !session.hasMore
      || session.articlesInitialLoading
      || session.articlesLoadingMore
      || session.articlesLoadMoreError
      || session.nextCursor === null
    ) return session;

    const requestedCursor = session.nextCursor;
    const requestVersion = ++session.articleRequestVersion;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    session.articlesLoadingMore = true;
    session.articlesLoadMoreError = '';
    try {
      const page = await getUserArticles(String(userID), { limit: pageSize, cursor: requestedCursor });
      if (
        !currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)
        || session.nextCursor !== requestedCursor
      ) return session;
      const newPosts = appendArticles(session, page.items);
      session.nextCursor = page.next_cursor;
      session.hasMore = page.next_cursor !== null;
      void hydrateLikeStates(
        newPosts.map((post) => post.id),
        capturedViewerGeneration,
        () => currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration),
      );
    } catch (error) {
      if (currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.articlesLoadMoreError = getErrorStatus(error) === 404
          ? 'The user posts could not be found.'
          : 'Try again to load more posts.';
      }
    } finally {
      if (currentRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.articlesLoadingMore = false;
      }
    }
    return session;
  };

  const retryLoadMoreArticles = (rawUserID: unknown) => {
    const session = getSession(rawUserID);
    if (!session) return;
    session.articlesLoadMoreError = '';
    void loadMoreArticles(rawUserID);
  };

  const loadFollowState = async (rawUserID: unknown, force = false) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    const capturedViewerID = viewerID.value;
    if (!userID || !session || capturedViewerID === null || !authStore.isAuthenticated) {
      if (session) {
        session.followLoaded = true;
        session.followLoading = false;
        session.followState = null;
      }
      return session;
    }
    if (session.followLoaded && !force) return session;
    if (session.followLoading && !force) return session;

    if (force) session.followRequestVersion += 1;
    const requestVersion = ++session.followRequestVersion;
    const capturedViewerGeneration = viewerGeneration.value;
    const mutationVersion = session.followMutationVersion;
    session.followLoading = true;
    session.followError = '';
    session.followActionError = '';
    const isCurrent = () => sessions.get(userID) === session
      && session.followRequestVersion === requestVersion
      && session.followMutationVersion === mutationVersion
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && authStore.isAuthenticated;
    try {
      const response = await getUserFollowState(userID);
      if (!isCurrent()) return session;
      if (response.user_id !== undefined && response.user_id !== userID) throw new Error('invalid follow response');
      session.followState = response;
      session.followLoaded = true;
    } catch {
      if (isCurrent()) {
        session.followState = null;
        session.followLoaded = false;
        session.followError = 'Social stats unavailable.';
      }
    } finally {
      if (isCurrent()) session.followLoading = false;
    }
    return session;
  };

  const toggleFollow = async (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : sessions.get(userID);
    const capturedViewerID = viewerID.value;
    const previous = session?.followState;
    if (
      !userID
      || !session
      || capturedViewerID === null
      || userID === capturedViewerID
      || !previous
      || session.followPending
      || !authStore.isAuthenticated
    ) return false;

    const previousState = { ...previous };
    const mutationVersion = ++session.followMutationVersion;
    const capturedViewerGeneration = viewerGeneration.value;
    const requestVersion = session.followRequestVersion;
    session.followPending = true;
    session.followActionError = '';
    session.followState = {
      ...previousState,
      following: !previousState.following,
      follower_count: previousState.following
        ? Math.max(0, previousState.follower_count - 1)
        : previousState.follower_count + 1,
    };
    const isCurrent = () => sessions.get(userID) === session
      && session.followPending
      && session.followMutationVersion === mutationVersion
      && session.followRequestVersion === requestVersion
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && authStore.isAuthenticated;
    try {
      const response = previousState.following
        ? await unfollowUser(userID)
        : await followUser(userID);
      if (!isCurrent()) return false;
      if (response.user_id !== undefined && response.user_id !== userID) throw new Error('invalid follow response');
      session.followState = response;
      session.followLoaded = true;
      session.followPending = false;
      syncProfileFollowState(response);
      return true;
    } catch {
      if (!isCurrent()) return false;
      session.followState = previousState;
      session.followPending = false;
      session.followActionError = 'Could not update follow status.';
      return false;
    }
  };

  const toggleLike = async (articleId: number, rawUserID?: unknown) => {
    const post = findPost(articleId, rawUserID);
    if (!post || post.likeStatus !== 'ready' || likePendingArticleIds.has(articleId)) return false;
    const previousLiked = post.liked;
    const previousLikes = post.likeCount;
    const mutationVersion = (likeMutationVersions.get(articleId) ?? 0) + 1;
    likeMutationVersions.set(articleId, mutationVersion);
    const capturedLikeGeneration = likeGeneration;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    likePendingArticleIds.add(articleId);
    applyLikeStateUpdateEverywhere({
      articleId,
      likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
      liked: !previousLiked,
      status: 'ready',
    });
    const isCurrent = () =>
      likePendingArticleIds.has(articleId)
      && (likeMutationVersions.get(articleId) ?? 0) === mutationVersion
      && likeGeneration === capturedLikeGeneration
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && authStore.isAuthenticated;
    try {
      const result = previousLiked
        ? await unlikeArticle(articleId)
        : await likeArticle(articleId);
      if (!isCurrent()) return false;
      const settledVersion = mutationVersion + 1;
      likeMutationVersions.set(articleId, settledVersion);
      applyLikeStateUpdateEverywhere({
        articleId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      });
      likePendingArticleIds.delete(articleId);
      return true;
    } catch (error) {
      if (!isCurrent()) return false;
      const settledVersion = mutationVersion + 1;
      likeMutationVersions.set(articleId, settledVersion);
      applyLikeStateUpdateEverywhere({
        articleId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'ready',
      });
      if (getErrorStatus(error) === 503) {
        applyLikeStateUpdateEverywhere({
          articleId,
          likes: previousLikes,
          liked: previousLiked,
          status: 'unavailable',
        });
      }
      likePendingArticleIds.delete(articleId);
      return false;
    }
  };

  const removeArticleEverywhereLocal = (articleId: number) => {
    sessions.forEach((session) => {
      const removedFromSession = session.articles.some((post) => post.id === articleId);
      session.articles = session.articles.filter((post) => post.id !== articleId);
      session.loadedArticleIds.add(articleId);
      if (removedFromSession && session.articlesLoadingMore) {
        session.articleRequestVersion += 1;
        session.articlesLoadingMore = false;
        session.articlesLoadMoreError = '';
      }
    });
    likePendingArticleIds.delete(articleId);
    likeMutationVersions.delete(articleId);
    pendingDeleteArticleIds.delete(articleId);
    deleteErrors.delete(articleId);
    deleteTargetProfileIDs.delete(articleId);
    deleteMutationVersions.delete(articleId);
  };

  const removeArticleEverywhere = (articleId: number, ownerUserID?: number) => {
    if (ownerUserID !== undefined && !feedStore.markArticleDeleted(articleId, ownerUserID)) return false;
    removeArticleEverywhereLocal(articleId);
    if (ownerUserID !== undefined) syncProfileArticleRemoval(articleId);
    return true;
  };

  const deletePost = async (articleId: number, rawUserID?: unknown) => {
    const ownerUserID = viewerID.value;
    const targetUserID = normalizeID(rawUserID);
    const post = findPost(articleId, targetUserID);
    if (
      ownerUserID === null
      || !authStore.isAuthenticated
      || !post
      || post.author.id !== ownerUserID
      || pendingDeleteArticleIds.has(articleId)
    ) return false;

    const capturedViewerGeneration = viewerGeneration.value;
    const capturedViewerID = ownerUserID;
    const deleteMutationVersion = (deleteMutationVersions.get(articleId) ?? 0) + 1;
    deleteMutationVersions.set(articleId, deleteMutationVersion);
    if (targetUserID !== null) deleteTargetProfileIDs.set(articleId, targetUserID);
    pendingDeleteArticleIds.add(articleId);
    deleteErrors.delete(articleId);
    const isCurrent = () => authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && (deleteMutationVersions.get(articleId) ?? 0) === deleteMutationVersion
      && pendingDeleteArticleIds.has(articleId);
    try {
      await deleteArticle(articleId);
      if (!isCurrent()) return false;
      return removeArticleEverywhere(articleId, ownerUserID);
    } catch (error) {
      if (!isCurrent()) return false;
      if (getErrorStatus(error) === 404) return removeArticleEverywhere(articleId, ownerUserID);
      deleteErrors.set(
        articleId,
        getErrorStatus(error) === 403
          ? 'You can only delete your own posts.'
          : getErrorStatus(error) === 401
            ? 'Please log in again to delete this post.'
            : 'Could not delete post. Please try again.',
      );
      pendingDeleteArticleIds.delete(articleId);
      deleteTargetProfileIDs.delete(articleId);
      return false;
    }
  };

  const cancelPendingDeletesForProfile = (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    if (userID === null) return;
    Array.from(deleteTargetProfileIDs.entries()).forEach(([articleId, targetID]) => {
      if (targetID !== userID) return;
      deleteMutationVersions.set(articleId, (deleteMutationVersions.get(articleId) ?? 0) + 1);
      deleteTargetProfileIDs.delete(articleId);
      pendingDeleteArticleIds.delete(articleId);
      deleteErrors.delete(articleId);
    });
  };

  const replaceAuthorIdentityEverywhereLocal = (author: PublicAuthor) => {
    sessions.forEach((session) => {
      if (session.user?.id === author.id) {
        session.user = { ...session.user, ...author };
      }
      session.articles = session.articles.map((post) => (
        post.author.id === author.id ? { ...post, author } : post
      ));
    });
  };

  const replaceAuthorIdentityEverywhere = (author: PublicAuthor) => {
    replaceAuthorIdentityEverywhereLocal(author);
    feedStore.replaceAuthorIdentity(author);
    syncProfileAuthorIdentity(author);
  };

  const updateUser = (updatedUser: PublicUser) => {
    const session = ensureSession(updatedUser.id);
    if (!session) return false;
    session.user = updatedUser;
    session.profileLoaded = true;
    session.profileError = '';
    session.profileNotFound = false;
    replaceAuthorIdentityEverywhere({
      id: updatedUser.id,
      username: updatedUser.username,
      display_name: updatedUser.display_name,
      avatar_url: updatedUser.avatar_url,
    });
    return true;
  };

  const loadProfile = async (rawUserID: unknown, force = false) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (!userID || !session) return session;
    if (session.profileLoading && !force) return session;
    if (session.profileLoaded && !force) {
      if (!session.articlesLoaded && !session.articlesInitialLoading) void loadArticles(userID);
      if (viewerID.value !== null && !session.followLoaded && !session.followLoading) {
        void loadFollowState(userID);
      }
      return session;
    }

    if (force) {
      session.profileRequestVersion += 1;
      session.user = null;
      session.profileLoaded = false;
      session.profileError = '';
      session.profileNotFound = false;
      session.articles = [];
      session.articlesLoaded = false;
      session.articlesInitialLoading = false;
      session.articlesLoadingMore = false;
      session.articlesInitialError = '';
      session.articlesLoadMoreError = '';
      session.nextCursor = null;
      session.hasMore = false;
      session.loadedArticleIds.clear();
      session.followState = null;
      session.followLoaded = false;
      session.followLoading = false;
      session.followError = '';
    }

    const profileVersion = ++session.profileRequestVersion;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    session.profileLoading = true;
    session.profileError = '';
    session.profileNotFound = false;
    const isCurrent = () => sessions.get(userID) === session
      && session.profileRequestVersion === profileVersion
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration;
    try {
      const loadedUser = await getUser(String(userID));
      if (!isCurrent()) return session;
      session.user = loadedUser;
      session.profileLoaded = true;
      session.profileLoading = false;
      void loadArticles(userID);
      if (viewerID.value !== null && authStore.isAuthenticated) void loadFollowState(userID);
    } catch (error) {
      if (!isCurrent()) return session;
      session.profileNotFound = getErrorStatus(error) === 404;
      session.profileError = session.profileNotFound
        ? ''
        : 'The profile could not be loaded. Try again.';
    } finally {
      if (isCurrent()) session.profileLoading = false;
    }
    return session;
  };

  const registerPublishedArticle = (article: Article, publisherUserID: number) => {
    const publisherID = normalizeID(publisherUserID);
    if (
      publisherID === null
      || viewerID.value !== publisherID
      || article?.ID <= 0
      || !article.author
      || article.author.id !== publisherID
      || feedStore.isArticleDeleted(article.ID)
    ) return false;
    const session = ensureSession(publisherID);
    if (!session) return false;
    const post = articleToFeedPost(article);
    session.articles = [
      post,
      ...session.articles.filter((item) => item.id !== post.id),
    ];
    session.loadedArticleIds.add(post.id);
    session.lastAccessedAt = nextAccessTime();
    return true;
  };

  const setScrollY = (rawUserID: unknown, value: number) => {
    const session = ensureSession(rawUserID);
    if (session) session.scrollY = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  registerProfileSessionSync({
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyCommentCountUpdateEverywhereLocal,
    applyExternalFollowStateLocal,
    removeArticleEverywhereLocal,
    replaceAuthorIdentityEverywhereLocal,
  });

  watch(
    () => authStore.currentIdentity?.id,
    (nextViewerID) => {
      setViewer(nextViewerID ?? null);
    },
    { immediate: true },
  );

  return {
    viewerID,
    viewerGeneration,
    sessions,
    maxProfileSessions,
    likePendingArticleIds,
    pendingDeleteArticleIds,
    deleteErrors,
    setViewer,
    getSession,
    ensureSession,
    captureSession,
    isCurrentSessionCapture,
    loadProfile,
    loadArticles,
    loadMoreArticles,
    retryLoadMoreArticles,
    loadFollowState,
    toggleFollow,
    toggleLike,
    deletePost,
    applyLikeStateUpdateEverywhere,
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyCommentCountUpdateEverywhereLocal,
    applyExternalFollowStateLocal,
    removeArticleEverywhere,
    removeArticleEverywhereLocal,
    replaceAuthorIdentityEverywhere,
    replaceAuthorIdentityEverywhereLocal,
    updateUser,
    registerPublishedArticle,
    setScrollY,
    cancelPendingDeletesForProfile,
  };
});
