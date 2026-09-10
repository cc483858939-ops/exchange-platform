// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { flushPromises } from '@vue/test-utils';
import { defineComponent, h, nextTick, ref } from 'vue';
import { vi } from 'vitest';
import ReplyComposer from './ReplyComposer.vue';
import type { PublicAuthor } from '../../types/User';

vi.mock('emoji-picker-element', () => {
  class Picker extends HTMLElement {
    constructor() {
      super();
    }
  }
  customElements.define('emoji-picker', Picker);
  return { Picker };
});

const author = (overrides: Partial<PublicAuthor> = {}): PublicAuthor => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  avatar_url: 'https://example.test/alice.jpg',
  ...overrides,
});

const mountControlledReply = (initialValue: string) => {
  const modelValue = ref(initialValue);
  const Host = defineComponent({
    setup() {
      return () => h(ReplyComposer, {
        author: author(),
        modelValue: modelValue.value,
        'onUpdate:modelValue': (value: string) => {
          modelValue.value = value;
        },
      });
    },
  });

  return { modelValue, wrapper: mount(Host, { attachTo: document.body }) };
};

describe('ReplyComposer avatar and reply behavior', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('renders the real avatar when the profile provides an avatar URL', () => {
    wrapper = mount(ReplyComposer, { props: { author: author() } });

    expect(wrapper.get('.reply-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
  });

  it('uses the display-name initial when the avatar is missing', () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author({ avatar_url: '', display_name: 'Universal' }) },
    });

    expect(wrapper.find('.reply-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.reply-composer__avatar .user-avatar__fallback').text()).toBe('U');
  });

  it('uses the username initial when the display name is empty', () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author({ avatar_url: '', display_name: '', username: '12345678' }) },
    });

    expect(wrapper.find('.reply-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.reply-composer__avatar .user-avatar__fallback').text()).toBe('1');
  });

  it('replaces a broken avatar image with the initial fallback', async () => {
    wrapper = mount(ReplyComposer, { props: { author: author() } });

    await wrapper.get('.reply-composer__avatar .user-avatar__image').trigger('error');

    expect(wrapper.find('.reply-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.reply-composer__avatar .user-avatar__fallback').text()).toBe('A');
  });

  it('resets the avatar failure when the avatar URL changes', async () => {
    wrapper = mount(ReplyComposer, { props: { author: author() } });
    await wrapper.get('.reply-composer__avatar .user-avatar__image').trigger('error');

    await wrapper.setProps({
      author: author({ avatar_url: 'https://example.test/alice-new.jpg' }),
    });

    expect(wrapper.get('.reply-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice-new.jpg');
  });

  it('renders the controlled model value', () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author(), modelValue: 'restored draft' },
    });

    expect(wrapper.get('textarea').element.value).toBe('restored draft');
  });

  it('emits model updates when the user edits the textarea', async () => {
    wrapper = mount(ReplyComposer, { props: { author: author(), modelValue: '' } });

    await wrapper.get('textarea').setValue('hello');

    expect(wrapper.emitted('update:modelValue')).toEqual([['hello']]);
  });

  it('opens the emoji picker with an accessible toggle', async () => {
    wrapper = mount(ReplyComposer, { props: { author: author() } });

    const button = wrapper.get('.reply-composer__emoji');
    expect(button.attributes('aria-label')).toBe('Add emoji');
    expect(button.attributes('title')).toBe('Add emoji');
    expect(button.attributes('aria-haspopup')).toBe('dialog');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(button.attributes('aria-controls')).toBe('reply-emoji-picker');

    await button.trigger('click');
    await flushPromises();

    expect(button.attributes('aria-expanded')).toBe('true');
    expect(document.getElementById('reply-emoji-picker')).not.toBeNull();
  });

  it('inserts an emoji at the saved caret and keeps the picker open', async () => {
    const controlled = mountControlledReply('hello world');
    wrapper = controlled.wrapper;
    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement;

    textarea.setSelectionRange(6, 6);
    await wrapper.get('textarea').trigger('select');
    await wrapper.get('.reply-composer__emoji').trigger('click');
    await flushPromises();

    const pickerMount = document.querySelector('.emoji-picker-popover__mount');
    expect(pickerMount).not.toBeNull();
    const picker = pickerMount!.firstElementChild as HTMLElement;
    picker.dispatchEvent(new CustomEvent('emoji-click', {
      detail: { unicode: '😂' },
      bubbles: true,
      composed: true,
    }));
    await flushPromises();

    expect(textarea.value).toBe('hello 😂world');
    expect(controlled.modelValue.value).toBe('hello 😂world');
    expect(document.activeElement).toBe(textarea);
    expect(textarea.selectionStart).toBe(8);
    expect(textarea.selectionEnd).toBe(8);
    expect(wrapper.get('.reply-composer__emoji').attributes('aria-expanded')).toBe('true');
  });

  it('replaces a selection and inserts a second emoji at the new caret', async () => {
    const controlled = mountControlledReply('hello bad world');
    wrapper = controlled.wrapper;
    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement;

    textarea.setSelectionRange(6, 9);
    await wrapper.get('textarea').trigger('select');
    await wrapper.get('.reply-composer__emoji').trigger('click');
    await flushPromises();
    const pickerMount = document.querySelector('.emoji-picker-popover__mount');
    expect(pickerMount).not.toBeNull();
    const picker = pickerMount!.firstElementChild as HTMLElement;

    picker.dispatchEvent(new CustomEvent('emoji-click', {
      detail: { unicode: '❤️' },
      bubbles: true,
      composed: true,
    }));
    await flushPromises();
    expect(controlled.modelValue.value).toBe('hello ❤️ world');
    picker.dispatchEvent(new CustomEvent('emoji-click', {
      detail: { unicode: '🔥' },
      bubbles: true,
      composed: true,
    }));
    await flushPromises();

    expect(textarea.value).toBe('hello ❤️🔥 world');
    expect(controlled.modelValue.value).toBe('hello ❤️🔥 world');
    expect(textarea.selectionStart).toBe('hello ❤️🔥'.length);
    expect(textarea.selectionEnd).toBe('hello ❤️🔥'.length);
  });

  it('does not open the picker while disabled or submitting', async () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author(), disabled: true },
    });

    const button = wrapper.get('.reply-composer__emoji');
    expect(button.attributes('disabled')).toBeDefined();
    await button.trigger('click');
    expect(document.getElementById('reply-emoji-picker')).toBeNull();

    await wrapper.setProps({ disabled: false, submitting: true });
    expect(button.attributes('disabled')).toBeDefined();
    await button.trigger('click');
    expect(document.getElementById('reply-emoji-picker')).toBeNull();
  });

  it('resizes after an external multiline draft restore', async () => {
    wrapper = mount(ReplyComposer, { props: { author: author(), modelValue: '' } });
    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement;
    Object.defineProperty(textarea, 'scrollHeight', {
      configurable: true,
      value: 84,
    });

    await wrapper.setProps({ modelValue: 'line 1\nline 2' });
    await nextTick();

    expect(textarea.style.height).toBe('84px');
  });

  it('accepts reply content and emits the trimmed value', async () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author(), modelValue: '  useful reply  ' },
    });

    expect(wrapper.get('textarea').attributes('placeholder')).toBe('Post your reply...');
    expect(wrapper.find('.reply-composer__hint').exists()).toBe(false);

    await wrapper.get('form').trigger('submit');

    expect(wrapper.emitted('submit')).toEqual([['useful reply']]);
  });

  it('keeps the 1000-character validation while presenting a reply row', async () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author(), modelValue: 'a'.repeat(1001) },
    });

    expect(wrapper.get('.reply-composer__validation').text())
      .toBe('1001/1000 characters. Please shorten your reply.');
    expect(wrapper.get('.reply-composer__submit').attributes('disabled')).toBeDefined();
  });

  it('keeps clear as an exposed controlled-input command', async () => {
    wrapper = mount(ReplyComposer, {
      props: { author: author(), modelValue: 'draft' },
    });

    (wrapper.vm as unknown as { clear: () => void }).clear();
    await wrapper.setProps({ modelValue: '' });

    expect(wrapper.emitted('update:modelValue')).toEqual([['']]);
    expect(wrapper.get('textarea').element.value).toBe('');
  });
});
