// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import CoverCropDialog from './CoverCropDialog.vue';
import { CoverCropError, createCoverCropGeometry, centeredCoverCropState } from '../../utils/coverCrop';

const mocks = vi.hoisted(() => ({
  decodeCoverImage: vi.fn(),
  createCroppedCover: vi.fn(),
}));

vi.mock('../../utils/coverCrop', async importOriginal => {
  const actual = await importOriginal<typeof import('../../utils/coverCrop')>();
  return {
    ...actual,
    decodeCoverImage: mocks.decodeCoverImage,
    createCroppedCover: mocks.createCroppedCover,
  };
});

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalCreateObjectURL = URL.createObjectURL;
const originalRevokeObjectURL = URL.revokeObjectURL;

beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: vi.fn((file: File) => `blob:${file.name}`),
  });
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: vi.fn(),
  });
});

afterAll(() => {
  if (originalShowModal) {
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: originalCreateObjectURL });
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL');
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: originalRevokeObjectURL });
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL');
  }
});

const settle = async () => {
  await flushPromises();
  await Promise.resolve();
};

const sourceFile = (name = 'cover.webp') => new File(['cover'], name, { type: 'image/webp' });
const outputFile = () => new File(['cropped'], 'cover-cover-cropped.png', { type: 'image/png' });

const mountDialog = (file = sourceFile()) => mount(CoverCropDialog, {
  props: { file },
});

const readImageState = (wrapper: VueWrapper) => {
  const image = wrapper.get('.cover-crop-dialog__image').element as HTMLImageElement;
  const match = image.style.transform.match(/translate3d\(([-\d.]+)px, ([-\d.]+)px, 0\)/);
  if (!match) throw new Error('Crop image transform not found');
  return {
    width: Number.parseFloat(image.style.width),
    height: Number.parseFloat(image.style.height),
    offsetX: Number(match[1]),
    offsetY: Number(match[2]),
  };
};

const dispatchPointer = (
  element: Element,
  type: string,
  pointerId: number,
  clientX: number,
  clientY: number,
) => {
  const event = new Event(type, { bubbles: true });
  Object.defineProperties(event, {
    pointerId: { value: pointerId },
    clientX: { value: clientX },
    clientY: { value: clientY },
  });
  element.dispatchEvent(event);
};

