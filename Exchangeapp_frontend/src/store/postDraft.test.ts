import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { usePostDraftStore } from './postDraft';

const repositoryMocks = vi.hoisted(() => ({
  deletePostDraft: vi.fn(),
  getPostDraft: vi.fn(),
  listPostDrafts: vi.fn(),
  savePostDraft: vi.fn(),
}));

vi.mock('../storage/postDraftRepository', () => ({
  deletePostDraft: repositoryMocks.deletePostDraft,
  getPostDraft: repositoryMocks.getPostDraft,
  listPostDrafts: repositoryMocks.listPostDrafts,
  savePostDraft: repositoryMocks.savePostDraft,
}));

const file = (name = 'one.png') => new File(['image'], name, { type: 'image/png' });

describe('postDraft store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    repositoryMocks.deletePostDraft.mockReset().mockResolvedValue(true);
    repositoryMocks.getPostDraft.mockReset().mockResolvedValue(null);
    repositoryMocks.listPostDrafts.mockReset().mockResolvedValue([]);
    repositoryMocks.savePostDraft.mockReset().mockResolvedValue(undefined);
  });

  it('derives new-composer cleanliness from whether it has content', () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    expect(store.hasUnsavedChanges).toBe(false);
    expect(store.dirty).toBe(false);

    store.setContent('A post');
    expect(store.hasUnsavedChanges).toBe(true);
    expect(store.dirty).toBe(true);
    store.setContent('');
    expect(store.hasUnsavedChanges).toBe(false);
    expect(repositoryMocks.savePostDraft).not.toHaveBeenCalled();
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

  it('saves one durable snapshot and tracks edits without treating uploads as edits', async () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Saved post');
    const mediaID = store.addMedia(file());

    const draftID = await store.saveCurrentDraft();
    expect(repositoryMocks.savePostDraft).toHaveBeenCalledTimes(1);
    expect(repositoryMocks.savePostDraft).toHaveBeenCalledWith(expect.objectContaining({
      id: draftID,
      viewerID: 7,
      content: 'Saved post',
      media: [expect.objectContaining({ id: mediaID, name: 'one.png', type: 'image/png' })],
    }));
    expect(store.draftID).toBe(draftID);
    expect(store.isSavedDraft).toBe(true);
    expect(store.hasUnsavedChanges).toBe(false);

    store.setUploadedURL(mediaID, '/temporary/one.png');
    store.setContent('Saved post');
    expect(store.hasUnsavedChanges).toBe(false);

    store.setContent('Edited post');
    expect(store.hasUnsavedChanges).toBe(true);
    await store.saveCurrentDraft();
    expect(repositoryMocks.savePostDraft).toHaveBeenCalledTimes(2);
    expect(repositoryMocks.savePostDraft.mock.calls[1]?.[0].id).toBe(draftID);
    expect(store.hasUnsavedChanges).toBe(false);

    store.removeMedia(mediaID);
    expect(store.hasUnsavedChanges).toBe(true);
  });

  it('compares media order and metadata rather than File object identity', async () => {
    const store = usePostDraftStore();
    store.setViewer(7);
    const firstFile = new File(['same'], 'same.png', { type: 'image/png', lastModified: 10 });
    const secondFile = new File(['next'], 'next.png', { type: 'image/png', lastModified: 20 });
    store.addMedia(firstFile);
    store.addMedia(secondFile);
    await store.saveCurrentDraft();

    store.media[0]!.file = new File(['same'], 'same.png', { type: 'image/png', lastModified: 10 });
    expect(store.hasUnsavedChanges).toBe(false);
    store.media.reverse();
    expect(store.hasUnsavedChanges).toBe(true);
  });

  it('restores saved File metadata and remains clean', async () => {
    const mediaBlob = new Blob(['restored-image'], { type: 'image/webp' });
    repositoryMocks.getPostDraft.mockResolvedValue({
      id: 'saved-id',
      viewerID: 7,
      content: 'Restored post',
      media: [{
        id: 'restored-media-id',
        blob: mediaBlob,
        name: 'restored.webp',
        type: 'image/webp',
        size: mediaBlob.size,
        lastModified: 1234,
        uploadedURL: '/uploaded/restored.webp',
      }],
      createdAt: 100,
      updatedAt: 200,
    });
    const store = usePostDraftStore();
    store.setViewer(7);

    expect(await store.loadSavedDraft('saved-id')).toBe(true);
    expect(store.content).toBe('Restored post');
    expect(store.draftID).toBe('saved-id');
    expect(store.media[0]?.id).toBe('restored-media-id');
    expect(store.media[0]?.file).toBeInstanceOf(File);
    expect(store.media[0]?.file).toMatchObject({
      name: 'restored.webp',
      type: 'image/webp',
      size: mediaBlob.size,
      lastModified: 1234,
    });
    expect(store.media[0]?.uploadedURL).toBe('/uploaded/restored.webp');
    expect(store.hasUnsavedChanges).toBe(false);
  });

  it('preserves working content and dirty state after a failed save', async () => {
    repositoryMocks.savePostDraft.mockRejectedValueOnce(new Error('quota exceeded'));
    const store = usePostDraftStore();
    store.setViewer(7);
    store.setContent('Keep this in memory');

    await expect(store.saveCurrentDraft()).rejects.toThrow('quota exceeded');
    expect(store.content).toBe('Keep this in memory');
    expect(store.draftID).toBeNull();
    expect(store.hasUnsavedChanges).toBe(true);
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
    expect(repositoryMocks.deletePostDraft).not.toHaveBeenCalled();
  });
});
