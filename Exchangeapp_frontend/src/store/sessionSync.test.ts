import { describe, expect, it, vi } from 'vitest';
import type {
  FeedBookmarkStateUpdate,
  FeedLikeStateUpdate,
  FeedRepostStateUpdate,
} from '../types/Feed';
import type { UserFollowState } from '../services/userService';
import {
  registerHomeTimelineSync,
  registerConnectionsSessionSync,
  registerHistorySessionSync,
  registerProfileSessionSync,
  registerSearchSessionSync,
  registerBookmarksSessionSync,
  captureBookmarkStateSyncVersion,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  syncExternalPostBookmarkState,
  syncHydratedPostBookmarkState,
  syncExternalPostRemoval,
  syncExternalReplyCount,
  syncExternalFollowState,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncProfileAuthorIdentity,
  syncProfileFollowState,
  syncProfileLikeState,
} from './sessionSync';

const likeUpdate: FeedLikeStateUpdate = {
  postId: 42,
  likes: 9,
  liked: true,
  status: 'ready',
};

const repostUpdate: FeedRepostStateUpdate = {
  postId: 42,
  reposts: 4,
  reposted: true,
  status: 'ready',
};

const bookmarkUpdate: FeedBookmarkStateUpdate = {
  postId: 42,
  bookmarked: true,
  status: 'ready',
};

const followState: UserFollowState = {
  user_id: 8,
  following: false,
  follower_count: 3,
  following_count: 4,
};

const registerSinks = () => {
  const home = {
    applyLikeStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyRepostStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyBookmarkStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    reconcileFollowStateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const profile = {
    applyLikeStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyRepostStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyBookmarkStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateEverywhereLocal: vi.fn().mockReturnValue(true),
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    removePostEverywhereLocal: vi.fn(),
    replaceAuthorIdentityEverywhereLocal: vi.fn(),
  };
  const search = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
  };
  const history = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const connections = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    replaceUserIdentityLocal: vi.fn().mockReturnValue(true),
  };
  const bookmarks = {
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn().mockReturnValue(true),
    replaceAuthorIdentityLocal: vi.fn().mockReturnValue(true),
  };
  registerHomeTimelineSync(home);
  registerProfileSessionSync(profile);
  registerSearchSessionSync(search);
  registerHistorySessionSync(history);
  registerConnectionsSessionSync(connections);
  registerBookmarksSessionSync(bookmarks);
  return { home, profile, search, history, connections, bookmarks };
};

describe('sessionSync external mutation sinks', () => {
  it('sends external likes to Home and Profile exactly once', () => {
    const { home, profile, history, bookmarks } = registerSinks();

    syncExternalPostLikeState(likeUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
  });

  it('sends external reposts to Bookmarks as presentation updates', () => {
    const { home, profile, history, bookmarks } = registerSinks();

    syncExternalPostRepostState(repostUpdate);

    expect(home.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(profile.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(history.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
  });

  it('fans out external bookmark state to every live post surface', () => {
    const { home, profile, history, bookmarks } = registerSinks();

    syncExternalPostBookmarkState(bookmarkUpdate);

    expect(home.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(profile.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(history.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
  });

  it('rejects a bookmark hydration result after an external mutation advances its fence', () => {
    const { bookmarks } = registerSinks();
    const capturedVersion = captureBookmarkStateSyncVersion(42);

    syncExternalPostBookmarkState(bookmarkUpdate);

    expect(syncHydratedPostBookmarkState({
      postId: 42,
      bookmarked: false,
      status: 'ready',
    }, capturedVersion)).toBe(false);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledTimes(1);
  });

  it('sends external removals to Home and Profile exactly once', () => {
    const { home, profile, history, bookmarks } = registerSinks();

    syncExternalPostRemoval(42);

    expect(home.removePostLocal).toHaveBeenCalledOnce();
    expect(home.removePostLocal).toHaveBeenCalledWith(42);
    expect(profile.removePostEverywhereLocal).toHaveBeenCalledOnce();
    expect(profile.removePostEverywhereLocal).toHaveBeenCalledWith(42);
    expect(history.removePostLocal).toHaveBeenCalledOnce();
    expect(history.removePostLocal).toHaveBeenCalledWith(42);
    expect(bookmarks.removePostLocal).toHaveBeenCalledOnce();
    expect(bookmarks.removePostLocal).toHaveBeenCalledWith(42);
  });

  it('sends absolute comment counts to both caches', () => {
    const { home, profile, history } = registerSinks();
    const update = { postId: 42, replyCount: 5 };

    syncExternalReplyCount(update);

    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(profile.applyReplyCountUpdateEverywhereLocal).toHaveBeenCalledWith(update);
    expect(history.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
  });

  it('routes Profile follow success to Home and Search only', () => {
    const { home, profile, search, connections } = registerSinks();

    syncProfileFollowState(followState);

    expect(home.reconcileFollowStateLocal).toHaveBeenCalledWith(followState);
    expect(profile.applyExternalFollowStateLocal).not.toHaveBeenCalled();
    expect(search.applyExternalFollowStateLocal).toHaveBeenCalledOnce();
    expect(search.applyExternalFollowStateLocal).toHaveBeenCalledWith(followState);
    expect(connections.applyExternalFollowStateLocal).toHaveBeenCalledWith(followState);
  });

  it('routes external follow success to Home, Profile, and Search', () => {
    const { home, profile, search, connections } = registerSinks();

    syncExternalFollowState(followState);

    expect(home.reconcileFollowStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalFollowStateLocal).toHaveBeenCalledOnce();
    expect(search.applyExternalFollowStateLocal).toHaveBeenCalledOnce();
    expect(search.applyExternalFollowStateLocal).toHaveBeenCalledWith(followState);
    expect(connections.applyExternalFollowStateLocal).toHaveBeenCalledOnce();

    expect(profile.applyExternalFollowStateLocal).toHaveBeenCalledWith(followState);
  });

  it('routes home and profile like writes to the other feed plus History', () => {
    const { home, profile, history } = registerSinks();

    syncHomeLikeState(likeUpdate);
    syncProfileLikeState(likeUpdate);

    expect(profile.applyLikeStateUpdateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(home.applyLikeStateUpdateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledTimes(2);
  });

  it('fans out home and profile identity updates to History and Connections', () => {
    const { home, profile, history, connections, bookmarks } = registerSinks();
    const author = { id: 8, username: 'new-name', display_name: 'New Name', avatar_url: '' };

    syncHomeAuthorIdentity(author);
    syncProfileAuthorIdentity(author);

    expect(home.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
    expect(profile.replaceAuthorIdentityEverywhereLocal).toHaveBeenCalledWith(author);
    expect(history.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(connections.replaceUserIdentityLocal).toHaveBeenCalledTimes(2);
    expect(bookmarks.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(bookmarks.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
  });
});
