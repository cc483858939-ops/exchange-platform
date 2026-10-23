// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import PostPublishStatus from './PostPublishStatus.vue';

const mocks = vi.hoisted(() => ({
  store: null as any,
  router: { push: vi.fn() },
}));

vi.mock('../../store/postPublish', () => ({
  usePostPublishStore: () => mocks.store,
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

const operationFor = (phase: string, id = 'publish-1') => ({
  id,
  phase,
  failureKind: phase === 'failed' ? 'retryable' : null,
  error: '',
});

describe('PostPublishStatus', () => {
  beforeEach(() => {
    mocks.store = reactive({
      latestOperation: null,
      currentSuccessNotice: null,
      recoveryError: '',
      retry: vi.fn(),
      recoverRejectedPostForEdit: vi.fn(),
      abandonFailedOperation: vi.fn(),
    });
    mocks.router.push.mockReset().mockResolvedValue(undefined);
  });

  it.each([
    ['uploading', 'Uploading post...'],
    ['publishing', 'Posting...'],
    ['succeeded', 'Post sent. Finishing cleanup...'],
  ])('renders the %s state', (phase, message) => {
    mocks.store.latestOperation = operationFor(phase);

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain(message);
    expect(wrapper.get('[role="status"]').attributes('aria-live')).toBe('polite');
    expect(wrapper.find('button').exists()).toBe(false);
  });

  it('renders an accessible retry action for an ambiguous failure', async () => {
    mocks.store.latestOperation = operationFor('failed', 'publish-42');

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain('Couldn’t confirm this post.');
    const retry = wrapper.get('button');
    expect(retry.text()).toBe('Retry');
    await retry.trigger('click');
    expect(mocks.store.retry).toHaveBeenCalledWith('publish-42');
    expect(wrapper.get('.post-publish-status__discard').text()).toBe('Discard attempt');
  });

  it('does not offer retry but allows discarding an idempotency conflict', () => {
    mocks.store.latestOperation = {
      ...operationFor('failed', 'publish-conflict'),
      failureKind: 'idempotency_conflict',
      error: 'This post can’t be retried safely.',
    };

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain('This post can’t be retried safely.');
    expect(wrapper.find('.post-publish-status__retry').exists()).toBe(false);
    expect(wrapper.get('.post-publish-status__discard').text()).toBe('Discard attempt');
  });

  it('saves a rejected post as a draft before navigating to edit it', async () => {
    mocks.store.latestOperation = {
      ...operationFor('failed', 'publish-validation'),
      failureKind: 'validation_error',
      error: 'Review the post and try again.',
    };
    mocks.store.recoverRejectedPostForEdit.mockResolvedValue('rejected-post-publish-validation');

    const wrapper = mount(PostPublishStatus);
    const edit = wrapper.get('.post-publish-status__discard');
    expect(edit.text()).toBe('Edit and try again');
    await edit.trigger('click');
    await flushPromises();

    expect(mocks.store.recoverRejectedPostForEdit).toHaveBeenCalledWith('publish-validation');
    expect(mocks.store.abandonFailedOperation).not.toHaveBeenCalled();
    expect(mocks.router.push).toHaveBeenCalledWith({
      name: 'PostCreate',
      query: { draft: 'rejected-post-publish-validation' },
    });
    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
  });

  it('keeps the failed operation visible when rejected-post recovery cannot persist', async () => {
    mocks.store.latestOperation = {
      ...operationFor('failed', 'publish-validation'),
      failureKind: 'validation_error',
    };
    mocks.store.recoverRejectedPostForEdit.mockResolvedValue(null);

    const wrapper = mount(PostPublishStatus);
    await wrapper.get('.post-publish-status__discard').trigger('click');
    await flushPromises();

    expect(mocks.store.latestOperation.id).toBe('publish-validation');
    expect(mocks.store.abandonFailedOperation).not.toHaveBeenCalled();
    expect(mocks.router.push).not.toHaveBeenCalled();
    expect(wrapper.get('.post-publish-status__error').text()).toContain('original attempt and its content are still preserved');
  });

  it('reports when router push resolves with a cancelled navigation failure', async () => {
    mocks.store.latestOperation = {
      ...operationFor('failed', 'publish-navigation-cancelled'),
      failureKind: 'validation_error',
      error: 'Review the post and try again.',
    };
    mocks.store.recoverRejectedPostForEdit.mockResolvedValue('rejected-post-navigation-cancelled');
    mocks.router.push.mockResolvedValueOnce({ type: 8 });

    const wrapper = mount(PostPublishStatus);
    await wrapper.get('.post-publish-status__discard').trigger('click');
    await flushPromises();

    expect(mocks.store.recoverRejectedPostForEdit).toHaveBeenCalledWith('publish-navigation-cancelled');
    expect(mocks.router.push).toHaveBeenCalledWith({
      name: 'PostCreate',
      query: { draft: 'rejected-post-navigation-cancelled' },
    });
    expect(wrapper.get('.post-publish-status__error').text())
      .toBe('The post is saved as a draft. Open Saved drafts to continue editing.');
    expect(mocks.store.abandonFailedOperation).not.toHaveBeenCalled();
  });

  it('requires confirmation before abandoning and preserves the operation on cancel', async () => {
    mocks.store.latestOperation = operationFor('failed', 'publish-43');

    const wrapper = mount(PostPublishStatus);
    await wrapper.get('.post-publish-status__discard').trigger('click');

    expect(wrapper.get('.confirm-dialog__title').text()).toBe('Discard this post attempt?');
    expect(wrapper.get('.confirm-dialog__description').text())
      .toContain('This attempt may already have been posted');
    expect(mocks.store.abandonFailedOperation).not.toHaveBeenCalled();

    await wrapper.get('.confirm-dialog__button--cancel').trigger('click');

    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
    expect(mocks.store.latestOperation.id).toBe('publish-43');
    expect(mocks.store.abandonFailedOperation).not.toHaveBeenCalled();
  });

  it('retires the operation only after durable abandon succeeds', async () => {
    mocks.store.latestOperation = operationFor('failed', 'publish-44');
    mocks.store.abandonFailedOperation.mockImplementation(async () => {
      mocks.store.latestOperation = null;
      return true;
    });

    const wrapper = mount(PostPublishStatus);
    await wrapper.get('.post-publish-status__discard').trigger('click');
    await wrapper.get('.confirm-dialog__button--confirm').trigger('click');

    expect(mocks.store.abandonFailedOperation).toHaveBeenCalledWith('publish-44');
    expect(wrapper.find('.confirm-dialog').exists()).toBe(false);
  });

  it('keeps the confirmation and operation visible when durable abandon fails', async () => {
    mocks.store.latestOperation = operationFor('failed', 'publish-45');
    mocks.store.abandonFailedOperation.mockResolvedValue(false);

    const wrapper = mount(PostPublishStatus);
    await wrapper.get('.post-publish-status__discard').trigger('click');
    await wrapper.get('.confirm-dialog__button--confirm').trigger('click');

    expect(mocks.store.latestOperation.id).toBe('publish-45');
    expect(wrapper.get('.confirm-dialog [role="alert"]').text())
      .toContain('Couldn’t discard this post attempt');
    expect(wrapper.find('.confirm-dialog').exists()).toBe(true);
  });

  it('shows a viewer recovery error without an operation', () => {
    mocks.store.recoveryError = 'Couldn’t restore the pending post from this device.';

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain('Couldn’t restore the pending post');
  });

  it('shows a resolved success notice after the full operation is retired', () => {
    mocks.store.currentSuccessNotice = {
      operationID: 'publish-success',
      publisherUserID: 7,
      postID: 101,
      completedAt: 1,
    };

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain('Post sent.');
    expect(wrapper.find('.post-publish-status__retry').exists()).toBe(false);
  });

  it('hides the success message when the viewer-scoped notice expires', async () => {
    mocks.store.currentSuccessNotice = {
      operationID: 'publish-success',
      publisherUserID: 7,
      postID: 101,
      completedAt: 1,
    };
    const wrapper = mount(PostPublishStatus);
    expect(wrapper.get('[role="status"]').text()).toContain('Post sent.');

    mocks.store.currentSuccessNotice = null;
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[role="status"]').exists()).toBe(false);
  });

  it('keeps a failed operation and its actions ahead of a success notice', () => {
    mocks.store.latestOperation = operationFor('failed', 'publish-failed');
    mocks.store.currentSuccessNotice = {
      operationID: 'publish-success',
      publisherUserID: 7,
      postID: 101,
      completedAt: 1,
    };

    const wrapper = mount(PostPublishStatus);

    expect(wrapper.get('[role="status"]').text()).toContain('Couldn’t confirm this post.');
    expect(wrapper.find('.post-publish-status__retry').exists()).toBe(true);
    expect(wrapper.find('.post-publish-status__discard').exists()).toBe(true);
  });

  it('does not render without a publish operation', () => {
    const wrapper = mount(PostPublishStatus);

    expect(wrapper.find('[role="status"]').exists()).toBe(false);
  });
});
