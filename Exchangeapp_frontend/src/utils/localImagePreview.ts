export type PreviewDimensions = {
  width: number;
  height: number;
};

export type LocalImagePreview = PreviewDimensions & {
  blob: Blob;
};

export type LocalImagePreviewGenerator = {
  generate: (file: File) => Promise<LocalImagePreview>;
  dispose: () => void;
};

export type LocalImagePreviewGeneratorOptions = {
  maxSide?: number;
};

const defaultMaxSide = 1024;
const previewQuality = 0.82;
const disposedMessage = 'Image preview preparation was cancelled.';

export const calculatePreviewDimensions = (
  sourceWidth: number,
  sourceHeight: number,
  maxSide = defaultMaxSide,
): PreviewDimensions | null => {
  if (
    !Number.isFinite(sourceWidth)
    || !Number.isFinite(sourceHeight)
    || sourceWidth <= 0
    || sourceHeight <= 0
    || !Number.isFinite(maxSide)
    || maxSide <= 0
  ) {
    return null;
  }

  const scale = Math.min(1, maxSide / Math.max(sourceWidth, sourceHeight));
  let width = Math.max(1, Math.round(sourceWidth * scale));
  let height = Math.max(1, Math.round(sourceHeight * scale));

  if (Math.max(width, height) > maxSide) {
    const correction = maxSide / Math.max(width, height);
    width = Math.max(1, Math.floor(width * correction));
    height = Math.max(1, Math.floor(height * correction));
  }

  return { width, height };
};

const errorMessage = (error: unknown, fallback: string) => (
  error instanceof Error && error.message ? error.message : fallback
);

const revokeObjectURL = (url: string) => {
  if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
    URL.revokeObjectURL(url);
  }
};

const createMainThreadPreview = async (
  file: File,
  maxSide: number,
  registerCancellation?: (cancel: (() => void) | null) => void,
): Promise<LocalImagePreview> => {
  if (
    typeof URL === 'undefined'
    || typeof URL.createObjectURL !== 'function'
    || typeof Image === 'undefined'
    || typeof document === 'undefined'
  ) {
    throw new Error('This browser cannot prepare a local image preview.');
  }

  const sourceURL = URL.createObjectURL(file);
  let sourceURLRevoked = false;
  const revokeSourceURL = () => {
    if (sourceURLRevoked) {
      return;
    }
    sourceURLRevoked = true;
    revokeObjectURL(sourceURL);
  };

  try {
    const image = new Image();
    let cancelled = false;
    let rejectCancellation: ((reason?: unknown) => void) | null = null;
    const cancellation = new Promise<never>((_, reject) => {
      rejectCancellation = reject;
    });
    const cancel = () => {
      if (cancelled) {
        return;
      }
      cancelled = true;
      image.onload = null;
      image.onerror = null;
      revokeSourceURL();
      rejectCancellation?.(new Error(disposedMessage));
    };
    registerCancellation?.(cancel);
    image.decoding = 'async';
    const loaded = new Promise<void>((resolve, reject) => {
      image.onload = () => resolve();
      image.onerror = () => reject(new Error('The image could not be decoded.'));
    });
    image.src = sourceURL;
    await Promise.race([loaded, cancellation]);
    if (typeof image.decode === 'function') {
      await Promise.race([image.decode(), cancellation]);
    }
    revokeSourceURL();

    const dimensions = calculatePreviewDimensions(
      image.naturalWidth || image.width,
      image.naturalHeight || image.height,
      maxSide,
    );
    if (!dimensions) {
      throw new Error('The image has invalid dimensions.');
    }

    const canvas = document.createElement('canvas');
    canvas.width = dimensions.width;
    canvas.height = dimensions.height;
    const context = canvas.getContext('2d');
    if (!context) {
      throw new Error('This browser cannot render a local image preview.');
    }
    context.drawImage(image, 0, 0, dimensions.width, dimensions.height);

    const blob = await Promise.race([
      new Promise<Blob>((resolve, reject) => {
        canvas.toBlob(
          candidate => candidate
            ? resolve(candidate)
            : reject(new Error('The image preview could not be encoded.')),
          'image/webp',
          previewQuality,
        );
      }),
      cancellation,
    ]);

    return { ...dimensions, blob };
  } catch (error) {
    throw new Error(errorMessage(error, 'The image preview could not be prepared.'));
  } finally {
    registerCancellation?.(null);
    revokeSourceURL();
  }
};

