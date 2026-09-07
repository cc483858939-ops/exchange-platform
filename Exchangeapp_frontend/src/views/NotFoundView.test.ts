// @vitest-environment jsdom

import { defineComponent } from 'vue';
import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import NotFoundView from './NotFoundView.vue';

const RouterLinkStub = defineComponent({
  name: 'RouterLinkStub',
  props: {
    to: {
      type: [String, Object],
      required: true,
    },
  },
  template: '<a data-testid="home-link"><slot /></a>',
});

describe('NotFoundView', () => {
  it('renders the not-found message and links back to Home', () => {
    const wrapper = mount(NotFoundView, {
      global: {
        stubs: {
          RouterLink: RouterLinkStub,
        },
      },
    });

    expect(wrapper.find('section[aria-labelledby="not-found-title"]').exists()).toBe(true);
    expect(wrapper.get('.not-found__code').text()).toBe('404');
    expect(wrapper.get('#not-found-title').text()).toBe('Page not found');
    expect(wrapper.get('.not-found__description').text()).toBe(
      'The page you’re looking for doesn’t exist or may have moved.',
    );
    expect(wrapper.get('[data-testid="home-link"]').text()).toBe('Go to Home');
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({ name: 'Home' });
  });
});
