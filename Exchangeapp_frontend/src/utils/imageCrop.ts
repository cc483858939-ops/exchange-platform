export type ImageCropState = {
  scale: number;
  offsetX: number;
  offsetY: number;
};

export type ImageCropGeometry = {
  viewportWidth: number;
  viewportHeight: number;
  naturalWidth: number;
  naturalHeight: number;
  minScale: number;
  maxScale: number;
};

export type ImageCropSource = {
  source: CanvasImageSource;
  naturalWidth: number;
  naturalHeight: number;
  dispose: () => void;
};

export type ImageCropErrorCode = 'SOURCE_TOO_LARGE' | 'DECODE_FAILED' | 'OUTPUT_FAILED';

export type ImageDecodeLimits = {
  maxDimension: number;
  maxPixels: number;
};

export class ImageCropError extends Error {
  constructor(public readonly code: ImageCropErrorCode) {
    super(code);
    this.name = 'ImageCropError';
  }
}

export const isImageCropError = (
  error: unknown,
  code?: ImageCropErrorCode,
): error is ImageCropError => (
  error instanceof ImageCropError
  && (code === undefined || error.code === code)
);

const isPositiveFinite = (value: number) => Number.isFinite(value) && value > 0;
const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export const createImageCropGeometry = (
  viewportWidth: number,
  viewportHeight: number,
  naturalWidth: number,
  naturalHeight: number,
): ImageCropGeometry | null => {
  if (
    !isPositiveFinite(viewportWidth)
    || !isPositiveFinite(viewportHeight)
    || !isPositiveFinite(naturalWidth)
    || !isPositiveFinite(naturalHeight)
  ) {
    return null;
  }

  const minScale = Math.max(viewportWidth / naturalWidth, viewportHeight / naturalHeight);
  const maxScale = minScale * 4;
  if (!isPositiveFinite(minScale) || !isPositiveFinite(maxScale)) {
    return null;
  }

  return {
    viewportWidth,
    viewportHeight,
    naturalWidth,
    naturalHeight,
    minScale,
    maxScale,
  };
};

export const clampImageCropState = (
  state: ImageCropState,
  geometry: ImageCropGeometry,
): ImageCropState => {
  const scale = clamp(
    Number.isFinite(state.scale) && state.scale > 0 ? state.scale : geometry.minScale,
    geometry.minScale,
    geometry.maxScale,
  );
  const displayWidth = geometry.naturalWidth * scale;
  const displayHeight = geometry.naturalHeight * scale;
  const minX = geometry.viewportWidth - displayWidth;
  const minY = geometry.viewportHeight - displayHeight;

  return {
    scale,
    offsetX: clamp(Number.isFinite(state.offsetX) ? state.offsetX : 0, minX, 0),
    offsetY: clamp(Number.isFinite(state.offsetY) ? state.offsetY : 0, minY, 0),
  };
};

export const centeredImageCropState = (geometry: ImageCropGeometry): ImageCropState => (
  clampImageCropState({
    scale: geometry.minScale,
    offsetX: (geometry.viewportWidth - geometry.naturalWidth * geometry.minScale) / 2,
    offsetY: (geometry.viewportHeight - geometry.naturalHeight * geometry.minScale) / 2,
  }, geometry)
);

export const zoomImageCropState = (
  state: ImageCropState,
  nextScale: number,
  geometry: ImageCropGeometry,
): ImageCropState => {
  const current = clampImageCropState(state, geometry);
  const scale = clamp(nextScale, geometry.minScale, geometry.maxScale);
  const centerX = geometry.viewportWidth / 2;
  const centerY = geometry.viewportHeight / 2;
  const sourceCenterX = (centerX - current.offsetX) / current.scale;
  const sourceCenterY = (centerY - current.offsetY) / current.scale;

  return clampImageCropState({
    scale,
    offsetX: centerX - sourceCenterX * scale,
    offsetY: centerY - sourceCenterY * scale,
  }, geometry);
};

export const imageCropSourceRect = (
  state: ImageCropState,
  geometry: ImageCropGeometry,
) => {
  const clamped = clampImageCropState(state, geometry);
  const x = -clamped.offsetX / clamped.scale;
  const y = -clamped.offsetY / clamped.scale;
  return {
    x: x === 0 ? 0 : x,
    y: y === 0 ? 0 : y,
    width: geometry.viewportWidth / clamped.scale,
    height: geometry.viewportHeight / clamped.scale,
  };
};

