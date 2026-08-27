// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import NewsDetailView from './NewsDetailView.vue';
import type { Article } from '../types/Article';
import type { FeedPost } from '../types/Feed';
import { formatCompactEngagementCount } from '../utils/engagementCount';

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
    markArticleDeleted: vi.fn(),
  },
  handoffStore: null as any,
  consumeHandoff: vi.fn(),
  getArticleById: vi.fn(),
  getArticleLikeState: vi.fn(),
  getArticleRepostState: vi.fn().mockResolvedValue({ reposts: 0, reposted: false }),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  getArticleComments: vi.fn(),
  createArticleComment: vi.fn(),
  deleteComment: vi.fn(),
  deleteArticle: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn(),
  },
  articleViewTelemetry: {
    enqueue: vi.fn(),
  },
  composerFocus: vi.fn(),
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

vi.mock('../store/articleDetailHandoff', () => ({
  useArticleDetailHandoffStore: () => mocks.handoffStore,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: mocks.deleteArticle,
  getArticleById: mocks.getArticleById,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeState: mocks.getArticleLikeState,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

vi.mock('../services/repostService', () => ({
  getArticleRepostState: mocks.getArticleRepostState,
  repostArticle: vi.fn(),
  undoRepostArticle: vi.fn(),
}));

vi.mock('../services/commentService', () => ({
  createArticleComment: mocks.createArticleComment,
  deleteComment: mocks.deleteComment,
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

vi.mock('../store/sessionSync', () => ({
  syncExternalArticleLikeState: vi.fn(),
  syncExternalArticleRepostState: vi.fn(),
  syncExternalArticleRemoval: vi.fn(),
  syncExternalCommentCount: vi.fn(),
}));

const article = (overrides: Partial<Article> = {}): Article => ({
  ID: 42,
  CreatedAt: '2026-08-27T13:42:00',
  UpdatedAt: '2026-08-27T13:42:00',
  title: 'A thoughtful post',
  content: 'Full post body',
  preview: 'Warm post preview',
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-27T13:42:00',
  expired_at: '2026-09-01T00:00:00',
  like_count: 11,
  comment_count: 4,
  view_count: 1234,
  like_sync_version: 1,
  author: {
    id: 7,
    username: 'post-author',
    display_name: 'Post Author',
    avatar_url: '/post-author.png',
  },
  ...overrides,
});

const post = (overrides: Partial<FeedPost> = {}): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'warm-author',
    display_name: 'Warm Author',
    avatar_url: '/warm-author.png',
  },
  title: 'Warm post',
  excerpt: 'Warm post preview',
  coverImageUrl: '',
  createdAt: '2026-08-27T13:42:00',
  likeCount: 10,
  commentCount: 3,
  viewCount: 300,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
  ...overrides,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const mountDetail = () => mount(NewsDetailView, {
  attachTo: document.body,
  global: {
    plugins: [createPinia()],
    stubs: {
      AppIcon: {
        props: ['name'],
        template: '<span class="test-icon" :data-icon="name" />',
      },
      AuthorIdentity: {
        props: ['author', 'createdAt', 'variant'],
        template: '<div class="test-author" :data-variant="variant" :data-created-at="createdAt">{{ author.display_name }}</div>',
      },
      LikeAction: {
        props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'variant'],
        emits: ['toggle'],
        template: '<button class="test-like-action" type="button">{{ count }}</button>',
      },
      CommentComposer: {
        methods: {
          focus: mocks.composerFocus,
          clear: vi.fn(),
        },
        template: '<div class="test-composer" />',
      },
      CommentList: { template: '<div class="test-comment-list" />' },
      RouterLink: { template: '<a class="test-link"><slot /></a>' },
    },
  },
});

describe('NewsDetailView post-first surface', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({
      params: { id: '42' },
      query: {},
      hash: '',
    });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer test-token',
      currentIdentity: {
        id: 7,
        username: 'viewer',
        display_name: 'Viewer',
        avatar_url: '/viewer.png',
      },
    });
    mocks.handoffStore = { consume: mocks.consumeHandoff };
    mocks.consumeHandoff.mockReturnValue(null);
    mocks.getArticleById.mockResolvedValue(article());
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 11 });
    mocks.getArticleComments.mockResolvedValue({ items: [], next_cursor: null });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.composerFocus.mockResolvedValue(true);
    mocks.telemetry.flush.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('renders one Post heading and a continuous conversation surface', async () => {
    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-header__title').text()).toBe('Post');
    expect(wrapper.find('.detail-header__back').text()).toBe('');
    expect(wrapper.findAll('h1')).toHaveLength(1);
    expect(wrapper.find('.post-detail').exists()).toBe(true);
    expect(wrapper.find('.post-detail__headline').element.tagName).toBe('P');
    expect(wrapper.find('.test-author').attributes('data-variant')).toBe('post');
    expect(wrapper.find('.test-author').attributes('data-created-at')).toBeUndefined();
    expect(wrapper.find('.post-detail__views').text())
      .toBe(`${formatCompactEngagementCount(1234)} Views`);
    expect(wrapper.findAll('.post-detail__views')).toHaveLength(1);
    expect(wrapper.find('.post-detail__engagement [data-icon="analytics"]').exists()).toBe(false);
    expect(wrapper.find('.post-detail__meta .post-detail__delete').exists()).toBe(false);
    expect(wrapper.find('.post-detail__delete').exists()).toBe(true);
    expect(wrapper.find('.post-conversation').attributes('aria-label')).toBe('Conversation');
    expect(wrapper.find('h2').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Article');
    expect(wrapper.text()).not.toContain('Expired');
    expect(wrapper.text()).not.toContain('Expires');
    expect(wrapper.text()).not.toContain('Keep it useful.');
  });

  it('keeps Reply non-interactive while warm and replaces cached views on success', async () => {
    const request = deferred<Article>();
    mocks.consumeHandoff.mockReturnValueOnce(post());
    mocks.getArticleById.mockReturnValueOnce(request.promise);

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-detail__views').text()).toBe('300 Views');
    expect(wrapper.find('.post-detail__reply').element.tagName).toBe('SPAN');
    expect(wrapper.find('.post-detail__like').exists()).toBe(true);
    expect(wrapper.find('.post-conversation').exists()).toBe(false);
    expect(wrapper.find('.test-composer').exists()).toBe(false);
    expect(mocks.getArticleLikeState).not.toHaveBeenCalled();
    expect(mocks.getArticleComments).not.toHaveBeenCalled();

    request.resolve(article({ view_count: 321 }));
    await flushPromises();

    expect(wrapper.find('.post-detail__views').text()).toBe('321 Views');
    expect(wrapper.find('.post-detail__reply').element.tagName).toBe('BUTTON');
    expect(wrapper.find('.post-conversation').exists()).toBe(true);
  });

  it('focuses the existing composer from the authoritative Reply action', async () => {
    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-detail__reply').element.tagName).toBe('BUTTON');
    await wrapper.get('.post-detail__reply').trigger('click');
    await flushPromises();

    expect(mocks.composerFocus).toHaveBeenCalledTimes(1);
  });

  it('uses Post vocabulary for an invalid post URL', async () => {
    mocks.route.params.id = 'not-an-id';

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('Post unavailable');
    expect(wrapper.find('.detail-state--error').text()).toContain('This post URL is not valid.');
    expect(wrapper.text()).not.toContain('Article');
    expect(mocks.getArticleById).not.toHaveBeenCalled();
  });

  it('uses Post vocabulary for a missing post and generic load failures', async () => {
    mocks.getArticleById.mockRejectedValueOnce({ response: { status: 404 } });

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('This post does not exist.');
    expect(wrapper.text()).not.toContain('This article does not exist.');
    expect(wrapper.text()).not.toContain('Article unavailable');

    wrapper.unmount();
    wrapper = null;
    mocks.getArticleById.mockRejectedValueOnce(new Error('offline'));
    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('The post could not be loaded.');
  });

  it('keeps the unauthenticated Post error surface without mounting conversation UI', async () => {
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('Log in to view this post');
    expect(wrapper.find('.detail-state--error').text())
      .toContain('Sign in to open this post and join the conversation.');
    expect(wrapper.find('.post-detail').exists()).toBe(false);
    expect(wrapper.find('.test-composer').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Log in to join the conversation.');
    expect(mocks.getArticleById).not.toHaveBeenCalled();
  });
});
