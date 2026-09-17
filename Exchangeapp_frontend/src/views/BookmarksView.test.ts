// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive, ref } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import BookmarksView from './BookmarksView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  routeLeaveGuard: null as (() => void) | null,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  session: null as any,
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: (guard: () => void) => {
    mocks.routeLeaveGuard = guard;
  },
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

vi.mock('../store/bookmarksSession', () => ({
  useBookmarksSessionStore: () => mocks.session,
}));

const post = (id: number, content = `Post ${id}`) => ({
  id,
  content,
});

const createSession = () => reactive({
  viewerID: ref(7),
  items: ref([] as Array<{ id: number; content: string }>),
  loaded: ref(false),
  initialLoading: ref(false),
  initialError: ref(''),
  nextCursor: ref<string | null>(null),
  loadingMore: ref(false),
  loadMoreError: ref(''),
  stale: ref(false),
  revalidating: ref(false),
  revalidateError: ref(''),
  scrollTop: ref(0),
  likePendingPostIDs: ref(new Set<number>()),
  repostPendingPostIDs: ref(new Set<number>()),
  bookmarkPendingPostIDs: ref(new Set<number>()),
  mutationErrors: ref(new Map<number, string>()),
  loadInitial: vi.fn(),
  loadMore: vi.fn(),
  retryInitial: vi.fn(),
  retryLoadMore: vi.fn(),
  revalidateBookmarks: vi.fn(),
  toggleLike: vi.fn(),
  toggleRepost: vi.fn(),
  toggleBookmark: vi.fn(),
  saveScrollTop: vi.fn(),
});

const PostCardStub = {
  props: ['post', 'trackView', 'likePending', 'repostPending', 'bookmarkPending'],
  emits: ['toggleLike', 'toggleRepost', 'toggleBookmark'],
  template: `
    <article class="bookmarks-post" :data-id="post.id">
      <span>{{ post.content }}</span>
      <button class="bookmarks-post__like" type="button" @click="$emit('toggleLike', post.id)">Like</button>
      <button class="bookmarks-post__repost" type="button" @click="$emit('toggleRepost', post.id)">Repost</button>
      <button class="bookmarks-post__bookmark" type="button" @click="$emit('toggleBookmark', post.id)">Bookmark</button>
    </article>
  `,
};

const mountBookmarks = () => mount(BookmarksView, {
  global: {
    stubs: {
      AppIcon: {
        props: ['name', 'size'],
        template: '<span class="test-icon" :data-icon="name" :data-size="size" />',
      },
      MobileAccountMenu: { template: '<span data-mobile-account-menu />' },
      PostCard: PostCardStub,
      RouterLink: {
        props: ['to'],
        template: '<a class="router-link-stub" :data-route-name="to && to.name"><slot /></a>',
      },
    },
  },
});

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];
  readonly callback: IntersectionObserverCallback;
  readonly root: Element | Document | null;
  readonly rootMargin: string;
  observed: Element | null = null;
  disconnected = false;

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.callback = callback;
    this.root = options?.root ?? null;
    this.rootMargin = options?.rootMargin ?? '';
    FakeIntersectionObserver.instances.push(this);
  }

  observe(target: Element) {
    this.observed = target;
  }

  unobserve() {}

  disconnect() {
    this.disconnected = true;
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }

  trigger(isIntersecting = true) {
    this.callback([
      { isIntersecting, target: this.observed } as unknown as IntersectionObserverEntry,
    ], this as unknown as IntersectionObserver);
  }
}

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
  await nextTick();
};

