// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import PostDetailView from './PostDetailView.vue';
import type { Post } from '../types/Post';
import type { FeedPost } from '../types/Feed';

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

type CanonicalPostOverrides = Partial<Post> & {
  title?: string;
  preview?: string;
  cover_image_url?: string;
};

const canonicalPost = (input: CanonicalPostOverrides = {}): Post => {
  const {
    title,
    preview,
    cover_image_url,
    ...overrides
  } = input;
  return {
    id: 42,
    created_at: '2026-08-26T00:00:00.000Z',
    updated_at: '2026-08-26T00:00:00.000Z',
    published_at: '2026-08-26T00:00:00.000Z',
    author: {
      id: 7,
      username: 'server-author',
      display_name: 'Server Author',
      avatar_url: '/server-author.png',
    },
    content: 'Authoritative post body',
    conversation_id: 42,
    reply_to_post_id: null,
    quote_post_id: null,
    reply_to_post: null,
    quote_post: null,
    visibility: 'public',
    article: {
      title: title ?? 'Server title',
      preview: preview ?? 'Server preview',
      cover_image_url: cover_image_url ?? '',
      publication_state: 'published',
      published_at: '2026-08-26T00:00:00.000Z',
      expired_at: null,
    },
    like_count: 11,
    reply_count: 4,
    view_count: 321,
    deleted: false,
    ...overrides,
  };
};

