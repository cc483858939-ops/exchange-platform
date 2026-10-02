import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  searchPosts: vi.fn(),
  getPostEngagementStates: vi.fn(),
  executeLikeToggle: vi.fn(),
  executeRepostToggle: vi.fn(),
  executeBookmarkToggle: vi.fn(),
  syncPostSearchLikeState: vi.fn(),
  syncPostSearchRepostState: vi.fn(),
  syncPostSearchBookmarkState: vi.fn(),
}));

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/postSearchService', () => ({ searchPosts: mocks.searchPosts }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.getPostEngagementStates }));
vi.mock('./sessionSync', () => ({
  beginBookmarkStateMutation: vi.fn(),
  registerPostSearchSessionSync: vi.fn(),
  syncPostSearchLikeState: mocks.syncPostSearchLikeState,
  syncPostSearchRepostState: mocks.syncPostSearchRepostState,
  syncPostSearchBookmarkState: mocks.syncPostSearchBookmarkState,
}));
vi.mock('./engagementOperations', () => ({
  createOptimisticLikeUpdate: (post: any) => ({ postId: post.id, likes: post.likeCount + (post.liked ? -1 : 1), liked: !post.liked, status: 'ready' }),
  createOptimisticRepostUpdate: (post: any) => ({ postId: post.id, reposts: post.repostCount + (post.reposted ? -1 : 1), reposted: !post.reposted, status: 'ready' }),
  createOptimisticBookmarkUpdate: (post: any) => ({ postId: post.id, bookmarked: !post.bookmarked, status: 'ready' }),
  executeLikeToggle: mocks.executeLikeToggle,
  executeRepostToggle: mocks.executeRepostToggle,
  executeBookmarkToggle: mocks.executeBookmarkToggle,
}));

import { usePostSearchSessionStore, type PostSearchCriteriaV1 } from './postSearchSession';

const makePost = (id: number): Post => ({
  id,
  created_at: '2026-09-30T12:00:00.000Z',
  updated_at: '2026-09-30T12:00:00.000Z',
  published_at: '2026-09-30T12:00:00.000Z',
  author: { id: 8, username: 'writer', display_name: 'Writer', avatar_url: '' },
  content: `Post ${id}`,
  language: 'en',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 4,
  repost_count: 2,
  reply_count: 1,
  quote_count: 0,
  view_count: 0,
  deleted: false,
});

