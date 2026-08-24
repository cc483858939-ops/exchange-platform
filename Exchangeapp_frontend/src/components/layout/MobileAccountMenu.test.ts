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
  attachTo: document.body,
  global: {
    stubs: {
      RouterLink: {
        props: ['to'],
        template: '<a :data-route-name="to && to.name"><slot /></a>',
      },
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

  it('keeps the menu open while focus moves between menu items', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');

    await trigger.trigger('click');
    const history = wrapper.find('[data-route-name="History"]');
    const logout = wrapper.find('.mobile-account-menu__item--logout');

    trigger.element.dispatchEvent(new FocusEvent('focusout', {
      bubbles: true,
      relatedTarget: history.element,
    }));
    await nextTick();
    expect(wrapper.find('[role="menu"]').exists()).toBe(true);

    history.element.dispatchEvent(new FocusEvent('focusout', {
      bubbles: true,
      relatedTarget: logout.element,
    }));
    await nextTick();
    expect(wrapper.find('[role="menu"]').exists()).toBe(true);
  });

  it('closes when focus leaves the menu', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');
    const outside = document.createElement('button');
    document.body.appendChild(outside);

    await trigger.trigger('click');
    const logout = wrapper.find('.mobile-account-menu__item--logout');
    logout.element.dispatchEvent(new FocusEvent('focusout', {
      bubbles: true,
      relatedTarget: outside,
    }));
    await nextTick();

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
  });

  it('does not close from the document Tab keydown alone', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');

    await trigger.trigger('click');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    await nextTick();

    expect(wrapper.find('[role="menu"]').exists()).toBe(true);
  });

  it('closes on Escape and restores focus, then closes on outside pointerdown', async () => {
    const wrapper = mountMenu();
    const trigger = wrapper.find('.mobile-account-menu__trigger');

    await trigger.trigger('click');
    const triggerElement = trigger.element as HTMLButtonElement;
    triggerElement.focus();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await nextTick();
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(document.activeElement).toBe(triggerElement);

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
