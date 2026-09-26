// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { ElMessage } from 'element-plus';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import UserProfileView from './UserProfileView.vue';
import { useProfileSessionStore } from '../store/profileSession';

vi.mock('element-plus/es/components/message/style/css', () => ({}));

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  feedStore: null as any,
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
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
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

vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));

vi.mock('../store/sessionSync', () => ({
  registerProfileSessionSync: vi.fn(),
  registerBookmarksSessionSync: vi.fn(),
  syncProfilePostRemoval: vi.fn(),
  syncProfileAuthorIdentity: vi.fn(),
  syncProfileLikeState: vi.fn(),
  syncProfileRepostState: vi.fn(),
  syncProfileBookmarkState: vi.fn(),
  syncProfileFollowState: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
  markOwnProfileTimelineStale: vi.fn(),
}));

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  cover_image_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
  follower_count: 0,
  following_count: 0,
});

const timelineItem = {
  activity_type: 'post' as const,
  activity_at: '2026-08-15T00:00:00.000Z',
  source_id: 101,
  actor: {
    id: 8,
    username: 'user-8',
    display_name: 'User 8',
    avatar_url: '',
  },
  post: {
    id: 101,
    created_at: '2026-08-15T00:00:00.000Z',
    updated_at: '2026-08-15T00:00:00.000Z',
    published_at: '2026-08-15T00:00:00.000Z',
    author: {
      id: 8,
      username: 'user-8',
      display_name: 'User 8',
      avatar_url: '',
    },
    content: 'Profile engagement test post',
    conversation_id: 101,
    reply_to_post_id: null,
    quote_post_id: null,
    reply_to_post: null,
    quote_post: null,
    visibility: 'public' as const,
    media: [],
    like_count: 0,
    repost_count: 0,
    reply_count: 0,
    view_count: 0,
    deleted: false as const,
  },
};

const PostCardStub = {
  props: ['post'],
  emits: ['toggle-like', 'toggle-repost'],
  template: `
    <article>
      <button class="test-profile-like" @click="$emit('toggle-like', post.id)">Like</button>
      <button class="test-profile-repost" @click="$emit('toggle-repost', post.id)">Repost</button>
    </article>
  `,
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const mountProfile = async () => {
  const wrapper = mount(UserProfileView, {
    global: {
      stubs: {
        AppIcon: { template: '<span />' },
        MobileAccountMenu: { template: '<span />' },
        PostCard: PostCardStub,
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  await settle();
  return wrapper;
};

describe('UserProfileView engagement failure feedback', () => {
  let profileStore: ReturnType<typeof useProfileSessionStore>;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.route = reactive({
      name: 'UserProfile',
      params: { id: '7' },
      fullPath: '/users/7?source=feed',
    });
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: profile(8),
      syncCurrentIdentityProfile: vi.fn(),
    });
    mocks.feedStore = reactive({
      viewerID: 8,
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
      markPostDeleted: vi.fn(),
      replaceAuthorIdentity: vi.fn(),
      applyLikeStateUpdate: vi.fn(),
      applyRepostStateUpdate: vi.fn(),
      applyBookmarkStateUpdate: vi.fn(),
    });
    mocks.getUser.mockResolvedValue(profile(7));
    mocks.getUserTimeline.mockResolvedValue({ items: [timelineItem], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      user_id: 7,
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.likePost.mockResolvedValue({ likes: 1, liked: true });
    mocks.repostPost.mockResolvedValue({ reposts: 1, reposted: true });
    profileStore = useProfileSessionStore();
    vi.spyOn(profileStore, 'toggleLike').mockResolvedValue('succeeded');
    vi.spyOn(profileStore, 'toggleRepost').mockResolvedValue('succeeded');
    vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: vi.fn() }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows one actionable message when Like fails', async () => {
    vi.mocked(profileStore.toggleLike).mockResolvedValueOnce('failed');
    const wrapper = await mountProfile();

    await wrapper.get('.test-profile-like').trigger('click');
    await settle();

    expect(profileStore.toggleLike).toHaveBeenCalledWith(101, 7);
    expect(ElMessage.error).toHaveBeenCalledTimes(1);
    expect(ElMessage.error).toHaveBeenCalledWith('Couldn’t update your like. Try again.');
    wrapper.unmount();
  });

  it.each(['succeeded', 'ignored'] as const)('does not show an error when Like is %s', async (result) => {
    vi.mocked(profileStore.toggleLike).mockResolvedValueOnce(result);
    const wrapper = await mountProfile();

    await wrapper.get('.test-profile-like').trigger('click');
    await settle();

    expect(ElMessage.error).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('shows one actionable message when Repost fails', async () => {
    vi.mocked(profileStore.toggleRepost).mockResolvedValueOnce('failed');
    const wrapper = await mountProfile();

    await wrapper.get('.test-profile-repost').trigger('click');
    await settle();

    expect(profileStore.toggleRepost).toHaveBeenCalledWith(101, 7);
    expect(ElMessage.error).toHaveBeenCalledTimes(1);
    expect(ElMessage.error).toHaveBeenCalledWith('Couldn’t update your repost. Try again.');
    wrapper.unmount();
  });

  it.each(['succeeded', 'ignored'] as const)('does not show an error when Repost is %s', async (result) => {
    vi.mocked(profileStore.toggleRepost).mockResolvedValueOnce(result);
    const wrapper = await mountProfile();

    await wrapper.get('.test-profile-repost').trigger('click');
    await settle();

    expect(ElMessage.error).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('keeps unauthenticated Like and Repost actions routed to Login without a toast', async () => {
    const wrapper = await mountProfile();
    const postCard = wrapper.findComponent(PostCardStub);
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;

    postCard.vm.$emit('toggle-like', 101);
    postCard.vm.$emit('toggle-repost', 101);
    await settle();

    expect(profileStore.toggleLike).not.toHaveBeenCalled();
    expect(profileStore.toggleRepost).not.toHaveBeenCalled();
    expect(mocks.router.push).toHaveBeenCalledTimes(2);
    expect(mocks.router.push).toHaveBeenCalledWith({
      name: 'Login',
      query: { returnTo: '/users/7?source=feed' },
    });
    expect(ElMessage.error).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});
