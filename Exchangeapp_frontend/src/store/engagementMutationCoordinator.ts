import { reactive } from 'vue';

export type EngagementMutationKind = 'like' | 'repost' | 'bookmark';

export type EngagementMutationResult = 'succeeded' | 'failed' | 'ignored';

export type EngagementMutationToken = {
  kind: EngagementMutationKind;
  postId: number;
  version: number;
  generation: number;
};

export const createEngagementMutationCoordinator = () => {
  const pending = {
    like: reactive(new Set<number>()),
    repost: reactive(new Set<number>()),
    bookmark: reactive(new Set<number>()),
  };
  const versions: Record<EngagementMutationKind, Map<number, number>> = {
    like: new Map(),
    repost: new Map(),
    bookmark: new Map(),
  };
  const generations: Record<EngagementMutationKind, number> = {
    like: 0,
    repost: 0,
    bookmark: 0,
  };

  const getVersion = (kind: EngagementMutationKind, postId: number) => versions[kind].get(postId) ?? 0;

  const begin = (kind: EngagementMutationKind, postId: number): EngagementMutationToken => {
    const version = getVersion(kind, postId) + 1;
    versions[kind].set(postId, version);
    pending[kind].add(postId);
    return { kind, postId, version, generation: generations[kind] };
  };

  const isCurrent = (token: EngagementMutationToken) => (
    generations[token.kind] === token.generation
    && getVersion(token.kind, token.postId) === token.version
    && pending[token.kind].has(token.postId)
  );

  const settle = (token: EngagementMutationToken) => {
    if (!isCurrent(token)) return false;
    versions[token.kind].set(token.postId, token.version + 1);
    pending[token.kind].delete(token.postId);
    return true;
  };

  const invalidate = (kind: EngagementMutationKind, postId: number) => {
    versions[kind].set(postId, getVersion(kind, postId) + 1);
    pending[kind].delete(postId);
  };

  const invalidatePost = (postId: number) => {
    invalidate('like', postId);
    invalidate('repost', postId);
    invalidate('bookmark', postId);
  };

  const resetKind = (kind: EngagementMutationKind) => {
    generations[kind] += 1;
    pending[kind].clear();
    versions[kind].clear();
  };

  const resetAll = () => {
    resetKind('like');
    resetKind('repost');
    resetKind('bookmark');
  };

  return {
    likePendingPostIDs: pending.like,
    repostPendingPostIDs: pending.repost,
    bookmarkPendingPostIDs: pending.bookmark,
    begin,
    isCurrent,
    settle,
    invalidate,
    invalidatePost,
    resetKind,
    resetAll,
    getVersion,
  };
};
