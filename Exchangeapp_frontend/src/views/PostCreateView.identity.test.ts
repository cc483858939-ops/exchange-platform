// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { defineComponent, h, nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';
import { usePostPublishStore } from '../store/postPublish';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  route: null as any,
  beforeRouteLeave: null as any,
  beforeRouteUpdate: null as any,
  feedStore: { registerPublishedPost: vi.fn() },
  profileSessionStore: { registerPublishedTimelinePost: vi.fn() },
  createPost: vi.fn(),
  getPostById: vi.fn(),
  uploadPostMedia: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  captureBookmarkStateSyncVersion: vi.fn(),
  syncHydratedPostBookmarkState: vi.fn(),
  publishRecords: new Map<number, any>(),
  getPostPublishOperation: vi.fn(),
  claimPostPublishOperation: vi.fn(),
  updatePostPublishOperation: vi.fn(),
  deletePostPublishOperation: vi.fn(),
  serializePublishOperation: vi.fn(),
  restorePublishOperation: vi.fn(),
}));

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue');
  mocks.route = reactive({ name: 'PostCreate', query: {}, params: {}, fullPath: '/posts/new' });
  return {
    useRouter: () => mocks.router,
    useRoute: () => mocks.route,
    onBeforeRouteLeave: (guard: unknown) => { mocks.beforeRouteLeave = guard; },
    onBeforeRouteUpdate: (guard: unknown) => { mocks.beforeRouteUpdate = guard; },
  };
});

vi.mock('../store/auth', async () => {
  const { reactive } = await import('vue');
  mocks.authStore = reactive({
    isAuthenticated: true,
    sessionID: 'session-7',
    sessionVersion: 5,
    currentIdentity: {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: 'https://example.test/alice.jpg',
    },
    syncCurrentIdentityProfile: vi.fn(),
    captureRequestAuthBinding: () => {
      const store = mocks.authStore;
      if (!store.isAuthenticated || !store.currentIdentity?.id) return null;
      return Object.freeze({
        userID: store.currentIdentity.id,
        sessionID: store.sessionID ?? `session-${store.currentIdentity.id}`,
        sessionVersion: store.sessionVersion,
      });
    },
    matchesRequestAuthBinding: (binding: any) => {
      const store = mocks.authStore;
      return Boolean(store.isAuthenticated
        && store.currentIdentity?.id === binding.userID
        && (store.sessionID ?? `session-${store.currentIdentity.id}`) === binding.sessionID
        && store.sessionVersion === binding.sessionVersion);
    },
  });
  return { useAuthStore: () => mocks.authStore };
});

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/profileSession', () => ({
  useProfileSessionStore: () => mocks.profileSessionStore,
}));

vi.mock('../services/postService', () => ({
  createPost: mocks.createPost,
  getPostById: mocks.getPostById,
  uploadPostMedia: mocks.uploadPostMedia,
}));

vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
}));

vi.mock('../store/sessionSync', () => ({
  captureBookmarkStateSyncVersion: mocks.captureBookmarkStateSyncVersion,
  syncHydratedPostBookmarkState: mocks.syncHydratedPostBookmarkState,
}));

vi.mock('../storage/postPublishRepository', () => ({
  getPostPublishOperation: mocks.getPostPublishOperation,
  claimPostPublishOperation: mocks.claimPostPublishOperation,
  updatePostPublishOperation: mocks.updatePostPublishOperation,
  deletePostPublishOperation: mocks.deletePostPublishOperation,
  serializePublishOperation: mocks.serializePublishOperation,
  restorePublishOperation: mocks.restorePublishOperation,
}));

const identity = (id = 7, username = id === 7 ? 'alice' : 'bob') => ({
  id,
  username,
  display_name: id === 7 ? 'Alice Smith' : 'Bob Jones',
  avatar_url: id === 7
    ? 'https://example.test/alice.jpg'
    : 'https://example.test/bob.jpg',
});

const publishedPost = (authorID = 7) => ({
  id: 101,
  author: {
    id: authorID,
    username: authorID === 7 ? 'alice' : 'bob',
    display_name: authorID === 7 ? 'Alice Smith' : 'Bob Jones',
    avatar_url: '',
  },
  content: 'published',
  media: [],
});

