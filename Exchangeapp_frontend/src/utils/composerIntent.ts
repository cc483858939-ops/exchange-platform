import { isValidQuotePostID } from './postDraftSnapshot';

export type ComposerIntent =
  | { kind: 'new' }
  | { kind: 'quote'; postID: number }
  | { kind: 'draft'; draftID: string };

export const parseComposerIntent = (query: Record<string, unknown>): ComposerIntent => {
  const draftID = query.draft;
  if (typeof draftID === 'string' && draftID.trim()) {
    return { kind: 'draft', draftID: draftID.trim() };
  }

  const rawQuoteID = query.quote;
  if (typeof rawQuoteID === 'string' && /^\d+$/.test(rawQuoteID)) {
    const postID = Number(rawQuoteID);
    if (isValidQuotePostID(postID)) {
      return { kind: 'quote', postID };
    }
  }

  return { kind: 'new' };
};

export const composerIntentKey = (intent: ComposerIntent): string => {
  switch (intent.kind) {
    case 'draft':
      return `draft:${intent.draftID}`;
    case 'quote':
      return `quote:${intent.postID}`;
    case 'new':
    default:
      return 'new';
  }
};

export const composerIntentsEqual = (left: ComposerIntent, right: ComposerIntent): boolean => (
  composerIntentKey(left) === composerIntentKey(right)
);
