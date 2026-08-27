// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, type Pinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import NewsDetailView from './NewsDetailView.vue';
import { useReplyDraftStore } from '../store/replyDraft';
import type { Article } from '../types/Article';
import type { ArticleComment } from '../types/Comment';
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
  externalRemoval: vi.fn(),
  externalCommentCount: vi.fn(),
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
  getArticleById: mocks.getArticleById,
  deleteArticle: mocks.deleteArticle,
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
  getArticleComments: mocks.getArticleComments,
  createArticleComment: mocks.createArticleComment,
  deleteComment: mocks.deleteComment,
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
  syncExternalArticleRemoval: mocks.externalRemoval,
  syncExternalCommentCount: mocks.externalCommentCount,
}));

const article = (id = 42, overrides: Partial<Article> = {}): Article => ({
  ID: id,
  CreatedAt: '2026-08-27T13:42:00.000Z',
  UpdatedAt: '2026-08-27T13:42:00.000Z',
  title: `Post ${id}`,
  content: `Post ${id} body`,
  preview: `Post ${id} preview`,
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-27T13:42:00.000Z',
  expired_at: null,
  like_count: 3,
  comment_count: 0,
  view_count: 12,
  like_sync_version: 1,
  author: {
    id: 7,
    username: 'author',
    display_name: 'Author',
    avatar_url: '',
  },
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
  title: `Post ${id}`,
  excerpt: `Post ${id} preview`,
  coverImageUrl: '',
  createdAt: '2026-08-27T13:42:00.000Z',
  likeCount: 3,
  commentCount: 0,
  viewCount: 12,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

const comment = (id: number, articleID = 42): ArticleComment => ({
  id,
  article_id: articleID,
  content: `Reply ${id}`,
  created_at: '2026-08-27T13:42:00.000Z',
  author: {
    id: 8,
    username: 'commenter',
    display_name: 'Commenter',
    avatar_url: '',
  },
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
  (wrapper.get('.comment-composer__textarea').element as HTMLTextAreaElement).value
);

const mountDetail = () => mount(NewsDetailView, {
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
      CommentList: { template: '<div class="test-comments" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('NewsDetailView persistent reply drafts', () => {
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
    mocks.feedStore.markArticleDeleted.mockReturnValue(true);
    mocks.handoffStore = { consume: mocks.consumeHandoff };
    mocks.consumeHandoff.mockReturnValue(null);
    mocks.getArticleById.mockImplementation((id: string) => Promise.resolve(article(Number(id))));
    mocks.getArticleLikeState.mockResolvedValue({ liked: false, likes: 3 });
    mocks.getArticleComments.mockResolvedValue({ items: [], next_cursor: null });
    mocks.createArticleComment.mockReset();
    mocks.deleteArticle.mockResolvedValue(undefined);
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

    await wrapper.get('.comment-composer__textarea').setValue('draft 42');
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
    await wrapper.get('.comment-composer__textarea').setValue('draft A');

    mocks.route.params.id = '43';
    await flushPromises();
    expect(textareaValue(wrapper)).toBe('');
    await wrapper.get('.comment-composer__textarea').setValue('draft B');

    mocks.route.params.id = '42';
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('draft A');
    expect(draftStore().getDraft(43)).toBe('draft B');
  });

  it('does not mount or expose the draft during warm handoff loading', async () => {
    const request = deferred<Article>();
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'warm-hidden draft');
    mocks.consumeHandoff.mockReturnValueOnce(warmPost());
    mocks.getArticleById.mockReturnValueOnce(request.promise);

    wrapper = mountDetail();
    await flushPromises();

    expect(wrapper.find('.post-conversation').exists()).toBe(false);
    expect(wrapper.find('.comment-composer__textarea').exists()).toBe(false);

    request.resolve(article());
    await flushPromises();

    expect(textareaValue(wrapper)).toBe('warm-hidden draft');
  });

  it('clears only the submitted draft after a successful reply', async () => {
    const request = deferred<ArticleComment>();
    mocks.createArticleComment.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.comment-composer__textarea').setValue('  useful reply  ');
    await wrapper.get('.comment-composer').trigger('submit');

    expect(mocks.createArticleComment).toHaveBeenCalledWith('42', 'useful reply');
    expect(draftStore().getDraft(42)).toBe('  useful reply  ');

    request.resolve(comment(101));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(textareaValue(wrapper)).toBe('');
    expect(mocks.externalCommentCount).toHaveBeenCalledWith({
      articleId: 42,
      commentCount: 1,
    });
  });

  it('preserves the draft on failure and allows retry without retyping', async () => {
    mocks.createArticleComment
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(comment(102));
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.comment-composer__textarea').setValue('retry me');
    await wrapper.get('.comment-composer').trigger('submit');
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('retry me');
    expect(wrapper.get('.comment-error').text()).toBe('Reply failed. Please try again.');

    await wrapper.get('.comment-composer').trigger('submit');
    await flushPromises();

    expect(mocks.createArticleComment).toHaveBeenNthCalledWith(2, '42', 'retry me');
    expect(draftStore().getDraft(42)).toBe('');
  });

  it('does not clear a newer draft that replaces the submitted snapshot', async () => {
    const request = deferred<ArticleComment>();
    mocks.createArticleComment.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.comment-composer__textarea').setValue('draft A');
    await wrapper.get('.comment-composer').trigger('submit');
    draftStore().setDraft(42, 'draft B');

    request.resolve(comment(103));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('draft B');
    expect(textareaValue(wrapper)).toBe('draft B');
  });

  it('clears a late successful Post A reply without mutating Post B', async () => {
    const request = deferred<ArticleComment>();
    mocks.createArticleComment.mockReturnValueOnce(request.promise);
    wrapper = mountDetail();
    await flushPromises();

    await wrapper.get('.comment-composer__textarea').setValue('draft A');
    await wrapper.get('.comment-composer').trigger('submit');

    mocks.route.params.id = '43';
    await flushPromises();
    draftStore().setDraft(43, 'draft B');

    request.resolve(comment(104, 42));
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(draftStore().getDraft(43)).toBe('draft B');
    expect(mocks.externalCommentCount).not.toHaveBeenCalled();
  });

  it('preserves drafts across same-viewer identity replacement', async () => {
    wrapper = mountDetail();
    await flushPromises();
    await wrapper.get('.comment-composer__textarea').setValue('same viewer draft');

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
    await wrapper.get('.comment-composer__textarea').setValue('private draft');

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
    expect(wrapper.find('.comment-composer').exists()).toBe(false);
  });

  it('clears a draft for a current canonical 404 but preserves it for a generic failure', async () => {
    draftStore().setViewer(7);
    draftStore().setDraft(42, 'remove on 404');
    mocks.getArticleById.mockRejectedValueOnce({ response: { status: 404 } });

    wrapper = mountDetail();
    await flushPromises();

    expect(draftStore().getDraft(42)).toBe('');
    expect(wrapper.find('.detail-state--error').text()).toContain('This post does not exist.');

    wrapper.unmount();
    wrapper = null;
    draftStore().setDraft(42, 'keep on network error');
    mocks.getArticleById.mockRejectedValueOnce(new Error('offline'));
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
      mocks.deleteArticle.mockRejectedValueOnce(error);
    } else {
      mocks.deleteArticle.mockResolvedValueOnce(undefined);
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
    expect(document.activeElement).toBe(wrapper.get('.comment-composer__textarea').element);
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
    expect(document.activeElement).toBe(wrapper.get('.comment-composer__textarea').element);
    expect(textareaValue(wrapper)).toBe('existing draft');
  });
});
