// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useProfileSessionStore } from '../store/profileSession';

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
};

const mocks = vi.hoisted(() => ({
  route: { name: 'UserProfile', params: { id: '7' } },
  setRouteID: (_id: string) => {},
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
  },
  feedStore: {
    isPostDeleted: vi.fn(),
    markPostDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
    applyLikeStateUpdate: vi.fn(),
  },
}));

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue');
  const route = reactive(mocks.route);
  mocks.setRouteID = (id: string) => {
    route.params.id = id;
  };
  return {
    useRoute: () => route,
    useRouter: () => mocks.router,
  };
});
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
}));

vi.mock('../services/postService', () => ({
  deletePost: mocks.deletePost,
}));

vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];

  readonly callback: IntersectionObserverCallback;
  observed: Element | null = null;
  disconnectCount = 0;

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback;
    FakeIntersectionObserver.instances.push(this);
  }

  observe(element: Element) {
    this.observed = element;
  }

  unobserve(_element: Element) {}

  disconnect() {
    this.disconnectCount += 1;
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }

  trigger(isIntersecting = true) {
    const entry = {
      isIntersecting,
      target: this.observed,
    } as IntersectionObserverEntry;
    this.callback([entry], this as unknown as IntersectionObserver);
  }
}

const originalIntersectionObserverDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  'IntersectionObserver',
);
const installFakeIntersectionObserver = () => {
  Object.defineProperty(globalThis, 'IntersectionObserver', {
    configurable: true,
    writable: true,
    value: FakeIntersectionObserver,
  });
};
const restoreIntersectionObserver = () => {
  if (originalIntersectionObserverDescriptor) {
    Object.defineProperty(globalThis, 'IntersectionObserver', originalIntersectionObserverDescriptor);
  } else {
    Reflect.deleteProperty(globalThis, 'IntersectionObserver');
  }
};
installFakeIntersectionObserver();

type UserProfileComponent = typeof import('./UserProfileView.vue')['default'];
let UserProfileView: UserProfileComponent;

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});
const post = (id: number, authorID: number) => ({
  id,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
  content: `Body ${id}`,
  language: 'und' as const,
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public' as const,
  media: [],
  like_count: 0,
  reply_count: 0,
  view_count: 0,
  deleted: false as const,
});

const timelineItem = (id: number, authorID: number) => ({
  activity_type: 'post' as const,
  activity_at: '2026-08-15T00:00:00.000Z',
  source_id: id,
  actor: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
  post: post(id, authorID),
});

const profileTimelineItem = (id: number, authorID: number) => ({
  activityType: 'post' as const,
  activityAt: '2026-08-15T00:00:00.000Z',
  sourceId: id,
  actor: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
  post: {
    ...post(id, authorID),
    createdAt: '2026-08-15T00:00:00.000Z',
    likeCount: 0,
    replyCount: 0,
    viewCount: 0,
    liked: false,
    likeStatus: 'ready' as const,
    repostCount: 0,
    reposted: false,
    repostStatus: 'ready' as const,
  },
});

