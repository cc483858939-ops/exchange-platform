import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getPostById: vi.fn(),
  syncExternalQuoteCount: vi.fn(),
}));

vi.mock('../services/postService', () => ({ getPostById: mocks.getPostById }));
vi.mock('./sessionSync', () => ({ syncExternalQuoteCount: mocks.syncExternalQuoteCount }));

import { refreshAndSyncPostQuoteCount } from './postQuoteCountReconciliation';

const post = (id: number, quoteCount: unknown) => ({ id, quote_count: quoteCount });

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

describe('postQuoteCountReconciliation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('reads the target Post and fans out its canonical absolute quote_count', async () => {
    mocks.getPostById.mockResolvedValueOnce(post(42, 6));

    await expect(refreshAndSyncPostQuoteCount(42)).resolves.toBe(true);

    expect(mocks.getPostById).toHaveBeenCalledOnce();
    expect(mocks.getPostById).toHaveBeenCalledWith('42');
    expect(mocks.syncExternalQuoteCount).toHaveBeenCalledOnce();
    expect(mocks.syncExternalQuoteCount).toHaveBeenCalledWith({ postId: 42, quoteCount: 6 });
  });

  it.each([-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, '6', null])(
    'rejects malformed canonical quote_count %s without fanout',
    async quoteCount => {
      mocks.getPostById.mockResolvedValueOnce(post(42, quoteCount));

      await expect(refreshAndSyncPostQuoteCount(42)).resolves.toBe(false);
      expect(mocks.syncExternalQuoteCount).not.toHaveBeenCalled();
    },
  );

  it('rejects a canonical Post response for a different ID', async () => {
    mocks.getPostById.mockResolvedValueOnce(post(99, 6));

    await expect(refreshAndSyncPostQuoteCount(42)).resolves.toBe(false);
    expect(mocks.syncExternalQuoteCount).not.toHaveBeenCalled();
  });

  it('returns false for invalid target IDs without making a request', async () => {
    await expect(refreshAndSyncPostQuoteCount(0)).resolves.toBe(false);
    await expect(refreshAndSyncPostQuoteCount(Number.NaN)).resolves.toBe(false);
    await expect(refreshAndSyncPostQuoteCount('not-an-id')).resolves.toBe(false);
    expect(mocks.getPostById).not.toHaveBeenCalled();
  });

  it('swallows target GET failures and does not fan out', async () => {
    mocks.getPostById.mockRejectedValueOnce(new Error('offline'));

    await expect(refreshAndSyncPostQuoteCount(42)).resolves.toBe(false);
    expect(mocks.syncExternalQuoteCount).not.toHaveBeenCalled();
  });

  it('allows only the latest overlapping refresh to apply', async () => {
    const older = deferred<ReturnType<typeof post>>();
    const newer = deferred<ReturnType<typeof post>>();
    mocks.getPostById.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);

    const olderRefresh = refreshAndSyncPostQuoteCount(42);
    const newerRefresh = refreshAndSyncPostQuoteCount(42);
    newer.resolve(post(42, 5));
    await expect(newerRefresh).resolves.toBe(true);
    older.resolve(post(42, 4));
    await expect(olderRefresh).resolves.toBe(false);

    expect(mocks.syncExternalQuoteCount).toHaveBeenCalledOnce();
    expect(mocks.syncExternalQuoteCount).toHaveBeenCalledWith({ postId: 42, quoteCount: 5 });
  });
});
