// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  deletePostDraft,
  deletePostDraftIfUnchanged,
  getPostDraft,
  listPostDrafts,
  savePostDraft,
  type PersistedPostDraft,
} from './postDraftRepository';
import type { DraftSnapshot } from '../utils/postDraftSnapshot';

type RequestHandler = ((event: Event) => void) | null;
type TestRequest<T> = {
  result?: T;
  error?: DOMException | null;
  onsuccess: RequestHandler;
  onerror: RequestHandler;
};

const createRequest = <T>(): TestRequest<T> => ({
  result: undefined,
  error: null,
  onsuccess: null,
  onerror: null,
});

class TestTransaction {
  oncomplete: RequestHandler = null;
  onerror: RequestHandler = null;
  onabort: RequestHandler = null;
  error: DOMException | null = null;
  private pending = 0;
  private completionScheduled = false;

  constructor(
    private readonly records: Map<string, PersistedPostDraft>,
    private readonly completionDelayMs = 0,
  ) {}

  request<T>(action: () => T): TestRequest<T> {
    const request = createRequest<T>();
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
    if (this.pending !== 0 || this.completionScheduled) return;
    this.completionScheduled = true;
    setTimeout(() => {
      this.completionScheduled = false;
      if (this.pending === 0) this.oncomplete?.(new Event('complete'));
    }, this.completionDelayMs);
  }
}

class TestObjectStore {
  readonly indexNames = {
    contains: (_name: string) => true,
  };

  constructor(
    private readonly records: Map<string, PersistedPostDraft>,
    private readonly transaction?: TestTransaction,
  ) {}

  createIndex(_name: string, _keyPath: string, _options?: IDBIndexParameters) {}

  index(name: string) {
    if (!this.transaction) throw new Error(`Index ${name} has no transaction.`);
    return new TestIndex(this.records, this.transaction, name);
  }

  get(id: string) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => this.records.get(id));
  }

  put(record: PersistedPostDraft) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => {
      this.records.set(record.id, record);
      return record.id;
    });
  }

  delete(id: string) {
    if (!this.transaction) throw new Error('Object store has no transaction.');
    return this.transaction.request(() => {
      this.records.delete(id);
      return undefined;
    });
  }
}

class TestIndex {
  constructor(
    private readonly records: Map<string, PersistedPostDraft>,
    private readonly transaction: TestTransaction,
    private readonly keyPath: string,
  ) {}

  getAll(range: { value: IDBValidKey }) {
    return this.transaction.request(() => Array.from(this.records.values())
      .filter(record => record[this.keyPath as keyof PersistedPostDraft] === range.value));
  }
}

class TestDatabase {
  readonly stores = new Map<string, Map<string, PersistedPostDraft>>();
  readonly transactionModes: IDBTransactionMode[] = [];
  transactionCompletionDelayMs = 0;
  readonly objectStoreNames = {
    contains: (name: string) => this.stores.has(name),
  };

  constructor(readonly name: string) {}

  createObjectStore(name: string, _options: IDBObjectStoreParameters) {
    const records = new Map<string, PersistedPostDraft>();
    this.stores.set(name, records);
    return new TestObjectStore(records);
  }

  transaction(name: string, mode: IDBTransactionMode) {
    const records = this.stores.get(name);
    if (!records) throw new Error(`Unknown object store ${name}.`);
    this.transactionModes.push(mode);
    return new TestTransaction(records, this.transactionCompletionDelayMs);
  }

  close() {}
}

class TestIndexedDBFactory {
  database: TestDatabase | null = null;

  open(name: string, _version?: number) {
    const request = Object.assign(createRequest<TestDatabase>(), {
      result: undefined as TestDatabase | undefined,
      transaction: undefined as TestTransaction | undefined,
      onupgradeneeded: null as RequestHandler,
      onblocked: null as RequestHandler,
    });
    setTimeout(() => {
      const newDatabase = this.database === null;
      this.database ||= new TestDatabase(name);
      request.result = this.database;
      if (newDatabase) {
        request.transaction = new TestTransaction(new Map());
        request.onupgradeneeded?.(new Event('upgradeneeded'));
      }
      request.onsuccess?.(new Event('success'));
    }, 0);
    return request;
  }
}

