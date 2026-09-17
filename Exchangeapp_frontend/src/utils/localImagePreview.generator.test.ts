// @vitest-environment jsdom

import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createLocalImagePreviewGenerator } from './localImagePreview';

type PostedJob = {
  jobID: number;
  file: File;
  maxSide: number;
};

class FakeWorker {
  static instances: FakeWorker[] = [];
  static throwOnConstruct = false;
  static throwOnPostMessage = false;

  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: ErrorEvent) => void) | null = null;
  posted: PostedJob[] = [];
  terminated = false;

  constructor() {
    if (FakeWorker.throwOnConstruct) {
      throw new Error('worker construction failed');
    }
    FakeWorker.instances.push(this);
  }

  postMessage(message: PostedJob) {
    if (FakeWorker.throwOnPostMessage) {
      throw new Error('worker postMessage failed');
    }
    this.posted.push(message);
  }

  terminate() {
    this.terminated = true;
  }

  respond(data: unknown) {
    this.onmessage?.({ data } as MessageEvent);
  }

  fail(message: string) {
    this.onerror?.({ message } as ErrorEvent);
  }
}

class FakeImage {
  static instances: FakeImage[] = [];

  naturalWidth = 640;
  naturalHeight = 480;
  width = 640;
  height = 480;
  decoding = '';
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private currentSource = '';

  constructor() {
    FakeImage.instances.push(this);
  }

  set src(value: string) {
    this.currentSource = value;
    queueMicrotask(() => this.onload?.());
  }

  get src() {
    return this.currentSource;
  }

  decode() {
    return Promise.resolve();
  }
}

const imageFile = (name: string) => new File(['image'], name, { type: 'image/jpeg' });
const disposedMessage = 'Image preview preparation was cancelled.';

let fallbackOutcomes: Array<Blob | null> = [];
let restoreURLMethods = () => {};

const installMainThreadFallback = (outcomes: Array<Blob | null>) => {
  const urlConstructor = globalThis.URL as typeof URL & {
    createObjectURL?: (object: Blob | MediaSource) => string;
    revokeObjectURL?: (url: string) => void;
  };
  const createObjectURLDescriptor = Object.getOwnPropertyDescriptor(urlConstructor, 'createObjectURL');
  const revokeObjectURLDescriptor = Object.getOwnPropertyDescriptor(urlConstructor, 'revokeObjectURL');
  const createObjectURL = vi.fn(() => 'blob:local-image-preview-test');
  const revokeObjectURL = vi.fn();
  Object.defineProperty(urlConstructor, 'createObjectURL', {
    configurable: true,
    writable: true,
    value: createObjectURL,
  });
  Object.defineProperty(urlConstructor, 'revokeObjectURL', {
    configurable: true,
    writable: true,
    value: revokeObjectURL,
  });
  restoreURLMethods = () => {
    if (createObjectURLDescriptor) {
      Object.defineProperty(urlConstructor, 'createObjectURL', createObjectURLDescriptor);
    } else {
      Reflect.deleteProperty(urlConstructor, 'createObjectURL');
    }
    if (revokeObjectURLDescriptor) {
      Object.defineProperty(urlConstructor, 'revokeObjectURL', revokeObjectURLDescriptor);
    } else {
      Reflect.deleteProperty(urlConstructor, 'revokeObjectURL');
    }
  };

  fallbackOutcomes = [...outcomes];
  FakeImage.instances = [];
  vi.stubGlobal('Image', FakeImage);

  const context = {
    drawImage: vi.fn(),
  };
  const canvas = {
    width: 0,
    height: 0,
    getContext: vi.fn(() => context),
    toBlob: vi.fn((callback: (blob: Blob | null) => void) => {
      const outcome = fallbackOutcomes.length > 0
        ? fallbackOutcomes.shift() ?? null
        : new Blob(['fallback'], { type: 'image/webp' });
      queueMicrotask(() => callback(outcome));
    }),
  };
  vi.stubGlobal('document', {
    createElement: vi.fn(() => canvas),
  });

  return { canvas, createObjectURL, revokeObjectURL, images: FakeImage.instances };
};

