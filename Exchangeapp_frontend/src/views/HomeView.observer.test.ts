// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost, FeedTab } from '../types/Feed';

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];

  readonly observed = new Set<Element>();
  readonly observe = vi.fn((element: Element) => {
    this.observed.add(element);
  });
  readonly disconnect = vi.fn(() => {
    this.observed.clear();
  });
  readonly rootMargin: string;
  private readonly callback: IntersectionObserverCallback;

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.callback = callback;
    this.rootMargin = options?.rootMargin || '';
    FakeIntersectionObserver.instances.push(this);
  }

  trigger(isIntersecting = true) {
    const target = this.observed.values().next().value as Element | undefined;
    if (!target) return;
    this.callback([
      { isIntersecting, target } as IntersectionObserverEntry,
    ], this as unknown as IntersectionObserver);
  }
}

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: null as any,
  homeTimeline: null as any,
  route: null as any,
  router: null as any,
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
}));

const author = {
  id: 7,
  username: 'viewer',
  display_name: 'Viewer',
  avatar_url: '',
};

const feedPost: FeedPost = {
  id: 1,
  author,
  content: 'Post 1',
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
};

const recommendationItem = {
  recommendation: {
    post: { id: 1 },
    score: 1,
  },
  post: feedPost,
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const mountHomeView = async () => {
  const module = await import('./HomeView.vue');
  const wrapper = mount(module.default, {
    global: {
      stubs: {
        FeedTabs: { template: '<div />' },
        PostCard: { template: '<div />' },
        AppIcon: { template: '<span />' },
        MobileHomeHeader: { template: '<div />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  await settle();
  return wrapper;
};

const mountKeepAliveHomeView = async () => {
  const module = await import('./HomeView.vue');
  const state = reactive({ showHome: true });
  const Placeholder = defineComponent({
    name: 'PostDetailView',
    template: '<div data-placeholder />',
  });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { include: 'HomeView', max: 1 }, {
        default: () => (state.showHome ? h(module.default) : h(Placeholder)),
      });
    },
  });
  const wrapper = mount(Host, {
    global: {
      stubs: {
        FeedTabs: { template: '<div />' },
        PostCard: { template: '<div />' },
        AppIcon: { template: '<span />' },
        MobileHomeHeader: { template: '<div />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  await settle();
  return { wrapper, state };
};

describe('HomeView For You pagination observer', () => {
  beforeEach(() => {
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    FakeIntersectionObserver.instances = [];
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: author,
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
    });
    mocks.homeTimeline = reactive({
      activeTab: 'for-you' as FeedTab,
      forYou: reactive({
        items: [recommendationItem],
        loading: false,
        error: false,
        loaded: true,
        loadingMore: false,
        loadMoreError: false,
        depleted: false,
      }),
      following: reactive({
        items: [],
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
      scrollY: { 'for-you': 0, following: 0 },
      likePendingPostIds: new Set<number>(),
      repostPendingPostIds: new Set<number>(),
      pendingDeletePostIds: new Set<number>(),
      deleteErrors: new Map<number, string>(),
      setActiveTab: vi.fn((tab: FeedTab) => {
        mocks.homeTimeline.activeTab = tab;
      }),
      setScrollY: vi.fn(),
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
    mocks.route = reactive({ name: 'Home', query: {} });
    mocks.router = {
      push: vi.fn().mockResolvedValue(undefined),
      replace: vi.fn().mockResolvedValue(undefined),
    };
    Object.values(mocks.telemetry).forEach((mock) => mock.mockClear());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('observes the For You sentinel with an 800px prefetch margin', async () => {
    const wrapper = await mountHomeView();

    expect(FakeIntersectionObserver.instances).toHaveLength(1);
    const observer = FakeIntersectionObserver.instances[0];
    expect(observer.rootMargin).toBe('800px 0px');
    expect(observer.observed.size).toBe(1);

    observer.trigger();
    expect(mocks.homeTimeline.loadMoreForYou).toHaveBeenCalledTimes(1);

    wrapper.unmount();
    expect(observer.disconnect).toHaveBeenCalledTimes(1);
  });

  it('does not create an observer while paging or after exhaustion', async () => {
    mocks.homeTimeline.forYou.loadingMore = true;
    const loadingWrapper = await mountHomeView();
    expect(FakeIntersectionObserver.instances).toHaveLength(0);
    loadingWrapper.unmount();

    mocks.homeTimeline.forYou.loadingMore = false;
    mocks.homeTimeline.forYou.depleted = true;
    const depletedWrapper = await mountHomeView();
    expect(FakeIntersectionObserver.instances).toHaveLength(0);
    depletedWrapper.unmount();
  });

  it('disconnects on a tab switch and recreates when returning to For You', async () => {
    const wrapper = await mountHomeView();
    const firstObserver = FakeIntersectionObserver.instances[0];

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    expect(firstObserver.disconnect).toHaveBeenCalledTimes(1);

    mocks.homeTimeline.activeTab = 'for-you';
    await settle();
    expect(FakeIntersectionObserver.instances).toHaveLength(2);
    expect(FakeIntersectionObserver.instances[1].observed.size).toBe(1);

    wrapper.unmount();
  });

  it('does not auto-retry while a load-more error is visible', async () => {
    mocks.homeTimeline.forYou.loadMoreError = true;
    const wrapper = await mountHomeView();

    expect(FakeIntersectionObserver.instances).toHaveLength(0);
    expect(mocks.homeTimeline.loadMoreForYou).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('Could not load more recommendations.');

    wrapper.find('button.home-state__primary').trigger('click');
    expect(mocks.homeTimeline.retryForYouLoadMore).toHaveBeenCalledTimes(1);
  });

  it('renders a manual load-more button when IntersectionObserver is unavailable', async () => {
    vi.resetModules();
    vi.stubGlobal('IntersectionObserver', undefined);
    const wrapper = await mountHomeView();

    const button = wrapper.find('button.home-state__primary');
    expect(button.exists()).toBe(true);
    expect(button.text()).toContain('Load more recommendations');

    await button.trigger('click');
    expect(mocks.homeTimeline.loadMoreForYou).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});

describe('HomeView KeepAlive lifecycle', () => {
  beforeEach(() => {
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    FakeIntersectionObserver.instances = [];
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: author,
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
    });
    mocks.homeTimeline = reactive({
      activeTab: 'for-you' as FeedTab,
      forYou: reactive({
        items: [recommendationItem],
        loading: false,
        error: false,
        loaded: true,
        loadingMore: false,
        loadMoreError: false,
        depleted: false,
      }),
      following: reactive({
        items: [],
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
      scrollY: { 'for-you': 0, following: 0 },
      likePendingPostIds: new Set<number>(),
      repostPendingPostIds: new Set<number>(),
      pendingDeletePostIds: new Set<number>(),
      deleteErrors: new Map<number, string>(),
      setActiveTab: vi.fn((tab: FeedTab) => {
        mocks.homeTimeline.activeTab = tab;
      }),
      setScrollY: vi.fn(),
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
    mocks.route = reactive({ name: 'Home', query: {} });
    mocks.router = {
      push: vi.fn().mockResolvedValue(undefined),
      replace: vi.fn().mockResolvedValue(undefined),
    };
    Object.values(mocks.telemetry).forEach((mock) => mock.mockClear());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('pauses on deactivation, retains card bindings, and restores observers on activation', async () => {
    const { wrapper, state } = await mountKeepAliveHomeView();
    const firstObserver = FakeIntersectionObserver.instances[0];
    mocks.telemetry.resetObservedCards.mockClear();
    mocks.telemetry.flush.mockClear();
    mocks.telemetry.observeFeedCard.mockClear();

    state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();

    expect(firstObserver.disconnect).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.setScrollY).toHaveBeenCalledWith('for-you', expect.any(Number));
    expect(mocks.telemetry.resetObservedCards).toHaveBeenCalledTimes(1);
    expect(mocks.telemetry.flush).toHaveBeenCalledWith(false);
    expect(mocks.telemetry.observeFeedCard).not.toHaveBeenCalled();

    mocks.route.name = 'Home';
    mocks.route.query = {};
    state.showHome = true;
    await settle();

    expect(FakeIntersectionObserver.instances).toHaveLength(2);
    expect(mocks.telemetry.observeFeedCard).toHaveBeenCalled();
    wrapper.unmount();
    expect(firstObserver.disconnect).toHaveBeenCalledTimes(1);
  });

  it('keeps a Following deep link while Home is inactive', async () => {
    mocks.route.query = { tab: 'following' };
    const { wrapper, state } = await mountKeepAliveHomeView();
    expect(mocks.homeTimeline.activeTab).toBe('following');

    state.showHome = false;
    mocks.route.name = 'PostDetail';
    mocks.route.query = {};
    await settle();
    expect(mocks.homeTimeline.activeTab).toBe('following');

    mocks.route.name = 'Home';
    mocks.route.query = { tab: 'following' };
    state.showHome = true;
    await settle();

    expect(mocks.homeTimeline.activeTab).toBe('following');
    expect(mocks.router.replace).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('does not create observers or load pages from hidden Home state changes', async () => {
    const { wrapper, state } = await mountKeepAliveHomeView();
    const firstObserver = FakeIntersectionObserver.instances[0];
    state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();

    firstObserver.trigger();
    mocks.homeTimeline.forYou.loadingMore = true;
    mocks.homeTimeline.forYou.loadingMore = false;
    await settle();

    expect(mocks.homeTimeline.loadMoreForYou).not.toHaveBeenCalled();
    expect(FakeIntersectionObserver.instances).toHaveLength(1);
    wrapper.unmount();
  });

  it('does not rebind telemetry for hidden item changes, then rebinds on activation', async () => {
    const { wrapper, state } = await mountKeepAliveHomeView();
    state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();
    mocks.telemetry.observeFeedCard.mockClear();
    mocks.telemetry.detachFeedCard.mockClear();
    mocks.telemetry.unobserveFeedCard.mockClear();

    mocks.homeTimeline.forYou.items.push({
      recommendation: { post: { id: 2 }, score: 0.5 },
      post: { ...feedPost, id: 2 },
    });
    await settle();

    expect(mocks.telemetry.observeFeedCard).not.toHaveBeenCalled();
    expect(mocks.telemetry.detachFeedCard).not.toHaveBeenCalled();
    expect(mocks.telemetry.unobserveFeedCard).not.toHaveBeenCalled();

    mocks.route.name = 'Home';
    state.showHome = true;
    await settle();
    expect(mocks.telemetry.observeFeedCard).toHaveBeenCalled();
    wrapper.unmount();
  });
});
