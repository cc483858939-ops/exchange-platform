// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';
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

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
}));

vi.mock('../services/articleService', () => ({
  createArticle: mocks.createArticle,
  uploadArticleCover: mocks.uploadArticleCover,
}));

const profile = ({
  id = 7,
  username = 'alice',
  displayName = 'Alice Smith',
  avatarURL = 'https://example.test/avatar.jpg',
}: {
  id?: number;
  username?: string;
  displayName?: string;
  avatarURL?: string;
} = {}): PublicUser => ({
  id,
  username,
  display_name: displayName,
  avatar_url: avatarURL,
  bio: '',
  created_at: '2026-08-21T00:00:00.000Z',
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;

  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });

  return { promise, resolve, reject };
};

const mountPage = () => mount(ArticleCreateView, {
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('ArticleCreateView author identity', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
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
  });

  it('renders auth fallback immediately while the profile is pending', () => {
    const request = deferred<PublicUser>();
    mocks.getUser.mockReturnValue(request.promise);

    wrapper = mountPage();

    expect(mocks.getUser).toHaveBeenCalledOnce();
    expect(mocks.getUser).toHaveBeenCalledWith(7);
    expect(wrapper.get('.composer-author__avatar').text()).toBe('A');
    expect(wrapper.get('.composer-author__copy strong').text()).toBe('alice');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.find('#article-content').exists()).toBe(true);
    expect(wrapper.get('label[for="article-content"]').classes()).toContain('sr-only');

    request.reject(new Error('cleanup'));
  });

  it('enriches the composer with the current profile identity and avatar', async () => {
    mocks.getUser.mockResolvedValue(profile({ avatarURL: 'https://example.test/alice.jpg' }));

    wrapper = mountPage();
    await flushPromises();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Alice Smith');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.get('.composer-author__avatar img').attributes('src')).toBe('https://example.test/alice.jpg');
  });

  it('falls back to username when the profile display name is empty', async () => {
    mocks.getUser.mockResolvedValue(profile({ displayName: '', avatarURL: '' }));

    wrapper = mountPage();
    await flushPromises();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('alice');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.get('.composer-author__avatar').text()).toBe('A');
    expect(wrapper.find('.composer-author__avatar img').exists()).toBe(false);
  });

  it('uses the initial when the profile has no avatar', async () => {
    mocks.getUser.mockResolvedValue(profile({ avatarURL: '' }));

    wrapper = mountPage();
    await flushPromises();

    expect(wrapper.find('.composer-author__avatar img').exists()).toBe(false);
    expect(wrapper.get('.composer-author__avatar').text()).toBe('A');
  });

  it('replaces a broken avatar image with the initial', async () => {
    wrapper = mountPage();
    await flushPromises();

    const image = wrapper.get('.composer-author__avatar img');
    await image.trigger('error');

    expect(wrapper.find('.composer-author__avatar img').exists()).toBe(false);
    expect(wrapper.get('.composer-author__avatar').text()).toBe('A');
  });

  it('keeps the fallback identity and publish available when profile loading fails', async () => {
    mocks.getUser.mockRejectedValue(new Error('profile unavailable'));

    wrapper = mountPage();
    await flushPromises();
    await wrapper.get('#article-content').setValue('A post without profile enrichment');

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('alice');
    expect(wrapper.get('.publish-button').attributes('disabled')).toBeUndefined();

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createArticle).toHaveBeenCalledOnce();
  });

  it('does not wait for profile loading before publishing', async () => {
    const request = deferred<PublicUser>();
    mocks.getUser.mockReturnValue(request.promise);

    wrapper = mountPage();
    await wrapper.get('#article-content').setValue('A post while profile is pending');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createArticle).toHaveBeenCalledOnce();

    request.reject(new Error('cleanup'));
  });

  it('does not request a profile for an unauthenticated composer', () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountPage();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.get('.composer-auth-state').text()).toContain('Log in to create a post.');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });

  it('ignores a profile response for another user', async () => {
    mocks.getUser.mockResolvedValue(profile({
      id: 99,
      username: 'wrong-user',
      displayName: 'Wrong User',
      avatarURL: '/wrong.jpg',
    }));

    wrapper = mountPage();
    await flushPromises();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('alice');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.find('.composer-author__avatar img').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Wrong User');
    expect(wrapper.html()).not.toContain('/wrong.jpg');
  });

  it('keeps the newest account after an out-of-order profile response', async () => {
    const accountA = deferred<PublicUser>();
    const accountB = deferred<PublicUser>();
    mocks.getUser.mockImplementation((userID: number) => (
      userID === 7 ? accountA.promise : accountB.promise
    ));

    wrapper = mountPage();
    expect(mocks.getUser).toHaveBeenCalledWith(7);

    mocks.authStore!.currentIdentity = { id: 8, username: 'bob' };
    await nextTick();

    expect(mocks.getUser).toHaveBeenCalledWith(8);
    expect(mocks.getUser).toHaveBeenCalledTimes(2);

    accountB.resolve(profile({
      id: 8,
      username: 'bob',
      displayName: 'Bob Jones',
      avatarURL: 'https://example.test/bob.jpg',
    }));
    await flushPromises();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Bob Jones');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@bob');
    expect(wrapper.get('.composer-author__avatar img').attributes('src')).toBe('https://example.test/bob.jpg');

    accountA.resolve(profile({ avatarURL: 'https://example.test/alice.jpg' }));
    await flushPromises();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Bob Jones');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@bob');
    expect(wrapper.html()).not.toContain('alice.jpg');
  });

  it('resets avatar failure when the account changes', async () => {
    mocks.getUser.mockImplementation((userID: number) => Promise.resolve(profile({
      id: userID,
      username: userID === 7 ? 'alice' : 'bob',
      displayName: userID === 7 ? 'Alice Smith' : 'Bob Jones',
      avatarURL: userID === 7 ? 'https://example.test/alice.jpg' : 'https://example.test/bob.jpg',
    })));

    wrapper = mountPage();
    await flushPromises();
    await wrapper.get('.composer-author__avatar img').trigger('error');
    expect(wrapper.find('.composer-author__avatar img').exists()).toBe(false);

    mocks.authStore!.currentIdentity = { id: 8, username: 'bob' };
    await nextTick();
    await flushPromises();

    expect(mocks.getUser).toHaveBeenNthCalledWith(2, 8);
    expect(wrapper.get('.composer-author__avatar img').attributes('src')).toBe('https://example.test/bob.jpg');
  });
});