const PostCardStub = {
  props: ['post', 'showDelete'],
  template: `
    <article class="post-card">
      <span class="post-card__id">{{ post.id }}</span>
      <button v-if="showDelete" class="post-card__delete" type="button" @click="$emit('delete-post', post.id)">Delete</button>
    </article>
  `,
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const deferred = <T>(): Deferred<T> => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
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

const mountKeepAliveProfile = () => {
  const state = reactive({ showProfile: true });
  const Placeholder = defineComponent({
    name: 'PostDetailView',
    template: '<div data-placeholder />',
  });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { include: 'UserProfileView', max: 1 }, {
        default: () => (state.showProfile ? h(UserProfileView) : h(Placeholder)),
      });
    },
  });
  const wrapper = mount(Host, {
    global: {
      stubs: {
        AppIcon: { template: '<span />' },
        PostCard: PostCardStub,
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  return { wrapper, state };
};

const activeObserver = () => [...FakeIntersectionObserver.instances]
  .reverse()
  .find((candidate) => candidate.observed !== null && candidate.disconnectCount === 0);

const mountedViews: ReturnType<typeof mountProfile>[] = [];

beforeAll(async () => {
  vi.resetModules();
  UserProfileView = (await import('./UserProfileView.vue')).default;
});

afterEach(() => {
  mountedViews.splice(0).forEach((mounted) => mounted.unmount());
  FakeIntersectionObserver.instances.length = 0;
  vi.clearAllMocks();
  restoreIntersectionObserver();
});

afterAll(() => {
  restoreIntersectionObserver();
});

describe('UserProfileView observer and cursor concurrency', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    installFakeIntersectionObserver();
    vi.resetAllMocks();
    FakeIntersectionObserver.instances.length = 0;
    mocks.route.name = 'UserProfile';
    mocks.setRouteID('7');
    mocks.authStore.currentIdentity.id = 7;
    mocks.getUser.mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserTimeline.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.isPostDeleted.mockReturnValue(false);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
  });

  it('re-establishes the observer after delete and continues cursor pagination', async () => {
    mocks.getUserTimeline
      .mockResolvedValueOnce({ items: [timelineItem(1, 7)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [timelineItem(2, 7)], next_cursor: null });

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();

    const initialObserver = activeObserver();
    expect(initialObserver).toBeDefined();
    expect(initialObserver?.observed).toBe(mounted.find('.profile-feed-sentinel').element);

    await mounted.find('.post-card__delete').trigger('click');
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(initialObserver?.disconnectCount).toBeGreaterThan(0);

    const replacementObserver = activeObserver();
    expect(replacementObserver).toBeDefined();
    expect(replacementObserver).not.toBe(initialObserver);
    expect(replacementObserver?.observed).toBe(mounted.find('.profile-feed-sentinel').element);

    replacementObserver?.trigger();
    await settle();

    expect(mocks.getUserTimeline).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
    expect(mounted.findAll('.post-card__id')).toHaveLength(1);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Loading more activity...');
  });

  it('invalidates a pending load-more response without losing the original cursor', async () => {
    const pendingLoadMore = deferred<{ items: ReturnType<typeof timelineItem>[]; next_cursor: string | null }>();
    let serveNewPage = false;
    mocks.getUserTimeline.mockImplementation((_id: string, options?: { cursor?: string }) => {
      if (!options?.cursor) {
        return Promise.resolve({ items: [timelineItem(1, 7)], next_cursor: 'cursor-1' });
      }
      return serveNewPage
        ? Promise.resolve({ items: [timelineItem(2, 7)], next_cursor: null })
        : pendingLoadMore.promise;
    });

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();

    const initialObserver = activeObserver();
    expect(initialObserver).toBeDefined();
    initialObserver?.trigger();
    await nextTick();
    await flushPromises();

    expect(mocks.getUserTimeline).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });

    await mounted.find('.post-card__delete').trigger('click');
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(activeObserver()).toBeDefined();

    pendingLoadMore.resolve({ items: [timelineItem(2, 7)], next_cursor: 'cursor-2' });
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(mocks.getUserTimeline).toHaveBeenCalledTimes(2);

    serveNewPage = true;
    const replacementObserver = activeObserver();
    expect(replacementObserver).toBeDefined();
    replacementObserver?.trigger();
    await settle();

    expect(mocks.getUserTimeline).toHaveBeenNthCalledWith(3, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
    expect(mounted.findAll('.post-card__id')).toHaveLength(1);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Loading more activity...');
  });

  it('restores cached scroll once and never rewinds it during pagination changes', async () => {
    const userAgentDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
    Object.defineProperty(window.navigator, 'userAgent', {
      configurable: true,
      value: 'Mozilla/5.0',
    });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    const profileStore = useProfileSessionStore();
    const session = profileStore.ensureSession(7)!;
    session.user = profile(7);
    session.profileLoaded = true;
    session.timelineLoaded = true;
    session.timelineItems = [profileTimelineItem(1, 7)];
    session.loadedActivityKeys.add('post:1');
    session.hasMore = true;
    session.nextCursor = 'cursor-1';
    session.scrollY = 500;

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith({ top: 500, behavior: 'auto' });

    session.timelineLoadingMore = true;
    session.timelineItems = [...session.timelineItems, profileTimelineItem(2, 7)];
    session.timelineLoadingMore = false;
    await settle();
    expect(scrollTo).toHaveBeenCalledTimes(1);

    if (userAgentDescriptor) {
      Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor);
    } else {
      Reflect.deleteProperty(window.navigator, 'userAgent');
    }
    scrollTo.mockRestore();
  });

  it('keeps the Profile identity when PostDetail reuses the route parameter name', async () => {
    const profileStore = useProfileSessionStore();
    const ensureSession = vi.spyOn(profileStore, 'ensureSession');
    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();

    expect(mounted.find('h1').text()).toContain('User 7');

    mocks.route.name = 'PostDetail';
    mocks.setRouteID('9999');
    await settle();

    expect(mounted.find('h1').text()).toContain('User 7');
    expect(mocks.getUser).not.toHaveBeenCalledWith('9999');
    expect(mocks.getUserTimeline).not.toHaveBeenCalledWith('9999', expect.anything());
    expect(ensureSession.mock.calls.some(([id]) => id === 9999)).toBe(false);
  });

  it('pauses and resumes the cached Profile without a second component scroll restoration', async () => {
    const userAgentDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
    const scrollYDescriptor = Object.getOwnPropertyDescriptor(window, 'scrollY');
    Object.defineProperty(window.navigator, 'userAgent', {
      configurable: true,
      value: 'Mozilla/5.0',
    });
    Object.defineProperty(window, 'scrollY', {
      configurable: true,
      writable: true,
      value: 1480,
    });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    const profileStore = useProfileSessionStore();
    const session = profileStore.ensureSession(7)!;
    session.user = profile(7);
    session.profileLoaded = true;
    session.timelineLoaded = true;
    session.timelineItems = [profileTimelineItem(1, 7)];
    session.loadedActivityKeys.add('post:1');
    session.hasMore = true;
    session.nextCursor = 'cursor-1';
    session.scrollY = 900;

    const { wrapper, state } = mountKeepAliveProfile();
    mountedViews.push(wrapper);

    try {
      await settle();
      expect(scrollTo).toHaveBeenCalledTimes(1);
      const initialObserver = activeObserver();
      expect(initialObserver).toBeDefined();

      state.showProfile = false;
      await nextTick();
      mocks.route.name = 'PostDetail';
      mocks.setRouteID('9999');
      await settle();

      expect(session.scrollY).toBe(1480);
      expect(initialObserver?.disconnectCount).toBeGreaterThan(0);

      initialObserver?.trigger();
      session.timelineItems = [...session.timelineItems, profileTimelineItem(2, 7)];
      await settle();

      expect(mocks.getUserTimeline).not.toHaveBeenCalledWith('9999', expect.anything());
      expect(FakeIntersectionObserver.instances).toHaveLength(1);
      expect(mocks.getUserTimeline).not.toHaveBeenCalledWith('7', { limit: 20, cursor: 'cursor-1' });

      const userCallsBeforeActivation = mocks.getUser.mock.calls.length;
      const timelineCallsBeforeActivation = mocks.getUserTimeline.mock.calls.length;
      mocks.route.name = 'UserProfile';
      mocks.setRouteID('7');
      state.showProfile = true;
      await settle();

      const resumedObserver = activeObserver();
      expect(resumedObserver).toBeDefined();
      expect(resumedObserver).not.toBe(initialObserver);
      expect(resumedObserver?.observed).toBe(wrapper.find('.profile-feed-sentinel').element);
      expect(scrollTo).toHaveBeenCalledTimes(1);
      expect(mocks.getUser).toHaveBeenCalledTimes(userCallsBeforeActivation);
      expect(mocks.getUserTimeline).toHaveBeenCalledTimes(timelineCallsBeforeActivation);
    } finally {
      scrollTo.mockRestore();
      if (userAgentDescriptor) {
        Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor);
      }
      if (scrollYDescriptor) {
        Object.defineProperty(window, 'scrollY', scrollYDescriptor);
      } else {
        Reflect.deleteProperty(window, 'scrollY');
      }
    }
  });

  it('saves the previous profile and restores the next profile once on route switch', async () => {
    const userAgentDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
    Object.defineProperty(window.navigator, 'userAgent', {
      configurable: true,
      value: 'Mozilla/5.0',
    });
    Object.defineProperty(window, 'scrollY', {
      configurable: true,
      writable: true,
      value: 400,
    });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    const profileStore = useProfileSessionStore();
    const first = profileStore.ensureSession(7)!;
    first.user = profile(7);
    first.profileLoaded = true;
    first.timelineLoaded = true;
    first.scrollY = 400;
    const second = profileStore.ensureSession(8)!;
    second.user = profile(8);
    second.profileLoaded = true;
    second.timelineLoaded = true;
    second.scrollY = 900;

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 400, behavior: 'auto' });

    mocks.setRouteID('8');
    await settle();
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 900, behavior: 'auto' });
    expect(scrollTo).toHaveBeenCalledTimes(2);
    expect(first.scrollY).toBe(400);

    if (userAgentDescriptor) {
      Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor);
    } else {
      Reflect.deleteProperty(window.navigator, 'userAgent');
    }
    scrollTo.mockRestore();
  });
});
