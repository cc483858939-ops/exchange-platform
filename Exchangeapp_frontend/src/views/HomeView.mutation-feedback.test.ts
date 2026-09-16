// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive, nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ElMessage } from 'element-plus';
import type { FeedPost, FeedTab } from '../types/Feed';

vi.mock('element-plus/es/components/message/style/css', () => ({}));

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  feedStore: null as any,
  homeTimeline: null as any,
  route: null as any,
  router: null as any,
  telemetry: {
    resetObservedCards: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
    observeFeedCard: vi.fn(),
    detachFeedCard: vi.fn(),
    unobserveFeedCard: vi.fn(),
    recordClick: vi.fn(),
    recordNotInterested: vi.fn(),
    notifyViewportChange: vi.fn(),
  },
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

vi.mock('../services/recommendationTelemetry', () => ({
  getRecommendationTelemetry: () => mocks.telemetry,
}));

vi.mock('../services/recommendationAttribution', () => ({
  savePendingRecommendationAttribution: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
  onBeforeRouteLeave: vi.fn(),
}));

const post: FeedPost = {
  id: 1,
  author: {
    id: 7,
    username: 'viewer',
    display_name: 'Viewer',
    avatar_url: '',
  },
  content: 'Post 1',
  language: 'und',
  media: [],
  createdAt: '2026-08-24T00:00:00.000Z',
  likeCount: 0,
  replyCount: 0,
  viewCount: 0,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
  bookmarked: false,
  bookmarkStatus: 'ready',
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const mountHomeView = async () => {
  const module = await import('./HomeView.vue');
  const wrapper = mount(module.default, {
    global: {
      stubs: {
        FeedTabs: { template: '<div />' },
        PostCard: {
          props: ['post'],
          template: `
            <div class="post-card-stub">
              <button class="test-like" type="button" @click="$emit('toggle-like', post.id)">Like</button>
              <button class="test-repost" type="button" @click="$emit('toggle-repost', post.id)">Repost</button>
            </div>
          `,
        },
        AppIcon: { template: '<span />' },
        MobileHomeHeader: { template: '<div />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  await settle();
  return wrapper;
};

describe('HomeView engagement mutation feedback', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7 },
      token: 'Bearer token',
    });
    mocks.feedStore = reactive({
      recentlyPublishedPosts: [],
      isPostDeleted: vi.fn().mockReturnValue(false),
    });
    mocks.homeTimeline = reactive({
      activeTab: 'for-you' as FeedTab,
      homeReselectVersion: 0,
      forYou: reactive({
        items: [{ recommendation: { post: { id: post.id }, score: 1 }, post }],
        loading: false,
        error: false,
        loaded: true,
        loadingMore: false,
        loadMoreError: false,
        depleted: true,
      }),
      following: reactive({
        items: [],
        loading: false,
        error: false,
        loaded: true,
        nextCursor: null,
        loadingMore: false,
        loadMoreError: false,
        stale: false,
        revalidating: false,
        revalidateError: false,
      }),
      scrollTop: { 'for-you': 0, following: 0 },
      likePendingPostIds: new Set<number>(),
      repostPendingPostIds: new Set<number>(),
      bookmarkPendingPostIds: new Set<number>(),
      pendingDeletePostIds: new Set<number>(),
      deleteErrors: new Map<number, string>(),
      setActiveTab: vi.fn((tab: FeedTab) => { mocks.homeTimeline.activeTab = tab; }),
      setScrollTop: vi.fn(),
      requestHomeReselect: vi.fn(),
      loadForYou: vi.fn().mockResolvedValue(undefined),
      loadMoreForYou: vi.fn().mockResolvedValue(undefined),
      retryForYouLoadMore: vi.fn(),
      loadFollowing: vi.fn().mockResolvedValue(undefined),
      loadMoreFollowing: vi.fn().mockResolvedValue(undefined),
      revalidateFollowing: vi.fn().mockResolvedValue(undefined),
      retryFollowingLoadMore: vi.fn(),
      toggleLike: vi.fn().mockResolvedValue('succeeded'),
      toggleRepost: vi.fn().mockResolvedValue('succeeded'),
      deletePost: vi.fn().mockResolvedValue(true),
      dismissRecommendation: vi.fn(),
    });
    mocks.route = reactive({ name: 'Home', query: {} });
    mocks.router = {
      push: vi.fn().mockResolvedValue(undefined),
      replace: vi.fn().mockResolvedValue(undefined),
    };
    Object.values(mocks.telemetry).forEach((mock) => mock.mockClear());
    vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: vi.fn() }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows one explicit error when Like fails', async () => {
    mocks.homeTimeline.toggleLike.mockResolvedValueOnce('failed');
    const wrapper = await mountHomeView();

    await wrapper.get('.test-like').trigger('click');
    await settle();

    expect(ElMessage.error).toHaveBeenCalledTimes(1);
    expect(ElMessage.error).toHaveBeenCalledWith('Couldn’t update your like. Try again.');
    wrapper.unmount();
  });

  it('does not show an error for successful or ignored Like mutations', async () => {
    mocks.homeTimeline.toggleLike
      .mockResolvedValueOnce('succeeded')
      .mockResolvedValueOnce('ignored');
    const wrapper = await mountHomeView();

    await wrapper.get('.test-like').trigger('click');
    await settle();
    await wrapper.get('.test-like').trigger('click');
    await settle();

    expect(ElMessage.error).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('shows one explicit error when Repost fails', async () => {
    mocks.homeTimeline.toggleRepost.mockResolvedValueOnce('failed');
    const wrapper = await mountHomeView();

    await wrapper.get('.test-repost').trigger('click');
    await settle();

    expect(ElMessage.error).toHaveBeenCalledTimes(1);
    expect(ElMessage.error).toHaveBeenCalledWith('Couldn’t update your repost. Try again.');
    wrapper.unmount();
  });

  it('does not show an error for successful or ignored Repost mutations', async () => {
    mocks.homeTimeline.toggleRepost
      .mockResolvedValueOnce('succeeded')
      .mockResolvedValueOnce('ignored');
    const wrapper = await mountHomeView();

    await wrapper.get('.test-repost').trigger('click');
    await settle();
    await wrapper.get('.test-repost').trigger('click');
    await settle();

    expect(ElMessage.error).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});
