// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';
import PostDetailView from './PostDetailView.vue';

const mocks = vi.hoisted(() => ({
  getPostById: vi.fn(),
  getPostLikeState: vi.fn(),
  getPostRepostState: vi.fn().mockResolvedValue({ reposts: 0, reposted: false }),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostReplies: vi.fn(),
  getUser: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  },
  postViewTelemetry: {
    enqueue: vi.fn(),
  },
  routeLeave: vi.fn(),
  router: {
    back: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
  },
  authStore: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    refreshToken: null,
    currentIdentity: {
      id: 7,
      username: 'reader',
    },
  },
  feedStore: {
    viewerID: 7,
    markPostDeleted: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => ({
    params: { id: '42' },
    query: {},
    hash: '',
  }),
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
  usePostDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));

vi.mock('../services/postService', () => ({
  deletePost: vi.fn(),
  getPostById: mocks.getPostById,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
}));

vi.mock('../services/likeService', () => ({
  getPostLikeState: mocks.getPostLikeState,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));

vi.mock('../services/repostService', () => ({
  getPostRepostState: mocks.getPostRepostState,
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
}));

vi.mock('../services/replyService', () => ({
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
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

const post = {
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
  conversation_id: 42,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  reply_count: 2,
  view_count: 12,
  deleted: false,
};

type LikeResult = { liked: boolean; likes: number };

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

describe('PostDetailView LikeAction wiring', () => {
  let mounted: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.routeLeave.mockReset();
    mocks.getPostById.mockResolvedValue(post);
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUser.mockResolvedValue({
      id: 7,
      username: 'reader',
      display_name: 'Reader',
      avatar_url: '',
      bio: '',
      created_at: '2026-08-15T00:00:00.000Z',
    });
    mocks.consumeAttribution.mockReturnValue(null);
  });

  afterEach(() => {
    mounted?.unmount();
    mounted = null;
  });

  const mountDetail = () => mount(PostDetailView, {
    attachTo: document.body,
    global: {
      plugins: [createPinia()],
      stubs: {
        AppIcon: { template: '<span />' },
        AuthorIdentity: { template: '<span />' },
        ReplyComposer: { template: '<div />' },
        ReplyList: { template: '<div />' },
        RouterLink: { template: '<a><slot /></a>' },
        LikeAction: {
          props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'ariaPressed', 'variant'],
          emits: ['toggle'],
          template: '<button class="test-like-action" type="button" :disabled="disabled || loading || pending" :data-liked="liked" :data-count="count" :data-loading="loading" :data-pending="pending" :data-aria-label="ariaLabel" @click="$emit(\'toggle\')">{{ count }}</button>',
        },
      },
    },
  });

  it('forwards detail like state, labels, and pending state before success reconciliation', async () => {
    const request = deferred<LikeResult>();
    mocks.likePost.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();

    const likeAction = mounted.find('.test-like-action');
    expect(likeAction.attributes('data-liked')).toBe('false');
    expect(likeAction.attributes('data-count')).toBe('3');
    expect(likeAction.attributes('data-loading')).toBe('false');
    expect(likeAction.attributes('data-pending')).toBe('false');
    expect(likeAction.attributes('data-aria-label')).toBe('Like post, 3 likes');

    await likeAction.trigger('click');
    expect(likeAction.attributes('data-liked')).toBe('true');
    expect(likeAction.attributes('data-count')).toBe('4');
    expect(likeAction.attributes('data-pending')).toBe('true');
    expect(mocks.likePost).toHaveBeenCalledWith('42');

    request.resolve({ liked: true, likes: 4 });
    await flushPromises();

    expect(likeAction.attributes('data-liked')).toBe('true');
    expect(likeAction.attributes('data-count')).toBe('4');
    expect(likeAction.attributes('data-pending')).toBe('false');
  });

  it('rolls back optimistic detail like state on failure without changing the wiring path', async () => {
    const request = deferred<LikeResult>();
    mocks.likePost.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();

    const likeAction = mounted.find('.test-like-action');
    await likeAction.trigger('click');
    expect(likeAction.attributes('data-liked')).toBe('true');
    expect(likeAction.attributes('data-count')).toBe('4');

    request.reject(new Error('offline'));
    await flushPromises();

    expect(likeAction.attributes('data-liked')).toBe('false');
    expect(likeAction.attributes('data-count')).toBe('3');
    expect(likeAction.attributes('data-pending')).toBe('false');
    expect(mounted.find('.detail-inline-error').text()).toBe('Like failed. Please try again.');
  });
});

