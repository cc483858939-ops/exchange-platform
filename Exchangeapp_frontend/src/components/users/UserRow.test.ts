// @vitest-environment jsdom

import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import UserRow from './UserRow.vue';

const item = {
  user: {
    id: 7,
    username: 'alice',
    display_name: 'Alice Smith',
    avatar_url: 'https://example.test/alice.jpg',
    bio: 'Exchange reader',
    created_at: '2026-08-21T00:00:00.000Z',
  },
  following: false,
};

const mountRow = (overrides: Record<string, unknown> = {}) => mount(UserRow, {
  props: {
    item: { ...item, ...overrides },
    pending: false,
    isSelf: false,
  },
  global: {
    stubs: {
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('UserRow avatar', () => {
  it('uses the shared avatar component for the connection identity', () => {
    const wrapper = mountRow();

    expect(wrapper.get('.user-row__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
    expect(wrapper.get('.user-row__avatar').attributes('aria-hidden')).toBe('true');
  });

  it('keeps a Unicode-safe fallback when the avatar fails', async () => {
    const wrapper = mountRow({
      user: {
        ...item.user,
        display_name: ' 你好吗',
        avatar_url: '/broken.jpg',
      },
    });

    await wrapper.get('.user-row__avatar .user-avatar__image').trigger('error');

    expect(wrapper.find('.user-row__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.user-row__avatar .user-avatar__fallback').text()).toBe('你');
  });
});
