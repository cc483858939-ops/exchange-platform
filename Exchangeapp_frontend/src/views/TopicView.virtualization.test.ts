// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import {
  defineComponent,
  h,
  KeepAlive,
  nextTick,
  reactive,
  ref,
} from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FeedPost } from '../types/Feed';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  topicSession: null as any,
  route: null as any,
  router: null as any,
  beforeRouteLeave: null as null | (() => void),
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/topicSession', () => ({ useTopicSessionStore: () => mocks.topicSession }));
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => mocks.route,
    useRouter: () => mocks.router,
    onBeforeRouteLeave: (guard: () => void) => { mocks.beforeRouteLeave = guard; },
  };
});

import TopicView from './TopicView.vue';

const PostCardStub = defineComponent({
  name: 'PostCard',
  props: [
    'post',
    'trackView',
    'requiresAuthForActions',
    'viewSessionKey',
    'likePending',
    'repostPending',
    'bookmarkPending',
  ],
  template: `
    <article
      class="topic-card-stub"
      :data-post-id="post.id"
      :data-track-view="trackView"
      :data-requires-auth="requiresAuthForActions"
      :data-session-key="viewSessionKey"
    >{{ post.content }}</article>
  `,
});

const createPosts = (count: number): FeedPost[] => Array.from({ length: count }, (_, index) => ({
  id: index + 1,
  content: `Topic post ${index + 1}`,
  language: 'und',
  media: [],
  author: { id: 9, username: 'author', display_name: 'Author', avatar_url: '' },
  createdAt: '2026-09-20T12:00:00.000Z',
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
}));

const createSession = (items = createPosts(300), overrides: Record<string, unknown> = {}) => {
  const session = reactive({
    activeSlug: 'japan',
    topic: { slug: 'japan', label: 'Japan', description: 'Topic description' },
    items,
    loaded: true,
    initialLoading: false,
    initialError: '',
    nextCursor: null as string | null,
    scrollTop: 0,
    loadingMore: false,
    loadMoreError: '',
    viewerID: null as number | null,
    likePendingPostIDs: new Set<number>(),
    repostPendingPostIDs: new Set<number>(),
    bookmarkPendingPostIDs: new Set<number>(),
    mutationErrors: new Map<number, string>(),
    setTopic: vi.fn().mockResolvedValue(undefined),
    saveScrollTop: vi.fn((value: number) => {
      if (mocks.topicSession) mocks.topicSession.scrollTop = value;
    }),
    reset: vi.fn(),
    retryInitial: vi.fn(),
    retryLoadMore: vi.fn(),
    loadMore: vi.fn(),
    toggleLike: vi.fn(),
    toggleRepost: vi.fn(),
    toggleBookmark: vi.fn(),
    ...overrides,
  });
  return session;
};

const TopicDetailProbe = defineComponent({ template: '<main data-topic-detail />' });

const stubs = {
  AppIcon: true,
  MobileAccountMenu: true,
  PostCard: PostCardStub,
};

const wrappers: Array<ReturnType<typeof mount>> = [];

const mountTopic = () => {
  const wrapper = mount(TopicView, {
    attachTo: document.body,
    global: { stubs },
  });
  wrappers.push(wrapper);
  return wrapper;
};

const mountCachedTopic = () => {
  const topicVisible = ref(true);
  const Host = defineComponent({
    setup: () => () => h(KeepAlive, { include: 'TopicView', max: 1 }, {
      default: () => topicVisible.value
        ? h(TopicView, { key: 'topic' })
        : h(TopicDetailProbe, { key: 'detail' }),
    }),
  });
  const wrapper = mount(Host, { attachTo: document.body, global: { stubs } });
  wrappers.push(wrapper);
  return {
    wrapper,
    showTopic: (visible: boolean) => { topicVisible.value = visible; },
  };
};

const originalOffsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
const originalClientHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight');
const originalClientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
const originalScrollHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight');
const originalGetBoundingClientRect = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'getBoundingClientRect');
const originalScrollTo = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo');

let rowHeights = new Map<number, number>();
let viewportWidth = 1000;
const defaultRowHeight = 360;

