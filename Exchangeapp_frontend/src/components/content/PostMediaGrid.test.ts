// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import type { PostMedia } from '../../types/Post';
import PostMediaGrid from './PostMediaGrid.vue';

const media = (count: number) => Array.from({ length: count }, (_, index) => ({
  type: 'image' as const,
  url: `/media/${index}.jpg`,
  large_url: `/media/${index}-large.jpg`,
  width: 1200,
  height: 800,
  position: index,
}));

const singleMedia = (overrides: Partial<PostMedia> = {}): PostMedia => ({
  type: 'image',
  url: '/medium.jpg',
  large_url: '/large.jpg',
  width: 960,
  height: 1200,
  position: 0,
  ...overrides,
});

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
    expect(wrapper.findAll('.post-media-grid__item--featured')).toHaveLength(count === 3 ? 1 : 0);
    if (count === 3) {
      expect(wrapper.findAll('.post-media-grid__item')[0].classes())
        .toContain('post-media-grid__item--featured');
    }
  });

  it('updates the featured item as media is removed from the composer', async () => {
    const wrapper = mountGrid(4, true);
    const firstItem = wrapper.findAll('.post-media-grid__item')[0].element;

    await wrapper.setProps({ media: media(3) });
    expect(wrapper.get('.post-media-grid__item--featured').element).toBe(firstItem);

    await wrapper.setProps({ media: media(2) });
    expect(wrapper.findAll('.post-media-grid__item')[0].element).toBe(firstItem);
    expect(wrapper.findAll('.post-media-grid__item--featured')).toHaveLength(0);
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

  it('renders a read-only single image with Medium only', () => {
    const wrapper = mount(PostMediaGrid, {
      props: { media: [singleMedia()] },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });
    const image = wrapper.get('img');

    expect(image.attributes('src')).toBe('/medium.jpg');
    expect(image.attributes('srcset')).toBeUndefined();
    expect(image.attributes('width')).toBe('960');
    expect(image.attributes('height')).toBe('1200');
    expect(image.attributes('loading')).toBe('lazy');
    expect(image.attributes('decoding')).toBe('async');
  });

  it('keeps Medium-only delivery and open interaction for an interactive single image', async () => {
    const wrapper = mount(PostMediaGrid, {
      props: { media: [singleMedia()], interactive: true },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });
    const image = wrapper.get('.post-media-grid__open img');

    expect(image.attributes('src')).toBe('/medium.jpg');
    expect(image.attributes('srcset')).toBeUndefined();

    await wrapper.get('.post-media-grid__open').trigger('click');
    expect(wrapper.emitted('open')).toEqual([[0]]);
  });

  it('keeps removable single-image previews Medium-only', async () => {
    const wrapper = mount(PostMediaGrid, {
      props: { media: [singleMedia()], removable: true },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });
    const image = wrapper.get('img');

    expect(image.attributes('src')).toBe('/medium.jpg');
    expect(image.attributes('srcset')).toBeUndefined();

    await wrapper.get('.post-media-grid__remove').trigger('click');
    expect(wrapper.emitted('remove')).toEqual([[0]]);
  });

  it.each([2, 3, 4])('keeps %i-image delivery Medium-only', count => {
    const wrapper = mountGrid(count);
    const expectedURLs = media(count).map(item => item.url);
    const images = wrapper.findAll('img');

    expect(images.map(image => image.attributes('src'))).toEqual(expectedURLs);
    expect(images.every(image => image.attributes('srcset') === undefined)).toBe(true);
    expect(wrapper.findAll('.post-media-grid__item--featured')).toHaveLength(count === 3 ? 1 : 0);
  });

  it.each([
    { label: 'empty', largeURL: '' },
    { label: 'whitespace-only', largeURL: '   ' },
    { label: 'same URL', largeURL: '/medium.jpg' },
  ])('does not emit a srcset for a $label large URL', ({ largeURL }) => {
    const wrapper = mount(PostMediaGrid, {
      props: { media: [singleMedia({ large_url: largeURL })] },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });

    expect(wrapper.get('img').attributes('src')).toBe('/medium.jpg');
    expect(wrapper.get('img').attributes('srcset')).toBeUndefined();
  });

  it('shows a placeholder immediately when the Medium image fails', async () => {
    const wrapper = mount(PostMediaGrid, {
      props: { media: [singleMedia()] },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });

    await wrapper.get('img').trigger('error');
    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').attributes('aria-label'))
      .toBe('Post image 1 unavailable');
  });

  it.each([
    { label: 'landscape', width: 1200, height: 800, expectedWidth: '520px' },
    { label: '4:5 portrait', width: 800, height: 1000, expectedWidth: '512px' },
    { label: '9:16 portrait', width: 900, height: 1600, expectedWidth: '360px' },
    { label: 'small source', width: 300, height: 200, expectedWidth: '300px' },
  ])('sizes a known-dimension $label image without upscaling', ({ width, height, expectedWidth }) => {
    const wrapper = mount(PostMediaGrid, {
      props: {
        media: [{
          type: 'image',
          url: '/single.jpg',
          large_url: '/single-large.jpg',
          width,
          height,
          position: 0,
        }],
      },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });

    const grid = wrapper.get('.post-media-grid');
    const gridElement = grid.element as HTMLElement;
    expect(grid.classes()).toContain('post-media-grid--single-sized');
    expect(gridElement.style.getPropertyValue('--post-media-single-width'))
      .toBe(expectedWidth);
    expect(gridElement.style.getPropertyValue('--post-media-single-aspect-ratio'))
      .toBe(`${width} / ${height}`);
  });

  it('does not apply sized-single presentation to a removable preview', () => {
    const wrapper = mountGrid(1, true);
    const grid = wrapper.get('.post-media-grid');
    const gridElement = grid.element as HTMLElement;

    expect(grid.classes()).not.toContain('post-media-grid--single-sized');
    expect(gridElement.style.getPropertyValue('--post-media-single-width')).toBe('');
  });

  it('keeps the single-image fallback when dimensions are unknown', () => {
    const wrapper = mount(PostMediaGrid, {
      props: {
        media: [{
          type: 'image',
          url: '/preview.jpg',
          large_url: '/preview.jpg',
          width: 0,
          height: 0,
          position: 0,
        }],
      },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });

    const grid = wrapper.get('.post-media-grid');
    const gridElement = grid.element as HTMLElement;
    expect(grid.classes()).not.toContain('post-media-grid--single-sized');
    expect(gridElement.style.getPropertyValue('--post-media-single-width')).toBe('');
  });

  it.each([2, 3, 4])('does not apply sized-single presentation to %i images', count => {
    const wrapper = mountGrid(count);

    expect(wrapper.get('.post-media-grid').classes())
      .not.toContain('post-media-grid--single-sized');
  });

  it('uses async decoding and known dimensions for server media', () => {
    const wrapper = mountGrid(1);
    const image = wrapper.get('img');

    expect(image.attributes('loading')).toBe('lazy');
    expect(image.attributes('decoding')).toBe('async');
    expect(image.attributes('width')).toBe('1200');
    expect(image.attributes('height')).toBe('800');
  });

  it('omits dimensions when a local preview does not know them yet', () => {
    const wrapper = mount(PostMediaGrid, {
      props: {
        media: [{ type: 'image', url: '/preview.jpg', large_url: '/preview.jpg', width: 0, height: 0, position: 0 }],
      },
      global: { stubs: { AppIcon: { template: '<span class="icon-stub" />' } } },
    });

    expect(wrapper.get('img').attributes('width')).toBeUndefined();
    expect(wrapper.get('img').attributes('height')).toBeUndefined();
    expect(wrapper.get('img').attributes('srcset')).toBeUndefined();
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

  it('shows an accessible placeholder after a Medium image fails', async () => {
    const wrapper = mountGrid(1);
    await wrapper.get('img').trigger('error');

    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').attributes('aria-label'))
      .toBe('Post image 1 unavailable');
  });
});
