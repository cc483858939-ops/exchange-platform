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

  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: ErrorEvent) => void) | null = null;
  posted: PostedJob[] = [];
  terminated = false;

  constructor() {
    FakeWorker.instances.push(this);
  }

  postMessage(message: PostedJob) {
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

const imageFile = (name: string) => new File(['image'], name, { type: 'image/jpeg' });

describe('createLocalImagePreviewGenerator worker recovery', () => {
  beforeEach(() => {
    FakeWorker.instances = [];
    vi.stubGlobal('Worker', FakeWorker);
    vi.stubGlobal('Image', undefined);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the worker available after a per-image worker error', async () => {
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
    await expect(first).rejects.toThrow('This browser cannot prepare a local image preview.');

    const second = generator.generate(imageFile('valid.jpg'));
    await flushPromises();
    expect(FakeWorker.instances).toHaveLength(1);
    expect(worker.posted).toHaveLength(2);

    const previewBlob = new Blob(['preview'], { type: 'image/webp' });
    worker.respond({
      jobID: 2,
      type: 'success',
      blob: previewBlob,
      width: 800,
      height: 600,
    });
    await expect(second).resolves.toEqual({
      blob: previewBlob,
      width: 800,
      height: 600,
    });

    generator.dispose();
    expect(worker.terminated).toBe(true);
  });

  it('disables the worker after infrastructure failure and falls back later', async () => {
    const generator = createLocalImagePreviewGenerator();
    const first = generator.generate(imageFile('first.jpg'));
    const worker = FakeWorker.instances[0];
    expect(worker).toBeDefined();

    worker.fail('worker script failed to load');
    await expect(first).rejects.toThrow('This browser cannot prepare a local image preview.');
    expect(worker.terminated).toBe(true);

    const second = generator.generate(imageFile('second.jpg'));
    await expect(second).rejects.toThrow('This browser cannot prepare a local image preview.');
    expect(FakeWorker.instances).toHaveLength(1);
    expect(worker.posted).toHaveLength(1);

    generator.dispose();
  });
});
