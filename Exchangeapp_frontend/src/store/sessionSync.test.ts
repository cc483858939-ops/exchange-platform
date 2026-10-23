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
  registerSuggestedUsersSessionSync,
  registerPostSearchSessionSync,
  registerBookmarksSessionSync,
  registerPostDetailSessionSync,
  registerQuotesSessionSync,
  registerTopicSessionSync,
  captureBookmarkStateSyncVersion,
  beginBookmarkStateMutation,
  beginReplyCountRefresh,
  captureReplyCountVersion,
  commitReplyCountMutation,
  commitReplyCountRefresh,
  invalidateReplyCountRefreshes,
  isReplyCountReconciliationNeeded,
  markReplyCountReconciliationNeeded,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  syncExternalPostBookmarkState,
  syncHydratedPostBookmarkState,
  syncExternalPostRemoval,
  syncExternalQuoteCount,
  syncExternalFollowState,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncHomeRepostState,
  syncHomeBookmarkState,
  syncHomePostRemoval,
  syncProfileAuthorIdentity,
  syncProfileFollowState,
  syncProfileLikeState,
  syncProfileRepostState,
  syncProfileBookmarkState,
  syncProfilePostRemoval,
  syncHistoryBookmarkState,
  syncTopicBookmarkState,
  syncTopicLikeState,
  syncTopicRepostState,
  syncQuotesLikeState,
  syncQuotesRepostState,
  syncQuotesBookmarkState,
  syncPostSearchLikeState,
  syncPostSearchRepostState,
  syncPostSearchBookmarkState,
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
  muted: false,
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
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
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
    applyQuoteCountUpdateEverywhereLocal: vi.fn().mockReturnValue(true),
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    removePostEverywhereLocal: vi.fn(),
    replaceAuthorIdentityEverywhereLocal: vi.fn(),
  };
  const search = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
  };
  const postSearch = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const history = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const topic = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn().mockReturnValue(true),
    replaceAuthorIdentityLocal: vi.fn().mockReturnValue(true),
  };
  const connections = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    replaceUserIdentityLocal: vi.fn().mockReturnValue(true),
  };
  const bookmarks = {
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn().mockReturnValue(true),
    replaceAuthorIdentityLocal: vi.fn().mockReturnValue(true),
  };
  const quotes = {
    removePostLocal: vi.fn(),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
  };
  const postDetail = {
    removePostLocal: vi.fn(),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    applyQuoteCountUpdateLocal: vi.fn().mockReturnValue(true),
  };
  registerHomeTimelineSync(home);
  registerProfileSessionSync(profile);
  registerSearchSessionSync(search);
  registerPostSearchSessionSync(postSearch);
  registerHistorySessionSync(history);
  registerTopicSessionSync(topic);
  registerConnectionsSessionSync(connections);
  registerBookmarksSessionSync(bookmarks);
  registerPostDetailSessionSync(postDetail);
  registerQuotesSessionSync(quotes);
  return { home, profile, search, postSearch, history, topic, connections, bookmarks, postDetail, quotes };
};

