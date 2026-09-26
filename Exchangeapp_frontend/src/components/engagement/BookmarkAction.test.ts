// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import BookmarkAction from './BookmarkAction.vue';

const mountBookmarkAction = (props: Partial<{
  bookmarked: boolean;
  loading: boolean;
  pending: boolean;
  disabled: boolean;
  ariaLabel: string;
  variant: 'compact' | 'detail';
}> = {}) => mount(BookmarkAction, {
  props: {
    bookmarked: false,
    ariaLabel: 'Bookmark post',
    ...props,
  },
  global: {
    stubs: {
      BookmarkMorphIcon: {
        props: ['size', 'bookmarked', 'motion'],
        template: '<span class="test-bookmark-morph-icon" :data-size="size" :data-bookmarked="String(bookmarked)" :data-motion="motion" />',
      },
    },
  },
});

afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe('BookmarkAction', () => {
  it('renders the inactive state with parent-controlled accessibility', () => {
    const wrapper = mountBookmarkAction();
    const button = wrapper.get('button');

    expect(button.attributes('aria-pressed')).toBe('false');
    expect(button.attributes('aria-label')).toBe('Bookmark post');
    expect(button.attributes('data-motion')).toBe('idle');
    expect(button.classes()).not.toContain('bookmark-action--bookmarked');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-size': '18',
      'data-bookmarked': 'false',
      'data-motion': 'idle',
    });
  });

  it('renders the active state and the larger detail icon', () => {
    const wrapper = mountBookmarkAction({
      bookmarked: true,
      ariaLabel: 'Remove bookmark',
      variant: 'detail',
    });
    const button = wrapper.get('button');

    expect(button.classes()).toContain('bookmark-action--bookmarked');
    expect(button.attributes('aria-pressed')).toBe('true');
    expect(button.attributes('data-motion')).toBe('idle');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-size': '20',
      'data-bookmarked': 'true',
      'data-motion': 'idle',
    });
  });

  it('shows immediate bookmark feedback without changing aria-pressed before parent confirmation', async () => {
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toEqual([[]]);
    expect(wrapper.get('button').attributes('data-motion')).toBe('bookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarked');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('false');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-bookmarked': 'true',
      'data-motion': 'bookmarking',
    });
  });

  it('shows immediate unbookmark feedback while keeping aria-pressed parent-controlled', async () => {
    const wrapper = mountBookmarkAction({ bookmarked: true, ariaLabel: 'Remove bookmark' });

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toEqual([[]]);
    expect(wrapper.get('button').attributes('data-motion')).toBe('unbookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--unbookmarking');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarked');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('true');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-bookmarked': 'false',
      'data-motion': 'unbookmarking',
    });
  });

  it('keeps bookmark motion through 539ms and settles at 540ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(539);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('bookmarking');

    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
  });

  it('keeps unbookmark motion through 299ms and settles at 300ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountBookmarkAction({ bookmarked: true });

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(299);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('unbookmarking');

    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
  });

  it.each([
    ['disabled', { disabled: true }, undefined],
    ['loading', { loading: true }, 'true'],
    ['pending', { pending: true, bookmarked: true }, 'true'],
  ] as const)('uses native disabled semantics for %s', async (_name, props, busy) => {
    const wrapper = mountBookmarkAction(props);
    const button = wrapper.get('button');

    expect(button.attributes('disabled')).toBe('');
    expect(button.attributes('aria-busy')).toBe(busy);

    await button.trigger('click');
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('cancels bookmark motion on rollback without starting unbookmark motion', async () => {
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ bookmarked: true });
    expect(wrapper.get('button').attributes('data-motion')).toBe('bookmarking');

    await wrapper.setProps({ bookmarked: false });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarking');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--unbookmarking');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-bookmarked': 'false',
      'data-motion': 'idle',
    });
  });

  it('cancels unbookmark motion on rollback without starting bookmark motion', async () => {
    const wrapper = mountBookmarkAction({ bookmarked: true });

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ bookmarked: false });
    expect(wrapper.get('button').attributes('data-motion')).toBe('unbookmarking');

    await wrapper.setProps({ bookmarked: true });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarking');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--unbookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarked');
    expect(wrapper.get('.test-bookmark-morph-icon').attributes()).toMatchObject({
      'data-bookmarked': 'true',
      'data-motion': 'idle',
    });
  });

  it('does not animate prop-only bookmark changes', async () => {
    const wrapper = mountBookmarkAction();

    await wrapper.setProps({ bookmarked: true });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarked');
  });

  it('does not animate prop-only unbookmark changes', async () => {
    const wrapper = mountBookmarkAction({ bookmarked: true });

    await wrapper.setProps({ bookmarked: false });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--unbookmarking');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarked');
  });

  it('clears the motion timer when unmounted', async () => {
    vi.useFakeTimers();
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');
    expect(vi.getTimerCount()).toBe(1);

    wrapper.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