export const remapImageCropState = (
  state: ImageCropState,
  oldGeometry: ImageCropGeometry,
  newGeometry: ImageCropGeometry,
): ImageCropState => {
  const current = clampImageCropState(state, oldGeometry);
  const oldScaleRange = oldGeometry.maxScale - oldGeometry.minScale;
  const zoomRatio = oldScaleRange > 0
    ? clamp((current.scale - oldGeometry.minScale) / oldScaleRange, 0, 1)
    : 0;
  const newScale = newGeometry.minScale
    + zoomRatio * (newGeometry.maxScale - newGeometry.minScale);
  const oldCenterX = oldGeometry.viewportWidth / 2;
  const oldCenterY = oldGeometry.viewportHeight / 2;
  const sourceCenterX = (oldCenterX - current.offsetX) / current.scale;
  const sourceCenterY = (oldCenterY - current.offsetY) / current.scale;
  const newCenterX = newGeometry.viewportWidth / 2;
  const newCenterY = newGeometry.viewportHeight / 2;

  return clampImageCropState({
    scale: newScale,
    offsetX: newCenterX - sourceCenterX * newScale,
    offsetY: newCenterY - sourceCenterY * newScale,
  }, newGeometry);
};

const validateImageDimensions = (
  width: number,
  height: number,
  limits: ImageDecodeLimits,
) => {
  if (!isPositiveFinite(width) || !isPositiveFinite(height)) {
    throw new ImageCropError('DECODE_FAILED');
  }
  if (
    width > limits.maxDimension
    || height > limits.maxDimension
    || width > limits.maxPixels / height
  ) {
    throw new ImageCropError('SOURCE_TOO_LARGE');
  }
};

const revokeObjectURL = (url: string) => {
  if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
    URL.revokeObjectURL(url);
  }
};

const decodeWithImageElement = async (
  source: Blob,
  limits: ImageDecodeLimits,
): Promise<ImageCropSource> => {
  if (
    typeof URL === 'undefined'
    || typeof URL.createObjectURL !== 'function'
    || typeof Image === 'undefined'
  ) {
    throw new ImageCropError('DECODE_FAILED');
  }

  const sourceURL = URL.createObjectURL(source);
  let image: HTMLImageElement | null = null;
  try {
    image = new Image();
    image.decoding = 'async';
    await new Promise<void>((resolve, reject) => {
      image!.onload = () => resolve();
      image!.onerror = () => reject(new ImageCropError('DECODE_FAILED'));
      image!.src = sourceURL;
    });
    if (typeof image.decode === 'function') {
      await image.decode();
    }

    const naturalWidth = image.naturalWidth || image.width;
    const naturalHeight = image.naturalHeight || image.height;
    validateImageDimensions(naturalWidth, naturalHeight, limits);

    return {
      source: image,
      naturalWidth,
      naturalHeight,
      dispose: () => undefined,
    };
  } catch (error) {
    if (isImageCropError(error)) {
      throw error;
    }
    throw new ImageCropError('DECODE_FAILED');
  } finally {
    revokeObjectURL(sourceURL);
  }
};

export const decodeImage = async (
  source: Blob,
  limits: ImageDecodeLimits,
): Promise<ImageCropSource> => {
  if (typeof createImageBitmap === 'function') {
    let bitmap: ImageBitmap;
    try {
      bitmap = await createImageBitmap(source, { imageOrientation: 'from-image' });
    } catch (error) {
      if (isImageCropError(error)) {
        throw error;
      }
      return decodeWithImageElement(source, limits);
    }

    try {
      validateImageDimensions(bitmap.width, bitmap.height, limits);
      return {
        source: bitmap,
        naturalWidth: bitmap.width,
        naturalHeight: bitmap.height,
        dispose: () => bitmap.close(),
      };
    } catch (error) {
      bitmap.close();
      if (isImageCropError(error)) {
        throw error;
      }
      throw new ImageCropError('DECODE_FAILED');
    }
  }

  return decodeWithImageElement(source, limits);
};
