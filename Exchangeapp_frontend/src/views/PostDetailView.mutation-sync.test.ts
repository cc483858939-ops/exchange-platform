// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import { reactive } from 'vue';
import PostDetailView from './PostDetailView.vue';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  getPostById: vi.fn(),
  getPostLikeState: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostState: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
  getPostReplies: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  getUser: vi.fn(),
  deletePost: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  },
  postViewTelemetry: { enqueue: vi.fn() },
  router: { back: vi.fn(), push: vi.fn(), replace: vi.fn() },
  route: { params: { id: '42' }, query: {}, hash: '' },
  routeLeave: vi.fn(),
  authStore: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    currentIdentity: { id: 7, username: 'viewer' },
  },
  feedStore: {
    viewerID: 7,
    markPostDeleted: vi.fn(),
  },
  externalLike: vi.fn(),
  externalBookmark: vi.fn(),
  externalRepost: vi.fn(),
  externalRemoval: vi.fn(),
  externalReplyCount: vi.fn(),
  beginBookmarkStateMutation: vi.fn(),
  detailSync: null as { applyExternalBookmarkStateLocal: (update: unknown) => boolean } | null,
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
  onBeforeRouteLeave: (guard: (to: { name?: string }) => void) => {
    mocks.routeLeave.mockImplementation(guard);
  },
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/feed', () => ({ useFeedStore: () => mocks.feedStore }));
vi.mock('../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));
vi.mock('../store/sessionSync', () => ({
  beginBookmarkStateMutation: mocks.beginBookmarkStateMutation,
  registerPostDetailSessionSync: vi.fn((sync: typeof mocks.detailSync) => {
    mocks.detailSync = sync;
  }),
  syncExternalPostLikeState: mocks.externalLike,
  syncExternalPostBookmarkState: mocks.externalBookmark,
  syncExternalPostRepostState: mocks.externalRepost,
  markOwnProfileTimelineStale: vi.fn(),
  syncExternalPostRemoval: mocks.externalRemoval,
  syncExternalReplyCount: mocks.externalReplyCount,
}));
vi.mock('../services/postService', () => ({
  getPostById: mocks.getPostById,
  deletePost: mocks.deletePost,
}));
vi.mock('../services/likeService', () => ({
  getPostLikeState: mocks.getPostLikeState,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));
vi.mock('../services/repostService', () => ({
  getPostRepostState: mocks.getPostRepostState,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));
vi.mock('../services/replyService', () => ({
  createPostReply: mocks.createPostReply,
  deletePostReply: mocks.deletePostReply,
  getPostReplies: mocks.getPostReplies,
}));
vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));
vi.mock('../services/userService', () => ({ getUser: mocks.getUser }));
vi.mock('../services/recommendationAttribution', () => ({
  consumePendingRecommendationAttribution: mocks.consumeAttribution,
}));
vi.mock('../services/recommendationTelemetry', () => ({
  getRecommendationTelemetry: () => mocks.telemetry,
}));
vi.mock('../services/postViewTelemetry', () => ({
  createPostViewEventID: () => '00000000-0000-4000-8000-000000000042',
  getPostViewTelemetry: () => mocks.postViewTelemetry,
}));

const post: Post = {
  id: 42,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: { id: 7, username: 'author', display_name: 'Author', avatar_url: '' },
  content: 'Post body',
  language: 'und',
  conversation_id: 42,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  repost_count: 0,
  reply_count: 2,
  view_count: 0,
  deleted: false,
};

const reply = (id: number): Post => ({
  id,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: { id: 8, username: 'commenter', display_name: 'Commenter', avatar_url: '' },
  content: `Reply ${id}`,
  language: 'und',
  conversation_id: 42,
  reply_to_post_id: 42,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  repost_count: 0,
  reply_count: 0,
  view_count: 0,
  deleted: false,
});

const ownReply = (id: number): Post => ({
  ...reply(id),
  author: { id: 7, username: 'viewer', display_name: 'Viewer', avatar_url: '' },
});

const deferred = <T,>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const mountDetail = () => mount(PostDetailView, {
  attachTo: document.body,
  global: {
    plugins: [createPinia()],
    stubs: {
      AppIcon: { template: '<span />' },
      AuthorIdentity: { template: '<span />' },
      RouterLink: { template: '<a><slot /></a>' },
      LikeAction: {
        props: ['liked', 'count', 'disabled', 'loading', 'pending'],
        emits: ['toggle'],
        template: '<button class="test-like" type="button" @click="$emit(\'toggle\')">{{ count }}</button>',
      },
      ReplyComposer: {
        emits: ['submit'],
        methods: { clear: vi.fn() },
        template: '<button class="test-create-comment" type="button" @click="$emit(\'submit\', \'hello\')">Reply</button>',
      },
      ReplyList: {
        props: ['replies', 'deletingReplyId', 'hasNext', 'bookmarkStates'],
        emits: ['requestDelete', 'load-more', 'toggle-bookmark'],
        template: '<div><button class="test-delete-comment" type="button" :disabled="deletingReplyId !== null" @click="$emit(\'requestDelete\', replies[0]?.id)">Delete reply</button><button v-if="hasNext" class="test-load-more" type="button" @click="$emit(\'load-more\')">More</button><button v-for="reply in replies" :key="reply.id" class="test-reply-bookmark" type="button" :data-id="reply.id" :data-state="bookmarkStates[reply.id]?.status ?? \'unknown\'" :data-bookmarked="String(bookmarkStates[reply.id]?.bookmarked ?? false)" @click="$emit(\'toggle-bookmark\', reply.id)">{{ bookmarkStates[reply.id]?.status }}:{{ bookmarkStates[reply.id]?.bookmarked }}</button></div>',
      },
      ConfirmDialog: {
        props: ['title', 'description', 'confirmLabel', 'cancelLabel', 'danger', 'busy', 'error'],
        emits: ['confirm', 'cancel'],
        template: '<div class="test-confirm-dialog"><h2>{{ title }}</h2><p>{{ description }}</p><span v-if="error" class="test-confirm-error">{{ error }}</span><button class="test-confirm-cancel" type="button" :disabled="busy" @click="$emit(\'cancel\')">{{ cancelLabel }}</button><button class="test-confirm-delete" type="button" :disabled="busy" @click="$emit(\'confirm\')">{{ busy ? \'Deleting…\' : confirmLabel }}</button></div>',
      },
    },
  },
});

describe('PostDetailView mutation synchronization', () => {
  let originalHistoryState: unknown;
  let originalHistoryURL = '';

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({ params: { id: '42' }, query: {}, hash: '' });
    originalHistoryState = window.history.state;
    originalHistoryURL = window.location.href;
    window.history.replaceState({ back: null }, '', originalHistoryURL);
    mocks.getPostById.mockResolvedValue(post);
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostRepostState.mockResolvedValue({ reposts: 0, reposted: false });
    mocks.getPostReplies.mockResolvedValue({ items: [reply(9)], next_cursor: null });
    mocks.getPostBookmarkStates.mockImplementation(async (postIDs: number[]) => ({
      items: postIDs.map(postID => ({ post_id: postID, bookmarked: false })),
      unavailable_post_ids: [],
    }));
    mocks.bookmarkPost.mockResolvedValue({ post_id: 9, bookmarked: true });
    mocks.unbookmarkPost.mockResolvedValue({ post_id: 9, bookmarked: false });
    mocks.getUser.mockResolvedValue({
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
      bio: '',
      created_at: '2026-08-15T00:00:00.000Z',
    });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
    mocks.detailSync = null;
  });

  afterEach(() => {
    window.history.replaceState(originalHistoryState, '', originalHistoryURL);
  });

  it('syncs a successful Detail like but not a failed like', async () => {
    mocks.likePost.mockResolvedValueOnce({ liked: true, likes: 4 });
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).toHaveBeenCalledWith({
      postId: 42,
      likes: 4,
      liked: true,
      status: 'ready',
    });

    mounted.unmount();
    mocks.externalLike.mockClear();
    mocks.likePost.mockRejectedValueOnce(new Error('offline'));
    const failed = mountDetail();
    await flushPromises();
    await failed.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).not.toHaveBeenCalled();
    failed.unmount();
  });

  it('begins the shared fence before a main Post bookmark request settles', async () => {
    const mutation = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.bookmarkPost.mockReturnValueOnce(mutation.promise);
    const mounted = mountDetail();
    await flushPromises();
    await mounted.vm.$nextTick();

    const bookmark = mounted.get('.post-detail__bookmark');
    await bookmark.trigger('click');

    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledTimes(1);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(42);
    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-busy')).toBe('true');

    mutation.resolve({ post_id: 42, bookmarked: true });
    await flushPromises();

    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-busy')).toBeUndefined();
    mounted.unmount();
  });

  it('optimistically toggles Detail Repost, settles from server state, and syncs cached surfaces', async () => {
    mocks.getPostRepostState.mockResolvedValueOnce({ reposts: 8, reposted: false });
    mocks.repostPost.mockResolvedValueOnce({ reposts: 9, reposted: true });
    const mounted = mountDetail();
    await flushPromises();

    const repost = mounted.find('.repost-action');
    expect(repost.attributes('aria-label')).toBe('Repost post, 8 reposts');
    await repost.trigger('click');
    expect(repost.text()).toContain('9');
    await flushPromises();

    expect(mocks.repostPost).toHaveBeenCalledWith('42');
    expect(mocks.externalRepost).toHaveBeenCalledWith({
      postId: 42,
      reposts: 9,
      reposted: true,
      status: 'ready',
    });
    expect(repost.attributes('aria-label')).toBe('Undo repost, 9 reposts');
    mounted.unmount();
  });

  it('rolls Detail Repost back with the specified error after mutation failure', async () => {
    mocks.getPostRepostState.mockResolvedValueOnce({ reposts: 8, reposted: false });
    mocks.repostPost.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.repost-action').trigger('click');
    await flushPromises();

    expect(mounted.find('.repost-action').text()).toContain('8');
    expect(mounted.find('.detail-inline-error').text()).toBe('Could not update repost. Please try again.');
    expect(mocks.externalRepost).not.toHaveBeenCalled();
    mounted.unmount();
  });

  it('keeps bookmark hydration for concurrent reply pages independent', async () => {
    const pageOneHydration = deferred<{
      items: Array<{ post_id: number; bookmarked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const pageTwoHydration = deferred<{
      items: Array<{ post_id: number; bookmarked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    mocks.getPostReplies
      .mockResolvedValueOnce({ items: [reply(9)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [reply(10)], next_cursor: null });
    mocks.getPostBookmarkStates.mockImplementation((postIDs: number[]) => {
      if (postIDs.includes(42)) {
        return Promise.resolve({ items: [{ post_id: 42, bookmarked: false }], unavailable_post_ids: [] });
      }
      return postIDs.includes(9) ? pageOneHydration.promise : pageTwoHydration.promise;
    });

    const mounted = mountDetail();
    await flushPromises();
    await mounted.get('.test-load-more').trigger('click');
    await flushPromises();

    pageTwoHydration.resolve({
      items: [{ post_id: 10, bookmarked: true }],
      unavailable_post_ids: [],
    });
    await flushPromises();

    expect(mounted.get('[data-id="9"]').attributes('data-state')).toBe('unknown');
    expect(mounted.get('[data-id="10"]').attributes('data-state')).toBe('ready');

    pageOneHydration.resolve({
      items: [{ post_id: 9, bookmarked: false }],
      unavailable_post_ids: [],
    });
    await flushPromises();

    expect(mounted.get('[data-id="9"]').attributes('data-state')).toBe('ready');
    expect(mounted.get('[data-id="9"]').attributes('data-bookmarked')).toBe('false');
    expect(mounted.get('[data-id="10"]').attributes('data-bookmarked')).toBe('true');
    mounted.unmount();
  });

  it('does not let an older reply bookmark hydration overwrite a mutation', async () => {
    const hydration = deferred<{
      items: Array<{ post_id: number; bookmarked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const mutation = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.getPostBookmarkStates.mockImplementation((postIDs: number[]) => (
      postIDs.includes(42)
        ? Promise.resolve({ items: [{ post_id: 42, bookmarked: false }], unavailable_post_ids: [] })
        : hydration.promise
    ));

    const mounted = mountDetail();
    await flushPromises();
    mocks.detailSync?.applyExternalBookmarkStateLocal({
      postId: 9,
      bookmarked: false,
      status: 'ready',
    });
    await mounted.vm.$nextTick();
    mocks.bookmarkPost.mockReturnValueOnce(mutation.promise);
    await mounted.get('[data-id="9"]').trigger('click');

    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledTimes(1);
    expect(mocks.beginBookmarkStateMutation).toHaveBeenCalledWith(9);

    hydration.resolve({
      items: [{ post_id: 9, bookmarked: false }],
      unavailable_post_ids: [],
    });
    await flushPromises();
    expect(mounted.get('[data-id="9"]').attributes('data-bookmarked')).toBe('true');

    mutation.resolve({ post_id: 9, bookmarked: true });
    await flushPromises();
    expect(mounted.get('[data-id="9"]').attributes('data-state')).toBe('ready');
    expect(mounted.get('[data-id="9"]').attributes('data-bookmarked')).toBe('true');
    mounted.unmount();
  });

  it('drops reply bookmark hydration from the previous detail after a route change', async () => {
    const oldHydration = deferred<{
      items: Array<{ post_id: number; bookmarked: boolean }>;
      unavailable_post_ids: number[];
    }>();
    const nextPost = { ...post, id: 43, conversation_id: 43 };
    const nextReply = { ...reply(10), conversation_id: 43, reply_to_post_id: 43 };
    mocks.getPostById
      .mockResolvedValueOnce(post)
      .mockResolvedValueOnce(nextPost);
    mocks.getPostReplies
      .mockResolvedValueOnce({ items: [reply(9)], next_cursor: null })
      .mockResolvedValueOnce({ items: [nextReply], next_cursor: null });
    mocks.getPostBookmarkStates.mockImplementation((postIDs: number[]) => {
      if (postIDs.includes(9)) return oldHydration.promise;
      return Promise.resolve({
        items: postIDs.map(postID => ({ post_id: postID, bookmarked: false })),
        unavailable_post_ids: [],
      });
    });

    const mounted = mountDetail();
    await flushPromises();
    expect(mounted.find('[data-id="9"]').exists()).toBe(true);

    mocks.route.params.id = '43';
    await flushPromises();
    await flushPromises();

    expect(mounted.get('[data-id="10"]').attributes('data-state')).toBe('ready');
    oldHydration.resolve({
      items: [{ post_id: 9, bookmarked: true }],
      unavailable_post_ids: [],
    });
    await flushPromises();

    expect(mounted.find('[data-id="9"]').exists()).toBe(false);
    expect(mounted.get('[data-id="10"]').attributes('data-bookmarked')).toBe('false');
    mounted.unmount();
  });

  it.each([
    ['success', undefined],
    ['terminal 404', { response: { status: 404 } }],
  ])('syncs Detail deletion before navigation on %s', async (_label, error) => {
    if (error) mocks.deletePost.mockRejectedValueOnce(error);
    window.history.replaceState({ back: '/history' }, '', window.location.href);
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.post-detail__delete').trigger('click');
    expect(mounted.find('.test-confirm-dialog').exists()).toBe(true);
    expect(mounted.find('.test-confirm-dialog h2').text()).toBe('Delete post?');
    expect(mocks.deletePost).not.toHaveBeenCalled();

    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.feedStore.markPostDeleted).toHaveBeenCalledWith(42, 7);
    expect(mocks.externalRemoval).toHaveBeenCalledWith(42);
    expect(mocks.router.back).toHaveBeenCalledTimes(1);
    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(mocks.feedStore.markPostDeleted.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.externalRemoval.mock.invocationCallOrder[0]);
    expect(mocks.externalRemoval.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.router.back.mock.invocationCallOrder[0]);
    expect(mounted.find('.test-confirm-dialog').exists()).toBe(false);
    mounted.unmount();
  });

  it.each([
    '/',
    '/history',
    '/users/7',
    '/notifications',
  ])('returns to the previous app entry after deleting from %s', async back => {
    window.history.replaceState({ back }, '', window.location.href);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.router.back).toHaveBeenCalledTimes(1);
    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(mocks.externalRemoval).toHaveBeenCalledTimes(1);
    expect(mocks.externalRemoval.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.router.back.mock.invocationCallOrder[0]);
    mounted.unmount();
  });

  it.each([null, '', '   '])('falls back to Own Profile without a valid back target: %s', async back => {
    window.history.replaceState({ back }, '', window.location.href);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.router.back).not.toHaveBeenCalled();
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'UserProfile',
      params: { id: '7' },
    });
    mounted.unmount();
  });

  it('opens Post deletion confirmation and Cancel leaves all mutation state untouched', async () => {
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');

    expect(mounted.find('.test-confirm-dialog h2').text()).toBe('Delete post?');
    expect(mocks.deletePost).not.toHaveBeenCalled();
    expect(mocks.feedStore.markPostDeleted).not.toHaveBeenCalled();
    expect(mocks.externalRemoval).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();

    await mounted.find('.test-confirm-cancel').trigger('click');

    expect(mounted.find('.test-confirm-dialog').exists()).toBe(false);
    expect(mocks.deletePost).not.toHaveBeenCalled();
    expect(mocks.feedStore.markPostDeleted).not.toHaveBeenCalled();
    expect(mocks.externalRemoval).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
  });

  it('keeps Post deletion failure in the confirmation dialog without synchronizing removal', async () => {
    mocks.deletePost.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mounted.find('.test-confirm-dialog').exists()).toBe(true);
    expect(mounted.find('.test-confirm-error').text())
      .toBe('Could not delete post. Please try again.');
    expect(mounted.find('.detail-inline-error').exists()).toBe(false);
    expect(mocks.feedStore.markPostDeleted).not.toHaveBeenCalled();
    expect(mocks.externalRemoval).not.toHaveBeenCalled();
    expect(mocks.router.back).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
  });

  it('preserves the 403 Post deletion error copy inside the confirmation dialog', async () => {
    mocks.deletePost.mockRejectedValueOnce({ response: { status: 403 } });
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mounted.find('.test-confirm-error').text())
      .toBe('You can only delete your own posts.');
    expect(mocks.feedStore.markPostDeleted).not.toHaveBeenCalled();
    expect(mocks.router.back).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
  });

  it('blocks repeated Post deletion requests while the confirmation is busy', async () => {
    let resolveDelete!: () => void;
    const pendingDelete = new Promise<void>(resolve => {
      resolveDelete = resolve;
    });
    mocks.deletePost.mockReturnValueOnce(pendingDelete);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.post-detail__delete').trigger('click');
    await mounted.find('.test-confirm-delete').trigger('click');
    await mounted.vm.$nextTick();

    expect(mocks.deletePost).toHaveBeenCalledTimes(1);
    expect(mounted.find('.test-confirm-delete').attributes('disabled')).toBe('');
    expect(mounted.find('.test-confirm-cancel').attributes('disabled')).toBe('');

    await mounted.find('.test-confirm-delete').trigger('click');
    expect(mocks.deletePost).toHaveBeenCalledTimes(1);

    resolveDelete();
    await flushPromises();
  });

  it('syncs absolute comment counts after create and delete success', async () => {
    mocks.createPostReply.mockResolvedValueOnce(ownReply(10));
    mocks.deletePostReply.mockResolvedValueOnce(undefined);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();
    expect(mocks.externalReplyCount).toHaveBeenNthCalledWith(1, {
      postId: 42,
      replyCount: 3,
    });

    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();
    expect(mocks.deletePostReply).not.toHaveBeenCalled();
    expect(mounted.find('.test-confirm-dialog').exists()).toBe(true);

    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.deletePostReply).toHaveBeenCalledWith(10);
    expect(mocks.externalReplyCount).toHaveBeenNthCalledWith(2, {
      postId: 42,
      replyCount: 2,
    });
  });

  it('does not synchronize failed reply creation', async () => {
    mocks.createPostReply.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();

    expect(mocks.externalReplyCount).not.toHaveBeenCalled();
  });

  it('does not synchronize a failed confirmed reply deletion', async () => {
    mocks.getPostReplies.mockResolvedValueOnce({ items: [ownReply(9)], next_cursor: null });
    mocks.deletePostReply.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.deletePostReply).toHaveBeenCalledWith(9);
    expect(mocks.externalReplyCount).not.toHaveBeenCalled();
    expect(mounted.find('.test-confirm-error').text())
      .toBe('Reply could not be deleted. Please try again.');
  });
});
