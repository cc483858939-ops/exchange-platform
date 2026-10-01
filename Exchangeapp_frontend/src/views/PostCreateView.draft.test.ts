// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PersistedPostDraft } from '../storage/postDraftRepository';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  route: null as any,
  beforeRouteLeave: null as any,
  beforeRouteUpdate: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  drafts: new Map<string, PersistedPostDraft>(),
  deletePostDraft: vi.fn(),
  getPostDraft: vi.fn(),
  listPostDrafts: vi.fn(),
  savePostDraft: vi.fn(),
  feedStore: {
    registerPublishedPost: vi.fn(),
    applyBookmarkStateUpdate: vi.fn(),
  },
  profileSessionStore: { registerPublishedTimelinePost: vi.fn() },
  createPost: vi.fn(),
  getPostById: vi.fn(),
  uploadPostMedia: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  captureBookmarkStateSyncVersion: vi.fn(),
  syncHydratedPostBookmarkState: vi.fn(),
  publishOperations: new Map<number, any>(),
  getPostPublishOperation: vi.fn(),
  replacePostPublishOperation: vi.fn(),
  updatePostPublishOperation: vi.fn(),
  deletePostPublishOperation: vi.fn(),
  serializePublishOperation: vi.fn(),
  restorePublishOperation: vi.fn(),
  previewGenerator: {
    generate: vi.fn(),
    dispose: vi.fn(),
  },
}));

vi.mock('vue-router', async () => {
  const { reactive: makeReactive } = await import('vue');
  mocks.route = makeReactive({ name: 'PostCreate', query: {}, params: {}, fullPath: '/posts/new' });
  return {
    onBeforeRouteLeave: (guard: unknown) => { mocks.beforeRouteLeave = guard; },
    onBeforeRouteUpdate: (guard: unknown) => { mocks.beforeRouteUpdate = guard; },
    useRoute: () => mocks.route,
    useRouter: () => mocks.router,
  };
});

vi.mock('../store/auth', async () => {
  const { reactive: makeReactive } = await import('vue');
  mocks.authStore = makeReactive({
    isAuthenticated: true,
    sessionID: 'session-7',
    sessionVersion: 5,
    currentIdentity: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' },
    syncCurrentIdentityProfile: vi.fn(),
    captureRequestAuthBinding: () => {
      const auth = mocks.authStore;
      if (!auth.isAuthenticated || !auth.currentIdentity?.id) return null;
      return Object.freeze({ userID: auth.currentIdentity.id, sessionID: auth.sessionID, sessionVersion: auth.sessionVersion });
    },
    matchesRequestAuthBinding: (binding: any) => {
      const auth = mocks.authStore;
      return Boolean(auth.isAuthenticated
        && auth.currentIdentity?.id === binding.userID
        && auth.sessionID === binding.sessionID
        && auth.sessionVersion === binding.sessionVersion);
    },
  });
  return { useAuthStore: () => mocks.authStore };
});

