// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, type Pinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostDetailView from './PostDetailView.vue';
import { useReplyDraftStore } from '../store/replyDraft';
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
  externalRemoval: vi.fn(),
  externalReplyCount: vi.fn(),
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
  repostPost: vi.fn(),
  undoRepostPost: vi.fn(),
}));

vi.mock('../services/replyService', () => ({
  getPostReplies: mocks.getPostReplies,
  createPostReply: mocks.createPostReply,
  deletePostReply: mocks.deletePostReply,
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
  syncExternalPostRemoval: mocks.externalRemoval,
  syncExternalReplyCount: mocks.externalReplyCount,
}));

const post = (id = 42, overrides: Partial<Post> = {}): Post => ({
  id,
  created_at: '2026-08-27T13:42:00.000Z',
  updated_at: '2026-08-27T13:42:00.000Z',
  published_at: '2026-08-27T13:42:00.000Z',
  author: {
    id: 7,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
  content: `Post ${id} body`,
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  reply_count: 0,
  view_count: 12,
  deleted: false,
  ...overrides,
});

const warmPost = (id = 42): FeedPost => ({
  id,
  author: {
    id: 7,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
  content: `Post ${id} body`,
  media: [],
  createdAt: '2026-08-27T13:42:00.000Z',
  likeCount: 3,
  replyCount: 0,
  viewCount: 12,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

const reply = (id: number, postID = 42): Post => ({
  id,
  created_at: '2026-08-27T13:42:00.000Z',
  updated_at: '2026-08-27T13:42:00.000Z',
  published_at: '2026-08-27T13:42:00.000Z',
  author: {
    id: 8,
    username: 'commenter',
    display_name: 'Commenter',
    avatar_url: '',
  },
  content: `Reply ${id}`,
  conversation_id: postID,
  reply_to_post_id: postID,
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

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

let pinia: Pinia;
let scrollIntoViewMock: ReturnType<typeof vi.fn>;

const draftStore = () => useReplyDraftStore(pinia);

const textareaValue = (wrapper: ReturnType<typeof mount>) => (
  (wrapper.get('.reply-composer__textarea').element as HTMLTextAreaElement).value
);

const mountDetail = () => mount(PostDetailView, {
  attachTo: document.body,
  global: {
    plugins: [pinia],
    stubs: {
      AppIcon: {
        props: ['name', 'size'],
        template: '<span class="test-icon" :data-icon="name" />',
      },
      AuthorIdentity: { template: '<span class="test-author" />' },
      LikeAction: {
        props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'variant'],
        emits: ['toggle'],
        template: '<button class="test-like" type="button" @click="$emit(\'toggle\')">{{ count }}</button>',
      },
      ReplyList: { template: '<div class="test-comments" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('PostDetailView persistent reply drafts', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    pinia = createPinia();
    scrollIntoViewMock = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoViewMock,
    });
    mocks.route = reactive({
      params: { id: '42' },
      query: {},
      hash: '',
    });
    mocks.router.back.mockReset();
    mocks.router.push.mockReset();
    mocks.router.replace.mockReset();
    mocks.router.push.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer test-token',
      currentIdentity: {
        id: 7,
        username: 'viewer',
        display_name: 'Viewer',
        avatar_url: '',
      },
    });
    mocks.feedStore.viewerID = 7;
    mocks.feedStore.markPostDeleted.mockReturnValue(true);
    mocks.handoffStore = { consume: mocks.consumeHandoff };
    mocks.consumeHandoff.mockReturnValue(null);
    mocks.getPostById.mockImplementation((id: string) => Promise.resolve(post(Number(id))));
    mocks.getPostLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
    mocks.createPostReply.mockReset();
    mocks.deletePost.mockResolvedValue(undefined);
    mocks.consumeAttribution.mockReturnValue(null);
    mocks.telemetry.flush.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.restoreAllMocks();
  });

  it('restores a draft after leaving and returning to the same post', async () => {
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('draft 42');
    expect(draftStore().getDraft(42)).toBe('draft 42');

    wrapper.unmount();
    wrapper = null;
    wrapper = mountDetail();
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('draft 42');
  });

  it('keeps drafts isolated while navigating between posts', async () => {
    wrapper = mountDetail();
    await flushPromises();
    await wrapper.get('.reply-composer__textarea').setValue('draft A');

    mocks.route.params.id = '43';
    await flushPromises();
    expect(textareaValue(wrapper)).toBe('');
    await wrapper.get('.reply-composer__textarea').setValue('draft B');

    mocks.route.params.id = '42';
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('draft A');
    expect(draftStore().getDraft(43)).toBe('draft B');
  });

  it('does not mount or expose the draft during warm handoff loading', async () => {
    const request = deferred<Post>();
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'warm-hidden draft');
    mocks.consumeHandoff.mockReturnValueOnce(warmPost());
    mocks.getPostById.mockReturnValueOnce(request.promise);

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-conversation').exists()).toBe(false);
    expect(wrapper.find('.reply-composer__textarea').exists()).toBe(false);

    request.resolve(post());
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('warm-hidden draft');
  });

  it('clears only the submitted draft after a successful reply', async () => {
    const request = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('  useful reply  ');
    await wrapper.get('.reply-composer').trigger('submit');

    expect(mocks.createPostReply).toHaveBeenCalledWith('42', 'useful reply');
    expect(draftStore().getDraft(42)).toBe('  useful reply  ');

    request.resolve(reply(101));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(textareaValue(wrapper)).toBe('');
    expect(mocks.externalReplyCount).toHaveBeenCalledWith({
      postId: 42,
      replyCount: 1,
    });
  });

  it('preserves the draft on failure and allows retry without retyping', async () => {
    mocks.createPostReply
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(reply(102));
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('retry me');
    await wrapper.get('.reply-composer').trigger('submit');
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('retry me');
    expect(wrapper.get('.reply-error').text()).toBe('Reply failed. Please try again.');

    await wrapper.get('.reply-composer').trigger('submit');
    await flushPromises();

    expect(mocks.createPostReply).toHaveBeenNthCalledWith(2, '42', 'retry me');
    expect(draftStore().getDraft(42)).toBe('');
  });

  it('does not clear a newer draft that replaces the submitted snapshot', async () => {
    const request = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('draft A');
    await wrapper.get('.reply-composer').trigger('submit');
    draftStore().setDraft(42, 'draft B');

    request.resolve(reply(103));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('draft B');
    expect(textareaValue(wrapper)).toBe('draft B');
  });

  it('clears a late successful Post A reply without mutating Post B', async () => {
    const request = deferred<Post>();
    mocks.createPostReply.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.reply-composer__textarea').setValue('draft A');
    await wrapper.get('.reply-composer').trigger('submit');

    mocks.route.params.id = '43';
    await flushPromises();
    draftStore().setDraft(43, 'draft B');

    request.resolve(reply(104, 42));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(draftStore().getDraft(43)).toBe('draft B');
    expect(mocks.externalReplyCount).not.toHaveBeenCalled();
  });

  it('preserves drafts across same-viewer identity replacement', async () => {
    wrapper = mountDetail();
    await flushPromises();
    await wrapper.get('.reply-composer__textarea').setValue('same viewer draft');

    mocks.authStore.currentIdentity = {
      id: 7,
      username: 'viewer-renewed',
      display_name: 'Viewer Renewed',
      avatar_url: '',
    };
    await nextTick();

    expect(draftStore().viewerID).toBe(7);
    expect(draftStore().getDraft(42)).toBe('same viewer draft');
  });

  it('clears drafts when the account changes or logs out', async () => {
    wrapper = mountDetail();
    await flushPromises();
    await wrapper.get('.reply-composer__textarea').setValue('private draft');

    mocks.authStore.currentIdentity = {
      id: 8,
      username: 'other-viewer',
      display_name: 'Other Viewer',
      avatar_url: '',
    };
    await nextTick();

    expect(draftStore().viewerID).toBe(8);
    expect(draftStore().getDraft(42)).toBe('');

    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
    await flushPromises();

    expect(draftStore().viewerID).toBeNull();
    expect(draftStore().drafts).toEqual({});
    expect(wrapper.find('.reply-composer').exists()).toBe(false);
  });

  it('clears a draft for a current canonical 404 but preserves it for a generic failure', async () => {
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'remove on 404');
    mocks.getPostById.mockRejectedValueOnce({ response: { status: 404 } });

    wrapper = mountDetail();
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(wrapper.find('.detail-state--error').text()).toContain('This post does not exist.');

    wrapper.unmount();
    wrapper = null;
    draftStore().setDraft(42, 'keep on network error');
    mocks.getPostById.mockRejectedValueOnce(new Error('offline'));
    wrapper = mountDetail();
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('keep on network error');
  });

  it.each([
    ['success', null],
    ['terminal 404', { response: { status: 404 } }],
  ])('clears the current draft after an original Post delete %s', async (_label, error) => {
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'post draft');
    if (error) {
      mocks.deletePost.mockRejectedValueOnce(error);
    } else {
      mocks.deletePost.mockResolvedValueOnce(undefined);
    }
    vi.spyOn(window, 'confirm').mockReturnValue(true);

    wrapper = mountDetail();
    await flushPromises();
    await wrapper.get('.post-detail__delete').trigger('click');
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(mocks.externalRemoval).toHaveBeenCalledWith(42);
  });

  it('restores a draft before consuming ?reply=1 and focuses exactly once', async () => {
    mocks.route.query = { reply: '1' };
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'unfinished reply');

    wrapper = mountDetail();
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('unfinished reply');
    expect(scrollIntoViewMock).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(wrapper.get('.reply-composer__textarea').element);
    expect(mocks.router.replace).toHaveBeenCalledTimes(1);
    expect(mocks.router.replace).toHaveBeenCalledWith(expect.objectContaining({ query: {} }));
    expect(draftStore().getDraft(42)).toBe('unfinished reply');
  });

  it('focuses the existing draft from the Post Reply action', async () => {
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'existing draft');
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.post-detail__reply').trigger('click');
    await flushPromises();

    expect(scrollIntoViewMock).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(wrapper.get('.reply-composer__textarea').element);
    expect(textareaValue(wrapper)).toBe('existing draft');
  });
});
