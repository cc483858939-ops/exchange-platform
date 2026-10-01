// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, type Pinia } from 'pinia';
import { nextTick, reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PostDetailView from './PostDetailView.vue';
import { useReplyDraftStore } from '../store/replyDraft';
import type { Post } from '../types/Post';

const mocks = vi.hoisted(() => ({
  route: null as any,
  router: { back: vi.fn(), push: vi.fn(), replace: vi.fn() },
  routeLeave: vi.fn(),
  routeUpdate: vi.fn(),
  authStore: null as any,
  replySubmissionStore: null as any,
  feedStore: { viewerID: 7, markPostDeleted: vi.fn() },
  handoffStore: null as any,
  getPostById: vi.fn(),
  getPostEngagementStates: vi.fn(),
  getPostLikeState: vi.fn(),
  getPostRepostState: vi.fn(),
  likePost: vi.fn(),
  unlikePost: vi.fn(),
  getPostReplies: vi.fn(),
  deletePostReply: vi.fn(),
  deletePost: vi.fn(),
  getReplyDraft: vi.fn(),
  saveReplyDraft: vi.fn(),
  deleteReplyDraftIfUnchanged: vi.fn(),
  consumeAttribution: vi.fn(),
  telemetry: { recordReadEnd: vi.fn(), flush: vi.fn() },
  postViewTelemetry: { enqueue: vi.fn() },
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
  onBeforeRouteLeave: (guard: any) => mocks.routeLeave.mockImplementation(guard),
  onBeforeRouteUpdate: (guard: any) => mocks.routeUpdate.mockImplementation(guard),
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/feed', () => ({ useFeedStore: () => mocks.feedStore }));
vi.mock('../store/postDetailHandoff', () => ({ usePostDetailHandoffStore: () => mocks.handoffStore }));
vi.mock('../store/replySubmission', () => ({ useReplySubmissionStore: () => mocks.replySubmissionStore }));
vi.mock('../services/postService', () => ({ getPostById: mocks.getPostById, deletePost: mocks.deletePost }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.getPostEngagementStates }));
vi.mock('../services/likeService', () => ({ getPostLikeState: mocks.getPostLikeState, likePost: mocks.likePost, unlikePost: mocks.unlikePost }));
vi.mock('../services/repostService', () => ({ getPostRepostState: mocks.getPostRepostState, repostPost: vi.fn(), undoRepostPost: vi.fn() }));
vi.mock('../services/replyService', () => ({ getPostReplies: mocks.getPostReplies, deletePostReply: mocks.deletePostReply }));
vi.mock('../storage/replyStorage', () => ({
  getReplyDraft: mocks.getReplyDraft,
  saveReplyDraft: mocks.saveReplyDraft,
  deleteReplyDraftIfUnchanged: mocks.deleteReplyDraftIfUnchanged,
}));
vi.mock('../services/recommendationAttribution', () => ({ consumePendingRecommendationAttribution: mocks.consumeAttribution }));
vi.mock('../services/recommendationTelemetry', () => ({ getRecommendationTelemetry: () => mocks.telemetry }));
vi.mock('../services/postViewTelemetry', () => ({ createPostViewEventID: () => 'view-id', getPostViewTelemetry: () => mocks.postViewTelemetry }));
vi.mock('../services/postReadTracker', () => ({
  createPostReadGeometry: () => ({ postTopDoc: 0, postHeight: 1, currentViewportBottomDoc: 1 }),
  PostReadTracker: class {
    start() {}
    updateGeometry() {}
    recordScroll() {}
    pause() {}
    resume() {}
    finish(exitType: string) { return { exitType }; }
  },
}));

const post = (id = 42, overrides: Partial<Post> = {}): Post => ({
  id,
  created_at: '2026-08-27T13:42:00.000Z',
  updated_at: '2026-08-27T13:42:00.000Z',
  published_at: '2026-08-27T13:42:00.000Z',
  author: { id: 7, username: 'author', display_name: 'Author', avatar_url: '' },
  content: `Post ${id} body`,
  language: 'und',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 3,
  repost_count: 0,
  reply_count: 0,
  quote_count: 0,
  view_count: 12,
  deleted: false,
  ...overrides,
});

const flushAsync = async () => {
  await flushPromises();
  for (let index = 0; index < 4; index += 1) await nextTick();
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
};

let pinia: Pinia;
let wrapper: ReturnType<typeof mount> | null;
const draftStore = () => useReplyDraftStore(pinia);

const mountDetail = () => mount(PostDetailView, {
  attachTo: document.body,
  global: {
    plugins: [pinia],
    stubs: {
      AppIcon: { props: ['name'], template: '<span :data-icon="name" />' },
      AuthorIdentity: { template: '<span />' },
      LikeAction: { template: '<button />' },
      RepostAction: { template: '<button />' },
      BookmarkAction: { template: '<button />' },
      PostMediaGrid: { emits: ['open'], template: '<button class="test-open-media" type="button" @click="$emit(\'open\', 0)" />' },
      PostMediaViewer: { template: '<div><slot name="context" /></div>' },
      ReplyList: { template: '<div />' },
      ReplyItem: { template: '<div />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const makeSubmissionStore = () => {
  const state = reactive({ operations: [] as any[] });
  return Object.assign(state, {
    activeViewerID: 7,
    recoveryError: '',
    currentViewerID: 7,
    activateViewer: vi.fn(async () => undefined),
    ensureContextHydrated: vi.fn(async () => null),
    getOperation: vi.fn((viewerID: number, parentPostID: number) => state.operations.find(operation => (
      operation.viewerID === viewerID && operation.parentPostID === parentPostID
    )) ?? null),
    getBlockReason: vi.fn(() => null),
    startOrRetry: vi.fn(async (parentPostID: number, content: string) => {
      const operation = {
        id: 'reply-operation', viewerID: 7, parentPostID, content: content.trim(),
        sourceDraftContent: null, phase: 'publishing', failureKind: null,
        error: '', startedAt: 1, post: null, durableOwned: true, cleanupPending: false,
      };
      draftStore().bindSubmission(parentPostID, operation.id, operation.content);
      state.operations.push(operation);
      return { status: 'accepted', operation };
    }),
    retry: vi.fn(async () => true),
    abandonFailedOperation: vi.fn(async () => true),
    adoptHydratedOperation: vi.fn(() => false),
    acknowledgeSucceededOperation: vi.fn(() => true),
  });
};

describe('PostDetail reply draft durability UX', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    pinia = createPinia();
    wrapper = null;
    mocks.route = reactive({ params: { id: '42' }, query: {}, hash: '' });
    mocks.authStore = reactive({
      isAuthenticated: true,
      token: 'Bearer token',
      currentIdentity: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' },
    });
    mocks.replySubmissionStore = makeSubmissionStore();
    mocks.handoffStore = { consume: vi.fn(() => null) };
    mocks.getPostById.mockImplementation(async (id: string | number) => post(Number(id)));
    mocks.getPostEngagementStates.mockResolvedValue({ items: [] });
    mocks.getPostLikeState.mockResolvedValue({ likes: 3, liked: false });
    mocks.getPostRepostState.mockResolvedValue({ reposts: 0, reposted: false });
    mocks.getPostReplies.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getReplyDraft.mockResolvedValue(null);
    mocks.saveReplyDraft.mockResolvedValue(undefined);
    mocks.deleteReplyDraftIfUnchanged.mockResolvedValue('deleted');
    mocks.consumeAttribution.mockReturnValue({ source: 'recommendation' });
    mocks.router.back.mockReset();
    mocks.router.push.mockReset().mockResolvedValue(undefined);
    mocks.router.replace.mockReset().mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    document.body.innerHTML = '';
  });

  it('hydrates the exact saved reply and only persists again on explicit Save draft', async () => {
    mocks.getReplyDraft.mockResolvedValue({
      key: '7:42', viewerID: 7, parentPostID: 42, content: '  saved\nreply  ', createdAt: 1, updatedAt: 2,
    });
    wrapper = mountDetail();
    await flushAsync();

    const textareas = wrapper.findAll('.reply-composer__textarea');
    expect(textareas.length).toBeGreaterThanOrEqual(1);
    expect((textareas[0]!.element as HTMLTextAreaElement).value).toBe('  saved\nreply  ');
    expect(mocks.saveReplyDraft).not.toHaveBeenCalled();

    await textareas[0]!.setValue('  updated reply  ');
    await wrapper.find('.reply-composer__draft-action').trigger('click');
    await flushAsync();
    expect(mocks.saveReplyDraft).toHaveBeenCalledWith(expect.objectContaining({ content: '  updated reply  ' }));
    expect(draftStore().hasUnsavedChanges(42)).toBe(false);
  });

  it('keeps normal and media-context composers synchronized and disables both after durable acceptance', async () => {
    mocks.getPostById.mockResolvedValueOnce(post(42, { media: [{
      type: 'image', url: '/image.png', large_url: '/image-large.png', width: 800, height: 600, position: 0,
    }] }));
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.test-open-media').trigger('click');
    await nextTick();
    const textareas = wrapper.findAll('.reply-composer__textarea');
    expect(textareas.length).toBe(2);
    await textareas[0]!.setValue('shared reply');
    await nextTick();
    expect((textareas[1]!.element as HTMLTextAreaElement).value).toBe('shared reply');

    await wrapper.find('.reply-composer').trigger('submit');
    await flushAsync();
    expect(mocks.replySubmissionStore.startOrRetry).toHaveBeenCalledWith(42, 'shared reply');
    expect(textareas.every(textarea => (textarea.element as HTMLTextAreaElement).disabled)).toBe(true);
  });

  it('disables both composers and blocks Reply while an explicit draft Save is pending', async () => {
    const write = deferred<void>();
    mocks.saveReplyDraft.mockReturnValueOnce(write.promise);
    mocks.getPostById.mockResolvedValueOnce(post(42, { media: [{
      type: 'image', url: '/image.png', large_url: '/image-large.png', width: 800, height: 600, position: 0,
    }] }));
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.test-open-media').trigger('click');
    await nextTick();
    const textareas = wrapper.findAll('.reply-composer__textarea');
    await textareas[0]!.setValue('save before reply');
    await wrapper.find('.reply-composer__draft-action').trigger('click');
    await nextTick();

    expect(mocks.saveReplyDraft).toHaveBeenCalledTimes(1);
    expect(textareas.every(textarea => (textarea.element as HTMLTextAreaElement).disabled)).toBe(true);
    expect(wrapper.findAll('.reply-composer__draft-action').every(action => action.attributes('disabled') !== undefined)).toBe(true);
    await wrapper.find('.reply-composer').trigger('submit');
    expect(mocks.replySubmissionStore.startOrRetry).not.toHaveBeenCalled();

    write.resolve();
    await flushAsync();
  });

  it('reports successful cleanup without presenting it as a Reply failure', async () => {
    mocks.replySubmissionStore.startOrRetry.mockResolvedValueOnce({
      status: 'resolved_previous', operationID: 'previous-operation',
    });
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('new reply');
    await wrapper.find('.reply-composer').trigger('submit');
    await flushAsync();

    expect(wrapper.find('.reply-notice').text()).toContain('previous reply was already posted');
    expect(wrapper.find('.reply-error').exists()).toBe(false);
    expect((wrapper.get('.reply-composer__textarea').element as HTMLTextAreaElement).value).toBe('new reply');
    expect(mocks.replySubmissionStore.startOrRetry).toHaveBeenCalledTimes(1);
  });

  it('registers beforeunload only for unsaved text and removes the warning after durable binding', async () => {
    wrapper = mountDetail();
    await flushAsync();
    const emptyEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(emptyEvent);
    expect(emptyEvent.defaultPrevented).toBe(false);

    const textarea = wrapper.get('.reply-composer__textarea');
    await textarea.setValue('unsaved');
    await nextTick();
    const dirtyEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);

    await wrapper.find('.reply-composer').trigger('submit');
    await flushAsync();
    const durableEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(durableEvent);
    expect(durableEvent.defaultPrevented).toBe(false);

    mocks.replySubmissionStore.operations[0].phase = 'failed';
    mocks.replySubmissionStore.operations[0].failureKind = 'retryable';
    await flushAsync();
    await textarea.setValue('edited after acceptance');
    await nextTick();
    const editedEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(editedEvent);
    expect(editedEvent.defaultPrevented).toBe(true);
  });

  it('cancels SPA leave without finishing recommendation read telemetry', async () => {
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('unsaved reply');

    const navigation = mocks.routeLeave({ name: 'Home' });
    await flushAsync();
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(true);
    await wrapper.get('.reply-draft-exit-dialog__button--cancel').trigger('click');

    expect(await navigation).toBe(false);
    expect(draftStore().getDraft(42)).toBe('unsaved reply');
    expect(mocks.telemetry.recordReadEnd).not.toHaveBeenCalled();
  });

  it('saves a reply before allowing leave and finishing read telemetry', async () => {
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('save before leaving');

    const navigation = mocks.routeLeave({ name: 'Home' });
    await flushAsync();
    await wrapper.get('.reply-draft-exit-dialog__button--save').trigger('click');
    expect(await navigation).toBe(true);
    expect(mocks.saveReplyDraft).toHaveBeenCalledWith(expect.objectContaining({ content: 'save before leaving' }));
    expect(mocks.telemetry.recordReadEnd).toHaveBeenCalledTimes(1);
  });

  it('waits for an already pending successful Save before leaving without a dialog', async () => {
    const write = deferred<void>();
    mocks.saveReplyDraft.mockReturnValueOnce(write.promise);
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('durable before leave');
    void wrapper.get('.reply-composer__draft-action').trigger('click');
    await nextTick();

    let navigationSettled = false;
    const navigation = mocks.routeLeave({ name: 'Home' }).then((allowed: boolean) => {
      navigationSettled = true;
      return allowed;
    });
    await flushAsync();
    expect(navigationSettled).toBe(false);
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(false);
    expect(mocks.telemetry.recordReadEnd).not.toHaveBeenCalled();

    write.resolve();
    await flushAsync();
    expect(await navigation).toBe(true);
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(false);
    expect(mocks.telemetry.recordReadEnd).toHaveBeenCalledTimes(1);
  });

  it('opens the exit dialog after a pending Save fails and exposes the failure', async () => {
    const write = deferred<void>();
    mocks.saveReplyDraft.mockReturnValueOnce(write.promise);
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('keep after failed save');
    void wrapper.get('.reply-composer__draft-action').trigger('click');
    await nextTick();

    let navigationSettled = false;
    const navigation = mocks.routeLeave({ name: 'Home' }).then((allowed: boolean) => {
      navigationSettled = true;
      return allowed;
    });
    await flushAsync();
    expect(navigationSettled).toBe(false);
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(false);

    write.reject(new Error('IndexedDB unavailable'));
    await flushAsync();
    expect(navigationSettled).toBe(false);
    expect(wrapper.get('.reply-draft-exit-dialog__error').text()).toContain('pending reply draft save failed');
    expect(draftStore().hasUnsavedChanges(42)).toBe(true);
    wrapper.get('.reply-draft-exit-dialog__button--cancel').trigger('click');
    expect(await navigation).toBe(false);
    expect(mocks.telemetry.recordReadEnd).not.toHaveBeenCalled();
  });

  it('does not let Discard settle navigation while a later Save is still pending', async () => {
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('discard only after saves settle');
    let navigationSettled = false;
    const navigation = mocks.routeLeave({ name: 'Home' }).then((allowed: boolean) => {
      navigationSettled = true;
      return allowed;
    });
    await flushAsync();
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(true);

    const write = deferred<void>();
    mocks.saveReplyDraft.mockReturnValueOnce(write.promise);
    const saving = draftStore().saveDraft(42);
    await wrapper.get('.reply-draft-exit-dialog__button--discard').trigger('click');
    expect(navigationSettled).toBe(false);
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(true);
    expect(wrapper.get('.reply-draft-exit-dialog__error').text()).toContain('Wait for the reply draft save');

    write.resolve();
    expect(await saving).toBe('saved');
    await wrapper.get('.reply-draft-exit-dialog__button--discard').trigger('click');
    expect(await navigation).toBe(true);
  });

  it('keeps navigation blocked and reply dirty if Save draft fails', async () => {
    mocks.saveReplyDraft.mockRejectedValueOnce(new Error('IndexedDB unavailable'));
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('preserve me');

    const navigation = mocks.routeLeave({ name: 'Home' });
    await flushAsync();
    await wrapper.get('.reply-draft-exit-dialog__button--save').trigger('click');
    await flushAsync();

    expect(wrapper.find('.reply-draft-exit-dialog__error').text()).toContain('Could not save this reply draft');
    expect(draftStore().getDraft(42)).toBe('preserve me');
    expect(draftStore().hasUnsavedChanges(42)).toBe(true);
    expect(mocks.telemetry.recordReadEnd).not.toHaveBeenCalled();
    wrapper.get('.reply-draft-exit-dialog__button--cancel').trigger('click');
    expect(await navigation).toBe(false);
  });

  it('keeps saved raw whitespace edits dirty even when canonical submission content stays bound', async () => {
    mocks.getReplyDraft.mockResolvedValue({
      key: '7:42', viewerID: 7, parentPostID: 42, content: 'hello', createdAt: 1, updatedAt: 2,
    });
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.find('.reply-composer').trigger('submit');
    await flushAsync();
    expect(draftStore().getBoundOperationID(42)).toBe('reply-operation');

    mocks.replySubmissionStore.operations[0].phase = 'failed';
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('  hello  ');

    expect(draftStore().getBoundOperationID(42)).toBe('reply-operation');
    expect(draftStore().hasUnsavedChanges(42)).toBe(true);
    expect(wrapper.get('.reply-composer__draft-action').text()).toBe('Save changes');
    const beforeUnload = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);

    const navigation = mocks.routeLeave({ name: 'Home' });
    await flushAsync();
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(true);
    await wrapper.get('.reply-draft-exit-dialog__button--cancel').trigger('click');
    expect(await navigation).toBe(false);
  });

  it('uses the same exit decision for parent changes but skips same-parent query changes', async () => {
    wrapper = mountDetail();
    await flushAsync();
    await wrapper.get('.reply-composer__textarea').setValue('route update draft');

    expect(await mocks.routeUpdate(
      { params: { id: '42' }, query: { reply: '1' } },
      { params: { id: '42' }, query: {} },
    )).toBe(true);
    const navigation = mocks.routeUpdate(
      { params: { id: '43' }, query: {} },
      { params: { id: '42' }, query: {} },
    );
    await flushAsync();
    expect(wrapper.find('.reply-draft-exit-dialog').exists()).toBe(true);
    await wrapper.get('.reply-draft-exit-dialog__button--discard').trigger('click');
    expect(await navigation).toBe(true);
    expect(draftStore().getDraft(42)).toBe('');
  });

  it('preserves an unresolved durable operation after the parent returns 404', async () => {
    mocks.getPostById.mockRejectedValueOnce({ response: { status: 404 } });
    wrapper = mountDetail();
    await flushAsync();
    expect(mocks.getReplyDraft).not.toHaveBeenCalled();
    expect(mocks.replySubmissionStore.startOrRetry).not.toHaveBeenCalled();
  });
});
