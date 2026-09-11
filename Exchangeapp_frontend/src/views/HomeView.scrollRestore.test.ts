// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import {
  defineComponent,
  h,
  KeepAlive,
  nextTick,
  reactive,
} from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost, FeedTab } from '../types/Feed';
import HomeView from './HomeView.vue';

type RouteLeaveTarget = {
  name: unknown;
  params: Record<string, unknown>;
};

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: null as any,
  homeTimeline: null as any,
  route: null as any,
  router: null as any,
  routeLeave: null as ((to: RouteLeaveTarget) => unknown) | null,
  telemetry: {
    resetObservedCards: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
    observeFeedCard: vi.fn(),
    detachFeedCard: vi.fn(),
    unobserveFeedCard: vi.fn(),
    recordClick: vi.fn(),
    recordNotInterested: vi.fn(),
  },
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

vi.mock('../services/recommendationTelemetry', () => ({
  getRecommendationTelemetry: () => mocks.telemetry,
}));

vi.mock('../services/recommendationAttribution', () => ({
  savePendingRecommendationAttribution: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
  onBeforeRouteLeave: (guard: (to: RouteLeaveTarget) => unknown) => {
    mocks.routeLeave = guard;
  },
}));

const viewer = {
  id: 7,
  username: 'viewer',
  display_name: 'Viewer',
  avatar_url: '',
};

const makePost = (id: number): FeedPost => ({
  id,
  author: viewer,
  content: `Post ${id}`,
  language: 'und',
  media: [],
  createdAt: '2026-08-24T00:00:00.000Z',
  likeCount: 0,
  replyCount: 0,
  viewCount: 0,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

const PostCardStub = defineComponent({
  props: {
    post: {
      type: Object,
      required: true,
    },
  },
  template: '<article class="post-card-stub" :data-feed-post-id="post.id"></article>',
});

const makeHomeTimeline = () => {
  const scrollY = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 900,
  });
  const returnAnchors = reactive<Record<FeedTab, any>>({
    'for-you': null,
    following: null,
  });
  const timeline: any = reactive({
    activeTab: 'for-you' as FeedTab,
    forYou: reactive({
      items: [{ recommendation: { post: { id: 42 }, score: 1 }, post: makePost(42) }],
      loading: false,
      error: false,
      loaded: true,
      loadingMore: false,
      loadMoreError: false,
      depleted: true,
    }),
    following: reactive({
      items: [makePost(88)],
      loading: false,
      error: false,
      loaded: true,
      nextCursor: null,
      loadingMore: false,
      loadMoreError: false,
      stale: false,
      revalidating: false,
      revalidateError: false,
    }),
    scrollY,
    returnAnchors,
    likePendingPostIds: new Set<number>(),
    repostPendingPostIds: new Set<number>(),
    pendingDeletePostIds: new Set<number>(),
    deleteErrors: new Map<number, string>(),
    setActiveTab: vi.fn((tab: FeedTab) => {
      timeline.activeTab = tab;
    }),
    setScrollY: vi.fn((tab: FeedTab, value: number) => {
      scrollY[tab] = value;
    }),
    setReturnAnchor: vi.fn((tab: FeedTab, anchor: any) => {
      returnAnchors[tab] = { ...anchor };
      return true;
    }),
    clearReturnAnchor: vi.fn((tab: FeedTab) => {
      returnAnchors[tab] = null;
    }),
    loadForYou: vi.fn().mockResolvedValue(undefined),
    loadMoreForYou: vi.fn().mockResolvedValue(undefined),
    retryForYouLoadMore: vi.fn(),
    loadFollowing: vi.fn().mockResolvedValue(undefined),
    loadMoreFollowing: vi.fn().mockResolvedValue(undefined),
    revalidateFollowing: vi.fn().mockResolvedValue(undefined),
    retryFollowingLoadMore: vi.fn(),
    toggleLike: vi.fn(),
    toggleRepost: vi.fn(),
    deletePost: vi.fn().mockResolvedValue(true),
    dismissRecommendation: vi.fn(),
  });
  return timeline;
};

const originalScrollY = Object.getOwnPropertyDescriptor(window, 'scrollY');
const originalScrollTo = Object.getOwnPropertyDescriptor(window, 'scrollTo');
const originalRequestAnimationFrame = Object.getOwnPropertyDescriptor(window, 'requestAnimationFrame');
const originalUserAgent = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');

