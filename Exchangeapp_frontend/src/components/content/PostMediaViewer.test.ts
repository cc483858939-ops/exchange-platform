// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import PostMediaViewer from './PostMediaViewer.vue';

const media = (count: number) => Array.from({ length: count }, (_, index) => ({
  type: 'image' as const,
  url: `/media/${index}-medium.jpg`,
  large_url: `/media/${index}-large.jpg`,
  width: 1200,
  height: 800,
  position: index,
}));

type DecodeMode = 'resolve' | 'reject' | 'pending';
type PendingDecode = { resolve: () => void; reject: () => void };

const decodeModes = new Map<string, DecodeMode>();
const pendingDecodes = new Map<string, PendingDecode[]>();
let decodeCalls: string[] = [];
let imageDecodeAvailable = true;
let createdImages: ControlledImage[] = [];
type MediaQueryListener = (event: MediaQueryListEvent) => void;
let mediaQueryState: { matches: boolean; listeners: Set<MediaQueryListener> };

class ControlledImage {
  decoding = '';
  src = '';
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor() {
    createdImages.push(this);
    if (!imageDecodeAvailable) {
      Object.defineProperty(this, 'decode', {
        configurable: true,
        value: undefined,
      });
    }
  }

  removeAttribute(name: string) {
    if (name === 'src') {
      this.src = '';
    }
  }

  decode() {
    decodeCalls.push(this.src);
    const mode = decodeModes.get(this.src) ?? 'pending';
    if (mode === 'resolve') {
      return Promise.resolve();
    }
    if (mode === 'reject') {
      return Promise.reject(new Error(`decode failed for ${this.src}`));
    }
    return new Promise<void>((resolve, reject) => {
      const entries = pendingDecodes.get(this.src) ?? [];
      entries.push({ resolve, reject });
      pendingDecodes.set(this.src, entries);
    });
  }
}

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
const originalMatchMedia = Object.getOwnPropertyDescriptor(window, 'matchMedia');
const mountedViewers: Array<ReturnType<typeof mount>> = [];

beforeEach(() => {
  decodeModes.clear();
  pendingDecodes.clear();
  decodeCalls = [];
  imageDecodeAvailable = true;
  createdImages = [];
  mediaQueryState = { matches: false, listeners: new Set() };
  vi.stubGlobal('Image', ControlledImage);
  const matchMedia = () => ({
    matches: mediaQueryState.matches,
    media: '(min-width: 1100px)',
    onchange: null,
    addEventListener: (_type: string, listener: MediaQueryListener) => {
      mediaQueryState.listeners.add(listener);
    },
    removeEventListener: (_type: string, listener: MediaQueryListener) => {
      mediaQueryState.listeners.delete(listener);
    },
    addListener: (listener: MediaQueryListener) => {
      mediaQueryState.listeners.add(listener);
    },
    removeListener: (listener: MediaQueryListener) => {
      mediaQueryState.listeners.delete(listener);
    },
    dispatchEvent: () => true,
  });
  vi.stubGlobal('matchMedia', matchMedia);
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: matchMedia,
  });
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
  vi.useRealTimers();
  vi.unstubAllGlobals();
  if (originalMatchMedia) {
    Object.defineProperty(window, 'matchMedia', originalMatchMedia);
  } else {
    Reflect.deleteProperty(window, 'matchMedia');
  }
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

const setDecodeMode = (url: string, mode: DecodeMode) => {
  decodeModes.set(url, mode);
};

const resolveDecode = (url: string) => {
  const entries = pendingDecodes.get(url) ?? [];
  pendingDecodes.delete(url);
  entries.forEach(entry => entry.resolve());
};

const rejectDecode = (url: string) => {
  const entries = pendingDecodes.get(url) ?? [];
  pendingDecodes.delete(url);
  entries.forEach(entry => entry.reject());
};

const advanceTimers = async (milliseconds: number) => {
  await vi.advanceTimersByTimeAsync(milliseconds);
  await nextTick();
  await Promise.resolve();
};

const flushAsync = async () => {
  await Promise.resolve();
  await nextTick();
  await Promise.resolve();
};

