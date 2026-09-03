// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import HistoryView from './HistoryView.vue';
import { useHistorySessionStore } from '../store/historySession';
import type { Post } from '../types/Post';
import { postToFeedPost } from '../utils/feedPost';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
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

vi.mock('../store/sessionSync', () => ({
  registerHistorySessionSync: vi.fn((sync: any) => { mocks.historySync = sync; }),
  syncExternalPostLikeState: vi.fn((update: any) => {
    mocks.externalLike(update);
    mocks.historySync?.applyExternalLikeStateLocal(update);
  }),
}));

vi.mock('vue-router', () => ({
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

const postCardStub = {
  props: ['post', 'trackView', 'likePending'],
  emits: ['toggleLike'],
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
    </article>
  `,
};

const mountHistory = () => mount(HistoryView, {
  global: {
    stubs: {
      PostCard: postCardStub,
      AppIcon: { template: '<span class="test-icon" />' },
      RouterLink: { template: '<a class="router-link-stub"><slot /></a>' },
    },
  },
});

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
    setAuth(null);
    mocks.getLikedHistory.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
    mocks.unlikePost.mockResolvedValue({ likes: 0, liked: false });
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    setWindowScrollY(0);
  });

  afterEach(() => {
    setWindowScrollY(0);
    vi.restoreAllMocks();
  });

  it('shows the login state without requesting history or like states when unauthenticated', async () => {
    const wrapper = mountHistory();
    await flushPromises();

    expect(wrapper.text()).toContain('Log in to view your history.');
    expect(wrapper.find('.router-link-stub').text()).toBe('Log in');
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
    historySession.scrollY = 640;

    const scrollTo = window.scrollTo as ReturnType<typeof vi.fn>;
    const wrapper = mountHistory();
    await flushPromises();

    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith({ top: 640, behavior: 'auto' });

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

    expect(scrollTo).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it('saves History scroll on unmount and restores it once on the next mount', async () => {
    setAuth(7);
    const historySession = useHistorySessionStore();
    historySession.items = [postToFeedPost(post(1))];
    historySession.loaded = true;
    historySession.initialLoading = false;
    historySession.scrollY = 640;

    const scrollTo = window.scrollTo as ReturnType<typeof vi.fn>;
    const firstWrapper = mountHistory();
    await flushPromises();
    expect(scrollTo).toHaveBeenCalledTimes(1);

    setWindowScrollY(880);
    firstWrapper.unmount();
    scrollTo.mockClear();

    const secondWrapper = mountHistory();
    await flushPromises();

    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith({ top: 880, behavior: 'auto' });
    secondWrapper.unmount();
  });
});
