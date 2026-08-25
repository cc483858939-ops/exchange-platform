// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useArticleDraftStore } from './articleDraft';

describe('articleDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('starts empty and clean', () => {
    const store = useArticleDraftStore();

    expect(store.viewerID).toBeNull();
    expect(store.title).toBe('');
    expect(store.preview).toBe('');
    expect(store.content).toBe('');
    expect(store.coverFile).toBeNull();
    expect(store.uploadedCoverURL).toBe('');
    expect(store.dirty).toBe(false);
  });

  it('preserves a draft for the same viewer, including token-refresh-like repeats', () => {
    const store = useArticleDraftStore();
    store.setViewer(7);
    store.setTitle('Headline');
    store.setContent('Body');

    expect(store.setViewer(7)).toBe(false);
    expect(store.title).toBe('Headline');
    expect(store.content).toBe('Body');
  });

  it('clears all content when the viewer changes or logs out', () => {
    const store = useArticleDraftStore();
    const file = new File(['cover'], 'cover.png', { type: 'image/png' });
    store.setViewer(7);
    store.setTitle('Headline');
    store.setPreview('Summary');
    store.setContent('Body');
    store.setCoverFile(file);
    store.setUploadedCoverURL('https://example.test/cover.jpg');

    store.setViewer(8);
    expect(store.viewerID).toBe(8);
    expect(store.title).toBe('');
    expect(store.preview).toBe('');
    expect(store.content).toBe('');
    expect(store.coverFile).toBeNull();
    expect(store.uploadedCoverURL).toBe('');
    expect(store.dirty).toBe(false);

    store.setContent('New viewer draft');
    store.setViewer(null);
    expect(store.viewerID).toBeNull();
    expect(store.content).toBe('');
    expect(store.dirty).toBe(false);
  });

  it('marks text and cover mutations dirty and replacing a cover clears its upload URL', () => {
    const store = useArticleDraftStore();
    const first = new File(['first'], 'first.png', { type: 'image/png' });
    const second = new File(['second'], 'second.png', { type: 'image/png' });

    store.setTitle('Headline');
    store.setPreview('Summary');
    store.setContent('Body');
    expect(store.dirty).toBe(true);

    store.setCoverFile(first);
    store.setUploadedCoverURL('https://example.test/first.jpg');
    store.setCoverFile(second);
    expect(store.coverFile).toBe(second);
    expect(store.uploadedCoverURL).toBe('');
    expect(store.dirty).toBe(true);

    store.removeCover();
    expect(store.coverFile).toBeNull();
    expect(store.uploadedCoverURL).toBe('');
  });

  it('clears the draft explicitly after a successful publish', () => {
    const store = useArticleDraftStore();
    store.setViewer(7);
    store.setContent('Body');
    store.setUploadedCoverURL('https://example.test/cover.jpg');

    store.clear();

    expect(store.viewerID).toBe(7);
    expect(store.content).toBe('');
    expect(store.uploadedCoverURL).toBe('');
    expect(store.dirty).toBe(false);
  });
});
