// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import PostMediaViewer from './PostMediaViewer.vue';

const media = (count: number) => Array.from({ length: count }, (_, index) => ({
  type: 'image' as const,
  url: `/media/${index}.jpg`,
  position: index,
}));

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
const mountedViewers: Array<ReturnType<typeof mount>> = [];

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute('open');
    },
  });
});

afterEach(() => {
  mountedViewers.splice(0).forEach(wrapper => wrapper.unmount());
  if (originalShowModal) {
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
  if (originalClose) {
    Object.defineProperty(HTMLDialogElement.prototype, 'close', originalClose);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
  }
});

const mountViewer = (count = 3, initialIndex = 0) => {
  const wrapper = mount(PostMediaViewer, {
    props: {
      media: media(count),
      initialIndex,
    },
    global: {
      stubs: {
        AppIcon: {
          props: ['name'],
          template: '<span class="icon-stub" :data-icon="name" />',
        },
      },
    },
  });
  mountedViewers.push(wrapper);
  return wrapper;
};

describe('PostMediaViewer', () => {
  it('opens the requested initial image', () => {
    const wrapper = mountViewer(3, 1);

    expect(wrapper.get('dialog').attributes('open')).toBe('');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');
    expect(wrapper.get('.post-media-viewer__counter').text()).toBe('2 / 3');
  });

  it('clamps an invalid initial index to the visible media range', () => {
    const tooHigh = mountViewer(3, 99);
    expect(tooHigh.get('.post-media-viewer__image').attributes('src')).toBe('/media/2.jpg');

    const negative = mountViewer(3, -1);
    expect(negative.get('.post-media-viewer__image').attributes('src')).toBe('/media/0.jpg');
  });

  it('exposes an uncropped viewer image presentation', () => {
    const wrapper = mountViewer(1);

    expect(wrapper.get('.post-media-viewer__image').classes())
      .toContain('post-media-viewer__image');
    expect(wrapper.get('.post-media-viewer__image-frame').attributes('aria-label'))
      .toBe('Post image');
  });

  it('moves with bounded previous and next controls', async () => {
    const wrapper = mountViewer(3, 1);

    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0.jpg');

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');
    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2.jpg');
  });

  it('disables navigation at the first and last image', () => {
    const first = mountViewer(3, 0);
    expect(first.get('[aria-label="Previous image"]').attributes('disabled')).toBe('');

    const last = mountViewer(3, 2);
    expect(last.get('[aria-label="Next image"]').attributes('disabled')).toBe('');
  });

  it('supports keyboard navigation and Escape close', async () => {
    const wrapper = mountViewer(3, 1);

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }));
    await nextTick();
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2.jpg');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft' }));
    await nextTick();
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('supports horizontal swipe navigation without treating vertical movement as a swipe', async () => {
    const wrapper = mountViewer(3, 1);
    const stage = wrapper.get('.post-media-viewer__stage');

    await stage.trigger('pointerdown', { clientX: 200, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 120, clientY: 108 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2.jpg');

    await stage.trigger('pointerdown', { clientX: 120, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 200, clientY: 108 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');

    await stage.trigger('pointerdown', { clientX: 200, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 120, clientY: 180 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');
  });

  it('emits close from the close button', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('[aria-label="Close image viewer"]').trigger('click');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('emits close from dialog cancel', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('dialog').trigger('cancel');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('hides navigation and counter for a single image', () => {
    const wrapper = mountViewer(1);

    expect(wrapper.find('[aria-label="Previous image"]').exists()).toBe(false);
    expect(wrapper.find('[aria-label="Next image"]').exists()).toBe(false);
    expect(wrapper.find('.post-media-viewer__counter').exists()).toBe(false);
  });

  it('shows an accessible placeholder after an image fails and keeps navigation available', async () => {
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('error');
    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').attributes('aria-label')).toBe('Image unavailable');
    expect(wrapper.get('[role="img"]').text()).toContain('Image unavailable');

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1.jpg');
  });

  it('removes the global keyboard listener on unmount', () => {
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const wrapper = mountViewer();

    wrapper.unmount();

    expect(removeEventListener).toHaveBeenCalledWith('keydown', expect.any(Function));
    removeEventListener.mockRestore();
  });
});