export const createLocalImagePreviewGenerator = (
  options: LocalImagePreviewGeneratorOptions = {},
): LocalImagePreviewGenerator => {
  const maxSide = options.maxSide ?? defaultMaxSide;
  let disposed = false;
  let draining = false;
  let worker: Worker | null = null;
  let workerUnavailable = false;
  let nextWorkerJobID = 0;
  let activeWorkerReject: ((reason?: unknown) => void) | null = null;
  let activeWorkerCleanup: (() => void) | null = null;
  let activeFallbackCancel: (() => void) | null = null;

  type QueueItem = {
    file: File;
    resolve: (preview: LocalImagePreview) => void;
    reject: (reason?: unknown) => void;
  };

  const queue: QueueItem[] = [];

  const terminateWorker = () => {
    const currentWorker = worker;
    worker = null;
    if (!currentWorker) {
      return;
    }
    currentWorker.onmessage = null;
    currentWorker.onerror = null;
    currentWorker.terminate();
  };

  const disableWorker = () => {
    workerUnavailable = true;
    terminateWorker();
  };

  const getWorker = () => {
    if (worker || workerUnavailable || typeof Worker === 'undefined') {
      return worker;
    }
    try {
      worker = new Worker(
        new URL('../workers/localImagePreview.worker.ts', import.meta.url),
        { type: 'module' },
      );
      return worker;
    } catch {
      workerUnavailable = true;
      return null;
    }
  };

  const createWorkerPreview = (file: File, currentWorker: Worker) => new Promise<LocalImagePreview>(
    (resolve, reject) => {
      const jobID = ++nextWorkerJobID;
      const cleanup = () => {
        currentWorker.onmessage = null;
        currentWorker.onerror = null;
        if (activeWorkerCleanup === cleanup) {
          activeWorkerCleanup = null;
          activeWorkerReject = null;
        }
      };
      const fail = (error: unknown) => {
        cleanup();
        reject(error);
      };

      activeWorkerCleanup = cleanup;
      activeWorkerReject = reject;
      currentWorker.onmessage = event => {
        const response = event.data as {
          jobID?: number;
          type?: 'success' | 'error';
          blob?: Blob;
          width?: number;
          height?: number;
          message?: string;
        };
        if (response.jobID !== jobID) {
          return;
        }
        cleanup();
        if (
          response.type === 'success'
          && response.blob instanceof Blob
          && typeof response.width === 'number'
          && typeof response.height === 'number'
          && response.width > 0
          && response.height > 0
        ) {
          resolve({
            blob: response.blob,
            width: response.width,
            height: response.height,
          });
          return;
        }
        reject(new Error(response.message || 'The worker could not prepare the image preview.'));
      };
      currentWorker.onerror = event => {
        fail(new Error(event.message || 'The image preview worker failed.'));
      };

      try {
        currentWorker.postMessage({ jobID, file, maxSide });
      } catch (error) {
        fail(error);
      }
    },
  );

  const generateOne = async (file: File): Promise<LocalImagePreview> => {
    if (disposed) {
      throw new Error(disposedMessage);
    }

    const currentWorker = getWorker();
    if (currentWorker) {
      try {
        return await createWorkerPreview(file, currentWorker);
      } catch (error) {
        if (disposed) {
          throw new Error(disposedMessage);
        }
        disableWorker();
        if (error instanceof Error && error.message === disposedMessage) {
          throw error;
        }
      }
    }

    if (disposed) {
      throw new Error(disposedMessage);
    }
    const preview = await createMainThreadPreview(file, maxSide, cancel => {
      activeFallbackCancel = cancel;
    });
    if (disposed) {
      throw new Error(disposedMessage);
    }
    return preview;
  };

  const drain = async () => {
    if (draining) {
      return;
    }
    draining = true;
    try {
      while (queue.length > 0 && !disposed) {
        const item = queue.shift();
        if (!item) {
          continue;
        }
        try {
          item.resolve(await generateOne(item.file));
        } catch (error) {
          item.reject(error);
        }
      }
    } finally {
      draining = false;
    }
  };

  return {
    generate: (file: File) => {
      if (disposed) {
        return Promise.reject(new Error(disposedMessage));
      }
      const promise = new Promise<LocalImagePreview>((resolve, reject) => {
        queue.push({ file, resolve, reject });
      });
      void drain();
      return promise;
    },
    dispose: () => {
      if (disposed) {
        return;
      }
      disposed = true;
      const error = new Error(disposedMessage);
      while (queue.length > 0) {
        queue.shift()?.reject(error);
      }
      const rejectActive = activeWorkerReject;
      activeWorkerCleanup?.();
      rejectActive?.(error);
      activeFallbackCancel?.();
      terminateWorker();
    },
  };
};
