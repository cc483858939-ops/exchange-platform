// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import MobileAccountMenu from './MobileAccountMenu.vue';

const mocks = vi.hoisted(() => ({
  handleLogout: vi.fn(),
}));

vi.mock('../../composables/useLogout', () => ({
  useLogout: () => ({ handleLogout: mocks.handleLogout }),
}));

const mountMenu = () => mount(MobileAccountMenu, {
  global: {
    stubs: {
      RouterLink: { template: '<a><slot /></a>' },
      AppIcon: { props: ['name'], template: '<span class="test-icon" :data-icon="name" />' },
    },
  },
});

describe('MobileAccountMenu', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('starts closed and opens with History and Log out actions', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(trigger.attributes('aria-expanded')).toBe('false');

    await trigger.trigger('click');
    expect(wrapper.find('[role="menu"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('History');
    expect(wrapper.text()).toContain('Log out');
    expect(trigger.attributes('aria-expanded')).toBe('true');
  });

  it('closes on Escape and outside pointerdown', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');

    await trigger.trigger('click');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await nextTick();
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);

    await trigger.trigger('click');
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
  });

  it('delegates logout to useLogout exactly once', async () => {
    const wrapper = mountMenu();
    await wrapper.find('.mobile-account-menu__trigger').trigger('click');
    await wrapper.find('.mobile-account-menu__item--logout').trigger('click');

    expect(mocks.handleLogout).toHaveBeenCalledTimes(1);
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
  });
});
