// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostDetailView from './PostDetailView.vue';
import type { Post } from '../types/Post';
import {
  captureBookmarkStateSyncVersion,
  registerBookmarksSessionSync,
  syncHydratedPostBookmarkState,
} from '../store/sessionSync';

const mocks = vi.hoisted(() => ({
  route: null as any,
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  routeLeave: vi.fn(),
  authStore: null as any,
  feedStore: {
    viewerID: 7,
    markPostDeleted: vi.fn(),
  },
  handoffStore: null as any,
  consumeHandoff: vi.fn(),
  getPostById: vi.fn(),
  deletePost: vi.fn(),
  getPostLikeState: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostState: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  getPostBookmarkStates: vi.fn(),
  bookmarkPost: vi.fn(),
  unbookmarkPost: vi.fn(),
  getPostReplies: vi.fn(),
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn(),
  },
  postViewTelemetry: {
    enqueue: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
  onBeforeRouteLeave: (guard: (to: { name?: string }) => void) => {
    mocks.routeLeave.mockImplementation(guard);
  },
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => mocks.handoffStore,
}));

vi.mock('../services/postService', () => ({
  deletePost: mocks.deletePost,
  getPostById: mocks.getPostById,
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

vi.mock('../services/bookmarkService', () => ({
  getPostBookmarkStates: mocks.getPostBookmarkStates,
  bookmarkPost: mocks.bookmarkPost,
  unbookmarkPost: mocks.unbookmarkPost,
}));

vi.mock('../services/replyService', () => ({
  createPostReply: mocks.createPostReply,
  deletePostReply: mocks.deletePostReply,
  getPostReplies: mocks.getPostReplies,
}));

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
  author: {
    id: 7,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
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
  reply_count: 0,
  view_count: 0,
  deleted: false,
};

const deferred = <T>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>(resolvePromise => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const mountDetail = () => mount(PostDetailView, {
  attachTo: document.body,
  global: {
    plugins: [createPinia()],
    stubs: {
      AppIcon: { template: '<span />' },
      AuthorIdentity: { template: '<span />' },
      LinkifiedText: {
        props: ['text'],
        template: '<span>{{ text }}</span>',
      },
      LikeAction: {
        emits: ['toggle'],
        template: '<button type="button" @click="$emit(\'toggle\')">Like</button>',
      },
      RepostAction: {
        emits: ['toggle'],
        template: '<button type="button" @click="$emit(\'toggle\')">Repost</button>',
      },
      ReplyComposer: { template: '<div />' },
      ReplyList: { template: '<div />' },
      ConfirmDialog: { template: '<div />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('PostDetailView bookmark hydration race', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({ params: { id: '42' }, query: {}, hash: '' });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer test-token',
      currentIdentity: { id: 7, username: 'viewer' },
    });
    mocks.handoffStore = { consume: mocks.consumeHandoff };
    mocks.consumeHandoff.mockReturnValue(null);
    mocks.getPostById.mockResolvedValue(post);
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostRepostState.mockResolvedValue({ reposts: 0, reposted: false });
    mocks.getPostBookmarkStates.mockResolvedValue({
      items: [{ post_id: 42, bookmarked: false }],
      unavailable_post_ids: [],
    });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.telemetry.flush.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    registerBookmarksSessionSync({
      applyExternalBookmarkStateLocal: vi.fn(),
    });
  });

  it('rejects publish hydration at Detail mutation start and accepts the later success', async () => {
    const bookmarkSink = vi.fn().mockReturnValue(true);
    registerBookmarksSessionSync({ applyExternalBookmarkStateLocal: bookmarkSink });

    wrapper = mountDetail();
    await flushPromises();
    await nextTick();

    const publishHydrationVersion = captureBookmarkStateSyncVersion(42);
    const mutation = deferred<{ post_id: number; bookmarked: boolean }>();
    mocks.bookmarkPost.mockReturnValueOnce(mutation.promise);

    const bookmark = wrapper.get('.post-detail__bookmark');
    await bookmark.trigger('click');

    expect(mocks.bookmarkPost).toHaveBeenCalledWith('42');
    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-busy')).toBe('true');

    expect(syncHydratedPostBookmarkState({
      postId: 42,
      bookmarked: false,
      status: 'ready',
    }, publishHydrationVersion)).toBe(false);
    expect(bookmarkSink).not.toHaveBeenCalled();

    await nextTick();
    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-busy')).toBe('true');

    mutation.resolve({ post_id: 42, bookmarked: true });
    await flushPromises();
    await nextTick();

    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-busy')).toBeUndefined();
    expect(bookmarkSink).toHaveBeenCalledWith({
      postId: 42,
      bookmarked: true,
      status: 'ready',
    });
  });
});
