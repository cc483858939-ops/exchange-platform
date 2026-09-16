import type {
  FeedBookmarkStateUpdate,
  FeedLikeStateUpdate,
  FeedPost,
  FeedRepostStateUpdate,
} from '../types/Feed';
import type { UserFollowState } from '../services/userService';
import type { PublicAuthor } from '../types/User';

export type PostReplyCountUpdate = {
  postId: number;
  replyCount: number;
};

export type HomeTimelineSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate, expectedVersion?: number) => boolean;
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyRepostStateUpdateLocal: (update: FeedRepostStateUpdate, expectedVersion?: number) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyBookmarkStateUpdateLocal?: (update: FeedBookmarkStateUpdate, expectedVersion?: number) => boolean;
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  reconcileFollowStateLocal: (state: UserFollowState) => boolean;
  removePostLocal: (postID: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type ProfileSessionSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyRepostStateUpdateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyBookmarkStateUpdateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateEverywhereLocal: (update: PostReplyCountUpdate) => boolean;
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  markOwnProfileTimelineStale?: () => boolean;
  removePostEverywhereLocal: (postID: number) => void;
  replaceAuthorIdentityEverywhereLocal: (author: PublicAuthor) => void;
};

export type SearchSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
};

export type HistorySessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  removePostLocal: (postID: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type ConnectionsSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  replaceUserIdentityLocal: (author: PublicAuthor) => void;
};

export type BookmarksSessionSync = {
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyExternalLikeStateLocal?: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal?: (update: FeedRepostStateUpdate) => boolean;
  removePostLocal?: (postID: number) => boolean;
  replaceAuthorIdentityLocal?: (author: PublicAuthor) => boolean;
};

export type PostDetailSessionSync = {
  applyExternalBookmarkStateLocal: (update: FeedBookmarkStateUpdate) => boolean;
};

let homeTimelineSync: HomeTimelineSync | null = null;
let profileSessionSync: ProfileSessionSync | null = null;
let searchSessionSync: SearchSessionSync | null = null;
let historySessionSync: HistorySessionSync | null = null;
let connectionsSessionSync: ConnectionsSessionSync | null = null;
let bookmarksSessionSync: BookmarksSessionSync | null = null;
let postDetailSessionSync: PostDetailSessionSync | null = null;
const bookmarkStateSyncVersions = new Map<number, number>();

const getBookmarkStateSyncVersion = (postID: number) => (
  bookmarkStateSyncVersions.get(postID) ?? 0
);

const bumpBookmarkStateSyncVersion = (postID: number) => {
  const nextVersion = getBookmarkStateSyncVersion(postID) + 1;
  bookmarkStateSyncVersions.set(postID, nextVersion);
  return nextVersion;
};

export const captureBookmarkStateSyncVersion = (postID: number) => (
  getBookmarkStateSyncVersion(postID)
);

export const registerHomeTimelineSync = (sync: HomeTimelineSync) => {
  homeTimelineSync = sync;
};

export const registerProfileSessionSync = (sync: ProfileSessionSync) => {
  profileSessionSync = sync;
};

export const registerSearchSessionSync = (sync: SearchSessionSync) => {
  searchSessionSync = sync;
};

export const registerHistorySessionSync = (sync: HistorySessionSync) => {
  historySessionSync = sync;
};

export const registerConnectionsSessionSync = (sync: ConnectionsSessionSync) => {
  connectionsSessionSync = sync;
};

export const registerBookmarksSessionSync = (sync: BookmarksSessionSync) => {
  bookmarksSessionSync = sync;
};

export const registerPostDetailSessionSync = (sync: PostDetailSessionSync | null) => {
  postDetailSessionSync = sync;
};

export const syncHomeLikeState = (update: FeedLikeStateUpdate) => {
  const profileApplied = profileSessionSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalLikeStateLocal?.(update) ?? false;
  return profileApplied || historyApplied || bookmarksApplied;
};

export const syncHomeRepostState = (update: FeedRepostStateUpdate) => {
  const profileApplied = profileSessionSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalRepostStateLocal?.(update) ?? false;
  return profileApplied || historyApplied || bookmarksApplied;
};

export const syncHomePostRemoval = (postID: number) => {
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
};

export const syncHomeAuthorIdentity = (author: PublicAuthor) => {
  profileSessionSync?.replaceAuthorIdentityEverywhereLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
  bookmarksSessionSync?.replaceAuthorIdentityLocal?.(author);
};

export const syncProfileLikeState = (update: FeedLikeStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalLikeStateLocal?.(update) ?? false;
  return homeApplied || historyApplied || bookmarksApplied;
};

export const syncProfileRepostState = (update: FeedRepostStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalRepostStateLocal?.(update) ?? false;
  return homeApplied || historyApplied || bookmarksApplied;
};

export const syncHomeBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  const profileApplied = profileSessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  return profileApplied || historyApplied;
};

export const syncProfileBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  const homeApplied = homeTimelineSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  return homeApplied || historyApplied;
};

export const syncHistoryBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const syncProfilePostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  historySessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
};

export const syncProfileAuthorIdentity = (author: PublicAuthor) => {
  homeTimelineSync?.replaceAuthorIdentityLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
  bookmarksSessionSync?.replaceAuthorIdentityLocal?.(author);
};

export const syncExternalPostLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
  bookmarksSessionSync?.applyExternalLikeStateLocal?.(update);
};

export const syncExternalPostRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
  bookmarksSessionSync?.applyExternalRepostStateLocal?.(update);
};

export const syncExternalPostBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  historySessionSync?.applyExternalBookmarkStateLocal?.(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const markOwnProfileTimelineStale = () =>
  profileSessionSync?.markOwnProfileTimelineStale?.() ?? false;

export const syncExternalPostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
};

export const syncHydratedPostBookmarkState = (
  update: FeedBookmarkStateUpdate,
  capturedVersion: number,
) => {
  if (getBookmarkStateSyncVersion(update.postId) !== capturedVersion) {
    return false;
  }
  syncExternalPostBookmarkState(update);
  return true;
};

export const syncExternalReplyCount = (update: PostReplyCountUpdate) => {
  homeTimelineSync?.applyReplyCountUpdateLocal(update);
  profileSessionSync?.applyReplyCountUpdateEverywhereLocal(update);
  historySessionSync?.applyReplyCountUpdateLocal(update);
};

export const syncProfileFollowState = (state: UserFollowState) => {
  homeTimelineSync?.reconcileFollowStateLocal(state);
  searchSessionSync?.applyExternalFollowStateLocal(state);
  connectionsSessionSync?.applyExternalFollowStateLocal(state);
};

export const syncExternalFollowState = (state: UserFollowState) => {
  homeTimelineSync?.reconcileFollowStateLocal(state);
  profileSessionSync?.applyExternalFollowStateLocal(state);
  searchSessionSync?.applyExternalFollowStateLocal(state);
  connectionsSessionSync?.applyExternalFollowStateLocal(state);
};

export type { FeedPost };
