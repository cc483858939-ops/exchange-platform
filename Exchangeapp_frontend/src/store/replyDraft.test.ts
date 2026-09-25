// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useReplyDraftStore } from './replyDraft';

const mocks = vi.hoisted(() => ({
  createClientOperationID: vi.fn(),
}));

vi.mock('../utils/clientOperationId', () => ({
  createClientOperationID: mocks.createClientOperationID,
}));

let operationSequence = 0;

const operationUUID = (value: number) => (
  `00000000-0000-4000-8000-${String(value).padStart(12, '0')}`
);

describe('replyDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    operationSequence = 0;
    mocks.createClientOperationID.mockReset().mockImplementation(() => operationUUID(++operationSequence));
  });

  it('starts without a viewer or drafts', () => {
    const store = useReplyDraftStore();

    expect(store.viewerID).toBeNull();
    expect(store.drafts).toEqual({});
    expect(store.submissionOperations).toEqual({});
    expect(store.getDraft(42)).toBe('');
  });

  it('preserves drafts when the same viewer is bound again', () => {
    const store = useReplyDraftStore();

    expect(store.setViewer(7)).toBe(true);
    store.setDraft(42, 'hello');
    const operation = store.prepareSubmission(42, 'hello');

    expect(store.setViewer(7)).toBe(false);
    expect(store.getDraft(42)).toBe('hello');
    expect(store.prepareSubmission(42, 'hello')).toEqual(operation);
  });

  it('keeps drafts isolated per post', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);

    store.setDraft(42, 'draft A');
    store.setDraft('43', 'draft B');

    expect(store.getDraft(42)).toBe('draft A');
    expect(store.getDraft('43')).toBe('draft B');
  });

  it('clears drafts when the viewer changes or logs out', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'private draft');
    store.prepareSubmission(42, 'private draft');

    expect(store.setViewer(8)).toBe(true);
    expect(store.viewerID).toBe(8);
    expect(store.getDraft(42)).toBe('');
    expect(store.submissionOperations).toEqual({});

    store.setDraft(43, 'viewer 8 draft');
    expect(store.setViewer(null)).toBe(true);
    expect(store.viewerID).toBeNull();
    expect(store.drafts).toEqual({});
    expect(store.submissionOperations).toEqual({});
  });

  it('preserves raw whitespace and newlines', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    const raw = '  hello\nworld  ';

    store.setDraft(42, raw);

    expect(store.getDraft(42)).toBe(raw);
  });

  it('reuses the operation for the same canonical content and whitespace-only edits', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'hello');

    const first = store.prepareSubmission(42, 'hello');
    store.setDraft(42, '  hello  ');
    const retry = store.prepareSubmission('42', ' hello ');

    expect(retry).toEqual(first);
    expect(retry.content).toBe('hello');
    expect(mocks.createClientOperationID).toHaveBeenCalledTimes(1);
  });

  it('creates a new operation when canonical content changes and keeps operations per post', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'hello');
    const first = store.prepareSubmission(42, 'hello');

    store.setDraft(42, 'hello again');
    expect(store.submissionOperations['42']).toBeUndefined();
    const edited = store.prepareSubmission(42, 'hello again');
    const otherPost = store.prepareSubmission(43, 'hello again');

    expect(edited.id).not.toBe(first.id);
    expect(edited.content).toBe('hello again');
    expect(otherPost.id).not.toBe(edited.id);
    expect(store.submissionOperations).toEqual({ '42': edited, '43': otherPost });
  });

  it('clears submission operations with drafts, clearAll, and viewer changes', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'draft A');
    store.prepareSubmission(42, 'draft A');
    store.setDraft(43, 'draft B');
    store.prepareSubmission(43, 'draft B');

    store.clearDraft(42);
    expect(store.submissionOperations['42']).toBeUndefined();
    expect(store.submissionOperations['43']).toBeDefined();

    store.clearAll();
    expect(store.drafts).toEqual({});
    expect(store.submissionOperations).toEqual({});

    store.setDraft(42, 'viewer 7');
    store.prepareSubmission(42, 'viewer 7');
    store.setViewer(8);
    expect(store.submissionOperations).toEqual({});
  });

  it('clears a submission only when the requested operation ID still matches', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    const first = store.prepareSubmission(42, 'draft A');
    store.clearSubmissionOperation(42, 'stale-operation');
    expect(store.submissionOperations['42']).toEqual(first);

    store.clearSubmissionOperation(42, first.id);
    expect(store.submissionOperations).toEqual({});
  });

  it('removes a post draft when its content becomes empty', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'draft A');
    store.setDraft(43, 'draft B');

    store.setDraft(42, '');

    expect(store.getDraft(42)).toBe('');
    expect(store.getDraft(43)).toBe('draft B');
    expect(store.drafts).toEqual({ '43': 'draft B' });
  });

  it('ignores writes without a valid viewer or post ID', () => {
    const store = useReplyDraftStore();
    const invalidPostIDs: Array<number | string> = [0, -1, NaN, Infinity, 1.5, 'abc', '42.5'];

    invalidPostIDs.forEach(postID => store.setDraft(postID, 'should not persist'));
    expect(store.drafts).toEqual({});

    store.setViewer(7);
    invalidPostIDs.forEach(postID => store.setDraft(postID, 'should not persist'));

    expect(store.drafts).toEqual({});
    invalidPostIDs.forEach(postID => expect(store.getDraft(postID)).toBe(''));
  });

  it('clears all post drafts without changing the viewer', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'draft A');
    store.setDraft(43, 'draft B');

    store.clearAll();

    expect(store.viewerID).toBe(7);
    expect(store.drafts).toEqual({});
  });
});
