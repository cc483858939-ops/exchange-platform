// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import AuthRequiredState from './AuthRequiredState.vue';

const RouterLinkStub = {
  props: ['to'],
  template: '<a class="router-link-stub"><slot /></a>',
};

describe('AuthRequiredState', () => {
  it('renders an accessible login CTA with the exact deep-link return target', () => {
    const wrapper = mount(AuthRequiredState, {
      props: {
        title: 'Log in to view followers.',
        description: "Sign in to view this user's connections.",
        returnTo: '/users/7/followers?source=share#top',
      },
      global: {
        stubs: {
          RouterLink: RouterLinkStub,
        },
      },
    });

    expect(wrapper.get('h1').text()).toBe('Log in to view followers.');
    expect(wrapper.get('p').text()).toBe("Sign in to view this user's connections.");
    expect(wrapper.get('.auth-required-state').attributes('aria-live')).toBe('polite');
    expect(wrapper.get('.auth-required-state').attributes('role')).toBeUndefined();
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({
      name: 'Login',
      query: {
        returnTo: '/users/7/followers?source=share#top',
      },
    });
  });
});
