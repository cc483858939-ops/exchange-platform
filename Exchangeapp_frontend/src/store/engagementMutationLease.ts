import type { EngagementMutationKind } from './engagementMutationCoordinator';

export type EngagementMutationLeaseToken = {
  viewerID: number;
  postID: number;
  kind: EngagementMutationKind;
  nonce: number;
};

const isValidResourceID = (value: number) => Number.isSafeInteger(value) && value > 0;

const isValidKind = (kind: unknown): kind is EngagementMutationKind => (
  kind === 'like' || kind === 'repost' || kind === 'bookmark'
);

export const createEngagementMutationLeaseRegistry = () => {
  const active = new Map<string, EngagementMutationLeaseToken>();
  let nonce = 0;

  const keyFor = (viewerID: number, kind: EngagementMutationKind, postID: number) => (
    `${viewerID}:${kind}:${postID}`
  );

  const tryBegin = (
    viewerID: number,
    kind: EngagementMutationKind,
    postID: number,
  ): EngagementMutationLeaseToken | null => {
    if (!isValidResourceID(viewerID) || !isValidKind(kind) || !isValidResourceID(postID)) {
      return null;
    }

    const key = keyFor(viewerID, kind, postID);
    if (active.has(key)) {
      return null;
    }

    const token = { viewerID, kind, postID, nonce: ++nonce };
    active.set(key, token);
    return token;
  };

  const release = (token: EngagementMutationLeaseToken) => {
    const key = keyFor(token.viewerID, token.kind, token.postID);
    if (active.get(key) !== token) {
      return false;
    }
    active.delete(key);
    return true;
  };

  const isLeased = (viewerID: number, kind: EngagementMutationKind, postID: number) => {
    if (!isValidResourceID(viewerID) || !isValidKind(kind) || !isValidResourceID(postID)) {
      return false;
    }
    return active.has(keyFor(viewerID, kind, postID));
  };

  return { tryBegin, release, isLeased };
};

const sharedRegistry = createEngagementMutationLeaseRegistry();

export const tryBeginEngagementMutationLease = (
  viewerID: number,
  kind: EngagementMutationKind,
  postID: number,
) => sharedRegistry.tryBegin(viewerID, kind, postID);

export const releaseEngagementMutationLease = (token: EngagementMutationLeaseToken) => (
  sharedRegistry.release(token)
);

export const isEngagementMutationLeased = (
  viewerID: number,
  kind: EngagementMutationKind,
  postID: number,
) => sharedRegistry.isLeased(viewerID, kind, postID);
