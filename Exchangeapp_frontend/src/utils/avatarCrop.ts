import {
  clampImageCropState,
  centeredImageCropState,
  createImageCropGeometry,
  decodeImage,
  imageCropSourceRect,
  isImageCropError,
  zoomImageCropState,
  type ImageCropGeometry,
  type ImageCropSource,
} from './imageCrop';

export type AvatarCropState = {
  scale: number;
  offsetX: number;
  offsetY: number;
};

export type AvatarCropGeometry = {
  cropSize: number;
  naturalWidth: number;
  naturalHeight: number;
  minScale: number;
  maxScale: number;
};

export type AvatarCropSource = ImageCropSource;

export type AvatarCropRequest = {
  source: Blob;
  cropSize: number;
  naturalWidth: number;
  naturalHeight: number;
  scale: number;
  offsetX: number;
  offsetY: number;
  outputSize?: number;
};

export type AvatarCropErrorCode = 'SOURCE_TOO_LARGE' | 'DECODE_FAILED' | 'OUTPUT_FAILED';

export class AvatarCropError extends Error {
  constructor(public readonly code: AvatarCropErrorCode) {
    super(code);
    this.name = 'AvatarCropError';
  }
}

export const isAvatarCropError = (
  error: unknown,
  code?: AvatarCropErrorCode,
): error is AvatarCropError => (
  error instanceof AvatarCropError
  && (code === undefined || error.code === code)
);

const defaultOutputSize = 512;
const maxOutputBytes = 2 * 1024 * 1024;
const maxSourceDimension = 8192;
const maxSourcePixels = 20_000_000;
const avatarDecodeLimits = { maxDimension: maxSourceDimension, maxPixels: maxSourcePixels };

const toImageCropGeometry = (geometry: AvatarCropGeometry): ImageCropGeometry => ({
  viewportWidth: geometry.cropSize,
  viewportHeight: geometry.cropSize,
  naturalWidth: geometry.naturalWidth,
  naturalHeight: geometry.naturalHeight,
  minScale: geometry.minScale,
  maxScale: geometry.maxScale,
});

export const createAvatarCropGeometry = (
  cropSize: number,
  naturalWidth: number,
  naturalHeight: number,
): AvatarCropGeometry | null => {
  const geometry = createImageCropGeometry(cropSize, cropSize, naturalWidth, naturalHeight);
  if (!geometry) return null;
  return {
    cropSize,
    naturalWidth,
    naturalHeight,
    minScale: geometry.minScale,
    maxScale: geometry.maxScale,
  };
};

export const clampAvatarCropState = (
  state: AvatarCropState,
  geometry: AvatarCropGeometry,
): AvatarCropState => clampImageCropState(state, toImageCropGeometry(geometry));

export const centeredAvatarCropState = (
  geometry: AvatarCropGeometry,
): AvatarCropState => centeredImageCropState(toImageCropGeometry(geometry));

export const zoomAvatarCropState = (
  state: AvatarCropState,
  nextScale: number,
  geometry: AvatarCropGeometry,
): AvatarCropState => zoomImageCropState(state, nextScale, toImageCropGeometry(geometry));

export const avatarCropSourceRect = (
  state: AvatarCropState,
  geometry: AvatarCropGeometry,
) => imageCropSourceRect(state, toImageCropGeometry(geometry));

export const decodeAvatarImage = async (source: Blob): Promise<AvatarCropSource> => {
  try {
    return await decodeImage(source, avatarDecodeLimits);
  } catch (error) {
    if (error instanceof AvatarCropError) {
      throw error;
    }
    if (isImageCropError(error)) {
      throw new AvatarCropError(error.code);
    }
    throw new AvatarCropError('DECODE_FAILED');
  }
};

const outputFormat = (source: Blob) => (
  source.type === 'image/png' || source.type === 'image/webp'
    ? { type: 'image/png', extension: 'png' }
    : { type: 'image/jpeg', extension: 'jpg' }
);

const outputName = (source: Blob, extension: string) => {
  const candidate = source instanceof File ? source.name : 'avatar';
  const base = candidate.replace(/\.[^./\\]+$/, '').trim() || 'avatar';
  return `${base}-cropped.${extension}`;
};

export const createCroppedAvatar = async (request: AvatarCropRequest): Promise<File> => {
  const outputSize = request.outputSize ?? defaultOutputSize;
  const geometry = createAvatarCropGeometry(
    request.cropSize,
    request.naturalWidth,
    request.naturalHeight,
  );
  if (!geometry || !Number.isFinite(outputSize) || outputSize <= 0) {
    throw new AvatarCropError('OUTPUT_FAILED');
  }

  const decoded = await decodeAvatarImage(request.source);
  try {
    const crop = avatarCropSourceRect({
      scale: request.scale,
      offsetX: request.offsetX,
      offsetY: request.offsetY,
    }, geometry);
    if (typeof document === 'undefined') {
      throw new AvatarCropError('OUTPUT_FAILED');
    }
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(outputSize);
    canvas.height = Math.round(outputSize);
    const context = canvas.getContext('2d');
    if (!context) {
      throw new AvatarCropError('OUTPUT_FAILED');
    }
    context.drawImage(
      decoded.source,
      crop.x,
      crop.y,
      crop.width,
      crop.height,
      0,
      0,
      canvas.width,
      canvas.height,
    );

    const format = outputFormat(request.source);
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        candidate => candidate
          ? resolve(candidate)
          : reject(new AvatarCropError('OUTPUT_FAILED')),
        format.type,
        format.type === 'image/jpeg' ? 0.9 : undefined,
      );
    });
    if (blob.size <= 0 || blob.size > maxOutputBytes) {
      throw new AvatarCropError('OUTPUT_FAILED');
    }

    return new File([blob], outputName(request.source, format.extension), {
      type: format.type,
      lastModified: Date.now(),
    });
  } finally {
    decoded.dispose();
  }
};
