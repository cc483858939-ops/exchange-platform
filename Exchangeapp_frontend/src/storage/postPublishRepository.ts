import { indexedDBRequestResult, withIndexedDBStore } from './indexedDBTransaction';
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

export type PublishSourceDraft =
  | { sourceDraftID: null; sourceDraftSnapshot: null }
  | { sourceDraftID: string; sourceDraftSnapshot: DraftSnapshot };

export type PublishOperationValue = PublishSourceDraft & {
  id: string;
  publisherUserID: number;
  publisherSessionID: string | null;
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

export type PersistedPostPublishOperation = PublishSourceDraft & {
  schemaVersion: 4;
  id: string;
  publisherUserID: number;
  publisherSessionID: string;
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

export type PostPublishClaimResult =
  | { status: 'claimed' }
  | { status: 'occupied'; operation: PersistedPostPublishOperation };

export type PersistedPublishCheckpoint = PublishSourceDraft
  & Omit<PersistedPostPublishOperation, 'media' | 'sourceDraftID' | 'sourceDraftSnapshot'> & {
  media: Omit<PersistedPublishOperationMedia, 'blob'>[];
};

const DATABASE_NAME = 'exchangeapp-publish-operations';
const DATABASE_VERSION = 2;
const OBJECT_STORE_NAME = 'post_publish_operations';
const CHECKPOINT_STORE_NAME = 'post_publish_checkpoints';

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

const isDraftSnapshot = (value: unknown): value is DraftSnapshot => {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<DraftSnapshot>;
  const quotePostID = snapshot.quotePostID;
  return typeof snapshot.content === 'string'
    && (quotePostID === null || isValidQuotePostID(quotePostID))
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
  if (
    record.schemaVersion !== 4
    || typeof record.id !== 'string'
    || !record.id.trim()
    || !validViewerID(record.publisherUserID)
    || !(record.sourceDraftID === null
      || (typeof record.sourceDraftID === 'string' && record.sourceDraftID.trim()))
    || typeof record.content !== 'string'
    || !Array.isArray(record.media)
    || !validPhase(record.phase)
    || !isDurableSubmissionFailureKind(record.failureKind)
    || !hasValidDurableSubmissionFailureShape(record.phase, record.failureKind)
    || typeof record.publisherSessionID !== 'string'
    || !record.publisherSessionID.trim()
    || !(record.quotePostID === null || isValidQuotePostID(record.quotePostID))
    || typeof record.error !== 'string'
    || typeof record.startedAt !== 'number'
    || !Number.isFinite(record.startedAt)
    || typeof record.updatedAt !== 'number'
    || !Number.isFinite(record.updatedAt)
    || !(record.post === null || (typeof record.post === 'object' && record.post !== null))
  ) {
    throw new Error('The saved publish operation is invalid.');
  }

  if (record.sourceDraftSnapshot !== null && !isDraftSnapshot(record.sourceDraftSnapshot)) {
    throw new Error('The saved publish source snapshot is invalid.');
  }
  if ((record.sourceDraftID === null) !== (record.sourceDraftSnapshot === null)) {
    throw new Error('The saved publish source draft ID and snapshot must be present together.');
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
    schemaVersion: 4,
    id: record.id,
    publisherUserID: record.publisherUserID,
    publisherSessionID: record.publisherSessionID,
    sourceDraftID: record.sourceDraftID,
    sourceDraftSnapshot: record.sourceDraftSnapshot,
    content: record.content,
    quotePostID: record.quotePostID,
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
      if (!database.objectStoreNames.contains(CHECKPOINT_STORE_NAME)) {
        database.createObjectStore(CHECKPOINT_STORE_NAME, { keyPath: 'publisherUserID' });
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
): Promise<T> => withIndexedDBStore(await openDatabase(), [OBJECT_STORE_NAME, CHECKPOINT_STORE_NAME], mode, execute, {
  failed: 'Publish storage failed.',
  aborted: 'Publish storage was aborted.',
});

const requestResult = <T>(request: IDBRequest<T>): Promise<T> => (
  indexedDBRequestResult(request, 'Publish storage request failed.')
);

const checkpointFromRecord = (record: PersistedPostPublishOperation): PersistedPublishCheckpoint => ({
  ...record,
  media: record.media.map(({ blob: _blob, ...metadata }) => metadata),
});

const mergeCheckpoint = (
  record: PersistedPostPublishOperation,
  value: unknown,
): PersistedPostPublishOperation => {
  if (value === undefined) return record;
  const checkpoint = value as Partial<PersistedPublishCheckpoint> | null;
  if (!checkpoint || checkpoint.id !== record.id
    || checkpoint.publisherUserID !== record.publisherUserID
    || !Array.isArray(checkpoint.media) || checkpoint.media.length !== record.media.length) {
    throw new Error('The saved publish checkpoint is invalid.');
  }
  const media = checkpoint.media.map((metadata, index) => {
    const original = record.media[index]!;
    // A checkpoint may advance uploads, but cannot replace the saved recovery files.
    if (!metadata || metadata.draftMediaID !== original.draftMediaID
      || metadata.name !== original.name || metadata.type !== original.type
      || metadata.size !== original.size || metadata.lastModified !== original.lastModified) {
      throw new Error('The saved publish checkpoint media is invalid.');
    }
    return { ...metadata, blob: original.blob };
  });
  return validateRecord({ ...checkpoint, media });
};

export const getPostPublishOperation = async (
  viewerID: number,
): Promise<PersistedPostPublishOperation | null> => {
  if (!validViewerID(viewerID)) throw new Error('Invalid publish viewer.');
  return withStore('readonly', async (store, transaction) => {
    const value = await requestResult(store.get(viewerID));
    if (value === undefined) return null;
    const checkpoint = await requestResult(transaction.objectStore(CHECKPOINT_STORE_NAME).get(viewerID));
    return mergeCheckpoint(validateRecord(value), checkpoint);
  });
};

export const claimPostPublishOperation = async (
  operation: PersistedPostPublishOperation,
): Promise<PostPublishClaimResult> => {
  const record = validateRecord(operation);
  return withStore('readwrite', async (store, transaction) => {
    const checkpoints = transaction.objectStore(CHECKPOINT_STORE_NAME);
    const value = await requestResult(store.get(record.publisherUserID));
    if (value !== undefined) {
      const current = validateRecord(value);
      if (current.publisherUserID !== record.publisherUserID) {
        throw new Error('The saved publish operation belongs to another viewer.');
      }
      return { status: 'occupied', operation: mergeCheckpoint(current, await requestResult(checkpoints.get(record.publisherUserID))) };
    }
    await requestResult(store.put(record));
    await requestResult(checkpoints.delete(record.publisherUserID));
    return { status: 'claimed' };
  });
};

export const updatePostPublishOperationCheckpoint = async (
  checkpoint: PersistedPublishCheckpoint,
): Promise<boolean> => {
  if (!validViewerID(checkpoint.publisherUserID) || typeof checkpoint.id !== 'string' || !checkpoint.id.trim()) {
    throw new Error('The publish checkpoint owner is invalid.');
  }
  return withStore('readwrite', async (store, transaction) => {
    const value = await requestResult(store.get(checkpoint.publisherUserID));
    if (value === undefined) return false;
    const current = validateRecord(value);
    if (current.publisherUserID !== checkpoint.publisherUserID || current.id !== checkpoint.id) return false;
    const record = mergeCheckpoint(current, checkpoint);
    await requestResult(transaction.objectStore(CHECKPOINT_STORE_NAME).put(checkpointFromRecord(record)));
    return true;
  });
};

export const deletePostPublishOperation = async (
  viewerID: number,
  operationID: string,
): Promise<boolean> => {
  if (!validViewerID(viewerID) || !operationID.trim()) return false;
  return withStore('readwrite', async (store, transaction) => {
    const value = await requestResult(store.get(viewerID));
    if (value === undefined) return false;
    const current = validateRecord(value);
    if (current.publisherUserID !== viewerID || current.id !== operationID) return false;
    await requestResult(store.delete(viewerID));
    await requestResult(transaction.objectStore(CHECKPOINT_STORE_NAME).delete(viewerID));
    return true;
  });
};

const copyPublishSourceDraft = (source: PublishSourceDraft): PublishSourceDraft => (
  source.sourceDraftID === null
    ? { sourceDraftID: null, sourceDraftSnapshot: null }
    : {
      sourceDraftID: source.sourceDraftID,
      sourceDraftSnapshot: createPostDraftSnapshot(
        source.sourceDraftSnapshot.content,
        source.sourceDraftSnapshot.media,
        source.sourceDraftSnapshot.quotePostID,
      ),
    }
);

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
  const record = validateRecord({
    schemaVersion: 4,
    id: operation.id,
    publisherUserID: operation.publisherUserID,
    publisherSessionID: operation.publisherSessionID,
    sourceDraftID: operation.sourceDraftID,
    sourceDraftSnapshot: operation.sourceDraftSnapshot,
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
  return { ...record, ...copyPublishSourceDraft(record) };
};

export const serializePublishOperationCheckpoint = (
  operation: PublishOperationValue,
  updatedAt = Date.now(),
): PersistedPublishCheckpoint => checkpointFromRecord(serializePublishOperation(operation, updatedAt));

export const restorePublishOperation = (
  persisted: PersistedPostPublishOperation,
): PublishOperationValue => {
  const record = validateRecord(persisted);
  return {
    ...copyPublishSourceDraft(record),
    id: record.id,
    publisherUserID: record.publisherUserID,
    publisherSessionID: record.publisherSessionID,
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