describe('sessionSync external mutation sinks', () => {
  it.each([syncExternalPostRemoval, syncHomePostRemoval, syncProfilePostRemoval])(
    'sends deletion from every entry point to Quotes and detail without rebroadcast', (sync) => {
      const { quotes, postDetail } = registerSinks();
      sync(42);
      expect(quotes.removePostLocal).toHaveBeenCalledExactlyOnceWith(42);
      expect(postDetail.removePostLocal).toHaveBeenCalledExactlyOnceWith(42);
    },
  );
  it('sends external likes to Home and Profile exactly once', () => {
    const { home, profile, postSearch, history, topic, bookmarks, quotes } = registerSinks();

    syncExternalPostLikeState(likeUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(topic.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(topic.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(postSearch.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
  });

  it('sends external reposts to Bookmarks as presentation updates', () => {
    const { home, profile, postSearch, history, topic, bookmarks, quotes } = registerSinks();

    syncExternalPostRepostState(repostUpdate);

    expect(home.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(profile.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(history.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(topic.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(topic.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(postSearch.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
  });

  it('fans out external bookmark state to every live post surface', () => {
    const { home, profile, postSearch, history, topic, bookmarks, postDetail, quotes } = registerSinks();

    syncExternalPostBookmarkState(bookmarkUpdate);

    expect(home.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(profile.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(history.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(topic.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(topic.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(postSearch.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(postDetail.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
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

  it('invalidates an older bookmark hydration as soon as a mutation begins', () => {
    const { bookmarks } = registerSinks();
    const capturedVersion = captureBookmarkStateSyncVersion(42001);

    beginBookmarkStateMutation(42001);

    expect(syncHydratedPostBookmarkState({
      postId: 42001,
      bookmarked: false,
      status: 'ready',
    }, capturedVersion)).toBe(false);
    expect(bookmarks.applyExternalBookmarkStateLocal).not.toHaveBeenCalled();
  });

  it('sends external removals to Home and Profile exactly once', () => {
    const { home, profile, postSearch, history, topic, bookmarks } = registerSinks();

    syncExternalPostRemoval(42);

    expect(home.removePostLocal).toHaveBeenCalledOnce();
    expect(home.removePostLocal).toHaveBeenCalledWith(42);
    expect(profile.removePostEverywhereLocal).toHaveBeenCalledOnce();
    expect(profile.removePostEverywhereLocal).toHaveBeenCalledWith(42);
    expect(history.removePostLocal).toHaveBeenCalledOnce();
    expect(history.removePostLocal).toHaveBeenCalledWith(42);
    expect(topic.removePostLocal).toHaveBeenCalledOnce();
    expect(topic.removePostLocal).toHaveBeenCalledWith(42);
    expect(bookmarks.removePostLocal).toHaveBeenCalledOnce();
    expect(bookmarks.removePostLocal).toHaveBeenCalledWith(42);
    expect(postSearch.removePostLocal).toHaveBeenCalledOnce();
    expect(postSearch.removePostLocal).toHaveBeenCalledWith(42);
  });

  it('fans out an accepted absolute reply count to every Post surface once', () => {
    const { home, profile, postSearch, history, topic, postDetail } = registerSinks();
    const update = { postId: 42, replyCount: 5 };

    expect(commitReplyCountMutation(update)).toBe(true);

    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(update);
    expect(profile.applyReplyCountUpdateEverywhereLocal).toHaveBeenCalledExactlyOnceWith(update);
    expect(history.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(update);
    expect(topic.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(update);
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(postDetail.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(update);
  });

  it('rejects older same-post refreshes without changing or rebroadcasting a newer count', () => {
    const { home, profile, postSearch, history, topic, postDetail } = registerSinks();
    const older = beginReplyCountRefresh(42001);
    const newer = beginReplyCountRefresh(42001);
    const accepted = { postId: 42001, replyCount: 6 };
    const stale = { postId: 42001, replyCount: 5 };

    expect(commitReplyCountRefresh(newer, accepted)).toBe(true);
    expect(commitReplyCountRefresh(older, stale)).toBe(false);

    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(home.applyReplyCountUpdateLocal).not.toHaveBeenCalledWith(stale);
    expect(profile.applyReplyCountUpdateEverywhereLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(profile.applyReplyCountUpdateEverywhereLocal).not.toHaveBeenCalledWith(stale);
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(postSearch.applyReplyCountUpdateLocal).not.toHaveBeenCalledWith(stale);
    expect(history.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(history.applyReplyCountUpdateLocal).not.toHaveBeenCalledWith(stale);
    expect(topic.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(topic.applyReplyCountUpdateLocal).not.toHaveBeenCalledWith(stale);
    expect(postDetail.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(accepted);
    expect(postDetail.applyReplyCountUpdateLocal).not.toHaveBeenCalledWith(stale);
  });

  it('invalidates pending reads after a confirmed mutation and permits count decreases', () => {
    const { home, profile, history, topic, postSearch } = registerSinks();
    const pending = beginReplyCountRefresh(42002);
    const mutation = { postId: 42002, replyCount: 4 };

    expect(commitReplyCountMutation(mutation)).toBe(true);
    expect(commitReplyCountRefresh(pending, { postId: 42002, replyCount: 5 })).toBe(false);
    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(mutation);
    expect(profile.applyReplyCountUpdateEverywhereLocal).toHaveBeenCalledExactlyOnceWith(mutation);
    expect(history.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(mutation);
    expect(topic.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(mutation);
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(mutation);
  });

  it('accepts confirmed reply-count changes from six to five and back to six', () => {
    const { home } = registerSinks();
    const updates = [6, 5, 6].map(replyCount => ({ postId: 42011, replyCount }));

    for (const update of updates) {
      expect(commitReplyCountMutation(update)).toBe(true);
    }

    expect(home.applyReplyCountUpdateLocal.mock.calls).toEqual(updates.map(update => [update]));
  });

  it('keeps reply-count refresh versions isolated by post ID', () => {
    registerSinks();
    const firstPost = beginReplyCountRefresh(42003);
    const secondPost = beginReplyCountRefresh(42004);

    expect(commitReplyCountMutation({ postId: 42004, replyCount: 7 })).toBe(true);
    expect(commitReplyCountRefresh(firstPost, { postId: 42003, replyCount: 2 })).toBe(true);
    expect(commitReplyCountRefresh(secondPost, { postId: 42004, replyCount: 8 })).toBe(false);
  });

  it('rejects malformed IDs, counts, mismatched responses, and superseded failed reads', () => {
    const { home } = registerSinks();
    const older = beginReplyCountRefresh(42005);
    beginReplyCountRefresh(42005);

    expect(beginReplyCountRefresh(0)).toBeNull();
    expect(beginReplyCountRefresh(Number.MAX_SAFE_INTEGER + 1)).toBeNull();
    expect(beginReplyCountRefresh(Number.NaN)).toBeNull();
    expect(commitReplyCountRefresh(older, { postId: 42005, replyCount: 3 })).toBe(false);

    const current = beginReplyCountRefresh(42006);
    expect(commitReplyCountRefresh(current, { postId: 42007, replyCount: 3 })).toBe(false);
    expect(commitReplyCountMutation({ postId: 42006, replyCount: -1 })).toBe(false);
    expect(commitReplyCountMutation({ postId: 42006, replyCount: 1.5 })).toBe(false);
    expect(commitReplyCountMutation({ postId: 42006, replyCount: Number.NaN })).toBe(false);
    expect(commitReplyCountMutation({ postId: 42006, replyCount: Number.POSITIVE_INFINITY })).toBe(false);
    expect(commitReplyCountMutation({ postId: Number.POSITIVE_INFINITY, replyCount: 3 })).toBe(false);
    expect(home.applyReplyCountUpdateLocal).not.toHaveBeenCalled();
  });

  it('does not apply a delete based on an outdated count version', () => {
    const { home } = registerSinks();
    const beforeDelete = captureReplyCountVersion(42008);
    const read = beginReplyCountRefresh(42008);
    expect(commitReplyCountRefresh(read, { postId: 42008, replyCount: 6 })).toBe(true);

    expect(commitReplyCountMutation({ postId: 42008, replyCount: 5 }, beforeDelete)).toBe(false);
    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith({ postId: 42008, replyCount: 6 });
  });

  it('lets a newer refresh supersede an older request even if the newer request fails', () => {
    const { home } = registerSinks();
    const older = beginReplyCountRefresh(42009);
    beginReplyCountRefresh(42009);
    invalidateReplyCountRefreshes(42009);

    expect(commitReplyCountRefresh(older, { postId: 42009, replyCount: 8 })).toBe(false);
    expect(home.applyReplyCountUpdateLocal).not.toHaveBeenCalled();
  });

  it('keeps a per-post reconciliation mark until a current server snapshot commits', () => {
    const { home } = registerSinks();
    const postID = 42012;
    const staleRead = beginReplyCountRefresh(postID);

    expect(commitReplyCountMutation({ postId: postID, replyCount: 6 })).toBe(true);
    expect(markReplyCountReconciliationNeeded(postID)).toBe(true);
    expect(isReplyCountReconciliationNeeded(postID)).toBe(true);
    expect(isReplyCountReconciliationNeeded(postID + 1)).toBe(false);

    expect(commitReplyCountMutation({ postId: postID, replyCount: 5 })).toBe(false);
    expect(commitReplyCountRefresh(staleRead, { postId: postID, replyCount: 7 })).toBe(false);
    expect(isReplyCountReconciliationNeeded(postID)).toBe(true);

    const currentRead = beginReplyCountRefresh(postID);
    expect(commitReplyCountRefresh(currentRead, { postId: postID, replyCount: 5 })).toBe(true);
    expect(isReplyCountReconciliationNeeded(postID)).toBe(false);
    expect(home.applyReplyCountUpdateLocal.mock.calls).toEqual([
      [{ postId: postID, replyCount: 6 }],
      [{ postId: postID, replyCount: 5 }],
    ]);
  });

  it('rejects invalid reconciliation post IDs without creating a marker', () => {
    registerSinks();

    expect(markReplyCountReconciliationNeeded(0)).toBe(false);
    expect(markReplyCountReconciliationNeeded(Number.NaN)).toBe(false);
    expect(markReplyCountReconciliationNeeded(Number.MAX_SAFE_INTEGER + 1)).toBe(false);
    expect(isReplyCountReconciliationNeeded(0)).toBe(false);
  });

  it('does not update an unregistered detail instance while preserving shared-store updates', () => {
    const { home, postDetail } = registerSinks();
    registerPostDetailSessionSync(null);
    const update = { postId: 42010, replyCount: 3 };

    expect(commitReplyCountMutation(update)).toBe(true);
    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith(update);
    expect(postDetail.applyReplyCountUpdateLocal).not.toHaveBeenCalled();
  });

  it('fans out an absolute Quote count to every registered Post surface exactly once', () => {
    const { home, profile, history, topic, bookmarks, postSearch, quotes, postDetail } = registerSinks();
    const update = { postId: 42, quoteCount: 7 };

    syncExternalQuoteCount(update);

    expect(home.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(home.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(profile.applyQuoteCountUpdateEverywhereLocal).toHaveBeenCalledOnce();
    expect(profile.applyQuoteCountUpdateEverywhereLocal).toHaveBeenCalledWith(update);
    expect(history.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(history.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(topic.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(topic.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(bookmarks.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(bookmarks.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(postSearch.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(quotes.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(postDetail.applyQuoteCountUpdateLocal).toHaveBeenCalledOnce();
    expect(postDetail.applyQuoteCountUpdateLocal).toHaveBeenCalledWith(update);
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

  it('syncs Profile follow and unfollow state to every suggested list once', () => {
    const { home, profile, search, connections } = registerSinks();
    const first = { applyExternalFollowStateLocal: vi.fn().mockReturnValue(true) };
    const second = { applyExternalFollowStateLocal: vi.fn().mockReturnValue(true) };
    const unregisterFirst = registerSuggestedUsersSessionSync(first);
    const unregisterSecond = registerSuggestedUsersSessionSync(second);
    const followedState = { ...followState, following: true };

    try {
      syncProfileFollowState(followedState);
      expect(first.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followedState);
      expect(second.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followedState);
      expect(home.reconcileFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followedState);
      expect(search.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followedState);
      expect(connections.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followedState);

      syncProfileFollowState(followState);
      expect(first.applyExternalFollowStateLocal).toHaveBeenNthCalledWith(2, followState);
      expect(second.applyExternalFollowStateLocal).toHaveBeenNthCalledWith(2, followState);
      expect(home.reconcileFollowStateLocal).toHaveBeenNthCalledWith(2, followState);
      expect(search.applyExternalFollowStateLocal).toHaveBeenNthCalledWith(2, followState);
      expect(connections.applyExternalFollowStateLocal).toHaveBeenNthCalledWith(2, followState);

      unregisterFirst();
      syncProfileFollowState(followedState);
      expect(first.applyExternalFollowStateLocal).toHaveBeenCalledTimes(2);
      expect(second.applyExternalFollowStateLocal).toHaveBeenNthCalledWith(3, followedState);
      expect(home.reconcileFollowStateLocal).toHaveBeenCalledTimes(3);
      expect(search.applyExternalFollowStateLocal).toHaveBeenCalledTimes(3);
      expect(connections.applyExternalFollowStateLocal).toHaveBeenCalledTimes(3);
      expect(profile.applyExternalFollowStateLocal).not.toHaveBeenCalled();
    } finally {
      unregisterFirst();
      unregisterSecond();
    }
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
    const { home, profile, history, quotes } = registerSinks();

    syncHomeLikeState(likeUpdate);
    syncHomeRepostState(repostUpdate);
    syncProfileLikeState(likeUpdate);
    syncProfileRepostState(repostUpdate);
    syncHomeBookmarkState(bookmarkUpdate);
    syncProfileBookmarkState(bookmarkUpdate);
    syncHistoryBookmarkState(bookmarkUpdate);

    expect(profile.applyLikeStateUpdateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(home.applyLikeStateUpdateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledTimes(2);
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledTimes(2);
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledTimes(2);
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledTimes(3);
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
  });

  it('fans out home and profile identity updates to History and Connections', () => {
    const { home, profile, postSearch, history, topic, connections, bookmarks } = registerSinks();
    const author = { id: 8, username: 'new-name', display_name: 'New Name', avatar_url: '' };

    syncHomeAuthorIdentity(author);
    syncProfileAuthorIdentity(author);

    expect(home.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
    expect(profile.replaceAuthorIdentityEverywhereLocal).toHaveBeenCalledWith(author);
    expect(history.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(connections.replaceUserIdentityLocal).toHaveBeenCalledTimes(2);
    expect(bookmarks.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(bookmarks.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
    expect(topic.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(topic.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
    expect(postSearch.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(postSearch.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
  });

  it('broadcasts Topic-originated engagement to other surfaces without self-broadcast', () => {
    const { home, profile, postSearch, history, topic, bookmarks, quotes } = registerSinks();

    syncTopicLikeState(likeUpdate);
    syncTopicRepostState(repostUpdate);
    syncTopicBookmarkState(bookmarkUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(home.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(profile.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(history.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(home.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(profile.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(history.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(topic.applyExternalLikeStateLocal).not.toHaveBeenCalled();
    expect(topic.applyExternalRepostStateLocal).not.toHaveBeenCalled();
    expect(topic.applyExternalBookmarkStateLocal).not.toHaveBeenCalled();
    expect(postSearch.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(postSearch.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(postSearch.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
  });

  it('fans out Post Search engagement to other surfaces without self-sync', () => {
    const { home, profile, postSearch, history, topic, bookmarks, quotes } = registerSinks();

    syncPostSearchLikeState(likeUpdate);
    syncPostSearchRepostState(repostUpdate);
    syncPostSearchBookmarkState(bookmarkUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(topic.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(home.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(profile.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(history.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(topic.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(home.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(profile.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(history.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(topic.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
    expect(postSearch.applyExternalLikeStateLocal).not.toHaveBeenCalled();
    expect(postSearch.applyExternalRepostStateLocal).not.toHaveBeenCalled();
    expect(postSearch.applyExternalBookmarkStateLocal).not.toHaveBeenCalled();
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledOnce();
    expect(quotes.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(bookmarkUpdate);
  });

  it('fans out Quotes Like, Repost, and Bookmark only to other surfaces', () => {
    const { home, profile, postSearch, history, topic, bookmarks, postDetail, quotes } = registerSinks();
    const bookmarkPostID = 42002;
    const quoteBookmarkUpdate = { ...bookmarkUpdate, postId: bookmarkPostID };
    const versionBefore = captureBookmarkStateSyncVersion(bookmarkPostID);

    syncQuotesLikeState(likeUpdate);
    syncQuotesRepostState(repostUpdate);
    syncQuotesBookmarkState(quoteBookmarkUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(topic.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(bookmarks.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(postSearch.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(home.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(profile.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(history.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(topic.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(bookmarks.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(postSearch.applyExternalRepostStateLocal).toHaveBeenCalledWith(repostUpdate);
    expect(home.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(profile.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(history.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(topic.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(bookmarks.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(postDetail.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(postSearch.applyExternalBookmarkStateLocal).toHaveBeenCalledWith(quoteBookmarkUpdate);
    expect(quotes.applyExternalLikeStateLocal).not.toHaveBeenCalled();
    expect(quotes.applyExternalRepostStateLocal).not.toHaveBeenCalled();
    expect(quotes.applyExternalBookmarkStateLocal).not.toHaveBeenCalled();
    expect(captureBookmarkStateSyncVersion(bookmarkPostID)).toBe(versionBefore + 1);
  });

  it('clears the Quotes registration when null is registered', () => {
    const { quotes } = registerSinks();

    registerQuotesSessionSync(null);
    syncExternalPostLikeState(likeUpdate);

    expect(quotes.applyExternalLikeStateLocal).not.toHaveBeenCalled();
  });

  it('fans follow state into all mounted suggested-user lists and unregisters them', () => {
    const first = { applyExternalFollowStateLocal: vi.fn().mockReturnValue(true) };
    const second = { applyExternalFollowStateLocal: vi.fn().mockReturnValue(true) };
    const unregisterFirst = registerSuggestedUsersSessionSync(first);
    const unregisterSecond = registerSuggestedUsersSessionSync(second);

    syncExternalFollowState(followState);
    expect(first.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followState);
    expect(second.applyExternalFollowStateLocal).toHaveBeenCalledExactlyOnceWith(followState);

    unregisterFirst();
    syncExternalFollowState(followState);
    expect(first.applyExternalFollowStateLocal).toHaveBeenCalledOnce();
    expect(second.applyExternalFollowStateLocal).toHaveBeenCalledTimes(2);
    unregisterSecond();
  });
});
