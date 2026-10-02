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
  private released = false;

  constructor(
    private readonly records: Map<string, any>,
    private readonly afterGet?: (key: string, value: unknown) => void,
    private readonly ready: Promise<void> = Promise.resolve(),
    private readonly releaseReadwrite: () => void = () => undefined,
  ) {}

  run<T>(action: () => T): Request<T> {
    const result = request<T>();
    this.pending += 1;
    void this.ready.then(() => new Promise<void>(resolve => setTimeout(resolve, 0))).then(() => {
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
    });
    return result;
  }

  store() {
    return {
      get: (key: IDBValidKey) => this.run(() => {
        const stringKey = String(key);
        const value = this.records.get(stringKey);
        this.afterGet?.(stringKey, value);
        return value;
      }),
      put: (value: { key: string }) => this.run(() => { this.records.set(value.key, value); return value.key; }),
      delete: (key: IDBValidKey) => this.run(() => { this.records.delete(String(key)); return undefined; }),
      index: (name: string) => ({
        getAll: (value: IDBValidKey) => this.run(() => Array.from(this.records.values())
          .filter(record => record[name] === value)),
      }),
    };
  }

  abort() {
    setTimeout(() => {
      this.onabort?.(new Event('abort'));
      this.release();
    }, 0);
  }

  private scheduleComplete() {
    if (this.pending || this.completionScheduled) return;
    this.completionScheduled = true;
    setTimeout(() => {
      this.completionScheduled = false;
      if (!this.pending) {
        this.oncomplete?.(new Event('complete'));
        this.release();
      }
    }, 0);
  }

  private release() {
    if (this.released) return;
    this.released = true;
    this.releaseReadwrite();
  }
}

