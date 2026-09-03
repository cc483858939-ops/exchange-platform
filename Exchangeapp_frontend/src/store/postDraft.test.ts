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
  });
});
