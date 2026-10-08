// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostQuotesView from './PostQuotesView.vue';
import type { FeedPost } from '../types/Feed';

const mocks = vi.hoisted(() => ({
  route: null as any,
  router: { back: vi.fn(), push: vi.fn() },
  authStore: null as any,
  session: null as any,
}));

vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => mocks.router }));
vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/quotesSession', () => ({ useQuotesSessionStore: () => mocks.session }));

const post = (id: number): FeedPost => ({
  id,
  author: { id: 9, username: 'author', display_name: 'Author', avatar_url: '' },
  content: `Quote ${id}`,
  language: 'und',
  media: [],
  createdAt: '2026-09-20T12:00:00.000Z',
  likeCount: 3,
  replyCount: 1,
  quoteCount: 2,
  viewCount: 8,
  liked: false,
  likeStatus: 'ready',
  repostCount: 4,
  reposted: false,
  repostStatus: 'ready',
  bookmarked: false,
  bookmarkStatus: 'ready',
});

const createSession = (overrides: Record<string, unknown> = {}) => {
  const session: Record<string, any> = reactive({
    targetPostID: 42,
    items: [],
    loaded: false,
    initialLoading: false,
    initialError: '',
    nextCursor: null,
    loadingMore: false,
    loadMoreError: '',
    targetUnavailable: false,
    likePendingPostIDs: new Set<number>(),
    repostPendingPostIDs: new Set<number>(),
    bookmarkPendingPostIDs: new Set<number>(),
    mutationErrors: new Map<number, string>(),
    setTarget: vi.fn(async () => undefined),
    retryInitial: vi.fn(),
    loadMore: vi.fn(),
    retryLoadMore: vi.fn(),
    toggleLike: vi.fn(),
    toggleRepost: vi.fn(),
    toggleBookmark: vi.fn(),
    reset: vi.fn(),
    ...overrides,
  });
  return session;
};

const wrappers: Array<ReturnType<typeof mount>> = [];
const mountView = () => {
  const wrapper = mount(PostQuotesView, {
  attachTo: document.body,
  global: {
    stubs: {
      AppIcon: { props: ['name'], template: '<span :data-icon="name" />' },
      MobileAccountMenu: { template: '<div class="test-account-menu" />' },
      PostCard: {
        props: ['post', 'trackView', 'requiresAuthForActions', 'likePending', 'repostPending', 'bookmarkPending'],
        emits: ['toggle-like'],
        template: '<article class="test-post-card" :data-id="post.id" :data-track-view="String(trackView)" :data-requires-auth="String(requiresAuthForActions)">{{ post.content }}<button @click="$emit(\'toggle-like\', post.id)">Like</button></article>',
      },
    },
  },
  });
  wrappers.push(wrapper);
  return wrapper;
};

