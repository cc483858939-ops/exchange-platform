// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ArticleCreateView from './ArticleCreateView.vue';
import type { PublicUser } from '../types/User';

const mocks = vi.hoisted(() => ({
  authState: {
    isAuthenticated: true,
    currentIdentity: { id: 7, username: 'alice' } as { id: number; username: string } | null,
  },
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number; username: string } | null;
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
  getUser: vi.fn(),
  createArticle: vi.fn(),
  uploadArticleCover: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', async () => {
  const { reactive } = await import('vue');
  mocks.authStore = reactive(mocks.authState);
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

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
}));

vi.mock('../services/articleService', () => ({
  createArticle: mocks.createArticle,
  uploadArticleCover: mocks.uploadArticleCover,
}));

const profile = (): PublicUser => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice Smith',
  avatar_url: '',
  bio: '',
  created_at: '2026-08-21T00:00:00.000Z',
});

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
  const input = wrapper.get('#article-cover-input');
  const file = new File(['image-bytes'], 'cover.png', { type: 'image/png' });

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
    vi.clearAllMocks();
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn().mockReturnValue('blob:cover-preview'),
      revokeObjectURL: vi.fn(),
    });
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = { id: 7, username: 'alice' };
    mocks.getUser.mockResolvedValue(profile());
    mocks.createArticle.mockResolvedValue({ id: 101, author: { id: 7 } });
    mocks.uploadArticleCover.mockResolvedValue('https://example.test/cover.jpg');
    mocks.feedStore.registerPublishedArticle.mockReturnValue(true);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
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
});
