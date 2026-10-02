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
  registerPostSearchSessionSync,
  registerBookmarksSessionSync,
  registerPostDetailSessionSync,
  registerQuotesSessionSync,
  registerTopicSessionSync,
  captureBookmarkStateSyncVersion,
  beginBookmarkStateMutation,
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  syncExternalPostBookmarkState,
  syncHydratedPostBookmarkState,
  syncExternalPostRemoval,
  syncExternalReplyCount,
  syncExternalFollowState,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncHomeRepostState,
  syncHomeBookmarkState,
  syncProfileAuthorIdentity,
  syncProfileFollowState,
  syncProfileLikeState,
  syncProfileRepostState,
  syncProfileBookmarkState,
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
  const postSearch = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const history = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
    removePostLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const topic = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
    applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true),
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
    removePostLocal: vi.fn().mockReturnValue(true),
    replaceAuthorIdentityLocal: vi.fn().mockReturnValue(true),
  };
  const quotes = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
  };
  const postDetail = {
    applyExternalBookmarkStateLocal: vi.fn().mockReturnValue(true),
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

  it('sends absolute comment counts to both caches', () => {
    const { home, profile, postSearch, history, topic } = registerSinks();
    const update = { postId: 42, replyCount: 5 };

    syncExternalReplyCount(update);

    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(profile.applyReplyCountUpdateEverywhereLocal).toHaveBeenCalledWith(update);
    expect(history.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(topic.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledOnce();
    expect(postSearch.applyReplyCountUpdateLocal).toHaveBeenCalledWith(update);
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
});
