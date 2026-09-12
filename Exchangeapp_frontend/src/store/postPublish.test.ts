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
  },
  profileSessionStore: {
    registerPublishedTimelinePost: vi.fn(),
  },
  createPost: vi.fn(),
  uploadPostMedia: vi.fn(),
  randomUUID: vi.fn(),
}));

vi.mock('../services/postService', () => ({
  createPost: mocks.createPost,
  uploadPostMedia: mocks.uploadPostMedia,
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
    const draft = usePostDraftStore();
    draft.clear();
    draft.setViewer(7);
  });

  it('starts immediately, sends one key, and reconciles the authoritative response', async () => {
    const draft = usePostDraftStore();
    draft.setContent('A background post');
    const store = usePostPublishStore();

    const result = store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;
    expect(operation.phase).toBe('publishing');
    expect(draft.publishOperationID).toBe(operation.id);

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
  });

  it('limits a viewer to one in-flight operation', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('Only once');
    const store = usePostPublishStore();

    const first = store.startOrRetryDraft();
    expect(first.status).toBe('accepted');
    if (first.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const second = store.startOrRetryDraft();
    expect(second.status).toBe('accepted');
    if (second.status !== 'accepted') {
      throw new Error('second submit was not accepted');
    }
    expect(second.operation.id).toBe(first.operation.id);
    expect(mocks.createPost).toHaveBeenCalledTimes(1);

    request.resolve(publishedPost());
    await flushPromises();
  });

  it('retries an ambiguous failure with the same operation id and key', async () => {
    mocks.createPost
      .mockRejectedValueOnce(new Error('timeout'))
      .mockResolvedValueOnce(publishedPost());
    const draft = usePostDraftStore();
    draft.setContent('Retry me');
    const store = usePostPublishStore();

    const firstResult = store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');
    expect(draft.publishOperationID).toBe(first.id);

    const retryResult = store.startOrRetryDraft();
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

    const firstResult = store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    draft.setContent('Edited');
    const secondResult = store.startOrRetryDraft();
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
    const result = store.startOrRetryDraft();
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
    const result = store.startOrRetryDraft();
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

    const result = store.startOrRetryDraft();
    expect(result.status).toBe('accepted');
    if (result.status !== 'accepted') {
      throw new Error('publish was not accepted');
    }
    const operation = result.operation;
    await flushPromises();
    expect(operation?.phase).toBe('failed');
    expect(draft.media.find(item => item.id === firstID)?.uploadedURL).toBe('');
    expect(draft.media.find(item => item.id === secondID)?.uploadedURL).toBe('/media/second.png');

    store.retry(operation!.id);
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

    const firstResult = store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    expect(store.retry(first.id)).toBe(true);
    expect(first.phase).toBe('publishing');
    expect(mocks.randomUUID).toHaveBeenCalledTimes(1);
    expect(store.isDraftBlockedByAnotherPublish(7, draft.publishOperationID)).toBe(true);

    const blocked = store.startOrRetryDraft();
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

    const secondResult = store.startOrRetryDraft();
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

    const firstResult = store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    const secondResult = store.startOrRetryDraft();
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

    const firstResult = store.startOrRetryDraft();
    expect(firstResult.status).toBe('accepted');
    if (firstResult.status !== 'accepted') {
      throw new Error('first publish was not accepted');
    }
    const first = firstResult.operation;
    await flushPromises();
    expect(first.phase).toBe('failed');

    draft.setContent('Post B');
    const secondResult = store.startOrRetryDraft();
    expect(secondResult.status).toBe('accepted');
    if (secondResult.status !== 'accepted') {
      throw new Error('second publish was not accepted');
    }
    const second = secondResult.operation;
    expect(second.phase).toBe('publishing');

    expect(store.retry(first.id)).toBe(false);
    expect(first.phase).toBe('failed');
    expect(second.phase).toBe('publishing');
    expect(mocks.createPost).toHaveBeenCalledTimes(2);

    secondRequest.resolve(publishedPost());
    await flushPromises();
  });

  it('returns rejected when no authenticated viewer is available', () => {
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    const result = usePostPublishStore().startOrRetryDraft();

    expect(result).toEqual({ status: 'rejected', reason: 'unauthenticated' });
  });
});
