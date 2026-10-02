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

export type PostQuoteCountUpdate = {
  postId: number;
  quoteCount: number;
};

export type HomeTimelineSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate, expectedVersion?: number) => boolean;
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyRepostStateUpdateLocal: (update: FeedRepostStateUpdate, expectedVersion?: number) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyBookmarkStateUpdateLocal?: (update: FeedBookmarkStateUpdate, expectedVersion?: number) => boolean;
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
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
  applyQuoteCountUpdateEverywhereLocal: (update: PostQuoteCountUpdate) => boolean;
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  markOwnProfileTimelineStale?: () => boolean;
  removePostEverywhereLocal: (postID: number) => void;
  replaceAuthorIdentityEverywhereLocal: (author: PublicAuthor) => void;
};

export type SearchSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
};

export type PostSearchSessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalBookmarkStateLocal: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
  removePostLocal: (postID: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type HistorySessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
  removePostLocal: (postID: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type TopicSessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalBookmarkStateLocal: (update: FeedBookmarkStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
  removePostLocal: (postID: number) => boolean;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => boolean;
};

export type ConnectionsSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  replaceUserIdentityLocal: (author: PublicAuthor) => void;
};

export type BookmarksSessionSync = {
  applyExternalBookmarkStateLocal?: (update: FeedBookmarkStateUpdate) => boolean;
  applyExternalLikeStateLocal?: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal?: (update: FeedRepostStateUpdate) => boolean;
  applyQuoteCountUpdateLocal?: (update: PostQuoteCountUpdate) => boolean;
  removePostLocal?: (postID: number) => boolean;
  replaceAuthorIdentityLocal?: (author: PublicAuthor) => boolean;
};

export type PostDetailSessionSync = {
  applyExternalBookmarkStateLocal: (update: FeedBookmarkStateUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
};

export type QuotesSessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyExternalBookmarkStateLocal: (update: FeedBookmarkStateUpdate) => boolean;
  applyQuoteCountUpdateLocal: (update: PostQuoteCountUpdate) => boolean;
};

let homeTimelineSync: HomeTimelineSync | null = null;
let profileSessionSync: ProfileSessionSync | null = null;
let searchSessionSync: SearchSessionSync | null = null;
let postSearchSessionSync: PostSearchSessionSync | null = null;
let historySessionSync: HistorySessionSync | null = null;
let topicSessionSync: TopicSessionSync | null = null;
let connectionsSessionSync: ConnectionsSessionSync | null = null;
let bookmarksSessionSync: BookmarksSessionSync | null = null;
let postDetailSessionSync: PostDetailSessionSync | null = null;
let quotesSessionSync: QuotesSessionSync | null = null;
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

export const beginBookmarkStateMutation = (postID: number) => {
  bumpBookmarkStateSyncVersion(postID);
};

export const registerHomeTimelineSync = (sync: HomeTimelineSync) => {
  homeTimelineSync = sync;
};

export const registerProfileSessionSync = (sync: ProfileSessionSync) => {
  profileSessionSync = sync;
};

export const registerSearchSessionSync = (sync: SearchSessionSync) => {
  searchSessionSync = sync;
};

export const registerPostSearchSessionSync = (sync: PostSearchSessionSync) => {
  postSearchSessionSync = sync;
};

export const registerHistorySessionSync = (sync: HistorySessionSync) => {
  historySessionSync = sync;
};

export const registerTopicSessionSync = (sync: TopicSessionSync) => {
  topicSessionSync = sync;
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

export const registerQuotesSessionSync = (sync: QuotesSessionSync | null) => {
  quotesSessionSync = sync;
};

export const syncHomeLikeState = (update: FeedLikeStateUpdate) => {
  const profileApplied = profileSessionSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalLikeStateLocal?.(update) ?? false;
  const searchApplied = postSearchSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  return profileApplied || historyApplied || topicApplied || bookmarksApplied || searchApplied || quotesApplied;
};

export const syncHomeRepostState = (update: FeedRepostStateUpdate) => {
  const profileApplied = profileSessionSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalRepostStateLocal?.(update) ?? false;
  const searchApplied = postSearchSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  return profileApplied || historyApplied || topicApplied || bookmarksApplied || searchApplied || quotesApplied;
};

export const syncHomePostRemoval = (postID: number) => {
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
  topicSessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
  postSearchSessionSync?.removePostLocal(postID);
};

export const syncHomeAuthorIdentity = (author: PublicAuthor) => {
  profileSessionSync?.replaceAuthorIdentityEverywhereLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  topicSessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
  bookmarksSessionSync?.replaceAuthorIdentityLocal?.(author);
  postSearchSessionSync?.replaceAuthorIdentityLocal(author);
};

export const syncProfileLikeState = (update: FeedLikeStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalLikeStateLocal?.(update) ?? false;
  const searchApplied = postSearchSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalLikeStateLocal(update) ?? false;
  return homeApplied || historyApplied || topicApplied || bookmarksApplied || searchApplied || quotesApplied;
};

export const syncProfileRepostState = (update: FeedRepostStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const bookmarksApplied = bookmarksSessionSync?.applyExternalRepostStateLocal?.(update) ?? false;
  const searchApplied = postSearchSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalRepostStateLocal(update) ?? false;
  return homeApplied || historyApplied || topicApplied || bookmarksApplied || searchApplied || quotesApplied;
};

export const syncTopicLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
  bookmarksSessionSync?.applyExternalLikeStateLocal?.(update);
  postSearchSessionSync?.applyExternalLikeStateLocal(update);
  quotesSessionSync?.applyExternalLikeStateLocal(update);
};

export const syncTopicRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
  bookmarksSessionSync?.applyExternalRepostStateLocal?.(update);
  postSearchSessionSync?.applyExternalRepostStateLocal(update);
  quotesSessionSync?.applyExternalRepostStateLocal(update);
};

