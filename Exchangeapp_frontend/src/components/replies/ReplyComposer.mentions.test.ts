// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, nextTick, ref } from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ReplyComposer from './ReplyComposer.vue';

const serviceMocks = vi.hoisted(() => ({
  getMentionSuggestions: vi.fn(),
  searchUsers: vi.fn(),
}));

vi.mock('../../services/mentionService', () => ({
  getMentionSuggestions: serviceMocks.getMentionSuggestions,
  resolveMentionUsername: vi.fn(),
}));

vi.mock('../../services/userService', () => ({
  searchUsers: serviceMocks.searchUsers,
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
  const editorIdentity = ref('viewer-7:post-42');
  const submissions: string[] = [];
  const wrapper = mount(defineComponent({
    setup() {
      return () => h(ReplyComposer, {
        modelValue: value.value,
        editorIdentity: editorIdentity.value,
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
  return {
    value,
    submissions,
    wrapper,
    setEditorIdentity: (next: string) => { editorIdentity.value = next; },
  };
};

describe('ReplyComposer mentions', () => {
  afterEach(() => {
    vi.useRealTimers();
    serviceMocks.getMentionSuggestions.mockReset();
    serviceMocks.searchUsers.mockReset();
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
    expect(controlled.wrapper.get('.mention-suggestions__search').text()).toBe('Search more users');
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

  it('submits more than five username tokens for server-side validation', async () => {
    const names = ['alice', 'bob', 'carol', 'dana', 'erin', 'frank'];
    const controlled = mountControlled(names.map(name => `@${name}`).join(' '));
    await controlled.wrapper.get('form').trigger('submit');
    expect(controlled.submissions).toEqual([names.map(name => `@${name}`).join(' ')]);
    controlled.wrapper.unmount();
  });

  it('searches all users on explicit submit and inserts the returned username at the original caret', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    serviceMocks.searchUsers.mockResolvedValue({
      items: [{ user: { id: 31, username: 'alina', display_name: 'Alina Chen', avatar_url: '' }, following: false }],
      has_more: false,
    });
    const controlled = mountControlled('🌱 hello @alX there\nnext line');
    const textarea = controlled.wrapper.get('textarea');
    const caret = (textarea.element as HTMLTextAreaElement).value.indexOf('@alX') + 3;
    (textarea.element as HTMLTextAreaElement).setSelectionRange(caret, caret);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();

    expect(serviceMocks.searchUsers).not.toHaveBeenCalled();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    const query = controlled.wrapper.get('input[type="search"]');
    await query.setValue('alina');
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');
    await flushPromises();

    expect(serviceMocks.searchUsers).toHaveBeenCalledWith(
      { q: 'alina', limit: 8 },
      expect.any(AbortSignal),
    );
    expect(controlled.wrapper.get('[role="option"]').text()).toContain('@alina');
    await controlled.wrapper.get('[role="option"]').trigger('click');
    await nextTick();

    expect(controlled.value.value).toBe('🌱 hello @alina there\nnext line');
    expect((textarea.element as HTMLTextAreaElement).selectionStart)
      .toBe('🌱 hello @alina'.length);
    expect(controlled.submissions).toEqual([]);
    controlled.wrapper.unmount();
  });

  it('returns from global search without changing the draft or searching in default mode', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    const controlled = mountControlled('Draft @al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(9, 9);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    await controlled.wrapper.get('input[type="search"]').setValue('alice');
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    await nextTick();

    expect(controlled.value.value).toBe('Draft @al');
    expect(controlled.wrapper.find('input[type="search"]').exists()).toBe(false);
    expect(serviceMocks.searchUsers).not.toHaveBeenCalled();
    expect(controlled.wrapper.get('.mention-suggestions__search').text()).toBe('Search more users');
    controlled.wrapper.unmount();
  });

  it('does not insert a search result after the editor text changes', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    serviceMocks.searchUsers.mockResolvedValue({
      items: [{ user: { id: 31, username: 'alina', display_name: 'Alina', avatar_url: '' }, following: false }],
      has_more: false,
    });
    const controlled = mountControlled('Draft @al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(9, 9);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');
    await flushPromises();

    await textarea.setValue('Changed draft @x');
    await nextTick();
    expect(controlled.wrapper.text()).toContain('The draft changed.');
    expect(controlled.wrapper.findAll('[role="option"]')).toHaveLength(0);
    expect(controlled.value.value).toBe('Changed draft @x');
    controlled.wrapper.unmount();
  });

  it('rejects search results after the editor identity changes', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    serviceMocks.searchUsers.mockResolvedValue({
      items: [{ user: { id: 31, username: 'alina', display_name: 'Alina', avatar_url: '' }, following: false }],
      has_more: false,
    });
    const controlled = mountControlled('Draft @al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(9, 9);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');
    await flushPromises();

    controlled.setEditorIdentity('another-viewer:42');
    await nextTick();

    expect(controlled.wrapper.text()).toContain('The draft changed.');
    expect(controlled.wrapper.findAll('[role="option"]')).toHaveLength(0);
    expect(controlled.value.value).toBe('Draft @al');
    controlled.wrapper.unmount();
  });

  it('aborts stale global search requests when a newer query is submitted', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    let resolveFirst!: (page: any) => void;
    let resolveSecond!: (page: any) => void;
    serviceMocks.searchUsers
      .mockImplementationOnce((_options: unknown, _signal: AbortSignal) => new Promise(resolve => { resolveFirst = resolve; }))
      .mockImplementationOnce((_options: unknown, _signal: AbortSignal) => new Promise(resolve => { resolveSecond = resolve; }));
    const controlled = mountControlled('Draft @al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(9, 9);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    const query = controlled.wrapper.get('input[type="search"]');
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');
    const firstSignal = serviceMocks.searchUsers.mock.calls[0]?.[1] as AbortSignal;
    await query.setValue('ali');
    expect(firstSignal.aborted).toBe(true);
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');

    resolveSecond({
      items: [{ user: { id: 32, username: 'alina', display_name: 'Alina', avatar_url: '' }, following: false }],
      has_more: false,
    });
    await flushPromises();
    resolveFirst({
      items: [{ user: { id: 33, username: 'alice-old', display_name: 'Old Alice', avatar_url: '' }, following: false }],
      has_more: false,
    });
    await flushPromises();

    expect(controlled.wrapper.text()).toContain('@alina');
    expect(controlled.wrapper.text()).not.toContain('@alice-old');
    controlled.wrapper.unmount();
  });

  it('keeps the composer usable after a global search failure', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([alice]);
    serviceMocks.searchUsers.mockRejectedValue(new Error('offline'));
    const controlled = mountControlled('Draft @al');
    const textarea = controlled.wrapper.get('textarea');
    (textarea.element as HTMLTextAreaElement).setSelectionRange(9, 9);
    await textarea.trigger('select');
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    await controlled.wrapper.get('.mention-suggestions__search').trigger('click');
    await controlled.wrapper.get('.mention-suggestions__search-submit').trigger('click');
    await flushPromises();

    expect(controlled.wrapper.text()).toContain('Search is unavailable.');
    await controlled.wrapper.get('.mention-suggestions__search--quiet').trigger('click');
    expect(controlled.value.value).toBe('Draft @al');
    expect(controlled.wrapper.get('.mention-suggestions__search').text()).toBe('Search more users');
    controlled.wrapper.unmount();
  });
});
