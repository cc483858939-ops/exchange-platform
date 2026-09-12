import { defineStore } from 'pinia';
import { reactive, ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import {
  deletePost as deletePostRequest,
  type TimelineActivityType,
  type TimelineItem,
} from '../services/postService';
import {
  followUser,
  getUser,
  getUserTimeline,
  getUserFollowState,
  unfollowUser,
  type UserFollowState,
} from '../services/userService';
import { getPostLikeStates, likePost, unlikePost } from '../services/likeService';
import {
  getPostRepostStates,
  repostPost,
  undoRepostPost,
} from '../services/repostService';
import type { Post } from '../types/Post';
import type { FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
import type { PublicAuthor, PublicUser } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  applyFeedRepostStateUpdate,
  postToFeedPost,
  setFeedPostLikeUnavailable,
  setFeedPostRepostUnavailable,
} from '../utils/feedPost';
import {
  registerProfileSessionSync,
  syncProfilePostRemoval,
  syncProfileAuthorIdentity,
  syncProfileLikeState,
  syncProfileRepostState,
  markOwnProfileTimelineStale,
} from './sessionSync';
import type { PostReplyCountUpdate } from './sessionSync';
import { syncProfileFollowState } from './sessionSync';

export type ProfileSessionEntry = {
  user: PublicUser | null;
  profileLoaded: boolean;
  profileLoading: boolean;
  profileError: string;
  profileNotFound: boolean;
  timelineItems: ProfileTimelineItem[];
  timelineLoaded: boolean;
  timelineInitialLoading: boolean;
  timelineLoadingMore: boolean;
  timelineInitialError: string;
  timelineLoadMoreError: string;
  nextCursor: string | null;
  hasMore: boolean;
  timelineStale: boolean;
  timelineStaleVersion: number;
  followState: UserFollowState | null;
  followLoaded: boolean;
  followLoading: boolean;
  followError: string;
  followPending: boolean;
  followActionError: string;
  scrollTop: number;
  lastAccessedAt: number;
  loadedActivityKeys: Set<string>;
  removedPostIDs: Set<number>;
  timelineGeneration: number;
  profileRequestVersion: number;
  timelineRequestVersion: number;
  followRequestVersion: number;
  followMutationVersion: number;
};

export type ProfileTimelineItem = {
  activityType: TimelineActivityType;
  activityAt: string;
  sourceId: number;
  actor: PublicAuthor;
  post: FeedPost;
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

const normalizeReplyCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) && Number.isInteger(count) && count >= 0 ? count : null;
};

const timelineActivityKey = (activityType: TimelineActivityType, sourceID: number) => (
  `${activityType}:${sourceID}`
);

const timelineActivityToProfileItem = (activity: TimelineItem): ProfileTimelineItem => ({
  activityType: activity.activity_type,
  activityAt: activity.activity_at,
  sourceId: activity.source_id,
  actor: activity.actor,
  post: postToFeedPost(
    activity.post,
    activity.activity_type === 'repost' ? { repostActor: activity.actor } : {},
  ),
});