const post = (overrides: Partial<FeedPost> = {}): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'warm-author',
    display_name: 'Warm Author',
    avatar_url: '/warm-author.png',
  },
  title: 'Warm title',
  excerpt: 'Warm excerpt only',
  coverImageUrl: '/cover-a.png',
  createdAt: '2026-08-25T00:00:00.000Z',
  likeCount: 10,
  replyCount: 3,
  viewCount: 300,
  liked: true,
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
      AppIcon: { template: '<span class="test-icon" />' },
      AuthorIdentity: {
        props: ['author', 'createdAt'],
        template: '<div class="test-author">{{ author.username }}</div>',
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

describe('PostDetailView warm and cold transition', () => {
  let mounted: ReturnType<typeof mount> | null = null;

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
    mounted?.unmount();
    mounted = null;
    vi.restoreAllMocks();
  });

  it('shows the Post header and circular cold spinner without visible loading text', async () => {
    const request = deferred<Post>();
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();

    expect(mounted.find('.detail-header__back').exists()).toBe(true);
    expect(mounted.find('.detail-loading[role="status"]').exists()).toBe(true);
    expect(mounted.find('.detail-loading__spinner').exists()).toBe(true);
    expect(mounted.text()).not.toContain('Loading full article');
    expect(mounted.find('.post-detail').exists()).toBe(false);
    expect(mocks.getPostLikeState).not.toHaveBeenCalled();
    expect(mocks.getPostRepostState).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();
    expect(mocks.postViewTelemetry.enqueue).not.toHaveBeenCalled();

    request.resolve(canonicalPost());
    await flushPromises();

    expect(mounted.find('.detail-loading').exists()).toBe(false);
    expect(mounted.find('.post-detail__body').text()).toBe('Authoritative post body');
    expect(mocks.getPostById).toHaveBeenCalledTimes(1);
    expect(mocks.getPostLikeState).toHaveBeenCalledTimes(1);
    expect(mocks.getPostRepostState).toHaveBeenCalledTimes(1);
    expect(mocks.getPostReplies).toHaveBeenCalledTimes(1);
  });

  it('removes the cold spinner and shows the existing error UI on a 404', async () => {
    mocks.getPostById.mockRejectedValueOnce({ response: { status: 404 } });

    mounted = mountDetail();
    await flushPromises();

    expect(mounted.find('.detail-loading').exists()).toBe(false);
    expect(mounted.find('.detail-state--error').text()).toContain('This post does not exist.');
    expect(mocks.postViewTelemetry.enqueue).not.toHaveBeenCalled();
    expect(mocks.getPostLikeState).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();
  });

  it('renders a warm handoff immediately without starting authoritative work early', async () => {
    const request = deferred<Post>();
    const warmPost = post();
    mocks.consumeHandoff.mockReturnValueOnce(warmPost);
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();

    expect(mocks.consumeHandoff).toHaveBeenCalledWith(42);
    expect(mounted.find('.test-author').text()).toBe('warm-author');
    expect(mounted.find('.post-detail__headline').text()).toBe('Warm title');
    expect(mounted.find('.post-detail__body').text()).toBe('Warm excerpt only');
    expect(mounted.find('.post-detail__body').attributes('aria-busy')).toBe('true');
    expect(mounted.find('.post-detail__cover').exists()).toBe(true);
    expect(mounted.find('.detail-warm-loading[role="status"]').exists()).toBe(true);
    expect(mounted.find('.detail-loading').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Loading full article');
    expect(mounted.find('.test-like-action').exists()).toBe(false);
    expect(mounted.find('.repost-action').exists()).toBe(false);
    expect(mounted.find('.post-conversation').exists()).toBe(false);
    expect(mocks.getPostById).toHaveBeenCalledTimes(1);
    expect(mocks.getPostLikeState).not.toHaveBeenCalled();
    expect(mocks.getPostRepostState).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();
    expect(mocks.postViewTelemetry.enqueue).not.toHaveBeenCalled();

    request.resolve(canonicalPost());
    await flushPromises();
    expect(mounted.find('.detail-warm-loading').exists()).toBe(false);
  });

  it('replaces warm presentation with authoritative title, body, cover, counts, and replies', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post({
      title: 'Warm title',
      excerpt: 'Warm excerpt only',
      coverImageUrl: '/cover-a.png',
      likeCount: 10,
      replyCount: 3,
      viewCount: 300,
    }));
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();
    request.resolve(canonicalPost({
      title: 'Server title',
      content: 'Authoritative article body',
      cover_image_url: '/cover-b.png',
      like_count: 11,
      reply_count: 4,
      view_count: 321,
    }));
    await flushPromises();

    expect(mounted.find('.post-detail__headline').text()).toBe('Server title');
    expect(mounted.find('.post-detail__body').text()).toBe('Authoritative article body');
    expect(mounted.find('.post-detail__body').attributes('aria-busy')).toBeUndefined();
    expect(mounted.find('.post-detail__cover img').attributes('src')).toBe('/cover-b.png');
    expect(mounted.find('.post-conversation').exists()).toBe(true);
    expect(mounted.find('.test-like-action').exists()).toBe(true);
    expect(mounted.find('.detail-warm-loading').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Warm excerpt only');
    expect(mounted.text()).toContain('4');
    expect(mocks.postViewTelemetry.enqueue).toHaveBeenCalledTimes(1);
  });

  it('removes stale warm content on a 404 without starting detail side effects', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post());
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();
    expect(mounted.find('.post-detail').exists()).toBe(true);

    request.reject({ response: { status: 404 } });
    await flushPromises();

    expect(mounted.find('.post-detail').exists()).toBe(false);
    expect(mounted.find('.detail-state--error').text()).toContain('This post does not exist.');
    expect(mounted.find('.detail-warm-loading').exists()).toBe(false);
    expect(mocks.postViewTelemetry.enqueue).not.toHaveBeenCalled();
    expect(mocks.getPostLikeState).not.toHaveBeenCalled();
    expect(mocks.getPostRepostState).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();
  });

  it('keeps reply intent in the URL during warm loading and consumes it after success', async () => {
    const request = deferred<Post>();
    mocks.route.query.reply = '1';
    mocks.consumeHandoff.mockReturnValueOnce(post());
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();

    expect(mocks.route.query.reply).toBe('1');
    expect(mounted.find('.test-composer').exists()).toBe(false);
    expect(mocks.composerFocus).not.toHaveBeenCalled();
    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(mocks.getPostReplies).not.toHaveBeenCalled();

    request.resolve(canonicalPost());
    await flushPromises();

    expect(mounted.find('.test-composer').exists()).toBe(true);
    expect(mocks.composerFocus).toHaveBeenCalledTimes(1);
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'PostDetail',
      params: { id: '42' },
      query: {},
      hash: '',
    });
  });

  it('keeps the current route presentation when an older detail request resolves last', async () => {
    const firstRequest = deferred<Post>();
    const secondRequest = deferred<Post>();
    mocks.consumeHandoff.mockImplementation((id: number) => id === 43 ? post({
      id: 43,
      title: 'Warm B',
      excerpt: 'Warm B excerpt',
    }) : null);
    mocks.getPostById
      .mockImplementationOnce(() => firstRequest.promise)
      .mockImplementationOnce(() => secondRequest.promise);

    mounted = mountDetail();
    await flushPromises();
    mocks.route.params.id = '43';
    await flushPromises();

    expect(mocks.getPostById).toHaveBeenCalledTimes(2);
    expect(mounted.find('.post-detail__body').text()).toBe('Warm B excerpt');

    firstRequest.resolve(canonicalPost({ id: 42, title: 'Stale A', content: 'Stale A body' }));
    await flushPromises();
    expect(mounted.find('.post-detail__body').text()).toBe('Warm B excerpt');
    expect(mocks.postViewTelemetry.enqueue).not.toHaveBeenCalled();

    secondRequest.resolve(canonicalPost({ id: 43, title: 'Server B', content: 'Server B body' }));
    await flushPromises();
    expect(mounted.find('.post-detail__body').text()).toBe('Server B body');
    expect(mocks.postViewTelemetry.enqueue).toHaveBeenCalledTimes(1);
    expect(mocks.postViewTelemetry.enqueue).toHaveBeenCalledWith(
      43,
      expect.any(String),
      'post_detail',
    );
  });

  it('keeps a failed warm cover slot and retries a different authoritative URL', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post({ coverImageUrl: '/cover-a.png' }));
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();
    await mounted.find('.post-detail__cover img').trigger('error');

    expect(mounted.find('.post-detail__cover').exists()).toBe(true);
    expect(mounted.find('.post-detail__cover-placeholder').exists()).toBe(true);

    request.resolve(canonicalPost({ cover_image_url: '/cover-b.png' }));
    await flushPromises();

    expect(mounted.find('.post-detail__cover').exists()).toBe(true);
    expect(mounted.find('.post-detail__cover img').attributes('src')).toBe('/cover-b.png');
    expect(mounted.find('.post-detail__cover-placeholder').exists()).toBe(false);
  });

  it('keeps the placeholder when the authoritative response returns the same failed cover URL', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post({ coverImageUrl: '/cover-a.png' }));
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();
    await mounted.find('.post-detail__cover img').trigger('error');
    request.resolve(canonicalPost({ cover_image_url: '/cover-a.png' }));
    await flushPromises();

    expect(mounted.find('.post-detail__cover').exists()).toBe(true);
    expect(mounted.find('.post-detail__cover-placeholder').exists()).toBe(true);
    expect(mounted.find('.post-detail__cover img').exists()).toBe(false);
  });

  it('removes the cover figure when the authoritative article removes its cover', async () => {
    const request = deferred<Post>();
    mocks.consumeHandoff.mockReturnValueOnce(post({ coverImageUrl: '/cover-a.png' }));
    mocks.getPostById.mockReturnValueOnce(request.promise);

    mounted = mountDetail();
    await flushPromises();
    request.resolve(canonicalPost({ cover_image_url: '' }));
    await flushPromises();

    expect(mounted.find('.post-detail__cover').exists()).toBe(false);
  });
});
