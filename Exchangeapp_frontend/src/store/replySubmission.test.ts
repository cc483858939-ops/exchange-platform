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
  deleteReplyDraftIfUnchanged: vi.fn(async (
    viewerID: number,
    parentPostID: number,
    expected: string,
    shouldDelete: () => boolean = () => true,
  ) => {
    if (!shouldDelete()) return 'changed';
    const key = storageKey(viewerID, parentPostID);
    const current = mocks.drafts.get(key);
    if (!current) return 'missing';
    if (current.content !== expected) return 'changed';
    if (!shouldDelete()) return 'changed';
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
  quote_count: 0,
  view_count: 0,
  deleted: false,
});

const record = (overrides: Partial<any> = {}) => ({
  key: '7:42', id: 'operation-a', viewerID: 7, viewerSessionID: 'session-7', parentPostID: 42,
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
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7 },
      sessionID: 'session-7',
      sessionVersion: 5,
      captureRequestAuthBinding: () => {
        const auth = mocks.authStore;
        if (!auth.isAuthenticated || !auth.currentIdentity?.id) return null;
        return Object.freeze({ userID: auth.currentIdentity.id, sessionID: auth.sessionID, sessionVersion: auth.sessionVersion });
      },
      matchesRequestAuthBinding: (binding: any) => {
        const auth = mocks.authStore;
        return Boolean(auth.isAuthenticated
          && auth.currentIdentity?.id === binding.userID
          && auth.sessionID === binding.sessionID
          && auth.sessionVersion === binding.sessionVersion);
      },
    });
    mocks.createClientOperationID.mockReset().mockReturnValue('operation-new');
    mocks.createPostReply.mockReset().mockResolvedValue(replyPost());
    mocks.replaceReplySubmissionOperation.mockReset().mockImplementation(async (operation: any) => {
      mocks.records.set(operation.key, operation);
    });
  });

  afterEach(() => vi.restoreAllMocks());

  it('writes the immutable operation durably before starting reply HTTP', async () => {
    const write = deferred<void>();
    mocks.replaceReplySubmissionOperation.mockImplementationOnce(async (operation: any) => {
      await write.promise;
      mocks.records.set(operation.key, operation);
    });
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
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-new',
      authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7', sessionVersion: 5 }),
    }));
    mocks.records.set('7:42', mocks.replaceReplySubmissionOperation.mock.calls[0]?.[0]);
    expect(mocks.records.get('7:42')).toMatchObject({ viewerSessionID: 'session-7' });
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

    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-a', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    pending.resolve(replyPost());
    await flushPromises();
  });

  it('does not auto-retry failed operations and reuses the original operation on an exact retry', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable', error: 'offline' }));
    drafts().setViewer(7);
    drafts().setDraft(42, 'hello');
    await store().activateViewer(7);
    await flushPromises();
    expect(mocks.createPostReply).not.toHaveBeenCalled();

    const result = await store().startOrRetry(42, ' hello ');
    expect(result).toMatchObject({ status: 'accepted', operation: { id: 'operation-a' } });
    await flushPromises();
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-a', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
  });

  it('blocks edited content and idempotency conflicts without generating a replacement key', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    drafts().setViewer(7);
    drafts().setDraft(42, 'edited');
    await store().activateViewer(7);
    expect(await store().startOrRetry(42, 'edited')).toEqual({ status: 'blocked', reason: 'unresolved_reply' });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();

    const existing = store().getOperation(7, 42)!;
    existing.failureKind = 'idempotency_conflict';
    existing.phase = 'failed';
    drafts().setDraft(42, 'hello');
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
    drafts().setViewer(7);
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
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-a', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('waits for a pending Save before transitioning a failed operation to publishing', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    drafts().setViewer(7);
    await store().activateViewer(7);
    drafts().setDraft(42, 'saved editor');
    const storageModule = await import('../storage/replyStorage');
    const write = deferred<void>();
    vi.mocked(storageModule.saveReplyDraft).mockReturnValueOnce(write.promise);
    const saving = drafts().saveDraft(42);
    const retrying = store().retry('operation-a');
    await flushPromises();

    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(vi.mocked(storageModule.updateReplySubmissionOperation)).not.toHaveBeenCalled();

    write.resolve();
    expect(await saving).toBe('saved');
    expect(await retrying).toBe(true);
    await flushPromises();
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-a', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('does not abandon a failed operation while its parent draft Save is unresolved', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    drafts().setViewer(7);
    await store().activateViewer(7);
    drafts().setDraft(42, 'edited draft');
    const storageModule = await import('../storage/replyStorage');
    const write = deferred<void>();
    vi.mocked(storageModule.saveReplyDraft).mockReturnValueOnce(write.promise);
    const saving = drafts().saveDraft(42);
    const abandoning = store().abandonFailedOperation('operation-a');
    await flushPromises();

    expect(vi.mocked(storageModule.deleteReplySubmissionOperation)).not.toHaveBeenCalled();
    const saveFailure = expect(saving).rejects.toThrow('quota');
    write.reject(new Error('quota'));
    expect(await abandoning).toBe(false);
    await saveFailure;
    expect(mocks.records.has('7:42')).toBe(true);
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

  it('fails closed when the same user starts a new session before a recovered reply can send', async () => {
    mocks.records.set('7:42', record({ viewerSessionID: 'session-7', phase: 'publishing' }));
    mocks.authStore.sessionID = 'session-7-new';
    drafts().setViewer(7);
    drafts().setDraft(42, 'hello');

    await store().activateViewer(7);
    await flushPromises();

    expect(store().getOperation(7, 42)).toMatchObject({
      viewerSessionID: 'session-7', phase: 'failed', failureKind: 'auth_context_changed',
    });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(drafts().getDraft(42)).toBe('hello');
    expect(await store().retry('operation-a')).toBe(false);
  });

  it('keeps a recovered succeeded reply successful after the same user starts a new session', async () => {
    mocks.records.set('7:42', record({
      viewerSessionID: 'session-7',
      phase: 'succeeded',
      post: replyPost(),
      sourceDraftContent: null,
    }));
    mocks.authStore.sessionID = 'session-7-new';
    drafts().setViewer(7);
    drafts().setDraft(42, 'new session reply draft');

    await store().activateViewer(7);
    await flushPromises();

    expect(store().getOperation(7, 42)).toMatchObject({
      viewerSessionID: 'session-7',
      phase: 'succeeded',
      failureKind: null,
    });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(mocks.records.has('7:42')).toBe(false);
    expect(drafts().getDraft(42)).toBe('new session reply draft');
  });

  it('preserves a same-content draft and skips reply draft store mutations across sessions', async () => {
    const draftStore = drafts();
    draftStore.setViewer(7);
    draftStore.setDraft(42, 'same text');
    await draftStore.saveDraft(42);
    const markDeleted = vi.spyOn(draftStore, 'markSourceDraftDeleted');
    const refreshBaseline = vi.spyOn(draftStore, 'refreshSavedBaseline');
    const finishSubmission = vi.spyOn(draftStore, 'finishBoundSubmission');
    const clearBinding = vi.spyOn(draftStore, 'clearSubmissionBinding');
    mocks.drafts.set('7:42', {
      key: '7:42', viewerID: 7, parentPostID: 42,
      content: 'same text', createdAt: 1, updatedAt: 2,
    });
    mocks.records.set('7:42', record({
      viewerSessionID: 'session-7',
      phase: 'succeeded',
      post: replyPost(),
      sourceDraftContent: 'same text',
    }));
    mocks.authStore.sessionID = 'session-7-new';
    const storageModule = await import('../storage/replyStorage');

    await store().activateViewer(7);
    await flushPromises();

    expect(vi.mocked(storageModule.deleteReplyDraftIfUnchanged)).not.toHaveBeenCalled();
    expect(mocks.drafts.get('7:42')?.content).toBe('same text');
    expect(draftStore.getDraft(42)).toBe('same text');
    expect(markDeleted).not.toHaveBeenCalled();
    expect(refreshBaseline).not.toHaveBeenCalled();
    expect(finishSubmission).not.toHaveBeenCalled();
    expect(clearBinding).not.toHaveBeenCalled();
    expect(mocks.records.has('7:42')).toBe(false);
  });

  it('retires a cross-session succeeded reply operation without retrying it', async () => {
    mocks.records.set('7:42', record({
      viewerSessionID: 'session-7',
      phase: 'succeeded',
      post: replyPost(),
      sourceDraftContent: 'S1 saved source',
    }));
    mocks.authStore.sessionID = 'session-7-new';
    const storageModule = await import('../storage/replyStorage');

    await store().activateViewer(7);
    await flushPromises();

    expect(vi.mocked(storageModule.deleteReplyDraftIfUnchanged)).not.toHaveBeenCalled();
    expect(vi.mocked(storageModule.deleteReplySubmissionOperation)).toHaveBeenCalledWith(
      7,
      42,
      'operation-a',
    );
    expect(mocks.records.has('7:42')).toBe(false);
    expect(mocks.createPostReply).not.toHaveBeenCalled();
  });

  it('retains same-session succeeded reply source cleanup behavior', async () => {
    const operation = record({
      content: 'saved source',
      phase: 'succeeded',
      post: replyPost(),
      sourceDraftContent: 'saved source',
    });
    mocks.records.set('7:42', operation);
    const draftStore = drafts();
    draftStore.setViewer(7);
    draftStore.setDraft(42, 'saved source');
    await draftStore.saveDraft(42);
    expect(draftStore.bindSubmission(42, 'operation-a', 'saved source')).toBe(true);
    const finishSubmission = vi.spyOn(draftStore, 'finishBoundSubmission');
    const storageModule = await import('../storage/replyStorage');

    await store().activateViewer(7);
    await flushPromises();

    expect(vi.mocked(storageModule.deleteReplyDraftIfUnchanged)).toHaveBeenCalledWith(
      7,
      42,
      'saved source',
      expect.any(Function),
    );
    expect(mocks.drafts.has('7:42')).toBe(false);
    expect(mocks.records.has('7:42')).toBe(false);
    expect(finishSubmission).toHaveBeenCalledWith(42, 'operation-a', 'saved source');
    expect(draftStore.getDraft(42)).toBe('');
    expect(mocks.createPostReply).not.toHaveBeenCalled();
  });

  it('preserves a same-content next-session draft when the session changes during succeeded cleanup', async () => {
    const draftStore = drafts();
    draftStore.setViewer(7);
    draftStore.setDraft(42, 'same text');
    await draftStore.saveDraft(42);
    const finishSubmission = vi.spyOn(draftStore, 'finishBoundSubmission');
    const markDeleted = vi.spyOn(draftStore, 'markSourceDraftDeleted');
    mocks.records.set('7:42', record({
      phase: 'succeeded',
      post: replyPost(),
      sourceDraftContent: 'same text',
    }));

    const storageModule = await import('../storage/replyStorage');
    const deleteStarted = deferred<void>();
    const continueDelete = deferred<void>();
    const conditionalDelete = vi.mocked(storageModule.deleteReplyDraftIfUnchanged);
    conditionalDelete.mockImplementationOnce(async (
      viewerID,
      parentPostID,
      expectedContent,
      shouldDelete = () => true,
    ) => {
      const key = storageKey(viewerID, parentPostID);
      const observedDraft = mocks.drafts.get(key);
      if (!shouldDelete()) return 'changed';
      if (!observedDraft) return 'missing';
      if (observedDraft.content !== expectedContent) return 'changed';

      deleteStarted.resolve();
      await continueDelete.promise;
      if (!shouldDelete()) return 'changed';
      mocks.drafts.delete(key);
      return 'deleted';
    });

    await store().activateViewer(7);
    await deleteStarted.promise;
    mocks.authStore.sessionID = 'session-7-next';
    mocks.authStore.sessionVersion = 6;
    const nextSessionDraft = {
      ...mocks.drafts.get('7:42'),
      content: 'same text',
      updatedAt: 3,
    };
    mocks.drafts.set('7:42', nextSessionDraft);
    continueDelete.resolve();
    await flushPromises();

    expect(mocks.drafts.get('7:42')).toEqual(nextSessionDraft);
    expect(mocks.records.has('7:42')).toBe(false);
    expect(store().getOperation(7, 42)).toMatchObject({
      phase: 'succeeded',
      failureKind: null,
      durableOwned: false,
      cleanupPending: false,
    });
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(finishSubmission).not.toHaveBeenCalled();
    expect(markDeleted).not.toHaveBeenCalled();
    expect(conditionalDelete).toHaveBeenCalledWith(7, 42, 'same text', expect.any(Function));
  });

  it('keeps a legacy reply draft and permits discard without adopting the current session', async () => {
    const { viewerSessionID: _sessionID, ...legacy } = record({ phase: 'publishing' });
    mocks.records.set('7:42', legacy);
    drafts().setViewer(7);
    drafts().setDraft(42, 'legacy reply draft');

    await store().activateViewer(7);
    await flushPromises();

    expect(store().getOperation(7, 42)).toMatchObject({
      viewerSessionID: null, phase: 'failed', failureKind: 'auth_context_changed',
    });
    expect(mocks.records.get('7:42')?.viewerSessionID).toBeNull();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(drafts().getDraft(42)).toBe('legacy reply draft');
    expect(await store().abandonFailedOperation('operation-a')).toBe(true);
    expect(drafts().getDraft(42)).toBe('legacy reply draft');
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

  it('resolves a succeeded operation on one click and requires a later click for the next reply', async () => {
    drafts().setViewer(7);
    drafts().setDraft(42, 'first');
    await drafts().saveDraft(42);
    mocks.createClientOperationID.mockReturnValueOnce('operation-a').mockReturnValueOnce('operation-b');
    const storageModule = await import('../storage/replyStorage');
    const conditionalDelete = vi.mocked(storageModule.deleteReplyDraftIfUnchanged);
    conditionalDelete.mockRejectedValueOnce(new Error('temporary storage failure'));

    await store().startOrRetry(42, 'first');
    await flushPromises();
    expect(store().getOperation(7, 42)).toMatchObject({ phase: 'succeeded', durableOwned: true, cleanupPending: true });
    expect(mocks.records.get('7:42')?.phase).toBe('succeeded');

    const cleanupOnly = await store().startOrRetry(42, 'first');
    expect(cleanupOnly).toEqual({ status: 'resolved_previous', operationID: 'operation-a' });
    expect(mocks.createClientOperationID).toHaveBeenCalledTimes(1);
    expect(mocks.replaceReplySubmissionOperation).toHaveBeenCalledTimes(1);
    expect(mocks.createPostReply).toHaveBeenCalledTimes(1);
    expect(drafts().getDraft(42)).toBe('');
    expect(drafts().getBoundOperationID(42)).toBeNull();

    drafts().setDraft(42, 'second');
    const next = await store().startOrRetry(42, 'second');
    expect(next).toMatchObject({ status: 'accepted', operation: { id: 'operation-b' } });
    expect(mocks.createClientOperationID).toHaveBeenCalledTimes(2);
    expect(mocks.createPostReply).toHaveBeenNthCalledWith(2, 42, 'second', expect.objectContaining({
      idempotencyKey: 'operation-b', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('preserves newer editor content when a succeeded operation is cleaned up', async () => {
    drafts().setViewer(7);
    drafts().setDraft(42, 'first');
    await drafts().saveDraft(42);
    mocks.createClientOperationID.mockReturnValueOnce('operation-a').mockReturnValueOnce('operation-b');
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.deleteReplyDraftIfUnchanged).mockRejectedValueOnce(new Error('temporary storage failure'));

    await store().startOrRetry(42, 'first');
    await flushPromises();
    expect(store().getOperation(7, 42)).toMatchObject({ phase: 'succeeded', durableOwned: true });

    drafts().setDraft(42, 'new reply');
    expect(drafts().getBoundOperationID(42)).toBeNull();
    const cleanupOnly = await store().startOrRetry(42, 'new reply');

    expect(cleanupOnly).toEqual({ status: 'resolved_previous', operationID: 'operation-a' });
    expect(drafts().getDraft(42)).toBe('new reply');
    expect(mocks.createClientOperationID).toHaveBeenCalledTimes(1);
    expect(mocks.createPostReply).toHaveBeenCalledTimes(1);

    const next = await store().startOrRetry(42, 'new reply');
    expect(next).toMatchObject({ status: 'accepted', operation: { id: 'operation-b' } });
    expect(mocks.createPostReply).toHaveBeenNthCalledWith(2, 42, 'new reply', expect.objectContaining({
      idempotencyKey: 'operation-b', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('does not adopt a recovered succeeded operation into the composer', async () => {
    mocks.records.set('7:42', record({
      phase: 'succeeded', post: replyPost(), sourceDraftContent: null,
    }));
    drafts().setViewer(7);
    await store().activateViewer(7);
    await flushPromises();
    await drafts().hydrateSavedDraft(42);
    const workingRevision = drafts().captureWorkingRevision(42)!;

    expect(store().adoptHydratedOperation(7, 42, workingRevision)).toBe(false);
    expect(drafts().getDraft(42)).toBe('');
    expect(drafts().getBoundOperationID(42)).toBeNull();
  });

  it('waits for a successful Save before creating an operation and capturing its source draft', async () => {
    const write = deferred<void>();
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.saveReplyDraft).mockImplementationOnce(async draft => {
      await write.promise;
      mocks.drafts.set(draft.key, draft);
    });
    drafts().setViewer(7);
    drafts().setDraft(42, 'hello');
    const saving = drafts().saveDraft(42);
    const starting = store().startOrRetry(42, 'hello');
    await flushPromises();

    expect(drafts().isSavePending(42)).toBe(true);
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();

    write.resolve();
    expect(await saving).toBe('saved');
    expect(await starting).toMatchObject({
      status: 'accepted', operation: { content: 'hello', sourceDraftContent: 'hello' },
    });
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'hello', expect.objectContaining({
      idempotencyKey: 'operation-new', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('waits for the draft Save before resolving succeeded-operation cleanup and never creates B', async () => {
    const write = deferred<void>();
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.saveReplyDraft).mockImplementationOnce(async draft => {
      await write.promise;
      mocks.drafts.set(draft.key, draft);
    });
    mocks.records.set('7:42', record({
      phase: 'succeeded', post: replyPost(), sourceDraftContent: 'older saved content',
    }));
    mocks.drafts.set('7:42', {
      key: '7:42', viewerID: 7, parentPostID: 42,
      content: 'older saved content', createdAt: 1, updatedAt: 2,
    });
    drafts().setViewer(7);
    drafts().setDraft(42, 'new saved content');
    const saving = drafts().saveDraft(42);
    const starting = store().startOrRetry(42, 'new saved content');
    await flushPromises();

    expect(vi.mocked(storageModule.deleteReplyDraftIfUnchanged)).not.toHaveBeenCalled();
    expect(vi.mocked(storageModule.deleteReplySubmissionOperation)).not.toHaveBeenCalled();
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();

    write.resolve();
    expect(await saving).toBe('saved');
    expect(await starting).toEqual({ status: 'resolved_previous', operationID: 'operation-a' });
    expect(mocks.drafts.get('7:42')?.content).toBe('new saved content');
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
  });

  it('blocks submission without allocating an operation when the pending Save fails', async () => {
    const write = deferred<void>();
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.saveReplyDraft).mockReturnValueOnce(write.promise);
    drafts().setViewer(7);
    drafts().setDraft(42, 'preserved reply');
    const saving = drafts().saveDraft(42);
    const saveFailure = expect(saving).rejects.toThrow('quota');
    const starting = store().startOrRetry(42, 'preserved reply');
    await flushPromises();

    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();

    write.reject(new Error('quota'));
    expect(await starting).toEqual({ status: 'rejected', reason: 'persistence_unavailable' });
    await saveFailure;
    expect(drafts().getDraft(42)).toBe('preserved reply');
  });

  it('re-reads the editor after Save and rejects stale submission content', async () => {
    const write = deferred<void>();
    const storageModule = await import('../storage/replyStorage');
    vi.mocked(storageModule.saveReplyDraft).mockReturnValueOnce(write.promise);
    drafts().setViewer(7);
    drafts().setDraft(42, 'hello');
    const saving = drafts().saveDraft(42);
    const starting = store().startOrRetry(42, 'hello');
    await flushPromises();
    drafts().setDraft(42, 'newer editor content');

    write.resolve();
    await saving;
    expect(await starting).toEqual({ status: 'rejected', reason: 'editor_changed' });
    expect(mocks.createClientOperationID).not.toHaveBeenCalled();
    expect(mocks.replaceReplySubmissionOperation).not.toHaveBeenCalled();
    expect(mocks.createPostReply).not.toHaveBeenCalled();
    expect(drafts().getDraft(42)).toBe('newer editor content');
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
    expect(mocks.createPostReply).toHaveBeenCalledWith(42, 'first', expect.objectContaining({
      idempotencyKey: 'operation-a', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
    expect(mocks.createPostReply).toHaveBeenCalledWith(43, 'second', expect.objectContaining({
      idempotencyKey: 'operation-b', authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7' }),
    }));
  });

  it('keeps viewer operations private when account identity changes', async () => {
    mocks.records.set('7:42', record({ phase: 'failed', failureKind: 'retryable' }));
    await store().activateViewer(7);
    mocks.authStore.currentIdentity.id = 8;
    await store().activateViewer(8);
    drafts().setDraft(42, 'other user');

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
