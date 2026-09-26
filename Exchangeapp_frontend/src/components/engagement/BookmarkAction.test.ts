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
      AppIcon: {
        props: ['name', 'size', 'filled'],
        template: '<span class="test-icon" :data-name="name" :data-size="size" :data-filled="String(filled)" />',
      },
    },
  },
});

afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

const expectBookmarkLayers = (wrapper: ReturnType<typeof mountBookmarkAction>) => {
  const outline = wrapper.get('.bookmark-action__icon--outline .test-icon');
  const filled = wrapper.get('.bookmark-action__icon--filled .test-icon');

  expect(outline.attributes()).toMatchObject({
    'data-name': 'bookmark',
    'data-filled': 'false',
  });
  expect(filled.attributes()).toMatchObject({
    'data-name': 'bookmark',
    'data-filled': 'true',
  });

  return { outline, filled };
};

describe('BookmarkAction', () => {
  it('renders the inactive state with parent-controlled accessibility', () => {
    const wrapper = mountBookmarkAction();
    const button = wrapper.get('button');

    expect(button.attributes('aria-pressed')).toBe('false');
    expect(button.attributes('aria-label')).toBe('Bookmark post');
    expect(button.attributes('data-motion')).toBe('idle');
    expect(button.classes()).not.toContain('bookmark-action--bookmarked');
    expect(wrapper.find('.bookmark-action__visual').exists()).toBe(true);
    expect(wrapper.findAll('.bookmark-action__icon--outline')).toHaveLength(1);
    expect(wrapper.findAll('.bookmark-action__icon--filled')).toHaveLength(1);
    expectBookmarkLayers(wrapper);
    expect(wrapper.get('.bookmark-action__icon--outline .test-icon').attributes('data-size')).toBe('18');
    expect(wrapper.get('.bookmark-action__icon--filled .test-icon').attributes('data-size')).toBe('18');
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
    expectBookmarkLayers(wrapper);
    expect(wrapper.get('.bookmark-action__icon--outline .test-icon').attributes('data-size')).toBe('20');
    expect(wrapper.get('.bookmark-action__icon--filled .test-icon').attributes('data-size')).toBe('20');
  });

  it('shows immediate bookmark feedback without changing aria-pressed before parent confirmation', async () => {
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toEqual([[]]);
    expect(wrapper.get('button').attributes('data-motion')).toBe('bookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--bookmarked');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('false');
    expectBookmarkLayers(wrapper);
  });

  it('shows immediate unbookmark feedback while keeping aria-pressed parent-controlled', async () => {
    const wrapper = mountBookmarkAction({ bookmarked: true, ariaLabel: 'Remove bookmark' });

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toEqual([[]]);
    expect(wrapper.get('button').attributes('data-motion')).toBe('unbookmarking');
    expect(wrapper.get('button').classes()).toContain('bookmark-action--unbookmarking');
    expect(wrapper.get('button').classes()).not.toContain('bookmark-action--bookmarked');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('true');
    expectBookmarkLayers(wrapper);
  });

  it('keeps bookmark motion through 289ms and settles at 290ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountBookmarkAction();

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(289);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('bookmarking');

    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
  });

  it('keeps unbookmark motion through 169ms and settles at 170ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountBookmarkAction({ bookmarked: true });

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(169);
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
    expectBookmarkLayers(wrapper);
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
    expectBookmarkLayers(wrapper);
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
