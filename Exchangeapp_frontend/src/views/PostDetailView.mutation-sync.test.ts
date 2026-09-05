// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import PostDetailView from './PostDetailView.vue';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  getPostById: vi.fn(),
  getPostLikeState: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostRepostState: vi.fn(),
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
  createPostReply: vi.fn(),
  deletePostReply: vi.fn(),
  getPostReplies: vi.fn(),
  getUser: vi.fn(),
  deletePost: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: {
    recordReadEnd: vi.fn(),
    flush: vi.fn().mockResolvedValue(undefined),
  },
  postViewTelemetry: { enqueue: vi.fn() },
  router: { back: vi.fn(), push: vi.fn(), replace: vi.fn() },
  routeLeave: vi.fn(),
  authStore: {
    isAuthenticated: true,
    token: 'Bearer test-token',
    currentIdentity: { id: 7, username: 'viewer' },
  },
  feedStore: {
    viewerID: 7,
    markPostDeleted: vi.fn(),
  },
  externalLike: vi.fn(),
  externalRepost: vi.fn(),
  externalRemoval: vi.fn(),
  externalReplyCount: vi.fn(),
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
vi.mock('../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => ({ consume: vi.fn(() => null) }),
}));
vi.mock('../store/sessionSync', () => ({
  syncExternalPostLikeState: mocks.externalLike,
  syncExternalPostRepostState: mocks.externalRepost,
  syncExternalPostRemoval: mocks.externalRemoval,
  syncExternalReplyCount: mocks.externalReplyCount,
}));
vi.mock('../services/postService', () => ({
  getPostById: mocks.getPostById,
  deletePost: mocks.deletePost,
}));
vi.mock('../services/likeService', () => ({
  getPostLikeState: mocks.getPostLikeState,
  likePost: mocks.likePost,
  unlikePost: mocks.unlikePost,
}));
vi.mock('../services/repostService', () => ({
  getPostRepostState: mocks.getPostRepostState,
  repostPost: mocks.repostPost,
  undoRepostPost: mocks.undoRepostPost,
}));
vi.mock('../services/replyService', () => ({
  createPostReply: mocks.createPostReply,
  deletePostReply: mocks.deletePostReply,
  getPostReplies: mocks.getPostReplies,
}));
vi.mock('../services/userService', () => ({ getUser: mocks.getUser }));
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

const post: Post = {
  id: 42,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: { id: 7, username: 'author', display_name: 'Author', avatar_url: '' },
  content: 'Post body',
  conversation_id: 42,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  reply_count: 2,
  view_count: 0,
  deleted: false,
};

const reply = (id: number): Post => ({
  id,
  created_at: '2026-08-15T00:00:00.000Z',
  updated_at: '2026-08-15T00:00:00.000Z',
  published_at: '2026-08-15T00:00:00.000Z',
  author: { id: 8, username: 'commenter', display_name: 'Commenter', avatar_url: '' },
  content: `Reply ${id}`,
  conversation_id: 42,
  reply_to_post_id: 42,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  reply_count: 0,
  view_count: 0,
  deleted: false,
});

const ownReply = (id: number): Post => ({
  ...reply(id),
  author: { id: 7, username: 'viewer', display_name: 'Viewer', avatar_url: '' },
});

const mountDetail = () => mount(PostDetailView, {
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
      ReplyComposer: {
        emits: ['submit'],
        methods: { clear: vi.fn() },
        template: '<button class="test-create-comment" type="button" @click="$emit(\'submit\', \'hello\')">Reply</button>',
      },
      ReplyList: {
        props: ['replies', 'deletingReplyId'],
        emits: ['requestDelete'],
        template: '<button class="test-delete-comment" type="button" :disabled="deletingReplyId !== null" @click="$emit(\'requestDelete\', replies[0]?.id)">Delete reply</button>',
      },
      ConfirmDialog: {
        props: ['title', 'description', 'confirmLabel', 'cancelLabel', 'danger', 'busy', 'error'],
        emits: ['confirm', 'cancel'],
        template: '<div class="test-confirm-dialog"><span v-if="error" class="test-confirm-error">{{ error }}</span><button class="test-confirm-cancel" type="button" :disabled="busy" @click="$emit(\'cancel\')">{{ cancelLabel }}</button><button class="test-confirm-delete" type="button" :disabled="busy" @click="$emit(\'confirm\')">{{ busy ? \'Deleting…\' : confirmLabel }}</button></div>',
      },
    },
  },
});

