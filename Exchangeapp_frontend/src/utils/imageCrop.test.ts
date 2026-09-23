// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  centeredImageCropState,
  clampImageCropState,
  createImageCropGeometry,
  decodeImage,
  imageCropSourceRect,
  remapImageCropState,
  zoomImageCropState,
} from './imageCrop';

describe('rectangular image crop geometry', () => {
  it('rejects invalid dimensions and derives the minimum scale for a rectangular viewport', () => {
    expect(createImageCropGeometry(0, 150, 1600, 900)).toBeNull();
    expect(createImageCropGeometry(Number.POSITIVE_INFINITY, 150, 1600, 900)).toBeNull();
    expect(createImageCropGeometry(450, 150, 0, 900)).toBeNull();

    const geometry = createImageCropGeometry(450, 150, 1600, 900);
    expect(geometry).toEqual({
      viewportWidth: 450,
      viewportHeight: 150,
      naturalWidth: 1600,
      naturalHeight: 900,
      minScale: 450 / 1600,
      maxScale: (450 / 1600) * 4,
    });
  });

  it('centers the selected crop and clamps both axes without exposing empty space', () => {
    const geometry = createImageCropGeometry(450, 150, 1600, 900)!;
    const centered = centeredImageCropState(geometry);

    expect(centered.scale).toBe(450 / 1600);
    expect(centered.offsetX).toBe(0);
    expect(centered.offsetY).toBeCloseTo((150 - 900 * centered.scale) / 2);
    expect(clampImageCropState({ ...centered, offsetX: -500 }, geometry).offsetX).toBe(0);
    expect(clampImageCropState({ ...centered, offsetY: 400 }, geometry).offsetY).toBe(0);
    expect(clampImageCropState({ ...centered, offsetY: -900 }, geometry).offsetY)
      .toBeCloseTo(150 - 900 * centered.scale);
  });

  it('keeps portrait images covered and clamps their vertical movement', () => {
    const geometry = createImageCropGeometry(450, 150, 900, 1600)!;
    const centered = centeredImageCropState(geometry);
    const top = clampImageCropState({ ...centered, offsetY: 100 }, geometry);
    const bottom = clampImageCropState({ ...centered, offsetY: -10_000 }, geometry);

    expect(centered.scale).toBe(0.5);
    expect(centered.offsetY).toBe(-325);
    expect(top.offsetY).toBe(0);
    expect(bottom.offsetY).toBe(150 - 1600 * 0.5);
    expect(imageCropSourceRect(bottom, geometry).y).toBe(1300);
  });

  it('preserves the source point under the viewport center while zooming', () => {
    const geometry = createImageCropGeometry(450, 150, 1600, 900)!;
    const centered = centeredImageCropState(geometry);
    const beforeCenter = {
      x: (geometry.viewportWidth / 2 - centered.offsetX) / centered.scale,
      y: (geometry.viewportHeight / 2 - centered.offsetY) / centered.scale,
    };
    const zoomed = zoomImageCropState(centered, geometry.minScale * 2, geometry);
    const afterCenter = {
      x: (geometry.viewportWidth / 2 - zoomed.offsetX) / zoomed.scale,
      y: (geometry.viewportHeight / 2 - zoomed.offsetY) / zoomed.scale,
    };

    expect(afterCenter.x).toBeCloseTo(beforeCenter.x);
    expect(afterCenter.y).toBeCloseTo(beforeCenter.y);
    expect(zoomed.scale).toBe(geometry.minScale * 2);
  });

  it('remaps zoom and crop center when the viewport is resized', () => {
    const oldGeometry = createImageCropGeometry(450, 150, 1600, 900)!;
    const newGeometry = createImageCropGeometry(330, 110, 1600, 900)!;
    const centered = centeredImageCropState(oldGeometry);
    const zoomed = zoomImageCropState(centered, oldGeometry.minScale * 2.5, oldGeometry);
    const oldCenter = {
      x: (oldGeometry.viewportWidth / 2 - zoomed.offsetX) / zoomed.scale,
      y: (oldGeometry.viewportHeight / 2 - zoomed.offsetY) / zoomed.scale,
    };
    const remapped = remapImageCropState(zoomed, oldGeometry, newGeometry);
    const newCenter = {
      x: (newGeometry.viewportWidth / 2 - remapped.offsetX) / remapped.scale,
      y: (newGeometry.viewportHeight / 2 - remapped.offsetY) / remapped.scale,
    };

    expect(remapped.scale).toBeCloseTo(newGeometry.minScale + 0.5 * (newGeometry.maxScale - newGeometry.minScale));
    expect(newCenter.x).toBeCloseTo(oldCenter.x);
    expect(newCenter.y).toBeCloseTo(oldCenter.y);
  });
});

describe('generic image decoding', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uses EXIF-aware ImageBitmap decoding and closes the bitmap on disposal', async () => {
    const close = vi.fn();
    const createImageBitmap = vi.fn().mockResolvedValue({ width: 1200, height: 800, close });
    vi.stubGlobal('createImageBitmap', createImageBitmap);
    const file = new File(['image'], 'photo.jpg', { type: 'image/jpeg' });

    const decoded = await decodeImage(file, { maxDimension: 8192, maxPixels: 25_000_000 });

    expect(createImageBitmap).toHaveBeenCalledWith(file, { imageOrientation: 'from-image' });
    expect(decoded).toMatchObject({ naturalWidth: 1200, naturalHeight: 800 });
    decoded.dispose();
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('falls back to HTMLImageElement when ImageBitmap creation fails and revokes its URL', async () => {
    const onCreateObjectURL = vi.fn(() => 'blob:source');
    const onRevokeObjectURL = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockRejectedValue(new Error('unsupported')));
    vi.stubGlobal('URL', {
      createObjectURL: onCreateObjectURL,
      revokeObjectURL: onRevokeObjectURL,
    });
    class MockImage {
      decoding = '';
      naturalWidth = 1000;
      naturalHeight = 600;
      width = 0;
      height = 0;
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      decode = vi.fn(async () => undefined);
      set src(_value: string) {
        queueMicrotask(() => this.onload?.());
      }
    }
    vi.stubGlobal('Image', MockImage);
    const file = new File(['image'], 'photo.jpg', { type: 'image/jpeg' });

    const decoded = await decodeImage(file, { maxDimension: 8192, maxPixels: 25_000_000 });

    expect(decoded).toMatchObject({ naturalWidth: 1000, naturalHeight: 600 });
    expect(onCreateObjectURL).toHaveBeenCalledWith(file);
    expect(onRevokeObjectURL).toHaveBeenCalledWith('blob:source');
  });

  it('closes and rejects an oversized bitmap without HTML image fallback', async () => {
    const close = vi.fn();
    const imageFallback = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue({ width: 5001, height: 5000, close }));
    vi.stubGlobal('Image', imageFallback);

    await expect(decodeImage(new Blob(['image']), { maxDimension: 8192, maxPixels: 25_000_000 }))
      .rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });

    expect(close).toHaveBeenCalledTimes(1);
    expect(imageFallback).not.toHaveBeenCalled();
  });
});
