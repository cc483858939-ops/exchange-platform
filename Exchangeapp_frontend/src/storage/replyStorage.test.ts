// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type Handler = ((event: Event) => void) | null;
type Request<T> = { result?: T; error?: DOMException | null; onsuccess: Handler; onerror: Handler };

const request = <T>(): Request<T> => ({ result: undefined, error: null, onsuccess: null, onerror: null });

class FakeTransaction {
  oncomplete: Handler = null;
  onerror: Handler = null;
  onabort: Handler = null;
  error: DOMException | null = null;
  private pending = 0;
  private completionScheduled = false;

  constructor(private readonly records: Map<string, any>) {}

  run<T>(action: () => T): Request<T> {
    const result = request<T>();
    this.pending += 1;
    setTimeout(() => {
      try {
        result.result = action();
        result.onsuccess?.(new Event('success'));
      } catch (error) {
        result.error = new DOMException(String(error));
        result.onerror?.(new Event('error'));
        this.error = result.error;
        this.onerror?.(new Event('error'));
      } finally {
        this.pending -= 1;
        this.scheduleComplete();
      }
    }, 0);
    return result;
  }

  store() {
    return {
      get: (key: IDBValidKey) => this.run(() => this.records.get(String(key))),
      put: (value: { key: string }) => this.run(() => { this.records.set(value.key, value); return value.key; }),
      delete: (key: IDBValidKey) => this.run(() => { this.records.delete(String(key)); return undefined; }),
      index: (name: string) => ({
        getAll: (value: IDBValidKey) => this.run(() => Array.from(this.records.values())
          .filter(record => record[name] === value)),
      }),
    };
  }

  abort() { this.onabort?.(new Event('abort')); }

  private scheduleComplete() {
    if (this.pending || this.completionScheduled) return;
    this.completionScheduled = true;
    setTimeout(() => {
      this.completionScheduled = false;
      if (!this.pending) this.oncomplete?.(new Event('complete'));
    }, 0);
  }
}

class FakeDatabase {
  readonly stores = new Map<string, Map<string, any>>();
  readonly transactionModes: IDBTransactionMode[] = [];
  readonly objectStoreNames = { contains: (name: string) => this.stores.has(name) };
  constructor(readonly name: string, readonly version: number) {}
  createObjectStore(name: string) {
    this.stores.set(name, new Map());
    return { createIndex: () => undefined };
  }
  transaction(name: string, mode: IDBTransactionMode) {
    const records = this.stores.get(name);
    if (!records) throw new Error(`Unknown store ${name}`);
    this.transactionModes.push(mode);
    const transaction = new FakeTransaction(records);
    return Object.assign(transaction, { objectStore: (_storeName: string) => transaction.store() });
  }
  close() {}
}

class FakeIndexedDB {
  database: FakeDatabase | null = null;
  open(name: string, version = 1) {
    const openRequest = Object.assign(request<FakeDatabase>(), {
      onupgradeneeded: null as Handler,
      onblocked: null as Handler,
    });
    setTimeout(() => {
      const fresh = this.database === null;
      this.database ||= new FakeDatabase(name, version);
      openRequest.result = this.database;
      if (fresh) openRequest.onupgradeneeded?.(new Event('upgradeneeded'));
      openRequest.onsuccess?.(new Event('success'));
    }, 0);
    return openRequest;
  }
}

let storage: typeof import('./replyStorage');
let indexedDBMock: FakeIndexedDB;

const operation = (overrides: Partial<import('./replyStorage').PersistedReplySubmissionOperation> = {}) => ({
  key: '7:42', id: 'op-a', viewerID: 7, parentPostID: 42, content: 'reply',
  sourceDraftContent: null, phase: 'publishing' as const, failureKind: null,
  error: '', startedAt: 1, updatedAt: 1, post: null, ...overrides,
});

describe('replyStorage', () => {
  beforeEach(async () => {
    vi.resetModules();
    indexedDBMock = new FakeIndexedDB();
    vi.stubGlobal('indexedDB', indexedDBMock as unknown as IDBFactory);
    storage = await import('./replyStorage');
  });

  afterEach(() => vi.unstubAllGlobals());

  it('creates one versioned database with draft and operation stores', async () => {
    await storage.getReplyDraft(7, 42);
    expect(indexedDBMock.database?.name).toBe('exchangeapp-replies');
    expect(indexedDBMock.database?.version).toBe(1);
    expect(Array.from(indexedDBMock.database!.stores.keys())).toEqual([
      'reply_drafts', 'reply_submission_operations',
    ]);
  });

  it('round-trips raw scoped drafts and does not cross viewer or parent keys', async () => {
    const draft = {
      key: '7:42', viewerID: 7, parentPostID: 42,
      content: '  line one\nline two 😀  ', createdAt: 1, updatedAt: 2,
    };
    await storage.saveReplyDraft(draft);
    expect(await storage.getReplyDraft(7, 42)).toEqual(draft);
    expect(await storage.getReplyDraft(8, 42)).toBeNull();
    expect(await storage.getReplyDraft(7, 43)).toBeNull();
    expect(await storage.deleteReplyDraft(8, 42)).toBe(false);
    expect(await storage.deleteReplyDraft(7, 42)).toBe(true);
    expect(await storage.getReplyDraft(7, 42)).toBeNull();
  });

  it('conditionally deletes a draft in a readwrite transaction and preserves changed content', async () => {
    await storage.saveReplyDraft({
      key: '7:42', viewerID: 7, parentPostID: 42, content: 'saved v1', createdAt: 1, updatedAt: 2,
    });
    expect(await storage.deleteReplyDraftIfUnchanged(7, 42, 'old')).toBe('changed');
    expect(await storage.getReplyDraft(7, 42)).not.toBeNull();
    expect(await storage.deleteReplyDraftIfUnchanged(7, 42, 'saved v1')).toBe('deleted');
    expect(await storage.deleteReplyDraftIfUnchanged(7, 42, 'saved v1')).toBe('missing');
    expect(indexedDBMock.database?.transactionModes).toContain('readwrite');
  });

  it('lists operations by viewer and conditionally updates/deletes only the owning ID', async () => {
    await storage.replaceReplySubmissionOperation(operation());
    await storage.replaceReplySubmissionOperation(operation({
      key: '7:43', id: 'op-b', parentPostID: 43, content: 'second',
    }));
    await storage.replaceReplySubmissionOperation(operation({
      key: '8:42', id: 'op-c', viewerID: 8, content: 'other viewer',
    }));
    expect((await storage.listReplySubmissionOperations(7)).map(item => item.id)).toEqual(['op-a', 'op-b']);
    expect(await storage.updateReplySubmissionOperation(operation({ id: 'stale', phase: 'failed', failureKind: 'retryable' }))).toBe(false);
    expect(await storage.updateReplySubmissionOperation(operation({ phase: 'failed', failureKind: 'retryable' }))).toBe(true);
    expect(await storage.getReplySubmissionOperation(7, 42)).toMatchObject({ phase: 'failed', failureKind: 'retryable' });
    expect(await storage.deleteReplySubmissionOperation(7, 42, 'wrong-id')).toBe(false);
    expect(await storage.deleteReplySubmissionOperation(7, 42, 'op-a')).toBe(true);
    expect(await storage.getReplySubmissionOperation(7, 42)).toBeNull();
    expect(await storage.getReplySubmissionOperation(8, 42)).toMatchObject({ id: 'op-c' });
  });
});