describe('PostDetailView mutation synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getPostById.mockResolvedValue(post);
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostRepostState.mockResolvedValue({ reposts: 0, reposted: false });
    mocks.getPostReplies.mockResolvedValue({ items: [reply(9)], next_cursor: null });
    mocks.getUser.mockResolvedValue({
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
      bio: '',
      created_at: '2026-08-15T00:00:00.000Z',
    });
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
  });

  it('syncs a successful Detail like but not a failed like', async () => {
    mocks.likePost.mockResolvedValueOnce({ liked: true, likes: 4 });
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).toHaveBeenCalledWith({
      postId: 42,
      likes: 4,
      liked: true,
      status: 'ready',
    });

    mounted.unmount();
    mocks.externalLike.mockClear();
    mocks.likePost.mockRejectedValueOnce(new Error('offline'));
    const failed = mountDetail();
    await flushPromises();
    await failed.find('.test-like').trigger('click');
    await flushPromises();

    expect(mocks.externalLike).not.toHaveBeenCalled();
    failed.unmount();
  });

  it('optimistically toggles Detail Repost, settles from server state, and syncs cached surfaces', async () => {
    mocks.getPostRepostState.mockResolvedValueOnce({ reposts: 8, reposted: false });
    mocks.repostPost.mockResolvedValueOnce({ reposts: 9, reposted: true });
    const mounted = mountDetail();
    await flushPromises();

    const repost = mounted.find('.repost-action');
    expect(repost.attributes('aria-label')).toBe('Repost post, 8 reposts');
    await repost.trigger('click');
    expect(repost.text()).toContain('9');
    await flushPromises();

    expect(mocks.repostPost).toHaveBeenCalledWith('42');
    expect(mocks.externalRepost).toHaveBeenCalledWith({
      postId: 42,
      reposts: 9,
      reposted: true,
      status: 'ready',
    });
    expect(repost.attributes('aria-label')).toBe('Undo repost, 9 reposts');
    mounted.unmount();
  });

  it('rolls Detail Repost back with the specified error after mutation failure', async () => {
    mocks.getPostRepostState.mockResolvedValueOnce({ reposts: 8, reposted: false });
    mocks.repostPost.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.repost-action').trigger('click');
    await flushPromises();

    expect(mounted.find('.repost-action').text()).toContain('8');
    expect(mounted.find('.detail-inline-error').text()).toBe('Could not update repost. Please try again.');
    expect(mocks.externalRepost).not.toHaveBeenCalled();
    mounted.unmount();
  });

  it.each([
    ['success', undefined],
    ['terminal 404', { response: { status: 404 } }],
  ])('syncs Detail deletion before navigation on %s', async (_label, error) => {
    if (error) mocks.deletePost.mockRejectedValueOnce(error);
    window.confirm = vi.fn().mockReturnValue(true);
    const mounted = mountDetail();
    await flushPromises();
    await mounted.find('.post-detail__delete').trigger('click');
    await flushPromises();

    expect(mocks.feedStore.markPostDeleted).toHaveBeenCalledWith(42, 7);
    expect(mocks.externalRemoval).toHaveBeenCalledWith(42);
    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'UserProfile',
      params: { id: '7' },
    });
    expect(mocks.feedStore.markPostDeleted.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.externalRemoval.mock.invocationCallOrder[0]);
    expect(mocks.externalRemoval.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.router.replace.mock.invocationCallOrder[0]);
    mounted.unmount();
  });

  it('syncs absolute comment counts after create and delete success', async () => {
    mocks.createPostReply.mockResolvedValueOnce(ownReply(10));
    mocks.deletePostReply.mockResolvedValueOnce(undefined);
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();
    expect(mocks.externalReplyCount).toHaveBeenNthCalledWith(1, {
      postId: 42,
      replyCount: 3,
    });

    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();
    expect(mocks.deletePostReply).not.toHaveBeenCalled();
    expect(mounted.find('.test-confirm-dialog').exists()).toBe(true);

    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.deletePostReply).toHaveBeenCalledWith(10);
    expect(mocks.externalReplyCount).toHaveBeenNthCalledWith(2, {
      postId: 42,
      replyCount: 2,
    });
  });

  it('does not synchronize failed reply creation', async () => {
    mocks.createPostReply.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-create-comment').trigger('click');
    await flushPromises();

    expect(mocks.externalReplyCount).not.toHaveBeenCalled();
  });

  it('does not synchronize a failed confirmed reply deletion', async () => {
    mocks.getPostReplies.mockResolvedValueOnce({ items: [ownReply(9)], next_cursor: null });
    mocks.deletePostReply.mockRejectedValueOnce(new Error('offline'));
    const mounted = mountDetail();
    await flushPromises();

    await mounted.find('.test-delete-comment').trigger('click');
    await flushPromises();
    await mounted.find('.test-confirm-delete').trigger('click');
    await flushPromises();

    expect(mocks.deletePostReply).toHaveBeenCalledWith(9);
    expect(mocks.externalReplyCount).not.toHaveBeenCalled();
    expect(mounted.find('.test-confirm-error').text())
      .toBe('Reply could not be deleted. Please try again.');
  });
});
