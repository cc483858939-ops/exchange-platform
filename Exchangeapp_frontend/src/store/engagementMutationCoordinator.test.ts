import { isReactive } from 'vue';
import { describe, expect, it } from 'vitest';
import { createEngagementMutationCoordinator } from './engagementMutationCoordinator';

describe('engagement mutation coordinator', () => {
  it('begins a pending mutation and returns a current token', () => {
    const mutations = createEngagementMutationCoordinator();

    const token = mutations.begin('like', 42);

    expect(token).toMatchObject({ kind: 'like', postId: 42, version: 1, generation: 0 });
    expect(mutations.likePendingPostIDs.has(42)).toBe(true);
    expect(isReactive(mutations.likePendingPostIDs)).toBe(true);
    expect(mutations.isCurrent(token)).toBe(true);
  });

  it('keeps engagement kinds independent', () => {
    const mutations = createEngagementMutationCoordinator();
    const like = mutations.begin('like', 42);
    const repost = mutations.begin('repost', 42);
    const bookmark = mutations.begin('bookmark', 42);

    mutations.invalidate('like', 42);

    expect(mutations.isCurrent(like)).toBe(false);
    expect(mutations.likePendingPostIDs.has(42)).toBe(false);
    expect(mutations.isCurrent(repost)).toBe(true);
    expect(mutations.repostPendingPostIDs.has(42)).toBe(true);
    expect(mutations.isCurrent(bookmark)).toBe(true);
    expect(mutations.bookmarkPendingPostIDs.has(42)).toBe(true);
  });

  it('keeps mutations for different posts independent', () => {
    const mutations = createEngagementMutationCoordinator();
    const first = mutations.begin('like', 1);
    const second = mutations.begin('like', 2);

    expect(mutations.settle(first)).toBe(true);
    expect(mutations.likePendingPostIDs.has(1)).toBe(false);
    expect(mutations.isCurrent(second)).toBe(true);

    mutations.invalidate('like', 1);
    expect(mutations.isCurrent(second)).toBe(true);
  });

  it('settles a current token and advances its version', () => {
    const mutations = createEngagementMutationCoordinator();
    const token = mutations.begin('repost', 7);

    expect(mutations.settle(token)).toBe(true);
    expect(mutations.isCurrent(token)).toBe(false);
    expect(mutations.repostPendingPostIDs.has(7)).toBe(false);
    expect(mutations.getVersion('repost', 7)).toBe(token.version + 1);
  });

  it('does not let a stale settle clear a newer mutation', () => {
    const mutations = createEngagementMutationCoordinator();
    const stale = mutations.begin('like', 1);
    mutations.invalidate('like', 1);
    const current = mutations.begin('like', 1);

    expect(mutations.settle(stale)).toBe(false);
    expect(mutations.isCurrent(current)).toBe(true);
    expect(mutations.likePendingPostIDs.has(1)).toBe(true);
  });

  it('invalidates one kind and post without clearing another post', () => {
    const mutations = createEngagementMutationCoordinator();
    const token = mutations.begin('bookmark', 1);

    mutations.invalidate('bookmark', 1);

    expect(mutations.bookmarkPendingPostIDs.has(1)).toBe(false);
    expect(mutations.isCurrent(token)).toBe(false);
  });

  it('invalidates every engagement kind for one post only', () => {
    const mutations = createEngagementMutationCoordinator();
    const like = mutations.begin('like', 1);
    const repost = mutations.begin('repost', 1);
    const bookmark = mutations.begin('bookmark', 1);
    const otherPost = mutations.begin('like', 2);

    mutations.invalidatePost(1);

    expect(mutations.isCurrent(like)).toBe(false);
    expect(mutations.isCurrent(repost)).toBe(false);
    expect(mutations.isCurrent(bookmark)).toBe(false);
    expect(mutations.isCurrent(otherPost)).toBe(true);
    expect(mutations.likePendingPostIDs.has(2)).toBe(true);
  });

  it('resets one kind without invalidating another kind', () => {
    const mutations = createEngagementMutationCoordinator();
    const firstLike = mutations.begin('like', 1);
    const secondLike = mutations.begin('like', 2);
    const repost = mutations.begin('repost', 1);

    mutations.resetKind('like');

    expect(mutations.likePendingPostIDs.size).toBe(0);
    expect(mutations.isCurrent(firstLike)).toBe(false);
    expect(mutations.isCurrent(secondLike)).toBe(false);
    expect(mutations.isCurrent(repost)).toBe(true);
  });

  it('keeps old tokens stale when resetKind clears versions', () => {
    const mutations = createEngagementMutationCoordinator();
    const oldToken = mutations.begin('like', 1);

    mutations.resetKind('like');
    const newToken = mutations.begin('like', 1);

    expect(newToken.version).toBe(oldToken.version);
    expect(newToken.generation).toBe(oldToken.generation + 1);
    expect(mutations.isCurrent(oldToken)).toBe(false);
    expect(mutations.isCurrent(newToken)).toBe(true);
  });

  it('resets all kinds and invalidates all captured tokens', () => {
    const mutations = createEngagementMutationCoordinator();
    const tokens = [
      mutations.begin('like', 1),
      mutations.begin('repost', 1),
      mutations.begin('bookmark', 1),
    ];

    mutations.resetAll();

    expect(mutations.likePendingPostIDs.size).toBe(0);
    expect(mutations.repostPendingPostIDs.size).toBe(0);
    expect(mutations.bookmarkPendingPostIDs.size).toBe(0);
    tokens.forEach(token => expect(mutations.isCurrent(token)).toBe(false));
  });

  it('creates isolated coordinator state for each factory call', () => {
    const first = createEngagementMutationCoordinator();
    const second = createEngagementMutationCoordinator();
    const token = first.begin('like', 4);

    expect(first.isCurrent(token)).toBe(true);
    expect(second.isCurrent(token)).toBe(false);
    expect(second.likePendingPostIDs.size).toBe(0);
    expect(first.likePendingPostIDs).not.toBe(second.likePendingPostIDs);
  });
});
