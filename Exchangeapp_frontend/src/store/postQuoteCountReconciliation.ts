import { getPostById } from '../services/postService';
import { normalizeQuoteCount } from '../utils/quoteCount';
import { syncExternalQuoteCount } from './sessionSync';

const refreshVersions = new Map<number, number>();

const normalizePostID = (value: unknown) => {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value > 0 ? value : null;
  }
  if (typeof value !== 'string' || !value.trim()) return null;
  const postID = Number(value.trim());
  return Number.isSafeInteger(postID) && postID > 0 ? postID : null;
};

export const refreshAndSyncPostQuoteCount = async (rawPostID: unknown): Promise<boolean> => {
  const postID = normalizePostID(rawPostID);
  if (postID === null) return false;

  const version = (refreshVersions.get(postID) ?? 0) + 1;
  refreshVersions.set(postID, version);

  try {
    const post = await getPostById(String(postID));
    if (refreshVersions.get(postID) !== version) return false;
    const quoteCount = normalizeQuoteCount(post.quote_count);
    if (post.id !== postID || quoteCount === null) return false;

    syncExternalQuoteCount({ postId: postID, quoteCount });
    return true;
  } catch {
    return false;
  }
};
