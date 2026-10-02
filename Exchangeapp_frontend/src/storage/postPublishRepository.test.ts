// @vitest-environment jsdom

import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import {
  deletePostPublishOperation,
  getPostPublishOperation,
  replacePostPublishOperation,
  restorePublishOperation,
  serializePublishOperation,
  updatePostPublishOperation,
  type PersistedPublishFailureKind,
  type PersistedPublishPhase,
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
    publisherSessionID: `session-${viewerID}`,
    sourceDraftID: 'saved-draft',
    sourceDraftSnapshot: {
      content: 'Exact text',
      quotePostID: null,
      media: [
        {
          id: 'media-a',
          name: file.name,
          type: file.type,
          size: file.size,
          lastModified: file.lastModified,
        },
        {
          id: 'media-b',
          name: 'second.png',
          type: 'image/png',
          size: 6,
          lastModified: 0,
        },
      ],
    },
    content: 'Exact text',
    quotePostID: null,
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
    const persisted = serializePublishOperation({
      ...original,
      sessionVersion: 55,
      authBinding: { userID: 7, sessionID: 'runtime-only', sessionVersion: 55 },
      accessToken: 'never-persist-this',
    } as any, 456);
    expect(persisted.schemaVersion).toBe(4);
    expect(persisted).not.toHaveProperty('sessionVersion');
    expect(persisted).not.toHaveProperty('authBinding');
    expect(persisted).not.toHaveProperty('accessToken');
    await replacePostPublishOperation(persisted);
    await replacePostPublishOperation(serializePublishOperation(makeOperation('operation-b', 8), 457));

    const restoredRecord = await getPostPublishOperation(7);
    expect(restoredRecord).toMatchObject({
      schemaVersion: 4,
      id: 'operation-a',
      publisherUserID: 7,
      publisherSessionID: 'session-7',
      sourceDraftID: 'saved-draft',
      sourceDraftSnapshot: {
        content: 'Exact text',
        quotePostID: null,
        media: [
          { id: 'media-a', name: 'photo.webp', type: 'image/webp' },
          { id: 'media-b', name: 'second.png', type: 'image/png' },
        ],
      },
      content: 'Exact text',
      quotePostID: null,
      phase: 'publishing',
      updatedAt: 456,
      media: [
        { draftMediaID: 'media-a', name: 'photo.webp', type: 'image/webp', uploadedURL: '/media/a' },
        { draftMediaID: 'media-b', name: 'second.png', type: 'image/png', uploadedURL: '' },
      ],
    });
    const restored = restorePublishOperation(restoredRecord!);
    expect(restored.publisherSessionID).toBe('session-7');
    expect(restored.sourceDraftSnapshot).toEqual(restoredRecord?.sourceDraftSnapshot);
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

  it('normalizes a legacy operation without a source snapshot to preservation-only state', async () => {
    const original = serializePublishOperation(makeOperation('legacy-operation', 7), 456);
    await replacePostPublishOperation(original);
    const legacyRecord = { ...original } as unknown as Record<string, unknown>;
    delete legacyRecord.schemaVersion;
    delete legacyRecord.sourceDraftSnapshot;
    delete legacyRecord.publisherSessionID;
    delete legacyRecord.quotePostID;
    factory.database!.records.set(7, legacyRecord as unknown as PersistedPostPublishOperation);

    const restoredRecord = await getPostPublishOperation(7);
    expect(restoredRecord).toMatchObject({
      schemaVersion: 1,
      sourceDraftID: 'saved-draft',
      sourceDraftSnapshot: null,
    });
    expect(restorePublishOperation(restoredRecord!).sourceDraftSnapshot).toBeNull();
    expect(restorePublishOperation(restoredRecord!).publisherSessionID).toBeNull();
  });

  it('restores schema 2 without a durable session owner as untrusted legacy state', async () => {
    const original = serializePublishOperation(makeOperation('legacy-v2', 7), 789);
    const { publisherSessionID: _sessionID, quotePostID: _quote, sourceDraftSnapshot, ...legacyV2 } = original;
    const legacySnapshot = { ...sourceDraftSnapshot! } as Record<string, unknown>;
    delete legacySnapshot.quotePostID;
    factory.database!.records.set(7, {
      ...legacyV2,
      schemaVersion: 2,
      sourceDraftSnapshot: legacySnapshot,
    } as unknown as PersistedPostPublishOperation);

    const restored = await getPostPublishOperation(7);

    expect(restored).toMatchObject({ schemaVersion: 2, publisherSessionID: null });
    expect(restorePublishOperation(restored!).publisherSessionID).toBeNull();
    expect(restorePublishOperation(restored!).quotePostID).toBeNull();
  });

  it('restores the earlier session-bound schema 3 as a non-quote operation', () => {
    const original = serializePublishOperation(makeOperation('legacy-v3', 7), 900);
    const { quotePostID: _quote, sourceDraftSnapshot, ...withoutQuote } = original;
    const legacySnapshot = { ...sourceDraftSnapshot! } as Record<string, unknown>;
    delete legacySnapshot.quotePostID;
    const restored = restorePublishOperation({
      ...withoutQuote,
      schemaVersion: 3,
      sourceDraftSnapshot: legacySnapshot as unknown as PersistedPostPublishOperation['sourceDraftSnapshot'],
    } as PersistedPostPublishOperation);

    expect(restored.publisherSessionID).toBe('session-7');
    expect(restored.quotePostID).toBeNull();
    expect(restored.sourceDraftSnapshot?.quotePostID).toBeNull();
  });

  it('round-trips a quote ID and rejects malformed schema 4 quote identity', () => {
    const quoted = serializePublishOperation({
      ...makeOperation('quoted-operation', 7),
      quotePostID: 42,
      sourceDraftSnapshot: {
        ...makeOperation('quoted-source', 7).sourceDraftSnapshot!,
        quotePostID: 42,
      },
    });
    expect(quoted.schemaVersion).toBe(4);
    expect(restorePublishOperation(quoted)).toMatchObject({
      quotePostID: 42,
      sourceDraftSnapshot: { quotePostID: 42 },
    });

    const { quotePostID: _quote, ...missingQuote } = quoted;
    expect(() => restorePublishOperation(missingQuote as PersistedPostPublishOperation))
      .toThrow('saved publish operation is invalid');
    expect(() => restorePublishOperation({ ...quoted, quotePostID: 0 }))
      .toThrow('saved publish operation is invalid');
    expect(() => restorePublishOperation({
      ...quoted,
      sourceDraftSnapshot: { ...quoted.sourceDraftSnapshot!, quotePostID: undefined } as any,
    })).toThrow('saved publish source snapshot is invalid');
  });

  it('requires a non-empty session owner for new schema 4 records', () => {
    expect(() => serializePublishOperation({
      ...makeOperation('missing-session', 7),
      publisherSessionID: null,
    })).toThrow('must be bound to an authentication session');
  });

  it('rejects invalid phase/failure combinations and round-trips valid durable shapes', async () => {
    const invalidShapes: Array<{
      phase: PersistedPublishPhase;
      failureKind: PersistedPublishFailureKind;
    }> = [
      { phase: 'failed', failureKind: null },
      { phase: 'succeeded', failureKind: 'retryable' },
      { phase: 'publishing', failureKind: 'auth_context_changed' },
      { phase: 'uploading', failureKind: 'idempotency_conflict' },
    ];

    for (const [index, shape] of invalidShapes.entries()) {
      const persisted = serializePublishOperation(makeOperation(`invalid-shape-${index}`, 7));
      await expect(replacePostPublishOperation({ ...persisted, ...shape }))
        .rejects.toThrow('saved publish operation is invalid');
    }

    const validShapes: Array<{
      phase: PersistedPublishPhase;
      failureKind: PersistedPublishFailureKind;
    }> = [
      { phase: 'failed', failureKind: 'retryable' },
      { phase: 'failed', failureKind: 'idempotency_conflict' },
      { phase: 'failed', failureKind: 'auth_context_changed' },
      { phase: 'publishing', failureKind: null },
      { phase: 'uploading', failureKind: null },
      { phase: 'succeeded', failureKind: null },
    ];

    for (const [index, shape] of validShapes.entries()) {
      const persisted = serializePublishOperation(makeOperation(`valid-shape-${index}`, 7));
      const record = { ...persisted, ...shape };
      await replacePostPublishOperation(record);
      await expect(getPostPublishOperation(7)).resolves.toMatchObject(shape);
    }
  });
});
