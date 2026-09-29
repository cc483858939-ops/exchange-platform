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

const persistedFailedOperation = (overrides: Record<string, any> = {}) => mocks.serializePublishOperation({
  id: operationUUID(901),
  publisherUserID: 7,
  sourceDraftID: null,
  content: 'Recovered post',
  media: [],
  phase: 'failed',
  failureKind: 'retryable',
  error: 'Couldn’t confirm this post. Retry safely.',
  startedAt: 901,
  post: null,
  ...overrides,
});

const persistedDraftMedia = (id: string, mediaFile: File, uploadedURL = '') => ({
  id,
  blob: mediaFile.slice(0, mediaFile.size, mediaFile.type),
  name: mediaFile.name,
  type: mediaFile.type,
  size: mediaFile.size,
  lastModified: mediaFile.lastModified,
  uploadedURL,
});

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

  it('blocks an edited failed draft without generating or persisting a replacement operation', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('offline'));
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

    expect(secondResult).toEqual({ status: 'blocked', reason: 'unresolved_publish' });
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(mocks.replacePostPublishOperation).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
    expect(mocks.publishRecords.get(7)?.id).toBe(first.id);
    expect(first.phase).toBe('failed');
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

  it('requires explicit durable abandonment before publishing an edited saved draft', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('initial failure'));
    mocks.createPost.mockResolvedValueOnce(publishedPost());
    const draft = usePostDraftStore();
    draft.setContent('Saved source');
    const sourceDraftID = await draft.saveCurrentDraft();
    draft.setContent('Edited source');
    const sourceSnapshot = draft.savedSnapshot;
    const sourceMedia = [...draft.media];
    const store = usePostPublishStore();

    const firstResult = await store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    expect(await store.abandonFailedOperation(first.id)).toBe(true);
    expect(mocks.deletePostPublishOperation).toHaveBeenCalledWith(7, first.id);
    expect(mocks.deletePostDraft).not.toHaveBeenCalled();
    expect(draft.publishOperationID).toBeNull();
    expect(draft.content).toBe('Edited source');
    expect(draft.media).toEqual(sourceMedia);
    expect(draft.draftID).toBe(sourceDraftID);
    expect(draft.savedSnapshot).toEqual(sourceSnapshot);
    expect(draft.hasUnsavedChanges).toBe(true);

    const secondResult = await store.startOrRetryDraft();
    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('second publish was not accepted');
    }
    expect(secondResult.operation.id).not.toBe(first.id);
    expect(secondResult.operation.sourceDraftID).toBe(sourceDraftID);
    expect(mocks.createPost).toHaveBeenCalledTimes(2);
    await flushPromises();
  });

  it('does not retry a failed operation alongside a different active operation', async () => {
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

    const second = {
      ...first,
      id: operationUUID(99),
      content: 'Post B',
      phase: 'publishing' as const,
      failureKind: null,
    };
    store.operations.push(second);

    await expect(store.retry(first.id)).resolves.toBe(false);
    expect(first.phase).toBe('failed');
    expect(second.phase).toBe('publishing');
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
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

  it('retries the exact recovered saved draft with the original key and durable payload', async () => {
    const originalFile = new File(['same image bytes'], 'same.png', {
      type: 'image/png',
      lastModified: 1234,
    });
    const reopenedFile = new File(['same image bytes'], 'same.png', {
      type: 'image/png',
      lastModified: 1234,
    });
    const operationID = operationUUID(902);
    const record = persistedFailedOperation({
      id: operationID,
      sourceDraftID: 'saved-source',
      content: 'Saved exact content',
      media: [{
        draftMediaID: 'saved-media-1',
        file: originalFile,
        uploadedURL: '/media/from-operation.png',
      }],
    });
    mocks.publishRecords.set(7, record);
    mocks.getPostDraft.mockResolvedValueOnce({
      id: 'saved-source',
      viewerID: 7,
      content: 'Saved exact content',
      media: [persistedDraftMedia('saved-media-1', reopenedFile)],
      createdAt: 1,
      updatedAt: 2,
    });
    const draft = usePostDraftStore();
    const store = usePostPublishStore();

    await store.activateViewer(7);
    const restoredOperationFile = store.latestOperation?.media[0]?.file;
    await expect(draft.loadSavedDraft('saved-source')).resolves.toEqual({ status: 'loaded' });
    expect(draft.media[0]?.file).not.toBe(restoredOperationFile);
    expect(draft.media[0]?.file).not.toBe(originalFile);
    expect(store.getDraftPublishBlockReason(7, draft.publishOperationID)).toBeNull();

    const result = await store.startOrRetryDraft();
    await flushPromises();

    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('recovered operation was not retried');
    expect(result.operation.id).toBe(operationID);
    expect(mocks.randomUUID).not.toHaveBeenCalled();
    expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.uploadPostMedia).not.toHaveBeenCalled();
    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'Saved exact content',
      media: [{ type: 'image', url: '/media/from-operation.png' }],
    }, { idempotencyKey: operationID });
  });

  it('rebinds the exact recovered source draft when Retry comes from the status action', async () => {
    const operationID = operationUUID(909);
    mocks.publishRecords.set(7, persistedFailedOperation({
      id: operationID,
      sourceDraftID: 'saved-source',
      content: 'Status retry source',
    }));
    mocks.getPostDraft.mockResolvedValueOnce({
      id: 'saved-source',
      viewerID: 7,
      content: 'Status retry source',
      media: [],
      createdAt: 1,
      updatedAt: 2,
    });
    const draft = usePostDraftStore();
    const store = usePostPublishStore();
    await store.activateViewer(7);
    await draft.loadSavedDraft('saved-source');

    await expect(store.retry(operationID)).resolves.toBe(true);
    expect(draft.publishOperationID).toBe(operationID);
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'Status retry source', media: [] },
      { idempotencyKey: operationID },
    );
    expect(draft.content).toBe('');
    expect(draft.draftID).toBeNull();
  });

  it('blocks a recovered saved draft after its content is edited', async () => {
    const record = persistedFailedOperation({
      id: operationUUID(903),
      sourceDraftID: 'saved-source',
      content: 'Original saved content',
    });
    mocks.publishRecords.set(7, record);
    mocks.getPostDraft.mockResolvedValueOnce({
      id: 'saved-source',
      viewerID: 7,
      content: 'Original saved content',
      media: [],
      createdAt: 1,
      updatedAt: 2,
    });
    const draft = usePostDraftStore();
    const store = usePostPublishStore();
    await store.activateViewer(7);
    await draft.loadSavedDraft('saved-source');
    draft.setContent('Edited saved content');

    expect(store.getDraftPublishBlockReason(7, draft.publishOperationID))
      .toBe('unresolved_publish');
    const result = await store.startOrRetryDraft();

    expect(result).toEqual({ status: 'blocked', reason: 'unresolved_publish' });
    expect(mocks.randomUUID).not.toHaveBeenCalled();
    expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(mocks.publishRecords.get(7)?.id).toBe(operationUUID(903));
  });

  it.each(['media id', 'file name', 'file type', 'file size', 'last modified', 'media order'])(
    'does not treat changed %s as the recovered saved submission',
    async changedField => {
      const operationFiles = [
        new File(['first'], 'one.png', { type: 'image/png', lastModified: 111 }),
        new File(['second'], 'two.webp', { type: 'image/webp', lastModified: 222 }),
      ];
      const operationMedia = operationFiles.map((item, index) => ({
        draftMediaID: `media-${index + 1}`,
        file: item,
        uploadedURL: `/media/${index + 1}.png`,
      }));
      const sourceMedia = operationFiles.map((item, index) => (
        persistedDraftMedia(`media-${index + 1}`, new File([item], item.name, {
          type: item.type,
          lastModified: item.lastModified,
        }))
      ));

      switch (changedField) {
        case 'media id':
          sourceMedia[0]!.id = 'different-media-id';
          break;
        case 'file name':
          sourceMedia[0] = persistedDraftMedia('media-1', new File(['first'], 'renamed.png', {
            type: 'image/png',
            lastModified: 111,
          }));
          break;
        case 'file type':
          sourceMedia[0] = persistedDraftMedia('media-1', new File(['first'], 'one.png', {
            type: 'image/jpeg',
            lastModified: 111,
          }));
          break;
        case 'file size':
          sourceMedia[0] = persistedDraftMedia('media-1', new File(['larger first'], 'one.png', {
            type: 'image/png',
            lastModified: 111,
          }));
          break;
        case 'last modified':
          sourceMedia[0] = persistedDraftMedia('media-1', new File(['first'], 'one.png', {
            type: 'image/png',
            lastModified: 999,
          }));
          break;
        case 'media order':
          sourceMedia.reverse();
          break;
      }

      const record = persistedFailedOperation({
        id: operationUUID(904),
        sourceDraftID: 'saved-source',
        content: 'Same source content',
        media: operationMedia,
      });
      mocks.publishRecords.set(7, record);
      mocks.getPostDraft.mockResolvedValueOnce({
        id: 'saved-source',
        viewerID: 7,
        content: 'Same source content',
        media: sourceMedia,
        createdAt: 1,
        updatedAt: 2,
      });
      const draft = usePostDraftStore();
      const store = usePostPublishStore();
      await store.activateViewer(7);
      await draft.loadSavedDraft('saved-source');

      await expect(store.startOrRetryDraft()).resolves.toEqual({
        status: 'blocked',
        reason: 'unresolved_publish',
      });
      expect(mocks.randomUUID).not.toHaveBeenCalled();
      expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
      expect(mocks.createPost).not.toHaveBeenCalled();
    },
  );

  it('blocks an unrelated saved draft and a refreshed unsaved composer', async () => {
    const record = persistedFailedOperation({
      id: operationUUID(905),
      sourceDraftID: 'saved-source-x',
      content: 'Same text',
    });
    mocks.publishRecords.set(7, record);
    mocks.getPostDraft.mockResolvedValueOnce({
      id: 'saved-source-y',
      viewerID: 7,
      content: 'Same text',
      media: [],
      createdAt: 1,
      updatedAt: 2,
    });
    const draft = usePostDraftStore();
    const store = usePostPublishStore();
    await store.activateViewer(7);
    await draft.loadSavedDraft('saved-source-y');

    await expect(store.startOrRetryDraft()).resolves.toEqual({
      status: 'blocked',
      reason: 'unresolved_publish',
    });

    draft.clear();
    draft.setViewer(7);
    draft.setContent('Same text');
    await expect(store.startOrRetryDraft()).resolves.toEqual({
      status: 'blocked',
      reason: 'unresolved_publish',
    });
    expect(mocks.randomUUID).not.toHaveBeenCalled();
    expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
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
    const draft = usePostDraftStore();
    draft.setContent('Conflict');
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
    expect(store.getDraftPublishBlockReason(7, draft.publishOperationID))
      .toBe('idempotency_conflict');

    draft.setContent('Different post');
    await expect(store.startOrRetryDraft()).resolves.toEqual({
      status: 'blocked',
      reason: 'unresolved_publish',
    });
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(mocks.replacePostPublishOperation).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
  });

  it('keeps a durable success checkpoint when source-draft cleanup fails', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Saved before publish');
    const sourceDraftID = await draft.saveCurrentDraft();
    mocks.deletePostDraft.mockRejectedValue(new Error('source draft storage unavailable'));
    const sourceDraftRecord = {
      id: sourceDraftID,
      viewerID: 7,
      content: 'Saved before publish',
      media: [],
      createdAt: 1,
      updatedAt: 2,
    };
    mocks.getPostDraft.mockResolvedValue(sourceDraftRecord);
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

    draft.setContent('A new post');
    const uuidCalls = mocks.randomUUID.mock.calls.length;
    await expect(store.startOrRetryDraft()).resolves.toEqual({
      status: 'blocked',
      reason: 'cleanup_pending',
    });
    expect(store.getDraftPublishBlockReason(7)).toBe('cleanup_pending');
    expect(mocks.randomUUID).toHaveBeenCalledTimes(uuidCalls);
    expect(mocks.replacePostPublishOperation).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
    expect(mocks.publishRecords.get(7)?.id).toBe(result.operation.id);
    expect(result.operation.phase).toBe('succeeded');
  });

  it('keeps a successful operation unresolved when its durable delete fails', async () => {
    const operationID = operationUUID(906);
    const record = persistedFailedOperation({
      id: operationID,
      content: 'Already published',
      phase: 'succeeded',
      failureKind: null,
      error: '',
      post: publishedPost(),
    });
    mocks.publishRecords.set(7, record);
    mocks.deletePostPublishOperation.mockResolvedValue(false);
    const draft = usePostDraftStore();
    draft.setContent('New post');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();

    expect(result).toEqual({ status: 'blocked', reason: 'cleanup_pending' });
    expect(store.latestOperation).toMatchObject({ id: operationID, phase: 'succeeded' });
    expect(mocks.publishRecords.get(7)?.id).toBe(operationID);
    expect(mocks.randomUUID).not.toHaveBeenCalled();
    expect(mocks.replacePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.createPost).not.toHaveBeenCalled();
  });

  it('retires a cleaned success before creating the next operation', async () => {
    const previousID = operationUUID(907);
    mocks.publishRecords.set(7, persistedFailedOperation({
      id: previousID,
      content: 'Already published',
      phase: 'succeeded',
      failureKind: null,
      error: '',
      post: publishedPost(),
    }));
    const nextRequest = deferred<Post>();
    mocks.createPost.mockReturnValue(nextRequest.promise);
    const draft = usePostDraftStore();
    draft.setContent('Next post');
    const store = usePostPublishStore();

    const result = await store.startOrRetryDraft();
    await flushPromises();

    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('new publish was not accepted');
    expect(result.operation.id).not.toBe(previousID);
    expect(result.operation.content).toBe('Next post');
    expect(mocks.deletePostPublishOperation).toHaveBeenCalledWith(7, previousID);
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(mocks.replacePostPublishOperation).toHaveBeenCalledTimes(1);
    expect(mocks.publishRecords.get(7)?.id).toBe(result.operation.id);
    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'Next post', media: [] },
      { idempotencyKey: result.operation.id },
    );

    nextRequest.resolve(publishedPost());
    await flushPromises();
  });

  it('does not retire a newer durable operation when abandoning a stale failed record', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('ambiguous failure'));
    const draft = usePostDraftStore();
    draft.setContent('Original failed post');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');
    const first = result.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    const newerRecord = persistedFailedOperation({
      id: operationUUID(908),
      content: 'Newer durable owner',
    });
    mocks.publishRecords.set(7, newerRecord);
    draft.setContent('A different composer');

    await expect(store.abandonFailedOperation(first.id)).resolves.toBe(false);
    expect(mocks.publishRecords.get(7)?.id).toBe(operationUUID(908));
    expect(store.getOperation(first.id)?.phase).toBe('failed');
    await expect(store.startOrRetryDraft()).resolves.toEqual({
      status: 'blocked',
      reason: 'unresolved_publish',
    });
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(mocks.replacePostPublishOperation).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
  });

  it('does not let another viewer abandon the current viewer’s operation', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('ambiguous failure'));
    usePostDraftStore().setContent('Viewer-owned failure');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');
    await flushPromises();

    mocks.authStore.currentIdentity = { id: 8, username: 'bob', display_name: 'Bob', avatar_url: '' };
    usePostDraftStore().setViewer(8);

    await expect(store.abandonFailedOperation(result.operation.id)).resolves.toBe(false);
    expect(mocks.deletePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.publishRecords.get(7)?.id).toBe(result.operation.id);
  });

  it('does not abandon a failed operation after its retry has started running', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('initial ambiguous response'));
    const draft = usePostDraftStore();
    draft.setContent('Retry before discard');
    const store = usePostPublishStore();
    const result = await store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') throw new Error('publish was not accepted');
    const operation = result.operation;
    await flushPromises();
    expect(operation.phase).toBe('failed');

    const retryRequest = deferred<Post>();
    mocks.createPost.mockReturnValue(retryRequest.promise);
    await expect(store.retry(operation.id)).resolves.toBe(true);
    expect(operation.phase).toBe('publishing');

    await expect(store.abandonFailedOperation(operation.id)).resolves.toBe(false);
    expect(mocks.deletePostPublishOperation).not.toHaveBeenCalled();
    expect(mocks.publishRecords.get(7)?.id).toBe(operation.id);

    retryRequest.resolve(publishedPost());
    await flushPromises();
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