const dispatchKeyboard = (element: Element, key: string, shiftKey = false) => {
  const event = new KeyboardEvent('keydown', {
    key,
    shiftKey,
    bubbles: true,
    cancelable: true,
  });
  element.dispatchEvent(event);
  return event;
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

describe('CoverCropDialog', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.decodeCoverImage.mockResolvedValue({
      source: {},
      naturalWidth: 1600,
      naturalHeight: 900,
      dispose: vi.fn(),
    });
    mocks.createCroppedCover.mockResolvedValue(outputFile());
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('opens with the required controls and a centered minimum-scale crop', async () => {
    wrapper = mountDialog();
    await settle();

    expect(wrapper.get('.cover-crop-dialog').attributes('open')).toBeDefined();
    expect(wrapper.get('#cover-crop-title').text()).toBe('Edit cover');
    const viewport = wrapper.get('.cover-crop-dialog__viewport');
    expect(viewport.attributes()).toMatchObject({
      role: 'group',
      'aria-label': 'Cover crop preview',
      'aria-describedby': 'cover-crop-keyboard-help',
      tabindex: '0',
    });
    expect(wrapper.get('#cover-crop-keyboard-help').text()).toBe(
      'Use the arrow keys to move the photo. Hold Shift while pressing an arrow key to move it farther.',
    );
    expect(wrapper.get('input[type="range"]').attributes()).toMatchObject({
      min: '0',
      max: '1',
      step: '0.001',
      'aria-label': 'Zoom photo',
    });
    expect(wrapper.get('.cover-crop-dialog__reset').text()).toBe('Reset');
    expect(wrapper.get('.cover-crop-dialog__actions').text()).toContain('Cancel');
    expect(wrapper.get('.cover-crop-dialog__apply').text()).toBe('Apply');

    const geometry = createCoverCropGeometry(460, 1600, 900)!;
    const centered = centeredCoverCropState(geometry);
    expect(readImageState(wrapper)).toMatchObject({
      width: 1600 * centered.scale,
      height: 900 * centered.scale,
      offsetX: centered.offsetX,
      offsetY: centered.offsetY,
    });
  });

  it('moves the crop with every arrow, supports Shift steps, and stays within bounds', async () => {
    wrapper = mountDialog();
    await settle();
    const viewport = wrapper.get('.cover-crop-dialog__viewport').element;

    let before = readImageState(wrapper);
    let event = dispatchKeyboard(viewport, 'ArrowUp');
    await wrapper.vm.$nextTick();
    let after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetY).toBeCloseTo(before.offsetY - 8);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowDown');
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetY).toBeCloseTo(before.offsetY + 8);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowUp', true);
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetY).toBeCloseTo(before.offsetY - 32);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowDown', true);
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetY).toBeCloseTo(before.offsetY + 32);

    before = after;
    for (const key of ['ArrowLeft', 'ArrowRight']) {
      event = dispatchKeyboard(viewport, key);
      expect(event.defaultPrevented).toBe(true);
    }
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(after.offsetX).toBeCloseTo(before.offsetX);

    await wrapper.get('input[type="range"]').setValue('0.5');
    before = readImageState(wrapper);
    event = dispatchKeyboard(viewport, 'ArrowLeft');
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetX).toBeCloseTo(before.offsetX - 8);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowRight');
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetX).toBeCloseTo(before.offsetX + 8);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowLeft', true);
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetX).toBeCloseTo(before.offsetX - 32);

    before = after;
    event = dispatchKeyboard(viewport, 'ArrowRight', true);
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetX).toBeCloseTo(before.offsetX + 32);

    before = after;
    event = dispatchKeyboard(viewport, 'Enter');
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(false);
    expect(after.offsetX).toBeCloseTo(before.offsetX);
    expect(after.offsetY).toBeCloseTo(before.offsetY);

    for (let index = 0; index < 100; index += 1) {
      event = dispatchKeyboard(viewport, 'ArrowUp');
    }
    await wrapper.vm.$nextTick();
    after = readImageState(wrapper);
    expect(event.defaultPrevented).toBe(true);
    expect(after.offsetY).toBeCloseTo(460 / 3 - after.height);

    await wrapper.get('.cover-crop-dialog__apply').trigger('click');
    await settle();
    const finalRequest = mocks.createCroppedCover.mock.calls[mocks.createCroppedCover.mock.calls.length - 1]![0];
    expect(finalRequest.offsetX).toBeCloseTo(after.offsetX);
    expect(finalRequest.offsetY).toBeCloseTo(after.offsetY);
  });

  it('keeps the viewport out of the tab order until the source loads', async () => {
    type DecodedCover = {
      source: CanvasImageSource;
      naturalWidth: number;
      naturalHeight: number;
      dispose: () => void;
    };
    let resolve!: (decoded: DecodedCover) => void;
    mocks.decodeCoverImage.mockReturnValue(new Promise<DecodedCover>(res => { resolve = res; }));
    wrapper = mountDialog();
    const viewport = wrapper.get('.cover-crop-dialog__viewport');
    expect(viewport.attributes('tabindex')).toBe('-1');

    resolve({ source: {} as CanvasImageSource, naturalWidth: 1600, naturalHeight: 900, dispose: vi.fn() });
    await settle();
    expect(viewport.attributes('tabindex')).toBe('0');
  });

  it('drags the image while clamping the crop to source pixels', async () => {
    wrapper = mountDialog();
    await settle();
    const viewport = wrapper.get('.cover-crop-dialog__viewport').element;
    const geometry = createCoverCropGeometry(460, 1600, 900)!;

    dispatchPointer(viewport, 'pointerdown', 11, 100, 100);
    dispatchPointer(viewport, 'pointermove', 11, 180, -200);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('.cover-crop-dialog__viewport').classes()).toContain('cover-crop-dialog__viewport--dragging');
    dispatchPointer(viewport, 'pointerup', 11, 180, -200);
    await wrapper.vm.$nextTick();

    const state = readImageState(wrapper);
    expect(state.offsetX).toBe(0);
    expect(state.offsetY).toBe(geometry.viewportHeight - geometry.naturalHeight * state.height / geometry.naturalHeight);
    expect(wrapper.get('.cover-crop-dialog__viewport').classes()).not.toContain('cover-crop-dialog__viewport--dragging');
  });

  it('zooms around the current crop center and Reset returns to centered minimum zoom', async () => {
    wrapper = mountDialog();
    await settle();
    const geometry = createCoverCropGeometry(460, 1600, 900)!;
    const before = readImageState(wrapper);
    const centerBefore = {
      x: (geometry.viewportWidth / 2 - before.offsetX) / (before.width / geometry.naturalWidth),
      y: (geometry.viewportHeight / 2 - before.offsetY) / (before.height / geometry.naturalHeight),
    };

    const zoom = wrapper.get('input[type="range"]');
    await zoom.setValue('0.5');
    const zoomed = readImageState(wrapper);
    const centerAfter = {
      x: (geometry.viewportWidth / 2 - zoomed.offsetX) / (zoomed.width / geometry.naturalWidth),
      y: (geometry.viewportHeight / 2 - zoomed.offsetY) / (zoomed.height / geometry.naturalHeight),
    };
    expect(centerAfter.x).toBeCloseTo(centerBefore.x);
    expect(centerAfter.y).toBeCloseTo(centerBefore.y);
    expect(zoomed.width).toBeGreaterThan(before.width);

    await wrapper.get('.cover-crop-dialog__reset').trigger('click');
    expect(readImageState(wrapper)).toMatchObject({
      width: 1600 * geometry.minScale,
      height: 900 * geometry.minScale,
      offsetX: centeredCoverCropState(geometry).offsetX,
      offsetY: centeredCoverCropState(geometry).offsetY,
    });
  });

  it('emits the generated file on Apply without disabling close actions during work', async () => {
    const file = sourceFile();
    const result = outputFile();
    const generation = deferred<File>();
    mocks.createCroppedCover.mockReturnValue(generation.promise);
    wrapper = mountDialog(file);
    await settle();

    await wrapper.get('.cover-crop-dialog__apply').trigger('click');
    const viewport = wrapper.get('.cover-crop-dialog__viewport');
    const viewportElement = viewport.element;
    const before = readImageState(wrapper);
    expect(wrapper.get('.cover-crop-dialog__apply').text()).toBe('Preparing…');
    expect(wrapper.get('.cover-crop-dialog__apply').attributes('disabled')).toBeDefined();
    expect(wrapper.get('.cover-crop-dialog__reset').attributes('disabled')).toBeDefined();
    expect(viewport.attributes('tabindex')).toBe('-1');
    dispatchKeyboard(viewportElement, 'ArrowUp');
    await wrapper.vm.$nextTick();
    expect(readImageState(wrapper)).toMatchObject({ offsetX: before.offsetX, offsetY: before.offsetY });
    expect(wrapper.get('.cover-crop-dialog__close').attributes('disabled')).toBeUndefined();
    expect(wrapper.get('.cover-crop-dialog__actions button').attributes('disabled')).toBeUndefined();
    expect(mocks.createCroppedCover).toHaveBeenCalledWith(expect.objectContaining({
      source: file,
      viewportWidth: 460,
      naturalWidth: 1600,
      naturalHeight: 900,
    }));

    generation.resolve(result);
    await settle();
    expect(wrapper.emitted('apply')).toEqual([[result]]);
  });

  it('does not emit a crop that finishes after Cancel', async () => {
    const generation = deferred<File>();
    mocks.createCroppedCover.mockReturnValue(generation.promise);
    wrapper = mountDialog();
    await settle();
    await wrapper.get('.cover-crop-dialog__apply').trigger('click');
    await wrapper.get('.cover-crop-dialog__actions button').trigger('click');
    expect(wrapper.emitted('cancel')).toHaveLength(1);
    wrapper.unmount();
    generation.resolve(outputFile());
    await settle();

    expect(wrapper.emitted('apply')).toBeUndefined();
  });

  it('does not emit a crop that finishes after Close', async () => {
    const generation = deferred<File>();
    mocks.createCroppedCover.mockReturnValue(generation.promise);
    wrapper = mountDialog();
    await settle();
    await wrapper.get('.cover-crop-dialog__apply').trigger('click');
    await wrapper.get('.cover-crop-dialog__close').trigger('click');
    expect(wrapper.emitted('cancel')).toHaveLength(1);
    wrapper.unmount();
    generation.resolve(outputFile());
    await settle();

    expect(wrapper.emitted('apply')).toBeUndefined();
  });

  it('shows the exact oversized-source message and keeps the dialog open', async () => {
    mocks.decodeCoverImage.mockRejectedValueOnce(new CoverCropError('SOURCE_TOO_LARGE'));
    wrapper = mountDialog();
    await settle();

    expect(wrapper.get('.cover-crop-dialog__error').text()).toBe('This cover is too large. Choose a smaller image.');
    expect(wrapper.get('.cover-crop-dialog__apply').attributes('disabled')).toBeDefined();
    expect(wrapper.get('.cover-crop-dialog__viewport').attributes('tabindex')).toBe('-1');
    expect(wrapper.get('.cover-crop-dialog').attributes('open')).toBeDefined();
  });

  it('shows decode and output errors without closing the dialog', async () => {
    mocks.decodeCoverImage.mockRejectedValueOnce(new Error('decode failed'));
    wrapper = mountDialog();
    await settle();
    expect(wrapper.get('.cover-crop-dialog__error').text()).toBe('This image could not be opened. Try another cover.');
    expect(wrapper.get('.cover-crop-dialog').attributes('open')).toBeDefined();
    expect(wrapper.get('.cover-crop-dialog__viewport').attributes('tabindex')).toBe('-1');

    wrapper.unmount();
    wrapper = null;
    mocks.decodeCoverImage.mockResolvedValueOnce({
      source: {},
      naturalWidth: 1600,
      naturalHeight: 900,
      dispose: vi.fn(),
    });
    mocks.createCroppedCover.mockRejectedValueOnce(new Error('canvas failed'));
    wrapper = mountDialog();
    await settle();
    await wrapper.get('.cover-crop-dialog__apply').trigger('click');
    await settle();

    expect(wrapper.get('.cover-crop-dialog__error').text()).toBe('Could not prepare this cover. Try another image.');
    expect(wrapper.get('.cover-crop-dialog__apply').attributes('disabled')).toBeUndefined();
    expect(wrapper.get('.cover-crop-dialog').attributes('open')).toBeDefined();
  });

  it('revokes the previous source URL on file change and revokes the active URL on unmount', async () => {
    const first = sourceFile('first.webp');
    const second = sourceFile('second.webp');
    wrapper = mountDialog(first);
    await settle();

    await wrapper.setProps({ file: second });
    await settle();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:first.webp');
    expect(URL.createObjectURL).toHaveBeenCalledWith(second);

    wrapper.unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:second.webp');
    wrapper = null;
  });

  it('preserves source center and normalized zoom when the viewport is resized', async () => {
    vi.stubGlobal('ResizeObserver', undefined);
    wrapper = mountDialog();
    const viewport = wrapper.get('.cover-crop-dialog__viewport').element as HTMLElement;
    Object.defineProperty(viewport, 'clientWidth', { configurable: true, value: 460 });
    await settle();
    await wrapper.get('input[type="range"]').setValue('0.5');

    const oldGeometry = createCoverCropGeometry(460, 1600, 900)!;
    const oldState = readImageState(wrapper);
    const oldScale = oldState.width / 1600;
    const oldCenter = {
      x: (oldGeometry.viewportWidth / 2 - oldState.offsetX) / oldScale,
      y: (oldGeometry.viewportHeight / 2 - oldState.offsetY) / oldScale,
    };

    Object.defineProperty(viewport, 'clientWidth', { configurable: true, value: 330 });
    window.dispatchEvent(new Event('resize'));
    await wrapper.vm.$nextTick();

    const newGeometry = createCoverCropGeometry(330, 1600, 900)!;
    const newState = readImageState(wrapper);
    const newScale = newState.width / 1600;
    const newCenter = {
      x: (newGeometry.viewportWidth / 2 - newState.offsetX) / newScale,
      y: (newGeometry.viewportHeight / 2 - newState.offsetY) / newScale,
    };
    expect((newScale - newGeometry.minScale) / (newGeometry.maxScale - newGeometry.minScale)).toBeCloseTo(0.5);
    expect(newCenter.x).toBeCloseTo(oldCenter.x);
    expect(newCenter.y).toBeCloseTo(oldCenter.y);
  });
});
