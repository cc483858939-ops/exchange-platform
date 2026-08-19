// @vitest-environment jsdom

import { nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import AuthorIdentity from './AuthorIdentity.vue';

const author = {
  id: 7,
  username: 'reader',
  display_name: 'Reader',
  avatar_url: '',
};

describe('AuthorIdentity time display', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 7, 19, 12, 0, 0));
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.useRealTimers();
  });

  it('updates the displayed relative time when the shared clock ticks', async () => {
    const createdAt = new Date(2026, 7, 19, 11, 0, 15).toISOString();

    wrapper = mount(AuthorIdentity, {
      props: { author, createdAt },
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    });

    await nextTick();
    expect(wrapper.find('.author-meta').text()).toBe('@reader · 59m');

    vi.advanceTimersByTime(30_000);
    await nextTick();

    expect(wrapper.find('.author-meta').text()).toBe('@reader · 1h');
  });
});
