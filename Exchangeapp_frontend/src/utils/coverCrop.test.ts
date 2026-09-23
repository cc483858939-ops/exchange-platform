// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  CoverCropError,
  centeredCoverCropState,
  createCoverCropGeometry,
  createCroppedCover,
  decodeCoverImage,
} from './coverCrop';

const makeFile = (name = 'cover.jpg', type = 'image/jpeg') => new File(['source'], name, { type });

const cropRequest = (
  source: File,
  naturalWidth = 1600,
  naturalHeight = 900,
  viewportWidth = 450,
) => {
  const geometry = createCoverCropGeometry(viewportWidth, naturalWidth, naturalHeight)!;
  const state = centeredCoverCropState(geometry);
  return {
    source,
    viewportWidth,
    naturalWidth,
    naturalHeight,
    ...state,
  };
};

const mockDecodedBitmap = (width: number, height: number) => {
  const close = vi.fn();
  const bitmap = { width, height, close };
  const createImageBitmap = vi.fn().mockResolvedValue(bitmap);
  vi.stubGlobal('createImageBitmap', createImageBitmap);
  return { bitmap, close, createImageBitmap };
};

const mockCanvas = (blobFactory: (type?: string) => Blob) => {
  const drawImage = vi.fn();
  const getContext = vi.spyOn(HTMLCanvasElement.prototype, 'getContext')
    .mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D);
  const toBlob = vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((callback, type) => {
    callback(blobFactory(type));
  });
  return { drawImage, getContext, toBlob };
};

describe('cover crop geometry and source limits', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('derives a fixed 3:1 viewport and centers the crop', () => {
    const geometry = createCoverCropGeometry(450, 1600, 900)!;
    expect(geometry).toMatchObject({
      viewportWidth: 450,
      viewportHeight: 150,
      minScale: 450 / 1600,
      maxScale: (450 / 1600) * 4,
    });
    expect(centeredCoverCropState(geometry)).toEqual({
      scale: geometry.minScale,
      offsetX: 0,
      offsetY: (150 - 900 * geometry.minScale) / 2,
    });
    expect(createCoverCropGeometry(0, 1600, 900)).toBeNull();
  });

  it('allows the exact 25MP source boundary and an 8192px dimension', async () => {
    const exactPixelLimit = mockDecodedBitmap(5000, 5000);
    const decodedSquare = await decodeCoverImage(makeFile());
    expect(decodedSquare).toMatchObject({ naturalWidth: 5000, naturalHeight: 5000 });
    decodedSquare.dispose();
    expect(exactPixelLimit.close).toHaveBeenCalledTimes(1);

    vi.unstubAllGlobals();
    const dimensionLimit = mockDecodedBitmap(8192, 3000);
    const decodedWide = await decodeCoverImage(makeFile());
    expect(decodedWide).toMatchObject({ naturalWidth: 8192, naturalHeight: 3000 });
    decodedWide.dispose();
    expect(dimensionLimit.createImageBitmap).toHaveBeenCalledTimes(1);
  });

  it.each([
    [8193, 1000],
    [5001, 5000],
  ])('rejects a source of %i×%i and closes its ImageBitmap', async (width, height) => {
    const { close } = mockDecodedBitmap(width, height);
    const result = decodeCoverImage(makeFile());

    await expect(result).rejects.toBeInstanceOf(CoverCropError);
    await expect(result).rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('preserves EXIF-aware decoding and does not fall back after bitmap dimensions exceed limits', async () => {
    const close = vi.fn();
    const createImageBitmap = vi.fn().mockResolvedValue({ width: 5001, height: 5000, close });
    const imageFallback = vi.fn();
    vi.stubGlobal('createImageBitmap', createImageBitmap);
    vi.stubGlobal('Image', imageFallback);
    const file = makeFile();

    await expect(decodeCoverImage(file)).rejects.toMatchObject({ code: 'SOURCE_TOO_LARGE' });

    expect(createImageBitmap).toHaveBeenCalledWith(file, { imageOrientation: 'from-image' });
    expect(close).toHaveBeenCalledTimes(1);
    expect(imageFallback).not.toHaveBeenCalled();
  });
});

describe('createCroppedCover output', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('creates an exact 1500×500 JPEG from a landscape crop', async () => {
    const { bitmap, close } = mockDecodedBitmap(1600, 900);
    const { drawImage } = mockCanvas(type => new Blob(['encoded'], { type }));
    const file = makeFile('landscape.jpg', 'image/jpeg');

    const result = await createCroppedCover(cropRequest(file));

    expect(result).toMatchObject({ name: 'landscape-cover-cropped.jpg', type: 'image/jpeg', size: 7 });
    expect(result.size).toBeLessThanOrEqual(5 * 1024 * 1024);
    expect(drawImage).toHaveBeenCalledWith(
      bitmap,
      0,
      expect.closeTo(183.3333333333),
      1600,
      expect.closeTo(533.3333333333),
      0,
      0,
      1500,
      500,
    );
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('does not upscale the selected crop', async () => {
    mockDecodedBitmap(900, 400);
    const { drawImage } = mockCanvas(type => new Blob(['small'], { type }));

    const result = await createCroppedCover(cropRequest(makeFile(), 900, 400));

    expect(result.size).toBeGreaterThan(0);
    expect(drawImage).toHaveBeenLastCalledWith(
      expect.anything(),
      0,
      50,
      900,
      300,
      0,
      0,
      900,
      300,
    );
  });

  it('limits a large source crop to 1500×500', async () => {
    mockDecodedBitmap(6000, 3000);
    const { drawImage } = mockCanvas(type => new Blob(['large'], { type }));

    await createCroppedCover(cropRequest(makeFile(), 6000, 3000));

    expect(drawImage).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
      expect.any(Number),
      0,
      0,
      1500,
      500,
    );
  });

  it.each([
    ['image/png', 'photo-cover-cropped.png', 'image/png'],
    ['image/webp', 'photo-cover-cropped.png', 'image/png'],
    ['image/jpeg', 'photo-cover-cropped.jpg', 'image/jpeg'],
  ])('preserves supported format behavior for %s input', async (inputType, name, outputType) => {
    mockDecodedBitmap(1600, 900);
    mockCanvas(type => new Blob(['encoded'], { type }));
    const file = makeFile(`photo.${inputType === 'image/jpeg' ? 'jpg' : inputType.slice(6)}`, inputType);

    const result = await createCroppedCover(cropRequest(file));

    expect(result).toMatchObject({ name, type: outputType });
  });

  it.each([
    ['zero-byte', new Blob([], { type: 'image/jpeg' })],
    ['over-limit', new Blob([new Uint8Array((5 * 1024 * 1024) + 1)], { type: 'image/jpeg' })],
  ])('rejects %s generated output', async (_label, blob) => {
    mockDecodedBitmap(1600, 900);
    mockCanvas(() => blob);

    await expect(createCroppedCover(cropRequest(makeFile()))).rejects.toMatchObject({
      name: 'CoverCropError',
      code: 'OUTPUT_FAILED',
    });
  });
});
