// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';

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
  profileSessionStore: { registerPublishedPost: vi.fn() },
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

const mountPage = () => mount(PostCreateView, {
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

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

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Alice Smith');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@alice');
    expect(wrapper.get('.composer-author__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
  });

  it('clears the draft when the authenticated viewer changes', async () => {
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('Alice draft');

    mocks.authStore!.currentIdentity = identity(8);
    await nextTick();

    expect(wrapper.get('.composer-author__copy strong').text()).toBe('Bob Jones');
    expect(wrapper.get('.composer-author__copy small').text()).toBe('@bob');
    expect(usePostDraftStore().viewerID).toBe(8);
    expect(usePostDraftStore().content).toBe('');
  });

  it('publishes a text-only Post and clears the draft after success', async () => {
    wrapper = mountPage();
    await wrapper.get('#post-content').setValue('A post from the current identity');
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'A post from the current identity',
      media: [],
    });
    expect(mocks.feedStore.registerPublishedPost).toHaveBeenCalledWith(
      publishedPost(),
      7,
    );
    expect(mocks.profileSessionStore.registerPublishedPost).toHaveBeenCalledWith(
      publishedPost(),
      7,
    );
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'Home',
      query: { tab: 'for-you' },
    });
    expect(usePostDraftStore().dirty).toBe(false);
  });

  it('does not render the composer while logged out', () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountPage();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.get('.composer-auth-state').text()).toContain('Log in to create a post.');
  });
});