const quotedPost = (id = 42): Post => ({
  id,
  created_at: '2026-09-01T00:00:00.000Z',
  updated_at: '2026-09-01T00:00:00.000Z',
  published_at: '2026-09-01T00:00:00.000Z',
  author: identity(8),
  content: `Quoted post ${id}`,
  language: 'en',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  repost_count: 0,
  reply_count: 0,
  quote_count: 0,
  view_count: 0,
  deleted: false,
});

const EmojiPickerStub = defineComponent({
  name: 'EmojiPickerPopover',
  props: {
    id: { type: String, required: true },
    open: { type: Boolean, required: true },
    anchorEl: { type: Object, default: null },
    disabled: { type: Boolean, default: false },
  },
  emits: ['select', 'close'],
  setup(props) {
    return () => props.open
      ? h('div', {
        class: 'emoji-picker-popover-stub',
        'data-picker-id': props.id,
      })
      : null;
  },
});

const mountPage = () => mount(PostCreateView, {
  attachTo: document.body,
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      EmojiPickerPopover: EmojiPickerStub,
      RouterLink: {
        props: ['to'],
        template: '<a :data-to="JSON.stringify(to)"><slot /></a>',
      },
    },
  },
});

const deferred = <T,>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>(resolvePromise => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

describe('PostCreateView identity and text publishing', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.sessionID = 'session-7';
    mocks.authStore!.sessionVersion = 5;
    mocks.authStore!.currentIdentity = identity();
    Object.assign(mocks.route, {
      name: 'PostCreate',
      query: {},
      params: {},
      fullPath: '/posts/new',
    });
    mocks.createPost.mockResolvedValue(publishedPost());
    mocks.getPostById.mockReset().mockImplementation(async (id: number) => quotedPost(id));
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.captureBookmarkStateSyncVersion.mockReturnValue(0);
    mocks.syncHydratedPostBookmarkState.mockReturnValue(true);
    mocks.feedStore.registerPublishedPost.mockReturnValue(true);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
    mocks.publishRecords.clear();
    mocks.getPostPublishOperation.mockImplementation(async (viewerID: number) => mocks.publishRecords.get(viewerID) || null);
    mocks.claimPostPublishOperation.mockImplementation(async (record: any) => {
      const current = mocks.publishRecords.get(record.publisherUserID);
      if (current) return { status: 'occupied', operation: current };
      mocks.publishRecords.set(record.publisherUserID, record);
      return { status: 'claimed' };
    });
    mocks.updatePostPublishOperation.mockImplementation(async (record: any) => {
      if (mocks.publishRecords.get(record.publisherUserID)?.id !== record.id) return false;
      mocks.publishRecords.set(record.publisherUserID, record);
      return true;
    });
    mocks.deletePostPublishOperation.mockImplementation(async (viewerID: number, id: string) => {
      if (mocks.publishRecords.get(viewerID)?.id !== id) return false;
      mocks.publishRecords.delete(viewerID);
      return true;
    });
    mocks.serializePublishOperation.mockImplementation((operation: any) => ({
      ...operation,
      schemaVersion: 4,
      publisherSessionID: operation.publisherSessionID ?? mocks.authStore.sessionID,
      sourceDraftSnapshot: operation.sourceDraftSnapshot ?? null,
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
      publisherSessionID: record.publisherSessionID ?? null,
      quotePostID: record.quotePostID ?? null,
      sourceDraftSnapshot: record.sourceDraftSnapshot ?? null,
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

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('renders the current identity without a profile request', () => {
    wrapper = mountPage();

    expect(wrapper.find('.composer-author__copy').exists()).toBe(false);
    const image = wrapper.get('.composer-author__avatar .user-avatar__image');
    expect(image.attributes('src')).toBe('https://example.test/alice.jpg');
    expect(image.attributes('loading')).toBe('eager');
    expect(image.attributes('decoding')).toBe('async');
  });

  it('clears the draft when the authenticated viewer changes', async () => {
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('Alice draft');

    mocks.authStore!.currentIdentity = identity(8);
    await nextTick();

    expect(wrapper.find('.composer-author__copy').exists()).toBe(false);
    expect(wrapper.get('.composer-author__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/bob.jpg');
    expect(usePostDraftStore().viewerID).toBe(8);
    expect(usePostDraftStore().content).toBe('');
  });

  it('uses a compact composer surface with accessible controls', () => {
    wrapper = mountPage();

    const back = wrapper.get('.composer-header__back');
    expect(back.attributes('aria-label')).toBe('Back');
    expect(back.text()).not.toContain('Back');
    expect(wrapper.get('.composer-header h1').text()).toBe('Post');
    expect(wrapper.get('.composer-header').find('.publish-button').exists()).toBe(false);
    expect(wrapper.find('.composer-input').exists()).toBe(true);
    const toolbar = wrapper.get('.composer-toolbar');
    expect(toolbar.find('.composer-toolbar__tools .media-picker').exists()).toBe(true);
    expect(toolbar.find('.composer-toolbar__tools .emoji-picker-trigger').exists()).toBe(true);
    const publish = toolbar.get('.publish-button');
    expect(publish.attributes('type')).toBe('submit');
    expect(publish.attributes('form')).toBeUndefined();
    expect(publish.attributes('disabled')).toBeDefined();
    expect(wrapper.get('.media-picker').attributes('aria-label')).toBe('Add images');
    expect(wrapper.get('.media-picker').text()).not.toContain('Add images');
    expect(wrapper.find('.composer-progress').exists()).toBe(false);
  });

  it('enables the toolbar publish action only when content is valid', async () => {
    wrapper = mountPage();
    const publish = wrapper.get('.composer-toolbar__actions .publish-button');

    expect(publish.attributes('disabled')).toBeDefined();
    await wrapper.get('#post-content').setValue('A valid post');
    expect(publish.attributes('disabled')).toBeUndefined();
  });

  it('keeps Post actionable while succeeded-operation cleanup is pending', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Post after cleanup');
    const publishStore = usePostPublishStore();
    vi.spyOn(publishStore, 'getDraftPublishBlockReason').mockReturnValue('cleanup_pending');
    wrapper = mountPage();
    await nextTick();

    const publish = wrapper.get('.publish-button');
    expect(publish.attributes('disabled')).toBeUndefined();
    expect(publish.attributes('aria-describedby')).toBe('publish-blocked-message');
    expect(wrapper.get('.publish-blocked-message').text())
      .toContain('Finishing the previous post on this device.');
  });

  it('toggles the emoji picker with an accessible control', async () => {
    wrapper = mountPage();

    const button = wrapper.get('.emoji-picker-trigger');
    expect(button.attributes('aria-label')).toBe('Add emoji');
    expect(button.attributes('title')).toBe('Add emoji');
    expect(button.attributes('aria-haspopup')).toBe('dialog');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(button.attributes('aria-controls')).toBe('post-emoji-picker');

    await button.trigger('click');
    expect(button.attributes('aria-expanded')).toBe('true');
    expect(wrapper.get('.emoji-picker-popover-stub').attributes('data-picker-id'))
      .toBe('post-emoji-picker');

    await button.trigger('click');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(wrapper.find('.emoji-picker-popover-stub').exists()).toBe(false);
  });

  it('inserts an emoji at the saved caret and restores focus', async () => {
    wrapper = mountPage();
    const input = wrapper.get('#post-content');

    await input.setValue('hello world');
    (input.element as HTMLTextAreaElement).setSelectionRange(6, 6);
    await input.trigger('select');
    await wrapper.get('.emoji-picker-trigger').trigger('click');

    wrapper.findComponent(EmojiPickerStub).vm.$emit('select', '😂');
    await flushPromises();

    expect((input.element as HTMLTextAreaElement).value).toBe('hello 😂world');
    expect(usePostDraftStore().content).toBe('hello 😂world');
    expect(document.activeElement).toBe(input.element);
    expect((input.element as HTMLTextAreaElement).selectionStart).toBe(8);
    expect((input.element as HTMLTextAreaElement).selectionEnd).toBe(8);
  });

  it('replaces the saved selection and keeps the picker open for another emoji', async () => {
    wrapper = mountPage();
    const input = wrapper.get('#post-content');
    const textarea = input.element as HTMLTextAreaElement;

    await input.setValue('hello bad world');
    textarea.setSelectionRange(6, 9);
    await input.trigger('select');
    await wrapper.get('.emoji-picker-trigger').trigger('click');
    const picker = wrapper.findComponent(EmojiPickerStub);

    picker.vm.$emit('select', '❤️');
    await flushPromises();
    picker.vm.$emit('select', '🔥');
    await flushPromises();

    expect(textarea.value).toBe('hello ❤️🔥 world');
    expect(wrapper.get('.emoji-picker-trigger').attributes('aria-expanded')).toBe('true');
    expect(document.activeElement).toBe(textarea);
    expect(textarea.selectionStart).toBe('hello ❤️🔥'.length);
    expect(textarea.selectionEnd).toBe('hello ❤️🔥'.length);
  });

  it('closes on escape and preserves emoji content when publishing', async () => {
    wrapper = mountPage();
    const input = wrapper.get('#post-content');
    await input.setValue('A post 😂');
    await wrapper.get('.emoji-picker-trigger').trigger('click');

    wrapper.findComponent(EmojiPickerStub).vm.$emit('close', 'escape');
    await nextTick();

    expect(wrapper.get('.emoji-picker-trigger').attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(input.element);

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.createPost).toHaveBeenCalledWith(
      { content: 'A post 😂', media: [] },
      expect.objectContaining({
        idempotencyKey: expect.any(String),
        authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7', sessionVersion: 5 }),
      }),
    );
  });

  it('closes and disables the picker while publishing', async () => {
    mocks.createPost.mockReturnValue(new Promise(() => {}));
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('Pending post');
    await wrapper.get('form').trigger('submit');
    await nextTick();

    const button = wrapper.get('.emoji-picker-trigger');
    expect(button.attributes('disabled')).toBeDefined();
    expect(button.attributes('aria-expanded')).toBe('false');
  });

  it('shows remaining characters only near or beyond the content limit', async () => {
    wrapper = mountPage();
    const input = wrapper.get('#post-content');

    await input.setValue('a'.repeat(100));
    expect(wrapper.find('.composer-character-count').exists()).toBe(false);

    await input.setValue('a'.repeat(9_000));
    expect(wrapper.get('.composer-character-count').text()).toBe('1000');

    await input.setValue('a'.repeat(10_000));
    expect(wrapper.get('.composer-character-count').text()).toBe('0');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeUndefined();

    await input.setValue('a'.repeat(10_001));
    expect(wrapper.get('.composer-character-count').text()).toBe('-1');
    expect(wrapper.get('.composer-character-count').classes())
      .toContain('composer-character-count--over');
    expect(wrapper.get('.content-error').text())
      .toContain('Post must be 10000 characters or fewer.');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeDefined();
  });

  it('publishes a text-only Post and clears the draft after success', async () => {
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('A post from the current identity');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'A post from the current identity',
        media: [],
      },
      expect.objectContaining({
        idempotencyKey: expect.any(String),
        authBinding: expect.objectContaining({ userID: 7, sessionID: 'session-7', sessionVersion: 5 }),
      }),
    );
    expect(mocks.uploadPostMedia).not.toHaveBeenCalled();
    expect(mocks.feedStore.registerPublishedPost).toHaveBeenCalledWith(
      publishedPost(),
      7,
    );
    expect(mocks.profileSessionStore.registerPublishedTimelinePost).toHaveBeenCalledWith(
      publishedPost(),
      7,
    );
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'Home',
      query: { tab: 'for-you' },
    });
    expect(usePostDraftStore().dirty).toBe(false);
  });

  it('keeps the publish operation alive after the composer unmounts', async () => {
    const request = deferred<ReturnType<typeof publishedPost>>();
    mocks.createPost.mockReturnValue(request.promise);
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('Survive composer unmount');

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    const operation = usePostPublishStore().latestOperation;
    expect(operation?.phase).toBe('publishing');
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'Home',
      query: { tab: 'for-you' },
    });

    wrapper.unmount();
    wrapper = null;
    request.resolve(publishedPost());
    await flushPromises();

    expect(operation?.phase).toBe('succeeded');
    expect(mocks.feedStore.registerPublishedPost).toHaveBeenCalledWith(publishedPost(), 7);
    expect(mocks.profileSessionStore.registerPublishedTimelinePost)
      .toHaveBeenCalledWith(publishedPost(), 7);
    expect(usePostDraftStore().content).toBe('');
    expect(usePostDraftStore().publishOperationID).toBeNull();
  });

  it('keeps a new draft in the composer while another post is sending', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Post B');
    const publishStore = usePostPublishStore();
    publishStore.operations.push({
      id: 'publish-a',
      publisherUserID: 7,
      publisherSessionID: 'session-7',
      sourceDraftID: null,
      sourceDraftSnapshot: null,
      content: 'Post A',
      quotePostID: null,
      media: [],
      phase: 'publishing',
      failureKind: null,
      error: '',
      startedAt: Date.now(),
      post: null,
    });

    wrapper = mountPage();
    await nextTick();

    const button = wrapper.get('.publish-button');
    expect(button.attributes('disabled')).toBeDefined();
    expect(button.attributes('aria-describedby')).toBe('publish-blocked-message');
    expect(wrapper.get('.publish-blocked-message').text())
      .toContain('Another post is still sending.');
    expect(wrapper.get('#post-content').attributes('disabled')).toBeUndefined();
    expect(wrapper.get('#post-media-input').attributes('disabled')).toBeUndefined();
    expect(wrapper.get('.emoji-picker-trigger').attributes('disabled')).toBeUndefined();

    await wrapper.get('form').trigger('submit');
    await nextTick();

    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(draft.content).toBe('Post B');
    expect(draft.publishOperationID).toBeNull();
    expect(wrapper.get('.composer-validation-error').text())
      .toContain('Another post is still sending.');
  });

  it('reactively enables the draft after the foreign operation becomes terminal', async () => {
    const draft = usePostDraftStore();
    draft.setContent('Post B');
    const publishStore = usePostPublishStore();
    const foreignOperation = {
      id: 'publish-a',
      publisherUserID: 7,
      publisherSessionID: 'session-7',
      sourceDraftID: null,
      sourceDraftSnapshot: null,
      content: 'Post A',
      quotePostID: null,
      media: [],
      phase: 'publishing' as const,
      failureKind: null,
      error: '',
      startedAt: Date.now(),
      post: null,
    };
    publishStore.operations.push(foreignOperation);

    wrapper = mountPage();
    await nextTick();
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeDefined();

    publishStore.operations[0]!.phase = 'succeeded';
    await nextTick();

    expect(wrapper.get('.publish-button').attributes('disabled')).toBeUndefined();
    expect(wrapper.find('.publish-blocked-message').exists()).toBe(false);
    expect(wrapper.get('#post-content').attributes('disabled')).toBeUndefined();
  });

  it('explains that a failed publish attempt must be resolved before another draft can send', async () => {
    mocks.createPost.mockRejectedValueOnce(new Error('ambiguous response'));
    const draft = usePostDraftStore();
    draft.setContent('Original attempt');
    const store = usePostPublishStore();
    const first = await store.startOrRetryDraft();
    expect(first.status).toBe('accepted');
    await flushPromises();
    expect(store.latestOperation?.phase).toBe('failed');

    draft.setContent('Different post');
    wrapper = mountPage();
    await nextTick();

    expect(wrapper.get('.publish-blocked-message').text())
      .toContain('Resolve the previous post attempt before sending another post.');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(wrapper.get('.composer-validation-error').text())
      .toContain('Retry or discard the previous attempt first.');
    expect(mocks.createPost).toHaveBeenCalledTimes(1);
    expect(mocks.claimPostPublishOperation).toHaveBeenCalledTimes(1);
  });

  it('does not render the composer while logged out', () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountPage();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.get('.composer-auth-state').text()).toContain('Log in to create a post.');
  });

  it('initializes quote intent, loads a preview, and still requires commentary', async () => {
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();
    await flushPromises();

    expect(usePostDraftStore().quotePostID).toBe(42);
    expect(mocks.getPostById).toHaveBeenCalledWith(42);
    expect(wrapper.get('.composer-quote-preview').text()).toContain('Quoted post 42');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeDefined();
    expect(usePostDraftStore().hasUnsavedChanges).toBe(false);

    await wrapper.get('#post-content').setValue('My commentary');
    expect(usePostDraftStore().hasUnsavedChanges).toBe(true);
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeUndefined();
  });

  it('allows leaving a quote-only composer without opening the draft exit dialog', async () => {
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();
    await flushPromises();

    const store = usePostDraftStore();
    expect(store.quotePostID).toBe(42);
    expect(store.hasContent).toBe(false);
    expect(store.hasUnsavedChanges).toBe(false);
    expect(wrapper.get('.composer-quote-preview').text()).toContain('Quoted post 42');

    await expect(mocks.beforeRouteLeave()).resolves.toBe(true);
    expect(wrapper.find('.post-draft-exit-dialog').exists()).toBe(false);
  });

  it('asks before switching between quote targets and discards only after confirmation', async () => {
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();
    await flushPromises();
    await wrapper.get('#post-content').setValue('Unsaved commentary');

    const guardPromise = mocks.beforeRouteUpdate(
      { name: 'PostCreate', query: { quote: '43' } },
      { name: 'PostCreate', query: { quote: '42' } },
    );
    await nextTick();
    expect(wrapper.find('.post-draft-exit-dialog').exists()).toBe(true);
    await wrapper.get('.post-draft-exit-dialog__button--discard').trigger('click');
    await expect(guardPromise).resolves.toBe(true);

    Object.assign(mocks.route, {
      query: { quote: '43' },
      fullPath: '/posts/new?quote=43',
    });
    await flushPromises();
    expect(usePostDraftStore().quotePostID).toBe(43);
    expect(usePostDraftStore().content).toBe('');
  });

  it('ignores a stale quote preview response after the target changes', async () => {
    const first = deferred<Post>();
    const second = deferred<Post>();
    mocks.getPostById.mockImplementation((id: number) => id === 42 ? first.promise : second.promise);
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();
    await nextTick();

    const guardPromise = mocks.beforeRouteUpdate(
      { name: 'PostCreate', query: { quote: '43' } },
      { name: 'PostCreate', query: { quote: '42' } },
    );
    await expect(guardPromise).resolves.toBe(true);
    expect(wrapper.find('.post-draft-exit-dialog').exists()).toBe(false);
    Object.assign(mocks.route, {
      query: { quote: '43' },
      fullPath: '/posts/new?quote=43',
    });
    await nextTick();
    second.resolve(quotedPost(43));
    await flushPromises();
    first.resolve(quotedPost(42));
    await flushPromises();

    expect(usePostDraftStore().quotePostID).toBe(43);
    expect(wrapper.get('.composer-quote-preview').text()).toContain('Quoted post 43');
    expect(wrapper.get('.composer-quote-preview').text()).not.toContain('Quoted post 42');
  });

  it('blocks publishing an unavailable quote until the user removes it', async () => {
    mocks.getPostById.mockRejectedValue(new Error('not found'));
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();
    await flushPromises();
    await wrapper.get('#post-content').setValue('Keep my commentary');

    expect(wrapper.get('.composer-quote-preview__error').text()).toContain('unavailable');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeDefined();
    expect(mocks.createPost).not.toHaveBeenCalled();

    await wrapper.get('.composer-quote-preview__remove').trigger('click');
    await flushPromises();
    expect(usePostDraftStore().quotePostID).toBeNull();
    expect(wrapper.find('.composer-quote-preview').exists()).toBe(false);
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeUndefined();
    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'PostCreate', query: {} });
  });

  it('keeps the exact quote route in the composer login return target', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;
    Object.assign(mocks.route, {
      query: { quote: '42' },
      fullPath: '/posts/new?quote=42',
    });
    wrapper = mountPage();

    const loginLink = wrapper.get('.composer-auth-state a');
    expect(JSON.parse(loginLink.attributes('data-to') || '{}')).toEqual({
      name: 'Login',
      query: { returnTo: '/posts/new?quote=42' },
    });
    expect(usePostDraftStore().quotePostID).toBeNull();
    await expect(mocks.beforeRouteLeave()).resolves.toBe(true);
  });
});
