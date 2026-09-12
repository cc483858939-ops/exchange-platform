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

    const operation = store.startOrRetryDraft();
    expect(operation?.phase).toBe('publishing');
    expect(draft.publishOperationID).toBe(operation?.id);

    await flushPromises();
    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'A background post', media: [] },
      { idempotencyKey: operation?.id },
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
    const second = store.startOrRetryDraft();
    expect(second?.id).toBe(first?.id);
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

    const first = store.startOrRetryDraft();
    await flushPromises();
    expect(first?.phase).toBe('failed');
    expect(draft.publishOperationID).toBe(first?.id);

    const retry = store.startOrRetryDraft();
    await flushPromises();
    expect(retry?.id).toBe(first?.id);
    expect(mocks.createPost).toHaveBeenNthCalledWith(
      1,
      { content: 'Retry me', media: [] },
      { idempotencyKey: first?.id },
    );
    expect(mocks.createPost).toHaveBeenNthCalledWith(
      2,
      { content: 'Retry me', media: [] },
      { idempotencyKey: first?.id },
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

    const first = store.startOrRetryDraft();
    await flushPromises();
    draft.setContent('Edited');
    const second = store.startOrRetryDraft();
    await flushPromises();

    expect(second?.id).not.toBe(first?.id);
    expect(mocks.createPost).toHaveBeenLastCalledWith(
      { content: 'Edited', media: [] },
      { idempotencyKey: second?.id },
    );
  });

  it('does not clear an edited draft when the old operation succeeds', async () => {
    const request = deferred<Post>();
    mocks.createPost.mockReturnValue(request.promise);
    const draft = usePostDraftStore();
    draft.setContent('Old content');
    const store = usePostPublishStore();
    const operation = store.startOrRetryDraft();

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
    const operation = store.startOrRetryDraft();

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

    const operation = store.startOrRetryDraft();
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
});