const setDesktopViewport = async (matches: boolean) => {
  mediaQueryState.matches = matches;
  const event = {
    matches,
    media: '(min-width: 1100px)',
  } as MediaQueryListEvent;
  mediaQueryState.listeners.forEach(listener => listener(event));
  await nextTick();
};

const mountViewer = (
  count = 3,
  initialIndex = 0,
  options: { desktopContext?: boolean; context?: string } = {},
) => {
  const wrapper = mount(PostMediaViewer, {
    props: {
      media: media(count),
      initialIndex,
      desktopContext: options.desktopContext ?? false,
    },
    slots: options.context ? { context: options.context } : undefined,
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

const setStableGeometry = (
  wrapper: ReturnType<typeof mountViewer>,
  options: { frameWidth?: number; frameHeight?: number; imageWidth?: number; imageHeight?: number } = {},
) => {
  const frameWidth = options.frameWidth ?? 300;
  const frameHeight = options.frameHeight ?? 300;
  const imageWidth = options.imageWidth ?? 300;
  const imageHeight = options.imageHeight ?? 200;
  const frame = wrapper.get('.post-media-viewer__image-frame').element as HTMLElement;
  const image = wrapper.get('.post-media-viewer__image').element as HTMLImageElement;

  Object.defineProperty(frame, 'clientWidth', { configurable: true, value: frameWidth });
  Object.defineProperty(frame, 'clientHeight', { configurable: true, value: frameHeight });
  Object.defineProperty(frame, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({
      width: frameWidth,
      height: frameHeight,
      top: 0,
      right: frameWidth,
      bottom: frameHeight,
      left: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }),
  });
  Object.defineProperty(image, 'offsetWidth', { configurable: true, value: imageWidth });
  Object.defineProperty(image, 'offsetHeight', { configurable: true, value: imageHeight });
};

const triggerPointer = (
  wrapper: ReturnType<typeof mountViewer>,
  type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel',
  pointerId: number,
  clientX: number,
  clientY: number,
) => wrapper.get('.post-media-viewer__stage').trigger(type, {
  pointerId,
  clientX,
  clientY,
});

const imageTransform = (wrapper: ReturnType<typeof mountViewer>) => (
  wrapper.get('.post-media-viewer__image').attributes('style') ?? ''
);

describe('PostMediaViewer', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', undefined);
  });

  it('opens the requested initial image', () => {
    const wrapper = mountViewer(3, 1);

    expect(wrapper.get('dialog').attributes('open')).toBe('');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
    expect(wrapper.get('.post-media-viewer__counter').text()).toBe('2 / 3');
  });

  it('clamps an invalid initial index to the visible media range', () => {
    const tooHigh = mountViewer(3, 99);
    expect(tooHigh.get('.post-media-viewer__image').attributes('src')).toBe('/media/2-medium.jpg');

    const negative = mountViewer(3, -1);
    expect(negative.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-medium.jpg');
  });

  it('exposes an uncropped viewer image presentation', () => {
    const wrapper = mountViewer(1);

    expect(wrapper.get('.post-media-viewer__image').classes())
      .toContain('post-media-viewer__image');
    expect(wrapper.get('.post-media-viewer__image').attributes('draggable')).toBe('false');
    expect(wrapper.get('.post-media-viewer__image-frame').attributes('aria-label'))
      .toBe('Post image');
  });

  it('closes when the image frame itself is clicked', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('.post-media-viewer__image-frame').trigger('click');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('closes when the stage itself is clicked', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('.post-media-viewer__stage').trigger('click');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('closes when the media pane itself is clicked', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('.post-media-viewer__media-pane').trigger('click');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('renders the context slot only for a wide desktop split viewer', async () => {
    mediaQueryState.matches = true;
    const wide = mountViewer(1, 0, {
      desktopContext: true,
      context: '<button class="context-child" type="button">Context</button>',
    });
    await nextTick();

    expect(wide.find('.post-media-viewer__context').exists()).toBe(true);
    expect(wide.find('.context-child').exists()).toBe(true);

    mediaQueryState.matches = false;
    const compact = mountViewer(1, 0, {
      desktopContext: true,
      context: '<button class="context-child" type="button">Context</button>',
    });
    expect(compact.find('.post-media-viewer__context').exists()).toBe(false);
    expect(compact.find('.context-child').exists()).toBe(false);
  });

  it('keeps a pure media viewer when desktopContext is false', () => {
    mediaQueryState.matches = true;
    const wrapper = mountViewer(1, 0, {
      context: '<button class="context-child" type="button">Context</button>',
    });

    expect(wrapper.find('.post-media-viewer__context').exists()).toBe(false);
    expect(wrapper.find('.context-child').exists()).toBe(false);
  });

  it('mounts and unmounts the context slot when the viewport crosses the breakpoint', async () => {
    mediaQueryState.matches = true;
    const wrapper = mountViewer(2, 1, {
      desktopContext: true,
      context: '<button class="context-child" type="button">Context</button>',
    });
    await nextTick();

    expect(wrapper.find('.post-media-viewer__context').exists()).toBe(true);
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    await setDesktopViewport(false);
    expect(wrapper.find('.post-media-viewer__context').exists()).toBe(false);
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    await setDesktopViewport(true);
    expect(wrapper.find('.post-media-viewer__context').exists()).toBe(true);
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
  });

  it('does not close when the context rail or its child is clicked', async () => {
    mediaQueryState.matches = true;
    const wrapper = mountViewer(1, 0, {
      desktopContext: true,
      context: '<button class="context-child" type="button">Context</button>',
    });
    await nextTick();

    await wrapper.get('.post-media-viewer__context').trigger('click');
    await wrapper.get('.context-child').trigger('click');

    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('does not close when the image itself is clicked', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('.post-media-viewer__image').trigger('click');

    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('moves with bounded previous and next controls', async () => {
    const wrapper = mountViewer(3, 1);

    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-medium.jpg');

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2-medium.jpg');
    expect(wrapper.emitted('close')).toBeUndefined();
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
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2-medium.jpg');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft' }));
    await nextTick();
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('supports horizontal swipe navigation without treating vertical movement as a swipe', async () => {
    const wrapper = mountViewer(3, 1);
    const stage = wrapper.get('.post-media-viewer__stage');

    await stage.trigger('pointerdown', { clientX: 200, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 120, clientY: 108 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2-medium.jpg');

    await stage.trigger('pointerdown', { clientX: 120, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 200, clientY: 108 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    await stage.trigger('pointerdown', { clientX: 200, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 120, clientY: 180 });
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
  });

  it('supports bounded pinch zoom and returns to the fitted scale', async () => {
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 1000, 150);

    expect(imageTransform(wrapper)).toContain('scale(4)');
    expect(imageTransform(wrapper)).not.toContain('scale(9)');

    await triggerPointer(wrapper, 'pointermove', 2, 200, 150);
    expect(imageTransform(wrapper)).toContain('scale(1)');
    expect(imageTransform(wrapper)).toContain('translate3d(0px, 0px, 0)');
    await triggerPointer(wrapper, 'pointerup', 2, 200, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 100, 150);
  });

  it('pans a zoomed image within bounds without navigating the carousel', async () => {
    const wrapper = mountViewer(3, 1);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 1000, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 1000, 150);

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
    expect(imageTransform(wrapper)).toContain('translate3d(150px, 0px, 0) scale(2)');
  });

  it('allows pinch-to-one-finger panning and restores swipe navigation at 1x', async () => {
    const wrapper = mountViewer(3, 1);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 50, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 250, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 250, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 90, 150);
    expect(imageTransform(wrapper)).toContain('scale(2)');
    expect(imageTransform(wrapper)).toContain('translate3d(40px, 0px, 0)');
    await triggerPointer(wrapper, 'pointerup', 1, 90, 150);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 300, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 200, 150);
    expect(imageTransform(wrapper)).toContain('scale(1)');
    await triggerPointer(wrapper, 'pointerup', 2, 200, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 100, 150);

    await triggerPointer(wrapper, 'pointerdown', 1, 200, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 120, 150);
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/2-medium.jpg');
  });

  it('resets zoom and pan when carousel navigation changes the active media', async () => {
    const wrapper = mountViewer(2);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 250, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 250, 150);
    expect(imageTransform(wrapper)).toContain('scale(2)');

    await wrapper.get('[aria-label="Next image"]').trigger('click');

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
    expect(imageTransform(wrapper)).toContain('translate3d(0px, 0px, 0) scale(1)');
  });

  it('suppresses the click that follows a pinch gesture', async () => {
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 100, 150);
    await wrapper.get('.post-media-viewer__stage').trigger('click');

    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('leaves a usable gesture state after pointer cancellation', async () => {
    const wrapper = mountViewer(3);
    setStableGeometry(wrapper);
    const stage = wrapper.get('.post-media-viewer__stage');

    await triggerPointer(wrapper, 'pointerdown', 1, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 120, 150);
    await triggerPointer(wrapper, 'pointercancel', 1, 120, 150);
    await stage.trigger('click');

    expect(wrapper.emitted('close')).toBeUndefined();
    await triggerPointer(wrapper, 'pointerdown', 1, 200, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 120, 150);
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
  });

  it('re-clamps the zoomed pan after a window resize', async () => {
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointermove', 1, 250, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 250, 150);
    expect(imageTransform(wrapper)).toContain('translate3d(150px, 0px, 0) scale(2)');

    setStableGeometry(wrapper, { frameWidth: 500 });
    window.dispatchEvent(new Event('resize'));
    await nextTick();

    expect(imageTransform(wrapper)).toContain('translate3d(50px, 0px, 0) scale(2)');
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

  it('shows Medium immediately and waits for Medium load plus dwell before Large preload', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(1);

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-medium.jpg');
    expect(decodeCalls).toEqual([]);

    await advanceTimers(300);
    expect(decodeCalls).toEqual([]);
    expect(pendingDecodes.has('/media/0-large.jpg')).toBe(false);

    await wrapper.get('img').trigger('load');
    await advanceTimers(249);
    expect(decodeCalls).toEqual([]);

    await advanceTimers(1);
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);
    expect(pendingDecodes.has('/media/0-large.jpg')).toBe(true);
  });

  it('starts with Medium and upgrades after Large decode succeeds', async () => {
    vi.useFakeTimers();
    setDecodeMode('/media/0-large.jpg', 'resolve');
    const wrapper = mountViewer(1);

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-medium.jpg');
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-large.jpg');
  });

  it('keeps Medium when Large decode fails', async () => {
    vi.useFakeTimers();
    setDecodeMode('/media/0-large.jpg', 'reject');
    const wrapper = mountViewer(1);
    const frame = wrapper.get('.post-media-viewer__image-frame').element;

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    const image = wrapper.get('.post-media-viewer__image');
    expect(image.attributes('src')).toBe('/media/0-medium.jpg');
    expect(image.element.parentElement).toBe(frame);
  });

  it('settles and cleans the no-decode Large load waiter on success', async () => {
    vi.useFakeTimers();
    imageDecodeAvailable = false;
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    const preload = createdImages[0];
    expect(preload).toBeDefined();
    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
    expect(preload.onload).toEqual(expect.any(Function));
    expect(preload.onerror).toEqual(expect.any(Function));

    const oldLoad = preload.onload;
    const oldError = preload.onerror;
    oldLoad?.();
    oldLoad?.();
    oldError?.();
    await flushAsync();

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-large.jpg');
    expect(preload.onload).toBeNull();
    expect(preload.onerror).toBeNull();
  });

  it('remembers a genuine no-decode Large failure without retrying it', async () => {
    vi.useFakeTimers();
    imageDecodeAvailable = false;
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    const preload = createdImages[0];
    const oldError = preload.onerror;
    oldError?.();
    await flushAsync();

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
    expect(preload.onload).toBeNull();
    expect(preload.onerror).toBeNull();

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(createdImages).toHaveLength(1);
    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
  });

  it('cancels a no-decode Large preload and allows it to retry after navigating back', async () => {
    vi.useFakeTimers();
    imageDecodeAvailable = false;
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    const canceledPreload = createdImages[0];
    const oldLoad = canceledPreload.onload;
    const oldError = canceledPreload.onerror;

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(canceledPreload.src).toBe('');
    expect(canceledPreload.onload).toBeNull();
    expect(canceledPreload.onerror).toBeNull();

    oldLoad?.();
    oldError?.();
    await flushAsync();
    expect(wrapper.get('img').attributes('src')).toBe('/media/1-medium.jpg');

    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(createdImages).toHaveLength(2);
    expect(createdImages[1].src).toBe('/media/0-large.jpg');
  });

  it('settles and clears a no-decode Large preload when closed', async () => {
    vi.useFakeTimers();
    imageDecodeAvailable = false;
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    const preload = createdImages[0];
    const oldLoad = preload.onload;
    const oldError = preload.onerror;

    await wrapper.get('[aria-label="Close image viewer"]').trigger('click');
    expect(preload.src).toBe('');
    expect(preload.onload).toBeNull();
    expect(preload.onerror).toBeNull();
    oldLoad?.();
    oldError?.();
    await advanceTimers(500);
    await flushAsync();

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
    expect(createdImages).toHaveLength(1);
  });

  it('settles and clears a no-decode Large preload when unmounted', async () => {
    vi.useFakeTimers();
    imageDecodeAvailable = false;
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    const preload = createdImages[0];
    const oldLoad = preload.onload;
    const oldError = preload.onerror;

    wrapper.unmount();
    expect(preload.src).toBe('');
    expect(preload.onload).toBeNull();
    expect(preload.onerror).toBeNull();
    oldLoad?.();
    oldError?.();
    await advanceTimers(500);
    await flushAsync();

    expect(createdImages).toHaveLength(1);
  });

  it('shows the existing placeholder only when Medium fails', async () => {
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('error');
    await wrapper.get('[role="img"]').trigger('click');

    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').text()).toContain('Image unavailable');
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('does not close when the counter is clicked', async () => {
    const wrapper = mountViewer(2);

    await wrapper.get('.post-media-viewer__counter').trigger('click');

    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('navigates on horizontal swipe without closing after the browser click', async () => {
    const wrapper = mountViewer(2);
    const stage = wrapper.get('.post-media-viewer__stage');

    await stage.trigger('pointerdown', { clientX: 200, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 120, clientY: 105 });
    await stage.trigger('click');

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('suppresses backdrop close for a small drag', async () => {
    const wrapper = mountViewer(2);
    const stage = wrapper.get('.post-media-viewer__stage');

    await stage.trigger('pointerdown', { clientX: 100, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 115, clientY: 103 });
    await stage.trigger('click');

    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/0-medium.jpg');
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  it('allows an independent backdrop click after a previous drag', async () => {
    const wrapper = mountViewer(1);
    const stage = wrapper.get('.post-media-viewer__stage');
    const mediaPane = wrapper.get('.post-media-viewer__media-pane');

    await stage.trigger('pointerdown', { clientX: 100, clientY: 100 });
    await stage.trigger('pointerup', { clientX: 115, clientY: 103 });
    await stage.trigger('click');
    await mediaPane.trigger('pointerdown', { clientX: 20, clientY: 20 });
    await mediaPane.trigger('pointerup', { clientX: 20, clientY: 20 });
    await mediaPane.trigger('click');

    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('shows the next Medium immediately and blocks an old Large race', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    resolveDecode('/media/0-large.jpg');
    await nextTick();
    await Promise.resolve();
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    resolveDecode('/media/1-large.jpg');
    await nextTick();
    await Promise.resolve();
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-large.jpg');
  });

  it('falls back to Medium if the visible Large later emits an error', async () => {
    vi.useFakeTimers();
    setDecodeMode('/media/0-large.jpg', 'resolve');
    const wrapper = mountViewer(1);
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-large.jpg');
    await wrapper.get('img').trigger('error');

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
  });

  it('preserves zoom and pan while the same media upgrades from Medium to Large', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 100, 150);
    const transformBeforeUpgrade = imageTransform(wrapper);

    resolveDecode('/media/0-large.jpg');
    await nextTick();
    await Promise.resolve();

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-large.jpg');
    expect(imageTransform(wrapper)).toBe(transformBeforeUpgrade);
  });

  it('preserves zoom and pan when a visible Large image falls back to Medium', async () => {
    vi.useFakeTimers();
    setDecodeMode('/media/0-large.jpg', 'resolve');
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 2, 300, 150);
    await triggerPointer(wrapper, 'pointerup', 1, 100, 150);
    const transformBeforeFallback = imageTransform(wrapper);

    await wrapper.get('img').trigger('error');

    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
    expect(imageTransform(wrapper)).toBe(transformBeforeFallback);
  });

  it('does not start a stale Large preload after navigating before dwell expires', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(100);
    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');

    await advanceTimers(500);
    expect(decodeCalls).toEqual([]);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    expect(decodeCalls).toEqual(['/media/1-large.jpg']);
    expect(decodeCalls).not.toContain('/media/0-large.jpg');
  });

  it('only starts Large for the settled image during rapid A to B to C navigation', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(3);

    await wrapper.get('img').trigger('load');
    await advanceTimers(50);
    await wrapper.get('[aria-label="Next image"]').trigger('click');

    await wrapper.get('img').trigger('load');
    await advanceTimers(50);
    await wrapper.get('[aria-label="Next image"]').trigger('click');

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(decodeCalls).toEqual(['/media/2-large.jpg']);
  });

  it('cancels the pending Large upgrade when the viewer is closed', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('load');
    await advanceTimers(100);
    await wrapper.get('[aria-label="Close image viewer"]').trigger('click');
    await advanceTimers(500);

    expect(createdImages).toHaveLength(0);
    expect(decodeCalls).toEqual([]);
  });

  it('cancels the pending Large upgrade when the viewer is unmounted', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(1);

    await wrapper.get('img').trigger('load');
    await advanceTimers(100);
    wrapper.unmount();
    await advanceTimers(500);

    expect(createdImages).toHaveLength(0);
    expect(decodeCalls).toEqual([]);
  });

  it('allows a canceled Large preload to retry when navigating back', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);
    expect(createdImages).toHaveLength(1);

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(createdImages[0].src).toBe('');
    rejectDecode('/media/0-large.jpg');
    await nextTick();
    await Promise.resolve();

    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(decodeCalls.filter(url => url === '/media/0-large.jpg')).toHaveLength(2);
  });

  it('remembers a genuine current Large failure without repeated retries', async () => {
    vi.useFakeTimers();
    setDecodeMode('/media/0-large.jpg', 'reject');
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('load');
    await advanceTimers(250);
    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    await wrapper.get('[aria-label="Previous image"]').trigger('click');
    await wrapper.get('img').trigger('load');
    await advanceTimers(250);

    expect(decodeCalls).toEqual(['/media/0-large.jpg']);
    expect(wrapper.get('img').attributes('src')).toBe('/media/0-medium.jpg');
  });

  it('accelerates Large loading on zoom intent after Medium is ready', async () => {
    vi.useFakeTimers();
    const wrapper = mountViewer(1);
    setStableGeometry(wrapper);

    await wrapper.get('img').trigger('load');
    await advanceTimers(100);
    expect(decodeCalls).toEqual([]);

    await triggerPointer(wrapper, 'pointerdown', 1, 100, 150);
    await triggerPointer(wrapper, 'pointerdown', 2, 200, 150);
    await triggerPointer(wrapper, 'pointermove', 2, 300, 150);

    expect(createdImages).toHaveLength(1);
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);

    await advanceTimers(500);
    expect(createdImages).toHaveLength(1);
    expect(decodeCalls).toEqual(['/media/0-large.jpg']);
  });

  it('shows an accessible placeholder after an image fails and keeps navigation available', async () => {
    const wrapper = mountViewer(2);

    await wrapper.get('img').trigger('error');
    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('[role="img"]').attributes('aria-label')).toBe('Image unavailable');
    expect(wrapper.get('[role="img"]').text()).toContain('Image unavailable');

    await wrapper.get('[aria-label="Next image"]').trigger('click');
    expect(wrapper.get('.post-media-viewer__image').attributes('src')).toBe('/media/1-medium.jpg');
  });

  it('removes the global keyboard listener on unmount', () => {
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const wrapper = mountViewer();

    wrapper.unmount();

    expect(removeEventListener).toHaveBeenCalledWith('keydown', expect.any(Function));
    removeEventListener.mockRestore();
  });
});
