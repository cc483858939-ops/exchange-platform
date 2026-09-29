// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { flushPromises } from '@vue/test-utils';
import { reactive } from 'vue';
import type { Post } from '../types/Post';
import { useReplyDraftStore } from './replyDraft';
import { useReplySubmissionStore } from './replySubmission';

const mocks = vi.hoisted(() => ({
  records: new Map<string, any>(),
  drafts: new Map<string, any>(),
  authStore: { isAuthenticated: true, currentIdentity: { id: 7 } } as any,
  createPostReply: vi.fn(),
  createClientOperationID: vi.fn(),
  replaceReplySubmissionOperation: vi.fn(),
}));

const storageKey = (viewerID: number, parentPostID: number) => `${viewerID}:${parentPostID}`;

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/replyService', () => ({ createPostReply: mocks.createPostReply }));
vi.mock('../utils/clientOperationId', () => ({ createClientOperationID: mocks.createClientOperationID }));
vi.mock('../storage/replyStorage', () => ({
  replyStorageKey: (viewerID: number, parentPostID: number) => `${viewerID}:${parentPostID}`,
  getReplyDraft: vi.fn(async (viewerID: number, parentPostID: number) => mocks.drafts.get(storageKey(viewerID, parentPostID)) ?? null),
  saveReplyDraft: vi.fn(async (record: any) => { mocks.drafts.set(record.key, record); }),
  deleteReplyDraft: vi.fn(async (viewerID: number, parentPostID: number) => mocks.drafts.delete(storageKey(viewerID, parentPostID))),
  deleteReplyDraftIfUnchanged: vi.fn(async (viewerID: number, parentPostID: number, expected: string) => {
    const key = storageKey(viewerID, parentPostID);
    const current = mocks.drafts.get(key);
    if (!current) return 'missing';
    if (current.content !== expected) return 'changed';
    mocks.drafts.delete(key);
    return 'deleted';
  }),
  getReplySubmissionOperation: vi.fn(async (viewerID: number, parentPostID: number) => mocks.records.get(storageKey(viewerID, parentPostID)) ?? null),
  listReplySubmissionOperations: vi.fn(async (viewerID: number) => Array.from(mocks.records.values()).filter((item: any) => item.viewerID === viewerID)),
  replaceReplySubmissionOperation: (...args: any[]) => mocks.replaceReplySubmissionOperation(...args),
  updateReplySubmissionOperation: vi.fn(async (operation: any) => {
    const current = mocks.records.get(operation.key);
    if (!current || current.id !== operation.id) return false;
    mocks.records.set(operation.key, operation);
    return true;
  }),
  deleteReplySubmissionOperation: vi.fn(async (viewerID: number, parentPostID: number, operationID: string) => {
    const key = storageKey(viewerID, parentPostID);
    const current = mocks.records.get(key);
    if (!current || current.id !== operationID) return false;
    mocks.records.delete(key);
    return true;
  }),
}));

const replyPost = (id = 101, parentPostID = 42): Post => ({
  id,
  created_at: '2026-09-29T00:00:00.000Z',
  updated_at: '2026-09-29T00:00:00.000Z',
  published_at: '2026-09-29T00:00:00.000Z',
  author: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' },
  content: 'reply',
  language: 'und',
  conversation_id: parentPostID,
  reply_to_post_id: parentPostID,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  repost_count: 0,
  reply_count: 0,
  view_count: 0,
  deleted: false,
});