class TestKeyRange {
  static only(value: IDBValidKey) {
    return { value };
  }
}

const draft = (
  id: string,
  viewerID: number,
  updatedAt: number,
  content = id,
): PersistedPostDraft => ({
  id,
  viewerID,
  content,
  media: [],
  createdAt: updatedAt - 100,
  updatedAt,
});

const snapshotOf = (record: PersistedPostDraft): DraftSnapshot => ({
  content: record.content,
  media: record.media.map(item => ({
    id: item.id,
    name: item.name,
    type: item.type,
    size: item.size,
    lastModified: item.lastModified,
  })),
});

const readBlobText = (blob: Blob) => new Promise<string>((resolve, reject) => {
  const reader = new FileReader();
  reader.onload = () => resolve(String(reader.result));
  reader.onerror = () => reject(reader.error || new Error('Could not read test Blob.'));
  reader.readAsText(blob);
});

describe('postDraftRepository', () => {
  beforeEach(() => {
    vi.stubGlobal('indexedDB', new TestIndexedDBFactory() as unknown as IDBFactory);
    vi.stubGlobal('IDBKeyRange', TestKeyRange as unknown as typeof IDBKeyRange);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('saves and updates drafts, lists by viewer and updated time, and isolates get/delete', async () => {
    const older = draft('draft-old', 7, 100, 'Older text');
    const newer = draft('draft-new', 7, 300, 'Newer text');
    await savePostDraft(older);
    await savePostDraft(newer);
    await savePostDraft({ ...older, content: 'Updated text', updatedAt: 400 });
    await savePostDraft(draft('other-viewer', 8, 500));

    expect((await listPostDrafts(7)).map(item => item.id)).toEqual(['draft-old', 'draft-new']);
    expect((await listPostDrafts(7))[0]?.content).toBe('Updated text');
    expect(await getPostDraft(8, 'draft-old')).toBeNull();
    expect(await deletePostDraft(8, 'draft-old')).toBe(false);
    expect(await getPostDraft(7, 'draft-old')).not.toBeNull();
    expect(await deletePostDraft(7, 'draft-old')).toBe(true);
    expect(await getPostDraft(7, 'draft-old')).toBeNull();
  });

  it('round-trips image binary and keeps the file metadata fields', async () => {
    const blob = new Blob(['pixels\u0000binary'], { type: 'image/webp' });
    const record = {
      ...draft('media-draft', 7, 700, 'Image post'),
      media: [{
        id: 'media-id',
        blob,
        name: 'photo.webp',
        type: 'image/webp',
        size: blob.size,
        lastModified: 1_700_000_000_000,
        uploadedURL: '/temporary/photo.webp',
      }],
    };
    await savePostDraft(record);

    const restored = await getPostDraft(7, 'media-draft');
    expect(restored?.media[0]).toMatchObject({
      id: 'media-id',
      name: 'photo.webp',
      type: 'image/webp',
      size: blob.size,
      lastModified: 1_700_000_000_000,
      uploadedURL: '/temporary/photo.webp',
    });
    expect(await readBlobText(restored!.media[0]!.blob)).toBe('pixels\u0000binary');
  });

  it('does not let another viewer overwrite an existing draft ID', async () => {
    const original = draft('known-id', 7, 100, 'Owner content');
    await savePostDraft(original);
    await expect(savePostDraft({ ...original, viewerID: 8, content: 'Unauthorized edit' }))
      .rejects.toThrow('belongs to another viewer');
    expect((await getPostDraft(7, 'known-id'))?.content).toBe('Owner content');
    expect(await getPostDraft(8, 'known-id')).toBeNull();
  });

  it('atomically deletes an unchanged saved draft after the readwrite transaction completes', async () => {
    const blob = new Blob(['image'], { type: 'image/png' });
    const record: PersistedPostDraft = {
      ...draft('same-version', 7, 100, 'Saved text'),
      media: [{
        id: 'media-1',
        blob,
        name: 'photo.png',
        type: 'image/png',
        size: blob.size,
        lastModified: 123,
        uploadedURL: '/old-upload-url',
      }],
    };
    await savePostDraft(record);
    const database = (globalThis.indexedDB as unknown as TestIndexedDBFactory).database!;
    database.transactionCompletionDelayMs = 40;

    let settled = false;
    const result = deletePostDraftIfUnchanged(7, record.id, snapshotOf(record)).then(value => {
      settled = true;
      return value;
    });
    await new Promise(resolve => setTimeout(resolve, 5));

    expect(settled).toBe(false);
    await expect(result).resolves.toBe('deleted');
    expect(database.transactionModes.at(-1)).toBe('readwrite');
    expect(await getPostDraft(7, record.id)).toBeNull();
  });

  it('preserves a changed draft version with the same ID', async () => {
    const original = draft('newer-version', 7, 100, 'Original');
    const newerBlob = new Blob(['new image'], { type: 'image/webp' });
    const newer: PersistedPostDraft = {
      ...original,
      content: 'Edited and saved',
      media: [{
        id: 'new-media-id',
        blob: newerBlob,
        name: 'new-photo.webp',
        type: 'image/webp',
        size: newerBlob.size,
        lastModified: 456,
        uploadedURL: '',
      }],
      updatedAt: 200,
    };
    await savePostDraft(newer);

    await expect(deletePostDraftIfUnchanged(7, original.id, snapshotOf(original)))
      .resolves.toBe('changed');
    expect(await getPostDraft(7, original.id)).toMatchObject({
      content: 'Edited and saved',
      media: [{ id: 'new-media-id', name: 'new-photo.webp' }],
      updatedAt: 200,
    });
  });

  it('treats missing and wrong-viewer drafts as missing without deleting another viewer data', async () => {
    const owned = draft('private-draft', 7, 100, 'Private');
    await savePostDraft(owned);

    await expect(deletePostDraftIfUnchanged(7, 'not-there', snapshotOf(owned)))
      .resolves.toBe('missing');
    await expect(deletePostDraftIfUnchanged(8, owned.id, snapshotOf(owned)))
      .resolves.toBe('missing');
    expect((await getPostDraft(7, owned.id))?.content).toBe('Private');
  });

  it('does not treat an uploadedURL-only change as a new source draft version', async () => {
    const blob = new Blob(['image'], { type: 'image/png' });
    const original: PersistedPostDraft = {
      ...draft('uploaded-url-change', 7, 100, 'Same content'),
      media: [{
        id: 'media-1',
        blob,
        name: 'photo.png',
        type: 'image/png',
        size: blob.size,
        lastModified: 123,
        uploadedURL: '/first-url',
      }],
    };
    await savePostDraft(original);
    await savePostDraft({
      ...original,
      media: [{ ...original.media[0]!, uploadedURL: '/refreshed-url' }],
      updatedAt: 200,
    });

    await expect(deletePostDraftIfUnchanged(7, original.id, snapshotOf(original)))
      .resolves.toBe('deleted');
    expect(await getPostDraft(7, original.id)).toBeNull();
  });

  it('rejects invalid viewer IDs and blank record IDs', async () => {
    expect(() => listPostDrafts(0)).toThrow('valid viewer ID');
    expect(() => getPostDraft(Number.NaN, 'draft')).toThrow('valid viewer ID');
    expect(() => deletePostDraft(-1, 'draft')).toThrow('valid viewer ID');
    expect(() => savePostDraft({ ...draft(' ', 7, 1) })).toThrow('draft ID');
  });
});
