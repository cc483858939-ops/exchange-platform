// @vitest-environment jsdom

import { IDBFactory } from 'fake-indexeddb';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const DATABASE_NAME = 'exchangeapp-publish-operations';
const OPERATIONS_STORE = 'post_publish_operations';
const CHECKPOINTS_STORE = 'post_publish_checkpoints';

const openDatabase = (
  factory: IDBFactory,
  version?: number,
  onUpgrade?: (database: IDBDatabase) => void,
) => new Promise<IDBDatabase>((resolve, reject) => {
  const request = version === undefined
    ? factory.open(DATABASE_NAME)
    : factory.open(DATABASE_NAME, version);
  request.onupgradeneeded = () => onUpgrade?.(request.result);
  request.onerror = () => reject(request.error || new Error('Could not open test database.'));
  request.onsuccess = () => resolve(request.result);
});

const transactionComplete = (transaction: IDBTransaction) => new Promise<void>((resolve, reject) => {
  transaction.oncomplete = () => resolve();
  transaction.onabort = () => reject(transaction.error || new Error('Test transaction was aborted.'));
  transaction.onerror = () => reject(transaction.error || new Error('Test transaction failed.'));
});

const requestResult = <T,>(request: IDBRequest<T>) => new Promise<T>((resolve, reject) => {
  request.onsuccess = () => resolve(request.result);
  request.onerror = () => reject(request.error || new Error('Test request failed.'));
});

describe('postPublishRepository IndexedDB upgrades', () => {
  let factory: IDBFactory;

  beforeEach(() => {
    factory = new IDBFactory();
    vi.stubGlobal('indexedDB', factory);
  });

  afterEach(async () => {
    const request = factory.deleteDatabase(DATABASE_NAME);
    await new Promise<void>((resolve, reject) => {
      request.onsuccess = () => resolve();
      request.onerror = () => reject(request.error || new Error('Could not delete test database.'));
    });
    vi.resetModules();
    vi.unstubAllGlobals();
  });

  it('creates a new v2 database with both stores', async () => {
    vi.resetModules();
    const repository = await import('./postPublishRepository');

    await expect(repository.getPostPublishOperation(7)).resolves.toBeNull();

    const database = await openDatabase(factory);
    expect(database.version).toBe(2);
    expect(database.objectStoreNames.contains(OPERATIONS_STORE)).toBe(true);
    expect(database.objectStoreNames.contains(CHECKPOINTS_STORE)).toBe(true);
    expect(database.transaction(OPERATIONS_STORE).objectStore(OPERATIONS_STORE).keyPath)
      .toBe('publisherUserID');
    expect(database.transaction(CHECKPOINTS_STORE).objectStore(CHECKPOINTS_STORE).keyPath)
      .toBe('publisherUserID');
    database.close();
  });

  it('upgrades v1 without recreating operations or changing its data', async () => {
    const legacyRecord = { publisherUserID: 17, marker: 'preserve-existing-operation' };
    const versionOne = await openDatabase(factory, 1, database => {
      database.createObjectStore(OPERATIONS_STORE, { keyPath: 'publisherUserID' });
    });
    const writeTransaction = versionOne.transaction(OPERATIONS_STORE, 'readwrite');
    const writeComplete = transactionComplete(writeTransaction);
    writeTransaction.objectStore(OPERATIONS_STORE).put(legacyRecord);
    await writeComplete;
    versionOne.close();

    vi.resetModules();
    const repository = await import('./postPublishRepository');
    await expect(repository.getPostPublishOperation(17))
      .rejects.toThrow('saved publish operation is invalid');

    const upgraded = await openDatabase(factory);
    expect(upgraded.version).toBe(2);
    expect(upgraded.objectStoreNames.contains(OPERATIONS_STORE)).toBe(true);
    expect(upgraded.objectStoreNames.contains(CHECKPOINTS_STORE)).toBe(true);

    const readTransaction = upgraded.transaction(OPERATIONS_STORE, 'readonly');
    const readComplete = transactionComplete(readTransaction);
    await expect(requestResult(readTransaction.objectStore(OPERATIONS_STORE).get(17)))
      .resolves.toEqual(legacyRecord);
    await readComplete;
    upgraded.close();
  });

  it('reopens an existing v2 database and reads normally', async () => {
    const versionTwo = await openDatabase(factory, 2, database => {
      database.createObjectStore(OPERATIONS_STORE, { keyPath: 'publisherUserID' });
      database.createObjectStore(CHECKPOINTS_STORE, { keyPath: 'publisherUserID' });
    });
    versionTwo.close();

    vi.resetModules();
    const firstRepository = await import('./postPublishRepository');
    await expect(firstRepository.getPostPublishOperation(29)).resolves.toBeNull();

    vi.resetModules();
    const reopenedRepository = await import('./postPublishRepository');
    await expect(reopenedRepository.getPostPublishOperation(29)).resolves.toBeNull();

    const reopened = await openDatabase(factory);
    expect(reopened.version).toBe(2);
    expect(reopened.objectStoreNames.contains(OPERATIONS_STORE)).toBe(true);
    expect(reopened.objectStoreNames.contains(CHECKPOINTS_STORE)).toBe(true);
    reopened.close();
  });
});
