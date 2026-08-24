// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import UserProfileView from './UserProfileView.vue';

const mocks = vi.hoisted(() => ({
  route: { params: { id: '7' } },
  getUser: vi.fn(),
  getUserArticles: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadProfileAvatar: vi.fn(),
  deleteArticle: vi.fn(),
  getArticleLikeStates: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  authStore: {
    isAuthenticated: true,
    currentIdentity: {
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
    },
    syncCurrentIdentityProfile: vi.fn(),
  },
  feedStore: {
    isArticleDeleted: vi.fn(),
    markArticleDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
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
  getUserArticles: mocks.getUserArticles,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: mocks.deleteArticle,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');

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
});

const originalUser = {
  id: 7,
  username: 'viewer',
  display_name: 'Viewer',
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
};

const updatedUser = {
  ...originalUser,
  display_name: 'Updated Viewer',
  avatar_url: '/api/files/profile-avatars/7/new.webp',
};

const PostCardStub = {
  props: ['post', 'showDelete'],
  template: '<article class="post-card">{{ post.title }}</article>',
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const mountProfile = () => mount(UserProfileView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      PostCard: PostCardStub,
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const editDisplayNameAndSave = async (wrapper: VueWrapper) => {
  const editButton = wrapper.findAll('button').find((button) => button.text() === 'Edit profile');
  if (!editButton) {
    throw new Error('Edit profile button not found');
  }
  await editButton.trigger('click');
  await nextTick();
  await wrapper.get('#profile-display-name').setValue('Updated Viewer');
  await wrapper.get('.profile-edit-form').trigger('submit');
  await settle();
};

describe('UserProfileView current identity synchronization', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity.id = 7;
    mocks.getUser.mockResolvedValue(originalUser);
    mocks.getUserArticles.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getArticleLikeStates.mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.feedStore.isArticleDeleted.mockReturnValue(false);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('synchronizes auth identity and feed author after a successful own-profile edit', async () => {
    mocks.updateUserProfile.mockResolvedValue(updatedUser);
    wrapper = mountProfile();
    await settle();

    await editDisplayNameAndSave(wrapper);

    expect(mocks.updateUserProfile).toHaveBeenCalledWith(7, { display_name: 'Updated Viewer' });
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledTimes(1);
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledWith(updatedUser);
    expect(mocks.feedStore.replaceAuthorIdentity).toHaveBeenCalledWith({
      id: 7,
      username: 'viewer',
      display_name: 'Updated Viewer',
      avatar_url: '/api/files/profile-avatars/7/new.webp',
    });
  });

  it('does not synchronize identity when the profile save fails', async () => {
    mocks.updateUserProfile.mockRejectedValue(new Error('save failed'));
    wrapper = mountProfile();
    await settle();

    await editDisplayNameAndSave(wrapper);

    expect(mocks.authStore.syncCurrentIdentityProfile).not.toHaveBeenCalled();
    expect(mocks.feedStore.replaceAuthorIdentity).not.toHaveBeenCalled();
  });
});