const record = (overrides: Partial<any> = {}) => ({
  key: '7:42', id: 'operation-a', viewerID: 7, parentPostID: 42,
  content: 'hello', sourceDraftContent: null, phase: 'publishing',
  failureKind: null, error: '', startedAt: 1, updatedAt: 1, post: null,
  ...overrides,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
};

const store = () => useReplySubmissionStore();
const drafts = () => useReplyDraftStore();

describe('replySubmission store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.records.clear();
    mocks.drafts.clear();
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.createClientOperationID.mockReset().mockReturnValue('operation-new');
    mocks.createPostReply.mockReset().mockResolvedValue(replyPost());
    mocks.replaceReplySubmissionOperation.mockReset().mockImplementation(async (operation: any) => {
      mocks.records.set(operation.key, operation);
    });
  });

  afterEach(() => vi.restoreAllMocks());

  it('writes the immutable operation durably before starting reply HTTP', async () => {
    const write = deferred<void>();
    mocks.replaceReplySubmissionOperation.mockReturnValueOnce(write.promise);
    drafts().setViewer(7);
    drafts().setDraft(42, '  hello  ');

    const starting = store().startOrRetry(42, '  hello  ');
    await flushPromises();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(mocks.replaceReplySubmissionOperation).toHaveBeenCalledWith(expect.objectContaining({
      id: 'operation-new', parentPostID: 42, content: 'hello', sourceDraftContent: null, phase: 'publishing',
    }));

    write.resolve();
    expect((await starting).status).toBe('accepted');
    await flushPromises();
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', { idempotencyKey: 'operation-new' });
  });

  it('fails closed when initial persistence fails', async () => {
    mocks.replaceReplySubmissionOperation.mockRejectedValueOnce(new Error('quota'));
    drafts().setViewer(7);
    drafts().setDraft(42, 'preserved reply');

    expect(await store().startOrRetry(42, 'preserved reply')).toEqual({
      status: 'rejected', reason: 'persistence_unavailable',
    });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(drafts().getDraft(42)).toBe('preserved reply');
  });

  it('recovers a persisted publishing operation with the same ID, parent, and canonical content', async () => {
    mocks.records.set('7:42', record({ content: '  hello  '.trim(), phase: 'publishing' }));
    const pending = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(pending.promise);
    await store().activateViewer(7);
    await flushPromises();

    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', { idempotencyKey: 'operation-a' });
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    pending.resolve(replyPost());
    await flushPromises();
  });

  it('does not auto-retry failed operations and reuses the original operation on an exact retry', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable', error: 'offline' }));
    await store().activateViewer(7);
    await flushPromises();
    expect(mocks.createPostReply).not.toHaveBeenCalled();

    const result = await store().startOrRetry(42, ' hello ');
    expect(result).toMatchObject({ status: 'accepted', operation: { id: 'operation-a' } });
    await flushPromises();
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', { idempotencyKey: 'operation-a' });
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
  });

  it('blocks edited content and idempotency conflicts without generating a replacement key', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    await store().activateViewer(7);
    expect(await store().startOrRetry(42, 'edited')).toEqual({ status: 'blocked', reason: 'unresolved_reply' });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();

    const existing = store().getOperation(7, 42)!;
    existing.failureKind = 'idempotency_conflict';
    existing.phase = 'failed';
    expect(await store().startOrRetry(42, 'hello')).toEqual({ status: 'blocked', reason: 'idempotency_conflict' });
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
  });

  it('persists an idempotency conflict as non-retryable and requires explicit abandon', async () => {
    mocks.createPostReply.mockRejectedValueOnce({
      response: { status: 409, data: { code: 'POST_IDEMPOTENCY_CONFLICT' } },
    });
    drafts().setViewer(7);
    drafts().setDraft(42, 'conflicted reply');
    await store().startOrRetry(42, 'conflicted reply');
    await flushPromises();

    expect(store().getOperation(7, 42)).toMatchObject({
      phase: 'failed', failureKind: 'idempotency_conflict',
    });
    expect(await store().startOrRetry(42, 'conflicted reply')).toEqual({
      status: 'blocked', reason: 'idempotency_conflict',
    });
    expect(mocks.createPostReply).toHaveBeenCalledTimes(1);
    expect(mocks.createClientOperationID).toHaveBeenCalledTimes(1);
  });

  it('durably transitions a retry before calling HTTP', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    await store().activateViewer(7);
    const storageModule = await import('../storage/replyStorage');
    const update = vi.mocked(storageModule.updateReplySubmissionOperation);
    const checkpoint = deferred<boolean>();
    update.mockReturnValueOnce(checkpoint.promise);

    const retrying = store().retry('operation-a');
    await flushPromises();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(update).toHaveBeenCalledWith(expect.objectContaining({ phase: 'publishing', failureKind: null }));

    checkpoint.resolve(true);
    expect(await retrying).toBe(true);
    await flushPromises();
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', { idempotencyKey: 'operation-a' });
  });

  it('abandons only after durable conditional deletion and preserves the editor', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    await store().activateViewer(7);
    drafts().setDraft(42, 'newer reply');

    expect(await store().abandonFailedOperation('operation-a')).toBe(true);
    expect(drafts().getDraft(42)).toBe('newer reply');
    expect(mocks.records.has('7:42')).toBe(false);
    expect(await store().startOrRetry(42, 'newer reply')).toMatchObject({ status: 'accepted', operation: { id: 'operation-new' } });
  });

  it('preserves a newer saved draft when an older operation succeeds', async () => {
    const replyRequest = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(replyRequest.promise);
    drafts().setViewer(7);
    drafts().setDraft(42, 'saved S1');
    await drafts().saveDraft(42);
    const starting = await store().startOrRetry(42, 'saved S1');
    expect(starting.status).toBe('accepted');
    drafts().setDraft(42, 'saved S2');
    await drafts().saveDraft(42);
    replyRequest.resolve(replyPost());
    await flushPromises();

    expect(drafts().getDraft(42)).toBe('saved S2');
    expect(mocks.drafts.get('7:42')?.content).toBe('saved S2');
    expect(mocks.records.has('7:42')).toBe(false);
  });

  it('does not delete a saved source for an unsaved reply derivative', async () => {
    drafts().setViewer(7);
    drafts().setDraft(42, 'saved original');
    await drafts().saveDraft(42);
    drafts().setDraft(42, 'unsaved derivative');

    const result = await store().startOrRetry(42, 'unsaved derivative');
    expect(result).toMatchObject({
      status: 'accepted', operation: { content: 'unsaved derivative', sourceDraftContent: null },
    });
    await flushPromises();

    expect(mocks.drafts.get('7:42')?.content).toBe('saved original');
    expect(drafts().getSavedContent(42)).toBe('saved original');
  });

  it('deletes an unchanged saved source only after a successful reply', async () => {
    drafts().setViewer(7);
    drafts().setDraft(42, '  exact saved source  ');
    await drafts().saveDraft(42);

    const result = await store().startOrRetry(42, '  exact saved source  ');
    expect(result).toMatchObject({
      status: 'accepted', operation: { content: 'exact saved source', sourceDraftContent: '  exact saved source  ' },
    });
    await flushPromises();

    expect(mocks.drafts.has('7:42')).toBe(false);
    expect(drafts().getDraft(42)).toBe('');
    expect(drafts().hasSavedDraft(42)).toBe(false);
  });

  it('keeps successful operations succeeded and retries cleanup before a new reply', async () => {
    drafts().setViewer(7);
    drafts().setDraft(42, 'first');
    await drafts().saveDraft(42);
    const storageModule = await import('../storage/replyStorage');
    const conditionalDelete = vi.mocked(storageModule.deleteReplyDraftIfUnchanged);
    conditionalDelete.mockRejectedValueOnce(new Error('temporary storage failure'));

    await store().startOrRetry(42, 'first');
    await flushPromises();
    expect(store().getOperation(7, 42)).toMatchObject({ phase: 'succeeded', durableOwned: true, cleanupPending: true });
    expect(mocks.records.get('7:42')?.phase).toBe('succeeded');

    const next = await store().startOrRetry(42, 'second');
    expect(next).toMatchObject({ status: 'accepted', operation: { id: 'operation-new' } });
    expect(mocks.createPostReply).toHaveBeenNthCalledWith(2, 42, 'second', { idempotencyKey: 'operation-new' });
  });

  it('does not create a new key while succeeded cleanup remains unavailable', async () => {
    mocks.records.set('7:42', record({
      phase: 'succeeded', post: replyPost(), sourceDraftContent: 'saved source',
    }));
    mocks.drafts.set('7:42', {
      key: '7:42', viewerID: 7, parentPostID: 42,
      content: 'saved source', createdAt: 1, updatedAt: 2,
    });
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.deleteReplyDraftIfUnchanged).mockRejectedValue(new Error('storage down'));

    const result = await store().startOrRetry(42, 'next reply');
    expect(result).toEqual({ status: 'blocked', reason: 'cleanup_pending' });
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(mocks.records.get('7:42')?.phase).toBe('succeeded');
  });

  it('recovers publishing operations independently across multiple parent posts', async () => {
    mocks.records.set('7:42', record({ id: 'operation-a', content: 'first' }));
    mocks.records.set('7:43', record({
      key: '7:43', id: 'operation-b', parentPostID: 43, content: 'second',
    }));
    await store().activateViewer(7);
    await flushPromises();

    expect(mocks.createPostReply).toHaveBeenCalledTimes(2);
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'first', { idempotencyKey: 'operation-a' });
    expect(mocks.createPostReply).toHaveBeenCalledWith(43, 'second', { idempotencyKey: 'operation-b' });
  });

  it('keeps viewer operations private when account identity changes', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    await store().activateViewer(7);
    mocks.authStore.currentIdentity.id = 8;
    await store().activateViewer(8);

    expect(store().getOperation(7, 42)).toBeNull();
    expect(await store().startOrRetry(42, 'other user')).toMatchObject({
      status: 'accepted', operation: { viewerID: 8, parentPostID: 42 },
    });
    expect(mocks.records.has('7:42')).toBe(true);
  });

  it('does not repopulate old viewer memory when an in-flight request completes after logout', async () => {
    const request = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(request.promise);
    drafts().setViewer(7);
    drafts().setDraft(42, 'in-flight private reply');
    await store().startOrRetry(42, 'in-flight private reply');

    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    await store().activateViewer(null);
    expect(store().operations).toEqual([]);

    request.resolve(replyPost());
    await flushPromises();
    expect(store().operations).toEqual([]);
  });
});
