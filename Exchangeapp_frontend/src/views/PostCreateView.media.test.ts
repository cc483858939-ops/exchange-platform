// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';

const mocks = vi.hoisted(() => ({
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
      avatar_url: '',
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

const publishedPost = () => ({
  id: 101,
  author: {
    id: 7,
    username: 'alice',
    display_name: 'Alice Smith',
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

const selectFiles = async (wrapper: VueWrapper, files: File[]) => {
  const input = wrapper.get('#post-media-input');
  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: files,
  });
  await input.trigger('change');
  return input;
};

const imageFile = (name: string, type = 'image/png', bytes = 'image-bytes') => (
  new File([bytes], name, { type })
);

describe('PostCreateView media picker and retry behavior', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn((file: File) => `blob:${file.name}`),
      revokeObjectURL: vi.fn(),
    });
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.currentIdentity = {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: '',
    };
    mocks.createPost.mockResolvedValue(publishedPost());
    mocks.uploadPostMedia.mockImplementation(async (file: File) => `/media/${file.name}`);
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
    vi.unstubAllGlobals();
  });

  it('accepts up to four valid images in request order', async () => {
    wrapper = mountPage();
    const files = [1, 2, 3, 4].map(index => imageFile(`image-${index}.png`));
    await selectFiles(wrapper, files);

    expect(usePostDraftStore().media.map(item => item.file)).toEqual(files);
    expect(wrapper.find('.post-media-grid--count-4').exists()).toBe(true);
    expect(wrapper.findAll('.post-media-grid__remove')).toHaveLength(4);
    expect(wrapper.get('.post-media-grid__remove').attributes('aria-label')).toBe('Remove image 1');
  });

  it('keeps existing images and reports overflow without clearing them', async () => {
    wrapper = mountPage();
    const existing = [1, 2, 3].map(index => imageFile(`existing-${index}.png`));
    await selectFiles(wrapper, existing);
    await selectFiles(wrapper, [imageFile('accepted.png'), imageFile('overflow.png')]);

    expect(usePostDraftStore().media).toHaveLength(4);
    expect(usePostDraftStore().media[3].file.name).toBe('accepted.png');
    expect(wrapper.get('.media-error').text()).toContain('up to 4 images');
  });

  it('rejects unsupported, empty, and oversized files', async () => {
    wrapper = mountPage();

    await selectFiles(wrapper, [imageFile('notes.txt', 'text/plain')]);
    expect(usePostDraftStore().media).toHaveLength(0);
    expect(wrapper.get('.media-error').text()).toContain('JPEG, PNG, or WebP');

    await selectFiles(wrapper, [imageFile('empty.png', 'image/png', '')]);
    expect(usePostDraftStore().media).toHaveLength(0);
    expect(wrapper.get('.media-error').text()).toContain('non-empty');

    const tooLarge = new File([new Uint8Array(5 * 1024 * 1024 + 1)], 'large.png', {
      type: 'image/png',
    });
    await selectFiles(wrapper, [tooLarge]);
    expect(usePostDraftStore().media).toHaveLength(0);
    expect(wrapper.get('.media-error').text()).toContain('5 MB or smaller');
  });

  it('removes media by stable identity and revokes its preview URL', async () => {
    wrapper = mountPage();
    await selectFiles(wrapper, [imageFile('remove-me.png')]);

    await wrapper.get('.post-media-grid__remove').trigger('click');

    expect(usePostDraftStore().media).toHaveLength(0);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:remove-me.png');
  });

  it('preserves successful uploads and resumes after a partial upload failure', async () => {
    const first = imageFile('first.png');
    const second = imageFile('second.png');
    mocks.uploadPostMedia
      .mockResolvedValueOnce('/api/files/post-media/7/first.jpg')
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValueOnce('/api/files/post-media/7/second.jpg');
    wrapper = mountPage();
    await selectFiles(wrapper, [first, second]);
    await wrapper.get('#post-content').setValue('Retry this post');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(usePostDraftStore().media[0].uploadedURL)
      .toBe('/api/files/post-media/7/first.jpg');
    expect(usePostDraftStore().media[1].uploadedURL).toBe('');
    expect(wrapper.get('.composer-status').text()).toContain('Image upload failed');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(3);
    expect(mocks.uploadPostMedia).toHaveBeenLastCalledWith(second);
    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'Retry this post',
      media: [
        { type: 'image', url: '/api/files/post-media/7/first.jpg' },
        { type: 'image', url: '/api/files/post-media/7/second.jpg' },
      ],
    });
  });

  it('reuses uploaded URLs when createPost fails and is retried', async () => {
    const file = imageFile('retry.png');
    mocks.createPost
      .mockRejectedValueOnce(new Error('create failed'))
      .mockResolvedValueOnce(publishedPost());
    wrapper = mountPage();
    await selectFiles(wrapper, [file]);
    await wrapper.get('#post-content').setValue('Retry create');

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(1);
    expect(usePostDraftStore().media[0].uploadedURL).toBe('/media/retry.png');
    expect(usePostDraftStore().content).toBe('Retry create');

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(2);
  });
});
