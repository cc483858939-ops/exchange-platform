// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useReplyDraftStore } from './replyDraft';

describe('replyDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('starts without a viewer or drafts', () => {
    const store = useReplyDraftStore();

    expect(store.viewerID).toBeNull();
    expect(store.drafts).toEqual({});
    expect(store.getDraft(42)).toBe('');
  });

  it('preserves drafts when the same viewer is bound again', () => {
    const store = useReplyDraftStore();

    expect(store.setViewer(7)).toBe(true);
    store.setDraft(42, 'hello');

    expect(store.setViewer(7)).toBe(false);
    expect(store.getDraft(42)).toBe('hello');
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

    expect(store.setViewer(8)).toBe(true);
    expect(store.viewerID).toBe(8);
    expect(store.getDraft(42)).toBe('');

    store.setDraft(43, 'viewer 8 draft');
    expect(store.setViewer(null)).toBe(true);
    expect(store.viewerID).toBeNull();
    expect(store.drafts).toEqual({});
  });

  it('preserves raw whitespace and newlines', () => {
    const store = useReplyDraftStore();
    store.setViewer(7);
    const raw = '  hello\nworld  ';

    store.setDraft(42, raw);

    expect(store.getDraft(42)).toBe(raw);
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