export const useProfileSessionStore = defineStore('profileSession', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const sessions = reactive(new Map<number, ProfileSessionEntry>());
  const likePendingPostIds = reactive(new Set<number>());
  const repostPendingPostIds = reactive(new Set<number>());
  const pendingDeletePostIds = reactive(new Set<number>());
  const deleteErrors = reactive(new Map<number, string>());
  const deleteTargetProfileIDs = new Map<number, number>();
  const deleteMutationVersions = new Map<number, number>();
  const likeMutationVersions = new Map<number, number>();
  const repostMutationVersions = new Map<number, number>();
  let likeGeneration = 0;
  let repostGeneration = 0;
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
    timelineItems: [],
    timelineLoaded: false,
    timelineInitialLoading: false,
    timelineLoadingMore: false,
    timelineInitialError: '',
    timelineLoadMoreError: '',
    nextCursor: null,
    hasMore: false,
    timelineStale: false,
    timelineStaleVersion: 0,
    followState: null,
    followLoaded: false,
    followLoading: false,
    followError: '',
    followPending: false,
    followActionError: '',
    scrollTop: 0,
    lastAccessedAt: nextAccessTime(),
    loadedActivityKeys: new Set<string>(),
    removedPostIDs: new Set<number>(),
    timelineGeneration: 0,
    profileRequestVersion: 0,
    timelineRequestVersion: 0,
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
    likePendingPostIds.clear();
    likeMutationVersions.clear();
  };

  const clearRepostWork = () => {
    repostGeneration += 1;
    repostPendingPostIds.clear();
    repostMutationVersions.clear();
  };

  const setViewer = (rawViewerID: unknown) => {
    const nextViewerID = normalizeID(rawViewerID);
    if (nextViewerID === viewerID.value) return false;
    viewerID.value = nextViewerID;
    viewerGeneration.value += 1;
    sessions.clear();
    clearLikeWork();
    clearRepostWork();
    pendingDeletePostIds.clear();
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

  const forEachProfilePost = (postId: number, callback: (post: FeedPost) => void) => {
    if (feedStore.isPostDeleted(postId)) return;
    sessions.forEach((session) => {
      session.timelineItems.forEach((item) => {
        if (item.post.id === postId) callback(item.post);
      });
    });
  };

  const findPost = (postId: number, rawUserID?: unknown) => {
    const preferred = normalizeID(rawUserID);
    const preferredSession = preferred === null ? null : sessions.get(preferred);
    const preferredPost = preferredSession?.timelineItems.find((item) => item.post.id === postId)?.post;
    if (preferredPost && !feedStore.isPostDeleted(postId)) return preferredPost;
    let found: FeedPost | undefined;
    forEachProfilePost(postId, (post) => {
      found ||= post;
    });
    return found;
  };

  const applyLikeStateUpdateLocal = (update: FeedLikeStateUpdate) => {
    let applied = false;
    forEachProfilePost(update.postId, (post) => {
      applied = applyFeedLikeStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalLikeStateLocal = (update: FeedLikeStateUpdate) => {
    likeMutationVersions.set(
      update.postId,
      (likeMutationVersions.get(update.postId) ?? 0) + 1,
    );
    likePendingPostIds.delete(update.postId);
    return applyLikeStateUpdateLocal(update);
  };

  const getRepostMutationVersion = (postId: number) =>
    repostMutationVersions.get(postId) ?? 0;

  const bumpRepostMutationVersion = (postId: number) => {
    const next = getRepostMutationVersion(postId) + 1;
    repostMutationVersions.set(postId, next);
    return next;
  };

  const applyRepostStateUpdateLocal = (
    update: FeedRepostStateUpdate,
    expectedVersion?: number,
  ) => {
    if (
      expectedVersion !== undefined
      && getRepostMutationVersion(update.postId) !== expectedVersion
    ) {
      return false;
    }
    let applied = false;
    forEachProfilePost(update.postId, (post) => {
      applied = applyFeedRepostStateUpdate(post, update) || applied;
    });
    return applied;
  };

  const applyExternalRepostStateLocal = (update: FeedRepostStateUpdate) => {
    bumpRepostMutationVersion(update.postId);
    repostPendingPostIds.delete(update.postId);
    return applyRepostStateUpdateLocal(update);
  };

  const markOwnProfileTimelineStaleLocal = () => {
    const ownUserID = viewerID.value;
    if (ownUserID === null) return false;
    const session = sessions.get(ownUserID);
    if (!session) return false;
    session.timelineStale = true;
    session.timelineStaleVersion += 1;
    return true;
  };

  const removeOwnRepostActivityLocal = (postId: number, actorID: number) => {
    const session = sessions.get(actorID);
    if (!session) return false;
    const removedKeys = session.timelineItems
      .filter((item) => item.activityType === 'repost'
        && item.sourceId > 0
        && item.post.id === postId
        && item.actor.id === actorID)
      .map((item) => timelineActivityKey(item.activityType, item.sourceId));
    if (removedKeys.length === 0) return false;
    session.timelineItems = session.timelineItems.filter((item) => !(
      item.activityType === 'repost'
      && item.post.id === postId
      && item.actor.id === actorID
    ));
    removedKeys.forEach((key) => session.loadedActivityKeys.delete(key));
    if (session.timelineLoadingMore) {
      session.timelineRequestVersion += 1;
      session.timelineLoadingMore = false;
      session.timelineLoadMoreError = '';
    }
    return true;
  };

  const applyReplyCountUpdateEverywhereLocal = (update: PostReplyCountUpdate) => {
    const replyCount = normalizeReplyCount(update.replyCount);
    if (replyCount === null) return false;
    let applied = false;
    sessions.forEach((session) => {
      session.timelineItems.forEach((item) => {
        if (item.post.id !== update.postId) return;
        item.post.replyCount = replyCount;
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

  const applyRepostStateUpdateEverywhere = (
    update: FeedRepostStateUpdate,
    expectedVersion?: number,
  ) => {
    const applied = applyRepostStateUpdateLocal(update, expectedVersion);
    if (expectedVersion !== undefined && !applied) {
      return false;
    }
    feedStore.applyRepostStateUpdate(update);
    syncProfileRepostState(update);
    return applied;
  };

  const markUnavailableLocal = (postIds: number[], versions: Map<number, number>) => {
    postIds.forEach((postId) => {
      const capturedVersion = versions.get(postId);
      if (
        capturedVersion === undefined
        || (likeMutationVersions.get(postId) ?? 0) !== capturedVersion
      ) return;
      forEachProfilePost(postId, (post) => {
        if (post.likeStatus === 'unknown') setFeedPostLikeUnavailable(post);
      });
    });
  };

  const hydrateLikeStates = async (
    postIds: number[],
    capturedViewerGeneration: number,
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;
    const versions = new Map(uniqueIDs.map((id) => [id, likeMutationVersions.get(id) ?? 0]));
    const capturedLikeGeneration = likeGeneration;
    try {
      const response = await getPostLikeStates(uniqueIDs);
      if (
        !isCurrent()
        || capturedViewerGeneration !== viewerGeneration.value
        || capturedLikeGeneration !== likeGeneration
      ) return;
      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.post_id);
        if (
          capturedVersion === undefined
          || (likeMutationVersions.get(item.post_id) ?? 0) !== capturedVersion
          || !findPost(item.post_id)
        ) return;
        readyIDs.add(item.post_id);
        applyLikeStateUpdateEverywhere({
          postId: item.post_id,
          likes: item.likes,
          liked: item.liked,
          status: 'ready',
        });
      });
      response.unavailable_post_ids.forEach((postId) => {
        const capturedVersion = versions.get(postId);
        if (
          readyIDs.has(postId)
          || capturedVersion === undefined
          || (likeMutationVersions.get(postId) ?? 0) !== capturedVersion
          || !findPost(postId)
        ) return;
        applyLikeStateUpdateEverywhere({
          postId,
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

  const markRepostUnavailableLocal = (postIds: number[], versions: Map<number, number>) => {
    postIds.forEach((postId) => {
      const capturedVersion = versions.get(postId);
      if (
        capturedVersion === undefined
        || getRepostMutationVersion(postId) !== capturedVersion
      ) return;
      forEachProfilePost(postId, (post) => {
        if (post.repostStatus === 'unknown') setFeedPostRepostUnavailable(post);
      });
    });
  };

  const hydrateRepostStates = async (
    postIds: number[],
    isCurrent: () => boolean,
  ) => {
    const uniqueIDs = Array.from(new Set(postIds));
    if (uniqueIDs.length === 0) return;
    const versions = new Map(uniqueIDs.map((id) => [id, getRepostMutationVersion(id)]));
    const capturedRepostGeneration = repostGeneration;
    try {
      const response = await getPostRepostStates(uniqueIDs);
      if (!isCurrent() || capturedRepostGeneration !== repostGeneration) return;
      const readyIDs = new Set<number>();
      response.items.forEach((item) => {
        const capturedVersion = versions.get(item.post_id);
        if (
          capturedVersion === undefined
          || getRepostMutationVersion(item.post_id) !== capturedVersion
          || !findPost(item.post_id)
        ) return;
        readyIDs.add(item.post_id);
        applyRepostStateUpdateEverywhere({
          postId: item.post_id,
          reposts: item.reposts,
          reposted: item.reposted,
          status: 'ready',
        }, capturedVersion);
      });
      response.unavailable_post_ids.forEach((postId) => {
        const capturedVersion = versions.get(postId);
        if (
          readyIDs.has(postId)
          || capturedVersion === undefined
          || getRepostMutationVersion(postId) !== capturedVersion
          || !findPost(postId)
        ) return;
        applyRepostStateUpdateEverywhere({
          postId,
          reposts: 0,
          reposted: false,
          status: 'unavailable',
        }, capturedVersion);
      });
    } catch {
      if (isCurrent() && capturedRepostGeneration === repostGeneration) {
        markRepostUnavailableLocal(uniqueIDs, versions);
      }
    }
  };

  const appendTimelineItems = (session: ProfileSessionEntry, activities: TimelineItem[]) => {
    const newItems: ProfileTimelineItem[] = [];
    activities.forEach((activity) => {
      const key = timelineActivityKey(activity.activity_type, activity.source_id);
      if (session.loadedActivityKeys.has(key)) return;
      session.loadedActivityKeys.add(key);
      const item = timelineActivityToProfileItem(activity);
      if (!session.removedPostIDs.has(item.post.id) && !feedStore.isPostDeleted(item.post.id)) {
        newItems.push(item);
      }
    });
    if (newItems.length > 0) {
      session.timelineItems = [...session.timelineItems, ...newItems];
    }
    return newItems;
  };

  const currentTimelineRequest = (
    userID: number,
    session: ProfileSessionEntry,
    version: number,
    capturedViewerID: number | null,
    capturedViewerGeneration: number,
  ) => sessions.get(userID) === session
    && session.timelineRequestVersion === version
    && authStore.isAuthenticated
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === capturedViewerGeneration;

  const currentTimelineSession = (
    userID: number,
    session: ProfileSessionEntry,
    capturedTimelineGeneration: number,
    capturedViewerID: number | null,
    capturedViewerGeneration: number,
  ) => sessions.get(userID) === session
    && session.timelineGeneration === capturedTimelineGeneration
    && authStore.isAuthenticated
    && viewerID.value === capturedViewerID
    && viewerGeneration.value === capturedViewerGeneration;

  const loadTimeline = async (rawUserID: unknown, force = false) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (
      !userID
      || !session
      || viewerID.value === null
      || !authStore.isAuthenticated
      || (session.timelineInitialLoading && !force)
    ) return session;
    if (session.timelineLoaded && !force && !session.timelineStale) return session;

    if (force) {
      session.timelineGeneration += 1;
      session.timelineRequestVersion += 1;
      session.timelineLoadingMore = false;
      session.timelineItems = [];
      session.loadedActivityKeys.clear();
      session.nextCursor = null;
      session.hasMore = false;
      session.timelineLoaded = false;
      session.timelineLoadMoreError = '';
    }

    const requestVersion = ++session.timelineRequestVersion;
    const capturedTimelineStaleVersion = session.timelineStaleVersion;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    session.timelineInitialLoading = true;
    session.timelineInitialError = '';
    session.timelineLoadMoreError = '';
    try {
      const page = await getUserTimeline(String(userID), { limit: pageSize });
      if (!currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) return session;
      const newItems = appendTimelineItems(session, page.items);
      session.nextCursor = page.next_cursor;
      session.hasMore = page.next_cursor !== null;
      session.timelineLoaded = true;
      if (session.timelineStaleVersion === capturedTimelineStaleVersion) {
        session.timelineStale = false;
      }
      const capturedTimelineGeneration = session.timelineGeneration;
      void hydrateLikeStates(
        newItems.map((item) => item.post.id),
        capturedViewerGeneration,
        () => currentTimelineSession(
          userID,
          session,
          capturedTimelineGeneration,
          capturedViewerID,
          capturedViewerGeneration,
        ),
      );
      void hydrateRepostStates(
        newItems.map((item) => item.post.id),
        () => currentTimelineSession(
          userID,
          session,
          capturedTimelineGeneration,
          capturedViewerID,
          capturedViewerGeneration,
        ),
      );
    } catch (error) {
      if (currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.timelineInitialError = getErrorStatus(error) === 404
          ? 'The user timeline could not be found.'
          : 'Try again to load this profile timeline.';
      }
    } finally {
      if (currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.timelineInitialLoading = false;
      }
    }
    return session;
  };

  const loadMoreTimeline = async (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    const session = userID === null ? null : ensureSession(userID);
    if (
      !userID
      || !session
      || viewerID.value === null
      || !authStore.isAuthenticated
      || !session.timelineLoaded
      || !session.hasMore
      || session.timelineStale
      || session.timelineInitialLoading
      || session.timelineLoadingMore
      || session.timelineLoadMoreError
      || session.nextCursor === null
    ) return session;

    const requestedCursor = session.nextCursor;
    const requestVersion = ++session.timelineRequestVersion;
    const capturedTimelineStaleVersion = session.timelineStaleVersion;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    session.timelineLoadingMore = true;
    session.timelineLoadMoreError = '';
    try {
      const page = await getUserTimeline(String(userID), { limit: pageSize, cursor: requestedCursor });
      if (
        !currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)
        || session.nextCursor !== requestedCursor
      ) return session;
      const newItems = appendTimelineItems(session, page.items);
      session.nextCursor = page.next_cursor;
      session.hasMore = page.next_cursor !== null;
      if (session.timelineStaleVersion === capturedTimelineStaleVersion) {
        session.timelineStale = false;
      }
      const capturedTimelineGeneration = session.timelineGeneration;
      void hydrateLikeStates(
        newItems.map((item) => item.post.id),
        capturedViewerGeneration,
        () => currentTimelineSession(
          userID,
          session,
          capturedTimelineGeneration,
          capturedViewerID,
          capturedViewerGeneration,
        ),
      );
      void hydrateRepostStates(
        newItems.map((item) => item.post.id),
        () => currentTimelineSession(
          userID,
          session,
          capturedTimelineGeneration,
          capturedViewerID,
          capturedViewerGeneration,
        ),
      );
    } catch (error) {
      if (currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.timelineLoadMoreError = getErrorStatus(error) === 404
          ? 'The user timeline could not be found.'
          : 'Try again to load more activity.';
      }
    } finally {
      if (currentTimelineRequest(userID, session, requestVersion, capturedViewerID, capturedViewerGeneration)) {
        session.timelineLoadingMore = false;
      }
    }
    return session;
  };

  const retryLoadMoreTimeline = (rawUserID: unknown) => {
    const session = getSession(rawUserID);
    if (!session) return;
    session.timelineLoadMoreError = '';
    void loadMoreTimeline(rawUserID);
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

  const toggleLike = async (postId: number, rawUserID?: unknown) => {
    const post = findPost(postId, rawUserID);
    if (!post || post.likeStatus !== 'ready' || likePendingPostIds.has(postId)) return false;
    const previousLiked = post.liked;
    const previousLikes = post.likeCount;
    const mutationVersion = (likeMutationVersions.get(postId) ?? 0) + 1;
    likeMutationVersions.set(postId, mutationVersion);
    const capturedLikeGeneration = likeGeneration;
    const capturedViewerID = viewerID.value;
    const capturedViewerGeneration = viewerGeneration.value;
    likePendingPostIds.add(postId);
    applyLikeStateUpdateEverywhere({
      postId,
      likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
      liked: !previousLiked,
      status: 'ready',
    });
    const isCurrent = () =>
      likePendingPostIds.has(postId)
      && (likeMutationVersions.get(postId) ?? 0) === mutationVersion
      && likeGeneration === capturedLikeGeneration
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && authStore.isAuthenticated;
    try {
      const result = previousLiked
        ? await unlikePost(postId)
        : await likePost(postId);
      if (!isCurrent()) return false;
      const settledVersion = mutationVersion + 1;
      likeMutationVersions.set(postId, settledVersion);
      applyLikeStateUpdateEverywhere({
        postId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      });
      likePendingPostIds.delete(postId);
      return true;
    } catch (error) {
      if (!isCurrent()) return false;
      const settledVersion = mutationVersion + 1;
      likeMutationVersions.set(postId, settledVersion);
      applyLikeStateUpdateEverywhere({
        postId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'ready',
      });
      if (getErrorStatus(error) === 503) {
        applyLikeStateUpdateEverywhere({
          postId,
          likes: previousLikes,
          liked: previousLiked,
          status: 'unavailable',
        });
      }
      likePendingPostIds.delete(postId);
      return false;
    }
  };

  const toggleRepost = async (postId: number, rawUserID?: unknown) => {
    const post = findPost(postId, rawUserID);
    const capturedViewerID = viewerID.value;
    if (
      !post
      || post.repostStatus !== 'ready'
      || capturedViewerID === null
      || repostPendingPostIds.has(postId)
      || !authStore.isAuthenticated
    ) return false;

    const previousReposted = post.reposted;
    const previousReposts = post.repostCount;
    const mutationVersion = bumpRepostMutationVersion(postId);
    const capturedGeneration = repostGeneration;
    const capturedViewerGeneration = viewerGeneration.value;
    repostPendingPostIds.add(postId);
    applyRepostStateUpdateEverywhere({
      postId,
      reposts: previousReposted ? Math.max(0, previousReposts - 1) : previousReposts + 1,
      reposted: !previousReposted,
      status: 'ready',
    }, mutationVersion);

    const isCurrent = () => (
      authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && repostGeneration === capturedGeneration
      && (repostMutationVersions.get(postId) ?? 0) === mutationVersion
      && repostPendingPostIds.has(postId)
    );

    try {
      const response = previousReposted
        ? await undoRepostPost(postId)
        : await repostPost(postId);
      if (!isCurrent()) return false;
      repostMutationVersions.set(postId, mutationVersion + 1);
      applyRepostStateUpdateEverywhere({
        postId,
        reposts: response.reposts,
        reposted: response.reposted,
        status: 'ready',
      }, mutationVersion + 1);
      repostPendingPostIds.delete(postId);
      markOwnProfileTimelineStale();
      if (previousReposted) removeOwnRepostActivityLocal(postId, capturedViewerID);
      return true;
    } catch {
      if (!isCurrent()) return false;
      repostMutationVersions.set(postId, mutationVersion + 1);
      applyRepostStateUpdateEverywhere({
        postId,
        reposts: previousReposts,
        reposted: previousReposted,
        status: 'ready',
      }, mutationVersion + 1);
      repostPendingPostIds.delete(postId);
      return false;
    }
  };

  const removePostEverywhereLocal = (postId: number) => {
    sessions.forEach((session) => {
      const removedFromSession = session.timelineItems.some((item) => item.post.id === postId);
      session.removedPostIDs.add(postId);
      if (!removedFromSession) return;
      session.timelineItems = session.timelineItems.filter((item) => item.post.id !== postId);
      if (session.timelineLoadingMore) {
        session.timelineRequestVersion += 1;
        session.timelineLoadingMore = false;
        session.timelineLoadMoreError = '';
      }
    });
    likePendingPostIds.delete(postId);
    likeMutationVersions.delete(postId);
    repostPendingPostIds.delete(postId);
    bumpRepostMutationVersion(postId);
    pendingDeletePostIds.delete(postId);
    deleteErrors.delete(postId);
    deleteTargetProfileIDs.delete(postId);
    deleteMutationVersions.delete(postId);
  };

  const removePostEverywhere = (postId: number, ownerUserID?: number) => {
    if (ownerUserID !== undefined && !feedStore.markPostDeleted(postId, ownerUserID)) return false;
    removePostEverywhereLocal(postId);
    if (ownerUserID !== undefined) syncProfilePostRemoval(postId);
    return true;
  };

  const deletePost = async (postId: number, rawUserID?: unknown) => {
    const ownerUserID = viewerID.value;
    const targetUserID = normalizeID(rawUserID);
    const post = findPost(postId, targetUserID);
    if (
      ownerUserID === null
      || !authStore.isAuthenticated
      || !post
      || post.author.id !== ownerUserID
      || pendingDeletePostIds.has(postId)
    ) return false;

    const capturedViewerGeneration = viewerGeneration.value;
    const capturedViewerID = ownerUserID;
    const deleteMutationVersion = (deleteMutationVersions.get(postId) ?? 0) + 1;
    deleteMutationVersions.set(postId, deleteMutationVersion);
    if (targetUserID !== null) deleteTargetProfileIDs.set(postId, targetUserID);
    pendingDeletePostIds.add(postId);
    deleteErrors.delete(postId);
    const isCurrent = () => authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration
      && (deleteMutationVersions.get(postId) ?? 0) === deleteMutationVersion
      && pendingDeletePostIds.has(postId);
    try {
      await deletePostRequest(postId);
      if (!isCurrent()) return false;
      return removePostEverywhere(postId, ownerUserID);
    } catch (error) {
      if (!isCurrent()) return false;
      if (getErrorStatus(error) === 404) return removePostEverywhere(postId, ownerUserID);
      deleteErrors.set(
        postId,
        getErrorStatus(error) === 403
          ? 'You can only delete your own posts.'
          : getErrorStatus(error) === 401
            ? 'Please log in again to delete this post.'
            : 'Could not delete post. Please try again.',
      );
      pendingDeletePostIds.delete(postId);
      deleteTargetProfileIDs.delete(postId);
      return false;
    }
  };

  const cancelPendingDeletesForProfile = (rawUserID: unknown) => {
    const userID = normalizeID(rawUserID);
    if (userID === null) return;
    Array.from(deleteTargetProfileIDs.entries()).forEach(([postId, targetID]) => {
      if (targetID !== userID) return;
      deleteMutationVersions.set(postId, (deleteMutationVersions.get(postId) ?? 0) + 1);
      deleteTargetProfileIDs.delete(postId);
      pendingDeletePostIds.delete(postId);
      deleteErrors.delete(postId);
    });
  };

  const replaceAuthorIdentityEverywhereLocal = (author: PublicAuthor) => {
    sessions.forEach((session) => {
      if (session.user?.id === author.id) {
        session.user = { ...session.user, ...author };
      }
      session.timelineItems = session.timelineItems.map((item) => {
        const canonicalMatches = item.post.author.id === author.id;
        const activityActorMatches = item.actor.id === author.id;
        const repostActorMatches = item.post.repostContext?.actor.id === author.id;
        return canonicalMatches || activityActorMatches || repostActorMatches
          ? {
            ...item,
            actor: activityActorMatches ? author : item.actor,
            post: {
              ...item.post,
              author: canonicalMatches ? author : item.post.author,
              repostContext: repostActorMatches
                ? { actor: author }
                : item.post.repostContext,
            },
          }
          : item;
      });
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
    if (!userID || !session || viewerID.value === null || !authStore.isAuthenticated) return session;
    if (session.profileLoading && !force) return session;
    if (session.profileLoaded && !force) {
      if (session.timelineStale) {
        void loadTimeline(userID, true);
      } else if (!session.timelineLoaded && !session.timelineInitialLoading) {
        void loadTimeline(userID);
      }
      if (viewerID.value !== null && !session.followLoaded && !session.followLoading) {
        void loadFollowState(userID);
      }
      return session;
    }

    if (force) {
      session.profileRequestVersion += 1;
      session.timelineGeneration += 1;
      session.timelineRequestVersion += 1;
      session.user = null;
      session.profileLoaded = false;
      session.profileError = '';
      session.profileNotFound = false;
      session.timelineItems = [];
      session.timelineLoaded = false;
      session.timelineInitialLoading = false;
      session.timelineLoadingMore = false;
      session.timelineInitialError = '';
      session.timelineLoadMoreError = '';
      session.nextCursor = null;
      session.hasMore = false;
      session.timelineStale = false;
      session.loadedActivityKeys.clear();
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
      && authStore.isAuthenticated
      && viewerID.value === capturedViewerID
      && viewerGeneration.value === capturedViewerGeneration;
    try {
      const loadedUser = await getUser(String(userID));
      if (!isCurrent()) return session;
      session.user = loadedUser;
      session.profileLoaded = true;
      session.profileLoading = false;
      void loadTimeline(userID);
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

  const registerPublishedTimelinePost = (post: Post, publisherUserID: number) => {
    const publisherID = normalizeID(publisherUserID);
    if (
      publisherID === null
      || viewerID.value !== publisherID
      || post?.id <= 0
      || !post.author
      || post.author.id !== publisherID
      || feedStore.isPostDeleted(post.id)
    ) return false;
    const session = ensureSession(publisherID);
    if (!session) return false;
    const feedPost = postToFeedPost(post);
    const item: ProfileTimelineItem = {
      activityType: 'post',
      activityAt: post.published_at || post.created_at,
      sourceId: feedPost.id,
      actor: {
        id: post.author.id,
        username: post.author.username,
        display_name: post.author.display_name,
        avatar_url: post.author.avatar_url,
      },
      post: feedPost,
    };
    const key = timelineActivityKey(item.activityType, item.sourceId);
    session.timelineItems = [
      item,
      ...session.timelineItems.filter((existing) => timelineActivityKey(
        existing.activityType,
        existing.sourceId,
      ) !== key),
    ];
    session.loadedActivityKeys.add(key);
    session.lastAccessedAt = nextAccessTime();
    return true;
  };

  const setScrollTop = (rawUserID: unknown, value: number) => {
    const session = ensureSession(rawUserID);
    if (session) session.scrollTop = Number.isFinite(value) && value >= 0 ? value : 0;
  };

  registerProfileSessionSync({
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyReplyCountUpdateEverywhereLocal,
    applyExternalFollowStateLocal,
    markOwnProfileTimelineStale: markOwnProfileTimelineStaleLocal,
    removePostEverywhereLocal,
    replaceAuthorIdentityEverywhereLocal,
  });

  watch(
    () => authStore.isAuthenticated ? authStore.currentIdentity?.id : null,
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
    likePendingPostIds,
    repostPendingPostIds,
    pendingDeletePostIds,
    deleteErrors,
    setViewer,
    getSession,
    ensureSession,
    captureSession,
    isCurrentSessionCapture,
    loadProfile,
    loadTimeline,
    loadMoreTimeline,
    retryLoadMoreTimeline,
    loadFollowState,
    toggleFollow,
    toggleLike,
    toggleRepost,
    deletePost,
    applyLikeStateUpdateEverywhere,
    applyLikeStateUpdateLocal,
    applyExternalLikeStateLocal,
    applyRepostStateUpdateLocal,
    applyExternalRepostStateLocal,
    applyReplyCountUpdateEverywhereLocal,
    applyExternalFollowStateLocal,
    removePostEverywhere,
    removePostEverywhereLocal,
    replaceAuthorIdentityEverywhere,
    replaceAuthorIdentityEverywhereLocal,
    updateUser,
    registerPublishedTimelinePost,
    setScrollTop,
    cancelPendingDeletesForProfile,
  };
});
