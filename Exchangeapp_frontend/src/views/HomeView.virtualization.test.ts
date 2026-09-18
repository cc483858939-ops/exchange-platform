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
  postViewTelemetry: {
    releaseFeedViewSession: vi.fn(),
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

vi.mock('../services/postViewTelemetry', () => ({
  getPostViewTelemetry: () => mocks.postViewTelemetry,
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

const makeRecommendation = (id: number) => {
  const tracking = { token: `recommendation-${id}`, source: 'home-test' };
  return {
    recommendation: {
      post: { id },
      score: 1,
      tracking,
    },
    post: makePost(id),
  };
};

const PostCardStub = defineComponent({
  props: {
    post: {
      type: Object,
      required: true,
    },
    viewSessionKey: {
      type: String,
      required: false,
    },
  },
  emits: ['notInterested'],
  template: `
    <article class="post-card-stub" :data-post-id="post.id">
      <button
        class="test-not-interested"
        type="button"
        @click="$emit('notInterested', post.id)"
      >
        Not interested
      </button>
    </article>
  `,
});

const makeTimeline = ({
  forYouItems = [],
  followingItems = [],
  recentlyPublishedPosts = [],
  forYouLoading = false,
}: {
  forYouItems?: any[];
  followingItems?: FeedPost[];
  recentlyPublishedPosts?: FeedPost[];
  forYouLoading?: boolean;
} = {}) => {
  const scrollTop = reactive<Record<FeedTab, number>>({
    'for-you': 0,
    following: 0,
  });
  const timeline: any = reactive({
    activeTab: 'for-you' as FeedTab,
    homeReselectVersion: 0,
    forYou: reactive({
      items: forYouItems,
      loading: forYouLoading,
      error: false,
      loaded: true,
      loadingMore: false,
      loadMoreError: false,
      depleted: true,
    }),
    following: reactive({
      items: followingItems,
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
    requestHomeReselect: vi.fn(),
    loadForYou: vi.fn().mockResolvedValue(undefined),
    loadMoreForYou: vi.fn().mockResolvedValue(undefined),
    retryForYouLoadMore: vi.fn(),
    loadFollowing: vi.fn().mockResolvedValue(undefined),
    loadMoreFollowing: vi.fn().mockResolvedValue(undefined),
    revalidateFollowing: vi.fn().mockResolvedValue(undefined),
    retryFollowingLoadMore: vi.fn(),
    toggleLike: vi.fn().mockResolvedValue('succeeded'),
    toggleRepost: vi.fn().mockResolvedValue('succeeded'),
    toggleBookmark: vi.fn().mockResolvedValue('succeeded'),
    deletePost: vi.fn().mockResolvedValue(true),
    dismissRecommendation: vi.fn(),
  });
  timeline.dismissRecommendation.mockImplementation((postID: number) => {
    timeline.forYou.items = timeline.forYou.items.filter(
      (item: { post: FeedPost }) => item.post.id !== postID,
    );
  });
  return timeline;
};

const mountHome = () => mount(HomeView, {
  attachTo: document.body,
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
    attachTo: document.body,
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

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const settleRefreshFrame = async () => {
  await settle();
  if (typeof window.requestAnimationFrame === 'function') {
    await new Promise<void>((resolve) => {
      window.requestAnimationFrame(() => resolve());
    });
  }
  await settle();
};

const deferred = <T>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const originalOffsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
const originalClientHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight');
const originalClientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
const originalScrollHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight');
const originalElementScrollTo = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo');

let rowHeights = new Map<number, number>();
let followingRowHeights = new Map<number, number>();
let defaultRowHeight = 180;
let panelWidth = 1000;

type TestResizeEntry = {
  target: Element;
  borderBoxSize: Array<{ blockSize: number; inlineSize: number }>;
};

class TestResizeObserver {
  static instances: TestResizeObserver[] = [];

  readonly observed = new Set<Element>();

  constructor(private readonly callback: (entries: TestResizeEntry[]) => void) {
    TestResizeObserver.instances.push(this);
  }

  observe(element: Element) {
    this.observed.add(element);
  }

  unobserve(element: Element) {
    this.observed.delete(element);
  }

  disconnect() {
    this.observed.clear();
  }

  trigger(element: Element, blockSize: number, inlineSize = panelWidth) {
    this.callback([{
      target: element,
      borderBoxSize: [{ blockSize, inlineSize }],
    }]);
  }
}

const readOriginalDimension = (
  descriptor: PropertyDescriptor | undefined,
  element: HTMLElement,
) => descriptor?.get?.call(element) ?? 0;

const installGeometry = () => {
  rowHeights = new Map<number, number>();
  followingRowHeights = new Map<number, number>();
  panelWidth = 1000;
  TestResizeObserver.instances = [];
  vi.stubGlobal('ResizeObserver', TestResizeObserver);

  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('home-feed-panel')) {
        return 800;
      }
      if (element.classList.contains('home-virtual-row')) {
        const feed = element.closest<HTMLElement>('[data-virtual-feed]')?.dataset.virtualFeed;
        const heights = feed === 'following' ? followingRowHeights : rowHeights;
        return heights.get(Number(element.dataset.index)) ?? defaultRowHeight;
      }
      return readOriginalDimension(originalOffsetHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('home-feed-panel')
        || element.classList.contains('home-virtual-row')) {
        return panelWidth;
      }
      return readOriginalDimension(originalOffsetWidth, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('home-feed-panel')) {
        return 800;
      }
      return readOriginalDimension(originalClientHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('home-feed-panel')) {
        return panelWidth;
      }
      return readOriginalDimension(originalClientWidth, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('home-feed-panel')) {
        const virtualList = element.querySelector<HTMLElement>('.home-virtual-list');
        return Number.parseFloat(virtualList?.style.height ?? '0') || 0;
      }
      return readOriginalDimension(originalScrollHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    configurable: true,
    value(this: HTMLElement, options: ScrollToOptions | number) {
      const top = typeof options === 'number' ? options : options.top;
      if (typeof top === 'number') {
        this.scrollTop = top;
        this.dispatchEvent(new Event('scroll'));
      }
    },
  });
};

const restoreGeometry = () => {
  for (const [property, descriptor] of [
    ['offsetHeight', originalOffsetHeight],
    ['offsetWidth', originalOffsetWidth],
    ['clientHeight', originalClientHeight],
    ['clientWidth', originalClientWidth],
    ['scrollHeight', originalScrollHeight],
  ] as const) {
    if (descriptor) {
      Object.defineProperty(HTMLElement.prototype, property, descriptor);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, property);
    }
  }
  if (originalElementScrollTo) {
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', originalElementScrollTo);
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo');
  }
  vi.unstubAllGlobals();
};

const mountedPostIDs = (wrapper: ReturnType<typeof mount>) => wrapper
  .findAll('.post-card-stub')
  .map((card) => Number(card.attributes('data-post-id')));

const mountedPostSessionKey = (wrapper: ReturnType<typeof mount>, postID: number) => wrapper
  .findAllComponents(PostCardStub)
  .find((card) => (card.props('post') as FeedPost).id === postID)
  ?.props('viewSessionKey');

const forYouHeightPattern = [180, 520, 240, 700, 190, 640, 300, 460];
const followingHeightPattern = [260, 680, 210, 580, 340, 760, 220, 430];

const setHeterogeneousHeights = (count: number) => {
  for (let index = 0; index < count; index += 1) {
    rowHeights.set(index, forYouHeightPattern[index % forYouHeightPattern.length]);
    followingRowHeights.set(index, followingHeightPattern[index % followingHeightPattern.length]);
  }
};

const scrollPanel = async (wrapper: ReturnType<typeof mount>, top: number) => {
  const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
  panel.scrollTop = top;
  panel.dispatchEvent(new Event('scroll'));
  await settle();
};

const virtualListTotalSize = (
  wrapper: ReturnType<typeof mount>,
  feed: FeedTab,
) => Number.parseFloat(
  wrapper
    .get(`[data-virtual-feed="${feed}"]`)
    .attributes('style')
    ?.match(/height: ([\d.]+)px/)?.[1] ?? '0',
);

const resizeRenderedRow = async (
  wrapper: ReturnType<typeof mount>,
  feed: FeedTab,
  index: number,
  blockSize: number,
) => {
  const row = wrapper
    .get(`[data-virtual-feed="${feed}"] .home-virtual-row[data-index="${index}"]`)
    .element as HTMLElement;
  const heights = feed === 'following' ? followingRowHeights : rowHeights;
  heights.set(index, blockSize);

  const observers = TestResizeObserver.instances
    .filter((observer) => observer.observed.has(row));
  expect(observers.length).toBeGreaterThan(0);
  observers.forEach((observer) => observer.trigger(row, blockSize));
  await settle();
};

describe('HomeView virtualization', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    installGeometry();
    defaultRowHeight = 180;
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: viewer,
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
    });
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
    });
    mocks.initialDocumentNavigation.type = 'unknown';
    mocks.initialDocumentNavigation.url = '';
    mocks.route = reactive({ name: 'Home', fullPath: '/', query: {} });
    mocks.router = {
      push: vi.fn().mockResolvedValue(undefined),
      replace: vi.fn().mockResolvedValue(undefined),
    };
    mocks.routeLeave = null;
    mocks.postViewTelemetry.releaseFeedViewSession.mockClear();
    Object.values(mocks.telemetry).forEach((mock) => mock.mockClear());
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    restoreGeometry();
    document.body.innerHTML = '';
  });


  it('keeps a cold reload pinned through late virtual measurement and synthetic displacement', async () => {
    mocks.initialDocumentNavigation.type = 'reload';
    mocks.initialDocumentNavigation.url = '/';
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);

    const firstRow = wrapper.get('[data-virtual-feed="for-you"] .home-virtual-row[data-index="0"]').element;
    rowHeights.set(0, (rowHeights.get(0) ?? defaultRowHeight) + 420);
    TestResizeObserver.instances
      .filter((observer) => observer.observed.has(firstRow))
      .forEach((observer) => observer.trigger(firstRow, rowHeights.get(0)!));
    await settle();

    panel.scrollTop = 10000;
    panel.dispatchEvent(new Event('scroll'));
    await settle();

    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
  });

  it('keeps For You mounted rows bounded while source items and measured ranges grow', async () => {
    wrapper = mountHome();
    await settle();

    expect(mocks.homeTimeline.forYou.items).toHaveLength(300);
    expect(mountedPostIDs(wrapper).length).toBeLessThan(50);
    expect(mountedPostIDs(wrapper)).toContain(1);
    expect(mountedPostIDs(wrapper)).not.toContain(300);

    await scrollPanel(wrapper, 25000);

    const deepIDs = mountedPostIDs(wrapper);
    expect(deepIDs.length).toBeLessThan(50);
    expect(deepIDs).not.toContain(1);
    expect(deepIDs.some((id) => id >= 60)).toBe(true);

    mocks.homeTimeline.forYou.items.push(
      ...Array.from({ length: 20 }, (_, index) => makeRecommendation(index + 301)),
    );
    await settle();

    expect(mocks.homeTimeline.forYou.items).toHaveLength(320);
    expect(mountedPostIDs(wrapper).length).toBeLessThan(50);
    expect(new Set(mountedPostIDs(wrapper)).size).toBe(mountedPostIDs(wrapper).length);
  });

  it('keeps Following mounted rows bounded and changes the range after deep scrolling', async () => {
    mocks.homeTimeline = makeTimeline({
      forYouItems: [],
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    mocks.homeTimeline.activeTab = 'following';
    mocks.route.query = { tab: 'following' };
    wrapper = mountHome();
    await settle();

    expect(mountedPostIDs(wrapper).length).toBeLessThan(50);
    expect(mountedPostIDs(wrapper)).toContain(1);

    await scrollPanel(wrapper, 25000);

    const deepIDs = mountedPostIDs(wrapper);
    expect(deepIDs.length).toBeLessThan(50);
    expect(deepIDs).not.toContain(1);
    expect(deepIDs.some((id) => id >= 60)).toBe(true);
  });

  it('preserves logical row order, deduplication, and dynamic measured offsets', async () => {
    const recentPosts = [makePost(1), makePost(2)];
    mocks.feedStore.recentlyPublishedPosts = recentPosts;
    mocks.homeTimeline = makeTimeline({
      forYouItems: [makeRecommendation(2), makeRecommendation(3)],
      recentlyPublishedPosts: recentPosts,
      forYouLoading: true,
    });
    rowHeights.set(0, 180);
    wrapper = mountHome();
    await settle();

    const rows = wrapper.findAll('.home-virtual-row');
    expect(rows).toHaveLength(4);
    expect(rows.map((row) => row.attributes('data-index'))).toEqual(['0', '1', '2', '3']);
    expect(rows[2].text()).toContain('Loading recommendations...');
    expect(mountedPostIDs(wrapper)).toEqual([1, 2, 3]);

    const virtualList = wrapper.get('[data-virtual-feed="for-you"]');
    const initialTotalSize = Number.parseFloat(virtualList.attributes('style')?.match(/height: ([\d.]+)px/)?.[1] ?? '0');
    const laterRowStart = Number.parseFloat(rows[3].attributes('style')?.match(/translateY\(([\d.]+)px\)/)?.[1] ?? '0');
    rowHeights.set(0, 520);
    const measuredRow = rows[0].element;
    TestResizeObserver.instances
      .filter((observer) => observer.observed.has(measuredRow))
      .forEach((observer) => observer.trigger(measuredRow, 520));
    await settle();

    const updatedTotalSize = Number.parseFloat(virtualList.attributes('style')?.match(/height: ([\d.]+)px/)?.[1] ?? '0');
    const updatedRows = wrapper.findAll('.home-virtual-row');
    const updatedLaterRowStart = Number.parseFloat(updatedRows[3].attributes('style')?.match(/translateY\(([\d.]+)px\)/)?.[1] ?? '0');
    expect(updatedTotalSize).not.toBe(initialTotalSize);
    expect(updatedLaterRowStart).not.toBe(laterRowStart);
    expect(new Set(updatedRows.map((row) => row.attributes('data-index'))).size).toBe(updatedRows.length);
  });

  it('detaches virtualized recommendation rows without logically unobserving them', async () => {
    wrapper = mountHome();
    await settle();
    mocks.telemetry.observeFeedCard.mockClear();
    mocks.telemetry.detachFeedCard.mockClear();
    mocks.telemetry.unobserveFeedCard.mockClear();

    await scrollPanel(wrapper, 25000);

    expect(mocks.telemetry.detachFeedCard).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ token: 'recommendation-1' }),
    );
    expect(mocks.telemetry.unobserveFeedCard).not.toHaveBeenCalledWith(
      1,
      expect.objectContaining({ token: 'recommendation-1' }),
    );

    await scrollPanel(wrapper, 0);

    expect(mocks.telemetry.observeFeedCard).toHaveBeenCalledWith(
      expect.anything(),
      1,
      expect.objectContaining({ token: 'recommendation-1' }),
    );

    await wrapper.get('[data-post-id="1"] .test-not-interested').trigger('click');
    await settle();

    expect(mocks.telemetry.unobserveFeedCard).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ token: 'recommendation-1' }),
    );
    expect(mocks.homeTimeline.forYou.items.some((item: { post: FeedPost }) => item.post.id === 1)).toBe(false);
  });

  it('tears down replaced recommendation refs with the captured old item', async () => {
    const oldItem = makeRecommendation(42);
    const freshItem = makeRecommendation(99);
    mocks.homeTimeline = makeTimeline({
      forYouItems: [oldItem],
    });
    wrapper = mountHome();
    await settle();

    expect(wrapper.find('.recommendation-card-wrapper').exists()).toBe(true);
    expect(mountedPostIDs(wrapper)).toContain(42);

    mocks.telemetry.detachFeedCard.mockClear();
    mocks.telemetry.unobserveFeedCard.mockClear();
    mocks.homeTimeline.forYou.items = [];
    mocks.homeTimeline.forYou.loading = true;
    mocks.homeTimeline.forYou.loaded = false;

    await settle();

    expect(wrapper.find('.recommendation-card-wrapper').exists()).toBe(false);
    expect(mocks.telemetry.detachFeedCard).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ token: 'recommendation-42' }),
    );
    expect(mocks.telemetry.unobserveFeedCard).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ token: 'recommendation-42' }),
    );

    mocks.telemetry.observeFeedCard.mockClear();
    mocks.homeTimeline.forYou.items = [freshItem];
    mocks.homeTimeline.forYou.loading = false;
    mocks.homeTimeline.forYou.loaded = true;
    mocks.homeTimeline.forYou.error = false;

    await settle();

    expect(wrapper.find('.recommendation-card-wrapper').exists()).toBe(true);
    expect(mountedPostIDs(wrapper)).toContain(99);
    expect(mocks.telemetry.observeFeedCard).toHaveBeenCalledWith(
      expect.anything(),
      99,
      expect.objectContaining({ token: 'recommendation-99' }),
    );
  });

  it('restores the same heterogeneous For You region after the cached DOM is reset', async () => {
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
    });
    setHeterogeneousHeights(300);
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();

    const originalPanel = wrapper.get('.home-feed-panel').element as HTMLElement;
    await scrollPanel(wrapper, 25000);
    const deepForYouIDs = mountedPostIDs(wrapper);
    expect(deepForYouIDs.length).toBeGreaterThan(0);
    expect(deepForYouIDs.some((id) => id >= 50)).toBe(true);
    expect(deepForYouIDs.length).toBeLessThan(50);

    mocks.routeLeave?.();
    const savedForYouOffset = mocks.homeTimeline.scrollTop['for-you'];
    expect(savedForYouOffset).toBe(25000);
    originalPanel.scrollTop = 0;
    mounted.state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();

    const hiddenRow = originalPanel.querySelector<HTMLElement>('.home-virtual-row');
    if (hiddenRow) {
      TestResizeObserver.instances
        .filter((observer) => observer.observed.has(hiddenRow))
        .forEach((observer) => observer.trigger(hiddenRow, 0));
    }

    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();

    const restoredForYouPanel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(restoredForYouPanel).toBe(originalPanel);
    expect(restoredForYouPanel.scrollTop).toBe(savedForYouOffset);
    expect(mountedPostIDs(wrapper)).toEqual(deepForYouIDs);
    expect(mountedPostIDs(wrapper).length).toBeLessThan(50);
    expect(mountedPostIDs(wrapper).some((id) => id <= 10)).toBe(false);
  });

  it('keeps separate heterogeneous measurement regions for For You and Following', async () => {
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();

    await scrollPanel(wrapper, 25000);
    const forYouDeepIDs = mountedPostIDs(wrapper);
    expect(forYouDeepIDs.some((id) => id >= 50)).toBe(true);
    expect((wrapper.get('.home-feed-panel').element as HTMLElement).scrollTop).toBe(25000);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    const forYouSessionKey = mountedPostSessionKey(wrapper, forYouDeepIDs[0]);

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(25000);
    await scrollPanel(wrapper, 25000);
    const followingDeepIDs = mountedPostIDs(wrapper);
    expect(followingDeepIDs.some((id) => id >= 40)).toBe(true);

    mocks.homeTimeline.activeTab = 'for-you';
    await settle();
    expect(mountedPostIDs(wrapper)).toEqual(forYouDeepIDs);
    expect(mountedPostSessionKey(wrapper, forYouDeepIDs[0])).toBe(forYouSessionKey);

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    expect(mountedPostIDs(wrapper)).toEqual(followingDeepIDs);
  });

  it('invalidates both virtualizers after a real panel width change', async () => {
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();

    const virtualList = wrapper.get('[data-virtual-feed="for-you"]');
    const initialTotalSize = Number.parseFloat(
      virtualList.attributes('style')?.match(/height: ([\d.]+)px/)?.[1] ?? '0',
    );
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    panelWidth = 900;
    for (let index = 0; index < 20; index += 1) {
      rowHeights.set(index, forYouHeightPattern[index % forYouHeightPattern.length] + 100);
    }
    TestResizeObserver.instances
      .filter((observer) => observer.observed.has(panel))
      .forEach((observer) => observer.trigger(panel, 800, 900));
    await settle();

    const resizedTotalSize = Number.parseFloat(
      virtualList.attributes('style')?.match(/height: ([\d.]+)px/)?.[1] ?? '0',
    );
    expect(resizedTotalSize).not.toBe(initialTotalSize);
  });

  it('keeps a refreshed heterogeneous For You feed anchored at the top after two reselections', async () => {
    const refresh = deferred<void>();
    const freshItems = Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1001));
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    mocks.homeTimeline.scrollTop.following = 4321;
    mocks.homeTimeline.loadForYou.mockImplementation(async (force = false) => {
      if (!force) {
        return;
      }
      mocks.homeTimeline.forYou.items = [];
      mocks.homeTimeline.forYou.loading = true;
      mocks.homeTimeline.forYou.loaded = false;
      await refresh.promise;
      mocks.homeTimeline.forYou.items = freshItems;
      mocks.homeTimeline.forYou.loaded = true;
      mocks.homeTimeline.forYou.loading = false;
      mocks.homeTimeline.forYou.error = false;
    });
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.loadForYou.mockClear();

    await scrollPanel(wrapper, 25000);
    const deepIDs = mountedPostIDs(wrapper);
    expect(deepIDs.some((id) => id >= 50)).toBe(true);

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    expect(mocks.homeTimeline.loadForYou).not.toHaveBeenCalledWith(true);

    mocks.homeTimeline.homeReselectVersion += 1;
    await nextTick();
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.forYou.items).toHaveLength(0);
    expect(panel.scrollTop).toBe(0);

    refresh.resolve(undefined);
    await settleRefreshFrame();

    const refreshedIDs = mountedPostIDs(wrapper);
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(4321);
    expect(refreshedIDs).toContain(1001);
    expect(refreshedIDs.every((id) => id >= 1001 && id <= 1300)).toBe(true);
    expect(refreshedIDs.some((id) => deepIDs.includes(id))).toBe(false);
    expect(refreshedIDs.length).toBeLessThan(50);

    const totalSizeBeforeLateMeasure = virtualListTotalSize(wrapper, 'for-you');
    const lateHeight = (rowHeights.get(0) ?? defaultRowHeight) + 360;
    await resizeRenderedRow(wrapper, 'for-you', 0, lateHeight);

    expect(virtualListTotalSize(wrapper, 'for-you')).not.toBe(totalSizeBeforeLateMeasure);
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
  });

  it('keeps a refreshed heterogeneous Following feed anchored at the top after two reselections', async () => {
    const refresh = deferred<void>();
    const freshItems = Array.from({ length: 300 }, (_, index) => makePost(index + 2001));
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    mocks.homeTimeline.activeTab = 'following';
    mocks.homeTimeline.scrollTop['for-you'] = 5432;
    mocks.route.query = { tab: 'following' };
    mocks.homeTimeline.loadFollowing.mockImplementation(async (force = false) => {
      if (!force) {
        return;
      }
      mocks.homeTimeline.following.items = [];
      mocks.homeTimeline.following.loading = true;
      mocks.homeTimeline.following.loaded = false;
      await refresh.promise;
      mocks.homeTimeline.following.items = freshItems;
      mocks.homeTimeline.following.loaded = true;
      mocks.homeTimeline.following.loading = false;
      mocks.homeTimeline.following.error = false;
      mocks.homeTimeline.following.nextCursor = null;
    });
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.loadFollowing.mockClear();

    await scrollPanel(wrapper, 25000);
    const deepIDs = mountedPostIDs(wrapper);
    expect(deepIDs.some((id) => id >= 40)).toBe(true);

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();
    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
    expect(mocks.homeTimeline.loadFollowing).not.toHaveBeenCalledWith(true);

    mocks.homeTimeline.homeReselectVersion += 1;
    await nextTick();
    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.loadFollowing).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.following.items).toHaveLength(0);
    expect(panel.scrollTop).toBe(0);

    refresh.resolve(undefined);
    await settleRefreshFrame();

    const refreshedIDs = mountedPostIDs(wrapper);
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(5432);
    expect(refreshedIDs).toContain(2001);
    expect(refreshedIDs.every((id) => id >= 2001 && id <= 2300)).toBe(true);
    expect(refreshedIDs.some((id) => deepIDs.includes(id))).toBe(false);
    expect(refreshedIDs.length).toBeLessThan(50);

    const totalSizeBeforeLateMeasure = virtualListTotalSize(wrapper, 'following');
    const lateHeight = (followingRowHeights.get(0) ?? defaultRowHeight) + 360;
    await resizeRenderedRow(wrapper, 'following', 0, lateHeight);

    expect(virtualListTotalSize(wrapper, 'following')).not.toBe(totalSizeBeforeLateMeasure);
    expect(panel.scrollTop).toBe(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(0);
  });

  it('releases refresh top pin after the user intentionally scrolls down', async () => {
    rowHeights.set(0, 2);
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.loadForYou.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settleRefreshFrame();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(panel.scrollTop).toBe(0);

    await scrollPanel(wrapper, 4);
    await resizeRenderedRow(wrapper, 'for-you', 0, 102);
    expect(panel.scrollTop).toBe(4);

    await wrapper.get('.home-feed-panel').trigger('wheel');
    await scrollPanel(wrapper, 150);
    const userOffset = panel.scrollTop;
    await resizeRenderedRow(wrapper, 'for-you', 0, 202);

    expect(panel.scrollTop).toBeGreaterThan(userOffset);
    expect(panel.scrollTop).not.toBe(0);
  });

  it('does not pull the new active tab to zero when a pending For You refresh finishes', async () => {
    const refresh = deferred<void>();
    const freshItems = Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 3001));
    mocks.homeTimeline = makeTimeline({
      forYouItems: Array.from({ length: 300 }, (_, index) => makeRecommendation(index + 1)),
      followingItems: Array.from({ length: 300 }, (_, index) => makePost(index + 1)),
    });
    mocks.homeTimeline.scrollTop.following = 12000;
    mocks.homeTimeline.loadForYou.mockImplementation(async (force = false) => {
      if (!force) {
        return;
      }
      mocks.homeTimeline.forYou.items = [];
      mocks.homeTimeline.forYou.loading = true;
      mocks.homeTimeline.forYou.loaded = false;
      await refresh.promise;
      mocks.homeTimeline.forYou.items = freshItems;
      mocks.homeTimeline.forYou.loaded = true;
      mocks.homeTimeline.forYou.loading = false;
      mocks.homeTimeline.forYou.error = false;
    });
    setHeterogeneousHeights(300);
    wrapper = mountHome();
    await settle();
    mocks.homeTimeline.loadForYou.mockClear();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(panel.scrollTop).toBe(0);

    mocks.homeTimeline.homeReselectVersion += 1;
    await nextTick();
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    const followingPanelOffset = panel.scrollTop;
    expect(followingPanelOffset).toBeGreaterThan(0);
    expect(mocks.homeTimeline.scrollTop.following).toBe(12000);

    refresh.resolve(undefined);
    await settle();

    expect(panel.scrollTop).toBe(followingPanelOffset);
    expect(mocks.homeTimeline.scrollTop.following).toBe(12000);
    expect(mocks.homeTimeline.scrollTop['for-you']).toBe(0);
  });

  it('keeps the Home view session key across virtual remounts and rotates it on refresh only', async () => {
    wrapper = mountHome();
    await settle();

    const initialSessionKey = mountedPostSessionKey(wrapper, 1);
    expect(initialSessionKey).toBe('home:7:for-you:0');

    await scrollPanel(wrapper, 25000);
    await scrollPanel(wrapper, 0);
    expect(mountedPostSessionKey(wrapper, 1)).toBe(initialSessionKey);

    mocks.homeTimeline.activeTab = 'following';
    await settle();
    mocks.homeTimeline.activeTab = 'for-you';
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe(initialSessionKey);

    mocks.homeTimeline.homeReselectVersion += 1;
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe('home:7:for-you:1');
  });

  it('restores normal virtualizer size adjustment after KeepAlive deactivation', async () => {
    rowHeights.set(0, 2);
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();
    mocks.homeTimeline.loadForYou.mockClear();

    mocks.homeTimeline.homeReselectVersion += 1;
    await settleRefreshFrame();

    const panel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(mocks.homeTimeline.loadForYou).toHaveBeenCalledWith(true);
    expect(panel.scrollTop).toBe(0);

    mounted.state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();

    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();

    const restoredPanel = wrapper.get('.home-feed-panel').element as HTMLElement;
    expect(restoredPanel).toBe(panel);

    await scrollPanel(wrapper, 4);
    const smallOffset = restoredPanel.scrollTop;
    await resizeRenderedRow(wrapper, 'for-you', 0, 102);

    expect(restoredPanel.scrollTop).toBeGreaterThan(smallOffset);
  });

  it('does not rotate the Home view session across KeepAlive deactivation', async () => {
    const mounted = mountKeepAliveHome();
    wrapper = mounted.wrapper;
    await settle();

    const sessionKey = mountedPostSessionKey(wrapper, 1);
    mocks.routeLeave?.();
    mounted.state.showHome = false;
    mocks.route.name = 'PostDetail';
    await settle();
    mocks.route.name = 'Home';
    mounted.state.showHome = true;
    await settle();

    expect(mountedPostSessionKey(wrapper, 1)).toBe(sessionKey);
  });

  it('releases tracked viewer sessions when Home unmounts before the viewer watcher flushes', async () => {
    wrapper = mountHome();
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe('home:7:for-you:0');

    mocks.postViewTelemetry.releaseFeedViewSession.mockClear();
    mocks.authStore.currentIdentity = { ...viewer, id: 8, username: 'viewer-8' };

    wrapper.unmount();
    wrapper = null;

    expect(mocks.postViewTelemetry.releaseFeedViewSession.mock.calls).toEqual([
      ['home:7:for-you:0'],
      ['home:7:following:0'],
    ]);
  });

  it('rotates and isolates Home view sessions when the authenticated viewer changes', async () => {
    wrapper = mountHome();
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe('home:7:for-you:0');

    mocks.authStore.currentIdentity = { ...viewer, id: 8, username: 'other-viewer' };
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe('home:8:for-you:1');

    mocks.authStore.currentIdentity = null;
    await settle();
    expect(mountedPostSessionKey(wrapper, 1)).toBe('home:anonymous:for-you:2');
  });
});
