// @vitest-environment jsdom

import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import {
  deletePostPublishOperation,
  getPostPublishOperation,
  replacePostPublishOperation,
  restorePublishOperation,
  serializePublishOperation,
  updatePostPublishOperation,
  type PersistedPostPublishOperation,
} from './postPublishRepository';

type Handler = ((event: Event) => void) | null;

const requestFor = <T,>() => ({
  result: undefined as T | undefined,
  error: null as DOMException | null,
  onsuccess: null as Handler,
  onerror: null as Handler,
});

class TestTransaction {
  oncomplete: Handler = null;
  onerror: Handler = null;
  onabort: Handler = null;
  error: DOMException | null = null;
  private pending = 0;
  private completionQueued = false;

  constructor(private readonly records: Map<number, PersistedPostPublishOperation>) {}

  request<T>(action: () => T) {
    const request = requestFor<T>();
    this.pending += 1;
    setTimeout(() => {
      try {
        request.result = action();
        request.onsuccess?.(new Event('success'));
      } catch (error) {
        request.error = error instanceof DOMException ? error : new DOMException(String(error));
        request.onerror?.(new Event('error'));
        this.error = request.error;
        this.onerror?.(new Event('error'));
      } finally {
        this.pending -= 1;
        this.scheduleCompletion();
      }
    }, 0);
    return request;
  }

  objectStore(_name: string) {
    return new TestObjectStore(this.records, this);
  }

  private scheduleCompletion() {
    if (this.pending !== 0 || this.completionQueued) return;
    this.completionQueued = true;
    setTimeout(() => {
      this.completionQueued = false;
      if (this.pending === 0) this.oncomplete?.(new Event('complete'));
    }, 0);
  }
}

class TestObjectStore {
  constructor(
    private readonly records: Map<number, PersistedPostPublishOperation>,
    private readonly transaction?: TestTransaction,
  ) {}

  createIndex(_name: string, _keyPath: string, _options?: IDBIndexParameters) {}

  get(viewerID: number) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => this.records.get(viewerID));
  }

  put(record: PersistedPostPublishOperation) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => {
      this.records.set(record.publisherUserID, record);
      return record.publisherUserID;
    });
  }

  delete(viewerID: number) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => {
      this.records.delete(viewerID);
      return undefined;
    });
  }
}

class TestDatabase {
  readonly records = new Map<number, PersistedPostPublishOperation>();
  readonly stores = new Set<string>();
  readonly objectStoreNames = { contains: (name: string) => this.stores.has(name) };
  onversionchange: (() => void) | null = null;

  constructor(readonly name: string) {}

  createObjectStore(name: string, _options: IDBObjectStoreParameters) {
    this.stores.add(name);
    return new TestObjectStore(this.records);
  }

  transaction(_name: string, _mode: IDBTransactionMode) {
    return new TestTransaction(this.records);
  }

  close() {}
}

class TestIndexedDBFactory {
  database: TestDatabase | null = null;

  open(name: string, _version?: number) {
    const request = Object.assign(requestFor<TestDatabase>(), {
      transaction: undefined as IDBTransaction | undefined,
      onupgradeneeded: null as Handler,
      onblocked: null as Handler,
    });
    setTimeout(() => {
      const isNew = this.database === null;
      this.database ||= new TestDatabase(name);
      request.result = this.database;
      if (isNew) request.onupgradeneeded?.(new Event('upgradeneeded'));
      request.onsuccess?.(new Event('success'));
    }, 0);
    return request;
  }
}

const makeOperation = (id: string, viewerID: number, name = 'photo.webp') => {
  const file = new File(['image-binary'], name, {
    type: 'image/webp',
    lastModified: 1_700_000_000_000,
  });
  return {
    id,
    publisherUserID: viewerID,
    sourceDraftID: 'saved-draft',
    content: 'Exact text',
    media: [
      { draftMediaID: 'media-a', file, uploadedURL: '/media/a' },
      { draftMediaID: 'media-b', file: new File(['second'], 'second.png', { type: 'image/png' }), uploadedURL: '' },
    ],
    phase: 'publishing' as const,
    failureKind: null,
    error: '',
    startedAt: 123,
    post: null,
  };
};

const readBlobText = (blob: Blob) => new Promise<string>((resolve, reject) => {
  const reader = new FileReader();
  reader.onload = () => resolve(String(reader.result));
  reader.onerror = () => reject(reader.error || new Error('Could not read test Blob.'));
  reader.readAsText(blob);
});

describe('postPublishRepository', () => {
  let factory: TestIndexedDBFactory;

  beforeAll(() => {
    factory = new TestIndexedDBFactory();
    vi.stubGlobal('indexedDB', factory as unknown as IDBFactory);
  });

  afterAll(() => {
    vi.unstubAllGlobals();
  });

  it('round-trips media and keeps replace, update, and delete scoped to the current operation', async () => {
    const original = makeOperation('operation-a', 7);
    const persisted = serializePublishOperation(original, 456);
    await replacePostPublishOperation(persisted);
    await replacePostPublishOperation(serializePublishOperation(makeOperation('operation-b', 8), 457));

    const restoredRecord = await getPostPublishOperation(7);
    expect(restoredRecord).toMatchObject({
      id: 'operation-a',
      publisherUserID: 7,
      sourceDraftID: 'saved-draft',
      content: 'Exact text',
      phase: 'publishing',
      updatedAt: 456,
      media: [
        { draftMediaID: 'media-a', name: 'photo.webp', type: 'image/webp', uploadedURL: '/media/a' },
        { draftMediaID: 'media-b', name: 'second.png', type: 'image/png', uploadedURL: '' },
      ],
    });
    const restored = restorePublishOperation(restoredRecord!);
    expect(restored.media.map(item => [item.draftMediaID, item.file.name, item.uploadedURL]))
      .toEqual([
        ['media-a', 'photo.webp', '/media/a'],
        ['media-b', 'second.png', ''],
      ]);
    expect(restored.media[0]?.file.lastModified).toBe(1_700_000_000_000);
    await expect(readBlobText(restored.media[0]!.file)).resolves.toBe('image-binary');
    expect(await getPostPublishOperation(8)).toMatchObject({ id: 'operation-b' });

    expect(await updatePostPublishOperation({ ...persisted, id: 'stale-operation' })).toBe(false);
    expect(await getPostPublishOperation(7)).toMatchObject({ id: 'operation-a' });
    expect(await updatePostPublishOperation({ ...persisted, error: 'checkpoint' })).toBe(true);
    expect(await getPostPublishOperation(7)).toMatchObject({ error: 'checkpoint' });
    expect(await deletePostPublishOperation(7, 'stale-operation')).toBe(false);
    expect(await getPostPublishOperation(7)).not.toBeNull();
    expect(await deletePostPublishOperation(7, 'operation-a')).toBe(true);
    expect(await getPostPublishOperation(7)).toBeNull();
    expect(await getPostPublishOperation(8)).toMatchObject({ id: 'operation-b' });
  });
});
