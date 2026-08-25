// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ArticleCreateView from './ArticleCreateView.vue';
import { useArticleDraftStore } from '../store/articleDraft';

const mocks = vi.hoisted(() => ({
  authState: {
    isAuthenticated: true,
    currentIdentity: {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: '',
    } as { id: number; username: string; display_name: string; avatar_url: string } | null,
  },
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number; username: string; display_name: string; avatar_url: string } | null;
    syncCurrentIdentityProfile: ReturnType<typeof vi.fn>;
  } | null,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  feedStore: {
    registerPublishedArticle: vi.fn(),
  },
  profileSessionStore: {
    registerPublishedArticle: vi.fn(),
  },
  createArticle: vi.fn(),
  uploadArticleCover: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', async () => {
  const { reactive } = await import('vue');
  mocks.authStore = reactive({ ...mocks.authState, syncCurrentIdentityProfile: vi.fn() });
  return {
    useAuthStore: () => mocks.authStore,
  };
});

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/profileSession', () => ({
  useProfileSessionStore: () => mocks.profileSessionStore,
}));

vi.mock('../services/articleService', () => ({
  createArticle: mocks.createArticle,
  uploadArticleCover: mocks.uploadArticleCover,
}));

const deferred = <T>() => {
  let resolve!: (value: T) => void;

  const promise = new Promise<T>(res => {
    resolve = res;
  });

  return { promise, resolve };
};

const mountPage = () => mount(ArticleCreateView, {
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const selectValidCover = async (wrapper: VueWrapper) => {
  const file = new File(['image-bytes'], 'cover.png', { type: 'image/png' });
  return selectCover(wrapper, file);
};

const selectCover = async (wrapper: VueWrapper, file: File) => {
  const input = wrapper.get('#article-cover-input');

  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: [file],
  });
  await input.trigger('change');

  return input;
};