describe('createLocalImagePreviewGenerator worker recovery', () => {
  beforeEach(() => {
    FakeWorker.instances = [];
    FakeWorker.throwOnConstruct = false;
    FakeWorker.throwOnPostMessage = false;
    vi.stubGlobal('Worker', FakeWorker);
    vi.stubGlobal('Image', undefined);
  });

  afterEach(() => {
    restoreURLMethods();
    restoreURLMethods = () => {};
    fallbackOutcomes = [];
    vi.unstubAllGlobals();
  });

  it('keeps the worker available after a per-image error and uses it for the next image', async () => {
    const fallbackBlob = new Blob(['fallback-a'], { type: 'image/webp' });
    const harness = installMainThreadFallback([fallbackBlob]);
    const generator = createLocalImagePreviewGenerator();
    const first = generator.generate(imageFile('broken.jpg'));
    const worker = FakeWorker.instances[0];
    expect(worker).toBeDefined();
    expect(worker.posted).toHaveLength(1);

    worker.respond({
      jobID: 1,
      type: 'error',
      message: 'The image could not be decoded.',
    });
    await expect(first).resolves.toEqual({
      blob: fallbackBlob,
      width: 640,
      height: 480,
    });
    expect(worker.terminated).toBe(false);
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(1);

    const second = generator.generate(imageFile('valid.jpg'));
    await flushPromises();
    expect(FakeWorker.instances).toHaveLength(1);
    expect(worker.posted).toHaveLength(2);

    const workerBlob = new Blob(['worker-preview'], { type: 'image/webp' });
    worker.respond({
      jobID: 2,
      type: 'success',
      blob: workerBlob,
      width: 800,
      height: 600,
    });
    await expect(second).resolves.toEqual({
      blob: workerBlob,
      width: 800,
      height: 600,
    });

    generator.dispose();
    expect(worker.terminated).toBe(true);
  });

  it('keeps the worker available when the per-image fallback also fails', async () => {
    const generator = createLocalImagePreviewGenerator();
    const harness = installMainThreadFallback([null]);
    const first = generator.generate(imageFile('broken.jpg'));
    const worker = FakeWorker.instances[0];

    worker.respond({
      jobID: 1,
      type: 'error',
      message: 'The image could not be decoded.',
    });
    await expect(first).rejects.toThrow('The image preview could not be encoded.');
    expect(worker.terminated).toBe(false);

    const second = generator.generate(imageFile('valid.jpg'));
    expect(worker.posted).toHaveLength(2);
    const workerBlob = new Blob(['worker-preview'], { type: 'image/webp' });
    worker.respond({
      jobID: 2,
      type: 'success',
      blob: workerBlob,
      width: 800,
      height: 600,
    });
    await expect(second).resolves.toEqual({
      blob: workerBlob,
      width: 800,
      height: 600,
    });
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(1);

    generator.dispose();
  });

  it('disables the worker after worker.onerror and uses fallback directly afterward', async () => {
    const firstBlob = new Blob(['fallback-a'], { type: 'image/webp' });
    const secondBlob = new Blob(['fallback-b'], { type: 'image/webp' });
    const harness = installMainThreadFallback([firstBlob, secondBlob]);
    const generator = createLocalImagePreviewGenerator();
    const first = generator.generate(imageFile('first.jpg'));
    const worker = FakeWorker.instances[0];

    worker.fail('worker script failed to load');
    await expect(first).resolves.toEqual({ blob: firstBlob, width: 640, height: 480 });
    expect(worker.terminated).toBe(true);

    const second = generator.generate(imageFile('second.jpg'));
    await expect(second).resolves.toEqual({ blob: secondBlob, width: 640, height: 480 });
    expect(FakeWorker.instances).toHaveLength(1);
    expect(worker.posted).toHaveLength(1);
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(2);

    generator.dispose();
  });

  it('disables the worker after postMessage throws and does not retry it', async () => {
    const firstBlob = new Blob(['fallback-a'], { type: 'image/webp' });
    const secondBlob = new Blob(['fallback-b'], { type: 'image/webp' });
    const harness = installMainThreadFallback([firstBlob, secondBlob]);
    FakeWorker.throwOnPostMessage = true;
    const generator = createLocalImagePreviewGenerator();
    const first = generator.generate(imageFile('first.jpg'));
    const worker = FakeWorker.instances[0];

    await expect(first).resolves.toEqual({ blob: firstBlob, width: 640, height: 480 });
    expect(worker.terminated).toBe(true);
    expect(worker.posted).toHaveLength(0);

    const second = generator.generate(imageFile('second.jpg'));
    await expect(second).resolves.toEqual({ blob: secondBlob, width: 640, height: 480 });
    expect(FakeWorker.instances).toHaveLength(1);
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(2);

    generator.dispose();
  });

  const invalidResponses: Array<[string, unknown]> = [
    ['an empty response', {}],
    ['a success response without a blob', {
      jobID: 1,
      type: 'success',
      blob: null,
      width: 640,
      height: 480,
    }],
    ['an unrecognized response type', { jobID: 1, type: 'unknown' }],
  ];

  it.each(invalidResponses)('disables the worker after %s', async (_description, response) => {
    const firstBlob = new Blob(['fallback-a'], { type: 'image/webp' });
    const secondBlob = new Blob(['fallback-b'], { type: 'image/webp' });
    const harness = installMainThreadFallback([firstBlob, secondBlob]);
    const generator = createLocalImagePreviewGenerator();
    const first = generator.generate(imageFile('invalid-response.jpg'));
    const worker = FakeWorker.instances[0];

    worker.respond(response);
    await expect(first).resolves.toEqual({ blob: firstBlob, width: 640, height: 480 });
    expect(worker.terminated).toBe(true);

    const second = generator.generate(imageFile('later.jpg'));
    await expect(second).resolves.toEqual({ blob: secondBlob, width: 640, height: 480 });
    expect(FakeWorker.instances).toHaveLength(1);
    expect(worker.posted).toHaveLength(1);
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(2);

    generator.dispose();
  });

  it('does not recreate a worker after constructor failure', async () => {
    const firstBlob = new Blob(['fallback-a'], { type: 'image/webp' });
    const secondBlob = new Blob(['fallback-b'], { type: 'image/webp' });
    const harness = installMainThreadFallback([firstBlob, secondBlob]);
    FakeWorker.throwOnConstruct = true;
    const generator = createLocalImagePreviewGenerator();

    await expect(generator.generate(imageFile('first.jpg'))).resolves.toEqual({
      blob: firstBlob,
      width: 640,
      height: 480,
    });
    FakeWorker.throwOnConstruct = false;
    await expect(generator.generate(imageFile('second.jpg'))).resolves.toEqual({
      blob: secondBlob,
      width: 640,
      height: 480,
    });
    expect(FakeWorker.instances).toHaveLength(0);
    expect(harness.canvas.toBlob).toHaveBeenCalledTimes(2);

    generator.dispose();
  });

  it('rejects active and queued jobs on dispose without starting fallback or a new worker', async () => {
    const harness = installMainThreadFallback([new Blob(['fallback'], { type: 'image/webp' })]);
    const generator = createLocalImagePreviewGenerator();
    const active = generator.generate(imageFile('active.jpg'));
    const queued = generator.generate(imageFile('queued.jpg'));
    const worker = FakeWorker.instances[0];

    generator.dispose();

    await expect(active).rejects.toThrow(disposedMessage);
    await expect(queued).rejects.toThrow(disposedMessage);
    expect(worker.terminated).toBe(true);
    expect(harness.images).toHaveLength(0);
    expect(FakeWorker.instances).toHaveLength(1);
    await expect(generator.generate(imageFile('later.jpg'))).rejects.toThrow(disposedMessage);
    expect(FakeWorker.instances).toHaveLength(1);
  });
});
