import type { Post } from '../types/Post';

export type PersistedPublishPhase = 'uploading' | 'publishing' | 'failed' | 'succeeded';
export type PersistedPublishFailureKind = 'retryable' | 'idempotency_conflict' | null;

export type PublishOperationMediaValue = {
  draftMediaID: string;
  file: File;
  uploadedURL: string;
};

export type PublishOperationValue = {
  id: string;
  publisherUserID: number;
  sourceDraftID: string | null;
  content: string;
  media: PublishOperationMediaValue[];
  phase: PersistedPublishPhase;
  failureKind: PersistedPublishFailureKind;
  error: string;
  startedAt: number;
  post: Post | null;
};

export type PersistedPublishOperationMedia = {
  draftMediaID: string;
  blob: Blob;
  name: string;
  type: string;
  size: number;
  lastModified: number;
  uploadedURL: string;
};

export type PersistedPostPublishOperation = {
  id: string;
  publisherUserID: number;
  sourceDraftID: string | null;
  content: string;
  media: PersistedPublishOperationMedia[];
  phase: PersistedPublishPhase;
  failureKind: PersistedPublishFailureKind;
  error: string;
  startedAt: number;
  updatedAt: number;
  post: Post | null;
};

const DATABASE_NAME = 'exchangeapp-publish-operations';
const DATABASE_VERSION = 1;
const OBJECT_STORE_NAME = 'post_publish_operations';

let databasePromise: Promise<IDBDatabase> | null = null;

const validViewerID = (viewerID: unknown): viewerID is number => (
  typeof viewerID === 'number'
  && Number.isSafeInteger(viewerID)
  && viewerID > 0
);

const validPhase = (phase: unknown): phase is PersistedPublishPhase => (
  phase === 'uploading'
  || phase === 'publishing'
  || phase === 'failed'
  || phase === 'succeeded'
);

const validateRecord = (value: unknown): PersistedPostPublishOperation => {
  if (!value || typeof value !== 'object') {
    throw new Error('The saved publish operation is invalid.');
  }
  const record = value as Partial<PersistedPostPublishOperation>;
  if (
    typeof record.id !== 'string'
    || !record.id.trim()
    || !validViewerID(record.publisherUserID)
    || !(record.sourceDraftID === null || typeof record.sourceDraftID === 'string')
    || typeof record.content !== 'string'
    || !Array.isArray(record.media)
    || !validPhase(record.phase)
    || !(record.failureKind === null || record.failureKind === 'retryable'
      || record.failureKind === 'idempotency_conflict')
    || typeof record.error !== 'string'
    || typeof record.startedAt !== 'number'
    || !Number.isFinite(record.startedAt)
    || typeof record.updatedAt !== 'number'
    || !Number.isFinite(record.updatedAt)
    || !(record.post === null || (typeof record.post === 'object' && record.post !== null))
  ) {
    throw new Error('The saved publish operation is invalid.');
  }

  for (const media of record.media) {
    if (
      !media
      || typeof media !== 'object'
      || typeof media.draftMediaID !== 'string'
      || !media.draftMediaID.trim()
      || !(media.blob instanceof Blob)
      || typeof media.name !== 'string'
      || typeof media.type !== 'string'
      || typeof media.size !== 'number'
      || !Number.isFinite(media.size)
      || media.size < 0
      || typeof media.lastModified !== 'number'
      || !Number.isFinite(media.lastModified)
      || typeof media.uploadedURL !== 'string'
    ) {
      throw new Error('The saved publish media is invalid.');
    }
  }

  return record as PersistedPostPublishOperation;
};

const openDatabase = (): Promise<IDBDatabase> => {
  if (databasePromise) return databasePromise;
  if (typeof indexedDB === 'undefined') {
    return Promise.reject(new Error('IndexedDB is unavailable.'));
  }

  databasePromise = new Promise<IDBDatabase>((resolve, reject) => {
    let request: IDBOpenDBRequest;
    try {
      request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION);
    } catch (error) {
      reject(error);
      return;
    }

    request.onupgradeneeded = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(OBJECT_STORE_NAME)) {
        database.createObjectStore(OBJECT_STORE_NAME, { keyPath: 'publisherUserID' });
      }
    };
    request.onerror = () => reject(request.error || new Error('Could not open publish storage.'));
    request.onblocked = () => reject(new Error('Publish storage is blocked by another tab.'));
    request.onsuccess = () => {
      const database = request.result;
      database.onversionchange = () => {
        database.close();
        databasePromise = null;
      };
      resolve(database);
    };
  }).catch(error => {
    databasePromise = null;
    throw error;
  });

  return databasePromise;
};

