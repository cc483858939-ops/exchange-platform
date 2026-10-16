// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useMentionSuggestions } from './useMentionSuggestions';

const serviceMocks = vi.hoisted(() => ({
  getMentionSuggestions: vi.fn(),
}));

vi.mock('../services/mentionService', () => ({
  getMentionSuggestions: serviceMocks.getMentionSuggestions,
}));

const person = (id: number, username: string) => ({
  id,
  username,
  display_name: username,
  avatar_url: '',
  reason: 'following' as const,
});

const harness = () => {
  let state!: ReturnType<typeof useMentionSuggestions>;
  const wrapper = mount(defineComponent({
    setup() {
      state = useMentionSuggestions();
      return () => h('div');
    },
  }));
  return { state, wrapper };
};

describe('useMentionSuggestions', () => {
  afterEach(() => {
    vi.useRealTimers();
    serviceMocks.getMentionSuggestions.mockReset();
  });

  it('debounces a default @ query and keeps the server candidate order', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValue([person(2, 'bob'), person(1, 'alice')]);
    const { state, wrapper } = harness();

    state.update('@', 1, 1);
    vi.advanceTimersByTime(199);
    expect(serviceMocks.getMentionSuggestions).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    await flushPromises();

    expect(serviceMocks.getMentionSuggestions).toHaveBeenCalledWith('', expect.any(AbortSignal));
    expect(state.items.value.map(item => item.username)).toEqual(['bob', 'alice']);
    state.moveSelection(1);
    expect(state.selectedItem()?.username).toBe('alice');
    wrapper.unmount();
  });

  it('uses the active prefix and ignores a late response from the prior context', async () => {
    vi.useFakeTimers();
    let resolveFirst!: (value: ReturnType<typeof person>[]) => void;
    serviceMocks.getMentionSuggestions
      .mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve; }))
      .mockResolvedValueOnce([person(2, 'bobby')]);
    const { state, wrapper } = harness();

    state.update('@a', 2, 2);
    vi.advanceTimersByTime(200);
    state.update('@bo', 3, 3);
    vi.advanceTimersByTime(200);
    await flushPromises();
    resolveFirst([person(1, 'alice')]);
    await flushPromises();

    expect(serviceMocks.getMentionSuggestions.mock.calls.map(([prefix]) => prefix)).toEqual(['a', 'bo']);
    expect(state.items.value.map(item => item.username)).toEqual(['bobby']);
    wrapper.unmount();
  });

  it('closes during IME composition, outside a token, and while disabled', () => {
    vi.useFakeTimers();
    const { state, wrapper } = harness();
    state.update('@al', 3, 3, { composing: true });
    expect(state.isOpen.value).toBe(false);
    state.update('hello', 5, 5);
    expect(state.isOpen.value).toBe(false);
    state.update('@al', 3, 3, { disabled: true });
    expect(state.isOpen.value).toBe(false);
    expect(serviceMocks.getMentionSuggestions).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('exposes empty and failed results without throwing into the editor', async () => {
    vi.useFakeTimers();
    serviceMocks.getMentionSuggestions.mockResolvedValueOnce([]).mockRejectedValueOnce(new Error('offline'));
    const { state, wrapper } = harness();

    state.update('@', 1, 1);
    vi.advanceTimersByTime(200);
    await flushPromises();
    expect(state.state.value).toBe('ready');
    expect(state.items.value).toEqual([]);

    state.update('@a', 2, 2);
    vi.advanceTimersByTime(200);
    await flushPromises();
    expect(state.state.value).toBe('error');
    expect(state.isOpen.value).toBe(true);
    wrapper.unmount();
  });
});