vi.mock('../store/feed', () => ({ useFeedStore: () => mocks.feedStore }));
vi.mock('../store/profileSession', () => ({ useProfileSessionStore: () => mocks.profileSessionStore }));
vi.mock('../services/postService', () => ({
  createPost: mocks.createPost,
  getPostById: mocks.getPostById,
  uploadPostMedia: mocks.uploadPostMedia,
}));
vi.mock('../services/bookmarkService', () => ({ getPostBookmarkStates: mocks.getPostBookmarkStates }));
vi.mock('../store/sessionSync', () => ({
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
vi.mock('../utils/localImagePreview', () => ({
  createLocalImagePreviewGenerator: () => mocks.previewGenerator,
}));

const publishedPost = () => ({
  id: 101,
  author: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' },
  content: 'posted',
  media: [],
});

const quotedPost = (id: number) => ({
  id,
  published_at: '2026-09-01T00:00:00.000Z',
  author: { id: 8, username: 'source', display_name: 'Source user', avatar_url: '' },
  content: `Quoted post ${id}`,
  media: [],
});

const makeDraft = (
  id: string,
  viewerID = 7,
  content = `content for ${id}`,
  updatedAt = 200,
  media: PersistedPostDraft['media'] = [],
): PersistedPostDraft => ({
  id,
  viewerID,
  content,
  quotePostID: null,
  media,
  createdAt: updatedAt - 100,
  updatedAt,
});

const makeRouteLocation = (query: Record<string, unknown> = {}) => ({
  name: 'PostCreate',
  query,
  params: {},
  fullPath: `/posts/new${Object.keys(query).length ? `?draft=${String(query.draft)}` : ''}`,
});

const mountPage = () => mount(PostCreateView, {
  attachTo: document.body,
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const runLeaveGuard = async () => (
  (mocks.beforeRouteLeave as (() => Promise<boolean> | boolean) | null)?.()
);

const makeBeforeUnloadEvent = () => new Event('beforeunload', { cancelable: true });

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('PostCreateView durable drafts and exit protection', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.drafts.clear();
    mocks.publishOperations.clear();
    mocks.route.name = 'PostCreate';
    mocks.route.query = {};
    mocks.route.params = {};
    mocks.route.fullPath = '/posts/new';
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.sessionID = 'session-7';
    mocks.authStore.sessionVersion = 5;
    mocks.authStore.currentIdentity = { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' };
    mocks.savePostDraft.mockImplementation(async (record: PersistedPostDraft) => {
      mocks.drafts.set(record.id, record);
    });
    mocks.getPostDraft.mockImplementation(async (viewerID: number, id: string) => {
      const record = mocks.drafts.get(id);
      return record?.viewerID === viewerID ? record : null;
    });
    mocks.listPostDrafts.mockImplementation(async (viewerID: number) => (
      Array.from(mocks.drafts.values())
        .filter(record => record.viewerID === viewerID)
        .sort((left, right) => right.updatedAt - left.updatedAt)
    ));
    mocks.deletePostDraft.mockImplementation(async (viewerID: number, id: string) => {
      if (mocks.drafts.get(id)?.viewerID !== viewerID) return false;
      return mocks.drafts.delete(id);
    });
    mocks.router.back.mockReset();
    mocks.router.push.mockReset().mockImplementation(async (target: any) => {
      if (target?.name !== 'PostCreate') return undefined;
      const to = makeRouteLocation(target.query || {});
      const from = makeRouteLocation({ ...mocks.route.query });
      const allowed = await (mocks.beforeRouteUpdate as ((to: any, from: any) => Promise<boolean> | boolean))?.(to, from);
      if (allowed === false) return { type: 'aborted' };
      mocks.route.query = { ...to.query };
      mocks.route.fullPath = to.fullPath;
      return undefined;
    });
    mocks.router.replace.mockReset().mockImplementation(async (target: any) => {
      if (target?.name === 'PostCreate') {
        const to = makeRouteLocation(target.query || {});
        const from = makeRouteLocation({ ...mocks.route.query });
        const allowed = await (mocks.beforeRouteUpdate as ((to: any, from: any) => Promise<boolean> | boolean))?.(to, from);
        if (allowed === false) return { type: 'aborted' };
        mocks.route.query = { ...to.query };
        mocks.route.fullPath = to.fullPath;
        return undefined;
      }
      if (target?.name === 'Home') {
        const allowed = await runLeaveGuard();
        if (allowed === false) return { type: 'aborted' };
        mocks.route.name = 'Home';
      }
      return undefined;
    });
    mocks.createPost.mockReset().mockResolvedValue(publishedPost());
    mocks.getPostById.mockReset().mockImplementation(async (id: number) => quotedPost(id));
    mocks.uploadPostMedia.mockReset().mockImplementation(async (file: File) => `/media/${file.name}`);
    mocks.getPostBookmarkStates.mockReset().mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.captureBookmarkStateSyncVersion.mockReset().mockReturnValue(0);
    mocks.syncHydratedPostBookmarkState.mockReset().mockReturnValue(true);
    mocks.getPostPublishOperation.mockReset().mockImplementation(async (viewerID: number) => (
      mocks.publishOperations.get(viewerID) || null
    ));
    mocks.replacePostPublishOperation.mockReset().mockImplementation(async (record: any) => {
      mocks.publishOperations.set(record.publisherUserID, record);
    });
    mocks.updatePostPublishOperation.mockReset().mockImplementation(async (record: any) => {
      if (mocks.publishOperations.get(record.publisherUserID)?.id !== record.id) return false;
      mocks.publishOperations.set(record.publisherUserID, record);
      return true;
    });
    mocks.deletePostPublishOperation.mockReset().mockImplementation(async (viewerID: number, id: string) => {
      if (mocks.publishOperations.get(viewerID)?.id !== id) return false;
      mocks.publishOperations.delete(viewerID);
      return true;
    });
    mocks.serializePublishOperation.mockReset().mockImplementation((operation: any) => ({
      ...operation,
      schemaVersion: 4,
      publisherSessionID: operation.publisherSessionID ?? mocks.authStore.sessionID,
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
    mocks.restorePublishOperation.mockReset().mockImplementation((record: any) => ({
      ...record,
      publisherSessionID: record.publisherSessionID ?? null,
      quotePostID: record.quotePostID ?? null,
      media: record.media.map((item: any) => ({
        draftMediaID: item.draftMediaID,
        file: new File([item.blob], item.name, { type: item.type, lastModified: item.lastModified }),
        uploadedURL: item.uploadedURL,
      })),
    }));
    mocks.previewGenerator.generate.mockReset().mockImplementation(async (file: File) => ({
      blob: file.slice(0, file.size, file.type),
      width: 10,
      height: 10,
    }));
    mocks.previewGenerator.dispose.mockReset();

    const nativeURL = globalThis.URL;
    class TestURL extends nativeURL {}
    Object.defineProperty(TestURL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:composer-draft-test'),
    });
    Object.defineProperty(TestURL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    });
    vi.stubGlobal('URL', TestURL);

    const store = usePostDraftStore();
    store.clear();
    store.setViewer(7);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.unstubAllGlobals();
  });

  it('keeps a fresh composer empty instead of reopening the latest saved draft', async () => {
    mocks.drafts.set('latest', makeDraft('latest'));
    wrapper = mountPage();
    await flushPromises();

    expect(usePostDraftStore().content).toBe('');
    expect(usePostDraftStore().draftID).toBeNull();
    expect(wrapper.find('.composer-header__drafts').exists()).toBe(true);
    expect(mocks.getPostDraft).not.toHaveBeenCalled();
  });

  it('restores only the draft explicitly requested by the route', async () => {
    mocks.drafts.set('requested', makeDraft('requested', 7, 'Requested draft'));
    mocks.drafts.set('other', makeDraft('other', 7, 'Other draft', 500));
    mocks.route.query = { draft: 'requested' };
    mocks.route.fullPath = '/posts/new?draft=requested';
    wrapper = mountPage();
    await flushPromises();

    expect(usePostDraftStore().content).toBe('Requested draft');
    expect(usePostDraftStore().draftID).toBe('requested');
    expect(usePostDraftStore().hasUnsavedChanges).toBe(false);
  });

  it('lets the requested saved draft quote override a quote query', async () => {
    mocks.drafts.set('draft-abc', {
      ...makeDraft('draft-abc', 7, 'Saved commentary'),
      quotePostID: 99,
    });
    mocks.route.query = { draft: 'draft-abc', quote: '42' };
    mocks.route.fullPath = '/posts/new?draft=draft-abc&quote=42';
    wrapper = mountPage();
    await flushPromises();

    const store = usePostDraftStore();
    expect(store.draftID).toBe('draft-abc');
    expect(store.content).toBe('Saved commentary');
    expect(store.quotePostID).toBe(99);
    expect(mocks.getPostById).toHaveBeenCalledWith(99);
    expect(mocks.getPostById).not.toHaveBeenCalledWith(42);
    expect(wrapper.get('.composer-quote-preview__content').text()).toBe('Quoted post 99');
  });

  it('keeps user input and the requested route when saved-draft hydration becomes stale', async () => {
    const pendingRead = deferred<PersistedPostDraft | null>();
    mocks.getPostDraft.mockReturnValueOnce(pendingRead.promise);
    mocks.route.query = { draft: 'pending' };
    mocks.route.fullPath = '/posts/new?draft=pending';
    wrapper = mountPage();

    expect(mocks.getPostDraft).toHaveBeenCalledWith(7, 'pending');
    const store = usePostDraftStore();
    store.setContent('Newer user content');
    pendingRead.resolve(makeDraft('pending', 7, 'Old saved content'));
    await flushPromises();

    expect(store.content).toBe('Newer user content');
    expect(store.draftID).toBeNull();
    expect(store.savedSnapshot).toBeNull();
    expect(mocks.route.query).toEqual({ draft: 'pending' });
    expect(mocks.router.replace).not.toHaveBeenCalledWith({ name: 'PostCreate' });
  });

  it('keeps the newer route draft when an older hydration resolves later', async () => {
    const pendingA = deferred<PersistedPostDraft | null>();
    const pendingB = deferred<PersistedPostDraft | null>();
    mocks.getPostDraft.mockImplementation((_viewerID: number, id: string) => (
      id === 'draft-a' ? pendingA.promise : pendingB.promise
    ));
    mocks.route.query = { draft: 'draft-a' };
    mocks.route.fullPath = '/posts/new?draft=draft-a';
    wrapper = mountPage();
    expect(mocks.getPostDraft).toHaveBeenCalledWith(7, 'draft-a');

    mocks.route.query = { draft: 'draft-b' };
    mocks.route.fullPath = '/posts/new?draft=draft-b';
    await nextMicrotask();
    expect(mocks.getPostDraft).toHaveBeenCalledWith(7, 'draft-b');

    pendingB.resolve(makeDraft('draft-b', 7, 'Draft B'));
    await flushPromises();
    pendingA.resolve(makeDraft('draft-a', 7, 'Draft A'));
    await flushPromises();

    expect(usePostDraftStore().content).toBe('Draft B');
    expect(usePostDraftStore().draftID).toBe('draft-b');
    expect(mocks.route.query).toEqual({ draft: 'draft-b' });
    expect(mocks.router.replace).not.toHaveBeenCalledWith({ name: 'PostCreate' });
  });

  it('clears a missing or another-viewer draft query to a fresh composer', async () => {
    mocks.drafts.set('private-to-eight', makeDraft('private-to-eight', 8, 'secret'));
    mocks.route.query = { draft: 'private-to-eight' };
    mocks.route.fullPath = '/posts/new?draft=private-to-eight';
    wrapper = mountPage();
    await flushPromises();

    expect(usePostDraftStore().content).toBe('');
    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'PostCreate' });
    expect(mocks.route.query).toEqual({});
  });

  it('dynamically warns for unsaved text and media and removes the listener when clean or unmounted', async () => {
    wrapper = mountPage();
    const emptyEvent = makeBeforeUnloadEvent();
    window.dispatchEvent(emptyEvent);
    expect(emptyEvent.defaultPrevented).toBe(false);

    const store = usePostDraftStore();
    store.setContent('Unsaved text');
    const textEvent = makeBeforeUnloadEvent();
    window.dispatchEvent(textEvent);
    expect(textEvent.defaultPrevented).toBe(true);
    expect(mocks.savePostDraft).not.toHaveBeenCalled();

    store.setContent('');
    store.addMedia(new File(['pixels'], 'image.png', { type: 'image/png' }));
    const mediaEvent = makeBeforeUnloadEvent();
    window.dispatchEvent(mediaEvent);
    expect(mediaEvent.defaultPrevented).toBe(true);

    await store.saveCurrentDraft();
    const cleanEvent = makeBeforeUnloadEvent();
    window.dispatchEvent(cleanEvent);
    expect(cleanEvent.defaultPrevented).toBe(false);

    store.setContent('Changed after save');
    wrapper.unmount();
    wrapper = null;
    const afterUnmount = makeBeforeUnloadEvent();
    window.dispatchEvent(afterUnmount);
    expect(afterUnmount.defaultPrevented).toBe(false);
  });

  it('allows clean navigation without a dialog and clears the in-memory composer', async () => {
    wrapper = mountPage();
    const store = usePostDraftStore();
    store.setContent('Temporary content');
    store.clear();

    await expect(runLeaveGuard()).resolves.toBe(true);
    expect(wrapper.find('dialog.post-draft-exit-dialog').exists()).toBe(false);
    expect(store.content).toBe('');
  });

  it('keeps working content when Cancel is chosen', async () => {
    wrapper = mountPage();
    const store = usePostDraftStore();
    store.setContent('Keep this post');

    const navigation = runLeaveGuard();
    await nextMicrotask();
    expect(wrapper.get('dialog.post-draft-exit-dialog h2').text()).toBe('Save post?');
    await wrapper.get('.post-draft-exit-dialog__button--cancel').trigger('click');

    await expect(navigation).resolves.toBe(false);
    expect(store.content).toBe('Keep this post');
    expect(store.hasUnsavedChanges).toBe(true);
  });

  it('discards new input and keeps the durable version when discarding edits to a saved draft', async () => {
    wrapper = mountPage();
    const store = usePostDraftStore();
    store.setContent('Never saved');
    const navigation = runLeaveGuard();
    await nextMicrotask();
    await wrapper.get('.post-draft-exit-dialog__button--discard').trigger('click');
    await expect(navigation).resolves.toBe(true);
    expect(store.content).toBe('');
    expect(mocks.drafts.size).toBe(0);

    store.setContent('Durable version');
    const savedID = await store.saveCurrentDraft();
    store.setContent('Unsaved edit');
    const savedDraftNavigation = runLeaveGuard();
    await nextMicrotask();
    expect(wrapper.get('dialog.post-draft-exit-dialog h2').text()).toBe('Save changes?');
    await wrapper.get('.post-draft-exit-dialog__button--discard').trigger('click');
    await expect(savedDraftNavigation).resolves.toBe(true);
    expect(store.content).toBe('');
    expect(mocks.drafts.get(savedID)?.content).toBe('Durable version');
  });

  it('saves before leaving and stays blocked when IndexedDB save fails', async () => {
    wrapper = mountPage();
    const store = usePostDraftStore();
    store.setContent('Save me before leaving');
    const navigation = runLeaveGuard();
    await nextMicrotask();
    await wrapper.get('.post-draft-exit-dialog__button--save').trigger('click');
    await expect(navigation).resolves.toBe(true);
    expect(mocks.drafts.size).toBe(1);
    expect(store.content).toBe('');

    store.setContent('Keep on device memory');
    mocks.savePostDraft.mockRejectedValueOnce(new Error('quota'));
    const failedNavigation = runLeaveGuard();
    await nextMicrotask();
    await wrapper.get('.post-draft-exit-dialog__button--save').trigger('click');
    await flushPromises();
    expect(wrapper.get('.post-draft-exit-dialog__error').text())
      .toBe('Could not save this draft on this device. Try again or discard it.');
    expect(store.content).toBe('Keep on device memory');
    expect(store.hasUnsavedChanges).toBe(true);
    await wrapper.get('.post-draft-exit-dialog__button--cancel').trigger('click');
    await expect(failedNavigation).resolves.toBe(false);
  });

  it('uses the same exit decision when opening another saved draft', async () => {
    mocks.drafts.set('target', makeDraft('target', 7, 'Target draft'));
    wrapper = mountPage();
    await flushPromises();
    usePostDraftStore().setContent('Unsaved current composer');
    await wrapper.get('.composer-header__drafts').trigger('click');
    await flushPromises();
    await wrapper.get('.post-drafts-dialog__button--open').trigger('click');
    await nextMicrotask();

    expect(wrapper.get('dialog.post-draft-exit-dialog h2').text()).toBe('Save post?');
    await wrapper.get('.post-draft-exit-dialog__button--discard').trigger('click');
    await flushPromises();

    expect(mocks.route.query).toEqual({ draft: 'target' });
    expect(usePostDraftStore().content).toBe('Target draft');
    expect(usePostDraftStore().hasUnsavedChanges).toBe(false);
  });

  it('deletes a selected durable draft after confirmation', async () => {
    mocks.drafts.set('to-delete', makeDraft('to-delete'));
    wrapper = mountPage();
    await flushPromises();
    await wrapper.get('.composer-header__drafts').trigger('click');
    await flushPromises();
    await wrapper.get('.post-drafts-dialog__button--delete').trigger('click');
    expect(wrapper.get('dialog.confirm-dialog h2').text()).toBe('Delete draft?');
    await wrapper.get('.confirm-dialog__button--confirm').trigger('click');
    await flushPromises();

    expect(mocks.drafts.has('to-delete')).toBe(false);
    expect(wrapper.find('.composer-header__drafts').exists()).toBe(false);
  });

  it('does not intercept or clear an explicitly submitted background publish', async () => {
    mocks.createPost.mockImplementation(() => new Promise(() => {}));
    wrapper = mountPage();
    const store = usePostDraftStore();
    store.setContent('Submitted post');
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'Home',
      query: { tab: 'for-you' },
    });
    expect(store.publishOperationID).not.toBeNull();
    expect(await runLeaveGuard()).toBe(true);
    expect(wrapper.find('dialog.post-draft-exit-dialog').exists()).toBe(false);
    expect(store.content).toBe('Submitted post');
  });
});

const nextMicrotask = async () => {
  await Promise.resolve();
  await flushPromises();
};
