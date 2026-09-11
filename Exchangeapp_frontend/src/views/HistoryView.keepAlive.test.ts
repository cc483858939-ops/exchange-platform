// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, nextTick, reactive, ref } from 'vue';

let HistoryView: any;

const mocks = vi.hoisted(() => ({
  route: null as any,
  historyStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  observerInstances: [] as any[],
  events: [] as string[],
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

vi.mock('../store/historySession', () => ({
  useHistorySessionStore: () => mocks.historyStore,
}));

const previousIntersectionObserver = (globalThis as any).IntersectionObserver;

class TestIntersectionObserver {
  private readonly callback: IntersectionObserverCallback;

  readonly observe = vi.fn((_target: Element) => {
    mocks.events.push('observe');
  });

  readonly disconnect = vi.fn(() => {
    mocks.events.push('disconnect');
  });

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback;
    mocks.observerInstances.push(this);
  }

  trigger(isIntersecting = true) {
    this.callback(
      [{ isIntersecting } as IntersectionObserverEntry],
      this as unknown as IntersectionObserver,
    );
  }
}

const postCardStub = {
  props: ['post', 'trackView', 'likePending', 'repostPending'],
  template: '<article class="history-card" :data-id="post.id">History post</article>',
};

const createHistoryStore = () => {
  const store = reactive({
    viewerID: ref(7 as number | null),
    items: ref([{ id: 1, content: 'History post' }]),
    loaded: ref(true),
    initialLoading: ref(false),
    initialError: ref(''),
    nextCursor: ref('cursor-1' as string | null),
    loadingMore: ref(false),
    loadMoreError: ref(''),
    stale: ref(false),
    revalidating: ref(false),
    scrollY: ref(900),
    pendingUnlikePostIDs: ref(new Set<number>()),
    repostPendingPostIDs: ref(new Set<number>()),
    mutationErrors: ref(new Map<number, string>()),
    loadInitial: vi.fn(),
    loadMore: vi.fn(),
    retryInitial: vi.fn(),
    retryLoadMore: vi.fn(),
    revalidateHistory: vi.fn(),
    toggleUnlike: vi.fn(),
    toggleRepost: vi.fn(),
    saveScroll: vi.fn((value: number) => {
      store.scrollY = value;
    }),
  });
  return store;
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

const setWindowScrollY = (value: number) => {
  Object.defineProperty(window, 'scrollY', { configurable: true, value });
};

const mountHarness = () => {
  const showHistory = reactive({ value: true });
  const Harness = defineComponent({
    components: { HistoryView },
    setup() {
      return { showHistory };
    },
    template: '<KeepAlive><HistoryView v-if="showHistory.value" /></KeepAlive>',
  });
  const wrapper = mount(Harness, {
    global: {
      stubs: {
        AppIcon: { template: '<span class="test-icon" />' },
        PostCard: postCardStub,
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  return { showHistory, wrapper };
};

const deactivateHistory = async (showHistory: { value: boolean }) => {
  mocks.route.name = 'PostDetail';
  showHistory.value = false;
  await settle();
};

const reactivateHistory = async (showHistory: { value: boolean }) => {
  mocks.route.name = 'History';
  showHistory.value = true;
  await settle();
};

describe('HistoryView KeepAlive lifecycle', () => {
  beforeAll(async () => {
    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: TestIntersectionObserver,
    });
    HistoryView = (await import('./HistoryView.vue')).default;
  });

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({ name: 'History' });
    mocks.historyStore = createHistoryStore();
    mocks.observerInstances.length = 0;
    mocks.events.length = 0;
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {
      mocks.events.push('scrollTo');
    });
    setWindowScrollY(0);
  });

  afterEach(() => {
    setWindowScrollY(0);
    vi.restoreAllMocks();
  });

  afterAll(() => {
    if (previousIntersectionObserver === undefined) {
      delete (globalThis as any).IntersectionObserver;
      return;
    }

    Object.defineProperty(globalThis, 'IntersectionObserver', {
      configurable: true,
      writable: true,
      value: previousIntersectionObserver,
    });
  });

  it('preserves the same History DOM and restores cached scroll before observer resume', async () => {
    const historyStore = mocks.historyStore;
    const { showHistory, wrapper } = mountHarness();
    await settle();

    const originalHistory = wrapper.find('.history-view').element;
    const originalPost = wrapper.find('.history-card').element;
    historyStore.nextCursor = 'cursor-2';
    await settle();
    expect(mocks.observerInstances).toHaveLength(1);

    const originalObserver = mocks.observerInstances[0] as TestIntersectionObserver;
    setWindowScrollY(1800);
    await deactivateHistory(showHistory);

    expect(historyStore.saveScroll).toHaveBeenLastCalledWith(1800);
    expect(historyStore.scrollY).toBe(1800);
    expect(originalObserver.disconnect).toHaveBeenCalledTimes(1);

    setWindowScrollY(300);
    mocks.events.length = 0;
    (window.scrollTo as ReturnType<typeof vi.fn>).mockClear();
    await reactivateHistory(showHistory);

    expect(wrapper.find('.history-view').element).toBe(originalHistory);
    expect(wrapper.find('.history-card').element).toBe(originalPost);
    expect(window.scrollTo).toHaveBeenLastCalledWith({ top: 1800, behavior: 'auto' });
    expect(window.scrollTo).not.toHaveBeenCalledWith({ top: 300, behavior: 'auto' });
    expect(mocks.events.indexOf('scrollTo')).toBeGreaterThanOrEqual(0);
    expect(mocks.events.indexOf('observe')).toBeGreaterThan(mocks.events.indexOf('scrollTo'));
    wrapper.unmount();
  });

  it('does not restore scroll or start a new load while History is hidden', async () => {
    const historyStore = mocks.historyStore;
    historyStore.loaded = false;
    historyStore.initialLoading = true;
    historyStore.items = [];
    historyStore.nextCursor = null;
    const { showHistory, wrapper } = mountHarness();
    await settle();

    setWindowScrollY(1800);
    await deactivateHistory(showHistory);
    expect(historyStore.scrollY).toBe(1800);

    setWindowScrollY(300);
    (window.scrollTo as ReturnType<typeof vi.fn>).mockClear();
    historyStore.loaded = true;
    historyStore.initialLoading = false;
    historyStore.items = [{ id: 2, content: 'Loaded while hidden' }];
    await settle();

    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(historyStore.scrollY).toBe(1800);
    wrapper.unmount();
  });

  it('blocks hidden observer callbacks, observer recreation, and viewer loads', async () => {
    const historyStore = mocks.historyStore;
    const { showHistory, wrapper } = mountHarness();
    await settle();
    historyStore.nextCursor = 'cursor-2';
    await settle();
    const originalObserver = mocks.observerInstances[0] as TestIntersectionObserver;
    historyStore.loadMore.mockClear();
    historyStore.loadInitial.mockClear();
    const observerCount = mocks.observerInstances.length;

    await deactivateHistory(showHistory);
    originalObserver.trigger();
    historyStore.viewerID = 8;
    historyStore.nextCursor = 'cursor-3';
    historyStore.loadingMore = true;
    historyStore.loadingMore = false;
    historyStore.loadMoreError = 'hidden error';
    historyStore.loadMoreError = '';
    historyStore.items = [{ id: 1, content: 'Changed while hidden' }, { id: 2, content: 'New' }];
    historyStore.stale = true;
    historyStore.revalidating = true;
    historyStore.revalidating = false;
    await settle();

    expect(historyStore.loadMore).not.toHaveBeenCalled();
    expect(historyStore.loadInitial).not.toHaveBeenCalled();
    expect(mocks.observerInstances).toHaveLength(observerCount);
    expect(historyStore.revalidateHistory).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('resumes stale revalidation only after cached scroll restoration', async () => {
    const historyStore = mocks.historyStore;
    historyStore.revalidateHistory.mockImplementation(() => {
      mocks.events.push('revalidate');
    });
    const { showHistory, wrapper } = mountHarness();
    await settle();

    setWindowScrollY(1800);
    await deactivateHistory(showHistory);
    historyStore.stale = true;
    await settle();
    expect(historyStore.revalidateHistory).not.toHaveBeenCalled();

    setWindowScrollY(300);
    mocks.events.length = 0;
    (window.scrollTo as ReturnType<typeof vi.fn>).mockClear();
    await reactivateHistory(showHistory);

    expect(window.scrollTo).toHaveBeenLastCalledWith({ top: 1800, behavior: 'auto' });
    expect(historyStore.revalidateHistory).toHaveBeenCalledTimes(1);
    expect(mocks.events.indexOf('scrollTo')).toBeLessThan(mocks.events.indexOf('revalidate'));
    wrapper.unmount();
  });

  it('does not overwrite the saved History scroll when a hidden cache is finally unmounted', async () => {
    const historyStore = mocks.historyStore;
    const { showHistory, wrapper } = mountHarness();
    await settle();

    setWindowScrollY(1800);
    await deactivateHistory(showHistory);
    setWindowScrollY(300);
    wrapper.unmount();

    expect(historyStore.saveScroll).toHaveBeenCalledTimes(1);
    expect(historyStore.saveScroll).toHaveBeenCalledWith(1800);
    expect(historyStore.scrollY).toBe(1800);
  });
});
