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
    replace: vi.fn(),
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
  getBookmarks: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
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
  registerBookmarksSessionSync: vi.fn(),
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

vi.mock('../services/bookmarkService', () => ({
  getBookmarks: mocks.getBookmarks,
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
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

const setProfileRoute = (id: number) => {
  mocks.route.name = 'UserProfile';
  mocks.route.params.id = String(id);
  mocks.route.fullPath = `/users/${id}`;
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
      query: {},
      fullPath: '/users/7',
    });
    mocks.routeLeaveGuard = null;
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: profile(7),
    });
    mocks.getUser.mockImplementation((id: number) => Promise.resolve(profile(id)));
    mocks.getUserTimeline.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getPostRepostStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.getBookmarks.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostBookmarkStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
    mocks.router.replace.mockImplementation(async (location: { query?: Record<string, unknown> }) => {
      mocks.route.query = location.query ?? {};
    });
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

  it('restores own-profile scroll after cached PostDetail return even if DOM scroll is reset', async () => {
    const scrollYDescriptor = Object.getOwnPropertyDescriptor(window, 'scrollY');
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    const { session } = prepareLoadedSession(7, 0);
    const { wrapper, state } = mountKeepAliveProfile();

    try {
      await settle();
      const originalViewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      originalViewport.scrollTop = 2400;
      mocks.routeLeaveGuard?.();
      originalViewport.scrollTop = 0;

      state.showProfile = false;
      await nextTick();
      originalViewport.scrollTop = 0;
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
      expect(session.scrollTop).toBe(2400);
      expect(scrollTo).not.toHaveBeenCalled();
    } finally {
      wrapper.unmount();
      scrollTo.mockRestore();
      restoreScrollY(scrollYDescriptor);
    }
  });

  it('restores an external profile after cached PostDetail return and stays one-shot during updates', async () => {
    const scrollYDescriptor = Object.getOwnPropertyDescriptor(window, 'scrollY');
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    setProfileRoute(8);
    const { session } = prepareLoadedSession(8, 0);
    const { wrapper, state } = mountKeepAliveProfile();

    try {
      await settle();
      const originalViewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      originalViewport.scrollTop = 2400;
      mocks.routeLeaveGuard?.();
      expect(session.scrollTop).toBe(2400);
      originalViewport.scrollTop = 0;

      state.showProfile = false;
      await nextTick();
      originalViewport.scrollTop = 0;
      session.timelineInitialError = 'hidden update';
      await settle();
      expect(originalViewport.scrollTop).toBe(0);

      mocks.route.name = 'PostDetail';
      mocks.route.params.id = '9999';
      await settle();
      setProfileRoute(8);
      state.showProfile = true;
      await settle();

      const restoredViewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      expect(restoredViewport).toBe(originalViewport);
      expect(restoredViewport.scrollTop).toBe(2400);
      expect(session.scrollTop).toBe(2400);

      restoredViewport.scrollTop = 3100;
      session.timelineInitialError = 'active update';
      await settle();
      expect(restoredViewport.scrollTop).toBe(3100);
      expect(scrollTo).not.toHaveBeenCalled();
    } finally {
      wrapper.unmount();
      scrollTo.mockRestore();
      restoreScrollY(scrollYDescriptor);
    }
  });

  it('keeps independent scroll positions for different profiles', async () => {
    prepareLoadedSession(8, 2400);
    prepareLoadedSession(9, 700);
    setProfileRoute(8);
    const wrapper = mountProfile();

    try {
      await settle();
      const viewport = wrapper.get('.profile-scroll-viewport').element as HTMLElement;
      expect(viewport.scrollTop).toBe(2400);

      mocks.routeLeaveGuard?.();
      setProfileRoute(9);
      await settle();
      expect(viewport.scrollTop).toBe(700);

      mocks.routeLeaveGuard?.();
      setProfileRoute(8);
      await settle();
      expect(viewport.scrollTop).toBe(2400);
    } finally {
      wrapper.unmount();
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

  it('ignores an obsolete bookmarks query and renders the normal Posts profile', async () => {
    prepareLoadedSession(7, 0);
    mocks.route.query = { tab: 'bookmarks' };
    mocks.route.fullPath = '/users/7?tab=bookmarks';
    const wrapper = mountProfile();

    await settle();

    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(mocks.getBookmarks).not.toHaveBeenCalled();
    expect(wrapper.find('.profile-tabs').exists()).toBe(false);
    expect(wrapper.find('#profile-bookmarks-heading').exists()).toBe(false);
    expect(wrapper.get('#profile-posts-heading').text()).toBe('Posts');
    wrapper.unmount();
  });
});
