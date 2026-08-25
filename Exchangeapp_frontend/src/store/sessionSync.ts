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

let homeTimelineSync: HomeTimelineSync | null = null;
let profileSessionSync: ProfileSessionSync | null = null;

export const registerHomeTimelineSync = (sync: HomeTimelineSync) => {
  homeTimelineSync = sync;
};

export const registerProfileSessionSync = (sync: ProfileSessionSync) => {
  profileSessionSync = sync;
};

export const syncHomeLikeState = (update: FeedLikeStateUpdate) =>
  profileSessionSync?.applyLikeStateUpdateLocal(update) ?? false;

export const syncHomeArticleRemoval = (articleId: number) => {
  profileSessionSync?.removeArticleEverywhereLocal(articleId);
};

export const syncHomeAuthorIdentity = (author: PublicAuthor) => {
  profileSessionSync?.replaceAuthorIdentityEverywhereLocal(author);
};

export const syncProfileLikeState = (update: FeedLikeStateUpdate) =>
  homeTimelineSync?.applyLikeStateUpdateLocal(update) ?? false;

export const syncProfileArticleRemoval = (articleId: number) => {
  homeTimelineSync?.removeArticleLocal(articleId);
};

export const syncProfileAuthorIdentity = (author: PublicAuthor) => {
  homeTimelineSync?.replaceAuthorIdentityLocal(author);
};

export const syncExternalArticleLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
};

export const syncExternalArticleRemoval = (articleId: number) => {
  homeTimelineSync?.removeArticleLocal(articleId);
  profileSessionSync?.removeArticleEverywhereLocal(articleId);
};

export const syncExternalCommentCount = (update: ArticleCommentCountUpdate) => {
  homeTimelineSync?.applyCommentCountUpdateLocal(update);
  profileSessionSync?.applyCommentCountUpdateEverywhereLocal(update);
};

export const syncProfileFollowState = (state: UserFollowState) => {
  homeTimelineSync?.reconcileFollowStateLocal(state);
};

export const syncExternalFollowState = (state: UserFollowState) => {
  homeTimelineSync?.reconcileFollowStateLocal(state);
  profileSessionSync?.applyExternalFollowStateLocal(state);
};
