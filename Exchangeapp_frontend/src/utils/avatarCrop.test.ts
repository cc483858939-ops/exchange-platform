// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  avatarCropSourceRect,
  centeredAvatarCropState,
  clampAvatarCropState,
  createAvatarCropGeometry,
  createCroppedAvatar,
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
