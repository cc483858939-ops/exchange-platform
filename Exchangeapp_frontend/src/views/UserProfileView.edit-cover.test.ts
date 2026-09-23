// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { defineComponent, nextTick, reactive, type PropType } from 'vue';
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
  avatarCropOutput: null as File | null,
  coverCropOutput: null as File | null,
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

const profile = (id: number, coverImageURL = '', avatarURL = '') => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: avatarURL,
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

const AvatarCropDialogStub = defineComponent({
  props: {
    file: { type: Object as PropType<File>, required: true },
  },
  emits: ['cancel', 'apply'],
  setup(props, { emit }) {
    const apply = () => emit('apply', mocks.avatarCropOutput ?? props.file);
    return { apply };
  },
  template: `
    <div class="avatar-crop-dialog">
      <button type="button" class="avatar-crop-dialog__cancel" @click="$emit('cancel')">Cancel</button>
      <button type="button" class="avatar-crop-dialog__apply" @click="apply">Apply</button>
    </div>
  `,
});

const CoverCropDialogStub = defineComponent({
  props: {
    file: { type: Object as PropType<File>, required: true },
  },
  emits: ['cancel', 'apply'],
  setup(props, { emit }) {
    return {
      apply: () => emit('apply', mocks.coverCropOutput ?? props.file),
    };
  },
  template: `
    <div class="cover-crop-dialog">
      <button type="button" class="cover-crop-dialog__cancel" @click="$emit('cancel')">Cancel</button>
      <button type="button" class="cover-crop-dialog__apply" @click="apply">Apply</button>
    </div>
  `,
});

