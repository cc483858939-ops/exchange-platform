// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useReplyDraftStore } from './replyDraft';

const mocks = vi.hoisted(() => ({
  getReplyDraft: vi.fn(),
  saveReplyDraft: vi.fn(),
  deleteReplyDraftIfUnchanged: vi.fn(),
}));

vi.mock('../storage/replyStorage', () => ({
  getReplyDraft: mocks.getReplyDraft,
  saveReplyDraft: mocks.saveReplyDraft,
  deleteReplyDraftIfUnchanged: mocks.deleteReplyDraftIfUnchanged,
}));

const saved = (viewerID = 7, parentPostID = 42, content = 'saved reply') => ({
  key: `${viewerID}:${parentPostID}`,
  viewerID,
  parentPostID,
  content,
  createdAt: 1,
  updatedAt: 2,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
};

describe('replyDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.getReplyDraft.mockReset().mockResolvedValue(null);
    mocks.saveReplyDraft.mockReset().mockResolvedValue(undefined);
    mocks.deleteReplyDraftIfUnchanged.mockReset().mockResolvedValue('deleted');
  });

  it('keeps ordinary typing in memory until the user explicitly saves', async () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, '  hello\nworld  ');

    expect(store.getDraft(42)).toBe('  hello\nworld  ');
    expect(store.hasUnsavedChanges(42)).toBe(true);
    expect(mocks.saveReplyDraft).not.toHaveBeenCalled();

    expect(await store.saveDraft(42)).toBe('saved');
    expect(mocks.saveReplyDraft).toHaveBeenCalledWith(expect.objectContaining({
      viewerID: 7,
      parentPostID: 42,
      content: '  hello\nworld  ',
    }));
    expect(store.hasUnsavedChanges(42)).toBe(false);

    store.setDraft(42, 'edited');
    expect(store.hasUnsavedChanges(42)).toBe(true);
  });

  it('restores viewer and parent scoped drafts without losing raw content', async () => {
    mocks.getReplyDraft.mockImplementation(async (viewerID: number, postID: number) => (
      viewerID === 7 && postID === 42 ? saved(7, 42, '  first\nsecond  ') : null
    ));
    const store = useReplyDraftStore();
    store.setViewer(7);

    expect(await store.hydrateSavedDraft(42)).toBe('loaded');
    expect(await store.hydrateSavedDraft(43)).toBe('empty');
    expect(store.getDraft(42)).toBe('  first\nsecond  ');
    expect(store.getDraft(43)).toBe('');
    expect(store.hasUnsavedChanges(42)).toBe(false);
  });

  it('does not overwrite text entered while draft hydration is pending', async () => {
    const request = deferred<ReturnType<typeof saved> | null>();
    mocks.getReplyDraft.mockReturnValueOnce(request.promise);
    const store = useReplyDraftStore();
    store.setViewer(7);
    const loading = store.hydrateSavedDraft(42);

    store.setDraft(42, 'newer user text');
    request.resolve(saved());

    expect(await loading).toBe('stale');
    expect(store.getDraft(42)).toBe('newer user text');
    expect(store.hasUnsavedChanges(42)).toBe(true);
  });

  it('treats canonical whitespace edits as durably bound but raw saved edits as dirty', async () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'hello');
    expect(store.bindSubmission(42, 'operation-a', 'hello')).toBe(true);
    expect(store.hasUnsavedChanges(42)).toBe(false);

    store.setDraft(42, '  hello  ');
    expect(store.getBoundOperationID(42)).toBe('operation-a');
    expect(store.hasUnsavedChanges(42)).toBe(false);

    store.setDraft(42, 'different');
    expect(store.getBoundOperationID(42)).toBeNull();
    expect(store.hasUnsavedChanges(42)).toBe(true);
  });

  it('requires an explicit empty save to remove a saved reply draft', async () => {
    mocks.getReplyDraft.mockResolvedValue(saved(7, 42, 'saved reply'));
    const store = useReplyDraftStore();
    store.setViewer(7);
    await store.hydrateSavedDraft(42);
    store.setDraft(42, '');

    expect(store.hasUnsavedChanges(42)).toBe(true);
    expect(mocks.deleteReplyDraftIfUnchanged).not.toHaveBeenCalled();
    expect(await store.saveDraft(42)).toBe('deleted');
    expect(mocks.deleteReplyDraftIfUnchanged).toHaveBeenCalledWith(7, 42, 'saved reply');
    expect(store.hasSavedDraft(42)).toBe(false);
    expect(store.hasUnsavedChanges(42)).toBe(false);
  });

  it('keeps viewer and parent state isolated and clears only memory on account changes', async () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    store.setDraft(42, 'A');
    store.setDraft(43, 'B');
    expect(store.getDraft(42)).toBe('A');
    expect(store.getDraft(43)).toBe('B');

    store.setViewer(8);
    expect(store.getDraft(42)).toBe('');
    expect(mocks.deleteReplyDraftIfUnchanged).not.toHaveBeenCalled();
    store.setViewer(null);
    expect(store.viewerID).toBeNull();
  });

  it('discards editor changes back to a saved baseline without deleting it', async () => {
    mocks.getReplyDraft.mockResolvedValue(saved(7, 42, 'saved version'));
    const store = useReplyDraftStore();
    store.setViewer(7);
    await store.hydrateSavedDraft(42);
    store.setDraft(42, 'unsaved edit');

    expect(store.discardChanges(42)).toBe(true);
    expect(store.getDraft(42)).toBe('saved version');
    expect(store.hasUnsavedChanges(42)).toBe(false);
    expect(mocks.deleteReplyDraftIfUnchanged).not.toHaveBeenCalled();
  });
});