type TestResizeEntry = {
  target: Element;
  borderBoxSize: Array<{ blockSize: number; inlineSize: number }>;
  contentRect: DOMRectReadOnly;
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

  trigger(element: Element, blockSize: number, inlineSize = viewportWidth) {
    const rect = {
      x: 0,
      y: 0,
      width: inlineSize,
      height: blockSize,
      top: 0,
      right: inlineSize,
      bottom: blockSize,
      left: 0,
      toJSON: () => ({}),
    } as DOMRectReadOnly;
    this.callback([{
      target: element,
      borderBoxSize: [{ blockSize, inlineSize }],
      contentRect: rect,
    }]);
  }
}

const readOriginalDimension = (descriptor: PropertyDescriptor | undefined, element: HTMLElement) =>
  descriptor?.get?.call(element) ?? 0;

const rowHeightFor = (index: number) => rowHeights.get(index) ?? defaultRowHeight;

const installGeometry = () => {
  rowHeights = new Map();
  viewportWidth = 1000;
  TestResizeObserver.instances = [];
  vi.stubGlobal('ResizeObserver', TestResizeObserver);

  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('topic-view__scroll')) return 800;
      if (element.classList.contains('topic-view__virtual-row')) {
        return rowHeightFor(Number(element.dataset.index));
      }
      return readOriginalDimension(originalOffsetHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (
        element.classList.contains('topic-view__scroll')
        || element.classList.contains('topic-view__virtual-row')
      ) return viewportWidth;
      return readOriginalDimension(originalOffsetWidth, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('topic-view__scroll')) return 800;
      return readOriginalDimension(originalClientHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('topic-view__scroll')) return viewportWidth;
      return readOriginalDimension(originalClientWidth, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get() {
      const element = this as HTMLElement;
      if (element.classList.contains('topic-view__scroll')) {
        const virtualList = element.querySelector<HTMLElement>('[data-topic-virtual-list]');
        return Number.parseFloat(virtualList?.style.height ?? '0') || 0;
      }
      return readOriginalDimension(originalScrollHeight, element);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
    configurable: true,
    value(this: HTMLElement) {
      if (this.classList.contains('topic-view__scroll')) {
        return {
          x: 0,
          y: 0,
          width: viewportWidth,
          height: 800,
          top: 0,
          right: viewportWidth,
          bottom: 800,
          left: 0,
          toJSON: () => ({}),
        } as DOMRect;
      }
      if (this.classList.contains('topic-view__virtual-row')) {
        const top = Number.parseFloat(this.style.transform.match(/translateY\(([-\d.]+)px\)/)?.[1] ?? '0');
        const height = rowHeightFor(Number(this.dataset.index));
        return {
          x: 0,
          y: top,
          width: viewportWidth,
          height,
          top,
          right: viewportWidth,
          bottom: top + height,
          left: 0,
          toJSON: () => ({}),
        } as DOMRect;
      }
      return originalGetBoundingClientRect?.value.call(this) as DOMRect;
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
    ['getBoundingClientRect', originalGetBoundingClientRect],
    ['scrollTo', originalScrollTo],
  ] as const) {
    if (descriptor) {
      Object.defineProperty(HTMLElement.prototype, property, descriptor);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, property);
    }
  }
  vi.unstubAllGlobals();
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const mountedPostIDs = (wrapper: ReturnType<typeof mount>) => wrapper
  .findAll('.topic-card-stub')
  .map(card => Number(card.attributes('data-post-id')));

const virtualListTotalSize = (wrapper: ReturnType<typeof mount>) => Number.parseFloat(
  wrapper.get('[data-topic-virtual-list]').attributes('style')?.match(/height: ([\d.]+)px/)?.[1] ?? '0',
);

const scrollTopic = async (wrapper: ReturnType<typeof mount>, top: number) => {
  const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
  viewport.scrollTop = top;
  viewport.dispatchEvent(new Event('scroll'));
  await settle();
};

const mountedCard = (wrapper: ReturnType<typeof mount>, postID: number) => wrapper
  .find(`.topic-card-stub[data-post-id="${postID}"]`);

const rowStart = (wrapper: ReturnType<typeof mount>, index: number) => Number.parseFloat(
  wrapper
    .get(`.topic-view__virtual-row[data-index="${index}"]`)
    .attributes('style')
    ?.match(/translateY\(([-\d.]+)px\)/)?.[1] ?? '0',
);

const triggerViewportResize = async (wrapper: ReturnType<typeof mount>, width: number) => {
  viewportWidth = width;
  const viewport = wrapper.get('.topic-view__scroll').element;
  const observers = TestResizeObserver.instances.filter(observer => observer.observed.has(viewport));
  expect(observers.length).toBeGreaterThan(0);
  observers.forEach(observer => observer.trigger(viewport, 800, width));
  await settle();
};

const resizeRenderedRow = async (
  wrapper: ReturnType<typeof mount>,
  index: number,
  height: number,
) => {
  const row = wrapper.get(`.topic-view__virtual-row[data-index="${index}"]`).element;
  rowHeights.set(index, height);
  const observers = TestResizeObserver.instances.filter(observer => observer.observed.has(row));
  expect(observers.length).toBeGreaterThan(0);
  observers.forEach(observer => observer.trigger(row, height));
  await settle();
};

const rowHeightPattern = [180, 520, 240, 700, 190, 640, 300, 460];

const setHeterogeneousHeights = (count: number) => {
  rowHeights = new Map(Array.from({ length: count }, (_, index) => (
    [index, rowHeightPattern[index % rowHeightPattern.length]]
  )));
};

describe('TopicView virtualization', () => {
  beforeEach(() => {
    installGeometry();
    mocks.authStore = reactive({ isAuthenticated: false, currentIdentity: null });
    mocks.route = reactive({ name: 'Topic', params: { slug: 'japan' }, fullPath: '/topics/japan' });
    mocks.router = { back: vi.fn(), push: vi.fn() };
    mocks.topicSession = createSession();
    mocks.beforeRouteLeave = null;
  });

  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount());
    document.body.innerHTML = '';
    restoreGeometry();
  });

  it('keeps a long logical list while mounting a bounded window and scrolling to later posts', async () => {
    setHeterogeneousHeights(300);
    const wrapper = mountTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);

    expect(mocks.topicSession.items).toHaveLength(300);
    expect(mountedPostIDs(wrapper).length).toBeGreaterThan(0);
    expect(mountedPostIDs(wrapper).length).toBeLessThan(50);
    expect(mountedPostIDs(wrapper)).toContain(1);
    expect(mountedPostIDs(wrapper)).not.toContain(300);
    expect(virtualListTotalSize(wrapper)).toBeGreaterThan(300 * 180);

    const originalFirstCard = mountedCard(wrapper, 1).element;
    await scrollTopic(wrapper, 25000);
    const deepIDs = mountedPostIDs(wrapper);
    expect(deepIDs.length).toBeLessThan(50);
    expect(deepIDs).not.toContain(1);
    expect(deepIDs.some(id => id >= 60)).toBe(true);
    expect(wrapper.find('.topic-card-stub[data-post-id="1"]').exists()).toBe(false);

    await scrollTopic(wrapper, 0);
    expect(mountedCard(wrapper, 1).element).not.toBe(originalFirstCard);
    expect(mountedCard(wrapper, 1).attributes('data-session-key')).toBe('topic:anonymous:japan');
  });

  it('measures heterogeneous rows and updates later offsets when a mounted row changes height', async () => {
    mocks.topicSession = createSession(createPosts(40));
    setHeterogeneousHeights(40);
    const wrapper = mountTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);

    const initialTotalSize = virtualListTotalSize(wrapper);
    const initialLaterStart = rowStart(wrapper, 2);
    await resizeRenderedRow(wrapper, 0, 600);

    expect(virtualListTotalSize(wrapper)).toBe(initialTotalSize + 420);
    expect(rowStart(wrapper, 2)).toBe(initialLaterStart + 420);
    expect(new Set(mountedPostIDs(wrapper)).size).toBe(mountedPostIDs(wrapper).length);
  });

  it('remeasures after a viewport width change once and ignores same-width notifications', async () => {
    setHeterogeneousHeights(300);
    const wrapper = mountTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);
    const initialTotalSize = virtualListTotalSize(wrapper);
    const mountedIndices = wrapper.findAll('.topic-view__virtual-row')
      .map(row => Number(row.attributes('data-index')));

    mountedIndices.forEach(index => rowHeights.set(index, rowHeightFor(index) + 100));
    await triggerViewportResize(wrapper, 900);
    const resizedTotalSize = virtualListTotalSize(wrapper);
    expect(resizedTotalSize).toBeGreaterThan(initialTotalSize);

    mountedIndices.forEach(index => rowHeights.set(index, rowHeightFor(index) + 100));
    await triggerViewportResize(wrapper, 900);
    expect(virtualListTotalSize(wrapper)).toBe(resizedTotalSize);
  });

  it('keeps the pagination sentinel outside the virtual list and appends without resetting scroll', async () => {
    class FakeIntersectionObserver {
      static instances: FakeIntersectionObserver[] = [];

      readonly observe = vi.fn();
      readonly disconnect = vi.fn();

      constructor(readonly callback: IntersectionObserverCallback) {
        FakeIntersectionObserver.instances.push(this);
      }

      unobserve() {}

      intersect(target: Element) {
        this.callback(
          [{ target, isIntersecting: true } as IntersectionObserverEntry],
          this as unknown as IntersectionObserver,
        );
      }
    }
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    mocks.topicSession = createSession(createPosts(20), { nextCursor: 'next-page' });
    mocks.topicSession.loadMore.mockImplementation(async () => {
      mocks.topicSession.items.push(...createPosts(5).map((post: FeedPost, index: number) => ({
        ...post,
        id: 21 + index,
        content: `Topic post ${21 + index}`,
      })));
      mocks.topicSession.nextCursor = null;
    });
    const wrapper = mountTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);

    const sentinel = wrapper.get('.topic-view__sentinel').element;
    expect(wrapper.get('[data-topic-virtual-list]').element.contains(sentinel)).toBe(false);
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    viewport.scrollTop = 420;
    const previousTotalSize = virtualListTotalSize(wrapper);
    const observer = FakeIntersectionObserver.instances.at(-1);
    expect(observer).toBeTruthy();
    observer?.intersect(sentinel);
    await settle();

    expect(mocks.topicSession.items).toHaveLength(25);
    expect(virtualListTotalSize(wrapper)).toBeGreaterThan(previousTotalSize);
    expect(viewport.scrollTop).toBe(420);
  });

  it('preserves deep virtual rows and pixel offset through KeepAlive deactivation', async () => {
    setHeterogeneousHeights(300);
    const { wrapper, showTopic } = mountCachedTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    await scrollTopic(wrapper, 25000);
    const beforeIDs = mountedPostIDs(wrapper);
    const beforeSessionKey = wrapper.find('.topic-card-stub').attributes('data-session-key');
    expect(beforeIDs.some(id => id >= 50)).toBe(true);

    mocks.beforeRouteLeave?.();
    expect(mocks.topicSession.scrollTop).toBe(25000);
    mocks.route.name = 'PostDetail';
    showTopic(false);
    await settle();
    expect(wrapper.find('[data-topic-detail]').exists()).toBe(true);

    mocks.route.name = 'Topic';
    mocks.topicSession.scrollTop = 25000;
    showTopic(true);
    await settle();

    expect(wrapper.get('.topic-view__scroll').element).toBe(viewport);
    expect(viewport.scrollTop).toBe(25000);
    expect(mountedPostIDs(wrapper)).toEqual(beforeIDs);
    expect(wrapper.find('.topic-card-stub').attributes('data-session-key')).toBe(beforeSessionKey);
  });

  it('keeps post identity when an earlier item is deleted and indices shift', async () => {
    setHeterogeneousHeights(100);
    const wrapper = mountTopic();
    await settle();
    await triggerViewportResize(wrapper, viewportWidth);
    await scrollTopic(wrapper, 7200);

    const retainedCard = mountedCard(wrapper, 21);
    expect(retainedCard.exists()).toBe(true);
    const retainedElement = retainedCard.element;
    mocks.topicSession.items.splice(0, 1);
    await settle();

    expect(mocks.topicSession.items).toHaveLength(299);
    expect(wrapper.find('.topic-card-stub[data-post-id="1"]').exists()).toBe(false);
    expect(mountedCard(wrapper, 21).element).toBe(retainedElement);
    expect(mountedCard(wrapper, 21).element.closest('.topic-view__virtual-row')?.getAttribute('data-index'))
      .toBe('19');
  });
});
