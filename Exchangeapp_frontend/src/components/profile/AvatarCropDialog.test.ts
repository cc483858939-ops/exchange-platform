// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import AvatarCropDialog from './AvatarCropDialog.vue';
import { AvatarCropError, createAvatarCropGeometry } from '../../utils/avatarCrop';

const mocks = vi.hoisted(() => ({
  decodeAvatarImage: vi.fn(),
  createCroppedAvatar: vi.fn(),
}));

vi.mock('../../utils/avatarCrop', async importOriginal => {
  const actual = await importOriginal<typeof import('../../utils/avatarCrop')>();
  return {
    ...actual,
    decodeAvatarImage: mocks.decodeAvatarImage,
    createCroppedAvatar: mocks.createCroppedAvatar,
  };
});

const originalCreateObjectURL = URL.createObjectURL;
const originalRevokeObjectURL = URL.revokeObjectURL;

beforeAll(() => {
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
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: originalCreateObjectURL,
    });
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL');
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: originalRevokeObjectURL,
    });
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL');
  }
});

const sourceFile = () => new File(['source'], 'avatar.webp', { type: 'image/webp' });

const mountDialog = (file = sourceFile()) => mount(AvatarCropDialog, {
  props: { file },
});

const settle = async () => {
  await flushPromises();
  await Promise.resolve();
};

const readCropState = (wrapper: VueWrapper, cropSize: number) => {
  const image = wrapper.get('.avatar-crop-dialog__image').element as HTMLImageElement;
  const transform = image.style.transform.match(/translate3d\(([-\d.]+)px, ([-\d.]+)px, 0\)/);
  if (!transform) throw new Error('Crop image transform not found');

  const scale = Number.parseFloat(image.style.width) / 1600;
  const offsetX = Number(transform[1]);
  const offsetY = Number(transform[2]);
  const geometry = createAvatarCropGeometry(cropSize, 1600, 900)!;
  return {
    scale,
    offsetX,
    offsetY,
    zoomRatio: (scale - geometry.minScale) / (geometry.maxScale - geometry.minScale),
    sourceCenterX: (cropSize / 2 - offsetX) / scale,
    sourceCenterY: (cropSize / 2 - offsetY) / scale,
  };
};

