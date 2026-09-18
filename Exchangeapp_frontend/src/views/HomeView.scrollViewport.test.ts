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

vi.mock('element-plus/es/components/message/style/css', () => ({}));
import HomeView from './HomeView.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: null as any,
  homeTimeline: null as any,
  route: null as any,
  router: null as any,
  routeLeave: null as (() => unknown) | null,
  initialDocumentNavigation: {
    type: 'unknown',
    url: '',
  },
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

vi.mock('../router/documentNavigation', () => ({
  isInitialDocumentReloadForRoute: (fullPath: string) => (
    mocks.initialDocumentNavigation.type === 'reload'
    && mocks.initialDocumentNavigation.url === fullPath
  ),
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
  bookmarked: false,
  bookmarkStatus: 'ready',
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
    homeReselectVersion: 0,
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
    bookmarkPendingPostIds: new Set<number>(),
    pendingDeletePostIds: new Set<number>(),
    deleteErrors: new Map<number, string>(),
    setActiveTab: vi.fn((tab: FeedTab) => {
      timeline.activeTab = tab;
    }),
    setScrollTop: vi.fn((tab: FeedTab, value: number) => {
      scrollTop[tab] = value;
    }),
    requestHomeReselect: vi.fn(() => {
      timeline.homeReselectVersion += 1;
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
const originalMatchMedia = Object.getOwnPropertyDescriptor(window, 'matchMedia');

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

const installMatchMedia = (matches: boolean) => {
  const matchMedia = vi.fn((query: string) => ({
    matches,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: matchMedia,
  });
  return matchMedia;
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const installPanelScrollbarGeometry = (
  panel: HTMLElement,
  {
    left = 0,
    top = 0,
    width = 1000,
    height = 800,
    clientWidth = width,
  }: {
    left?: number;
    top?: number;
    width?: number;
    height?: number;
    clientWidth?: number;
  } = {},
) => {
  Object.defineProperty(panel, 'offsetWidth', {
    configurable: true,
    value: width,
  });
  Object.defineProperty(panel, 'clientWidth', {
    configurable: true,
    value: clientWidth,
  });
  Object.defineProperty(panel, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({
      x: left,
      y: top,
      left,
      top,
      right: left + width,
      bottom: top + height,
      width,
      height,
      toJSON: () => ({}),
    }),
  });
};

const dispatchPanelPointerEvent = (
  target: HTMLElement,
  type: 'pointerdown' | 'pointerup' | 'pointercancel',
  {
    clientX,
    clientY,
    pointerType = 'mouse',
  }: {
    clientX: number;
    clientY: number;
    pointerType?: string;
  },
) => {
  const event = new Event(type, {
    bubbles: true,
    cancelable: true,
  });
  Object.defineProperties(event, {
    clientX: { value: clientX },
    clientY: { value: clientY },
    pointerType: { value: pointerType },
  });
  target.dispatchEvent(event);
};

const mountHome = () => mount(HomeView, {
  global: {
    stubs: {
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
    mocks.initialDocumentNavigation.type = 'unknown';
    mocks.initialDocumentNavigation.url = '';
    mocks.route = reactive({ name: 'Home', fullPath: '/', query: {} });
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
    restoreProperty(window, 'matchMedia', originalMatchMedia);
    document.body.innerHTML = '';
  });


  it('resets a cold For You document reload to zero and rejects browser-only restoration', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';
    mocks.homeTimeline.scrollTop['for-you'] = 12000;

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);

    panel.scrollTop = 12000;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);

    panel.scrollTop = 9000;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
  });

  it('resets only Following on a cold Following document reload', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/?tab=following';
    mocks.route.fullPath = '/?tab=following';
    mocks.route.query = { tab: 'following' };
    mocks.homeTimeline.activeTab = 'following';
    mocks.homeTimeline.scrollTop['for-you'] = 2400;
    mocks.homeTimeline.scrollTop.following = 9000;

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(2400);

    panel.scrollTop = 9000;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(2400);
  });


  it('releases a cold reload top pin for a native scrollbar drag', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    installPanelScrollbarGeometry(panel);
    dispatchPanelPointerEvent(panel, 'pointerdown', {
      clientX: 995,
      clientY: 120,
    });

    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(500);
  });

  it('does not release a cold reload top pin for a normal content pointerdown', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    installPanelScrollbarGeometry(panel);
    dispatchPanelPointerEvent(panel, 'pointerdown', {
      clientX: 500,
      clientY: 120,
    });

    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
  });

  it('clears scrollbar drag intent on pointercancel', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    installPanelScrollbarGeometry(panel);
    dispatchPanelPointerEvent(panel, 'pointerdown', {
      clientX: 995,
      clientY: 120,
    });
    dispatchPanelPointerEvent(panel, 'pointercancel', {
      clientX: 995,
      clientY: 120,
    });

    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
  });

  it('does not release a cold reload top pin for Space on a button', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const button = document.createElement('button');
    panel.appendChild(button);
    button.dispatchEvent(new KeyboardEvent('keydown', {
      key: ' ',
      bubbles: true,
    }));

    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
  });

  it('does not release a cold reload top pin for ArrowDown inside a textarea', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const textarea = document.createElement('textarea');
    panel.appendChild(textarea);
    textarea.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'ArrowDown',
      bubbles: true,
    }));

    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
  });

  it('releases a cold reload top pin after explicit wheel scrolling', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';

    wrapper = mountHome();
    await settle();

    const panelWrapper = wrapper.get('.home-feed-panel');
    const panel = panelWrapper.element as HTMLElement;
    await panelWrapper.trigger('wheel');
    panel.scrollTop = 500;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(500);
  });

  it.each(['PageDown', 'ArrowDown', 'End', ' '])(
    'releases a cold reload top pin after %s keyboard scrolling',
    async (key) => {
      mocks.initialDocumentNavigation.type = 'reload';
      mocks.initialDocumentNavigation.url = '/';

      wrapper = mountHome();
      await settle();

      const panelWrapper = wrapper.get('.home-feed-panel');
      const panel = panelWrapper.element as HTMLElement;
      await panelWrapper.trigger('keydown', { key });
      panel.scrollTop = 500;
      panel.dispatchEvent(new Event('scroll'));
      await settle();

      expect(panel.scrollTop).toBe(500);
    },
  );

  it('does not classify Home as cold reload after the document reloaded on another route', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/search';
    mocks.homeTimeline.scrollTop['for-you'] = 2400;

    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(2400);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(2400);
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
    (originalPanel as HTMLElement).scrollTop = 0;

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
    panel.scrollTop = 0;

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

  it('routes an active For You click through the shared reselect pipeline', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 2400;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.setActiveTab.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="for-you"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.setActiveTab).not.toHaveBeenCalled();
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('refreshes For You through the shared reselect pipeline when at the top', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 0;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="for-you"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('routes an active Following click through the shared reselect pipeline', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.activeTab = 'following';
    mocks.route.query = { tab: 'following' };
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 1800;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();
    mocks.homeTimeline.setActiveTab.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="following"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.activeTab).toBe('following');
    expect(mocks.homeTimeline.setActiveTab).not.toHaveBeenCalled();
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('refreshes Following through the shared reselect pipeline when at the top', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.activeTab = 'following';
    mocks.route.query = { tab: 'following' };
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 0;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="following"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
    expect(mocks.route.query).toEqual({ tab: 'following' });
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('switches tabs with select without reselecting and preserves each scrollTop', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 2200;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.setActiveTab.mockClear();
    mocks.homeTimeline.setScrollTop.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="following"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 2200);
    expect(mocks.homeTimeline.setActiveTab).toHaveBeenCalledWith('following');
    expect(mocks.router.push).toHaveBeenCalledWith({
      name: 'Home',
      query: { tab: 'following' },
    });
    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);
    expect(panel.scrollTop).toBe(900);

    mocks.homeTimeline.setScrollTop.mockClear();
    mocks.homeTimeline.setActiveTab.mockClear();
    mocks.router.push.mockClear();
    await wrapper.get('[data-feed-tab="for-you"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('following', 900);
    expect(mocks.homeTimeline.setActiveTab).toHaveBeenCalledWith('for-you');
    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'Home', query: {} });
    expect(panel.scrollTop).toBe(2200);
  });

  it('keeps same-tab keyboard selection as select without reselecting', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.router.push.mockClear();

    await wrapper.get('[data-feed-tab="for-you"]').trigger('keydown', { key: 'Home' });
    await settle();

    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('scrolls a deep For You panel to the top without refreshing', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 2400;
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();
    mocks.homeTimeline.setScrollTop.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 0);
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);
    expect(scrollToMock).not.toHaveBeenCalled();
  });

  it('keeps For You at the top across sequential reselect-then-refresh actions', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 2400;
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.setScrollTop.mockClear();

    await wrapper.get('[data-feed-tab="for-you"]').trigger('click');
    await settle();

    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 0);
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);

    panel.scrollTop = 0;
    panelScrollTo.mockClear();
    mocks.homeTimeline.setScrollTop.mockClear();

    await wrapper.get('[data-feed-tab="for-you"]').trigger('click');
    await settle();

    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    expect(mocks.homeTimeline.setScrollTop).toHaveBeenCalledWith('for-you', 0);
    expect(panel.scrollTop).toBe(0);
  });

  it('refreshes For You when Home is reselected at the top', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 0;
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    expect(panel.scrollTop).toBe(0);
  });

  it('uses the eight-pixel threshold for Home reselect', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    mocks.homeTimeline.loadForYou.mockClear();

    panel.scrollTop = 8;
    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    expect(panel.scrollTop).toBe(0);

    mocks.homeTimeline.loadForYou.mockClear();
    panelScrollTo.mockClear();
    panel.scrollTop = 9;
    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();
    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
  });

  it('refreshes only the active Following feed when reselected at the top', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.activeTab = 'following';
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 0;
    mocks.homeTimeline.loadForYou.mockClear();
    mocks.homeTimeline.loadFollowing.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.activeTab).toBe('following');
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
    expect(panel.scrollTop).toBe(0);
  });

  it('suppresses a For You refresh while that feed is already loading', async () => {
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 0;
    mocks.homeTimeline.forYou.loading = true;
    mocks.homeTimeline.loadForYou.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);
  });

  it('suppresses a Following refresh while it is revalidating', async () => {
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.activeTab = 'following';
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panel.scrollTop = 0;
    mocks.homeTimeline.following.revalidating = true;
    mocks.homeTimeline.loadFollowing.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);
  });

  it('uses auto panel scrolling when reduced motion is preferred', async () => {
    installMatchMedia(true);
    wrapper = mountHome();
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    const panelScrollTo = vi.fn();
    Object.defineProperty(panel, 'scrollTo', {
      configurable: true,
      value: panelScrollTo,
    });
    panel.scrollTop = 1000;

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();

    expect(panelScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' });
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