export const syncTopicBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  historySessionSync?.applyExternalBookmarkStateLocal?.(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  postSearchSessionSync?.applyExternalBookmarkStateLocal(update);
  quotesSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const syncHomeBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  const profileApplied = profileSessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  const searchApplied = postSearchSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  return profileApplied || historyApplied || topicApplied || searchApplied || quotesApplied;
};

export const syncProfileBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  const homeApplied = homeTimelineSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalBookmarkStateLocal?.(update) ?? false;
  const topicApplied = topicSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  const searchApplied = postSearchSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  const quotesApplied = quotesSessionSync?.applyExternalBookmarkStateLocal(update) ?? false;
  return homeApplied || historyApplied || topicApplied || searchApplied || quotesApplied;
};

export const syncHistoryBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  topicSessionSync?.applyExternalBookmarkStateLocal(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  postSearchSessionSync?.applyExternalBookmarkStateLocal(update);
  quotesSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const syncProfilePostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  historySessionSync?.removePostLocal(postID);
  topicSessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
  postSearchSessionSync?.removePostLocal(postID);
};

export const syncProfileAuthorIdentity = (author: PublicAuthor) => {
  homeTimelineSync?.replaceAuthorIdentityLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  topicSessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
  bookmarksSessionSync?.replaceAuthorIdentityLocal?.(author);
  postSearchSessionSync?.replaceAuthorIdentityLocal(author);
};

export const syncExternalPostLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
  topicSessionSync?.applyExternalLikeStateLocal(update);
  bookmarksSessionSync?.applyExternalLikeStateLocal?.(update);
  postSearchSessionSync?.applyExternalLikeStateLocal(update);
  quotesSessionSync?.applyExternalLikeStateLocal(update);
};

export const syncExternalPostRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
  topicSessionSync?.applyExternalRepostStateLocal(update);
  bookmarksSessionSync?.applyExternalRepostStateLocal?.(update);
  postSearchSessionSync?.applyExternalRepostStateLocal(update);
  quotesSessionSync?.applyExternalRepostStateLocal(update);
};

export const syncExternalPostBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  historySessionSync?.applyExternalBookmarkStateLocal?.(update);
  topicSessionSync?.applyExternalBookmarkStateLocal(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  postSearchSessionSync?.applyExternalBookmarkStateLocal(update);
  quotesSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const syncQuotesLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
  topicSessionSync?.applyExternalLikeStateLocal(update);
  bookmarksSessionSync?.applyExternalLikeStateLocal?.(update);
  postSearchSessionSync?.applyExternalLikeStateLocal(update);
};

export const syncQuotesRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
  topicSessionSync?.applyExternalRepostStateLocal(update);
  bookmarksSessionSync?.applyExternalRepostStateLocal?.(update);
  postSearchSessionSync?.applyExternalRepostStateLocal(update);
};

export const syncQuotesBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  historySessionSync?.applyExternalBookmarkStateLocal?.(update);
  topicSessionSync?.applyExternalBookmarkStateLocal(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  postSearchSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const syncPostSearchLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
  topicSessionSync?.applyExternalLikeStateLocal(update);
  bookmarksSessionSync?.applyExternalLikeStateLocal?.(update);
  quotesSessionSync?.applyExternalLikeStateLocal(update);
};

export const syncPostSearchRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
  topicSessionSync?.applyExternalRepostStateLocal(update);
  bookmarksSessionSync?.applyExternalRepostStateLocal?.(update);
  quotesSessionSync?.applyExternalRepostStateLocal(update);
};

export const syncPostSearchBookmarkState = (update: FeedBookmarkStateUpdate) => {
  bumpBookmarkStateSyncVersion(update.postId);
  homeTimelineSync?.applyExternalBookmarkStateLocal?.(update);
  profileSessionSync?.applyExternalBookmarkStateLocal?.(update);
  historySessionSync?.applyExternalBookmarkStateLocal?.(update);
  topicSessionSync?.applyExternalBookmarkStateLocal(update);
  bookmarksSessionSync?.applyExternalBookmarkStateLocal?.(update);
  postDetailSessionSync?.applyExternalBookmarkStateLocal(update);
  quotesSessionSync?.applyExternalBookmarkStateLocal(update);
};

export const markOwnProfileTimelineStale = () =>
  profileSessionSync?.markOwnProfileTimelineStale?.() ?? false;

export const syncExternalPostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
  topicSessionSync?.removePostLocal(postID);
  bookmarksSessionSync?.removePostLocal?.(postID);
  postSearchSessionSync?.removePostLocal(postID);
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
  topicSessionSync?.applyReplyCountUpdateLocal(update);
  postSearchSessionSync?.applyReplyCountUpdateLocal(update);
};

export const syncExternalQuoteCount = (update: PostQuoteCountUpdate) => {
  homeTimelineSync?.applyQuoteCountUpdateLocal(update);
  profileSessionSync?.applyQuoteCountUpdateEverywhereLocal(update);
  historySessionSync?.applyQuoteCountUpdateLocal(update);
  topicSessionSync?.applyQuoteCountUpdateLocal(update);
  bookmarksSessionSync?.applyQuoteCountUpdateLocal?.(update);
  postSearchSessionSync?.applyQuoteCountUpdateLocal(update);
  quotesSessionSync?.applyQuoteCountUpdateLocal(update);
  postDetailSessionSync?.applyQuoteCountUpdateLocal(update);
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