describe('AvatarCropDialog', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.decodeAvatarImage.mockResolvedValue({
      source: {} as CanvasImageSource,
      naturalWidth: 1600,
      naturalHeight: 900,
      dispose: vi.fn(),
    });
    mocks.createCroppedAvatar.mockResolvedValue(
      new File(['cropped'], 'avatar-cropped.png', { type: 'image/png' }),
    );
  });

  it('opens with a centered crop and exposes accessible zoom controls', async () => {
    const wrapper = mountDialog();
    await settle();

    expect(wrapper.get('dialog').attributes('open')).toBeDefined();
    expect(wrapper.get('.avatar-crop-dialog__image').attributes('src')).toBe('blob:avatar.webp');
    expect(wrapper.get('.avatar-crop-dialog__viewport').find('.avatar-crop-dialog__circle').exists()).toBe(true);
    expect(wrapper.get('#avatar-crop-zoom').attributes('aria-label')).toBe('Zoom photo');
    expect(wrapper.get('.avatar-crop-dialog__image').attributes('style')).toContain('translate3d(-140px, 0px, 0)');
  });

  it('clamps drag, zooms through the slider, and resets to the default position', async () => {
    const wrapper = mountDialog();
    await settle();
    const viewport = wrapper.get('.avatar-crop-dialog__viewport');
    const image = wrapper.get('.avatar-crop-dialog__image');

    await viewport.trigger('pointerdown', { pointerId: 1, clientX: 100, clientY: 100 });
    await viewport.trigger('pointermove', { pointerId: 1, clientX: -900, clientY: 100 });
    await viewport.trigger('pointerup', { pointerId: 1, clientX: -900, clientY: 100 });
    expect(image.attributes('style')).toContain('translate3d(-280px, 0px, 0)');

    await wrapper.get('#avatar-crop-zoom').setValue('1');
    expect(image.attributes('style')).toContain('width: 2560px');
    await wrapper.get('.avatar-crop-dialog__reset').trigger('click');
    expect(image.attributes('style')).toContain('width: 640px');
    expect(image.attributes('style')).toContain('translate3d(-140px, 0px, 0)');
  });

  it('preserves crop center and normalized zoom when the viewport is resized', async () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const wrapper = mountDialog();
    const viewport = wrapper.get('.avatar-crop-dialog__viewport').element;
    Object.defineProperty(viewport, 'clientWidth', { configurable: true, value: 360 });
    await settle();

    await wrapper.get('#avatar-crop-zoom').setValue('0.5');
    await wrapper.get('.avatar-crop-dialog__viewport').trigger('pointerdown', {
      pointerId: 7,
      clientX: 100,
      clientY: 100,
    });
    await wrapper.get('.avatar-crop-dialog__viewport').trigger('pointermove', {
      pointerId: 7,
      clientX: 125,
      clientY: 85,
    });
    await wrapper.get('.avatar-crop-dialog__viewport').trigger('pointerup', {
      pointerId: 7,
      clientX: 125,
      clientY: 85,
    });

    const beforeResize = readCropState(wrapper, 360);
    Object.defineProperty(viewport, 'clientWidth', { configurable: true, value: 300 });
    window.dispatchEvent(new Event('resize'));
    await wrapper.vm.$nextTick();

    const afterResize = readCropState(wrapper, 300);
    expect(afterResize.zoomRatio).toBeCloseTo(beforeResize.zoomRatio);
    expect(afterResize.sourceCenterX).toBeCloseTo(beforeResize.sourceCenterX);
    expect(afterResize.sourceCenterY).toBeCloseTo(beforeResize.sourceCenterY);

    await wrapper.get('.avatar-crop-dialog__reset').trigger('click');
    const afterReset = readCropState(wrapper, 300);
    expect(afterReset.zoomRatio).toBeCloseTo(0);
    expect(afterReset.offsetX).toBeCloseTo((300 - 1600 * createAvatarCropGeometry(300, 1600, 900)!.minScale) / 2);
    expect(afterReset.offsetY).toBeCloseTo(0);
    wrapper.unmount();
  });

  it('cancels without applying and applies a generated file only after Apply', async () => {
    const wrapper = mountDialog();
    await settle();

    const cancelEvent = new Event('cancel', { cancelable: true });
    wrapper.get('dialog').element.dispatchEvent(cancelEvent);
    await settle();
    expect(cancelEvent.defaultPrevented).toBe(true);
    expect(wrapper.emitted('cancel')).toHaveLength(1);
    expect(mocks.createCroppedAvatar).not.toHaveBeenCalled();

    await wrapper.get('.avatar-crop-dialog__button--secondary').trigger('click');
    expect(wrapper.emitted('cancel')).toHaveLength(2);

    await wrapper.get('.avatar-crop-dialog__button--primary').trigger('click');
    await settle();
    expect(mocks.createCroppedAvatar).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted('apply')?.[0]?.[0]).toBeInstanceOf(File);
  });

  it('keeps the dialog open and blocks duplicate Apply while preparing', async () => {
    let resolve!: (file: File) => void;
    mocks.createCroppedAvatar.mockReturnValue(new Promise<File>(res => { resolve = res; }));
    const wrapper = mountDialog();
    await settle();

    const apply = wrapper.get('.avatar-crop-dialog__button--primary');
    await apply.trigger('click');
    await apply.trigger('click');
    expect(mocks.createCroppedAvatar).toHaveBeenCalledTimes(1);
    expect(apply.attributes('disabled')).toBeDefined();

    resolve(new File(['cropped'], 'avatar-cropped.jpg', { type: 'image/jpeg' }));
    await settle();
    expect(wrapper.emitted('apply')).toHaveLength(1);
  });

  it('allows Cancel while preparing and ignores a stale Apply after unmount', async () => {
    let resolve!: (file: File) => void;
    mocks.createCroppedAvatar.mockReturnValue(new Promise<File>(res => { resolve = res; }));
    const wrapper = mountDialog();
    await settle();

    await wrapper.get('.avatar-crop-dialog__button--primary').trigger('click');
    expect(wrapper.get('.avatar-crop-dialog__button--primary').text()).toBe('Preparing…');
    expect(wrapper.get('.avatar-crop-dialog__button--secondary').attributes('disabled')).toBeUndefined();

    await wrapper.get('.avatar-crop-dialog__button--secondary').trigger('click');
    expect(wrapper.emitted('cancel')).toHaveLength(1);

    wrapper.unmount();
    resolve(new File(['cropped'], 'avatar-cropped.jpg', { type: 'image/jpeg' }));
    await settle();
    expect(wrapper.emitted('apply')).toBeUndefined();
  });

  it('allows the Close button to cancel while preparing', async () => {
    let resolve!: (file: File) => void;
    mocks.createCroppedAvatar.mockReturnValue(new Promise<File>(res => { resolve = res; }));
    const wrapper = mountDialog();
    await settle();

    await wrapper.get('.avatar-crop-dialog__button--primary').trigger('click');
    await wrapper.get('.avatar-crop-dialog__close').trigger('click');

    expect(wrapper.emitted('cancel')).toHaveLength(1);
    wrapper.unmount();
    resolve(new File(['cropped'], 'avatar-cropped.jpg', { type: 'image/jpeg' }));
    await settle();
  });

  it('shows a dedicated message for an oversized decoded source', async () => {
    mocks.decodeAvatarImage.mockRejectedValue(new AvatarCropError('SOURCE_TOO_LARGE'));
    const wrapper = mountDialog();
    await settle();

    expect(wrapper.get('[role="alert"]').text()).toBe('This photo is too large. Choose a smaller image.');
  });

  it('reports decode failures without closing', async () => {
    mocks.decodeAvatarImage.mockRejectedValue(new Error('decode failed'));
    const wrapper = mountDialog();
    await settle();

    expect(wrapper.get('[role="alert"]').text()).toBe('This image could not be opened. Try another photo.');
    expect(wrapper.get('.avatar-crop-dialog').attributes('open')).toBeDefined();
    expect(wrapper.get('.avatar-crop-dialog__button--primary').attributes('disabled')).toBeDefined();
  });

  it('revokes the source preview URL on unmount', async () => {
    const file = sourceFile();
    const wrapper = mountDialog(file);
    await settle();

    expect(URL.createObjectURL).toHaveBeenCalledWith(file);
    wrapper.unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:avatar.webp');
  });

  it('revokes the old source preview URL when the file changes', async () => {
    const first = sourceFile();
    const second = new File(['second'], 'second.webp', { type: 'image/webp' });
    const wrapper = mountDialog(first);
    await settle();

    await wrapper.setProps({ file: second });
    await settle();

    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:avatar.webp');
    expect(URL.createObjectURL).toHaveBeenCalledWith(second);
    wrapper.unmount();
  });
});