describe('ArticleCreateView cover picker', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn().mockReturnValue('blob:cover-preview'),
      revokeObjectURL: vi.fn(),
    });
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: '',
    };
    mocks.createArticle.mockResolvedValue({ id: 101, author: { id: 7 } });
    mocks.uploadArticleCover.mockResolvedValue('https://example.test/cover.jpg');
    mocks.feedStore.registerPublishedArticle.mockReturnValue(true);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
    const draft = useArticleDraftStore();
    draft.clear();
    draft.setViewer(7);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.unstubAllGlobals();
  });

  it('opens the existing file input without submitting the form', async () => {
    wrapper = mountPage();

    const input = wrapper.get('#article-cover-input');
    const inputClick = vi.spyOn(input.element as HTMLInputElement, 'click');
    const trigger = wrapper.get('.cover-preview-trigger');

    expect(trigger.element.tagName).toBe('BUTTON');
    expect(trigger.attributes('type')).toBe('button');
    expect(trigger.attributes('aria-label')).toBe('Add cover image');
    expect(wrapper.findAll('input[type="file"]')).toHaveLength(1);

    await trigger.trigger('click');

    expect(inputClick).toHaveBeenCalledOnce();
    expect(mocks.createArticle).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
  });

  it('disables the empty trigger while publishing', async () => {
    const publish = deferred<{ id: number; author: { id: number } }>();
    mocks.createArticle.mockReturnValue(publish.promise);
    wrapper = mountPage();

    await wrapper.get('#article-content').setValue('A post in progress');
    await wrapper.get('form').trigger('submit');

    const input = wrapper.get('#article-cover-input');
    const inputClick = vi.spyOn(input.element as HTMLInputElement, 'click');
    const trigger = wrapper.get('.cover-preview-trigger');

    expect((trigger.element as HTMLButtonElement).disabled).toBe(true);
    await trigger.trigger('click');
    expect(inputClick).not.toHaveBeenCalled();

    publish.resolve({ id: 101, author: { id: 7 } });
    await flushPromises();
  });

  it('uses the existing input pipeline and keeps selected preview passive', async () => {
    wrapper = mountPage();
    const input = await selectValidCover(wrapper);

    expect(wrapper.find('.cover-preview-trigger').exists()).toBe(false);
    expect(wrapper.get('.cover-preview-frame').element.tagName).toBe('FIGURE');
    expect(wrapper.get('.cover-preview-frame img').attributes('src')).toBe('blob:cover-preview');
    expect(wrapper.get('label[for="article-cover-input"]').text()).toContain('Replace cover');
    expect(wrapper.get('button.composer-action--secondary').text()).toContain('Remove');

    const inputClick = vi.spyOn(input.element as HTMLInputElement, 'click');
    await wrapper.get('.cover-preview-frame').trigger('click');

    expect(inputClick).not.toHaveBeenCalled();
    expect(wrapper.find('button.cover-preview-frame').exists()).toBe(false);
  });

  it('restores the same clickable trigger after Remove', async () => {
    wrapper = mountPage();
    const input = await selectValidCover(wrapper);
    const inputClick = vi.spyOn(input.element as HTMLInputElement, 'click');

    await wrapper.get('button.composer-action--secondary').trigger('click');

    expect(wrapper.find('.cover-preview-frame img').exists()).toBe(false);
    expect(wrapper.get('.cover-preview-trigger').text()).toContain('No cover selected');
    expect(wrapper.get('label[for="article-cover-input"]').text()).toContain('Add cover');

    await wrapper.get('.cover-preview-trigger').trigger('click');
    expect(inputClick).toHaveBeenCalledOnce();
  });

  it('restores text and the File across a same-viewer remount with a fresh object URL', async () => {
    const createObjectURL = vi.mocked(URL.createObjectURL);
    createObjectURL
      .mockReturnValueOnce('blob:first-preview')
      .mockReturnValueOnce('blob:second-preview');
    wrapper = mountPage();
    const file = new File(['image-bytes'], 'cover.png', { type: 'image/png' });
    await wrapper.get('#article-content').setValue('Draft survives route navigation');
    await selectCover(wrapper, file);

    expect(wrapper.get('.cover-preview-frame img').attributes('src')).toBe('blob:first-preview');
    wrapper.unmount();
    wrapper = null;
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:first-preview');

    wrapper = mountPage();
    expect(wrapper.get('#article-content').element).toHaveProperty('value', 'Draft survives route navigation');
    expect(wrapper.get('.cover-preview-frame img').attributes('src')).toBe('blob:second-preview');
    expect(useArticleDraftStore().coverFile).toBe(file);
    expect(createObjectURL).toHaveBeenCalledTimes(2);
  });

  it('keeps an existing valid cover when a replacement is invalid', async () => {
    wrapper = mountPage();
    const validFile = new File(['valid'], 'valid.png', { type: 'image/png' });
    await selectCover(wrapper, validFile);
    useArticleDraftStore().setUploadedCoverURL('https://example.test/already-uploaded.jpg');

    const invalidFile = new File(['text'], 'notes.txt', { type: 'text/plain' });
    await selectCover(wrapper, invalidFile);

    expect(useArticleDraftStore().coverFile).toBe(validFile);
    expect(useArticleDraftStore().uploadedCoverURL).toBe('https://example.test/already-uploaded.jpg');
    expect(wrapper.get('.cover-preview-frame img').attributes('src')).toBe('blob:cover-preview');
    expect(wrapper.get('.cover-error').text()).toContain('JPEG, PNG, or WebP');
  });

  it('preserves the draft and returns to idle when cover upload fails', async () => {
    mocks.uploadArticleCover.mockRejectedValueOnce(new Error('upload failed'));
    wrapper = mountPage();
    const file = new File(['valid'], 'cover.png', { type: 'image/png' });
    await selectCover(wrapper, file);
    await wrapper.get('#article-content').setValue('Keep this after upload failure');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.uploadArticleCover).toHaveBeenCalledWith(file);
    expect(mocks.createArticle).not.toHaveBeenCalled();
    expect(useArticleDraftStore().content).toBe('Keep this after upload failure');
    expect(useArticleDraftStore().coverFile).toBe(file);
    expect(useArticleDraftStore().uploadedCoverURL).toBe('');
    expect(wrapper.get('.composer-status').text()).toContain('Cover upload failed');
  });

  it('reuses a successful upload URL when create fails and the user retries', async () => {
    mocks.createArticle
      .mockRejectedValueOnce(new Error('create failed'))
      .mockResolvedValueOnce({
        id: 101,
        author: { id: 7, username: 'alice', display_name: 'Alice Smith', avatar_url: '' },
      });
    wrapper = mountPage();
    const file = new File(['valid'], 'cover.png', { type: 'image/png' });
    await selectCover(wrapper, file);
    await wrapper.get('#article-content').setValue('Retry with the same uploaded cover');

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadArticleCover).toHaveBeenCalledTimes(1);
    expect(useArticleDraftStore().uploadedCoverURL).toBe('https://example.test/cover.jpg');
    expect(useArticleDraftStore().content).toBe('Retry with the same uploaded cover');

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadArticleCover).toHaveBeenCalledTimes(1);
    expect(mocks.createArticle).toHaveBeenLastCalledWith({
      title: '',
      preview: '',
      content: 'Retry with the same uploaded cover',
      cover_image_url: 'https://example.test/cover.jpg',
    });
  });

  it('clears the draft only after a successful publish', async () => {
    wrapper = mountPage();
    const file = new File(['valid'], 'cover.png', { type: 'image/png' });
    await selectCover(wrapper, file);
    await wrapper.get('#article-content').setValue('Successful post');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    const draft = useArticleDraftStore();
    expect(draft.content).toBe('');
    expect(draft.coverFile).toBeNull();
    expect(draft.uploadedCoverURL).toBe('');
    expect(draft.dirty).toBe(false);
    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'Home', query: { tab: 'for-you' } });
  });

  it('ignores a stale upload result after the viewer changes', async () => {
    const upload = deferred<string>();
    mocks.uploadArticleCover.mockReturnValue(upload.promise);
    wrapper = mountPage();
    const file = new File(['valid'], 'cover.png', { type: 'image/png' });
    await selectCover(wrapper, file);
    await wrapper.get('#article-content').setValue('Viewer-bound draft');
    await wrapper.get('form').trigger('submit');

    mocks.authStore!.currentIdentity = {
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: '',
    };
    await flushPromises();
    expect(useArticleDraftStore().viewerID).toBe(8);
    expect(useArticleDraftStore().content).toBe('');

    upload.resolve('https://example.test/stale-cover.jpg');
    await flushPromises();

    expect(mocks.createArticle).not.toHaveBeenCalled();
    expect(mocks.feedStore.registerPublishedArticle).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(useArticleDraftStore().uploadedCoverURL).toBe('');
  });
});