describe('BookmarksView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    FakeIntersectionObserver.instances = [];
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    mocks.route = reactive({
      name: 'Bookmarks',
      fullPath: '/bookmarks',
    });
    mocks.routeLeaveGuard = null;
    mocks.session = createSession();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the login-required state without loading bookmarks when signed out', async () => {
    mocks.session.viewerID = null;
    const wrapper = mountBookmarks();
    await settle();

    expect(wrapper.text()).toContain('Log in to view your bookmarks.');
    expect(wrapper.find('.router-link-stub').text()).toBe('Log in');
    expect(mocks.session.loadInitial).not.toHaveBeenCalled();
    expect(wrapper.find('[data-mobile-account-menu]').exists()).toBe(false);
  });

  it('renders a timeline-shaped loading skeleton and avoids a duplicate initial request when already loading', async () => {
    mocks.session.initialLoading = true;
    const wrapper = mountBookmarks();
    await settle();

    expect(wrapper.find('[aria-label="Loading bookmarks"]').exists()).toBe(true);
    expect(wrapper.findAll('.bookmarks-skeleton')).toHaveLength(3);
    expect(mocks.session.loadInitial).not.toHaveBeenCalled();
  });

  it('loads the current viewer once when the session is not loaded', async () => {
    const wrapper = mountBookmarks();
    await settle();

    expect(mocks.session.loadInitial).toHaveBeenCalledTimes(1);
    expect(wrapper.get('h1').text()).toBe('Bookmarks');
  });

  it('shows the exact empty state copy', async () => {
    mocks.session.loaded = true;
    const wrapper = mountBookmarks();
    await settle();

    expect(wrapper.text()).toContain('Save posts for later');
    expect(wrapper.text()).toContain('Bookmark posts to easily find them again.');
    expect(wrapper.find('[aria-label="Bookmarked posts"]').exists()).toBe(false);
    expect(mocks.session.loadInitial).not.toHaveBeenCalled();
  });

  it('retries an initial failure with the existing session method', async () => {
    mocks.session.initialError = 'Bookmarks could not be loaded.';
    const wrapper = mountBookmarks();
    await settle();

    expect(wrapper.text()).toContain('Bookmarks could not be loaded.');
    await wrapper.get('.bookmarks-view__state .bookmarks-view__primary').trigger('click');

    expect(mocks.session.retryInitial).toHaveBeenCalledTimes(1);
  });

  it('renders posts in session order, passes pending state, disables view tracking, and delegates mutations', async () => {
    mocks.session.loaded = true;
    mocks.session.items = [post(2, 'Second'), post(1, 'First')];
    mocks.session.likePendingPostIDs.add(2);
    mocks.session.repostPendingPostIDs.add(1);
    mocks.session.bookmarkPendingPostIDs.add(2);
    const wrapper = mountBookmarks();
    await settle();

    expect(wrapper.findAll('.bookmarks-post').map(card => card.attributes('data-id')))
      .toEqual(['2', '1']);
    const cards = wrapper.findAllComponents(PostCardStub);
    expect(cards[0].props('trackView')).toBe(false);
    expect(cards[0].props('likePending')).toBe(true);
    expect(cards[0].props('bookmarkPending')).toBe(true);
    expect(cards[1].props('repostPending')).toBe(true);

    await cards[0].get('.bookmarks-post__like').trigger('click');
    await cards[1].get('.bookmarks-post__repost').trigger('click');
    await cards[0].get('.bookmarks-post__bookmark').trigger('click');

    expect(mocks.session.toggleLike).toHaveBeenCalledWith(2);
    expect(mocks.session.toggleRepost).toHaveBeenCalledWith(1);
    expect(mocks.session.toggleBookmark).toHaveBeenCalledWith(2);
  });

  it('supports manual load more and retry states when IntersectionObserver is unavailable', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    mocks.session.loaded = true;
    mocks.session.items = [post(1)];
    mocks.session.nextCursor = 'cursor-1';
    const wrapper = mountBookmarks();
    await settle();

    await wrapper.get('.bookmarks-view__sentinel .bookmarks-view__primary').trigger('click');
    expect(mocks.session.loadMore).toHaveBeenCalledTimes(1);

    mocks.session.loadMoreError = 'Could not load more bookmarks.';
    await nextTick();
    expect(wrapper.text()).toContain('Could not load more bookmarks.');
    await wrapper.get('.bookmarks-view__sentinel .bookmarks-view__primary').trigger('click');
    expect(mocks.session.retryLoadMore).toHaveBeenCalledTimes(1);
  });

  it('uses the internal viewport as the observer root and gates stale loads', async () => {
    mocks.session.loaded = true;
    mocks.session.items = [post(1)];
    mocks.session.nextCursor = 'cursor-1';
    const wrapper = mountBookmarks();
    await settle();

    const viewport = wrapper.get('.bookmarks-scroll-viewport').element;
    const observer = FakeIntersectionObserver.instances.at(-1);
    expect(observer?.root).toBe(viewport);
    expect(observer?.rootMargin).toBe('240px 0px');
    observer?.trigger();
    expect(mocks.session.loadMore).toHaveBeenCalledTimes(1);

    mocks.session.stale = true;
    await settle();
    const gatedObserver = FakeIntersectionObserver.instances.at(-1);
    gatedObserver?.trigger();
    expect(mocks.session.loadMore).toHaveBeenCalledTimes(1);
  });

  it('restores and saves the session-backed internal scroll position without window scrolling', async () => {
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    mocks.session.loaded = true;
    mocks.session.scrollTop = 1400;
    const wrapper = mountBookmarks();
    await settle();

    const viewport = wrapper.get('.bookmarks-scroll-viewport').element as HTMLElement;
    expect(viewport.scrollTop).toBe(1400);
    viewport.scrollTop = 1600;
    expect(mocks.routeLeaveGuard).toBeTypeOf('function');
    mocks.routeLeaveGuard?.();

    expect(mocks.session.saveScrollTop).toHaveBeenCalledWith(1600);
    expect(scrollTo).not.toHaveBeenCalled();
    scrollTo.mockRestore();
    wrapper.unmount();
  });

  it('revalidates stale data while keeping the visible timeline and mutation status', async () => {
    mocks.session.loaded = true;
    mocks.session.items = [post(1, 'Visible bookmark')];
    mocks.session.stale = true;
    mocks.session.revalidateError = 'Bookmarks could not be refreshed.';
    mocks.session.mutationErrors.set(1, 'Could not update bookmark. Please try again.');
    const wrapper = mountBookmarks();
    await settle();

    expect(mocks.session.revalidateBookmarks).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain('Visible bookmark');
    expect(wrapper.text()).toContain('Bookmarks could not be refreshed.');
    expect(wrapper.text()).toContain('Could not update bookmark. Please try again.');
    expect(wrapper.get('[role="status"]').attributes('aria-live')).toBe('polite');
  });
});
