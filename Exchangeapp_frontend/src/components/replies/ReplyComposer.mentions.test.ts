// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, nextTick, ref } from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ReplyComposer from './ReplyComposer.vue';

const serviceMocks = vi.hoisted(() => ({
  getMentionSuggestions: vi.fn(),
}));

vi.mock('../../services/mentionService', () => ({
  getMentionSuggestions: serviceMocks.getMentionSuggestions,
  resolveMentionUsername: vi.fn(),
}));

vi.mock('emoji-picker-element', () => ({
  Picker: class extends HTMLElement {},
}));

const alice = {
  id: 12,
  username: 'alice',
  display_name: 'Alice',
  avatar_url: '',
  reason: 'following' as const,
};
const bob = { ...alice, id: 13, username: 'bobby', display_name: 'Bobby' };

const mountControlled = (initialValue: string) => {
  const value = ref(initialValue);
  const submissions: string[] = [];
  const wrapper = mount(defineComponent({
    setup() {
      return () => h(ReplyComposer, {
        modelValue: value.value,
        'onUpdate:modelValue': (next: string) => { value.value = next; },
        onSubmit: (content: string) => submissions.push(content),
      });
    },
  }), {
    attachTo: document.body,
    global: {
      stubs: {
        RouterLink: { props: ['to'], template: '<a tabindex="0" :data-to="JSON.stringify(to)"><slot /></a>' },
        AppIcon: true,
        UserAvatar: true,
        EmojiPickerPopover: true,
      },
    },
  });
  return { value, submissions, wrapper };
};

describe('ReplyComposer mentions', () => {
  afterEach(() => {
    vi.useRealTimers();
    serviceMocks.getMentionSuggestions.mockReset();
  });

  it('replaces a middle mention with the API username and preserves the draft', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    const controlled = mountControlled('🌱 hello @alX there\nnext line');
    const textarea = controlled.wrapper.get('textarea').element as HTMLTextAreaElement;
    const caret = textarea.value.indexOf('@alX') + 3;
    textarea.setSelectionRange(caret, caret);
    await controlled.wrapper.get('textarea').trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();

    expect(serviceMocks.getMentionSuggestions).toHaveBeenCalledWith('al', expect.any(AbortSignal));
    expect(controlled.wrapper.get('.mention-suggestions__search').attributes('data-to'))
      .toBe('{"name":"UserSearch","query":{"tab":"people","q":"al"}}');
    await controlled.wrapper.get('[role="option"]').trigger('click');
    await nextTick();

    expect(controlled.value.value).toBe('🌱 hello @alice there\nnext line');
    expect(textarea.selectionStart).toBe(textarea.value.indexOf('@alice') + '@alice'.length);
    expect(controlled.submissions).toEqual([]);
    expect(controlled.wrapper.get('.mention-suggestions').isVisible()).toBe(false);
    controlled.wrapper.unmount();
  });

  it('supports keyboard selection and does not select during IME composition', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice, bob]);
    const controlled = mountControlled('@al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(3, 3);
    await textarea.trigger('compositionstart');
    await textarea.trigger('keydown', { key: 'Enter', isComposing: true });
    expect(controlled.value.value).toBe('@al');
    expect(serviceMocks.getMentionSuggestions).not.toHaveBeenCalled();

    await textarea.trigger('compositionend');
    await nextTick();
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await textarea.trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    expect(controlled.wrapper.findAll('[role="option"]')[1]!.attributes('aria-selected')).toBe('true');
    await textarea.trigger('keydown', { key: 'Tab' });
    await nextTick();
    expect(controlled.value.value).toBe('@bobby');
    expect(controlled.submissions).toEqual([]);
    controlled.wrapper.unmount();
  });

  it('closes suggestions with Escape without changing the draft', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    const controlled = mountControlled('@a');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(2, 2);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await textarea.trigger('keydown', { key: 'Escape' });
    await nextTick();

    expect(controlled.value.value).toBe('@a');
    expect(controlled.wrapper.get('.mention-suggestions').isVisible()).toBe(false);
    controlled.wrapper.unmount();
  });

  it('confirms the active candidate with Enter after ArrowUp navigation', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice, bob]);
    const controlled = mountControlled('@al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(3, 3);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();

    await textarea.trigger('keydown', { key: 'ArrowUp' });
    await nextTick();
    expect(controlled.wrapper.findAll('[role="option"]')[1]!.attributes('aria-selected')).toBe('true');
    await textarea.trigger('keydown', { key: 'Enter' });
    await nextTick();

    expect(controlled.value.value).toBe('@bobby');
    expect(controlled.submissions).toEqual([]);
    controlled.wrapper.unmount();
  });

  it('keeps the search-more route keyboard reachable', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    const controlled = mountControlled('@a');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(2, 2);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();

    await textarea.trigger('keydown', { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(controlled.wrapper.get('.mention-suggestions__search').element);
    controlled.wrapper.unmount();
  });

  it('shows an explicit error instead of silently sending more than five usernames', async () => {
    const names = ['alice', 'bob', 'carol', 'dana', 'erin', 'frank'];
    const controlled = mountControlled(names.map(name => `@${name}`).join(' '));
    await controlled.wrapper.get('form').trigger('submit');
    expect(controlled.submissions).toEqual([]);
    expect(controlled.wrapper.text()).toContain('A post can mention at most 5 different users.');
    controlled.wrapper.unmount();
  });
});
