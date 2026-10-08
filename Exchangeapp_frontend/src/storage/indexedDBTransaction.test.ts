import { describe, expect, it, vi } from 'vitest';
import { withIndexedDBStore } from './indexedDBTransaction';

const fixture = () => {
  const transaction = { oncomplete: null, onerror: null, onabort: null, error: null, objectStore: vi.fn(() => ({})), abort: vi.fn() };
  const database = { transaction: vi.fn(() => transaction) } as unknown as IDBDatabase;
  return { transaction, database };
};
const errors = { failed: 'failed', aborted: 'aborted' };

describe('durable IndexedDB transaction ownership', () => {
  it('keeps file and checkpoint requests in one transaction and rejects an abort after both succeed', async () => {
    const { transaction, database } = fixture();
    const stores = ['files', 'checkpoints'];
    const result = withIndexedDBStore(database, stores, 'readwrite', async (_files, owner) => {
      owner.objectStore('checkpoints');
      return true;
    }, errors);
    expect(database.transaction).toHaveBeenCalledWith(stores, 'readwrite');
    expect(transaction.objectStore.mock.calls).toEqual([['files'], ['checkpoints']]);
    await Promise.resolve();
    (transaction.onabort as unknown as () => void)();
    await expect(result).rejects.toThrow('aborted');
  });

  it('waits for commit after the request succeeds and rejects a later abort', async () => {
    const { transaction, database } = fixture();
    let settled = false;
    const result = withIndexedDBStore(database, 'records', 'readwrite', async () => 42, errors);
    void result.then(() => { settled = true; }, () => { settled = true; });
    await Promise.resolve();
    expect(settled).toBe(false);
    (transaction.onabort as unknown as () => void)();
    await expect(result).rejects.toThrow('aborted');
    (transaction.oncomplete as unknown as () => void)();
    expect(settled).toBe(true);
  });

  it('returns the request value only on commit and aborts business failures', async () => {
    const committed = fixture();
    const value = withIndexedDBStore(committed.database, 'records', 'readonly', async () => 42, errors);
    await Promise.resolve();
    (committed.transaction.oncomplete as unknown as () => void)();
    await expect(value).resolves.toBe(42);
    const failed = fixture();
    const cause = new Error('quota exceeded');
    await expect(withIndexedDBStore(failed.database, 'records', 'readwrite', async () => { throw cause; }, errors)).rejects.toBe(cause);
    expect(failed.transaction.abort).toHaveBeenCalledOnce();
  });
});
