// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia } from 'pinia';
import { ArticleReadTracker } from '../services/articleReadTracker';
import NewsDetailView from './NewsDetailView.vue';
import { formatCompactEngagementCount } from '../utils/engagementCount';

const mocks = vi.hoisted(() => ({
  getArticleById: vi.fn(),
  getArticleLikeState: vi.fn(),
  getArticleRepostState: vi.fn().mockResolvedValue({ reposts: 0, reposted: false }),
  getArticleComments: vi.fn(),
  getUser: vi.fn(),
  consumeAttribution: vi.fn(),
  assertBodyAtConsume: vi.fn(),
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

vi.mock('../store/articleDetailHandoff', () => ({
  useArticleDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
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
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
}));

vi.mock('../services/repostService', () => ({
  getArticleRepostState: mocks.getArticleRepostState,
  repostArticle: vi.fn(),
  undoRepostArticle: vi.fn(),
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

const tracking = {
  request_id: 'request-42',
  position: 1,
  scene: 'recommendation_page',
  ranker_version: 'rules_v3',
  ranker_config_hash: 'config-hash',
  strategy_id: 'cold_start_rules_v3',
  token: 'v2.token.signature',
  expires_at: '2099-08-15T00:00:00.000Z',
};

describe('NewsDetailView attributed read lifecycle', () => {
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
    mocks.consumeAttribution.mockImplementation(() => {
      mocks.assertBodyAtConsume(document.querySelector('.post-detail__body'));
      return tracking;
    });
  });

  afterEach(() => {
    mounted?.unmount();
    mounted = null;
    vi.restoreAllMocks();
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
        LikeAction: { template: '<button class="test-like-action" type="button" />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });

  it('does not enqueue a View when the article request fails', async () => {
    mocks.getArticleById.mockRejectedValueOnce(new Error('offline'));

    mounted = mountDetail();
    await flushPromises();

    expect(mocks.articleViewTelemetry.enqueue).not.toHaveBeenCalled();
  });

  it('does not enqueue a View when the response ID mismatches the route', async () => {
    mocks.getArticleById.mockResolvedValueOnce({ ...article, ID: 43 });

    mounted = mountDetail();
    await flushPromises();

    expect(mocks.articleViewTelemetry.enqueue).not.toHaveBeenCalled();
  });

  it('renders the server View count without optimistic increment and keeps one lifecycle event', async () => {
    const trackerStart = vi.spyOn(ArticleReadTracker.prototype, 'start');

    mounted = mountDetail();
    await flushPromises();

    const viewMetric = mounted.find('.post-detail__views');
    expect(viewMetric.attributes('aria-label')).toBe('1,234 views');
    expect(viewMetric.text()).toBe(`${formatCompactEngagementCount(1234)} Views`);
    expect(mounted.find('.detail-state').exists()).toBe(false);
    expect(mounted.find('.post-detail__body').exists()).toBe(true);
    expect(mocks.assertBodyAtConsume).toHaveBeenCalledWith(expect.any(HTMLElement));
    expect(trackerStart).toHaveBeenCalledTimes(1);
    expect(mocks.articleViewTelemetry.enqueue).toHaveBeenCalledTimes(1);
    expect(mocks.articleViewTelemetry.enqueue).toHaveBeenCalledWith(42, expect.any(String), 'article_detail');

    await flushPromises();
    expect(mocks.articleViewTelemetry.enqueue).toHaveBeenCalledTimes(1);
    expect(viewMetric.attributes('aria-label')).toBe('1,234 views');
    expect(viewMetric.text()).toBe(`${formatCompactEngagementCount(1234)} Views`);

    mocks.routeLeave({ name: 'Home' });
    mounted.unmount();

    expect(mocks.telemetry.recordReadEnd).toHaveBeenCalledTimes(1);
    expect(mocks.telemetry.recordReadEnd).toHaveBeenCalledWith(
      42,
      tracking,
      expect.objectContaining({ exit_type: 'route_leave' }),
    );
  });
});

