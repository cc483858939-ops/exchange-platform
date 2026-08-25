import type { FeedLikeStateUpdate } from '../types/Feed';
import type { PublicAuthor } from '../types/User';

export type HomeTimelineSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate, expectedVersion?: number) => boolean;
  removeArticleLocal: (articleId: number) => void;
  replaceAuthorIdentityLocal: (author: PublicAuthor) => void;
};

export type ProfileSessionSync = {
  applyLikeStateUpdateLocal: (update: FeedLikeStateUpdate) => boolean;
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
