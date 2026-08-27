import { describe, expect, it, vi } from 'vitest';
import type { FeedLikeStateUpdate } from '../types/Feed';
import type { UserFollowState } from '../services/userService';
import {
  registerHomeTimelineSync,
  registerConnectionsSessionSync,
  registerHistorySessionSync,
  registerProfileSessionSync,
  registerSearchSessionSync,
  syncExternalArticleLikeState,
  syncExternalArticleRemoval,
  syncExternalCommentCount,
  syncExternalFollowState,
  syncHomeAuthorIdentity,
  syncHomeLikeState,
  syncProfileAuthorIdentity,
  syncProfileFollowState,
  syncProfileLikeState,
} from './sessionSync';

const likeUpdate: FeedLikeStateUpdate = {
  articleId: 42,
  likes: 9,
  liked: true,
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
    applyCommentCountUpdateLocal: vi.fn().mockReturnValue(true),
    reconcileFollowStateLocal: vi.fn().mockReturnValue(true),
    removeArticleLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const profile = {
    applyLikeStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyRepostStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyCommentCountUpdateEverywhereLocal: vi.fn().mockReturnValue(true),
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    removeArticleEverywhereLocal: vi.fn(),
    replaceAuthorIdentityEverywhereLocal: vi.fn(),
  };
  const search = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
  };
  const history = {
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyExternalRepostStateLocal: vi.fn().mockReturnValue(true),
    applyCommentCountUpdateLocal: vi.fn().mockReturnValue(true),
    removeArticleLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const connections = {
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    replaceUserIdentityLocal: vi.fn().mockReturnValue(true),
  };
  registerHomeTimelineSync(home);
  registerProfileSessionSync(profile);
  registerSearchSessionSync(search);
  registerHistorySessionSync(history);
  registerConnectionsSessionSync(connections);
  return { home, profile, search, history, connections };
};

describe('sessionSync external mutation sinks', () => {
  it('sends external likes to Home and Profile exactly once', () => {
    const { home, profile, history } = registerSinks();

    syncExternalArticleLikeState(likeUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(history.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
  });

  it('sends external removals to Home and Profile exactly once', () => {
    const { home, profile, history } = registerSinks();

    syncExternalArticleRemoval(42);

    expect(home.removeArticleLocal).toHaveBeenCalledOnce();
    expect(home.removeArticleLocal).toHaveBeenCalledWith(42);
    expect(profile.removeArticleEverywhereLocal).toHaveBeenCalledOnce();
    expect(profile.removeArticleEverywhereLocal).toHaveBeenCalledWith(42);
    expect(history.removeArticleLocal).toHaveBeenCalledOnce();
    expect(history.removeArticleLocal).toHaveBeenCalledWith(42);
  });

  it('sends absolute comment counts to both caches', () => {
    const { home, profile, history } = registerSinks();
    const update = { articleId: 42, commentCount: 5 };

    syncExternalCommentCount(update);

    expect(home.applyCommentCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(profile.applyCommentCountUpdateEverywhereLocal).toHaveBeenCalledWith(update);
    expect(history.applyCommentCountUpdateLocal).toHaveBeenCalledWith(update);
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
    const { home, profile, history, connections } = registerSinks();
    const author = { id: 8, username: 'new-name', display_name: 'New Name', avatar_url: '' };

    syncHomeAuthorIdentity(author);
    syncProfileAuthorIdentity(author);

    expect(home.replaceAuthorIdentityLocal).toHaveBeenCalledWith(author);
    expect(profile.replaceAuthorIdentityEverywhereLocal).toHaveBeenCalledWith(author);
    expect(history.replaceAuthorIdentityLocal).toHaveBeenCalledTimes(2);
    expect(connections.replaceUserIdentityLocal).toHaveBeenCalledTimes(2);
  });
});
