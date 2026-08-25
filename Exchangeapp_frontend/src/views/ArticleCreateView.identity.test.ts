// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
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
      avatar_url: 'https://example.test/alice.jpg',
    } as {
      id: number;
      username: string;
      display_name: string;
      avatar_url: string;
    } | null,
  },
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
  mocks.authStore = reactive({
    ...mocks.authState,
    syncCurrentIdentityProfile: vi.fn(),
  });
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

const identity = (overrides: Partial<NonNullable<typeof mocks.authState.currentIdentity>> = {}) => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice Smith',
  avatar_url: 'https://example.test/alice.jpg',
  ...overrides,
});

const publishedArticle = (authorID = 7) => ({
  id: 101,
  author: {
    id: authorID,
    username: authorID === 7 ? 'alice' : 'bob',
    display_name: authorID === 7 ? 'Alice Smith' : 'Bob Jones',
    avatar_url: authorID === 7 ? 'https://example.test/alice.jpg' : '',
  },
});

const mountPage = () => mount(ArticleCreateView, {
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('ArticleCreateView current identity fast path', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = identity();
    mocks.createArticle.mockResolvedValue(publishedArticle());
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
  });

  it('renders the canonical identity and avatar without waiting for a profile request', () => {
    wrapper = mountPage();

    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Alice Smith');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.get('.composer-author__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
    expect(wrapper.get('.composer-author__avatar').attributes('aria-hidden')).toBe('true');
  });

  it('reacts to a current identity change and isolates the draft viewer', async () => {
    wrapper = mountPage();
    await wrapper.get('#article-content').setValue('Alice draft');

    mocks.authStore!.currentIdentity = identity({
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: 'https://example.test/bob.jpg',
    });
    await nextTick();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Bob Jones');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@bob');
    expect(wrapper.get('.composer-author__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/bob.jpg');
    expect(useArticleDraftStore().viewerID).toBe(8);
    expect(useArticleDraftStore().content).toBe('');
  });

  it('publishes from the fast-path identity without profile enrichment', async () => {
    wrapper = mountPage();
    await wrapper.get('#article-content').setValue('A post from the current identity');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(mocks.createArticle).toHaveBeenCalledWith({
      title: '',
      preview: '',
      content: 'A post from the current identity',
    });
    expect(mocks.feedStore.registerPublishedArticle).toHaveBeenCalledWith(
      publishedArticle(),
      7,
    );
    expect(mocks.authStore!.syncCurrentIdentityProfile).toHaveBeenCalledWith(
      publishedArticle().author,
    );
    expect(useArticleDraftStore().dirty).toBe(false);
  });

  it('does not render the composer or request a profile while logged out', () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountPage();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.get('.composer-auth-state').text()).toContain('Log in to create a post.');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });
});
