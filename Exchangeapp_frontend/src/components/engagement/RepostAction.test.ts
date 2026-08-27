// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
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
      AppIcon: { template: '<span class="test-icon" />' },
    },
  },
});

describe('RepostAction', () => {
  it('renders an inactive state with count and accessible pressed state', () => {
    const wrapper = mountRepostAction();
    const button = wrapper.find('button');

    expect(button.attributes('disabled')).toBeUndefined();
    expect(button.attributes('aria-pressed')).toBe('false');
    expect(button.attributes('aria-label')).toBe('Repost post, 8 reposts');
    expect(button.text()).toContain('8');
  });

  it('renders an active state', () => {
    const wrapper = mountRepostAction({
      reposted: true,
      count: 9,
      ariaLabel: 'Undo repost, 9 reposts',
    });

    expect(wrapper.find('button').classes()).toContain('repost-action--reposted');
    expect(wrapper.find('button').attributes('aria-pressed')).toBe('true');
  });

  it('emits one toggle when activated', async () => {
    const wrapper = mountRepostAction();

    await wrapper.find('button').trigger('click');

    expect(wrapper.emitted('toggle')).toHaveLength(1);
  });

  it.each([
    ['disabled', { disabled: true }],
    ['loading', { loading: true }],
    ['pending', { pending: true }],
  ] as const)('uses native disabled semantics for %s', async (_name, props) => {
    const wrapper = mountRepostAction(props);
    const button = wrapper.find('button');

    expect(button.attributes('disabled')).toBe('');
    expect(button.attributes('aria-busy')).toBe(_name === 'disabled' ? undefined : 'true');

    await button.trigger('click');
    expect(wrapper.emitted('toggle')).toBeUndefined();
  });
});
