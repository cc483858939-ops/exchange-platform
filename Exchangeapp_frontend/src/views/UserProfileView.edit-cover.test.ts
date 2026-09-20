// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import UserProfileView from './UserProfileView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  getUser: vi.fn(),
  getUserTimeline: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadProfileAvatar: vi.fn(),
  uploadProfileCover: vi.fn(),
  deletePost: vi.fn(),
  getPostLikeStates: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostStates: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  feedStore: {
    isPostDeleted: vi.fn(),
    markPostDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
    applyLikeStateUpdate: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: vi.fn(),
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserTimeline: mocks.getUserTimeline,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
  uploadProfileCover: mocks.uploadProfileCover,
}));

vi.mock('../services/postService', () => ({
  deletePost: mocks.deletePost,
}));

vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));

vi.mock('../services/repostService', () => ({
  getPostRepostStates: mocks.getPostRepostStates,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
const originalCreateObjectURL = URL.createObjectURL;
const originalRevokeObjectURL = URL.revokeObjectURL;

beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute('open');
      this.dispatchEvent(new Event('close'));
    },
  });
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: vi.fn((file: File) => `blob:${file.name}`),
  });
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: vi.fn(),
  });
});

afterAll(() => {
  if (originalShowModal) {
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
  if (originalClose) {
    Object.defineProperty(HTMLDialogElement.prototype, 'close', originalClose);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
  }
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: originalCreateObjectURL,
    });
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL');
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: originalRevokeObjectURL,
    });
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL');
  }
});

const profile = (id: number, coverImageURL = '') => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  cover_image_url: coverImageURL,
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
  follower_count: 0,
  following_count: 0,
});

const PostCardStub = {
  props: ['post', 'showDelete'],
  template: '<article class="post-card">{{ post.content }}</article>',
};

const mountProfile = () => mount(UserProfileView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      MobileAccountMenu: { template: '<span />' },
      PostCard: PostCardStub,
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const setCoverFile = async (wrapper: VueWrapper, file: File) => {
  const input = wrapper.get('#profile-cover-input');
  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: [file],
  });
  await input.trigger('change');
};

const setAvatarFile = async (wrapper: VueWrapper, file: File) => {
  const input = wrapper.get('#profile-avatar-input');
  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: [file],
  });
  await input.trigger('change');
};

const openEditor = async (wrapper: VueWrapper) => {
  const button = wrapper.findAll('button').find((item) => item.text() === 'Edit profile');
  if (!button) throw new Error('Edit profile button not found');
  await button.trigger('click');
  await nextTick();
};

const submitEditor = async (wrapper: VueWrapper) => {
  await wrapper.get('.profile-edit-form').trigger('submit');
  await settle();
};

