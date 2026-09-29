// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Post } from '../types/Post';
import { usePostDraftStore } from './postDraft';
import { usePostPublishStore } from './postPublish';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: {
    registerPublishedPost: vi.fn(),
    applyBookmarkStateUpdate: vi.fn(),
  },
  profileSessionStore: {
    registerPublishedTimelinePost: vi.fn(),
  },
  createPost: vi.fn(),
  uploadPostMedia: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  randomUUID: vi.fn(),
  captureBookmarkStateSyncVersion: vi.fn(),
  syncHydratedPostBookmarkState: vi.fn(),
  deletePostDraft: vi.fn(),
  getPostDraft: vi.fn(),
  listPostDrafts: vi.fn(),
  savePostDraft: vi.fn(),
  getPostPublishOperation: vi.fn(),
  replacePostPublishOperation: vi.fn(),
  updatePostPublishOperation: vi.fn(),
  deletePostPublishOperation: vi.fn(),
  serializePublishOperation: vi.fn(),
  restorePublishOperation: vi.fn(),
  publishRecords: new Map<number, any>(),
}));

vi.mock('../services/postService', () => ({
  createPost: mocks.createPost,
  uploadPostMedia: mocks.uploadPostMedia,
}));

vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
}));

vi.mock('./auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('./feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('./profileSession', () => ({
  useProfileSessionStore: () => mocks.profileSessionStore,
}));

vi.mock('./sessionSync', () => ({
  captureBookmarkStateSyncVersion: mocks.captureBookmarkStateSyncVersion,
  syncHydratedPostBookmarkState: mocks.syncHydratedPostBookmarkState,
}));

vi.mock('../storage/postDraftRepository', () => ({
  deletePostDraft: mocks.deletePostDraft,
  getPostDraft: mocks.getPostDraft,
  listPostDrafts: mocks.listPostDrafts,
  savePostDraft: mocks.savePostDraft,
}));

vi.mock('../storage/postPublishRepository', () => ({
  getPostPublishOperation: mocks.getPostPublishOperation,
  replacePostPublishOperation: mocks.replacePostPublishOperation,
  updatePostPublishOperation: mocks.updatePostPublishOperation,
  deletePostPublishOperation: mocks.deletePostPublishOperation,
  serializePublishOperation: mocks.serializePublishOperation,
  restorePublishOperation: mocks.restorePublishOperation,
}));

const operationUUID = (value: number) => (
  `00000000-0000-4000-8000-${String(value).padStart(12, '0')}`
);

const publishedPost = (authorID = 7) => ({
  id: 101,
  created_at: '2026-09-12T00:00:00Z',
  updated_at: '2026-09-12T00:00:00Z',
  published_at: '2026-09-12T00:00:00Z',
  author: {
    id: authorID,
    username: authorID === 7 ? 'alice' : 'bob',
    display_name: authorID === 7 ? 'Alice' : 'Bob',
    avatar_url: '',
  },
  content: 'published',
  language: 'en',
  conversation_id: 101,
  reply_to_post_id: null,
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
} as Post);

const file = (name: string) => new File(['image'], name, { type: 'image/png' });

const deferred = <T,>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

describe('postPublish store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    let uuidCounter = 0;
    mocks.randomUUID.mockImplementation(() => operationUUID(++uuidCounter));
    vi.stubGlobal('crypto', { randomUUID: mocks.randomUUID });
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' },
      syncCurrentIdentityProfile: vi.fn(),
    });
    mocks.createPost.mockResolvedValue(publishedPost());
    mocks.uploadPostMedia.mockImplementation(async (item: File) => `/media/${item.name}`);
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.captureBookmarkStateSyncVersion.mockReturnValue(0);
    mocks.syncHydratedPostBookmarkState.mockReturnValue(true);
    mocks.deletePostDraft.mockResolvedValue(true);
    mocks.getPostDraft.mockResolvedValue(null);
    mocks.listPostDrafts.mockResolvedValue([]);
    mocks.savePostDraft.mockResolvedValue(undefined);
    mocks.publishRecords.clear();
    mocks.getPostPublishOperation.mockImplementation(async (viewerID: number) => (
      mocks.publishRecords.get(viewerID) || null
    ));
    mocks.replacePostPublishOperation.mockImplementation(async (record: any) => {
      mocks.publishRecords.set(record.publisherUserID, record);
    });
    mocks.updatePostPublishOperation.mockImplementation(async (record: any) => {
      const current = mocks.publishRecords.get(record.publisherUserID);
      if (!current || current.id !== record.id) return false;
      mocks.publishRecords.set(record.publisherUserID, record);
      return true;
    });
    mocks.deletePostPublishOperation.mockImplementation(async (viewerID: number, operationID: string) => {
      const current = mocks.publishRecords.get(viewerID);
      if (!current || current.id !== operationID) return false;
      mocks.publishRecords.delete(viewerID);
      return true;
    });
    mocks.serializePublishOperation.mockImplementation((operation: any) => ({
      ...operation,
      media: operation.media.map((item: any) => ({
        draftMediaID: item.draftMediaID,
        blob: item.file.slice(0, item.file.size, item.file.type),
        name: item.file.name,
        type: item.file.type,
        size: item.file.size,
        lastModified: item.file.lastModified,
        uploadedURL: item.uploadedURL,
      })),
      updatedAt: Date.now(),
    }));
    mocks.restorePublishOperation.mockImplementation((record: any) => ({
      ...record,
      media: record.media.map((item: any) => ({
        draftMediaID: item.draftMediaID,
        file: new File([item.blob], item.name, { type: item.type, lastModified: item.lastModified }),
        uploadedURL: item.uploadedURL,
      })),
    }));
    const draft = usePostDraftStore();
    draft.clear();
    draft.setViewer(7);
  });

  it('starts immediately, sends one key, and reconciles the authoritative response', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('A background post');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;
    expect(operation.phase).toBe('publishing');
    expect(draft.publishOperationID).toBe(operation.id);

    request.resolve(publishedPost());

    await flushPromises();
    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'A background post', media: [] },
      { idempotencyKey: operation.id },
    );
    expect(operation?.phase).toBe('succeeded');
    expect(draft.publishOperationID).toBeNull();
    expect(mocks.feedStore.registerPublishedPost).toHaveBeenCalledWith(publishedPost(), 7);
    expect(mocks.profileSessionStore.registerPublishedTimelinePost)
      .toHaveBeenCalledWith(publishedPost(), 7);
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledWith(publishedPost().author);
    expect(mocks.getPostBookmarkStates).toHaveBeenCalledWith([101]);
    expect(mocks.syncHydratedPostBookmarkState).toHaveBeenCalledWith({
      postId: 101,
      bookmarked: false,
      status: 'unavailable',
    }, 0);
    expect(mocks.feedStore.applyBookmarkStateUpdate).toHaveBeenCalledWith({
      postId: 101,
      bookmarked: false,
      status: 'unavailable',
    });
  });

  it('deletes the source durable draft only after publishing it succeeds', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Saved source draft');
    const sourceDraftID = await draft.saveCurrentDraft();
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    expect(result.operation.sourceDraftID).toBe(sourceDraftID);
    await flushPromises();

    expect(mocks.deletePostDraft).toHaveBeenCalledWith(7, sourceDraftID);
    expect(result.operation.phase).toBe('succeeded');
    expect(draft.content).toBe('');
  });

  it('preserves the source durable draft when publishing fails', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('offline'));
    const draft = usePostDraftStore();
    draft.setContent('Keep saved on failure');
    const sourceDraftID = await draft.saveCurrentDraft();
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    await flushPromises();

    expect(result.operation.sourceDraftID).toBe(sourceDraftID);
    expect(result.operation.phase).toBe('failed');
    expect(mocks.deletePostDraft).not.toHaveBeenCalled();
    expect(draft.draftID).toBe(sourceDraftID);
    expect(draft.content).toBe('Keep saved on failure');
  });

  it('hydrates a published post bookmark state without changing the global post default', async () => {
    mocks.getPostBookmarkStates.mockResolvedValueOnce({
      items: [{ post_id: 101, bookmarked: false }],
      unavailable_post_ids: [],
    });
    const draft = usePostDraftStore();
    draft.setContent('Hydrate bookmark state');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    await flushPromises();

    expect(mocks.syncHydratedPostBookmarkState).toHaveBeenCalledWith({
      postId: 101,
      bookmarked: false,
      status: 'ready',
    }, 0);
    expect(mocks.feedStore.applyBookmarkStateUpdate).toHaveBeenCalledWith({
      postId: 101,
      bookmarked: false,
      status: 'ready',
    });
  });

  it('leaves a published bookmark hydration result fenced after a mutation advances its version', async () => {
    const hydration = deferred<{ items: Array<{ post_id: number; bookmarked: boolean }>; unavailable_post_ids: number[] }>();
    let bookmarkVersion = 0;
    const appliedUpdates: unknown[] = [];
    mocks.getPostBookmarkStates.mockReturnValueOnce(hydration.promise);
    mocks.captureBookmarkStateSyncVersion.mockImplementation(() => bookmarkVersion);
    mocks.syncHydratedPostBookmarkState.mockImplementation((update: unknown, capturedVersion: number) => {
      if (capturedVersion === bookmarkVersion) {
        appliedUpdates.push(update);
        return true;
      }
      return false;
    });
    const draft = usePostDraftStore();
    draft.setContent('Fence bookmark hydration');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    await flushPromises();
    expect(mocks.captureBookmarkStateSyncVersion).toHaveBeenCalledWith(101);

    bookmarkVersion += 1;
    hydration.resolve({
      items: [{ post_id: 101, bookmarked: false }],
      unavailable_post_ids: [],
    });
    await flushPromises();

    expect(appliedUpdates).toEqual([]);
    expect(mocks.feedStore.applyBookmarkStateUpdate).not.toHaveBeenCalled();
  });

  it('limits a viewer to one in-flight operation', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('Only once');
    const store = usePostPublishStore();

    const first = await store.startOrRetryDraft();
    expect(first.status).toBe('accepted');
    if (first.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const second = await store.startOrRetryDraft();
    expect(second.status).toBe('accepted');
    if (second.status !== 'accepted') {
      throw new Error('second submit was not accepted');
    }
    expect(second.operation.id).toBe(first.operation.id);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);

    request.resolve(publishedPost());
    await flushPromises();
  });

  it('retries a timed-out publish with the same operation id and key', async () => {
    mocks.createPost
      .mockRejectedValueOnce(Object.assign(new Error('Request timed out'), {
        isAxiosError: true,
        code: 'ECONNABORTED',
      }))
      .mockResolvedValueOnce(publishedPost());
    const draft = usePostDraftStore();
    draft.setContent('Retry me');
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');
    expect(draft.publishOperationID).toBe(first.id);

    const retryResult = await store.startOrRetryDraft();
    await flushPromises();
    expect(retryResult.status).toBe('accepted');
    if (retryResult.status !== 'accepted') {
      throw new Error('retry was not accepted');
    }
    expect(retryResult.operation.id).toBe(first.id);
    expect(mocks.createPost).toHaveBeenNthCalledWith(
      1,
      { content: 'Retry me', media: [] },
      { idempotencyKey: first.id },
    );
    expect(mocks.createPost).toHaveBeenNthCalledWith(
      2,
      { content: 'Retry me', media: [] },
      { idempotencyKey: first.id },
    );
    expect(draft.publishOperationID).toBeNull();
  });

  it('uses a new operation id after the failed draft is edited', async () => {
    mocks.createPost
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(publishedPost());
    const draft = usePostDraftStore();
    draft.setContent('Original');
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    draft.setContent('Edited');
    const secondResult = await store.startOrRetryDraft();
    await flushPromises();

    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('edited publish was not accepted');
    }
    expect(secondResult.operation.id).not.toBe(first.id);
    expect(mocks.createPost).toHaveBeenLastCalledWith(
      { content: 'Edited', media: [] },
      { idempotencyKey: secondResult.operation.id },
    );
  });

  it('does not clear an edited draft when the old operation succeeds', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('Old content');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;

    draft.setContent('New draft');
    request.resolve(publishedPost());
    await flushPromises();

    expect(operation?.phase).toBe('succeeded');
    expect(draft.content).toBe('New draft');
    expect(draft.publishOperationID).toBeNull();
  });

  it('keeps account B caches and draft untouched when account A completes', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('Account A post');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;

    mocks.authStore.currentIdentity = { id: 8, username: 'bob', display_name: 'Bob', avatar_url: '' };
    draft.setViewer(8);
    draft.setContent('Account B draft');
    request.resolve(publishedPost(7));
    await flushPromises();

    expect(operation?.phase).toBe('succeeded');
    expect(draft.viewerID).toBe(8);
    expect(draft.content).toBe('Account B draft');
    expect(mocks.feedStore.registerPublishedPost).not.toHaveBeenCalled();
    expect(mocks.profileSessionStore.registerPublishedTimelinePost).not.toHaveBeenCalled();
    expect(mocks.authStore.syncCurrentIdentityProfile).not.toHaveBeenCalled();
  });

  it('retries only media without an uploaded URL', async () => {
    mocks.uploadPostMedia
      .mockRejectedValueOnce(new Error('temporary upload failure'))
      .mockResolvedValueOnce('/media/second.png')
      .mockResolvedValueOnce('/media/first.png');
    const draft = usePostDraftStore();
    draft.setContent('Media retry');
    const firstID = draft.addMedia(file('first.png'));
    const secondID = draft.addMedia(file('second.png'));
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;
    await flushPromises();
    expect(operation?.phase).toBe('failed');
    expect(draft.media.find(item => item.id === firstID)?.uploadedURL).toBe('');
    expect(draft.media.find(item => item.id === secondID)?.uploadedURL).toBe('/media/second.png');

    await store.retry(operation!.id);
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(3);
    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Media retry',
        media: [
          { type: 'image', url: '/media/first.png' },
          { type: 'image', url: '/media/second.png' },
        ],
      },
      { idempotencyKey: operation?.id },
    );
  });

  it('does not accept a new draft while another operation is in flight', async () => {
    const retryRequest = deferred<Post>();
    mocks.createPost.mockRejectedValueOnce(new Error('initial failure'));
    mocks.createPost.mockReturnValue(retryRequest.promise);
    const draft = usePostDraftStore();
    draft.setContent('Post A');
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    await expect(store.retry(first.id)).resolves.toBe(true);
    expect(first.phase).toBe('publishing');
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(store.isDraftBlockedByAnotherPublish(7, draft.publishOperationID)).toBe(true);

    const blocked = await store.startOrRetryDraft();
    expect(blocked).toEqual({
      status: 'blocked',
      reason: 'another_publish_in_flight',
    });
    expect(draft.content).toBe('Post B');
    expect(draft.publishOperationID).toBeNull();
    expect(mocks.createPost).toHaveBeenCalledTimes(2);
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);

    retryRequest.resolve(publishedPost());
    await flushPromises();
    expect(first.phase).toBe('succeeded');
    expect(store.isDraftBlockedByAnotherPublish(7, draft.publishOperationID)).toBe(false);

    const secondResult = await store.startOrRetryDraft();
    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('second publish was not accepted');
    }
    expect(secondResult.operation.id).not.toBe(first.id);
    expect(mocks.createPost).toHaveBeenCalledTimes(3);
    expect(mocks.randomUUID).toHaveBeenCalledTimes(2);
    expect(mocks.createPost).toHaveBeenNthCalledWith(
      3,
      { content: 'Post B', media: [] },
      { idempotencyKey: secondResult.operation.id },
    );
    await flushPromises();
  });

  it('allows a new draft when the previous operation is only failed', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('initial failure'));
    const draft = usePostDraftStore();
    draft.setContent('Post A');
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    const secondResult = await store.startOrRetryDraft();
    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('second publish was not accepted');
    }
    expect(secondResult.operation.id).not.toBe(first.id);
    expect(mocks.createPost).toHaveBeenCalledTimes(2);
    await flushPromises();
  });

  it('does not retry a failed operation alongside a different active operation', async () => {
    const secondRequest = deferred<Post>();
    mocks.createPost.mockRejectedValueOnce(new Error('initial failure'));
    mocks.createPost.mockReturnValue(secondRequest.promise);
    const draft = usePostDraftStore();
    draft.setContent('Post A');
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    const secondResult = await store.startOrRetryDraft();
    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('second publish was not accepted');
    }
    const second = secondResult.operation;
    expect(second.phase).toBe('publishing');

    await expect(store.retry(first.id)).resolves.toBe(false);
    expect(first.phase).toBe('failed');
    expect(second.phase).toBe('publishing');
    expect(mocks.createPost).toHaveBeenCalledTimes(2);

    secondRequest.resolve(publishedPost());
    await flushPromises();
  });

  it('returns rejected when no authenticated viewer is available', async () => {
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    const result = await usePostPublishStore().startOrRetryDraft();

    expect(result).toEqual({ status: 'rejected', reason: 'unauthenticated' });
  });

  it('does not start uploads or publishing when the initial durable write fails', async () => {
    mocks.replacePostPublishOperation.mockRejectedValueOnce(new Error('quota exceeded'));
    const draft = usePostDraftStore();
    draft.setContent('Preserve me');
    draft.addMedia(file('unpublished.png'));

    const result = await usePostPublishStore().startOrRetryDraft();

    expect(result).toEqual({ status: 'rejected', reason: 'persistence_unavailable' });
    expect(draft.content).toBe('Preserve me');
    expect(draft.publishOperationID).toBeNull();
    expect(mocks.uploadPostMedia).not.toHaveBeenCalled();
    expect(mocks.createPost).not.toHaveBeenCalled();
  });

  it('durably checkpoints all successful upload URLs before creating the post', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Checkpoint media URLs');
    draft.addMedia(file('one.png'));
    draft.addMedia(file('two.png'));
    mocks.createPost.mockImplementation(async () => {
      const saved = mocks.publishRecords.get(7);
      expect(saved?.phase).toBe('publishing');
      expect(saved?.media.map((item: any) => item.uploadedURL)).toEqual([
        '/media/one.png',
        '/media/two.png',
      ]);
      return publishedPost();
    });

    const result = await usePostPublishStore().startOrRetryDraft();
    expect(result.status).toBe('accepted');
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'Checkpoint media URLs',
      media: [
        { type: 'image', url: '/media/one.png' },
        { type: 'image', url: '/media/two.png' },
      ],
    }, { idempotencyKey: expect.any(String) });
    expect(mocks.publishRecords.has(7)).toBe(false);
  });

  it('replays a recovered publishing operation with its original key and exact payload', async () => {
    const oldRequest = deferred<Post>();
    mocks.createPost
      .mockReturnValueOnce(oldRequest.promise)
      .mockResolvedValueOnce(publishedPost());
    const draft = usePostDraftStore();
    draft.setContent('Replay exactly');
    const image = file('already-uploaded.png');
    const mediaID = draft.addMedia(image);
    draft.setUploadedURL(mediaID, '/media/checkpointed.png');
    const firstStore = usePostPublishStore();
    const firstResult = await firstStore.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') throw new Error('initial publish was not accepted');
    await vi.waitFor(() => expect(mocks.createPost).toHaveBeenCalledTimes(1));
    const originalCall = mocks.createPost.mock.calls[0];

    // A new Pinia instance models a hard refresh: only IndexedDB remains.
    setActivePinia(createPinia());
    usePostDraftStore().setViewer(7);
    const recoveredStore = usePostPublishStore();
    await recoveredStore.activateViewer(7);
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledTimes(2);
    expect(mocks.createPost.mock.calls[1]).toEqual(originalCall);
    expect(recoveredStore.latestOperation?.id).toBe(firstResult.operation.id);
    expect(recoveredStore.latestOperation?.phase).toBe('succeeded');
  });

  it('scopes the visible operation to the authenticated viewer and hides it while logged out', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Viewer scoped');
    mocks.createPost.mockReturnValue(new Promise<Post>(() => {}));
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    expect(store.latestOperation?.publisherUserID).toBe(7);

    mocks.authStore.isAuthenticated = false;
    expect(store.latestOperation).toBeNull();
    await store.activateViewer(null);
    expect(mocks.publishRecords.get(7)?.id).toBe(result.status === 'accepted' ? result.operation.id : '');
  });

  it('fails closed and exposes a viewer-scoped error when hydration fails', async () => {
    mocks.getPostPublishOperation.mockRejectedValueOnce(new Error('IndexedDB unavailable'));
    const draft = usePostDraftStore();
    draft.setContent('Do not send without checking recovery');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();

    expect(result).toEqual({ status: 'rejected', reason: 'persistence_unavailable' });
    expect(store.recoveryError).toContain('Couldn’t restore the pending post');
    expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.createPost).not.toHaveBeenCalled();
  });

  it('deduplicates concurrent hydration for the same viewer', async () => {
    const request = deferred<any>();
    mocks.getPostPublishOperation.mockReturnValueOnce(request.promise);
    const store = usePostPublishStore();

    const first = store.activateViewer(7);
    const second = store.activateViewer(7);
    expect(mocks.getPostPublishOperation).toHaveBeenCalledTimes(1);

    request.resolve(null);
    await Promise.all([first, second]);
    expect(mocks.getPostPublishOperation).toHaveBeenCalledTimes(1);
  });

  it('resumes a hydrated operation after the viewer returns from a switch', async () => {
    const read = deferred<any>();
    mocks.getPostPublishOperation.mockImplementation(async (viewerID: number) => (
      viewerID === 7 ? read.promise : null
    ));
    const store = usePostPublishStore();
    const firstActivation = store.activateViewer(7);

    mocks.authStore.currentIdentity = { id: 8, username: 'bob', display_name: 'Bob', avatar_url: '' };
    await store.activateViewer(8);
    const record = mocks.serializePublishOperation({
      id: operationUUID(87),
      publisherUserID: 7,
      sourceDraftID: null,
      content: 'Resume after switching back',
      media: [],
      phase: 'publishing',
      failureKind: null,
      error: '',
      startedAt: 87,
      post: null,
    });
    mocks.publishRecords.set(7, record);
    read.resolve(record);
    await firstActivation;
    expect(mocks.createPost).not.toHaveBeenCalled();

    mocks.authStore.currentIdentity = { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' };
    await store.activateViewer(7);
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'Resume after switching back', media: [] },
      { idempotencyKey: operationUUID(87) },
    );
  });

  it('resumes only media missing a URL from a recovered uploading operation', async () => {
    const fileOne = file('already-done.png');
    const fileTwo = file('needs-upload.png');
    const record = mocks.serializePublishOperation({
      id: operationUUID(88),
      publisherUserID: 7,
      sourceDraftID: null,
      content: 'Resume upload',
      media: [
        { draftMediaID: 'one', file: fileOne, uploadedURL: '/media/already-done.png' },
        { draftMediaID: 'two', file: fileTwo, uploadedURL: '' },
      ],
      phase: 'uploading',
      failureKind: null,
      error: '',
      startedAt: 88,
      post: null,
    });
    mocks.publishRecords.set(7, record);
    const store = usePostPublishStore();

    await store.activateViewer(7);
    await flushPromises();

    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(1);
    expect(mocks.uploadPostMedia).toHaveBeenCalledWith(expect.objectContaining({ name: 'needs-upload.png' }));
    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'Resume upload',
      media: [
        { type: 'image', url: '/media/already-done.png' },
        { type: 'image', url: '/media/needs-upload.png' },
      ],
    }, { idempotencyKey: operationUUID(88) });
  });

  it('restores failed operations without automatically retrying them', async () => {
    const record = mocks.serializePublishOperation({
      id: operationUUID(89),
      publisherUserID: 7,
      sourceDraftID: null,
      content: 'Wait for retry',
      media: [],
      phase: 'failed',
      failureKind: 'retryable',
      error: 'Couldn’t confirm this post. Retry safely.',
      startedAt: 89,
      post: null,
    });
    mocks.publishRecords.set(7, record);
    const store = usePostPublishStore();

    await store.activateViewer(7);
    await flushPromises();

    expect(store.latestOperation?.phase).toBe('failed');
    expect(mocks.createPost).not.toHaveBeenCalled();
  });

  it('reconciles a saved success without sending POST again', async () => {
    const post = publishedPost();
    const record = mocks.serializePublishOperation({
      id: operationUUID(90),
      publisherUserID: 7,
      sourceDraftID: null,
      content: 'Already published',
      media: [],
      phase: 'succeeded',
      failureKind: null,
      error: '',
      startedAt: 90,
      post,
    });
    mocks.publishRecords.set(7, record);
    const store = usePostPublishStore();

    await store.activateViewer(7);
    await flushPromises();

    expect(store.latestOperation?.phase).toBe('succeeded');
    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(mocks.deletePostPublishOperation).toHaveBeenCalledWith(7, operationUUID(90));
  });

  it('marks an idempotency conflict non-retryable', async () => {
    mocks.createPost.mockRejectedValueOnce({
      response: { status: 409, data: { code: 'POST_IDEMPOTENCY_CONFLICT' } },
    });
    usePostDraftStore().setContent('Conflict');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');

    await flushPromises();

    expect(result.operation.phase).toBe('failed');
    expect(result.operation.failureKind).toBe('idempotency_conflict');
    expect(result.operation.error).toBe('This post can’t be retried safely.');
    await expect(store.retry(result.operation.id)).resolves.toBe(false);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
  });

  it('keeps a durable success checkpoint when source-draft cleanup fails', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Saved before publish');
    const sourceDraftID = await draft.saveCurrentDraft();
    mocks.deletePostDraft.mockRejectedValueOnce(new Error('source draft storage unavailable'));
    mocks.getPostDraft.mockResolvedValueOnce({
      id: sourceDraftID,
      viewerID: 7,
      content: 'Saved before publish',
      media: [],
      createdAt: 1,
      updatedAt: 2,
    });
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');

    await flushPromises();

    expect(result.operation.phase).toBe('succeeded');
    expect(mocks.publishRecords.get(7)).toMatchObject({
      id: result.operation.id,
      phase: 'succeeded',
      sourceDraftID,
      post: publishedPost(),
    });
    expect(mocks.deletePostPublishOperation).not.toHaveBeenCalled();
  });

  it('does not send the post if the exact pre-POST checkpoint fails', async () => {
    mocks.updatePostPublishOperation.mockRejectedValueOnce(new Error('quota exceeded'));
    usePostDraftStore().setContent('Checkpoint before network');

    const result = await usePostPublishStore().startOrRetryDraft();
    expect(result.status).toBe('accepted');
    await flushPromises();

    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(result.status === 'accepted' && result.operation.phase).toBe('failed');
    expect(result.status === 'accepted' && result.operation.error)
      .toBe('Couldn’t save publish progress on this device. Retry.');
  });

  it('does not retry network activity when its durable retry transition fails', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('offline'));
    usePostDraftStore().setContent('Retry checkpoint');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');
    await flushPromises();
    expect(result.operation.phase).toBe('failed');
    mocks.updatePostPublishOperation.mockRejectedValueOnce(new Error('quota exceeded'));

    await expect(store.retry(result.operation.id)).resolves.toBe(false);

    expect(result.operation.phase).toBe('failed');
    expect(result.operation.error).toBe('Couldn’t save publish progress on this device. Retry.');
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
  });
});
