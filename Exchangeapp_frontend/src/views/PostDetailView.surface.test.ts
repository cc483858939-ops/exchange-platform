// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import PostDetailView from './PostDetailView.vue';
import type { Post } from '../types/Post';
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
    markPostDeleted: vi.fn(),
  },
  handoffStore: null as any,
  consumeHandoff: vi.fn(),
  getPostById: vi.fn(),
  getPostLikeState: vi.fn(),
  getPostRepostState: vi.fn().mockResolvedValue({ reposts: 0, reposted: false }),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostReplies: vi.fn(),
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
  deletePost: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn(),
  },
  postViewTelemetry: {
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
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
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

vi.mock('../store/sessionSync', () => ({
  syncExternalPostLikeState: vi.fn(),
  syncExternalPostRepostState: vi.fn(),
  syncExternalPostRemoval: vi.fn(),
  syncExternalReplyCount: vi.fn(),
}));

const canonicalPost = (overrides: Partial<Post> = {}): Post => ({
  id: 42,
  created_at: '2026-08-27T13:42:00',
  updated_at: '2026-08-27T13:42:00',
  published_at: '2026-08-27T13:42:00',
  author: {
    id: 7,
    username: 'post-author',
    display_name: 'Post Author',
    avatar_url: '/post-author.png',
  },
  content: 'Full post body',
  conversation_id: 42,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 11,
  reply_count: 4,
  view_count: 1234,
  deleted: false,
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
  content: 'Warm post preview',
  media: [],
  createdAt: '2026-08-27T13:42:00',
  likeCount: 10,
  replyCount: 3,
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

const mountDetail = () => mount(PostDetailView, {
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
      ReplyComposer: {
        methods: {
          focus: mocks.composerFocus,
          clear: vi.fn(),
        },
        template: '<div class="test-composer" />',
      },
      ReplyList: { template: '<div class="test-comment-list" />' },
      RouterLink: { template: '<a class="test-link"><slot /></a>' },
    },
  },
});

describe('PostDetailView post-first surface', () => {
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
    mocks.getPostById.mockResolvedValue(canonicalPost());
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 11 });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
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
    expect(wrapper.find('.post-detail__body').text()).toContain('Full post body');
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

  it('renders primary and active-reference media with the shared grid', async () => {
    const reference = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [{ type: 'image' as const, url: '/reference.png', position: 0 }],
    };
    mocks.getPostById.mockResolvedValueOnce(canonicalPost({
      media: [{ type: 'image', url: '/primary.png', position: 0 }],
      quote_post_id: 9,
      quote_post: reference,
    }));

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.findAll('.post-media-grid')).toHaveLength(2);
    expect(wrapper.find('.post-detail__body img').attributes('src')).toBe('/primary.png');
    expect(wrapper.find('.post-detail__reference img').attributes('src')).toBe('/reference.png');
  });

  it('linkifies the authoritative post body without changing ordinary text', async () => {
    mocks.getPostById.mockResolvedValueOnce(canonicalPost({
      content: 'Visit https://example.com today',
    }));

    wrapper = mountDetail();
    await flushPromises();

    const body = wrapper.get('.post-detail__body');
    const external = body.get('a.linkified-text__external');

    expect(body.text()).toBe('Visit https://example.com today');
    expect(external.attributes('href')).toBe('https://example.com');
    expect(external.attributes('target')).toBe('_blank');
  });

  it('keeps Reply non-interactive while warm and replaces cached views on success', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post());
    mocks.getPostById.mockReturnValueOnce(request.promise);

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-detail__views').text()).toBe('300 Views');
    expect(wrapper.find('.post-detail__reply').element.tagName).toBe('SPAN');
    expect(wrapper.find('.post-detail__like').exists()).toBe(true);
    expect(wrapper.find('.post-conversation').exists()).toBe(false);
    expect(wrapper.find('.test-composer').exists()).toBe(false);
    expect(mocks.getPostLikeState).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();

    request.resolve(canonicalPost({ view_count: 321 }));
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
    expect(mocks.getPostById).not.toHaveBeenCalled();
  });

  it('uses Post vocabulary for a missing post and generic load failures', async () => {
    mocks.getPostById.mockRejectedValueOnce({ response: { status: 404 } });

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('This post does not exist.');
    expect(wrapper.text()).not.toContain('This article does not exist.');
    expect(wrapper.text()).not.toContain('Article unavailable');

    wrapper.unmount();
    wrapper = null;
    mocks.getPostById.mockRejectedValueOnce(new Error('offline'));
    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.detail-state--error').text()).toContain('The post could not be loaded.');
  });

  it('renders active and tombstoned bounded references', async () => {
    const activeReference = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [],
    };
    mocks.getPostById.mockResolvedValueOnce(canonicalPost({
      quote_post_id: 9,
      quote_post: activeReference,
    }));

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-detail__reference-content').text()).toBe('Referenced post body');
    expect(wrapper.find('.post-detail__reference-tombstone').exists()).toBe(false);

    wrapper.unmount();
    wrapper = null;
    mocks.getPostById.mockResolvedValueOnce(canonicalPost({
      quote_post_id: 9,
      quote_post: { id: 9, deleted: true },
    }));
    wrapper = mountDetail();
    await flushPromises();
    expect(wrapper.find('.post-detail__reference-tombstone').text()).toBe('Post unavailable');
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
    expect(mocks.getPostById).not.toHaveBeenCalled();
  });
});
