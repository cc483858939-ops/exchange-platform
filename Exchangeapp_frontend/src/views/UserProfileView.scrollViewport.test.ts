// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import UserProfileView from './UserProfileView.vue';
import { useProfileSessionStore } from '../store/profileSession';

const mocks = vi.hoisted(() => ({
  route: null as any,
  routeLeaveGuard: null as (() => void) | null,
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
    applyRepostStateUpdate: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: (guard: () => void) => {
    mocks.routeLeaveGuard = guard;
  },
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/sessionSync', () => ({
  registerProfileSessionSync: vi.fn(),
  syncProfilePostRemoval: vi.fn(),
  syncProfileAuthorIdentity: vi.fn(),
  syncProfileLikeState: vi.fn(),
  syncProfileRepostState: vi.fn(),
  markOwnProfileTimelineStale: vi.fn(),
  syncProfileFollowState: vi.fn(),
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserTimeline: mocks.getUserTimeline,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
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

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const PostCardStub = {
  props: ['post'],
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

const mountKeepAliveProfile = () => {
  const state = reactive({ showProfile: true });
  const Placeholder = defineComponent({
    name: 'PostDetailView',
    template: '<div data-detail />',
  });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { include: 'UserProfileView', max: 1 }, {
        default: () => (state.showProfile ? h(UserProfileView) : h(Placeholder)),
      });
    },
  });
  return { wrapper: mount(Host, { global: { stubs: {
    AppIcon: { template: '<span />' },
    MobileAccountMenu: { template: '<span />' },
    PostCard: PostCardStub,
    RouterLink: { template: '<a><slot /></a>' },
  } } }), state };
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const prepareLoadedSession = (id: number, scrollTop: number) => {
  const store = useProfileSessionStore();
  const session = store.ensureSession(id)!;
  session.user = profile(id);
  session.profileLoaded = true;
  session.timelineLoaded = true;
  session.timelineInitialLoading = false;
  session.timelineItems = [];
  session.hasMore = false;
  session.followLoaded = true;
  session.followState = {
    user_id: id,
    following: false,
    follower_count: 0,
    following_count: 0,
  };
  session.scrollTop = scrollTop;
  return { store, session };
};

const restoreScrollY = (descriptor: PropertyDescriptor | undefined) => {
  if (descriptor) {
    Object.defineProperty(window, 'scrollY', descriptor);
  } else {
    Reflect.deleteProperty(window, 'scrollY');
  }
};

describe('UserProfileView scroll viewport', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.route = reactive({
      name: 'UserProfile',
      params: { id: '7' },
      fullPath: '/users/7',
    });
    mocks.routeLeaveGuard = null;
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: profile(7),
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
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
  });

  it('saves the viewport scrollTop on route leave and ignores window scroll', async () => {
    const scrollYDescriptor = Object.getOwnPropertyDescriptor(window, 'scrollY');
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    Object.defineProperty(window, 'scrollY', {
      configurable: true,
      writable: true,
      value: 777,
    });
    const { session } = prepareLoadedSession(7, 0);
    const wrapper = mountProfile();

    try {
      await settle();
      const viewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      viewport.scrollTop = 2400;

      expect(mocks.routeLeaveGuard).toBeTypeOf('function');
      mocks.routeLeaveGuard?.();

      expect(session.scrollTop).toBe(2400);
      expect(scrollTo).not.toHaveBeenCalled();
    } finally {
      wrapper.unmount();
      scrollTo.mockRestore();
      restoreScrollY(scrollYDescriptor);
    }
  });

  it('keeps the same cached viewport through a scrolled PostDetail return', async () => {
    const scrollYDescriptor = Object.getOwnPropertyDescriptor(window, 'scrollY');
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    prepareLoadedSession(7, 0);
    const { wrapper, state } = mountKeepAliveProfile();

    try {
      await settle();
      const originalViewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      originalViewport.scrollTop = 2400;
      mocks.routeLeaveGuard?.();

      state.showProfile = false;
      await nextTick();
      mocks.route.name = 'PostDetail';
      mocks.route.params.id = '9999';
      await settle();

      Object.defineProperty(window, 'scrollY', {
        configurable: true,
        writable: true,
        value: 1300,
      });
      mocks.route.name = 'UserProfile';
      mocks.route.params.id = '7';
      state.showProfile = true;
      await settle();

      const restoredViewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      expect(restoredViewport).toBe(originalViewport);
      expect(restoredViewport.scrollTop).toBe(2400);
      expect(scrollTo).not.toHaveBeenCalled();
    } finally {
      wrapper.unmount();
      scrollTo.mockRestore();
      restoreScrollY(scrollYDescriptor);
    }
  });

  it('restores scrollTop after a full profile remount', async () => {
    const { session } = prepareLoadedSession(7, 1800);
    const wrapper = mountProfile();

    await settle();

    expect(wrapper.get('.profile-scroll-viewport').element.scrollTop).toBe(1800);
    expect(session.scrollTop).toBe(1800);
    wrapper.unmount();
  });
});
