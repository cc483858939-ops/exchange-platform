// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  avatarCropSourceRect,
  centeredAvatarCropState,
  clampAvatarCropState,
  createAvatarCropGeometry,
  createCroppedAvatar,
  decodeAvatarImage,
  remapAvatarCropState,
  zoomAvatarCropState,
} from './avatarCrop';

describe('avatar crop geometry', () => {
  it('calculates a landscape minimum scale and clamps horizontal dragging', () => {
    const geometry = createAvatarCropGeometry(360, 1600, 900);
    expect(geometry).toMatchObject({ minScale: 0.4, maxScale: 1.6 });

    const centered = centeredAvatarCropState(geometry!);
    expect(centered).toEqual({ scale: 0.4, offsetX: -140, offsetY: 0 });
    expect(clampAvatarCropState({ ...centered, offsetX: 1000 }, geometry!)).toEqual({
      ...centered,
      offsetX: 0,
    });
    expect(clampAvatarCropState({ ...centered, offsetX: -1000 }, geometry!)).toEqual({
      ...centered,
      offsetX: -280,
    });
  });

  it('clamps a portrait crop on the vertical axis', () => {
    const geometry = createAvatarCropGeometry(360, 900, 1600)!;
    const centered = centeredAvatarCropState(geometry);
    expect(centered).toEqual({ scale: 0.4, offsetX: 0, offsetY: -140 });
    expect(clampAvatarCropState({ ...centered, offsetY: 1000 }, geometry)).toEqual({
      ...centered,
      offsetY: 0,
    });
    expect(clampAvatarCropState({ ...centered, offsetY: -1000 }, geometry)).toEqual({
      ...centered,
      offsetY: -280,
    });
  });

  it('keeps a square image centered and preserves the crop center during zoom', () => {
    const geometry = createAvatarCropGeometry(360, 1000, 1000)!;
    const centered = centeredAvatarCropState(geometry);
    expect(centered).toEqual({ scale: 0.36, offsetX: 0, offsetY: 0 });

    const zoomed = zoomAvatarCropState(centered, geometry.maxScale, geometry);
    expect(zoomed).toEqual({ scale: 1.44, offsetX: -540, offsetY: -540 });
    expect(avatarCropSourceRect(zoomed, geometry)).toEqual({
      x: 375,
      y: 375,
      width: 250,
      height: 250,
    });
  });

  it('preserves normalized zoom and source center when the avatar viewport changes size', () => {
    const oldGeometry = createAvatarCropGeometry(360, 1600, 900)!;
    const newGeometry = createAvatarCropGeometry(300, 1600, 900)!;
    let state = centeredAvatarCropState(oldGeometry);
    state = zoomAvatarCropState(state, oldGeometry.minScale * 2.5, oldGeometry);
    state = clampAvatarCropState({
      ...state,
      offsetX: state.offsetX - 40,
      offsetY: state.offsetY - 20,
    }, oldGeometry);

    const oldSourceCenter = {
      x: (oldGeometry.cropSize / 2 - state.offsetX) / state.scale,
      y: (oldGeometry.cropSize / 2 - state.offsetY) / state.scale,
    };
    const oldZoomRatio = (state.scale - oldGeometry.minScale)
      / (oldGeometry.maxScale - oldGeometry.minScale);
    const remapped = remapAvatarCropState(state, oldGeometry, newGeometry);
    const newSourceCenter = {
      x: (newGeometry.cropSize / 2 - remapped.offsetX) / remapped.scale,
      y: (newGeometry.cropSize / 2 - remapped.offsetY) / remapped.scale,
    };
    const newZoomRatio = (remapped.scale - newGeometry.minScale)
      / (newGeometry.maxScale - newGeometry.minScale);

    expect(newZoomRatio).toBeCloseTo(oldZoomRatio);
    expect(newSourceCenter.x).toBeCloseTo(oldSourceCenter.x);
    expect(newSourceCenter.y).toBeCloseTo(oldSourceCenter.y);
  });

  it('keeps a remapped edge crop clamped to the resized viewport', () => {
    const oldGeometry = createAvatarCropGeometry(360, 1600, 900)!;
    const newGeometry = createAvatarCropGeometry(300, 1600, 900)!;
    const edgeState = clampAvatarCropState({
      scale: oldGeometry.maxScale,
      offsetX: -100_000,
      offsetY: 100_000,
    }, oldGeometry);

    const remapped = remapAvatarCropState(edgeState, oldGeometry, newGeometry);
    const displayWidth = newGeometry.naturalWidth * remapped.scale;
    const displayHeight = newGeometry.naturalHeight * remapped.scale;

    expect(displayWidth).toBeGreaterThanOrEqual(newGeometry.cropSize);
    expect(displayHeight).toBeGreaterThanOrEqual(newGeometry.cropSize);
    expect(remapped.offsetX).toBeLessThanOrEqual(0);
    expect(remapped.offsetY).toBeLessThanOrEqual(0);
    expect(remapped.offsetX).toBeGreaterThanOrEqual(newGeometry.cropSize - displayWidth);
    expect(remapped.offsetY).toBeGreaterThanOrEqual(newGeometry.cropSize - displayHeight);
  });
});

