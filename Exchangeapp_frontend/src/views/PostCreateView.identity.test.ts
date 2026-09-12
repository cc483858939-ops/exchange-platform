// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { defineComponent, h, nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';
import { usePostPublishStore } from '../store/postPublish';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: {
      id: number;
      username: string;
      display_name: string;
      avatar_url: string;
    } | null;
    syncCurrentIdentityProfile: ReturnType<typeof vi.fn>;
  } | null,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  feedStore: { registerPublishedPost: vi.fn() },
  profileSessionStore: { registerPublishedTimelinePost: vi.fn() },
  createPost: vi.fn(),
  uploadPostMedia: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', async () => {
  const { reactive } = await import('vue');
  mocks.authStore = reactive({
    isAuthenticated: true,
    currentIdentity: {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: 'https://example.test/alice.jpg',
    },
    syncCurrentIdentityProfile: vi.fn(),
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
  uploadPostMedia: mocks.uploadPostMedia,
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
      RouterLink: { template: '<a><slot /></a>' },
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
    mocks.authStore!.currentIdentity = identity();
    mocks.createPost.mockResolvedValue(publishedPost());
    mocks.feedStore.registerPublishedPost.mockReturnValue(true);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);

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
      { idempotencyKey: expect.any(String) },
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
      { idempotencyKey: expect.any(String) },
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

  it('does not render the composer while logged out', () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountPage();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.get('.composer-auth-state').text()).toContain('Log in to create a post.');
  });
});
