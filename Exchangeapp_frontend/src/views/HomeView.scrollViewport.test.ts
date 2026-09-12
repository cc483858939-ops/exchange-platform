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

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: null as any,
  homeTimeline: null as any,
  route: null as any,
  router: null as any,
  routeLeave: null as (() => unknown) | null,
  telemetry: {
    resetObservedCards: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
    observeFeedCard: vi.fn(),
    detachFeedCard: vi.fn(),
    unobserveFeedCard: vi.fn(),
    recordClick: vi.fn(),
    recordNotInterested: vi.fn(),
    notifyViewportChange: vi.fn(),
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
  onBeforeRouteLeave: (guard: () => unknown) => {
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
  template: '<article class="post-card-stub"></article>',
});

const makeHomeTimeline = () => {
  const scrollTop = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 900,
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
    scrollTop,
    likePendingPostIds: new Set<number>(),
    repostPendingPostIds: new Set<number>(),
    pendingDeletePostIds: new Set<number>(),
    deleteErrors: new Map<number, string>(),
    setActiveTab: vi.fn((tab: FeedTab) => {
      timeline.activeTab = tab;
    }),
    setScrollTop: vi.fn((tab: FeedTab, value: number) => {
      scrollTop[tab] = value;
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

let windowScrollY = 0;
const scrollToMock = vi.fn();

const installWindowScrollMocks = () => {
  windowScrollY = 0;
  scrollToMock.mockClear();
  Object.defineProperty(window, 'scrollY', {
    configurable: true,
    get: () => windowScrollY,
  });
  Object.defineProperty(window, 'scrollTo', {
    configurable: true,
    writable: true,
    value: scrollToMock,
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

describe('HomeView scroll viewport ownership', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    installWindowScrollMocks();
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
    document.body.innerHTML = '';
  });

  it('saves the Home feed panel scrollTop before leaving', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 2400;
    windowScrollY = 0;

    mocks.routeLeave?.();

    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(2400);
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 2400);
  });

  it('never uses window scroll position for Home state or restoration', async () => {
    mocks.homeTimeline.scrollTop['for-you'] = 2400;
    windowScrollY = 777;
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(2400);
    expect(scrollToMock).not.toHaveBeenCalled();

    panel.scrollTop = 3100;
    mocks.routeLeave?.();

    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(3100);
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 3100);
  });

  it('retains the same cached Home scroll surface through KeepAlive', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    const originalPanel = wrapper.get('.home-feed-panel').element;
    (originalPanel as HTMLElement).scrollTop = 2400;
    mocks.routeLeave?.();

    mounted.state.showHome = false;
    await settle();
    mounted.state.showHome = true;
    await settle();

    const restoredPanel = wrapper.get('.home-feed-panel').element;
    expect(restoredPanel).toBe(originalPanel);
    expect((restoredPanel as HTMLElement).scrollTop).toBe(2400);
  });

  it('keeps PostDetail window scrolling independent from Home panel scrollTop', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 2400;
    mocks.routeLeave?.();

    windowScrollY = 1300;
    mounted.state.showHome = false;
    await settle();
    mounted.state.showHome = true;
    await settle();

    expect((wrapper.get('.home-feed-panel').element as HTMLElement).scrollTop).toBe(2400);
    expect(scrollToMock).not.toHaveBeenCalled();
  });

  it('restores For You and Following panel positions independently', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 2400;

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(2400);
    expect(panel.scrollTop).toBe(900);

    mocks.homeTimeline.activeTab = 'for-you';
    await settle();
    expect(panel.scrollTop).toBe(2400);
    expect(mocks.homeTimeline.scrollTop.following).toBe(900);
  });

  it('notifies recommendation telemetry for inner feed scrolling', async () => {
    wrapper = mountHome();
    await settle();

    await wrapper.get('.home-feed-panel').trigger('scroll');

    expect(mocks.telemetry.notifyViewportChange).toHaveBeenCalledTimes(1);
  });

  it('saves panel scrollTop when Home is destroyed', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 1300;

    wrapper.unmount();
    wrapper = null;

    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 1300);
    expect(scrollToMock).not.toHaveBeenCalled();
  });
});
