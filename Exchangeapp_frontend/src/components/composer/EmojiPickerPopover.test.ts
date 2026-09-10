// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import EmojiPickerPopover from './EmojiPickerPopover.vue';

vi.mock('emoji-picker-element', () => {
  class Picker extends HTMLElement {
    constructor() {
      super();
    }
  }
  customElements.define('emoji-picker', Picker);
  return { Picker };
});

describe('EmojiPickerPopover', () => {
  let wrapper: VueWrapper | null = null;
  let anchor: HTMLButtonElement | null = null;

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    anchor?.remove();
    anchor = null;
    document.querySelectorAll('.emoji-picker-popover__panel').forEach(element => element.remove());
  });

  const mountPicker = (open = true) => {
    anchor = document.createElement('button');
    document.body.append(anchor);
    vi.spyOn(anchor, 'getBoundingClientRect').mockReturnValue({
      x: 40,
      y: 96,
      top: 96,
      bottom: 132,
      left: 40,
      right: 76,
      width: 36,
      height: 36,
      toJSON: () => ({}),
    } as DOMRect);
    wrapper = mount(EmojiPickerPopover, {
      attachTo: document.body,
      props: { id: 'test-emoji-picker', open, anchorEl: anchor },
    });
    return wrapper;
  };

  it('lazily mounts the picker and forwards selected emoji without closing', async () => {
    mountPicker();
    await flushPromises();

    const pickerMount = document.querySelector('.emoji-picker-popover__mount');
    const picker = pickerMount?.firstElementChild as HTMLElement | undefined;
    expect(picker).toBeDefined();
    picker?.dispatchEvent(new CustomEvent('emoji-click', {
      detail: { unicode: '😂' },
      bubbles: true,
      composed: true,
    }));

    expect(wrapper?.emitted('select')).toEqual([['😂']]);
    expect(wrapper?.emitted('close')).toBeUndefined();
    expect(document.getElementById('test-emoji-picker')).not.toBeNull();
  });

  it('closes on outside interaction while ignoring picker interaction', async () => {
    mountPicker();
    await flushPromises();
    const picker = document.querySelector('.emoji-picker-popover__mount')
      ?.firstElementChild as HTMLElement | undefined;
    picker?.dispatchEvent(new Event('pointerdown', { bubbles: true, composed: true }));
    expect(wrapper?.emitted('close')).toBeUndefined();

    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    expect(wrapper?.emitted('close')).toEqual([['outside']]);
  });

  it('closes on Escape and keeps the panel anchored inside the viewport', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 768 });
    mountPicker();
    if (anchor) {
      vi.spyOn(anchor, 'getBoundingClientRect').mockReturnValue({
        x: 950,
        y: 700,
        top: 700,
        bottom: 736,
        left: 950,
        right: 986,
        width: 36,
        height: 36,
        toJSON: () => ({}),
      } as DOMRect);
    }
    await flushPromises();
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));

    const panel = document.getElementById('test-emoji-picker');
    expect(panel?.style.left).toBe('632px');
    expect(panel?.style.top).toBe('272px');

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(wrapper?.emitted('close')).toEqual([['escape']]);
  });

  it('removes the teleported panel when closed', async () => {
    mountPicker();
    await flushPromises();
    await wrapper?.setProps({ open: false });

    expect(document.getElementById('test-emoji-picker')).toBeNull();
    expect(document.querySelector('.emoji-picker-popover__mount')).toBeNull();
  });
});
