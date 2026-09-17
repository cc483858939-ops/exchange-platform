// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive, ref } from 'vue';
import HistoryView from './HistoryView.vue';
import { useHistorySessionStore } from '../store/historySession';
import type { Post } from '../types/Post';
import { postToFeedPost } from '../utils/feedPost';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  routeLeaveGuard: null as (() => void) | null,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  bookmarksStore: null as any,
  route: null as any,
  getLikedHistory: vi.fn(),
  getPostLikeStates: vi.fn(),
  unlikePost: vi.fn(),
  externalLike: vi.fn(),
  historySync: null as any,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../services/historyService', () => ({
  getLikedHistory: mocks.getLikedHistory,
}));

vi.mock('../services/likeService', () => ({
  getPostLikeStates: mocks.getPostLikeStates,
  unlikePost: mocks.unlikePost,
}));

vi.mock('../store/bookmarksSession', () => ({
  useBookmarksSessionStore: () => mocks.bookmarksStore,
}));

vi.mock('../store/sessionSync', () => ({
  beginBookmarkStateMutation: vi.fn(),
  registerHistorySessionSync: vi.fn((sync: any) => { mocks.historySync = sync; }),
  syncExternalPostLikeState: vi.fn((update: any) => {
    mocks.externalLike(update);
    mocks.historySync?.applyExternalLikeStateLocal(update);
  }),
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: (guard: () => void) => {
    mocks.routeLeaveGuard = guard;
  },
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

const post = (id: number, content = `Post ${id}`): Post => ({
  id,
  created_at: '2026-08-17T00:00:00.000Z',
  updated_at: '2026-08-17T00:00:00.000Z',
  published_at: '2026-08-17T00:00:00.000Z',
  author: {
    id: 9,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
  content,
  language: 'und',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  reply_count: 1,
  view_count: 8,
  deleted: false,
});
const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
    token: id === null ? null : `Bearer token-${id}`,
  });
  return mocks.authStore;
};

const createBookmarksStore = () => ({
  items: ref<Post[]>([]),
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

const postCardStub = {
  props: ['post', 'trackView', 'likePending', 'repostPending', 'bookmarkPending'],
  emits: ['toggleLike', 'toggleRepost', 'toggleBookmark'],
  template: `
    <article
      class="history-post"
      :data-id="post.id"
      :data-status="post.likeStatus"
      :data-liked="String(post.liked)"
      :data-track-view="String(trackView)"
    >
      <span>{{ post.content }}</span>
      <button class="history-post__like" type="button" @click="$emit('toggleLike', post.id)">Unlike</button>
      <button class="history-post__repost" type="button" @click="$emit('toggleRepost', post.id)">Repost</button>
      <button class="history-post__bookmark" type="button" @click="$emit('toggleBookmark', post.id)">Bookmark</button>
    </article>
  `,
};

const mountHistory = () => mount(HistoryView, {
  global: {
    stubs: {
      PostCard: postCardStub,
      MobileAccountMenu: { template: '<div class="mobile-account-menu-stub" />' },
      AppIcon: { template: '<span class="test-icon" />' },
      RouterLink: {
        props: ['to'],
        template: '<a class="router-link-stub" :data-return-to="to && to.query && to.query.returnTo"><slot /></a>',
      },
    },
  },
});

const setHistoryTab = (tab: 'bookmarks' | 'likes' | string | undefined) => {
  mocks.route.query = tab === undefined ? {} : { tab };
  mocks.route.fullPath = tab === undefined ? '/history' : `/history?tab=${tab}`;
};

const setWindowScrollY = (value: number) => {
  Object.defineProperty(window, 'scrollY', { configurable: true, value });
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

describe('HistoryView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
    mocks.historySync = null;
    mocks.routeLeaveGuard = null;
    mocks.route = reactive({
      name: 'History',
      query: { tab: 'likes' },
      fullPath: '/history?tab=likes',
    });
    mocks.bookmarksStore = createBookmarksStore();
    mocks.bookmarksStore.saveScrollTop.mockImplementation((value: number) => {
      mocks.bookmarksStore.scrollTop.value = value;
    });
    setAuth(null);
    mocks.getLikedHistory.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.unlikePost.mockResolvedValue({ likes: 0, liked: false });
    setWindowScrollY(0);
  });

  afterEach(() => {
    setWindowScrollY(0);
    vi.restoreAllMocks();
  });

  it('uses Bookmarks as the default History tab and loads only the active session', async () => {
    setAuth(7);
    setHistoryTab(undefined);
    mocks.bookmarksStore.loaded.value = true;
    mocks.bookmarksStore.items.value = [post(42, 'Saved post')];
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.get('#history-bookmarks-tab').attributes('aria-selected')).toBe('true');
    expect(wrapper.get('#history-likes-tab').attributes('aria-selected')).toBe('false');
    expect(wrapper.find('#history-bookmarks-panel').exists()).toBe(true);
    expect(wrapper.text()).toContain('Saved post');
    expect(mocks.bookmarksStore.loadInitial).not.toHaveBeenCalled();
    expect(mocks.getLikedHistory).not.toHaveBeenCalled();
  });

  it('normalizes unknown History tab values to Bookmarks without loading Likes', async () => {
    setAuth(7);
    setHistoryTab('unexpected');
    mocks.bookmarksStore.loaded.value = true;
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.get('#history-bookmarks-tab').attributes('aria-selected')).toBe('true');
    expect(wrapper.get('#history-likes-tab').attributes('aria-selected')).toBe('false');
    expect(mocks.getLikedHistory).not.toHaveBeenCalled();
  });

  it('switches tabs through router.replace and keeps tab semantics accessible', async () => {
    setAuth(7);
    setHistoryTab(undefined);
    mocks.bookmarksStore.loaded.value = true;
    const wrapper = mountHistory();
    await flushPromises();

    const tablist = wrapper.get('[role="tablist"]');
    expect(tablist.get('[role="tab"]').attributes('aria-controls')).toBe('history-bookmarks-panel');
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2);
    await wrapper.get('#history-likes-tab').trigger('click');

    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'History', query: { tab: 'likes' } });
  });

  it('restores independent scroll positions for Bookmarks and Likes', async () => {
    setAuth(7);
    setHistoryTab(undefined);
    const historySession = useHistorySessionStore();
    historySession.items = [postToFeedPost(post(2))];
    historySession.loaded = true;
    historySession.initialLoading = false;
    historySession.scrollTop = 400;
    mocks.bookmarksStore.loaded.value = true;
    mocks.bookmarksStore.items.value = [post(1)];
    mocks.bookmarksStore.scrollTop.value = 1200;
    const wrapper = mountHistory();
    await flushPromises();

    const viewport = wrapper.get('.history-scroll-viewport').element as HTMLElement;
    expect(viewport.scrollTop).toBe(1200);

    setHistoryTab('likes');
    await flushPromises();
    expect(mocks.bookmarksStore.saveScrollTop).toHaveBeenCalledWith(1200);
    expect(viewport.scrollTop).toBe(400);

    setHistoryTab(undefined);
    await flushPromises();
    expect(viewport.scrollTop).toBe(1200);
    wrapper.unmount();
  });

  it('preserves Bookmarks behavior and delegates its mutations to the bookmark session', async () => {
    setAuth(7);
    setHistoryTab(undefined);
    mocks.bookmarksStore.loaded.value = true;
    mocks.bookmarksStore.items.value = [post(2, 'Second'), post(1, 'First')];
    mocks.bookmarksStore.likePendingPostIDs.value.add(2);
    mocks.bookmarksStore.repostPendingPostIDs.value.add(1);
    mocks.bookmarksStore.bookmarkPendingPostIDs.value.add(2);
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.findAll('.history-post').map(card => card.attributes('data-id'))).toEqual(['2', '1']);
    const cards = wrapper.findAllComponents(postCardStub);
    expect(cards[0].props('trackView')).toBe(false);
    expect(cards[0].props('likePending')).toBe(true);
    expect(cards[0].props('bookmarkPending')).toBe(true);
    expect(cards[1].props('repostPending')).toBe(true);

    await cards[0].get('.history-post__like').trigger('click');
    await cards[1].get('.history-post__repost').trigger('click');
    await cards[0].get('.history-post__bookmark').trigger('click');

    expect(mocks.bookmarksStore.toggleLike).toHaveBeenCalledWith(2);
    expect(mocks.bookmarksStore.toggleRepost).toHaveBeenCalledWith(1);
    expect(mocks.bookmarksStore.toggleBookmark).toHaveBeenCalledWith(2);
  });

  it('keeps bookmark pagination and retry controls scoped to the active tab', async () => {
    setAuth(7);
    setHistoryTab(undefined);
    mocks.bookmarksStore.loaded.value = true;
    mocks.bookmarksStore.items.value = [post(1)];
    mocks.bookmarksStore.nextCursor.value = 'cursor-1';
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('.history-view__sentinel .history-view__primary').trigger('click');
    expect(mocks.bookmarksStore.loadMore).toHaveBeenCalledTimes(1);
    expect(mocks.getLikedHistory).not.toHaveBeenCalled();

    mocks.bookmarksStore.loadMoreError.value = 'Could not load more bookmarks.';
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).toContain('Could not load more bookmarks.');
    await wrapper.get('.history-view__sentinel .history-view__primary').trigger('click');
    expect(mocks.bookmarksStore.retryLoadMore).toHaveBeenCalledTimes(1);
  });

  it('shows the login state without requesting history or like states when unauthenticated', async () => {
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.text()).toContain('Log in to view your history.');
    expect(wrapper.find('.router-link-stub').text()).toBe('Log in');
    expect(wrapper.find('.router-link-stub').attributes('data-return-to')).toBe('/history?tab=likes');
    expect(mocks.getLikedHistory).not.toHaveBeenCalled();
    expect(mocks.getPostLikeStates).not.toHaveBeenCalled();
  });

  it('loads the current viewer once, maps Posts, and opts PostCard out of feed telemetry', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(42)], next_cursor: null });
    const wrapper = mountHistory();
    await flushPromises();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
    expect(mocks.getLikedHistory).toHaveBeenCalledWith({ limit: 20 });
    const card = wrapper.find('.history-post');
    expect(card.attributes('data-id')).toBe('42');
    expect(card.attributes('data-status')).toBe('unknown');
    expect(card.attributes('data-liked')).toBe('false');
    expect(card.attributes('data-track-view')).toBe('false');
    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([42]);
  });

  it('preserves a pending history request when the same viewer refreshes their access token', async () => {
    setAuth(7);
    const pendingPage = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory.mockImplementationOnce(() => pendingPage.promise);
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 4, liked: true }],
      unavailable_post_ids: [],
    });
    const wrapper = mountHistory();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
    const authStore = mocks.authStore;
    authStore.token = 'Bearer token-7-b';
    await flushPromises();
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);

    pendingPage.resolve({ items: [post(1)], next_cursor: 'cursor-1' });
    await flushPromises();

    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('ready');
    expect(wrapper.find('[data-id="1"]').attributes('data-liked')).toBe('true');
    expect(wrapper.text()).toContain('Load more posts');
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
  });

  it('hydrates one page in a batch and keeps unavailable cards visible', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1), post(2)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 11, liked: true }],
      unavailable_post_ids: [2],
    });
    const wrapper = mountHistory();
    await flushPromises();

    const first = wrapper.find('[data-id="1"]');
    const second = wrapper.find('[data-id="2"]');
    expect(first.attributes('data-status')).toBe('ready');
    expect(first.attributes('data-liked')).toBe('true');
    expect(second.attributes('data-status')).toBe('unavailable');
    expect(mocks.getPostLikeStates).toHaveBeenCalledTimes(1);
    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([1, 2]);
  });

  it('suppresses a card immediately when Redis hydration says it is no longer liked', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: 'next-page' });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 0, liked: false }],
      unavailable_post_ids: [],
    });
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.find('.history-post').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('No liked posts yet.');
    expect(wrapper.text()).toContain('Load more posts');
  });

  it('marks all cards unavailable when batch hydration fails', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockRejectedValue(new Error('Redis unavailable'));
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('unavailable');
    expect(wrapper.find('[data-id="1"]').attributes('data-liked')).toBe('false');
  });

  it('retries an initial history failure without keeping stale page state', async () => {
    setAuth(7);
    mocks.getLikedHistory
      .mockRejectedValueOnce(new Error('history unavailable'))
      .mockResolvedValueOnce({ items: [post(3)], next_cursor: null });
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.text()).toContain('History could not be loaded.');
    await wrapper.get('button.history-view__primary').trigger('click');
    await flushPromises();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-id="3"]').exists()).toBe(true);
  });

  it('paginates in backend order and deduplicates post IDs', async () => {
    setAuth(7);
    mocks.getLikedHistory
      .mockResolvedValueOnce({ items: [post(1), post(2)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [post(2), post(3)], next_cursor: null });
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('button.history-view__primary').trigger('click');
    await flushPromises();

    expect(wrapper.findAll('.history-post').map(card => card.attributes('data-id'))).toEqual(['1', '2', '3']);
    expect(mocks.getLikedHistory).toHaveBeenNthCalledWith(2, { limit: 20, cursor: 'cursor-1' });
  });

  it('optimistically removes an unlike and keeps it suppressed after success', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 4, liked: true }],
      unavailable_post_ids: [],
    });
    mocks.unlikePost.mockResolvedValue({ likes: 3, liked: false });
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('.history-post__like').trigger('click');
    expect(wrapper.find('.history-post').exists()).toBe(false);
    await flushPromises();
    expect(wrapper.find('.history-post').exists()).toBe(false);
    expect(mocks.externalLike).toHaveBeenCalledWith({
      postId: 1,
      likes: 3,
      liked: false,
      status: 'ready',
    });
  });

  it('rolls an unlike failure back near its original index', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1), post(2)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [
        { post_id: 1, likes: 4, liked: true },
        { post_id: 2, likes: 2, liked: true },
      ],
      unavailable_post_ids: [],
    });
    mocks.unlikePost.mockRejectedValue(new Error('write failed'));
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('[data-id="1"] .history-post__like').trigger('click');
    await flushPromises();
    expect(wrapper.findAll('.history-post').map(card => card.attributes('data-id'))).toEqual(['1', '2']);
    expect(mocks.externalLike).not.toHaveBeenCalled();
    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('ready');
    expect(wrapper.find('[data-id="1"]').attributes('data-liked')).toBe('true');
    expect(wrapper.text()).toContain('Could not remove this like.');
  });

  it('rolls back a 503 unlike as unavailable', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 4, liked: true }],
      unavailable_post_ids: [],
    });
    mocks.unlikePost.mockRejectedValue({ response: { status: 503 } });
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('.history-post__like').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('unavailable');
    expect(wrapper.text()).toContain('Likes are temporarily unavailable.');
  });

  it('restores an unexpected liked=true unlike response using the response count', async () => {
    setAuth(7);
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 4, liked: true }],
      unavailable_post_ids: [],
    });
    mocks.unlikePost.mockResolvedValue({ likes: 8, liked: true });
    const wrapper = mountHistory();
    await flushPromises();

    await wrapper.get('.history-post__like').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('ready');
    expect(wrapper.find('[data-id="1"]').attributes('data-liked')).toBe('true');
    expect(mocks.externalLike).toHaveBeenCalledWith({
      postId: 1,
      likes: 8,
      liked: true,
      status: 'ready',
    });
  });

  it('ignores a pending page response after a viewer switch and starts the new request first', async () => {
    setAuth(1);
    const firstPage = deferred<{ items: Post[]; next_cursor: string | null }>();
    const secondPage = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory
      .mockImplementationOnce(() => firstPage.promise)
      .mockImplementationOnce(() => secondPage.promise);
    const wrapper = mountHistory();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);
    const authStore = mocks.authStore;
    Object.assign(authStore, {
      currentIdentity: { id: 2, username: 'viewer-2' },
      token: 'Bearer token-2',
    });
    await flushPromises();
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);

    secondPage.resolve({ items: [post(2)], next_cursor: null });
    await flushPromises();

    expect(wrapper.find('[data-id="2"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);

    firstPage.resolve({ items: [post(1)], next_cursor: null });
    await flushPromises();

    expect(wrapper.find('[data-id="2"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);
  });

  it('ignores a pending hydration response after a viewer switch', async () => {
    setAuth(1);
    const firstHydration = deferred<{
      items: Array<{ post_id: number; likes: number; liked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const secondPage = deferred<{ items: Post[]; next_cursor: string | null }>();
    mocks.getLikedHistory
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: null })
      .mockImplementationOnce(() => secondPage.promise);
    mocks.getPostLikeStates.mockImplementationOnce(() => firstHydration.promise);
    const wrapper = mountHistory();

    await flushPromises();
    expect(mocks.getPostLikeStates).toHaveBeenCalledTimes(1);
    expect(mocks.getPostLikeStates).toHaveBeenCalledWith([1]);

    const authStore = mocks.authStore;
    Object.assign(authStore, {
      currentIdentity: { id: 2, username: 'viewer-2' },
      token: 'Bearer token-2',
    });
    await flushPromises();
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);

    secondPage.resolve({ items: [post(2)], next_cursor: null });
    await flushPromises();
    expect(wrapper.find('[data-id="2"]').exists()).toBe(true);

    firstHydration.resolve({
      items: [{ post_id: 1, likes: 99, liked: true }],
      unavailable_post_ids: [],
    });
    await flushPromises();

    expect(wrapper.find('[data-id="2"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);
  });

  it('ignores a pending unlike response after logout', async () => {
    setAuth(1);
    const unlike = deferred<{ likes: number; liked: boolean }>();
    mocks.getLikedHistory.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({
      items: [{ post_id: 1, likes: 4, liked: true }],
      unavailable_post_ids: [],
    });
    mocks.unlikePost.mockImplementationOnce(() => unlike.promise);
    const wrapper = mountHistory();

    await flushPromises();
    expect(wrapper.find('[data-id="1"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').attributes('data-status')).toBe('ready');
    expect(wrapper.find('[data-id="1"]').attributes('data-liked')).toBe('true');

    await wrapper.get('[data-id="1"] .history-post__like').trigger('click');
    expect(mocks.unlikePost).toHaveBeenCalledTimes(1);
    expect(wrapper.find('.history-post').exists()).toBe(false);

    const authStore = mocks.authStore;
    authStore.isAuthenticated = false;
    authStore.currentIdentity = null;
    authStore.token = null;
    await flushPromises();

    expect(wrapper.text()).toContain('Log in to view your history.');
    expect(wrapper.find('.history-post').exists()).toBe(false);

    unlike.resolve({ likes: 4, liked: true });
    await flushPromises();

    expect(wrapper.find('.history-post').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Could not remove this like.');
  });

  it('loads a fresh page after logout and logging back in as the same viewer', async () => {
    setAuth(7);
    mocks.getLikedHistory
      .mockResolvedValueOnce({ items: [post(1)], next_cursor: null })
      .mockResolvedValueOnce({ items: [post(2)], next_cursor: null });
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.find('[data-id="1"]').exists()).toBe(true);
    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(1);

    const authStore = mocks.authStore;
    Object.assign(authStore, {
      isAuthenticated: false,
      currentIdentity: null,
      token: null,
    });
    await flushPromises();
    expect(wrapper.text()).toContain('Log in to view your history.');
    expect(wrapper.find('.history-post').exists()).toBe(false);

    Object.assign(authStore, {
      isAuthenticated: true,
      currentIdentity: { id: 7, username: 'viewer-7' },
      token: 'Bearer token-7-b',
    });
    await flushPromises();

    expect(mocks.getLikedHistory).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-id="2"]').exists()).toBe(true);
    expect(wrapper.find('[data-id="1"]').exists()).toBe(false);
  });

  it('restores cached History scroll once and ignores later session mutations', async () => {
    setAuth(7);
    const historySession = useHistorySessionStore();
    historySession.items = [postToFeedPost(post(1))];
    historySession.loaded = true;
    historySession.initialLoading = false;
    historySession.scrollTop = 640;
    const wrapper = mountHistory();
    await flushPromises();

    const viewport = wrapper.get('.history-scroll-viewport').element as HTMLElement;
    expect(viewport.scrollTop).toBe(640);

    viewport.scrollTop = 820;

    historySession.items = [...historySession.items, postToFeedPost(post(2))];
    historySession.nextCursor = 'cursor-2';
    historySession.loadingMore = true;
    historySession.loadingMore = false;
    historySession.revalidating = true;
    historySession.revalidating = false;
    historySession.applyReplyCountUpdateLocal({ postId: 1, replyCount: 12 });
    historySession.applyExternalLikeStateLocal({
      postId: 1,
      likes: 8,
      liked: true,
      status: 'ready',
    });
    historySession.stale = true;
    await flushPromises();

    expect(viewport.scrollTop).toBe(820);
    wrapper.unmount();
  });

  it('saves History scroll on unmount and restores it once on the next mount', async () => {
    setAuth(7);
    const historySession = useHistorySessionStore();
    historySession.items = [postToFeedPost(post(1))];
    historySession.loaded = true;
    historySession.initialLoading = false;
    historySession.scrollTop = 640;
    const firstWrapper = mountHistory();
    await flushPromises();

    const firstViewport = firstWrapper.get('.history-scroll-viewport').element as HTMLElement;
    firstViewport.scrollTop = 880;
    firstWrapper.unmount();

    const secondWrapper = mountHistory();
    await flushPromises();

    const secondViewport = secondWrapper.get('.history-scroll-viewport').element as HTMLElement;
    expect(historySession.scrollTop).toBe(880);
    expect(secondViewport.scrollTop).toBe(880);
    secondWrapper.unmount();
  });

  it('saves the internal viewport on route leave and ignores window scroll', async () => {
    setAuth(7);
    const historySession = useHistorySessionStore();
    historySession.items = [postToFeedPost(post(1))];
    historySession.loaded = true;
    historySession.initialLoading = false;
    const wrapper = mountHistory();
    await flushPromises();

    const viewport = wrapper.get('.history-scroll-viewport').element as HTMLElement;
    viewport.scrollTop = 1450;
    setWindowScrollY(777);

    expect(mocks.routeLeaveGuard).toBeTypeOf('function');
    mocks.routeLeaveGuard?.();

    expect(historySession.scrollTop).toBe(1450);
    wrapper.unmount();
  });
});