let currentScrollY = 0;
let frameCallbacks: FrameRequestCallback[] = [];
const scrollToMock = vi.fn((options: ScrollToOptions | number, y?: number) => {
  currentScrollY = typeof options === 'number'
    ? y ?? options
    : Number(options.top ?? currentScrollY);
});
const requestAnimationFrameMock = vi.fn((callback: FrameRequestCallback) => {
  frameCallbacks.push(callback);
  return frameCallbacks.length;
});

const installScrollMocks = () => {
  currentScrollY = 0;
  frameCallbacks = [];
  scrollToMock.mockClear();
  requestAnimationFrameMock.mockClear();
  Object.defineProperty(window, 'scrollY', {
    configurable: true,
    get: () => currentScrollY,
  });
  Object.defineProperty(window, 'scrollTo', {
    configurable: true,
    writable: true,
    value: scrollToMock,
  });
  Object.defineProperty(window, 'requestAnimationFrame', {
    configurable: true,
    writable: true,
    value: requestAnimationFrameMock,
  });
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    value: 'Mozilla/5.0 HomeView scroll restoration test',
  });
};

const restoreProperty = (
  target: object,
  property: PropertyKey,
  descriptor: PropertyDescriptor | undefined,
) => {
  if (descriptor) {
    Object.defineProperty(target, property, descriptor);
  } else {
    Reflect.deleteProperty(target, property);
  }
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const flushFrame = async () => {
  const callbacks = frameCallbacks.splice(0);
  callbacks.forEach(callback => callback(0));
  await settle();
};

const mountHome = () => mount(HomeView, {
  global: {
    stubs: {
      FeedTabs: { template: '<div />' },
      PostCard: PostCardStub,
      AppIcon: { template: '<span />' },
      MobileHomeHeader: { template: '<div />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const mountKeepAliveHome = () => {
  const state = reactive({ showHome: true });
  const Placeholder = defineComponent({
    name: 'PostDetailView',
    template: '<div data-placeholder />',
  });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { include: 'HomeView', max: 1 }, {
        default: () => (state.showHome ? h(HomeView) : h(Placeholder)),
      });
    },
  });
  const wrapper = mount(Host, {
    global: {
      stubs: {
        FeedTabs: { template: '<div />' },
        PostCard: PostCardStub,
        AppIcon: { template: '<span />' },
        MobileHomeHeader: { template: '<div />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  return { wrapper, state };
};

const setCardElementTop = (card: Element, getTop: () => number) => {
  Object.defineProperty(card, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({ top: getTop() } as DOMRect),
  });
};

const setCardTop = (wrapper: ReturnType<typeof mount>, getTop: () => number) => {
  const card = wrapper.get('[data-feed-post-id="42"]').element;
  setCardElementTop(card, getTop);
  return card;
};

describe('HomeView anchored scroll restoration', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    installScrollMocks();
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: viewer,
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
    });
    mocks.homeTimeline = makeHomeTimeline();
    mocks.route = reactive({ name: 'Home', query: {} });
    mocks.router = {
      push: vi.fn().mockResolvedValue(undefined),
      replace: vi.fn().mockResolvedValue(undefined),
    };
    mocks.routeLeave = null;
    Object.values(mocks.telemetry).forEach((mock) => mock.mockClear());
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    restoreProperty(window, 'scrollY', originalScrollY);
    restoreProperty(window, 'scrollTo', originalScrollTo);
    restoreProperty(window, 'requestAnimationFrame', originalRequestAnimationFrame);
    restoreProperty(window.navigator, 'userAgent', originalUserAgent);
    document.body.innerHTML = '';
  });

  it('captures the active card before leaving Home for a valid PostDetail route', async () => {
    wrapper = mountHome();
    await settle();
    currentScrollY = 2400;
    setCardTop(wrapper, () => 170);

    mocks.routeLeave?.({ name: 'PostDetail', params: { id: '42' } });

    expect(mocks.homeTimeline.scrollY['for-you']).toBe(2400);
    expect(mocks.homeTimeline.returnAnchors['for-you']).toEqual({
      postId: 42,
      viewportTop: 170,
      fallbackScrollY: 2400,
    });
  });

  it('does not capture anchors for other routes or invalid PostDetail ids', async () => {
    wrapper = mountHome();
    await settle();
    currentScrollY = 2400;
    setCardTop(wrapper, () => 170);

    mocks.routeLeave?.({ name: 'UserProfile', params: { id: '42' } });
    mocks.routeLeave?.({ name: 'PostDetail', params: { id: 'not-an-id' } });

    expect(mocks.homeTimeline.setReturnAnchor).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.returnAnchors['for-you']).toBeNull();
  });

  it('keeps the captured fallback when Home deactivates after navigation starts', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    currentScrollY = 2400;
    setCardTop(wrapper, () => 170);
    mocks.routeLeave?.({ name: 'PostDetail', params: { id: '42' } });

    currentScrollY = 0;
    mocks.route.name = 'PostDetail';
    mounted.state.showHome = false;
    await settle();

    expect(mocks.homeTimeline.scrollY['for-you']).toBe(2400);
    expect(mocks.homeTimeline.returnAnchors['for-you']).toEqual({
      postId: 42,
      viewportTop: 170,
      fallbackScrollY: 2400,
    });
  });

  it('uses fallback plus anchor correction and consumes the anchor once', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    currentScrollY = 2400;
    const card = setCardTop(wrapper, () => 150);
    mocks.routeLeave?.({ name: 'PostDetail', params: { id: '42' } });

    mocks.route.name = 'PostDetail';
    mounted.state.showHome = false;
    await settle();

    let cardTop = 230;
    setCardElementTop(card, () => cardTop);
    scrollToMock.mockImplementation((options: ScrollToOptions | number, y?: number) => {
      currentScrollY = typeof options === 'number'
        ? y ?? options
        : Number(options.top ?? currentScrollY);
      if (currentScrollY === 2480) {
        cardTop = 150;
      }
    });
    scrollToMock.mockClear();

    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();
    await flushFrame();
    await flushFrame();

    expect(scrollToMock).toHaveBeenCalledWith({ top: 2400, behavior: 'auto' });
    expect(scrollToMock).toHaveBeenCalledWith({ top: 2480, behavior: 'auto' });
    expect(mocks.homeTimeline.scrollY['for-you']).toBe(2480);
    expect(mocks.homeTimeline.returnAnchors['for-you']).toBeNull();
    expect(mocks.homeTimeline.clearReturnAnchor).toHaveBeenCalledWith('for-you');
  });

  it('falls back safely and clears an anchor when its card is missing', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    mocks.homeTimeline.returnAnchors['for-you'] = {
      postId: 99,
      viewportTop: 150,
      fallbackScrollY: 2400,
    };
    currentScrollY = 0;
    mocks.route.name = 'PostDetail';
    mounted.state.showHome = false;
    await settle();
    scrollToMock.mockClear();

    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();
    await flushFrame();
    await flushFrame();

    expect(scrollToMock).toHaveBeenCalledWith({ top: 2400, behavior: 'auto' });
    expect(mocks.homeTimeline.returnAnchors['for-you']).toBeNull();
    expect(mocks.homeTimeline.clearReturnAnchor).toHaveBeenCalledWith('for-you');
  });

  it('restores the Following scroll independently of a For You anchor', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.returnAnchors['for-you'] = {
      postId: 42,
      viewportTop: 150,
      fallbackScrollY: 2400,
    };
    scrollToMock.mockClear();

    mocks.homeTimeline.activeTab = 'following';
    await settle();

    expect(scrollToMock).toHaveBeenLastCalledWith({ top: 900, behavior: 'auto' });
    expect(mocks.homeTimeline.returnAnchors['for-you']).toEqual({
      postId: 42,
      viewportTop: 150,
      fallbackScrollY: 2400,
    });
    expect(mocks.homeTimeline.returnAnchors.following).toBeNull();
  });

  it('cancels an old async restore when the user switches tabs', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    currentScrollY = 2400;
    const card = setCardTop(wrapper, () => 170);
    mocks.routeLeave?.({ name: 'PostDetail', params: { id: '42' } });
    mocks.route.name = 'PostDetail';
    mounted.state.showHome = false;
    await settle();

    let cardTop = 230;
    setCardElementTop(card, () => cardTop);
    scrollToMock.mockClear();
    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    await flushFrame();

    expect(scrollToMock).not.toHaveBeenCalledWith({ top: 2480, behavior: 'auto' });
    expect(scrollToMock).toHaveBeenLastCalledWith({ top: 900, behavior: 'auto' });
    expect(mocks.homeTimeline.returnAnchors['for-you']).toEqual({
      postId: 42,
      viewportTop: 170,
      fallbackScrollY: 2400,
    });
    expect(mocks.homeTimeline.returnAnchors.following).toBeNull();
    expect(mocks.homeTimeline.clearReturnAnchor).not.toHaveBeenCalled();
  });
});