describe('UserProfileView profile cover editor', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.route = reactive({
      name: 'UserProfile',
      params: { id: '7' },
      fullPath: '/users/7',
    });
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { ...profile(7) },
      syncCurrentIdentityProfile: vi.fn(),
    });
    mocks.getUser.mockResolvedValue(profile(7));
    mocks.getUserTimeline.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
    mocks.updateUserProfile.mockResolvedValue(profile(7));
    mocks.uploadProfileAvatar.mockResolvedValue('/api/files/profile-avatars/7/avatar.webp');
    mocks.uploadProfileCover.mockResolvedValue('/api/files/profile-covers/users/v1/7/cover.webp');
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('initializes the current cover and does not upload before Save', async () => {
    mocks.getUser.mockResolvedValue(profile(7, '/api/files/profile-covers/users/v1/7/current.webp'));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(wrapper.get('.profile-edit-cover__preview img').attributes()).toMatchObject({
      src: '/api/files/profile-covers/users/v1/7/current.webp',
      alt: 'Profile cover preview',
    });
    const remove = wrapper.findAll('button').find((item) => item.text().includes('Remove cover'));
    expect(remove?.attributes('disabled')).toBeUndefined();
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();
  });

  it('keeps an empty preview and disables Remove cover when no cover exists', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);
    const remove = wrapper.findAll('button').find((item) => item.text().includes('Remove cover'));
    expect(remove?.attributes('disabled')).toBeDefined();
  });

  it('previews valid cover selection, replaces previews safely, and defers upload until Save', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    const first = new File(['first'], 'first.webp', { type: 'image/webp' });
    const second = new File(['second'], 'second.png', { type: 'image/png' });

    await setCoverFile(wrapper, first);
    expect(URL.createObjectURL).toHaveBeenCalledWith(first);
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:first.webp');
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await setCoverFile(wrapper, second);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:first.webp');
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:second.png');
    expect(wrapper.get('.profile-edit-form button[type="submit"]').attributes('disabled')).toBeUndefined();
  });

  it('rejects unsupported, zero-byte, and oversized cover files before creating previews', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await setCoverFile(wrapper, new File(['bad'], 'bad.gif', { type: 'image/gif' }));
    expect(wrapper.get('.profile-edit-cover').text()).toContain('Use a JPEG, PNG, or WebP image.');
    expect(URL.createObjectURL).not.toHaveBeenCalled();

    await setCoverFile(wrapper, new File([], 'empty.webp', { type: 'image/webp' }));
    expect(wrapper.get('.profile-edit-cover').text()).toContain('Cover must be between 1 byte and 5 MiB.');

    const oversized = new File([new Uint8Array((5 * 1024 * 1024) + 1)], 'large.webp', {
      type: 'image/webp',
    });
    await setCoverFile(wrapper, oversized);
    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();
  });

  it('removes an existing cover with one PATCH and no cover upload', async () => {
    const updated = profile(7, '');
    mocks.getUser.mockResolvedValue(profile(7, '/existing.webp'));
    mocks.updateUserProfile.mockResolvedValue(updated);
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    const remove = wrapper.findAll('button').find((item) => item.text().includes('Remove cover'));
    await remove!.trigger('click');
    await submitEditor(wrapper);

    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledWith(7, { cover_image_url: '' });
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledWith(updated);
  });

  it('uploads a cover-only edit once, patches once, and updates the profile immediately', async () => {
    const uploadedURL = '/api/files/profile-covers/users/v1/7/new.webp';
    const updated = profile(7, uploadedURL);
    mocks.uploadProfileCover.mockResolvedValue(uploadedURL);
    mocks.updateUserProfile.mockResolvedValue(updated);
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    await submitEditor(wrapper);

    expect(mocks.uploadProfileAvatar).not.toHaveBeenCalled();
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledWith(7, { cover_image_url: uploadedURL });
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledWith(updated);
    expect(wrapper?.find('.profile-edit-dialog').attributes('open')).toBeUndefined();
    expect(wrapper?.get('.profile-cover__image').attributes('src')).toBe(uploadedURL);
  });

  it('uploads avatar then cover, then sends one combined PATCH', async () => {
    const events: string[] = [];
    const avatarURL = '/new-avatar.webp';
    const coverURL = '/new-cover.webp';
    const updated = { ...profile(7, coverURL), avatar_url: avatarURL, display_name: 'Updated' };
    mocks.uploadProfileAvatar.mockImplementation(async () => {
      events.push('avatar');
      return avatarURL;
    });
    mocks.uploadProfileCover.mockImplementation(async () => {
      events.push('cover');
      return coverURL;
    });
    mocks.updateUserProfile.mockImplementation(async (_id: number, payload: unknown) => {
      events.push('patch');
      expect(payload).toEqual({
        display_name: 'Updated',
        avatar_url: avatarURL,
        cover_image_url: coverURL,
      });
      return updated;
    });
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-display-name').setValue('Updated');
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    await submitEditor(wrapper);

    expect(events).toEqual(['avatar', 'cover', 'patch']);
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(1);
  });

  it('retains a pending cover after cover upload failure and retries without reuploading avatar', async () => {
    const coverURL = '/new-cover.webp';
    const updated = profile(7, coverURL);
    mocks.uploadProfileAvatar.mockResolvedValue('/new-avatar.webp');
    mocks.uploadProfileCover
      .mockRejectedValueOnce(new Error('cover upload failed'))
      .mockResolvedValueOnce(coverURL);
    mocks.updateUserProfile.mockResolvedValue(updated);
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    await submitEditor(wrapper);
    expect(wrapper.get('.profile-edit-error').text()).toBe('Could not upload cover image. Please retry.');
    expect(mocks.uploadProfileAvatar).toHaveBeenCalledTimes(1);
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).not.toHaveBeenCalled();

    await submitEditor(wrapper);
    expect(mocks.uploadProfileAvatar).toHaveBeenCalledTimes(1);
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(2);
    expect(mocks.updateUserProfile).toHaveBeenCalledWith(7, {
      avatar_url: '/new-avatar.webp',
      cover_image_url: coverURL,
    });
  });

  it('retains an uploaded cover after PATCH failure and retries PATCH only', async () => {
    const coverURL = '/new-cover.webp';
    const updated = profile(7, coverURL);
    mocks.uploadProfileCover.mockResolvedValue(coverURL);
    mocks.updateUserProfile
      .mockRejectedValueOnce(new Error('save failed'))
      .mockResolvedValueOnce(updated);
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    await submitEditor(wrapper);
    expect(wrapper.get('.profile-edit-error').text()).toBe('Could not save profile. Please retry.');
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(1);

    await submitEditor(wrapper);
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(2);
    expect(mocks.updateUserProfile).toHaveBeenLastCalledWith(7, { cover_image_url: coverURL });
  });

  it('does not patch or synchronize a stale cover upload after switching profiles', async () => {
    let resolveCover!: (url: string) => void;
    mocks.getUser.mockImplementation((id: string) => Promise.resolve(
      id === '7' ? profile(7) : profile(8),
    ));
    mocks.uploadProfileCover.mockReturnValue(new Promise<string>((resolve) => {
      resolveCover = resolve;
    }));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    const savePromise = wrapper.get('.profile-edit-form').trigger('submit');
    await nextTick();

    mocks.route.params.id = '8';
    await settle();
    resolveCover('/stale-cover.webp');
    await savePromise;
    await settle();

    expect(mocks.updateUserProfile).not.toHaveBeenCalled();
    expect(mocks.authStore.syncCurrentIdentityProfile).not.toHaveBeenCalled();
    expect(wrapper?.find('.profile-edit-dialog').attributes('open')).toBeUndefined();
  });

  it('revokes a pending cover preview on cancel and unmount', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    await wrapper.findAll('button').find((item) => item.text() === 'Cancel')!.trigger('click');
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:cover.webp');
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await openEditor(wrapper);
    await setCoverFile(wrapper, new File(['cover2'], 'cover2.webp', { type: 'image/webp' }));
    wrapper.unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:cover2.webp');
  });
});
