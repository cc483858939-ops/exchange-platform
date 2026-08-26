import type { FeedLikeStateUpdate } from '../types/Feed';
import type { UserFollowState } from '../services/userService';
import type { PublicAuthor } from '../types/User';

export type ArticleCommentCountUpdate = {
  articleId: number;
  commentCount: number;
};

export type HomeTimelineSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate, expectedVersion?: number) => boolean;
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyCommentCountUpdateLocal: (update: ArticleCommentCountUpdate) => boolean;
  reconcileFollowStateLocal: (state: UserFollowState) => boolean;
  removeArticleLocal: (articleId: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type ProfileSessionSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyCommentCountUpdateEverywhereLocal: (update: ArticleCommentCountUpdate) => boolean;
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  removeArticleEverywhereLocal: (articleId: number) => void;
  replaceAuthorIdentityEverywhereLocal: (author: PublicAuthor) => void;
};

export type SearchSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
};

export type HistorySessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyCommentCountUpdateLocal: (update: ArticleCommentCountUpdate) => boolean;
  removeArticleLocal: (articleId: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type ConnectionsSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  replaceUserIdentityLocal: (author: PublicAuthor) => void;
};

let homeTimelineSync: HomeTimelineSync | null = null;
let profileSessionSync: ProfileSessionSync | null = null;
let searchSessionSync: SearchSessionSync | null = null;
let historySessionSync: HistorySessionSync | null = null;
let connectionsSessionSync: ConnectionsSessionSync | null = null;

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

export const syncHomeLikeState = (update: FeedLikeStateUpdate) => {
  const profileApplied = profileSessionSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  return profileApplied || historyApplied;
};

export const syncHomeArticleRemoval = (articleId: number) => {
  profileSessionSync?.removeArticleEverywhereLocal(articleId);
  historySessionSync?.removeArticleLocal(articleId);
};

export const syncHomeAuthorIdentity = (author: PublicAuthor) => {
  profileSessionSync?.replaceAuthorIdentityEverywhereLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
};

export const syncProfileLikeState = (update: FeedLikeStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyLikeStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
  return homeApplied || historyApplied;
};

export const syncProfileArticleRemoval = (articleId: number) => {
  homeTimelineSync?.removeArticleLocal(articleId);
  historySessionSync?.removeArticleLocal(articleId);
};

export const syncProfileAuthorIdentity = (author: PublicAuthor) => {
  homeTimelineSync?.replaceAuthorIdentityLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
};

export const syncExternalArticleLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
};

export const syncExternalArticleRemoval = (articleId: number) => {
  homeTimelineSync?.removeArticleLocal(articleId);
  profileSessionSync?.removeArticleEverywhereLocal(articleId);
  historySessionSync?.removeArticleLocal(articleId);
};

export const syncExternalCommentCount = (update: ArticleCommentCountUpdate) => {
  homeTimelineSync?.applyCommentCountUpdateLocal(update);
  profileSessionSync?.applyCommentCountUpdateEverywhereLocal(update);
  historySessionSync?.applyCommentCountUpdateLocal(update);
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