describe('decodeAvatarImage source limits', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('rejects a source wider than 8192 pixels and closes the bitmap', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({
      width: 8193,
      height: 1000,
      close,
    }));

    await expect(decodeAvatarImage(new File(['source'], 'wide.webp', { type: 'image/webp' })))
      .rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('rejects a source taller than 8192 pixels and closes the bitmap', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({
      width: 1000,
      height: 8193,
      close,
    }));

    await expect(decodeAvatarImage(new File(['source'], 'tall.webp', { type: 'image/webp' })))
      .rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('rejects a source larger than 20 megapixels and closes the bitmap', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({
      width: 5000,
      height: 5000,
      close,
    }));

    await expect(decodeAvatarImage(new File(['source'], 'large.webp', { type: 'image/webp' })))
      .rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('allows the exact 20 megapixel boundary and preserves EXIF orientation decoding', async () => {
    const close = vi.fn();
    const createImageBitmap = vi.fn().mockResolvedValue({
      width: 5000,
      height: 4000,
      close,
    });
    vi.stubGlobal('createImageBitmap', createImageBitmap);

    const file = new File(['source'], 'boundary.webp', { type: 'image/webp' });
    const decoded = await decodeAvatarImage(file);

    expect(decoded).toMatchObject({ naturalWidth: 5000, naturalHeight: 4000 });
    expect(createImageBitmap).toHaveBeenCalledWith(file, { imageOrientation: 'from-image' });
    decoded.dispose();
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('does not fall back to HTMLImageElement after an oversized ImageBitmap', async () => {
    const close = vi.fn();
    const fallbackImage = vi.fn();
    const createObjectURL = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({
      width: 5000,
      height: 5000,
      close,
    }));
    vi.stubGlobal('Image', fallbackImage);
    vi.stubGlobal('URL', {
      createObjectURL,
      revokeObjectURL: vi.fn(),
    });

    await expect(decodeAvatarImage(new File(['source'], 'large.webp', { type: 'image/webp' })))
      .rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });
    expect(fallbackImage).not.toHaveBeenCalled();
    expect(createObjectURL).not.toHaveBeenCalled();
  });
});

describe('createCroppedAvatar', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('renders a 512 square JPEG for a landscape source', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({
      width: 1600,
      height: 900,
      close,
    }));
    const drawImage = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((callback, type) => {
      callback(new Blob(['encoded'], { type }));
    });

    const file = new File(['source'], 'landscape.jpg', { type: 'image/jpeg' });
    const result = await createCroppedAvatar({
      source: file,
      cropSize: 360,
      naturalWidth: 1600,
      naturalHeight: 900,
      scale: 0.4,
      offsetX: -140,
      offsetY: 0,
    });

    expect(result).toBeInstanceOf(File);
    expect(result).toMatchObject({ name: 'landscape-cropped.jpg', type: 'image/jpeg' });
    expect(drawImage).toHaveBeenCalledWith(
      expect.anything(),
      350,
      0,
      900,
      900,
      0,
      0,
      512,
      512,
    );
    expect(close).toHaveBeenCalledTimes(1);
  });
});
