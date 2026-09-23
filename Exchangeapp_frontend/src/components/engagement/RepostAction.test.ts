// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RepostAction from './RepostAction.vue';

const mountRepostAction = (props: Partial<{
  reposted: boolean;
  count: number;
  disabled: boolean;
  loading: boolean;
  pending: boolean;
  variant: 'compact' | 'detail';
  ariaLabel: string;
}> = {}) => mount(RepostAction, {
  props: {
    reposted: false,
    count: 8,
    ariaLabel: 'Repost post, 8 reposts',
    ...props,
  },
  global: {
    stubs: {
      AppIcon: {
        props: ['name', 'size'],
        template: '<span class="test-icon" :data-name="name" :data-size="size" />',
      },
    },
  },
});

afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe('RepostAction', () => {
  it('renders a structured decorative icon and count without particle DOM', () => {
    const wrapper = mountRepostAction();

    expect(wrapper.findAll('.repost-action__visual')).toHaveLength(1);
    expect(wrapper.findAll('.repost-action__icon')).toHaveLength(1);
    expect(wrapper.findAll('.repost-action__count-window')).toHaveLength(1);
    expect(wrapper.findAll('.repost-action__count')).toHaveLength(1);
    expect(wrapper.find('.repost-action__visual').attributes('aria-hidden')).toBe('true');
    expect(wrapper.find('.repost-action__count-window').attributes('aria-hidden')).toBe('true');
    expect(wrapper.find('.repost-action__sprite').exists()).toBe(false);
    expect(wrapper.find('.repost-action__particles').exists()).toBe(false);
  });

  it('renders the inactive ready state with parent-controlled accessible semantics', () => {
    const wrapper = mountRepostAction();
    const button = wrapper.get('button');

    expect(button.attributes('disabled')).toBeUndefined();
    expect(button.attributes('aria-pressed')).toBe('false');
    expect(button.attributes('aria-label')).toBe('Repost post, 8 reposts');
    expect(button.attributes('aria-hidden')).toBeUndefined();
    expect(button.attributes('data-motion')).toBe('idle');
    expect(button.classes()).not.toContain('repost-action--reposted');
    expect(wrapper.get('.repost-action__count').text()).toBe('8');
    expect(wrapper.get('.test-icon').attributes('data-size')).toBe('18');
  });

  it('renders a stable active state and uses the larger icon in detail variant', () => {
    const wrapper = mountRepostAction({
      reposted: true,
      count: 9,
      ariaLabel: 'Undo repost, 9 reposts',
      variant: 'detail',
    });
    const button = wrapper.get('button');

    expect(button.classes()).toContain('repost-action--reposted');
    expect(button.attributes('aria-pressed')).toBe('true');
    expect(button.attributes('data-motion')).toBe('idle');
    expect(wrapper.get('.test-icon').attributes('data-size')).toBe('20');
  });

  it('shows immediate repost feedback while aria-pressed remains parent-controlled', async () => {
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
    expect(wrapper.get('button').classes()).toContain('repost-action--reposting');
    expect(wrapper.get('button').classes()).toContain('repost-action--reposted');
    expect(wrapper.get('button').attributes('data-motion')).toBe('reposting');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('false');
  });

  it('removes active visuals immediately when undoing before parent props update', async () => {
    const wrapper = mountRepostAction({ reposted: true, count: 9 });

    await wrapper.get('button').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
    expect(wrapper.get('button').classes()).toContain('repost-action--unreposting');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--reposted');
    expect(wrapper.get('button').attributes('data-motion')).toBe('unreposting');
    expect(wrapper.get('button').attributes('aria-pressed')).toBe('true');
  });

  it.each([
    ['disabled', { disabled: true }, undefined],
    ['loading', { loading: true }, 'true'],
    ['pending', { pending: true, reposted: true, count: 9 }, 'true'],
  ] as const)('uses native disabled semantics for %s', async (_name, props, busy) => {
    const wrapper = mountRepostAction(props);
    const button = wrapper.get('button');

    expect(button.attributes('disabled')).toBe('');
    expect(button.attributes('aria-busy')).toBe(busy);
    if (_name === 'pending') {
      expect(button.classes()).toContain('repost-action--reposted');
    }

    await button.trigger('click');
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });

  it('finishes repost motion after 320ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(319);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('reposting');

    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
  });

  it('finishes undo motion after 180ms', async () => {
    vi.useFakeTimers();
    const wrapper = mountRepostAction({ reposted: true, count: 9 });

    await wrapper.get('button').trigger('click');
    vi.advanceTimersByTime(179);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('unreposting');

    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
  });

  it('uses upward count motion for the first intended repost increment', async () => {
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ reposted: true, count: 9 });

    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-up');
  });

  it('uses downward count motion for the first intended undo decrement', async () => {
    const wrapper = mountRepostAction({ reposted: true, count: 9 });

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ reposted: false, count: 8 });

    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-down');
  });

  it('uses reconciliation fade after the first intended count change', async () => {
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ reposted: true, count: 9 });
    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-up');

    await wrapper.setProps({ count: 10 });
    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-fade');
  });

  it('cancels repost motion on rollback without starting undo motion', async () => {
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ reposted: true, count: 9 });
    expect(wrapper.get('button').attributes('data-motion')).toBe('reposting');

    await wrapper.setProps({ reposted: false, count: 8 });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--reposting');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--unreposting');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--reposted');
    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-fade');
  });

  it('cancels undo motion on rollback without starting repost motion', async () => {
    const wrapper = mountRepostAction({ reposted: true, count: 9 });

    await wrapper.get('button').trigger('click');
    await wrapper.setProps({ reposted: false, count: 8 });
    expect(wrapper.get('button').attributes('data-motion')).toBe('unreposting');

    await wrapper.setProps({ reposted: true, count: 9 });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--reposting');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--unreposting');
    expect(wrapper.get('button').classes()).toContain('repost-action--reposted');
    expect(wrapper.get('.repost-action__count-window').attributes('data-count-transition'))
      .toBe('repost-count-fade');
  });

  it('does not animate prop-only repost changes', async () => {
    const wrapper = mountRepostAction();

    await wrapper.setProps({ reposted: true });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).toContain('repost-action--reposted');
  });

  it('does not animate prop-only undo changes', async () => {
    const wrapper = mountRepostAction({ reposted: true, count: 9 });

    await wrapper.setProps({ reposted: false });

    expect(wrapper.get('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.get('button').classes()).not.toContain('repost-action--reposted');
  });

  it('clears the motion timer when unmounted', async () => {
    vi.useFakeTimers();
    const wrapper = mountRepostAction();

    await wrapper.get('button').trigger('click');
    expect(vi.getTimerCount()).toBe(1);

    wrapper.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
