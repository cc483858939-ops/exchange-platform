// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import NewsDetailView from './NewsDetailView.vue';
import type { PublicUser } from '../types/User';

const mocks = vi.hoisted(() => ({
  authState: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    currentIdentity: { id: 7, username: 'alice' } as { id: number; username: string } | null,
  },
  authStore: null as {
    isAuthenticated: boolean;
    token: string;
    currentIdentity: { id: number; username: string } | null;
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

const profile = ({
  id = 7,
  username = 'alice',
  displayName = 'Alice Smith',
  avatarURL = 'https://example.test/alice.jpg',
}: {
  id?: number;
  username?: string;
  displayName?: string;
  avatarURL?: string;
} = {}): PublicUser => ({
  id,
  username,
  display_name: displayName,
  avatar_url: avatarURL,
  bio: '',
  created_at: '2026-08-21T00:00:00.000Z',
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
    mocks.authStore!.currentIdentity = { id: 7, username: 'alice' };
    mocks.getArticleById.mockResolvedValue(article);
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getArticleComments.mockResolvedValue({ items: [], next_cursor: null });
    mocks.createArticleComment.mockResolvedValue({ id: 101 });
    mocks.getUser.mockResolvedValue(profile({ avatarURL: '' }));
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('renders the composer with an immediate initial while the profile is pending', async () => {
    const request = deferred<PublicUser>();
    mocks.getUser.mockReturnValue(request.promise);

    wrapper = mountDetail();
    await flushPromises();

    expect(mocks.getUser).toHaveBeenCalledOnce();
    expect(mocks.getUser).toHaveBeenCalledWith(7);
    expect(wrapper.get('.comment-composer__avatar').text()).toBe('A');
    const textarea = wrapper.get('.comment-composer__textarea');
    await textarea.setValue('reply while profile is pending');
    expect(textarea.element).not.toHaveProperty('disabled', true);

    request.resolve(profile({ avatarURL: '' }));
    await flushPromises();
  });

  it('replaces the fallback with the current profile avatar', async () => {
    mocks.getUser.mockResolvedValue(profile({ avatarURL: 'https://example.test/alice.jpg' }));

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.get('.comment-composer__avatar img').attributes('src'))
      .toBe('https://example.test/alice.jpg');
  });

  it('keeps the fallback and reply usable when profile loading fails', async () => {
    mocks.getUser.mockRejectedValue(new Error('profile unavailable'));

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.get('.comment-composer__avatar').text()).toBe('A');
    expect(wrapper.find('.comment-composer__avatar img').exists()).toBe(false);

    await wrapper.get('.comment-composer__textarea').setValue('reply without enrichment');
    await wrapper.get('.comment-composer').trigger('submit');
    await flushPromises();

    expect(mocks.createArticleComment).toHaveBeenCalledWith('42', 'reply without enrichment');
    expect(wrapper.find('.comment-error').exists()).toBe(false);
  });

  it('does not request a profile when authentication is false', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountDetail();
    await flushPromises();

    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(wrapper.find('.comment-composer').exists()).toBe(false);
    expect(wrapper.get('.detail-state__link').text()).toContain('Log in');
  });

  it('does not request a profile when authentication is false and identity is retained', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = { id: 7, username: 'alice' };

    wrapper = mountDetail();
    await flushPromises();

    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(wrapper.find('.comment-composer').exists()).toBe(false);
    expect(wrapper.find('.detail-state__link').exists()).toBe(true);
  });

  it('ignores a profile whose returned ID does not match the viewer', async () => {
    mocks.getUser.mockResolvedValue(profile({
      id: 99,
      username: 'wrong-user',
      displayName: 'Wrong User',
      avatarURL: 'https://example.test/wrong.jpg',
    }));

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.get('.comment-composer__avatar').text()).toBe('A');
    expect(wrapper.find('.comment-composer__avatar img').exists()).toBe(false);
    expect(wrapper.html()).not.toContain('wrong.jpg');
  });

  it('keeps the newest account after an out-of-order profile response', async () => {
    const accountA = deferred<PublicUser>();
    const accountB = deferred<PublicUser>();
    mocks.getUser.mockImplementation((userID: number) => (
      userID === 7 ? accountA.promise : accountB.promise
    ));

    wrapper = mountDetail();
    expect(mocks.getUser).toHaveBeenCalledWith(7);

    mocks.authStore!.currentIdentity = { id: 8, username: 'bob' };
    await nextTick();

    expect(mocks.getUser).toHaveBeenCalledTimes(2);
    expect(mocks.getUser).toHaveBeenLastCalledWith(8);

    accountB.resolve(profile({
      id: 8,
      username: 'bob',
      displayName: 'Bob Jones',
      avatarURL: 'https://example.test/bob.jpg',
    }));
    await flushPromises();

    expect(wrapper.get('.comment-composer__avatar img').attributes('src'))
      .toBe('https://example.test/bob.jpg');

    accountA.resolve(profile({ avatarURL: 'https://example.test/alice.jpg' }));
    await flushPromises();

    expect(wrapper.get('.comment-composer__avatar img').attributes('src'))
      .toBe('https://example.test/bob.jpg');
    expect(wrapper.html()).not.toContain('alice.jpg');
  });

  it('ignores a profile response that arrives after logout', async () => {
    const request = deferred<PublicUser>();
    mocks.getUser.mockReturnValue(request.promise);

    wrapper = mountDetail();
    await flushPromises();
    expect(mocks.getUser).toHaveBeenCalledWith(7);

    mocks.authStore!.isAuthenticated = false;
    await nextTick();

    expect(mocks.getUser).toHaveBeenCalledOnce();
    expect(wrapper.find('.comment-composer').exists()).toBe(false);

    request.resolve(profile({ avatarURL: 'https://example.test/alice-after-logout.jpg' }));
    await flushPromises();

    expect(wrapper.find('.comment-composer').exists()).toBe(false);
    expect(wrapper.html()).not.toContain('alice-after-logout.jpg');
  });
});
