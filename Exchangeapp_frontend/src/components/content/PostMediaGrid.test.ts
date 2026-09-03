// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import PostMediaGrid from './PostMediaGrid.vue';

const media = (count: number) => Array.from({ length: count }, (_, index) => ({
  type: 'image' as const,
  url: `/media/${index}.jpg`,
  position: index,
}));

const mountGrid = (count: number, removable = false) => mount(PostMediaGrid, {
  props: {
    media: media(count),
    removable,
  },
  global: {
    stubs: {
      AppIcon: { template: '<span class="icon-stub" />' },
    },
  },
});

describe('PostMediaGrid', () => {
  it.each([1, 2, 3, 4])('renders the %i-image layout', count => {
    const wrapper = mountGrid(count);

    expect(wrapper.find(`.post-media-grid--count-${count}`).exists()).toBe(true);
    expect(wrapper.findAll('.post-media-grid__item')).toHaveLength(count);
  });

  it('emits the selected index only when the composer enables removal', async () => {
    const displayWrapper = mountGrid(2);
    expect(displayWrapper.findAll('.post-media-grid__remove')).toHaveLength(0);

    const composerWrapper = mountGrid(2, true);
    await composerWrapper.findAll('.post-media-grid__remove')[1].trigger('click');
    expect(composerWrapper.emitted('remove')).toEqual([[1]]);
  });

  it('shows an accessible placeholder when an image fails', async () => {
    const wrapper = mountGrid(1);
    await wrapper.get('img').trigger('error');

    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').attributes('aria-label'))
      .toBe('Post image 1 unavailable');
  });
});
