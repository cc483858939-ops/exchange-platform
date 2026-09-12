import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import { usePostDraftStore } from './postDraft';

const file = (name = 'one.png') => new File(['image'], name, { type: 'image/png' });

describe('postDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('stores canonical post content and ordered media', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Hello');
    const firstID = store.addMedia(file());
    const secondID = store.addMedia(file('two.webp'));

    expect(store.content).toBe('Hello');
    expect(store.media.map(item => item.id)).toEqual([firstID, secondID]);
    expect(store.media.map(item => item.file.name)).toEqual(['one.png', 'two.webp']);
    expect(store.dirty).toBe(true);
  });

  it('updates uploaded URLs by stable media identity', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    const firstID = store.addMedia(file());
    const secondID = store.addMedia(file('two.png'));

    expect(store.setUploadedURL(secondID, '/api/files/post-media/7/two.png')).toBe(true);
    expect(store.media.find(item => item.id === secondID)?.uploadedURL)
      .toBe('/api/files/post-media/7/two.png');
    expect(store.media.find(item => item.id === firstID)?.uploadedURL).toBe('');
  });

  it('binds and clears a publish operation only for its viewer', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Draft');
    expect(store.bindPublishOperation('publish-1')).toBe(true);
    store.setUploadedURL('missing', '/media/missing.png');
    expect(store.publishOperationID).toBe('publish-1');
    expect(store.clearIfBoundTo('publish-1', 8)).toBe(false);
    expect(store.publishOperationID).toBe('publish-1');
    expect(store.clearIfBoundTo('other', 7)).toBe(false);
    expect(store.clearIfBoundTo('publish-1', 7)).toBe(true);
    expect(store.publishOperationID).toBeNull();
    expect(store.content).toBe('');
  });

  it('clears a publish binding on real draft edits but not uploaded URL hydration', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Draft');
    const mediaID = store.addMedia(file());
    store.bindPublishOperation('publish-2');

    store.setUploadedURL(mediaID, '/media/one.png');
    expect(store.publishOperationID).toBe('publish-2');
    store.setContent('Draft');
    expect(store.publishOperationID).toBe('publish-2');
    store.setContent('Edited draft');
    expect(store.publishOperationID).toBeNull();

    store.bindPublishOperation('publish-3');
    store.removeMedia(mediaID);
    expect(store.publishOperationID).toBeNull();
  });

  it('removes one media item without using its array index as identity', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    const firstID = store.addMedia(file());
    const secondID = store.addMedia(file('two.png'));

    expect(store.removeMedia(firstID)).toBe(true);
    expect(store.media).toHaveLength(1);
    expect(store.media[0]?.id).toBe(secondID);
  });

  it('clears all account-bound content and media when the viewer changes', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Account A draft');
    store.addMedia(file());

    expect(store.setViewer(8)).toBe(true);
    expect(store.viewerID).toBe(8);
    expect(store.content).toBe('');
    expect(store.media).toEqual([]);
    expect(store.dirty).toBe(false);
    expect(store.publishOperationID).toBeNull();
  });
});