const withStore = async <T>(
  mode: IDBTransactionMode,
  execute: (store: IDBObjectStore, transaction: IDBTransaction) => Promise<T>,
): Promise<T> => {
  const database = await openDatabase();
  return new Promise<T>((resolve, reject) => {
    let transaction: IDBTransaction;
    try {
      transaction = database.transaction(OBJECT_STORE_NAME, mode);
    } catch (error) {
      reject(error);
      return;
    }

    let result: T;
    let settled = false;
    const fail = (error: unknown) => {
      if (!settled) {
        settled = true;
        reject(error);
      }
    };

    transaction.oncomplete = () => {
      if (!settled) {
        settled = true;
        resolve(result);
      }
    };
    transaction.onerror = () => fail(transaction.error || new Error('Publish storage failed.'));
    transaction.onabort = () => fail(transaction.error || new Error('Publish storage was aborted.'));

    void execute(transaction.objectStore(OBJECT_STORE_NAME), transaction)
      .then(value => {
        result = value;
      })
      .catch(error => {
        try {
          transaction.abort();
        } catch {
          // The transaction may already have completed.
        }
        fail(error);
      });
  });
};

const requestResult = <T>(request: IDBRequest<T>): Promise<T> => new Promise((resolve, reject) => {
  request.onsuccess = () => resolve(request.result);
  request.onerror = () => reject(request.error || new Error('Publish storage request failed.'));
});

export const getPostPublishOperation = async (
  viewerID: number,
): Promise<PersistedPostPublishOperation | null> => {
  if (!validViewerID(viewerID)) throw new Error('Invalid publish viewer.');
  return withStore('readonly', async store => {
    const value = await requestResult(store.get(viewerID));
    return value === undefined ? null : validateRecord(value);
  });
};

export const replacePostPublishOperation = async (
  operation: PersistedPostPublishOperation,
): Promise<void> => {
  const record = validateRecord(operation);
  await withStore('readwrite', async store => {
    await requestResult(store.put(record));
  });
};

export const updatePostPublishOperation = async (
  operation: PersistedPostPublishOperation,
): Promise<boolean> => {
  const record = validateRecord(operation);
  return withStore('readwrite', async store => {
    const value = await requestResult(store.get(record.publisherUserID));
    if (value === undefined) return false;
    const current = validateRecord(value);
    if (current.publisherUserID !== record.publisherUserID || current.id !== record.id) {
      return false;
    }
    await requestResult(store.put(record));
    return true;
  });
};

export const deletePostPublishOperation = async (
  viewerID: number,
  operationID: string,
): Promise<boolean> => {
  if (!validViewerID(viewerID) || !operationID.trim()) return false;
  return withStore('readwrite', async store => {
    const value = await requestResult(store.get(viewerID));
    if (value === undefined) return false;
    const current = validateRecord(value);
    if (current.publisherUserID !== viewerID || current.id !== operationID) return false;
    await requestResult(store.delete(viewerID));
    return true;
  });
};

export const serializePublishOperation = (
  operation: PublishOperationValue,
  updatedAt = Date.now(),
): PersistedPostPublishOperation => validateRecord({
  ...operation,
  media: operation.media.map(item => ({
    draftMediaID: item.draftMediaID,
    blob: item.file.slice(0, item.file.size, item.file.type),
    name: item.file.name,
    type: item.file.type,
    size: item.file.size,
    lastModified: item.file.lastModified,
    uploadedURL: item.uploadedURL,
  })),
  updatedAt,
});

export const restorePublishOperation = (
  persisted: PersistedPostPublishOperation,
): PublishOperationValue => {
  const record = validateRecord(persisted);
  return {
    id: record.id,
    publisherUserID: record.publisherUserID,
    sourceDraftID: record.sourceDraftID,
    content: record.content,
    media: record.media.map(item => ({
      draftMediaID: item.draftMediaID,
      file: new File([item.blob], item.name, {
        type: item.type,
        lastModified: item.lastModified,
      }),
      uploadedURL: item.uploadedURL,
    })),
    phase: record.phase,
    failureKind: record.failureKind,
    error: record.error,
    startedAt: record.startedAt,
    post: record.post,
  };
};
