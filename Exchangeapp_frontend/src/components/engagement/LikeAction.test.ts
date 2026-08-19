// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import LikeAction from './LikeAction.vue';
import AppIcon from '../icons/AppIcon.vue';

const mountLikeAction = (props: Partial<{
  liked: boolean;
  count: number;
  disabled: boolean;
  loading: boolean;
  pending: boolean;
  variant: 'compact' | 'detail';
  ariaLabel: string;
  ariaPressed: boolean | null;
}> = {}) => mount(LikeAction, {
  props: {
    liked: false,
    count: 12,
    ariaLabel: 'Like post, 12 likes',
    ...props,
  },
});

describe('LikeAction', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders an unliked ready state with accessible state and count', () => {
    const wrapper = mountLikeAction();

    expect(wrapper.find('button').attributes('disabled')).toBeUndefined();
    expect(wrapper.find('button').attributes('aria-pressed')).toBe('false');
    expect(wrapper.find('.like-action__count').text()).toBe('12');
    expect(wrapper.findComponent(AppIcon).props('filled')).toBe(false);
  });

  it('renders an active liked state', () => {
    const wrapper = mountLikeAction({
      liked: true,
      count: 13,
      ariaLabel: 'Unlike post, 13 likes',
    });

    expect(wrapper.find('button').classes()).toContain('like-action--liked');
    expect(wrapper.find('button').attributes('aria-pressed')).toBe('true');
    expect(wrapper.findComponent(AppIcon).props('filled')).toBe(true);
  });

  it('starts liking motion and emits one toggle on activation', async () => {
    const wrapper = mountLikeAction();

    await wrapper.find('button').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
    expect(wrapper.find('button').classes()).toContain('like-action--liking');
    expect(wrapper.find('button').attributes('data-motion')).toBe('liking');
  });

  it('starts quiet unlike motion without a like burst', async () => {
    const wrapper = mountLikeAction({
      liked: true,
      count: 13,
      ariaLabel: 'Unlike post, 13 likes',
    });

    await wrapper.find('button').trigger('click');

    expect(wrapper.find('button').classes()).toContain('like-action--unliking');
    expect(wrapper.find('button').classes()).not.toContain('like-action--liking');
    expect(wrapper.find('.like-action__halo').attributes('style')).toBeUndefined();
  });

  it.each([
    ['disabled', { disabled: true }, false, false],
    ['loading', { loading: true }, true, false],
    ['pending', { pending: true, liked: true }, true, true],
  ] as const)('uses native disabled semantics for %s', async (_name, props, isBusy, isPending) => {
    const wrapper = mountLikeAction(props);

    expect(wrapper.find('button').attributes('disabled')).toBe('');
    expect(wrapper.emitted('toggle')).toBeUndefined();

    if (isBusy) {
      expect(wrapper.find('button').attributes('aria-busy')).toBe('true');
    }
    if (_name === 'loading') {
      expect(wrapper.find('button').classes()).toContain('like-action--loading');
    }
    if (isPending) {
      expect(wrapper.find('button').classes()).toContain('like-action--pending');
      expect(wrapper.findComponent(AppIcon).props('filled')).toBe(true);
    }
  });

  it('omits aria-pressed when the parent marks state unknown', () => {
    const wrapper = mountLikeAction({ ariaPressed: null });

    expect(wrapper.find('button').attributes('aria-pressed')).toBeUndefined();
  });

  it('does not start motion for prop-only liked changes', async () => {
    const wrapper = mountLikeAction();

    await wrapper.setProps({ liked: true });

    expect(wrapper.find('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.find('button').classes()).not.toContain('like-action--liking');
  });

  it('does not start motion for prop-only unlike changes', async () => {
    const wrapper = mountLikeAction({ liked: true, count: 13 });

    await wrapper.setProps({ liked: false });

    expect(wrapper.find('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.find('button').classes()).not.toContain('like-action--unliking');
  });

  it('cancels an in-flight like burst on rollback without starting unlike motion', async () => {
    const wrapper = mountLikeAction();

    await wrapper.find('button').trigger('click');
    await wrapper.setProps({ liked: true, count: 13 });
    expect(wrapper.find('button').classes()).toContain('like-action--liking');

    await wrapper.setProps({ liked: false, count: 12 });

    expect(wrapper.find('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.find('button').classes()).not.toContain('like-action--liking');
    expect(wrapper.find('button').classes()).not.toContain('like-action--unliking');
  });

  it('cancels an in-flight unlike motion on rollback without starting like motion', async () => {
    const wrapper = mountLikeAction({ liked: true, count: 13 });

    await wrapper.find('button').trigger('click');
    await wrapper.setProps({ liked: false, count: 12 });
    expect(wrapper.find('button').classes()).toContain('like-action--unliking');

    await wrapper.setProps({ liked: true, count: 13 });

    expect(wrapper.find('button').attributes('data-motion')).toBe('idle');
    expect(wrapper.find('button').classes()).not.toContain('like-action--unliking');
    expect(wrapper.find('button').classes()).not.toContain('like-action--liking');
  });

  it('returns motion to idle after the choreography timer completes', async () => {
    vi.useFakeTimers();
    const wrapper = mountLikeAction();

    await wrapper.find('button').trigger('click');
    vi.advanceTimersByTime(321);
    await wrapper.vm.$nextTick();

    expect(wrapper.find('button').attributes('data-motion')).toBe('idle');
  });

  it('uses directional count motion only for the first optimistic count change', async () => {
    const wrapper = mountLikeAction();

    await wrapper.find('button').trigger('click');
    await wrapper.setProps({ liked: true, count: 13 });
    expect(wrapper.find('[data-count-transition]').attributes('data-count-transition')).toBe('like-count-up');

    await wrapper.setProps({ count: 14 });
    expect(wrapper.find('[data-count-transition]').attributes('data-count-transition')).toBe('like-count-fade');
  });

  it('uses reconciliation fade for a rollback count change', async () => {
    const wrapper = mountLikeAction();

    await wrapper.find('button').trigger('click');
    await wrapper.setProps({ liked: true, count: 13 });
    await wrapper.setProps({ liked: false, count: 12 });

    expect(wrapper.find('[data-count-transition]').attributes('data-count-transition')).toBe('like-count-fade');
  });
});
