// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import RepostAction from './RepostAction.vue';
import RepostMenu from './RepostMenu.vue';

const mountMenu = (props: Partial<{
  reposted: boolean;
  count: number;
  loading: boolean;
  pending: boolean;
  disabled: boolean;
  quoteCount: number;
  ariaLabel: string;
  variant: 'compact' | 'detail';
}> = {}) => mount(RepostMenu, {
  attachTo: document.body,
  props: {
    reposted: false,
    count: 8,
    ariaLabel: 'Repost post, 8 reposts',
    ...props,
  },
});

const disclosure = (wrapper: ReturnType<typeof mount>) => wrapper.get('.repost-menu__disclosure');

afterEach(() => {
  vi.restoreAllMocks();
  document.querySelectorAll('.repost-menu').forEach(element => element.remove());
});

describe('RepostMenu', () => {
  it('forwards every repost state prop unchanged to RepostAction', () => {
    const props = {
      reposted: true,
      count: 19,
      loading: true,
      pending: false,
      disabled: true,
      ariaLabel: 'Undo repost, 19 reposts',
      variant: 'detail' as const,
    };
    const wrapper = mountMenu(props);

    expect(wrapper.findComponent(RepostAction).props()).toMatchObject(props);
  });

  it('keeps the primary action one-click and separate from the menu', async () => {
    const wrapper = mountMenu();

    await wrapper.get('.repost-action').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(wrapper.emitted('quote')).toBeUndefined();
  });

  it('opens from the disclosure without mutating either action', async () => {
    const wrapper = mountMenu();

    await disclosure(wrapper).trigger('click');

    expect(wrapper.find('[role="menu"]').exists()).toBe(true);
    expect(disclosure(wrapper).attributes()).toMatchObject({
      'aria-haspopup': 'menu',
      'aria-expanded': 'true',
      'aria-label': 'More repost options',
    });
    expect(wrapper.find('.repost-menu__disclosure svg path').exists()).toBe(true);
    expect(wrapper.emitted('toggle')).toBeUndefined();
    expect(wrapper.emitted('quote')).toBeUndefined();
  });

  it('closes before emitting Quote and does not toggle repost', async () => {
    const wrapper = mountMenu();
    await disclosure(wrapper).trigger('click');
    await wrapper.get('[aria-label="Quote post, no existing quotes"]').trigger('click');

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(wrapper.emitted('quote')).toEqual([[]]);
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('shows the Quote count with an accessible action label', async () => {
    const wrapper = mountMenu({ quoteCount: 12 });
    await disclosure(wrapper).trigger('click');
    const quoteItem = wrapper.get('[aria-label="Quote post, 12 existing quotes"]');

    expect(quoteItem.text()).toBe('Quote post12');
    expect(quoteItem.attributes('aria-label')).toBe('Quote post, 12 existing quotes');
    expect(quoteItem.get('.repost-menu__item-count').attributes('aria-hidden')).toBe('true');
    await quoteItem.trigger('click');
    expect(wrapper.emitted('quote')).toEqual([[]]);
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('keeps zero visible and labels the Quote action without existing quotes', async () => {
    const wrapper = mountMenu({ quoteCount: 0 });
    await disclosure(wrapper).trigger('click');
    const quoteItem = wrapper.get('[aria-label="Quote post, no existing quotes"]');

    expect(quoteItem.text()).toBe('Quote post0');
    expect(quoteItem.attributes('aria-label')).toBe('Quote post, no existing quotes');
    expect(wrapper.find('[aria-label="View quotes"]').exists()).toBe(false);
  });

  it('shows View quotes for a positive count and emits only viewQuotes', async () => {
    const wrapper = mountMenu({ quoteCount: 12 });
    await disclosure(wrapper).trigger('click');
    const item = wrapper.get('[aria-label="View quotes"]');

    expect(item.text()).toBe('View quotes');
    await item.trigger('click');

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(wrapper.emitted('viewQuotes')).toEqual([[]]);
    expect(wrapper.emitted('quote')).toBeUndefined();
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('routes the menu Repost command through RepostAction activation', async () => {
    const wrapper = mountMenu();
    await disclosure(wrapper).trigger('click');
    await wrapper.get('[role="menuitem"]').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
    expect(wrapper.findComponent(RepostAction).classes()).toContain('repost-action--reposting');
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
  });

  it('uses Undo repost when already reposted', async () => {
    const wrapper = mountMenu({ reposted: true });
    await disclosure(wrapper).trigger('click');

    expect(wrapper.get('[role="menuitem"]').text()).toBe('Undo repost');
  });

  it.each([
    { pending: true },
    { loading: true },
    { disabled: true },
  ])('keeps Quote available while Repost is disabled: %o', async state => {
    const wrapper = mountMenu(state);
    await disclosure(wrapper).trigger('click');
    const [repostItem, quoteItem] = wrapper.findAll('[role="menuitem"]');

    expect(repostItem?.attributes('disabled')).toBeDefined();
    expect(quoteItem?.attributes('disabled')).toBeUndefined();
    await repostItem?.trigger('click');
    expect(wrapper.emitted('toggle')).toBeUndefined();
    await quoteItem?.trigger('click');
    expect(wrapper.emitted('quote')).toEqual([[]]);
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it.each([
    { pending: true },
    { loading: true },
    { disabled: true },
  ])('keeps View quotes available while Repost is disabled: %o', async state => {
    const wrapper = mountMenu({ ...state, quoteCount: 12 });
    await disclosure(wrapper).trigger('click');
    const item = wrapper.get('[aria-label="View quotes"]');

    expect(item.attributes('disabled')).toBeUndefined();
    await item.trigger('click');
    expect(wrapper.emitted('viewQuotes')).toEqual([[]]);
    expect(wrapper.emitted('quote')).toBeUndefined();
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('supports Escape and restores focus to the disclosure', async () => {
    const wrapper = mountMenu();
    (disclosure(wrapper).element as HTMLButtonElement).focus();
    await disclosure(wrapper).trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    expect(document.activeElement).toBe(wrapper.get('[role="menuitem"]').element);

    await wrapper.get('[role="menuitem"]').trigger('keydown', { key: 'Escape' });
    await nextTick();

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(document.activeElement).toBe(disclosure(wrapper).element as HTMLButtonElement);
  });

  it('supports ArrowDown, ArrowUp, Home, and End menu navigation', async () => {
    const wrapper = mountMenu({ quoteCount: 12 });
    (disclosure(wrapper).element as HTMLButtonElement).focus();
    await disclosure(wrapper).trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    const items = wrapper.findAll('[role="menuitem"]');
    expect(items).toHaveLength(3);

    expect(document.activeElement).toBe(items[0]!.element as HTMLButtonElement);
    await items[0]!.trigger('keydown', { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1]!.element as HTMLButtonElement);
    await items[1]!.trigger('keydown', { key: 'ArrowUp' });
    expect(document.activeElement).toBe(items[0]!.element as HTMLButtonElement);
    await items[1]!.trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(items[0]!.element as HTMLButtonElement);
    await items[0]!.trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(items[2]!.element as HTMLButtonElement);
    await items[2]!.trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(items[0]!.element as HTMLButtonElement);
  });

  it('closes on outside pointer interaction', async () => {
    const wrapper = mountMenu();
    const outside = document.createElement('button');
    document.body.append(outside);
    await disclosure(wrapper).trigger('click');

    outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();

    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    outside.remove();
  });

  it('removes the outside listener when unmounted while open', async () => {
    const addListener = vi.spyOn(document, 'addEventListener');
    const removeListener = vi.spyOn(document, 'removeEventListener');
    const wrapper = mountMenu();
    await disclosure(wrapper).trigger('click');

    const listener = addListener.mock.calls.find(([type]) => type === 'pointerdown')?.[1];
    expect(listener).toBeTypeOf('function');
    wrapper.unmount();

    expect(removeListener).toHaveBeenCalledWith('pointerdown', listener);
    expect(() => document.dispatchEvent(new Event('pointerdown'))).not.toThrow();
  });
});
