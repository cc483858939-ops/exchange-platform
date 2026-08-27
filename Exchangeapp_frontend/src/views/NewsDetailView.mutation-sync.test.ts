// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import NewsDetailView from './NewsDetailView.vue';

const mocks = vi.hoisted(() => ({
  getArticleById: vi.fn(),
  getArticleLikeState: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  createArticleComment: vi.fn(),
  deleteComment: vi.fn(),
  getArticleComments: vi.fn(),
  getUser: vi.fn(),
  deleteArticle: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  },
  articleViewTelemetry: { enqueue: vi.fn() },
  router: { back: vi.fn(), push: vi.fn(), replace: vi.fn() },
  routeLeave: vi.fn(),
  authStore: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    currentIdentity: { id: 7, username: 'viewer' },
  },
  feedStore: {
    viewerID: 7,
    markArticleDeleted: vi.fn(),
  },
  externalLike: vi.fn(),
  externalRemoval: vi.fn(),
  externalCommentCount: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '42' }, query: {}, hash: '' }),
  useRouter: () => mocks.router,
  onBeforeRouteLeave: (guard: (to: { name?: string }) => void) => {
    mocks.routeLeave.mockImplementation(guard);
  },
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/feed', () => ({ useFeedStore: () => mocks.feedStore }));
vi.mock('../store/articleDetailHandoff', () => ({
  useArticleDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));
vi.mock('../store/sessionSync', () => ({
  syncExternalArticleLikeState: mocks.externalLike,
  syncExternalArticleRemoval: mocks.externalRemoval,
  syncExternalCommentCount: mocks.externalCommentCount,
}));
vi.mock('../services/articleService', () => ({
  getArticleById: mocks.getArticleById,
  deleteArticle: mocks.deleteArticle,
}));
vi.mock('../services/likeService', () => ({
  getArticleLikeState: mocks.getArticleLikeState,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));
vi.mock('../services/commentService', () => ({
  createArticleComment: mocks.createArticleComment,
  deleteComment: mocks.deleteComment,
  getArticleComments: mocks.getArticleComments,
}));
vi.mock('../services/userService', () => ({ getUser: mocks.getUser }));
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
  title: 'Article 42',
  content: 'Article body',
  preview: 'Article body',
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-15T00:00:00.000Z',
  expired_at: null,
  like_count: 3,
  comment_count: 2,
  view_count: 0,
  like_sync_version: 1,
  author: { id: 7, username: 'author', display_name: 'Author', avatar_url: '' },
};

const comment = (id: number) => ({
  id,
  article_id: 42,
  content: `Comment ${id}`,
  created_at: '2026-08-15T00:00:00.000Z',
  author: { id: 8, username: 'commenter', display_name: 'Commenter', avatar_url: '' },
});

const mountDetail = () => mount(NewsDetailView, {
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
      CommentComposer: {
        emits: ['submit'],
        methods: { clear: vi.fn() },
        template: '<button class="test-create-comment" type="button" @click="$emit(\'submit\', \'hello\')">Reply</button>',
      },
      CommentList: {
        props: ['comments'],
        emits: ['delete'],
        template: '<button class="test-delete-comment" type="button" @click="$emit(\'delete\', comments[0]?.id)">Delete reply</button>',
      },
    },
  },
});

describe('NewsDetailView mutation synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getArticleById.mockResolvedValue(article);
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getArticleComments.mockResolvedValue({ items: [comment(9)], next_cursor: null });
    mocks.getUser.mockResolvedValue({
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
      bio: '',
      created_at: '2026-08-15T00:00:00.000Z',
    });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.deleteArticle.mockResolvedValue(undefined);
    mocks.feedStore.markArticleDeleted.mockReturnValue(true);
  });

  it('syncs a successful Detail like but not a failed like', async () => {
    mocks.likeArticle.mockResolvedValueOnce({ liked: true, likes: 4 });
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).toHaveBeenCalledWith({
      articleId: 42,
      likes: 4,
      liked: true,
      status: 'ready',
    });

    mounted.unmount();
    mocks.externalLike.mockClear();
    mocks.likeArticle.mockRejectedValueOnce(new Error('offline'));
    const failed = mountDetail();
    await flushPromises();
    await failed.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).not.toHaveBeenCalled();
    failed.unmount();
  });

  it.each([
    ['success', undefined],
    ['terminal 404', { response: { status: 404 } }],
  ])('syncs Detail deletion before navigation on %s', async (_label, error) => {
    if (error) mocks.deleteArticle.mockRejectedValueOnce(error);
    window.confirm = vi.fn().mockReturnValue(true);
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.post-detail__delete').trigger('click');
    await flushPromises();

    expect(mocks.feedStore.markArticleDeleted).toHaveBeenCalledWith(42, 7);
    expect(mocks.externalRemoval).toHaveBeenCalledWith(42);
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'UserProfile',
      params: { id: '7' },
    });
    expect(mocks.feedStore.markArticleDeleted.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.externalRemoval.mock.invocationCallOrder[0]);
    expect(mocks.externalRemoval.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.router.replace.mock.invocationCallOrder[0]);
    mounted.unmount();
  });

  it('syncs absolute comment counts after create and delete success', async () => {
    mocks.createArticleComment.mockResolvedValueOnce(comment(10));
    mocks.deleteComment.mockResolvedValueOnce(undefined);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();
    expect(mocks.externalCommentCount).toHaveBeenNthCalledWith(1, {
      articleId: 42,
      commentCount: 3,
    });

    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();
    expect(mocks.externalCommentCount).toHaveBeenNthCalledWith(2, {
      articleId: 42,
      commentCount: 2,
    });
  });

  it('does not synchronize failed comment mutations', async () => {
    mocks.createArticleComment.mockRejectedValueOnce(new Error('offline'));
    mocks.deleteComment.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();
    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();

    expect(mocks.externalCommentCount).not.toHaveBeenCalled();
  });
});
