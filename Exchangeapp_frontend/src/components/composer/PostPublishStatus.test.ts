// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import PostPublishStatus from './PostPublishStatus.vue';

const mocks = vi.hoisted(() => ({
  store: null as any,
}));

vi.mock('../../store/postPublish', () => ({
  usePostPublishStore: () => mocks.store,
}));

const operationFor = (phase: string, id = 'publish-1') => ({
  id,
  phase,
});

describe('PostPublishStatus', () => {
  beforeEach(() => {
    mocks.store = reactive({
      latestOperation: null,
      retry: vi.fn(),
    });
  });

  it.each([
    ['uploading', 'Uploading post...'],
    ['publishing', 'Posting...'],
    ['succeeded', 'Post sent.'],
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
  });

  it('does not render without a publish operation', () => {
    const wrapper = mount(PostPublishStatus);

    expect(wrapper.find('[role="status"]').exists()).toBe(false);
  });
});
