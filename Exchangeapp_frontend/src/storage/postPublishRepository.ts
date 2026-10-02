import type { Post } from '../types/Post';
import {
  createPostDraftSnapshot,
  isValidQuotePostID,
  type DraftSnapshot,
} from '../utils/postDraftSnapshot';
import {
  hasValidDurableSubmissionFailureShape,
  isDurableSubmissionFailureKind,
  type DurableSubmissionFailureKind,
} from '../utils/durableSubmissionContract';

export type PersistedPublishPhase = 'uploading' | 'publishing' | 'failed' | 'succeeded';
export type PersistedPublishFailureKind = DurableSubmissionFailureKind;

export type PublishOperationMediaValue = {
  draftMediaID: string;
  file: File;
  uploadedURL: string;
};

export type PublishOperationValue = {
  id: string;
  publisherUserID: number;
  publisherSessionID: string | null;
  sourceDraftID: string | null;
  sourceDraftSnapshot: DraftSnapshot | null;
  content: string;
  quotePostID: number | null;
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
  schemaVersion: 1 | 2 | 3 | 4;
  id: string;
  publisherUserID: number;
  publisherSessionID: string | null;
  sourceDraftID: string | null;
  sourceDraftSnapshot: DraftSnapshot | null;
  content: string;
  quotePostID: number | null;
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

const isDraftSnapshot = (value: unknown, requireQuoteIdentity: boolean): value is DraftSnapshot => {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<DraftSnapshot>;
  const quotePostID = snapshot.quotePostID;
  return typeof snapshot.content === 'string'
    && (requireQuoteIdentity
      ? Object.prototype.hasOwnProperty.call(snapshot, 'quotePostID')
        && (quotePostID === null || isValidQuotePostID(quotePostID))
      : quotePostID === undefined || quotePostID === null || isValidQuotePostID(quotePostID))
    && Array.isArray(snapshot.media)
    && snapshot.media.every(item => Boolean(
      item
      && typeof item.id === 'string'
      && item.id.length > 0
      && typeof item.name === 'string'
      && typeof item.type === 'string'
      && Number.isFinite(item.size)
      && item.size >= 0
      && Number.isFinite(item.lastModified),
    ));
};

const validateRecord = (value: unknown): PersistedPostPublishOperation => {
  if (!value || typeof value !== 'object') {
    throw new Error('The saved publish operation is invalid.');
  }
  const record = value as Partial<PersistedPostPublishOperation>;
  const schemaVersion = record.schemaVersion === undefined ? 1 : record.schemaVersion;
  if (
    (schemaVersion !== 1 && schemaVersion !== 2 && schemaVersion !== 3 && schemaVersion !== 4)
    || typeof record.id !== 'string'
    || !record.id.trim()
    || !validViewerID(record.publisherUserID)
    || !(record.sourceDraftID === null || typeof record.sourceDraftID === 'string')
    || typeof record.content !== 'string'
    || !Array.isArray(record.media)
    || !validPhase(record.phase)
    || !isDurableSubmissionFailureKind(record.failureKind)
    || !hasValidDurableSubmissionFailureShape(record.phase, record.failureKind)
    || ((schemaVersion === 3 || schemaVersion === 4) && (typeof record.publisherSessionID !== 'string'
      || !record.publisherSessionID.trim()))
    || (schemaVersion === 4 && (!Object.prototype.hasOwnProperty.call(record, 'quotePostID')
      || !(record.quotePostID === null || isValidQuotePostID(record.quotePostID))))
    || typeof record.error !== 'string'
    || typeof record.startedAt !== 'number'
    || !Number.isFinite(record.startedAt)
    || typeof record.updatedAt !== 'number'
    || !Number.isFinite(record.updatedAt)
    || !(record.post === null || (typeof record.post === 'object' && record.post !== null))
  ) {
    throw new Error('The saved publish operation is invalid.');
  }

  let sourceDraftSnapshot: DraftSnapshot | null = null;
  if (schemaVersion === 2 || schemaVersion === 3 || schemaVersion === 4) {
    if (record.sourceDraftSnapshot === undefined) {
      throw new Error('The saved publish source snapshot is invalid.');
    }
    if (record.sourceDraftSnapshot !== null) {
      if (!isDraftSnapshot(record.sourceDraftSnapshot, schemaVersion === 4)) {
        throw new Error('The saved publish source snapshot is invalid.');
      }
      sourceDraftSnapshot = createPostDraftSnapshot(
        record.sourceDraftSnapshot.content,
        record.sourceDraftSnapshot.media,
        record.sourceDraftSnapshot.quotePostID ?? null,
      );
    }
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

  return {
    schemaVersion,
    id: record.id,
    publisherUserID: record.publisherUserID,
    publisherSessionID: schemaVersion === 3 || schemaVersion === 4
      ? record.publisherSessionID as string
      : null,
    sourceDraftID: record.sourceDraftID,
    // Records created before schema 2 deliberately retain no deletion authority.
    sourceDraftSnapshot,
    content: record.content,
    quotePostID: schemaVersion === 4 ? record.quotePostID as number | null : null,
    media: record.media as PersistedPublishOperationMedia[],
    phase: record.phase,
    failureKind: record.failureKind,
    error: record.error,
    startedAt: record.startedAt,
    updatedAt: record.updatedAt,
    post: record.post,
  } as PersistedPostPublishOperation;
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
): PersistedPostPublishOperation => {
  if (typeof operation.publisherSessionID !== 'string' || !operation.publisherSessionID.trim()) {
    throw new Error('A publish operation must be bound to an authentication session.');
  }
  if (operation.quotePostID !== null && !isValidQuotePostID(operation.quotePostID)) {
    throw new Error('A publish operation quote ID must be a positive safe integer.');
  }
  return validateRecord({
    schemaVersion: 4,
    id: operation.id,
    publisherUserID: operation.publisherUserID,
    publisherSessionID: operation.publisherSessionID,
    sourceDraftID: operation.sourceDraftID,
    sourceDraftSnapshot: operation.sourceDraftSnapshot === null
      ? null
      : createPostDraftSnapshot(
        operation.sourceDraftSnapshot.content,
        operation.sourceDraftSnapshot.media,
        operation.sourceDraftSnapshot.quotePostID,
      ),
    quotePostID: operation.quotePostID,
    media: operation.media.map(item => ({
      draftMediaID: item.draftMediaID,
      blob: item.file.slice(0, item.file.size, item.file.type),
      name: item.file.name,
      type: item.file.type,
      size: item.file.size,
      lastModified: item.file.lastModified,
      uploadedURL: item.uploadedURL,
    })),
    content: operation.content,
    phase: operation.phase,
    failureKind: operation.failureKind,
    error: operation.error,
    startedAt: operation.startedAt,
    updatedAt,
    post: operation.post,
  });
};

export const restorePublishOperation = (
  persisted: PersistedPostPublishOperation,
): PublishOperationValue => {
  const record = validateRecord(persisted);
  return {
    id: record.id,
    publisherUserID: record.publisherUserID,
    publisherSessionID: record.publisherSessionID,
    sourceDraftID: record.sourceDraftID,
    sourceDraftSnapshot: record.sourceDraftSnapshot === null
      ? null
      : createPostDraftSnapshot(
        record.sourceDraftSnapshot.content,
        record.sourceDraftSnapshot.media,
        record.sourceDraftSnapshot.quotePostID,
      ),
    content: record.content,
    quotePostID: record.quotePostID,
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
