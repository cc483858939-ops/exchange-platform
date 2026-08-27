// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';
import NewsDetailView from './NewsDetailView.vue';

const mocks = vi.hoisted(() => ({
  getArticleById: vi.fn(),
  getArticleLikeState: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  getArticleComments: vi.fn(),
  getUser: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  },
  articleViewTelemetry: {
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
    markArticleDeleted: vi.fn(),
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

vi.mock('../store/articleDetailHandoff', () => ({
  useArticleDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: vi.fn(),
  getArticleById: mocks.getArticleById,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeState: mocks.getArticleLikeState,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

vi.mock('../services/commentService', () => ({
  createArticleComment: vi.fn(),
  deleteComment: vi.fn(),
  getArticleComments: mocks.getArticleComments,
}));

vi.mock('../services/recommendationAttribution', () => ({
  consumePendingRecommendationAttribution: mocks.consumeAttribution,
}));

vi.mock('../services/recommendationTelemetry', () => ({
  getRecommendationTelemetry: () => mocks.telemetry,
}));

vi.mock('../services/articleViewTelemetry', () => ({
  createArticleViewEventID: () => '00000000-0000-4000-8000-000000000042',
  getArticleViewTelemetry: () => mocks.articleViewTelemetry,
}));

const article = {
  ID: 42,
  CreatedAt: '2026-08-15T00:00:00.000Z',
  UpdatedAt: '2026-08-15T00:00:00.000Z',
  title: 'Tracked article',
  content: 'Article body',
  preview: 'Article body',
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-15T00:00:00.000Z',
  expired_at: null,
  like_count: 3,
  comment_count: 0,
  view_count: 1234,
  like_sync_version: 1,
  author: {
    id: 7,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
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

describe('NewsDetailView LikeAction wiring', () => {
  let mounted: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.routeLeave.mockReset();
    mocks.getArticleById.mockResolvedValue(article);
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getArticleComments.mockResolvedValue({ items: [], next_cursor: null });
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

  const mountDetail = () => mount(NewsDetailView, {
    attachTo: document.body,
    global: {
      plugins: [createPinia()],
      stubs: {
        AppIcon: { template: '<span />' },
        AuthorIdentity: { template: '<span />' },
        CommentComposer: { template: '<div />' },
        CommentList: { template: '<div />' },
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
    mocks.likeArticle.mockReturnValueOnce(request.promise);

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
    expect(mocks.likeArticle).toHaveBeenCalledWith('42');

    request.resolve({ liked: true, likes: 4 });
    await flushPromises();

    expect(likeAction.attributes('data-liked')).toBe('true');
    expect(likeAction.attributes('data-count')).toBe('4');
    expect(likeAction.attributes('data-pending')).toBe('false');
  });

  it('rolls back optimistic detail like state on failure without changing the wiring path', async () => {
    const request = deferred<LikeResult>();
    mocks.likeArticle.mockReturnValueOnce(request.promise);

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