describe('PostQuotesView', () => {
  let originalHistoryState: unknown;

  beforeEach(() => {
    vi.clearAllMocks();
    originalHistoryState = window.history.state;
    window.history.replaceState({ back: null }, '', window.location.href);
    mocks.route = reactive({ name: 'PostQuotes', params: { id: '42' } });
    mocks.router.back.mockReset();
    mocks.router.push.mockReset();
    mocks.authStore = reactive({ isAuthenticated: false, currentIdentity: null });
    mocks.session = createSession();
    vi.stubGlobal('scrollY', 0);
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
  });

  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount());
    window.history.replaceState(originalHistoryState, '', window.location.href);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  const installRowGeometry = () => {
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(240);
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(640);
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const rowStart = Number.parseFloat(this.style.transform.match(/translateY\(([-\d.]+)px\)/)?.[1] ?? '0');
      const top = 64 + rowStart - window.scrollY;
      return { x: 0, y: top, top, bottom: top + 240, left: 0, right: 640, width: 640, height: 240, toJSON: () => ({}) };
    });
  };

  it('bounds mounted cards while preserving deep-scroll order and interaction identity', async () => {
    installRowGeometry();
    mocks.session.items = Array.from({ length: 1000 }, (_, index) => post(index + 1));
    mocks.session.loaded = true;
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.findAll('.test-post-card').length).toBeLessThan(25);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="500"]').exists()).toBe(false);
    vi.stubGlobal('scrollY', 64 + 499 * 240);
    window.dispatchEvent(new Event('scroll'));
    await flushPromises();
    expect(wrapper.findAll('.test-post-card').length).toBeLessThan(25);
    expect(wrapper.find('[data-id="500"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);
    await wrapper.get('[data-id="500"] button').trigger('click');
    expect(mocks.session.toggleLike).toHaveBeenCalledWith(500);
    mocks.session.items = mocks.session.items.filter((item: FeedPost) => item.id !== 500);
    await flushPromises();
    expect(wrapper.find('[data-id="500"]').exists()).toBe(false);
    expect(wrapper.find('[data-id="501"]').exists()).toBe(true);
  });

  it('keeps a keyboard-focused row mounted outside the visible range until focus leaves it', async () => {
    installRowGeometry();
    mocks.session.items = Array.from({ length: 1000 }, (_, index) => post(index + 1));
    mocks.session.loaded = true;
    const wrapper = mountView();
    await flushPromises();
    const focused = wrapper.get('[data-id="1"] button').element as HTMLButtonElement;
    focused.focus();
    await flushPromises();
    vi.stubGlobal('scrollY', 64 + 499 * 240);
    window.dispatchEvent(new Event('scroll'));
    await flushPromises();
    expect(wrapper.find('[data-id="1"]').exists()).toBe(true);
    expect(document.activeElement).toBe(focused);
    expect(wrapper.findAll('.test-post-card').length).toBeLessThan(25);
    focused.blur();
    await flushPromises();
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);
  });

  it('shows a Post-card skeleton without flashing the empty state during initial loading', () => {
    mocks.session.initialLoading = true;

    const wrapper = mountView();

    expect(wrapper.findAll('.quotes-skeleton')).toHaveLength(3);
    expect(wrapper.text()).not.toContain('No quotes are available.');
    expect(wrapper.find('.test-post-card').exists()).toBe(false);
  });

  it('loads the route target and renders public guest PostCards without view tracking', async () => {
    mocks.session.items = [post(101), post(102)];
    mocks.session.loaded = true;
    mocks.session.initialLoading = false;

    const wrapper = mountView();
    await flushPromises();

    expect(mocks.session.setTarget).toHaveBeenCalledWith('42');
    expect(wrapper.findAll('.test-post-card')).toHaveLength(2);
    expect(wrapper.get('[data-id="101"]').attributes('data-track-view')).toBe('false');
    expect(wrapper.get('[data-id="101"]').attributes('data-requires-auth')).toBe('true');
  });

  it('shows the exact available-quotes empty state after a successful empty page', () => {
    mocks.session.loaded = true;

    const wrapper = mountView();

    expect(wrapper.text()).toContain('No quotes are available.');
    expect(wrapper.text()).not.toContain('No one has quoted this post yet.');
  });

  it('shows generic initial errors with a retry action', async () => {
    mocks.session.initialError = 'Quotes could not be loaded.';
    const wrapper = mountView();

    await wrapper.get('button.quotes-view__button').trigger('click');

    expect(wrapper.text()).toContain('Quotes could not be loaded.');
    expect(mocks.session.retryInitial).toHaveBeenCalledOnce();
  });

  it('renders unavailable target state instead of the generic error', () => {
    mocks.session.targetUnavailable = true;
    mocks.session.initialError = '';
    const wrapper = mountView();

    expect(wrapper.text()).toContain('Post unavailable');
    expect(wrapper.text()).toContain('This post may have been deleted or is no longer available.');
    expect(wrapper.text()).not.toContain('Quotes could not be loaded.');
  });

  it('uses the button fallback when IntersectionObserver is unavailable', async () => {
    mocks.session.items = [post(101)];
    mocks.session.loaded = true;
    mocks.session.nextCursor = 'cursor-1';
    const wrapper = mountView();

    expect(wrapper.text()).toContain('Load more quotes');
    await wrapper.get('button.quotes-view__button').trigger('click');
    expect(mocks.session.loadMore).toHaveBeenCalledOnce();

    mocks.session.loadMoreError = 'Could not load more quotes.';
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).toContain('Could not load more quotes.');
    await wrapper.get('button.quotes-view__button').trigger('click');
    expect(mocks.session.retryLoadMore).toHaveBeenCalledOnce();
  });

  it('uses an IntersectionObserver sentinel when supported', async () => {
    const callbacks: IntersectionObserverCallback[] = [];
    vi.stubGlobal('IntersectionObserver', class {
      constructor(next: IntersectionObserverCallback) { callbacks.push(next); }
      observe() {}
      disconnect() {}
      unobserve() {}
      takeRecords() { return []; }
      root = null;
      rootMargin = '';
      thresholds = [];
    });
    mocks.session.items = [post(101)];
    mocks.session.loaded = true;
    mocks.session.nextCursor = 'cursor-1';
    const wrapper = mountView();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).not.toContain('Load more quotes');
    callbacks[0]?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(mocks.session.loadMore).toHaveBeenCalledOnce();
  });

  it('clears previous target content on route change and requests the new target', async () => {
    mocks.session.items = [post(42)];
    mocks.session.loaded = true;
    mocks.session.setTarget.mockImplementation(async (id: unknown) => {
      const value = Number(id);
      mocks.session.targetPostID = Number.isSafeInteger(value) && value > 0 ? value : null;
      mocks.session.items = [];
      mocks.session.loaded = false;
      mocks.session.initialLoading = true;
    });
    const wrapper = mountView();

    mocks.route.params.id = '99';
    await wrapper.vm.$nextTick();

    expect(mocks.session.setTarget).toHaveBeenLastCalledWith('99');
    expect(wrapper.find('.test-post-card').exists()).toBe(false);
    expect(mocks.session.initialLoading).toBe(true);
  });

  it('falls back to the current Post detail instead of Home when browser history is empty', async () => {
    const wrapper = mountView();

    await wrapper.get('.quotes-view__back').trigger('click');

    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'PostDetail', params: { id: '42' } });
    expect(mocks.router.push).not.toHaveBeenCalledWith({ name: 'Home' });
  });

  it('uses browser Back when a meaningful history entry exists', async () => {
    window.history.replaceState({ back: '/posts/42' }, '', window.location.href);
    const wrapper = mountView();

    await wrapper.get('.quotes-view__back').trigger('click');

    expect(mocks.router.back).toHaveBeenCalledOnce();
    expect(mocks.router.push).not.toHaveBeenCalled();
  });
});
