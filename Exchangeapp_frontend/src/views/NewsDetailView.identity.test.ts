// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import NewsDetailView from './NewsDetailView.vue';
import CommentComposer from '../components/comments/CommentComposer.vue';

const mocks = vi.hoisted(() => ({
  authState: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    currentIdentity: {
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: 'https://example.test/alice.jpg',
    } as {
      id: number;
      username: string;
      display_name: string;
      avatar_url: string;
    } | null,
  },
  authStore: null as {
    isAuthenticated: boolean;
    token: string;
    currentIdentity: {
      id: number;
      username: string;
      display_name: string;
      avatar_url: string;
    } | null;
  } | null,
  getArticleById: vi.fn(),
  getArticleLikeState: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  getArticleComments: vi.fn(),
  createArticleComment: vi.fn(),
  deleteComment: vi.fn(),
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

vi.mock('../store/auth', async () => {
  const { reactive } = await import('vue');
  mocks.authStore = reactive(mocks.authState);
  return {
    useAuthStore: () => mocks.authStore,
  };
});

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: vi.fn(),
  getArticleById: mocks.getArticleById,
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

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
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
  title: 'Identity article',
  content: 'Article body',
  preview: 'Article body',
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-15T00:00:00.000Z',
  expired_at: null,
  like_count: 3,
  comment_count: 0,
  view_count: 12,
  like_sync_version: 1,
  author: {
    id: 99,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
};

const identity = (overrides: Partial<NonNullable<typeof mocks.authState.currentIdentity>> = {}) => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice Smith',
  avatar_url: 'https://example.test/alice.jpg',
  ...overrides,
});

const mountDetail = () => mount(NewsDetailView, {
  attachTo: document.body,
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      AuthorIdentity: { template: '<span />' },
      CommentList: { template: '<div />' },
      LikeAction: { template: '<button type="button" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('NewsDetailView reply composer identity', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.routeLeave.mockReset();
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.token = 'Bearer test-token';
    mocks.authStore!.currentIdentity = identity();
    mocks.getArticleById.mockResolvedValue(article);
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getArticleComments.mockResolvedValue({ items: [], next_cursor: null });
    mocks.createArticleComment.mockResolvedValue({ id: 101 });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('passes the canonical current identity immediately without a profile request', async () => {
    wrapper = mountDetail();
    await flushPromises();

    const composer = wrapper.findComponent(CommentComposer);
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(composer.props('author')).toEqual({
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: 'https://example.test/alice.jpg',
    });
    expect(wrapper.get('.comment-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
  });

  it('updates the reply identity when the authenticated viewer changes', async () => {
    wrapper = mountDetail();
    await flushPromises();

    mocks.authStore!.currentIdentity = identity({
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: 'https://example.test/bob.jpg',
    });
    await nextTick();

    expect(wrapper.findComponent(CommentComposer).props('author')).toEqual({
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: 'https://example.test/bob.jpg',
    });
    expect(wrapper.get('.comment-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/bob.jpg');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });

  it('keeps replying usable from the canonical identity without enrichment', async () => {
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.comment-composer__textarea').setValue('reply without enrichment');
    await wrapper.get('.comment-composer').trigger('submit');
    await flushPromises();

    expect(mocks.createArticleComment).toHaveBeenCalledWith('42', 'reply without enrichment');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });

  it('does not render a composer or request a profile while logged out', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.comment-composer').exists()).toBe(false);
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(wrapper.get('.detail-state__link').text()).toContain('Log in');
  });
});
