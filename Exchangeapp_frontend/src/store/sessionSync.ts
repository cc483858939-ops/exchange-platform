import type { FeedLikeStateUpdate, FeedPost, FeedRepostStateUpdate } from '../types/Feed';
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
  applyReplyCountUpdateEverywhereLocal: (update: PostReplyCountUpdate) => boolean;
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
  removePostEverywhereLocal: (postID: number) => void;
  replaceAuthorIdentityEverywhereLocal: (author: PublicAuthor) => void;
};

export type SearchSessionSync = {
  applyExternalFollowStateLocal: (state: UserFollowState) => boolean;
};

export type HistorySessionSync = {
  applyExternalLikeStateLocal: (update: FeedLikeStateUpdate) => boolean;
  applyExternalRepostStateLocal: (update: FeedRepostStateUpdate) => boolean;
  applyReplyCountUpdateLocal: (update: PostReplyCountUpdate) => boolean;
  removePostLocal: (postID: number) => void;
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

export const syncHomeRepostState = (update: FeedRepostStateUpdate) => {
  const profileApplied = profileSessionSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  return profileApplied || historyApplied;
};

export const syncHomePostRemoval = (postID: number) => {
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
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

export const syncProfileRepostState = (update: FeedRepostStateUpdate) => {
  const homeApplied = homeTimelineSync?.applyRepostStateUpdateLocal(update) ?? false;
  const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
  return homeApplied || historyApplied;
};

export const syncProfilePostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  historySessionSync?.removePostLocal(postID);
};

export const syncProfileAuthorIdentity = (author: PublicAuthor) => {
  homeTimelineSync?.replaceAuthorIdentityLocal(author);
  historySessionSync?.replaceAuthorIdentityLocal(author);
  connectionsSessionSync?.replaceUserIdentityLocal(author);
};

export const syncExternalPostLikeState = (update: FeedLikeStateUpdate) => {
  homeTimelineSync?.applyExternalLikeStateLocal(update);
  profileSessionSync?.applyExternalLikeStateLocal(update);
  historySessionSync?.applyExternalLikeStateLocal(update);
};

export const syncExternalPostRepostState = (update: FeedRepostStateUpdate) => {
  homeTimelineSync?.applyExternalRepostStateLocal(update);
  profileSessionSync?.applyExternalRepostStateLocal(update);
  historySessionSync?.applyExternalRepostStateLocal(update);
};

export const syncExternalPostRemoval = (postID: number) => {
  homeTimelineSync?.removePostLocal(postID);
  profileSessionSync?.removePostEverywhereLocal(postID);
  historySessionSync?.removePostLocal(postID);
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
