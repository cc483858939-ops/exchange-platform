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

export type AvatarCropSource = {
  source: CanvasImageSource;
  naturalWidth: number;
  naturalHeight: number;
  dispose: () => void;
};

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

const defaultOutputSize = 512;
const maxOutputBytes = 2 * 1024 * 1024;

const isPositiveFinite = (value: number) => Number.isFinite(value) && value > 0;

export const createAvatarCropGeometry = (
  cropSize: number,
  naturalWidth: number,
  naturalHeight: number,
): AvatarCropGeometry | null => {
  if (
    !isPositiveFinite(cropSize)
    || !isPositiveFinite(naturalWidth)
    || !isPositiveFinite(naturalHeight)
  ) {
    return null;
  }

  const minScale = Math.max(cropSize / naturalWidth, cropSize / naturalHeight);
  return {
    cropSize,
    naturalWidth,
    naturalHeight,
    minScale,
    maxScale: minScale * 4,
  };
};

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export const clampAvatarCropState = (
  state: AvatarCropState,
  geometry: AvatarCropGeometry,
): AvatarCropState => {
  const scale = clamp(
    Number.isFinite(state.scale) && state.scale > 0 ? state.scale : geometry.minScale,
    geometry.minScale,
    geometry.maxScale,
  );
  const displayWidth = geometry.naturalWidth * scale;
  const displayHeight = geometry.naturalHeight * scale;
  const minX = geometry.cropSize - displayWidth;
  const minY = geometry.cropSize - displayHeight;

  return {
    scale,
    offsetX: clamp(Number.isFinite(state.offsetX) ? state.offsetX : 0, minX, 0),
    offsetY: clamp(Number.isFinite(state.offsetY) ? state.offsetY : 0, minY, 0),
  };
};

export const centeredAvatarCropState = (
  geometry: AvatarCropGeometry,
): AvatarCropState => clampAvatarCropState({
  scale: geometry.minScale,
  offsetX: (geometry.cropSize - geometry.naturalWidth * geometry.minScale) / 2,
  offsetY: (geometry.cropSize - geometry.naturalHeight * geometry.minScale) / 2,
}, geometry);

export const zoomAvatarCropState = (
  state: AvatarCropState,
  nextScale: number,
  geometry: AvatarCropGeometry,
): AvatarCropState => {
  const current = clampAvatarCropState(state, geometry);
  const scale = clamp(nextScale, geometry.minScale, geometry.maxScale);
  const center = geometry.cropSize / 2;
  const sourceCenterX = (center - current.offsetX) / current.scale;
  const sourceCenterY = (center - current.offsetY) / current.scale;

  return clampAvatarCropState({
    scale,
    offsetX: center - sourceCenterX * scale,
    offsetY: center - sourceCenterY * scale,
  }, geometry);
};

export const avatarCropSourceRect = (
  state: AvatarCropState,
  geometry: AvatarCropGeometry,
) => {
  const clamped = clampAvatarCropState(state, geometry);
  const sourceX = -clamped.offsetX / clamped.scale;
  const sourceY = -clamped.offsetY / clamped.scale;
  return {
    x: sourceX === 0 ? 0 : sourceX,
    y: sourceY === 0 ? 0 : sourceY,
    width: geometry.cropSize / clamped.scale,
    height: geometry.cropSize / clamped.scale,
  };
};

const revokeObjectURL = (url: string) => {
  if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
    URL.revokeObjectURL(url);
  }
};

const decodeWithImageElement = async (source: Blob): Promise<AvatarCropSource> => {
  if (
    typeof URL === 'undefined'
    || typeof URL.createObjectURL !== 'function'
    || typeof Image === 'undefined'
  ) {
    throw new Error('This image could not be opened.');
  }

  const sourceURL = URL.createObjectURL(source);
  let image: HTMLImageElement | null = null;
  try {
    image = new Image();
    image.decoding = 'async';
    await new Promise<void>((resolve, reject) => {
      image!.onload = () => resolve();
      image!.onerror = () => reject(new Error('This image could not be opened.'));
      image!.src = sourceURL;
    });
    if (typeof image.decode === 'function') {
      await image.decode();
    }

    const naturalWidth = image.naturalWidth || image.width;
    const naturalHeight = image.naturalHeight || image.height;
    if (!isPositiveFinite(naturalWidth) || !isPositiveFinite(naturalHeight)) {
      throw new Error('This image could not be opened.');
    }

    return {
      source: image,
      naturalWidth,
      naturalHeight,
      dispose: () => undefined,
    };
  } finally {
    revokeObjectURL(sourceURL);
  }
};

export const decodeAvatarImage = async (source: Blob): Promise<AvatarCropSource> => {
  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(source, { imageOrientation: 'from-image' });
      if (!isPositiveFinite(bitmap.width) || !isPositiveFinite(bitmap.height)) {
        bitmap.close();
        throw new Error('This image could not be opened.');
      }
      return {
        source: bitmap,
        naturalWidth: bitmap.width,
        naturalHeight: bitmap.height,
        dispose: () => bitmap.close(),
      };
    } catch {
      // Fall back to the browser image decoder when ImageBitmap is unavailable.
    }
  }

  return decodeWithImageElement(source);
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
  if (!geometry || !isPositiveFinite(outputSize)) {
    throw new Error('Could not prepare this photo. Try another image.');
  }

  const decoded = await decodeAvatarImage(request.source);
  try {
    const crop = avatarCropSourceRect({
      scale: request.scale,
      offsetX: request.offsetX,
      offsetY: request.offsetY,
    }, geometry);
    if (typeof document === 'undefined') {
      throw new Error('Could not prepare this photo. Try another image.');
    }
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(outputSize);
    canvas.height = Math.round(outputSize);
    const context = canvas.getContext('2d');
    if (!context) {
      throw new Error('Could not prepare this photo. Try another image.');
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
          : reject(new Error('Could not prepare this photo. Try another image.')),
        format.type,
        format.type === 'image/jpeg' ? 0.9 : undefined,
      );
    });
    if (blob.size <= 0 || blob.size > maxOutputBytes) {
      throw new Error('Could not prepare this photo. Try another image.');
    }

    return new File([blob], outputName(request.source, format.extension), {
      type: format.type,
      lastModified: Date.now(),
    });
  } finally {
    decoded.dispose();
  }
};