const page = (ids: number[], next_cursor: string | null = null) => ({
  items: ids.map(makePost),
  next_cursor,
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
};

const settle = async () => {
  await Promise.resolve();
  await Promise.resolve();
  await nextTick();
};

const criteria = (query: string, overrides: Partial<PostSearchCriteriaV1> = {}): PostSearchCriteriaV1 => ({
  query,
  authorId: null,
  time: { kind: 'any' },
  sort: 'latest',
  ...overrides,
});

describe('postSearchSession', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.searchPosts.mockResolvedValue(page([]));
    mocks.getPostEngagementStates.mockResolvedValue({ items: [] });
    mocks.executeLikeToggle.mockResolvedValue({ likes: 5, liked: true });
    mocks.executeRepostToggle.mockResolvedValue({ reposts: 3, reposted: true });
    mocks.executeBookmarkToggle.mockResolvedValue({ bookmarked: true });
  });

  it('trims text and resolves the criteria into the service query', async () => {
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('  yen  ', {
      authorId: 42,
      time: { kind: 'custom', from: '2026-09-01T08:00:00+08:00', to: '2026-10-01T08:00:00+08:00' },
    }));
    await settle();

    expect(mocks.searchPosts).toHaveBeenCalledWith({
      q: 'yen',
      author_id: 42,
      from: '2026-09-01T00:00:00.000Z',
      to: '2026-10-01T00:00:00.000Z',
      sort: 'latest',
      limit: 20,
    });
  });

  it('applies only valid absolute quote counts to the loaded search result', async () => {
    mocks.searchPosts.mockResolvedValueOnce(page([1]));
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen'));
    await settle();

    expect(store.applyQuoteCountUpdateLocal({ postId: 1, quoteCount: 8 })).toBe(true);
    expect(store.items[0].quoteCount).toBe(8);
    expect(store.applyQuoteCountUpdateLocal({ postId: 1, quoteCount: 1.5 })).toBe(false);
    expect(store.applyQuoteCountUpdateLocal({ postId: 0, quoteCount: 9 })).toBe(false);
    expect(store.applyQuoteCountUpdateLocal({ postId: 99, quoteCount: 9 })).toBe(false);
    expect(store.items[0].quoteCount).toBe(8);
  });

  it('ignores an older initial result after criteria change', async () => {
    const stale = deferred<ReturnType<typeof page>>();
    mocks.searchPosts.mockReturnValueOnce(stale.promise).mockResolvedValueOnce(page([2]));
    const store = usePostSearchSessionStore();

    store.activateCriteria(criteria('alpha'));
    store.activateCriteria(criteria('beta'));
    await settle();
    stale.resolve(page([1]));
    await settle();

    expect(store.items.map(post => post.id)).toEqual([2]);
  });

  it('ignores an in-flight page after the criteria changes', async () => {
    const pageTwo = deferred<ReturnType<typeof page>>();
    mocks.searchPosts.mockResolvedValueOnce(page([1], 'cursor-a'))
      .mockReturnValueOnce(pageTwo.promise)
      .mockResolvedValueOnce(page([3]));
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('alpha'));
    await settle();

    const pendingPage = store.loadMore();
    store.activateCriteria(criteria('beta'));
    await settle();
    pageTwo.resolve(page([2]));
    await pendingPage;

    expect(store.items.map(post => post.id)).toEqual([3]);
    expect(store.nextCursor).toBeNull();
  });

  it('does not reinsert a post tombstoned before an in-flight page resolves', async () => {
    const pageTwo = deferred<ReturnType<typeof page>>();
    mocks.searchPosts.mockResolvedValueOnce(page([1], 'cursor-a'))
      .mockReturnValueOnce(pageTwo.promise);
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen'));
    await settle();

    const pendingPage = store.loadMore();
    expect(store.removePostLocal(25)).toBe(false);
    pageTwo.resolve(page([25, 26]));
    await pendingPage;

    expect(store.items.map(post => post.id)).toEqual([1, 26]);
    expect(store.nextCursor).toBeNull();
  });

  it('removes a loaded post immediately and suppresses it from a stale page', async () => {
    const pageTwo = deferred<ReturnType<typeof page>>();
    mocks.searchPosts.mockResolvedValueOnce(page([10], 'cursor-a'))
      .mockReturnValueOnce(pageTwo.promise);
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen'));
    await settle();

    const pendingPage = store.loadMore();
    expect(store.removePostLocal(10)).toBe(true);
    expect(store.items.map(post => post.id)).toEqual([]);
    pageTwo.resolve(page([10, 11]));
    await pendingPage;

    expect(store.items.map(post => post.id)).toEqual([11]);
    expect(store.nextCursor).toBeNull();
  });

  it('keeps relative time bounds fixed across cursor pages and tab reactivation', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-01T00:00:00.000Z'));
    mocks.searchPosts.mockResolvedValueOnce(page([1], 'cursor-a')).mockResolvedValueOnce(page([2], null));
    const store = usePostSearchSessionStore();
    const selected = criteria('yen', { time: { kind: 'relative', duration: '7d' } });
    store.activateCriteria(selected);
    await settle();
    const original = { from: store.resolvedFrom, to: store.resolvedTo };

    vi.setSystemTime(new Date('2026-10-01T06:00:00.000Z'));
    await store.loadMore();
    store.activateCriteria(selected);
    await settle();

    expect(mocks.searchPosts).toHaveBeenCalledTimes(2);
    expect(mocks.searchPosts.mock.calls[1][0]).toMatchObject({ from: original.from, to: original.to, cursor: 'cursor-a' });
    vi.useRealTimers();
  });

  it('starts a fresh relative range when the same search is explicitly retried', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-01T00:00:00.000Z'));
    mocks.searchPosts.mockResolvedValue(page([]));
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen', { time: { kind: 'relative', duration: '24h' } }));
    await settle();
    const firstFrom = mocks.searchPosts.mock.calls[0][0].from;

    vi.setSystemTime(new Date('2026-10-01T01:00:00.000Z'));
    store.reload();
    await settle();

    expect(mocks.searchPosts).toHaveBeenCalledTimes(2);
    expect(mocks.searchPosts.mock.calls[1][0].from).not.toBe(firstFrom);
    vi.useRealTimers();
  });

  it('keeps a successful local Like when older engagement hydration returns', async () => {
    const hydration = deferred<{ items: any[] }>();
    mocks.getPostEngagementStates.mockReturnValueOnce(hydration.promise);
    mocks.searchPosts.mockResolvedValueOnce(page([1]));
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen'));
    await settle();
    const post = store.items[0];
    post.likeStatus = 'ready';
    post.liked = false;
    post.likeCount = 4;

    await expect(store.toggleLike(1)).resolves.toBe('succeeded');
    hydration.resolve({ items: [{
      post_id: 1,
      like: { status: 'ready', likes: 0, liked: false },
      repost: { status: 'ready', reposts: 0, reposted: false },
      bookmark: { status: 'ready', bookmarked: false },
    }] });
    await settle();

    expect(post.liked).toBe(true);
    expect(post.likeCount).toBe(5);
    expect(mocks.syncPostSearchLikeState).toHaveBeenCalledWith({ postId: 1, likes: 5, liked: true, status: 'ready' });
  });

  it('ignores an old viewer result after account switch', async () => {
    const oldViewer = deferred<ReturnType<typeof page>>();
    mocks.searchPosts.mockReturnValueOnce(oldViewer.promise).mockResolvedValueOnce(page([8]));
    const store = usePostSearchSessionStore();
    store.activateCriteria(criteria('yen'));

    mocks.authStore.currentIdentity = { id: 8 };
    await nextTick();
    await settle();
    oldViewer.resolve(page([7]));
    await settle();

    expect(store.viewerID).toBe(8);
    expect(store.items.map(post => post.id)).toEqual([8]);
  });
});
