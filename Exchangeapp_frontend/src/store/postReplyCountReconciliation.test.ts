// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({ getPostById: vi.fn() }));

vi.mock('../services/postService', () => ({ getPostById: mocks.getPostById }));

import {
  beginReplyCountRefresh,
  commitReplyCountRefresh,
  isReplyCountReconciliationNeeded,
  markReplyCountReconciliationNeeded,
  registerHomeTimelineSync,
  registerPostDetailSessionSync,
} from './sessionSync';
import { reconcilePostReplyCount } from './postReplyCountReconciliation';

const serverPost = (id: number, replyCount: unknown) => ({
  id,
  reply_count: replyCount,
}) as Post;

const deferred = <T,>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const flushMicrotasks = async () => {
  for (let index = 0; index < 6; index += 1) await Promise.resolve();
};

describe('postReplyCountReconciliation', () => {
  let markedPostIDs: Set<number>;

  const mark = (postID: number) => {
    markedPostIDs.add(postID);
    expect(markReplyCountReconciliationNeeded(postID)).toBe(true);
  };

  beforeEach(() => {
    vi.clearAllMocks();
    markedPostIDs = new Set();
    registerHomeTimelineSync({ applyReplyCountUpdateLocal: vi.fn() } as any);
    registerPostDetailSessionSync(null);
  });

  afterEach(() => {
    vi.clearAllTimers();
    for (const postID of markedPostIDs) {
      if (!isReplyCountReconciliationNeeded(postID)) continue;
      const token = beginReplyCountRefresh(postID);
      commitReplyCountRefresh(token, { postId: postID, replyCount: 0 });
    }
    vi.useRealTimers();
  });

  it('reads and fans out an authoritative reply count before clearing the mark', async () => {
    const home = { applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true) };
    registerHomeTimelineSync(home as any);
    mark(61001);
    mocks.getPostById.mockResolvedValueOnce(serverPost(61001, 5));

    await expect(reconcilePostReplyCount(61001)).resolves.toBe(true);

    expect(mocks.getPostById).toHaveBeenCalledExactlyOnceWith('61001');
    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith({
      postId: 61001,
      replyCount: 5,
    });
    expect(isReplyCountReconciliationNeeded(61001)).toBe(false);
  });

  it('keeps the mark after a failed request and succeeds on its bounded retry', async () => {
    vi.useFakeTimers();
    mark(61002);
    mocks.getPostById
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(serverPost(61002, 5));

    await expect(reconcilePostReplyCount(61002)).resolves.toBe(false);
    expect(isReplyCountReconciliationNeeded(61002)).toBe(true);

    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.getPostById).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(mocks.getPostById).toHaveBeenCalledTimes(2);
    expect(isReplyCountReconciliationNeeded(61002)).toBe(false);
  });

  it('coalesces repeated triggers for the same post into one in-flight request', async () => {
    const response = deferred<Post>();
    mark(61003);
    mocks.getPostById.mockReturnValueOnce(response.promise);

    const first = reconcilePostReplyCount(61003);
    const duplicate = reconcilePostReplyCount('61003');

    expect(duplicate).toBe(first);
    expect(mocks.getPostById).toHaveBeenCalledExactlyOnceWith('61003');
    response.resolve(serverPost(61003, 4));
    await expect(first).resolves.toBe(true);
  });

  it('rejects a read invalidated by a newer reply operation and retries from a fresh snapshot', async () => {
    vi.useFakeTimers();
    const older = deferred<Post>();
    mark(61004);
    mocks.getPostById.mockReturnValueOnce(older.promise);

    const oldReconciliation = reconcilePostReplyCount(61004);
    const freshToken = beginReplyCountRefresh(61004);
    expect(freshToken).not.toBeNull();
    mocks.getPostById.mockResolvedValue(serverPost(61004, 5));

    older.resolve(serverPost(61004, 6));
    await expect(oldReconciliation).resolves.toBe(false);
    expect(isReplyCountReconciliationNeeded(61004)).toBe(true);

    await vi.advanceTimersByTimeAsync(1_000);
    expect(isReplyCountReconciliationNeeded(61004)).toBe(false);
    expect(mocks.getPostById).toHaveBeenCalledTimes(2);
  });

  it('accepts a server-confirmed decrease from six to five', async () => {
    const home = { applyReplyCountUpdateLocal: vi.fn().mockReturnValue(true) };
    registerHomeTimelineSync(home as any);
    mark(61005);
    mocks.getPostById.mockResolvedValueOnce(serverPost(61005, 5));

    await expect(reconcilePostReplyCount(61005)).resolves.toBe(true);

    expect(home.applyReplyCountUpdateLocal).toHaveBeenCalledExactlyOnceWith({
      postId: 61005,
      replyCount: 5,
    });
  });

  it('keeps separate retries for separate posts', async () => {
    vi.useFakeTimers();
    const first = deferred<Post>();
    const second = deferred<Post>();
    mark(61006);
    mark(61007);
    mocks.getPostById.mockImplementation((id: string) => (
      id === '61006' ? first.promise : second.promise
    ));

    const firstRequest = reconcilePostReplyCount(61006);
    const secondRequest = reconcilePostReplyCount(61007);
    first.resolve(serverPost(61006, 3));
    second.reject(new Error('offline'));
    await expect(firstRequest).resolves.toBe(true);
    await expect(secondRequest).resolves.toBe(false);

    expect(isReplyCountReconciliationNeeded(61006)).toBe(false);
    expect(isReplyCountReconciliationNeeded(61007)).toBe(true);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(mocks.getPostById).toHaveBeenCalledTimes(3);
    expect(isReplyCountReconciliationNeeded(61007)).toBe(true);
  });

  it.each([
    ['mismatched post ID', 61009, 5],
    ['negative count', 61008, -1],
    ['fractional count', 61008, 1.5],
    ['non-finite count', 61008, Number.NaN],
  ])('does not clear the mark for an invalid response: %s', async (_caseName, responseID, count) => {
    const postID = 61008;
    mark(postID);
    mocks.getPostById.mockResolvedValueOnce(serverPost(responseID as number, count));

    await expect(reconcilePostReplyCount(postID)).resolves.toBe(false);
    expect(isReplyCountReconciliationNeeded(postID)).toBe(true);
  });

  it('pauses after three automatic retries and resumes after the browser comes online', async () => {
    vi.useFakeTimers();
    mark(61010);
    mocks.getPostById.mockRejectedValue(new Error('offline'));

    await expect(reconcilePostReplyCount(61010)).resolves.toBe(false);
    await vi.advanceTimersByTimeAsync(999);
    expect(mocks.getPostById).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(mocks.getPostById).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(3_000);
    expect(mocks.getPostById).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(mocks.getPostById).toHaveBeenCalledTimes(4);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(mocks.getPostById).toHaveBeenCalledTimes(4);
    expect(isReplyCountReconciliationNeeded(61010)).toBe(true);

    mocks.getPostById.mockResolvedValueOnce(serverPost(61010, 5));
    window.dispatchEvent(new Event('online'));
    await flushMicrotasks();

    expect(mocks.getPostById).toHaveBeenCalledTimes(5);
    expect(isReplyCountReconciliationNeeded(61010)).toBe(false);
  });

  it('does not request a count for an unmarked or invalid post ID', async () => {
    await expect(reconcilePostReplyCount(61011)).resolves.toBe(false);
    await expect(reconcilePostReplyCount(0)).resolves.toBe(false);
    await expect(reconcilePostReplyCount('bad-id')).resolves.toBe(false);
    expect(mocks.getPostById).not.toHaveBeenCalled();
  });
});