class FakeDatabase {
  readonly stores = new Map<string, Map<string, any>>();
  readonly transactionModes: IDBTransactionMode[] = [];
  readonly objectStoreNames = { contains: (name: string) => this.stores.has(name) };
  afterDraftGet: ((key: string, value: unknown) => void) | null = null;
  private readonly readwriteTails = new Map<string, Promise<void>>();
  constructor(readonly name: string, readonly version: number) {}
  createObjectStore(name: string) {
    this.stores.set(name, new Map());
    return { createIndex: () => undefined };
  }
  transaction(name: string, mode: IDBTransactionMode) {
    const records = this.stores.get(name);
    if (!records) throw new Error(`Unknown store ${name}`);
    this.transactionModes.push(mode);
    const previous = mode === 'readwrite'
      ? this.readwriteTails.get(name) ?? Promise.resolve()
      : Promise.resolve();
    let release!: () => void;
    const current = mode === 'readwrite'
      ? new Promise<void>(resolve => { release = resolve; })
      : null;
    if (current) this.readwriteTails.set(name, current);
    const transaction = new FakeTransaction(
      records,
      name === 'reply_drafts' ? this.afterDraftGet ?? undefined : undefined,
      previous,
      current
        ? () => {
          release();
          if (this.readwriteTails.get(name) === current) this.readwriteTails.delete(name);
        }
        : () => undefined,
    );
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

  it('rechecks session ownership after reading and before deleting a same-content draft', async () => {
    const original = {
      key: '7:42', viewerID: 7, parentPostID: 42,
      content: 'same text', createdAt: 1, updatedAt: 2,
    };
    const nextSessionDraft = { ...original, updatedAt: 3 };
    await storage.saveReplyDraft(original);

    const database = indexedDBMock.database!;
    let currentSessionID = 'session-1';
    const shouldDelete = vi.fn(() => currentSessionID === 'session-1');
    database.afterDraftGet = (key, value) => {
      expect(key).toBe('7:42');
      expect(value).toEqual(original);
      currentSessionID = 'session-2';
      database.stores.get('reply_drafts')!.set(key, nextSessionDraft);
    };

    await expect(storage.deleteReplyDraftIfUnchanged(7, 42, 'same text', shouldDelete))
      .resolves.toBe('changed');
    expect(shouldDelete).toHaveBeenCalledTimes(2);

    database.afterDraftGet = null;
    await expect(storage.getReplyDraft(7, 42)).resolves.toEqual(nextSessionDraft);
  });

  it('lists operations by viewer and conditionally updates/deletes only the owning ID', async () => {
    await expect(storage.claimReplySubmissionOperation(operation())).resolves.toEqual({ status: 'claimed' });
    await expect(storage.claimReplySubmissionOperation(operation({
      key: '7:43', id: 'op-b', parentPostID: 43, content: 'second',
    }))).resolves.toEqual({ status: 'claimed' });
    await expect(storage.claimReplySubmissionOperation(operation({
      key: '8:42', id: 'op-c', viewerID: 8, content: 'other viewer',
    }))).resolves.toEqual({ status: 'claimed' });
    expect((await storage.listReplySubmissionOperations(7)).map(item => item.id)).toEqual(['op-a', 'op-b']);
    expect(await storage.updateReplySubmissionOperation(operation({ id: 'stale', phase: 'failed', failureKind: 'retryable' }))).toBe(false);
    expect(await storage.updateReplySubmissionOperation(operation({ phase: 'failed', failureKind: 'retryable' }))).toBe(true);
    expect(await storage.getReplySubmissionOperation(7, 42)).toMatchObject({ phase: 'failed', failureKind: 'retryable' });
    expect(await storage.deleteReplySubmissionOperation(7, 42, 'wrong-id')).toBe(false);
    expect(await storage.deleteReplySubmissionOperation(7, 42, 'op-a')).toBe(true);
    expect(await storage.getReplySubmissionOperation(7, 42)).toBeNull();
    expect(await storage.getReplySubmissionOperation(8, 42)).toMatchObject({ id: 'op-c' });
  });

  it('returns the validated durable owner without replacing an occupied reply slot', async () => {
    const owner = operation({ viewerSessionID: 'session-7' });
    const candidate = operation({ id: 'op-b', viewerSessionID: 'session-7' });

    await expect(storage.claimReplySubmissionOperation(owner)).resolves.toEqual({ status: 'claimed' });
    await expect(storage.claimReplySubmissionOperation(candidate)).resolves.toMatchObject({
      status: 'occupied',
      operation: { id: 'op-a', viewerID: 7, parentPostID: 42, viewerSessionID: 'session-7' },
    });
    await expect(storage.getReplySubmissionOperation(7, 42)).resolves.toMatchObject({ id: 'op-a' });
  });

  it('serializes concurrent same-slot claims while allowing different reply slots', async () => {
    const [claimA, claimB] = await Promise.all([
      storage.claimReplySubmissionOperation(operation({ id: 'candidate-a' })),
      storage.claimReplySubmissionOperation(operation({ id: 'candidate-b' })),
    ]);
    const results = [claimA, claimB];
    const winner = results.find(result => result.status === 'claimed');
    const loser = results.find(result => result.status === 'occupied');

    expect(results.filter(result => result.status === 'claimed')).toHaveLength(1);
    expect(results.filter(result => result.status === 'occupied')).toHaveLength(1);
    if (winner?.status !== 'claimed' || loser?.status !== 'occupied') {
      throw new Error('same-slot reply claims did not produce one winner and one loser');
    }
    expect(loser.operation.id).toBe(winner === claimA ? 'candidate-a' : 'candidate-b');
    await expect(storage.getReplySubmissionOperation(7, 42)).resolves.toMatchObject({ id: loser.operation.id });

    const independent = await Promise.all([
      storage.claimReplySubmissionOperation(operation({
        key: '7:43', id: 'reply-43', parentPostID: 43,
      })),
      storage.claimReplySubmissionOperation(operation({
        key: '8:42', id: 'reply-8-42', viewerID: 8,
      })),
    ]);
    expect(independent).toEqual([{ status: 'claimed' }, { status: 'claimed' }]);
  });

  it('fails closed on a corrupted occupied operation without replacing it', async () => {
    const corrupted = operation({ content: '  not canonical  ' });
    await storage.getReplySubmissionOperation(7, 42);
    indexedDBMock.database!.stores.get('reply_submission_operations')!.set('7:42', corrupted);

    await expect(storage.claimReplySubmissionOperation(operation({ id: 'candidate' })))
      .rejects.toThrow('saved reply operation is invalid');
    expect(indexedDBMock.database!.stores.get('reply_submission_operations')!.get('7:42')).toBe(corrupted);
  });

  it('rejects invalid phase/failure combinations and accepts canonical failed records', async () => {
    const invalidShapes = [
      { phase: 'failed' as const, failureKind: null },
      { phase: 'publishing' as const, failureKind: 'retryable' as const },
      { phase: 'succeeded' as const, failureKind: 'auth_context_changed' as const },
    ];
    for (const shape of invalidShapes) {
      await expect(storage.claimReplySubmissionOperation(operation(shape)))
        .rejects.toThrow('saved reply operation is invalid');
    }

    const valid = operation({ phase: 'failed', failureKind: 'retryable' });
    await expect(storage.claimReplySubmissionOperation(valid)).resolves.toEqual({ status: 'claimed' });
    await expect(storage.getReplySubmissionOperation(7, 42)).resolves.toMatchObject({
      phase: 'failed',
      failureKind: 'retryable',
    });
  });
});
