// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import Register from './Register.vue';

const mocks = vi.hoisted(() => ({
  authStore: {
    register: vi.fn(),
  },
  router: {
    push: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

describe('Register branding', () => {
  it('renders the Exchange brand consistently', () => {
    const wrapper = mount(Register, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    });

    expect(wrapper.get('.auth-brand__mobile-mark').text()).toBe('EX');
    expect(wrapper.get('.auth-brand__name').text()).toBe('Exchange');
    expect(wrapper.get('.auth-visual__mark').text()).toBe('EX');
    expect(wrapper.text()).not.toContain('GX');
    expect(wrapper.text()).not.toContain('Go Exchange');
  });
});
