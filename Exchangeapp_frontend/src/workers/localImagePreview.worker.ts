import { calculatePreviewDimensions } from '../utils/localImagePreview';

type WorkerRequest = {
  jobID: number;
  file: File;
  maxSide: number;
};

type WorkerResponse =
  | {
      jobID: number;
      type: 'success';
      blob: Blob;
      width: number;
      height: number;
    }
  | {
      jobID: number;
      type: 'error';
      message: string;
    };

type WorkerScope = {
  onmessage: ((event: MessageEvent<WorkerRequest>) => void) | null;
  postMessage: (message: WorkerResponse) => void;
};

const workerScope = self as unknown as WorkerScope;

workerScope.onmessage = async ({ data }) => {
  let bitmap: ImageBitmap | null = null;
  try {
    bitmap = await createImageBitmap(data.file, { imageOrientation: 'from-image' });
    const dimensions = calculatePreviewDimensions(bitmap.width, bitmap.height, data.maxSide);
    if (!dimensions) {
      throw new Error('The image has invalid dimensions.');
    }

    const canvas = new OffscreenCanvas(dimensions.width, dimensions.height);
    const context = canvas.getContext('2d');
    if (!context) {
      throw new Error('This browser cannot render a local image preview.');
    }
    context.drawImage(bitmap, 0, 0, dimensions.width, dimensions.height);
    const blob = await canvas.convertToBlob({ type: 'image/webp', quality: 0.82 });
    workerScope.postMessage({
      jobID: data.jobID,
      type: 'success',
      blob,
      width: dimensions.width,
      height: dimensions.height,
    });
  } catch (error) {
    workerScope.postMessage({
      jobID: data.jobID,
      type: 'error',
      message: error instanceof Error ? error.message : 'The image preview could not be prepared.',
    });
  } finally {
    bitmap?.close();
    bitmap = null;
  }
};
