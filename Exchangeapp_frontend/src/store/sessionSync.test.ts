import { describe, expect, it, vi } from 'vitest';
import type { FeedLikeStateUpdate } from '../types/Feed';
import type { UserFollowState } from '../services/userService';
import {
  registerHomeTimelineSync,
  registerProfileSessionSync,
  syncExternalArticleLikeState,
  syncExternalArticleRemoval,
  syncExternalCommentCount,
  syncExternalFollowState,
  syncProfileFollowState,
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
    applyCommentCountUpdateLocal: vi.fn().mockReturnValue(true),
    reconcileFollowStateLocal: vi.fn().mockReturnValue(true),
    removeArticleLocal: vi.fn(),
    replaceAuthorIdentityLocal: vi.fn(),
  };
  const profile = {
    applyLikeStateUpdateLocal: vi.fn().mockReturnValue(true),
    applyExternalLikeStateLocal: vi.fn().mockReturnValue(true),
    applyCommentCountUpdateEverywhereLocal: vi.fn().mockReturnValue(true),
    applyExternalFollowStateLocal: vi.fn().mockReturnValue(true),
    removeArticleEverywhereLocal: vi.fn(),
    replaceAuthorIdentityEverywhereLocal: vi.fn(),
  };
  registerHomeTimelineSync(home);
  registerProfileSessionSync(profile);
  return { home, profile };
};

describe('sessionSync external mutation sinks', () => {
  it('sends external likes to Home and Profile exactly once', () => {
    const { home, profile } = registerSinks();

    syncExternalArticleLikeState(likeUpdate);

    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(home.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalLikeStateLocal).toHaveBeenCalledWith(likeUpdate);
  });

  it('sends external removals to Home and Profile exactly once', () => {
    const { home, profile } = registerSinks();

    syncExternalArticleRemoval(42);

    expect(home.removeArticleLocal).toHaveBeenCalledOnce();
    expect(home.removeArticleLocal).toHaveBeenCalledWith(42);
    expect(profile.removeArticleEverywhereLocal).toHaveBeenCalledOnce();
    expect(profile.removeArticleEverywhereLocal).toHaveBeenCalledWith(42);
  });

  it('sends absolute comment counts to both caches', () => {
    const { home, profile } = registerSinks();
    const update = { articleId: 42, commentCount: 5 };

    syncExternalCommentCount(update);

    expect(home.applyCommentCountUpdateLocal).toHaveBeenCalledWith(update);
    expect(profile.applyCommentCountUpdateEverywhereLocal).toHaveBeenCalledWith(update);
  });

  it('routes Profile follow success only to Home reconciliation', () => {
    const { home, profile } = registerSinks();

    syncProfileFollowState(followState);

    expect(home.reconcileFollowStateLocal).toHaveBeenCalledWith(followState);
    expect(profile.applyExternalFollowStateLocal).not.toHaveBeenCalled();
  });

  it('routes external follow success to Home and Profile', () => {
    const { home, profile } = registerSinks();

    syncExternalFollowState(followState);

    expect(home.reconcileFollowStateLocal).toHaveBeenCalledOnce();
    expect(profile.applyExternalFollowStateLocal).toHaveBeenCalledOnce();
  });
});
