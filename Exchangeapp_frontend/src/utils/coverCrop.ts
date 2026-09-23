import {
  clampImageCropState,
  centeredImageCropState,
  createImageCropGeometry,
  decodeImage,
  imageCropSourceRect,
  isImageCropError,
  remapImageCropState,
  zoomImageCropState,
  type ImageCropErrorCode,
  type ImageCropGeometry,
  type ImageCropState,
} from './imageCrop';

export const coverCropAspectRatio = 3;
export const coverCropMaxOutputWidth = 1500;
export const coverCropMaxOutputHeight = 500;
export const coverCropMaxSourceDimension = 8192;
export const coverCropMaxSourcePixels = 25_000_000;
export const coverCropMaxOutputBytes = 5 * 1024 * 1024;

export type CoverCropState = ImageCropState;
export type CoverCropGeometry = ImageCropGeometry;
export type CoverCropErrorCode = ImageCropErrorCode;

export type CoverCropSource = {
  source: CanvasImageSource;
  naturalWidth: number;
  naturalHeight: number;
  dispose: () => void;
};

export type CoverCropRequest = {
  source: Blob;
  viewportWidth: number;
  naturalWidth: number;
  naturalHeight: number;
  scale: number;
  offsetX: number;
  offsetY: number;
};

export class CoverCropError extends Error {
  constructor(public readonly code: CoverCropErrorCode) {
    super(code);
    this.name = 'CoverCropError';
  }
}

export const isCoverCropError = (
  error: unknown,
  code?: CoverCropErrorCode,
): error is CoverCropError => (
  error instanceof CoverCropError
  && (code === undefined || error.code === code)
);

const coverDecodeLimits = {
  maxDimension: coverCropMaxSourceDimension,
  maxPixels: coverCropMaxSourcePixels,
};

export const createCoverCropGeometry = (
  viewportWidth: number,
  naturalWidth: number,
  naturalHeight: number,
): CoverCropGeometry | null => createImageCropGeometry(
  viewportWidth,
  viewportWidth / coverCropAspectRatio,
  naturalWidth,
  naturalHeight,
);

export const clampCoverCropState = (
  state: CoverCropState,
  geometry: CoverCropGeometry,
): CoverCropState => clampImageCropState(state, geometry);

export const centeredCoverCropState = (
  geometry: CoverCropGeometry,
): CoverCropState => centeredImageCropState(geometry);

export const zoomCoverCropState = (
  state: CoverCropState,
  nextScale: number,
  geometry: CoverCropGeometry,
): CoverCropState => zoomImageCropState(state, nextScale, geometry);

export const remapCoverCropState = (
  state: CoverCropState,
  oldGeometry: CoverCropGeometry,
  newGeometry: CoverCropGeometry,
): CoverCropState => remapImageCropState(state, oldGeometry, newGeometry);

export const coverCropSourceRect = (
  state: CoverCropState,
  geometry: CoverCropGeometry,
) => imageCropSourceRect(state, geometry);

export const decodeCoverImage = async (source: Blob): Promise<CoverCropSource> => {
  try {
    return await decodeImage(source, coverDecodeLimits);
  } catch (error) {
    if (isImageCropError(error)) {
      throw new CoverCropError(error.code);
    }
    if (error instanceof CoverCropError) {
      throw error;
    }
    throw new CoverCropError('DECODE_FAILED');
  }
};

const outputFormat = (source: Blob) => (
  source.type === 'image/jpeg'
    ? { type: 'image/jpeg', extension: 'jpg' }
    : { type: 'image/png', extension: 'png' }
);

const outputName = (source: Blob, extension: string) => {
  const candidate = source instanceof File ? source.name : 'cover';
  const base = candidate.replace(/\.[^./\\]+$/, '').trim() || 'cover';
  return `${base}-cover-cropped.${extension}`;
};

const createCoverOutput = (
  source: CanvasImageSource,
  crop: ReturnType<typeof coverCropSourceRect>,
) => {
  const cappedWidth = Math.min(coverCropMaxOutputWidth, Math.floor(crop.width));
  const outputWidth = cappedWidth - (cappedWidth % coverCropAspectRatio);
  const outputHeight = outputWidth / coverCropAspectRatio;
  if (
    !Number.isFinite(outputWidth)
    || outputWidth < coverCropAspectRatio
    || outputHeight > coverCropMaxOutputHeight
  ) {
    throw new CoverCropError('OUTPUT_FAILED');
  }

  if (typeof document === 'undefined') {
    throw new CoverCropError('OUTPUT_FAILED');
  }
  const canvas = document.createElement('canvas');
  canvas.width = outputWidth;
  canvas.height = outputHeight;
  const context = canvas.getContext('2d');
  if (!context) {
    throw new CoverCropError('OUTPUT_FAILED');
  }
  context.drawImage(
    source,
    crop.x,
    crop.y,
    crop.width,
    crop.height,
    0,
    0,
    canvas.width,
    canvas.height,
  );
  return { canvas, outputWidth, outputHeight };
};

export const createCroppedCover = async (request: CoverCropRequest): Promise<File> => {
  const geometry = createCoverCropGeometry(
    request.viewportWidth,
    request.naturalWidth,
    request.naturalHeight,
  );
  if (!geometry) {
    throw new CoverCropError('OUTPUT_FAILED');
  }

  const crop = coverCropSourceRect({
    scale: request.scale,
    offsetX: request.offsetX,
    offsetY: request.offsetY,
  }, geometry);
  const decoded = await decodeCoverImage(request.source);
  try {
    const { canvas } = createCoverOutput(decoded.source, crop);
    const format = outputFormat(request.source);
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        candidate => candidate
          ? resolve(candidate)
          : reject(new CoverCropError('OUTPUT_FAILED')),
        format.type,
        format.type === 'image/jpeg' ? 0.9 : undefined,
      );
    });
    if (blob.size <= 0 || blob.size > coverCropMaxOutputBytes) {
      throw new CoverCropError('OUTPUT_FAILED');
    }

    return new File([blob], outputName(request.source, format.extension), {
      type: format.type,
      lastModified: Date.now(),
    });
  } catch (error) {
    if (error instanceof CoverCropError) {
      throw error;
    }
    throw new CoverCropError('OUTPUT_FAILED');
  } finally {
    decoded.dispose();
  }
};
