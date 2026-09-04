// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import PostMediaGrid from './PostMediaGrid.vue';

const media = (count: number) => Array.from({ length: count }, (_, index) => ({
  type: 'image' as const,
  url: `/media/${index}.jpg`,
  position: index,
}));

const mountGrid = (count: number, removable = false, interactive = false) => mount(PostMediaGrid, {
  props: {
    media: media(count),
    removable,
    interactive,
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

  it('exposes the intrinsic-ratio presentation state for a single image', () => {
    const singleWrapper = mountGrid(1);
    expect(singleWrapper.get('.post-media-grid').classes())
      .toContain('post-media-grid--count-1');
    expect(singleWrapper.get('img').classes())
      .toContain('post-media-grid__image--single');

    const multiWrapper = mountGrid(2);
    expect(multiWrapper.findAll('img')[0].classes())
      .not.toContain('post-media-grid__image--single');
  });

  it('keeps image-open controls disabled by default', () => {
    const wrapper = mountGrid(2);

    expect(wrapper.findAll('.post-media-grid__open')).toHaveLength(0);
  });

  it('exposes accessible image-open controls when interactive', () => {
    const wrapper = mountGrid(2, false, true);

    expect(wrapper.findAll('.post-media-grid__open')).toHaveLength(2);
    expect(wrapper.find('.post-media-grid__open').attributes('aria-label'))
      .toBe('Open post image 1 of 2');
  });

  it('emits the selected image index', async () => {
    const firstWrapper = mountGrid(1, false, true);
    await firstWrapper.get('.post-media-grid__open').trigger('click');
    expect(firstWrapper.emitted('open')).toEqual([[0]]);

    const thirdWrapper = mountGrid(4, false, true);
    await thirdWrapper.findAll('.post-media-grid__open')[2].trigger('click');
    expect(thirdWrapper.emitted('open')).toEqual([[2]]);
  });

  it('does not make a failed image activatable', async () => {
    const wrapper = mountGrid(2, false, true);
    await wrapper.findAll('img')[1].trigger('error');

    expect(wrapper.findAll('.post-media-grid__open')).toHaveLength(1);
    expect(wrapper.get('[role="img"]').attributes('aria-label'))
      .toBe('Post image 2 unavailable');
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
