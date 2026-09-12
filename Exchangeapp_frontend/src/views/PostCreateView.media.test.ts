// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostCreateView from './PostCreateView.vue';
import { usePostDraftStore } from '../store/postDraft';
import { usePostPublishStore } from '../store/postPublish';

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

const deferred = <T,>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

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

  it('keeps the image picker icon-only with an accessible name', () => {
    wrapper = mountPage();

    const picker = wrapper.get('.media-picker');
    expect(picker.attributes('aria-label')).toBe('Add images');
    expect(picker.attributes('title')).toBe('Add images');
    expect(picker.text()).not.toContain('Add images');
    expect(wrapper.find('#post-media-input').exists()).toBe(true);
    expect(wrapper.get('#post-media-input').attributes('multiple')).toBeDefined();
    expect(wrapper.get('#post-media-input').attributes('accept'))
      .toBe('image/jpeg,image/png,image/webp');
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

  it('uploads media two at a time and keeps selected order despite completion order', async () => {
    const files = ['a.png', 'b.png', 'c.png', 'd.png'].map(name => imageFile(name));
    const requests = new Map(files.map(file => [file.name, deferred<string>()]));
    let activeUploads = 0;
    let maxActiveUploads = 0;
    mocks.uploadPostMedia.mockImplementation((file: File) => {
      activeUploads += 1;
      maxActiveUploads = Math.max(maxActiveUploads, activeUploads);
      const request = requests.get(file.name);
      if (!request) {
        throw new Error(`Unexpected upload for ${file.name}.`);
      }
      return request.promise.finally(() => {
        activeUploads -= 1;
      });
    });
    wrapper = mountPage();
    await selectFiles(wrapper, files);
    await wrapper.get('#post-content').setValue('Upload four images');

    const submitPromise = wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadPostMedia.mock.calls.map(([file]) => file.name)).toEqual(['a.png', 'b.png']);
    expect(maxActiveUploads).toBe(2);

    requests.get('b.png')!.resolve('/media/b.png');
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(2);

    requests.get('a.png')!.resolve('/media/a.png');
    await flushPromises();
    expect(mocks.uploadPostMedia.mock.calls.map(([file]) => file.name)).toEqual([
      'a.png',
      'b.png',
      'c.png',
      'd.png',
    ]);
    expect(maxActiveUploads).toBe(2);

    requests.get('d.png')!.resolve('/media/d.png');
    await flushPromises();
    expect(mocks.createPost).not.toHaveBeenCalled();

    requests.get('c.png')!.resolve('/media/c.png');
    await submitPromise;
    await flushPromises();

    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Upload four images',
        media: [
          { type: 'image', url: '/media/a.png' },
          { type: 'image', url: '/media/b.png' },
          { type: 'image', url: '/media/c.png' },
          { type: 'image', url: '/media/d.png' },
        ],
      },
      { idempotencyKey: expect.any(String) },
    );
  });

  it('does not spend upload slots on cached media and preserves selected order', async () => {
    const files = ['cached.png', 'second.png', 'third.png', 'fourth.png'].map(name => imageFile(name));
    const uploadOrder: string[] = [];
    mocks.uploadPostMedia.mockImplementation(async (file: File) => {
      uploadOrder.push(file.name);
      return `/media/${file.name}`;
    });
    wrapper = mountPage();
    await selectFiles(wrapper, files);
    const draft = usePostDraftStore();
    draft.setUploadedURL(draft.media[0].id, '/media/cached.png');
    await wrapper.get('#post-content').setValue('Reuse cached media');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(uploadOrder).toEqual(['second.png', 'third.png', 'fourth.png']);
    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Reuse cached media',
        media: [
          { type: 'image', url: '/media/cached.png' },
          { type: 'image', url: '/media/second.png' },
          { type: 'image', url: '/media/third.png' },
          { type: 'image', url: '/media/fourth.png' },
        ],
      },
      { idempotencyKey: expect.any(String) },
    );
  });

  it('preserves successful uploads and retries only media still pending', async () => {
    const files = ['first.png', 'second.png', 'third.png', 'fourth.png'].map(name => imageFile(name));
    mocks.uploadPostMedia.mockImplementation(async (file: File) => `/media/${file.name}`);
    mocks.uploadPostMedia.mockResolvedValueOnce('/media/first.png');
    mocks.uploadPostMedia.mockRejectedValueOnce(new Error('temporary failure'));
    wrapper = mountPage();
    await selectFiles(wrapper, files);
    await wrapper.get('#post-content').setValue('Retry pending images');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.uploadPostMedia.mock.calls.map(([file]) => file.name)).toEqual([
      'first.png',
      'second.png',
    ]);
    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(usePostDraftStore().media.map(item => item.uploadedURL)).toEqual([
      '/media/first.png',
      '',
      '',
      '',
    ]);
    await vi.waitFor(() => {
      expect(usePostPublishStore().latestOperation?.phase).toBe('failed');
    });

    const operation = usePostPublishStore().latestOperation;
    expect(operation).not.toBeNull();
    expect(usePostPublishStore().retry(operation!.id)).toBe(true);
    await flushPromises();

    expect(mocks.uploadPostMedia.mock.calls.map(([file]) => file.name)).toEqual([
      'first.png',
      'second.png',
      'second.png',
      'third.png',
      'fourth.png',
    ]);
    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Retry pending images',
        media: [
          { type: 'image', url: '/media/first.png' },
          { type: 'image', url: '/media/second.png' },
          { type: 'image', url: '/media/third.png' },
          { type: 'image', url: '/media/fourth.png' },
        ],
      },
      { idempotencyKey: expect.any(String) },
    );
  });

  it('preserves sibling successes when an upload returns an empty URL', async () => {
    const first = imageFile('empty-url.png');
    const second = imageFile('valid-url.png');
    mocks.uploadPostMedia
      .mockResolvedValueOnce('   ')
      .mockResolvedValueOnce('/media/valid-url.png');
    wrapper = mountPage();
    await selectFiles(wrapper, [first, second]);
    await wrapper.get('#post-content').setValue('Keep the valid upload');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(mocks.createPost).not.toHaveBeenCalled();
    expect(usePostDraftStore().media.map(item => item.uploadedURL)).toEqual([
      '',
      '/media/valid-url.png',
    ]);
    const publishStore = usePostPublishStore();
    expect(publishStore.latestOperation?.phase).toBe('failed');
    expect(publishStore.latestOperation?.error).toContain('Couldn’t confirm this post');
  });

  it('keeps the background operation running when the authenticated account changes', async () => {
    const files = ['old-a.png', 'old-b.png', 'old-c.png', 'old-d.png'].map(name => imageFile(name));
    const requests = new Map(files.map(file => [file.name, deferred<string>()]));
    mocks.uploadPostMedia.mockImplementation((file: File) => {
      const request = requests.get(file.name);
      if (!request) {
        throw new Error(`Unexpected upload for ${file.name}.`);
      }
      return request.promise;
    });
    wrapper = mountPage();
    await selectFiles(wrapper, files);
    await wrapper.get('#post-content').setValue('Old account draft');

    const submitPromise = wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(2);

    mocks.authStore!.currentIdentity = {
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: '',
    };
    await flushPromises();
    requests.get('old-a.png')!.resolve('/media/old-a.png');
    requests.get('old-b.png')!.resolve('/media/old-b.png');
    await flushPromises();
    requests.get('old-c.png')!.resolve('/media/old-c.png');
    requests.get('old-d.png')!.resolve('/media/old-d.png');
    await submitPromise;
    await flushPromises();

    expect(mocks.uploadPostMedia.mock.calls.map(([file]) => file.name)).toEqual([
      'old-a.png',
      'old-b.png',
      'old-c.png',
      'old-d.png',
    ]);
    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Old account draft',
        media: [
          { type: 'image', url: '/media/old-a.png' },
          { type: 'image', url: '/media/old-b.png' },
          { type: 'image', url: '/media/old-c.png' },
          { type: 'image', url: '/media/old-d.png' },
        ],
      },
      { idempotencyKey: expect.any(String) },
    );
    expect(mocks.feedStore.registerPublishedPost).not.toHaveBeenCalled();
    expect(mocks.profileSessionStore.registerPublishedTimelinePost).not.toHaveBeenCalled();
    expect(usePostDraftStore().viewerID).toBe(8);
    expect(usePostDraftStore().media).toHaveLength(0);
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
    expect(usePostPublishStore().latestOperation?.phase).toBe('failed');
    await vi.waitFor(() => {
      expect(usePostPublishStore().latestOperation?.phase).toBe('failed');
    });

    const operation = usePostPublishStore().latestOperation;
    expect(operation).not.toBeNull();
    expect(usePostPublishStore().retry(operation!.id)).toBe(true);
    await flushPromises();

    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(3);
    expect(mocks.uploadPostMedia).toHaveBeenLastCalledWith(second);
    expect(mocks.createPost).toHaveBeenCalledWith(
      {
        content: 'Retry this post',
        media: [
          { type: 'image', url: '/api/files/post-media/7/first.jpg' },
          { type: 'image', url: '/api/files/post-media/7/second.jpg' },
        ],
      },
      { idempotencyKey: expect.any(String) },
    );
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
    await vi.waitFor(() => {
      expect(usePostPublishStore().latestOperation?.phase).toBe('failed');
    });
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(1);
    expect(usePostDraftStore().media[0].uploadedURL).toBe('/media/retry.png');
    expect(usePostDraftStore().content).toBe('Retry create');

    const operation = usePostPublishStore().latestOperation;
    expect(operation).not.toBeNull();
    expect(usePostPublishStore().retry(operation!.id)).toBe(true);
    await flushPromises();
    expect(mocks.uploadPostMedia).toHaveBeenCalledTimes(1);
    expect(mocks.createPost).toHaveBeenCalledTimes(2);
  });
});