const mountProfile = (options: { attachTo?: Element } = {}) => mount(UserProfileView, {
  attachTo: options.attachTo,
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      MobileAccountMenu: { template: '<span />' },
      PostCard: PostCardStub,
      AvatarCropDialog: AvatarCropDialogStub,
      CoverCropDialog: CoverCropDialogStub,
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

const applyAvatarCrop = async (wrapper: VueWrapper) => {
  mocks.avatarCropOutput = null;
  await wrapper.get('.avatar-crop-dialog__apply').trigger('click');
  mocks.avatarCropOutput = null;
  await nextTick();
};

const applyAvatarCropOutput = async (wrapper: VueWrapper, output: File) => {
  mocks.avatarCropOutput = output;
  await wrapper.get('.avatar-crop-dialog__apply').trigger('click');
  mocks.avatarCropOutput = null;
  await nextTick();
};

const createCroppedCover = (source: File, type: 'image/jpeg' | 'image/png' = 'image/png') => {
  const baseName = source.name.replace(/\.[^.]+$/, '');
  const extension = type === 'image/jpeg' ? 'jpg' : 'png';
  return new File([source], `${baseName}-cover-cropped.${extension}`, { type });
};

const applyCoverCrop = async (wrapper: VueWrapper, output: File) => {
  mocks.coverCropOutput = output;
  await wrapper.get('.cover-crop-dialog__apply').trigger('click');
  mocks.coverCropOutput = null;
  await nextTick();
};

const setAndApplyCoverFile = async (wrapper: VueWrapper, file: File) => {
  await setCoverFile(wrapper, file);
  await applyCoverCrop(wrapper, createCroppedCover(file));
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

const clickConfirmAction = async (wrapper: VueWrapper, label: string) => {
  const button = wrapper.findAll('.confirm-dialog__button').find((item) => item.text() === label);
  if (!button) throw new Error(`Confirm action not found: ${label}`);
  await button.trigger('click');
  await settle();
};

describe('UserProfileView profile cover editor', () => {
  let wrapper: VueWrapper | null = null;
  let attachedHost: HTMLDivElement | null = null;

  const mountAttachedProfile = () => {
    attachedHost = document.createElement('div');
    document.body.append(attachedHost);
    return mountProfile({ attachTo: attachedHost });
  };

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.avatarCropOutput = null;
    mocks.coverCropOutput = null;
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
    attachedHost?.remove();
    attachedHost = null;
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

  it('uses native button controls for cover and avatar media changes', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    const coverChange = wrapper.get('.profile-edit-cover__preview .profile-edit-cover__change');
    const avatarChange = wrapper.get('.profile-avatar--edit .profile-edit-avatar__change');
    expect(coverChange.element.tagName).toBe('BUTTON');
    expect(coverChange.attributes()).toMatchObject({
      'aria-label': 'Change cover',
      type: 'button',
    });
    expect(coverChange.attributes('for')).toBeUndefined();
    expect(coverChange.attributes('role')).toBeUndefined();
    expect(coverChange.attributes('tabindex')).toBeUndefined();
    expect(coverChange.attributes('aria-disabled')).toBeUndefined();
    expect(avatarChange.element.tagName).toBe('BUTTON');
    expect(avatarChange.attributes()).toMatchObject({
      'aria-label': 'Change photo',
      type: 'button',
    });
    expect(avatarChange.attributes('for')).toBeUndefined();
    expect(avatarChange.attributes('role')).toBeUndefined();
    expect(avatarChange.attributes('tabindex')).toBeUndefined();
    expect(avatarChange.attributes('aria-disabled')).toBeUndefined();
    expect(wrapper.get('.profile-edit-cover__actions').text().trim()).toBe('Remove cover');
    expect(wrapper.get('.profile-edit-avatar__actions').text().trim()).toBe('Remove photo');
    expect(wrapper.find('.profile-edit-avatar__copy > .profile-edit-field__label').exists()).toBe(false);
    expect(wrapper.findAll('.profile-edit-cover__actions label')).toHaveLength(0);
    expect(wrapper.findAll('.profile-edit-avatar__actions label')).toHaveLength(0);
    expect(wrapper.findAll('#profile-cover-input')).toHaveLength(1);
    expect(wrapper.findAll('#profile-avatar-input')).toHaveLength(1);
  });

  it('keeps hidden media file inputs out of the tab order', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(wrapper.get('#profile-cover-input').attributes('tabindex')).toBe('-1');
    expect(wrapper.get('#profile-avatar-input').attributes('tabindex')).toBe('-1');
  });

  it('keeps empty cover and avatar previews directly changeable', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);
    expect(wrapper.find('.profile-edit-cover__change').exists()).toBe(true);
    expect(wrapper.find('.profile-avatar--edit img').exists()).toBe(false);
    expect(wrapper.get('.profile-avatar--edit').text()).toBe('U');
    expect(wrapper.find('.profile-edit-avatar__change').exists()).toBe(true);
    expect(wrapper.get('.profile-edit-media-remove').attributes('disabled')).toBeDefined();
    expect(wrapper.get('.profile-edit-avatar__actions .profile-edit-media-remove').attributes('disabled')).toBeDefined();
  });

  it('clicking media change buttons activates the associated file inputs', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    const coverInput = wrapper.get('#profile-cover-input');
    const avatarInput = wrapper.get('#profile-avatar-input');
    const coverClick = vi.spyOn(coverInput.element as HTMLInputElement, 'click');
    const avatarClick = vi.spyOn(avatarInput.element as HTMLInputElement, 'click');

    await wrapper.get('.profile-edit-cover__change').trigger('click');
    await wrapper.get('.profile-edit-avatar__change').trigger('click');

    expect(coverClick).toHaveBeenCalledTimes(1);
    expect(avatarClick).toHaveBeenCalledTimes(1);
  });

  it('disables media cameras while saving', async () => {
    let resolveCover!: (url: string) => void;
    mocks.uploadProfileCover.mockReturnValue(new Promise<string>((resolve) => {
      resolveCover = resolve;
    }));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    const coverInput = wrapper.get('#profile-cover-input');
    const avatarInput = wrapper.get('#profile-avatar-input');
    const coverClick = vi.spyOn(coverInput.element as HTMLInputElement, 'click');
    const avatarClick = vi.spyOn(avatarInput.element as HTMLInputElement, 'click');
    const savePromise = wrapper.get('.profile-edit-form').trigger('submit');
    await nextTick();

    expect(coverInput.attributes('disabled')).toBeDefined();
    expect(avatarInput.attributes('disabled')).toBeDefined();
    expect(wrapper.get('.profile-edit-cover__change').attributes('disabled')).toBeDefined();
    expect(wrapper.get('.profile-edit-avatar__change').attributes('disabled')).toBeDefined();

    await wrapper.get('.profile-edit-cover__change').trigger('click');
    await wrapper.get('.profile-edit-avatar__change').trigger('click');
    expect(coverClick).not.toHaveBeenCalled();
    expect(avatarClick).not.toHaveBeenCalled();

    resolveCover('/saved-cover.webp');
    await savePromise;
    await settle();
  });

  it('keeps Remove photo disabled without an avatar', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    expect(wrapper.get('.profile-edit-avatar__actions .profile-edit-media-remove').attributes('disabled')).toBeDefined();
  });

  it('enables Remove photo when an avatar exists', async () => {
    mocks.getUser.mockResolvedValue(profile(7, '', '/existing-avatar.webp'));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    expect(wrapper.get('.profile-edit-avatar__actions .profile-edit-media-remove').attributes('disabled')).toBeUndefined();
  });

  it('uses the same cover framing classes for profile display and edit preview', async () => {
    wrapper = mountProfile();
    await settle();

    expect(wrapper.find('.profile-cover').exists()).toBe(true);

    await openEditor(wrapper);

    expect(wrapper.find('.profile-edit-cover__preview').exists()).toBe(true);
  });

  it('omits readonly username metadata from the edit profile form', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(wrapper.find('.profile-edit-field--readonly').exists()).toBe(false);
    expect(wrapper.get('.profile-edit-form').text()).not.toContain("Username can't be changed.");
    expect(wrapper.get('.profile-edit-form').text()).not.toContain('@user-7');
  });

  it('does not autofocus the display name when opening the profile editor', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    expect(document.activeElement).not.toBe(wrapper.get('#profile-display-name').element);
  });

  it('opens avatar crop before changing the preview and preserves the previous crop when a replacement is canceled', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await setAvatarFile(wrapper, new File(['first'], 'first.webp', { type: 'image/webp' }));
    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(true);
    expect(wrapper.find('.profile-avatar--edit img').exists()).toBe(false);
    expect(mocks.uploadProfileAvatar).not.toHaveBeenCalled();

    await wrapper.get('.avatar-crop-dialog__cancel').trigger('click');
    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(false);
    expect(wrapper.find('.profile-avatar--edit img').exists()).toBe(false);

    await setAvatarFile(wrapper, new File(['first'], 'first.webp', { type: 'image/webp' }));
    await applyAvatarCrop(wrapper);
    expect(wrapper.get('.profile-avatar--edit img').attributes('src')).toBe('blob:first.webp');

    await setAvatarFile(wrapper, new File(['second'], 'second.webp', { type: 'image/webp' }));
    await wrapper.get('.avatar-crop-dialog__cancel').trigger('click');
    expect(wrapper.get('.profile-avatar--edit img').attributes('src')).toBe('blob:first.webp');
  });

  it('restores focus to Change photo after canceling avatar crop', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);

    const avatarButton = wrapper.get('.profile-edit-avatar__change').element as HTMLButtonElement;
    const avatarFocus = vi.spyOn(avatarButton, 'focus');
    const coverButton = wrapper.get('.profile-edit-cover__change').element as HTMLButtonElement;
    const coverFocus = vi.spyOn(coverButton, 'focus');

    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await wrapper.get('.avatar-crop-dialog__cancel').trigger('click');
    await settle();

    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(false);
    expect(avatarFocus).toHaveBeenCalledTimes(1);
    expect(coverFocus).not.toHaveBeenCalled();
  });

  it('restores focus to Change photo after applying avatar crop', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);

    const avatarButton = wrapper.get('.profile-edit-avatar__change').element as HTMLButtonElement;
    const avatarFocus = vi.spyOn(avatarButton, 'focus');
    const source = new File(['avatar'], 'avatar.webp', { type: 'image/webp' });
    await setAvatarFile(wrapper, source);
    await applyAvatarCrop(wrapper);
    await settle();

    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(false);
    expect(wrapper.get('.profile-avatar--edit img').attributes('src')).toBe('blob:avatar.webp');
    expect(avatarFocus).toHaveBeenCalledTimes(1);
  });

  it('keeps focus in avatar crop when generated output is invalid', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);

    const avatarButton = wrapper.get('.profile-edit-avatar__change').element as HTMLButtonElement;
    const avatarFocus = vi.spyOn(avatarButton, 'focus');
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await applyAvatarCropOutput(wrapper, new File([], 'empty.webp', { type: 'image/webp' }));
    await settle();

    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(true);
    expect(avatarFocus).not.toHaveBeenCalled();
  });

  it('rejects an oversized avatar source before opening the crop dialog', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    const oversized = new File([new Uint8Array((10 * 1024 * 1024) + 1)], 'large.webp', {
      type: 'image/webp',
    });
    await setAvatarFile(wrapper, oversized);

    expect(wrapper.find('.avatar-crop-dialog').exists()).toBe(false);
    expect(wrapper.get('.profile-edit-avatar').text()).toContain('Photo is too large. Choose an image under 10 MB.');
  });

  it('crops valid cover selections before previewing and preserves the pending crop when replacement is canceled', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    const first = new File(['first'], 'first.webp', { type: 'image/webp' });
    const second = new File(['second'], 'second.png', { type: 'image/png' });
    const firstOutput = createCroppedCover(first);
    const secondOutput = createCroppedCover(second);

    await setCoverFile(wrapper, first);
    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(true);
    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);
    expect(URL.createObjectURL).not.toHaveBeenCalled();
    expect(wrapper.get('.profile-edit-form button[type="submit"]').attributes('disabled')).toBeDefined();
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await wrapper.get('.cover-crop-dialog__cancel').trigger('click');
    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(false);
    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);

    await setCoverFile(wrapper, first);
    await applyCoverCrop(wrapper, firstOutput);
    expect(URL.createObjectURL).toHaveBeenCalledWith(firstOutput);
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:first-cover-cropped.png');

    await setCoverFile(wrapper, second);
    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(true);
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:first-cover-cropped.png');
    await wrapper.get('.cover-crop-dialog__cancel').trigger('click');
    expect(URL.revokeObjectURL).not.toHaveBeenCalledWith('blob:first-cover-cropped.png');
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:first-cover-cropped.png');

    await setCoverFile(wrapper, second);
    await applyCoverCrop(wrapper, secondOutput);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:first-cover-cropped.png');
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:second-cover-cropped.png');
    expect(wrapper.get('.profile-edit-form button[type="submit"]').attributes('disabled')).toBeUndefined();
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();
  });

  it('restores focus to Change cover after canceling cover crop', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);

    const coverButton = wrapper.get('.profile-edit-cover__change').element as HTMLButtonElement;
    const coverFocus = vi.spyOn(coverButton, 'focus');
    const avatarButton = wrapper.get('.profile-edit-avatar__change').element as HTMLButtonElement;
    const avatarFocus = vi.spyOn(avatarButton, 'focus');

    await setCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    await wrapper.get('.cover-crop-dialog__cancel').trigger('click');
    await settle();

    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(false);
    expect(coverFocus).toHaveBeenCalledTimes(1);
    expect(avatarFocus).not.toHaveBeenCalled();
  });

  it('restores focus to Change cover after applying cover crop', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);

    const coverButton = wrapper.get('.profile-edit-cover__change').element as HTMLButtonElement;
    const coverFocus = vi.spyOn(coverButton, 'focus');
    const source = new File(['cover'], 'cover.webp', { type: 'image/webp' });
    const cropped = createCroppedCover(source);
    await setCoverFile(wrapper, source);
    await applyCoverCrop(wrapper, cropped);
    await settle();

    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(false);
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:cover-cover-cropped.png');
    expect(coverFocus).toHaveBeenCalledTimes(1);
  });

  it('rejects an invalid generated cover while keeping the crop dialog open and pending preview unchanged', async () => {
    wrapper = mountAttachedProfile();
    await settle();
    await openEditor(wrapper);
    const first = new File(['first'], 'first.webp', { type: 'image/webp' });
    await setAndApplyCoverFile(wrapper, first);
    const oldPreview = wrapper.get('.profile-edit-cover__preview img').attributes('src');
    const coverButton = wrapper.get('.profile-edit-cover__change').element as HTMLButtonElement;
    const coverFocus = vi.spyOn(coverButton, 'focus');

    await setCoverFile(wrapper, new File(['next'], 'next.webp', { type: 'image/webp' }));
    await applyCoverCrop(wrapper, new File(['invalid'], 'invalid.webp', { type: 'image/webp' }));

    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(true);
    expect(wrapper.get('.profile-edit-cover').text()).toContain('Could not prepare this cover. Try another image.');
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe(oldPreview);
    expect(URL.revokeObjectURL).not.toHaveBeenCalledWith(oldPreview);
    expect(coverFocus).not.toHaveBeenCalled();
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

  it('uploads the cropped cover file on Save and patches the returned URL once', async () => {
    const source = new File(['raw-source'], 'original.webp', { type: 'image/webp' });
    const cropped = new File(['cropped-output'], 'original-cover-cropped.png', { type: 'image/png' });
    const uploadedURL = '/api/files/profile-covers/users/v1/7/new.webp';
    const updated = profile(7, uploadedURL);
    mocks.uploadProfileCover.mockResolvedValue(uploadedURL);
    mocks.updateUserProfile.mockResolvedValue(updated);
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await setCoverFile(wrapper, source);
    expect(source).not.toBe(cropped);
    expect(wrapper.find('.cover-crop-dialog').exists()).toBe(true);
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await applyCoverCrop(wrapper, cropped);
    expect(URL.createObjectURL).toHaveBeenCalledWith(cropped);
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await submitEditor(wrapper);

    expect(mocks.uploadProfileAvatar).not.toHaveBeenCalled();
    expect(mocks.uploadProfileCover).toHaveBeenCalledTimes(1);
    expect(mocks.uploadProfileCover.mock.calls[0]?.[0]).toBe(cropped);
    expect(mocks.uploadProfileCover.mock.calls[0]?.[0]).not.toBe(source);
    expect(mocks.updateUserProfile).toHaveBeenCalledTimes(1);
    expect(mocks.updateUserProfile).toHaveBeenCalledWith(7, { cover_image_url: uploadedURL });
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledWith(updated);
    expect(wrapper?.find('.profile-edit-dialog').attributes('open')).toBeUndefined();
    expect(wrapper?.find('.confirm-dialog').exists()).toBe(false);
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
    await applyAvatarCrop(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
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
    await applyAvatarCrop(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

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
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

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
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
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
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));
    await wrapper.findAll('button').find((item) => item.text() === 'Cancel')!.trigger('click');
    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
    await clickConfirmAction(wrapper, 'Discard');
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:cover-cover-cropped.png');
    expect(mocks.uploadProfileCover).not.toHaveBeenCalled();

    await openEditor(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover2'], 'cover2.webp', { type: 'image/webp' }));
    wrapper.unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:cover2-cover-cropped.png');
  });

  it('closes immediately without confirmation when the editor is clean', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await wrapper.get('.profile-edit-dialog__close').trigger('click');

    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeUndefined();
    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
  });

  it('protects a changed bio when the close button is clicked', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-bio').setValue('A bio that should not be lost.');

    await wrapper.get('.profile-edit-dialog__close').trigger('click');

    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect(wrapper.find('.confirm-dialog').text()).toContain("Your profile changes haven't been saved.");
    expect((wrapper.get('#profile-bio').element as HTMLTextAreaElement).value).toBe('A bio that should not be lost.');
  });

  it('protects a changed display name when Cancel is clicked', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-display-name').setValue('Changed display name');

    await wrapper.findAll('button').find((item) => item.text() === 'Cancel')!.trigger('click');

    expect(wrapper.find('.confirm-dialog').text()).toContain('Discard changes?');
    expect((wrapper.get('#profile-display-name').element as HTMLInputElement).value).toBe('Changed display name');
  });

  it('protects a pending avatar and preview when Escape triggers dialog cancel', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await applyAvatarCrop(wrapper);

    const event = new Event('cancel', { cancelable: true });
    wrapper.get('.profile-edit-dialog').element.dispatchEvent(event);
    await nextTick();

    expect(event.defaultPrevented).toBe(true);
    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
    expect(wrapper.get('.profile-avatar--edit img').attributes('src')).toBe('blob:avatar.webp');
  });

  it('protects a pending cover and preview when Escape triggers dialog cancel', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    const event = new Event('cancel', { cancelable: true });
    wrapper.get('.profile-edit-dialog').element.dispatchEvent(event);
    await nextTick();

    expect(event.defaultPrevented).toBe(true);
    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:cover-cover-cropped.png');
  });

  it('keeps all draft and preview state after Keep editing', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-display-name').setValue('Changed display name');
    await wrapper.get('#profile-bio').setValue('Changed bio');
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await applyAvatarCrop(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    const closeButton = wrapper.get('.profile-edit-dialog__close');
    await closeButton.trigger('click');
    await clickConfirmAction(wrapper, 'Keep editing');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect((wrapper.get('#profile-display-name').element as HTMLInputElement).value).toBe('Changed display name');
    expect((wrapper.get('#profile-bio').element as HTMLTextAreaElement).value).toBe('Changed bio');
    expect(wrapper.get('.profile-avatar--edit img').attributes('src')).toBe('blob:avatar.webp');
    expect(wrapper.get('.profile-edit-cover__preview img').attributes('src')).toBe('blob:cover-cover-cropped.png');
  });

  it('clears the full edit draft and previews after Discard', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-bio').setValue('Changed bio');
    await setAvatarFile(wrapper, new File(['avatar'], 'avatar.webp', { type: 'image/webp' }));
    await applyAvatarCrop(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    await wrapper.findAll('button').find((item) => item.text() === 'Cancel')!.trigger('click');
    await clickConfirmAction(wrapper, 'Discard');

    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeUndefined();
    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:avatar.webp');
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:cover-cover-cropped.png');

    await openEditor(wrapper);
    expect((wrapper.get('#profile-bio').element as HTMLTextAreaElement).value).toBe('');
    expect(wrapper.find('.profile-avatar--edit img').exists()).toBe(false);
    expect(wrapper.find('.profile-edit-cover__preview img').exists()).toBe(false);
  });

  it('keeps an over-limit bio dirty even though Save is disabled', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-bio').setValue('b'.repeat(161));

    expect(wrapper.get('.profile-edit-form button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('.profile-edit-dialog__close').trigger('click');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
  });

  it('keeps an over-limit display name dirty even though Save is disabled', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-display-name').setValue('n'.repeat(51));

    expect(wrapper.get('.profile-edit-form button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.findAll('button').find((item) => item.text() === 'Cancel')!.trigger('click');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
  });

  it('treats removing an existing avatar as a dirty edit', async () => {
    mocks.getUser.mockResolvedValue(profile(7, '', '/existing-avatar.webp'));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await wrapper.findAll('button').find((item) => item.text().includes('Remove photo'))!.trigger('click');
    await wrapper.get('.profile-edit-dialog__close').trigger('click');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
  });

  it('treats removing an existing cover as a dirty edit', async () => {
    mocks.getUser.mockResolvedValue(profile(7, '/existing-cover.webp'));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);

    await wrapper.findAll('button').find((item) => item.text().includes('Remove cover'))!.trigger('click');
    await wrapper.get('.profile-edit-dialog__close').trigger('click');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
  });

  it('does not open discard confirmation or close while a save is in flight', async () => {
    let resolveCover!: (url: string) => void;
    mocks.uploadProfileCover.mockReturnValue(new Promise<string>((resolve) => {
      resolveCover = resolve;
    }));
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await setAndApplyCoverFile(wrapper, new File(['cover'], 'cover.webp', { type: 'image/webp' }));

    const savePromise = wrapper.get('.profile-edit-form').trigger('submit');
    await nextTick();
    const event = new Event('cancel', { cancelable: true });
    wrapper.get('.profile-edit-dialog').element.dispatchEvent(event);
    await nextTick();

    expect(event.defaultPrevented).toBe(true);
    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);

    resolveCover('/new-cover.webp');
    await savePromise;
    await settle();
  });

  it('does not stack confirmations after repeated close requests', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-bio').setValue('Changed bio');

    const closeButton = wrapper.get('.profile-edit-dialog__close');
    await closeButton.trigger('click');
    await closeButton.trigger('click');

    expect(wrapper.findAll('.confirm-dialog')).toHaveLength(1);
  });

  it('keeps the editor usable after Escape followed by Keep editing', async () => {
    wrapper = mountProfile();
    await settle();
    await openEditor(wrapper);
    await wrapper.get('#profile-bio').setValue('First draft');

    const event = new Event('cancel', { cancelable: true });
    wrapper.get('.profile-edit-dialog').element.dispatchEvent(event);
    await nextTick();
    await clickConfirmAction(wrapper, 'Keep editing');
    await wrapper.get('#profile-bio').setValue('Second draft');

    expect(wrapper.find('.profile-edit-dialog').attributes('open')).toBeDefined();
    expect((wrapper.get('#profile-bio').element as HTMLTextAreaElement).value).toBe('Second draft');
  });
});
