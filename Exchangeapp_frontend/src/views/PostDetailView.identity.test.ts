// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import PostDetailView from './PostDetailView.vue';
import ReplyComposer from '../components/replies/ReplyComposer.vue';

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
  getPostById: vi.fn(),
  getPostLikeState: vi.fn(),
  getPostRepostState: vi.fn().mockResolvedValue({ reposts: 0, reposted: false }),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostReplies: vi.fn(),
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
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

vi.mock('../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));

vi.mock('../services/postService', () => ({
  deletePost: vi.fn(),
  getPostById: mocks.getPostById,
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
  createPostReply: mocks.createPostReply,
  deletePostReply: mocks.deletePostReply,
  getPostReplies: mocks.getPostReplies,
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
    id: 99,
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
  article: {
    title: 'Identity post',
    preview: 'Post body',
    cover_image_url: '',
    publication_state: 'published',
    published_at: '2026-08-15T00:00:00.000Z',
    expired_at: null,
  },
  like_count: 3,
  reply_count: 0,
  view_count: 12,
  deleted: false,
};

const identity = (overrides: Partial<NonNullable<typeof mocks.authState.currentIdentity>> = {}) => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice Smith',
  avatar_url: 'https://example.test/alice.jpg',
  ...overrides,
});

const mountDetail = () => mount(PostDetailView, {
  attachTo: document.body,
  global: {
    plugins: [createPinia()],
    stubs: {
      AppIcon: { template: '<span />' },
      AuthorIdentity: { template: '<span />' },
      ReplyList: { template: '<div />' },
      LikeAction: { template: '<button type="button" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('PostDetailView reply composer identity', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.routeLeave.mockReset();
    mocks.authStore!.isAuthenticated = true;
    mocks.authStore!.token = 'Bearer test-token';
    mocks.authStore!.currentIdentity = identity();
    mocks.getPostById.mockResolvedValue(post);
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
    mocks.createPostReply.mockResolvedValue({ id: 101 });
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

    const composer = wrapper.findComponent(ReplyComposer);
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(composer.props('author')).toEqual({
      id: 7,
      username: 'alice',
      display_name: 'Alice Smith',
      avatar_url: 'https://example.test/alice.jpg',
    });
    expect(wrapper.get('.reply-composer__avatar .user-avatar__image').attributes('src'))
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

    expect(wrapper.findComponent(ReplyComposer).props('author')).toEqual({
      id: 8,
      username: 'bob',
      display_name: 'Bob Jones',
      avatar_url: 'https://example.test/bob.jpg',
    });
    expect(wrapper.get('.reply-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/bob.jpg');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });

  it('keeps replying usable from the canonical identity without enrichment', async () => {
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('reply without enrichment');
    await wrapper.get('.reply-composer').trigger('submit');
    await flushPromises();

    expect(mocks.createPostReply).toHaveBeenCalledWith('42', 'reply without enrichment');
    expect(mocks.getUser).not.toHaveBeenCalled();
  });

  it('does not render a composer or request a profile while logged out', async () => {
    mocks.authStore!.isAuthenticated = false;
    mocks.authStore!.currentIdentity = null;

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.reply-composer').exists()).toBe(false);
    expect(mocks.getUser).not.toHaveBeenCalled();
    expect(wrapper.get('.detail-state__link').text()).toContain('Log in');
  });
});

