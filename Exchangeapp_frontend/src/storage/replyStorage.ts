import { indexedDBRequestResult, withIndexedDBStore } from './indexedDBTransaction';
import type { Post } from '../types/Post';
import {
  hasValidDurableSubmissionFailureShape,
  isDurableSubmissionFailureKind,
  type DurableSubmissionFailureKind,
} from '../utils/durableSubmissionContract';

export type ReplySubmissionPhase = 'publishing' | 'failed' | 'succeeded';
export type ReplySubmissionFailureKind = DurableSubmissionFailureKind;

export type PersistedReplyDraft = {
  key: string;
  viewerID: number;
  parentPostID: number;
  content: string;
  createdAt: number;
  updatedAt: number;
};

export type PersistedReplySubmissionOperation = {
  key: string;
  id: string;
  viewerID: number;
  viewerSessionID: string;
  parentPostID: number;
  content: string;
  sourceDraftContent: string | null;
  phase: ReplySubmissionPhase;
  failureKind: ReplySubmissionFailureKind;
  error: string;
  startedAt: number;
  updatedAt: number;
  post: Post | null;
};

export type ReplySubmissionClaimResult =
  | { status: 'claimed' }
  | { status: 'occupied'; operation: PersistedReplySubmissionOperation };

const DATABASE_NAME = 'exchangeapp-replies';
const DATABASE_VERSION = 1;
const DRAFT_STORE = 'reply_drafts';
const OPERATION_STORE = 'reply_submission_operations';

let databasePromise: Promise<IDBDatabase> | null = null;

export const replyStorageKey = (viewerID: number, parentPostID: number) => (
  `${viewerID}:${parentPostID}`
);

const validID = (value: unknown): value is number => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0
);

const validateDraft = (value: unknown): PersistedReplyDraft => {
  if (!value || typeof value !== 'object') throw new Error('The saved reply draft is invalid.');
  const record = value as Partial<PersistedReplyDraft>;
  if (
    !validID(record.viewerID)
    || !validID(record.parentPostID)
    || record.key !== replyStorageKey(record.viewerID, record.parentPostID)
    || typeof record.content !== 'string'
    || typeof record.createdAt !== 'number' || !Number.isFinite(record.createdAt)
    || typeof record.updatedAt !== 'number' || !Number.isFinite(record.updatedAt)
  ) throw new Error('The saved reply draft is invalid.');
  return record as PersistedReplyDraft;
};

const validateOperation = (value: unknown): PersistedReplySubmissionOperation => {
  if (!value || typeof value !== 'object') throw new Error('The saved reply operation is invalid.');
  const record = value as Partial<PersistedReplySubmissionOperation>;
  if (
    !validID(record.viewerID)
    || !validID(record.parentPostID)
    || record.key !== replyStorageKey(record.viewerID, record.parentPostID)
    || typeof record.id !== 'string' || !record.id.trim()
    || typeof record.content !== 'string' || !record.content || record.content !== record.content.trim()
    || !(record.sourceDraftContent === null || typeof record.sourceDraftContent === 'string')
    || !(record.phase === 'publishing' || record.phase === 'failed' || record.phase === 'succeeded')
    || !isDurableSubmissionFailureKind(record.failureKind)
    || typeof record.viewerSessionID !== 'string' || !record.viewerSessionID.trim()
    || typeof record.error !== 'string'
    || typeof record.startedAt !== 'number' || !Number.isFinite(record.startedAt)
    || typeof record.updatedAt !== 'number' || !Number.isFinite(record.updatedAt)
    || !(record.post === null || (typeof record.post === 'object' && record.post !== null))
    || !hasValidDurableSubmissionFailureShape(record.phase, record.failureKind)
  ) throw new Error('The saved reply operation is invalid.');
  return record as PersistedReplySubmissionOperation;
};

const openDatabase = (): Promise<IDBDatabase> => {
  if (databasePromise) return databasePromise;
  if (typeof indexedDB === 'undefined') return Promise.reject(new Error('IndexedDB is unavailable.'));

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
      database.createObjectStore(DRAFT_STORE, { keyPath: 'key' });
      const store = database.createObjectStore(OPERATION_STORE, { keyPath: 'key' });
      store.createIndex('viewerID', 'viewerID', { unique: false });
    };
    request.onerror = () => reject(request.error || new Error('Could not open reply storage.'));
    request.onblocked = () => reject(new Error('Reply storage is blocked by another tab.'));
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

const requestResult = <T>(request: IDBRequest<T>): Promise<T> => (
  indexedDBRequestResult(request, 'Reply storage request failed.')
);

const withStore = async <T>(
  storeName: string,
  mode: IDBTransactionMode,
  execute: (store: IDBObjectStore) => Promise<T>,
): Promise<T> => withIndexedDBStore(await openDatabase(), storeName, mode, execute, {
  failed: 'Reply storage failed.',
  aborted: 'Reply storage was aborted.',
});

const validOwner = (record: { viewerID: number; parentPostID: number }, viewerID: number, parentPostID: number) => (
  record.viewerID === viewerID && record.parentPostID === parentPostID
);

export const getReplyDraft = async (viewerID: number, parentPostID: number) => {
  if (!validID(viewerID) || !validID(parentPostID)) throw new Error('Invalid reply draft owner.');
  return withStore(DRAFT_STORE, 'readonly', async store => {
    const value = await requestResult(store.get(replyStorageKey(viewerID, parentPostID)));
    if (value === undefined) return null;
    const record = validateDraft(value);
    return validOwner(record, viewerID, parentPostID) ? record : null;
  });
};

export const saveReplyDraft = async (record: PersistedReplyDraft): Promise<void> => {
  const normalized = validateDraft(record);
  await withStore(DRAFT_STORE, 'readwrite', async store => {
    await requestResult(store.put(normalized));
  });
};

export type ConditionalReplyDraftDelete = 'deleted' | 'missing' | 'changed';

export const deleteReplyDraftIfUnchanged = async (
  viewerID: number,
  parentPostID: number,
  expectedContent: string,
  shouldDelete: () => boolean = () => true,
): Promise<ConditionalReplyDraftDelete> => {
  if (!validID(viewerID) || !validID(parentPostID)) return 'missing';
  if (!shouldDelete()) return 'changed';
  return withStore(DRAFT_STORE, 'readwrite', async store => {
    const key = replyStorageKey(viewerID, parentPostID);
    const value = await requestResult(store.get(key));
    if (value === undefined) return 'missing';
    const record = validateDraft(value);
    if (!validOwner(record, viewerID, parentPostID)) return 'missing';
    if (record.content !== expectedContent) return 'changed';
    if (!shouldDelete()) return 'changed';
    await requestResult(store.delete(key));
    return 'deleted';
  });
};

export const getReplySubmissionOperation = async (viewerID: number, parentPostID: number) => {
  if (!validID(viewerID) || !validID(parentPostID)) throw new Error('Invalid reply operation owner.');
  return withStore(OPERATION_STORE, 'readonly', async store => {
    const value = await requestResult(store.get(replyStorageKey(viewerID, parentPostID)));
    if (value === undefined) return null;
    const record = validateOperation(value);
    return validOwner(record, viewerID, parentPostID) ? record : null;
  });
};

export const listReplySubmissionOperations = async (viewerID: number) => {
  if (!validID(viewerID)) throw new Error('Invalid reply operation viewer.');
  return withStore(OPERATION_STORE, 'readonly', async store => {
    const values = await requestResult(store.index('viewerID').getAll(viewerID));
    return values.map(validateOperation).filter(record => record.viewerID === viewerID);
  });
};

export const claimReplySubmissionOperation = async (
  operation: PersistedReplySubmissionOperation,
): Promise<ReplySubmissionClaimResult> => {
  const normalized = validateOperation(operation);
  return withStore(OPERATION_STORE, 'readwrite', async store => {
    const value = await requestResult(store.get(normalized.key));
    if (value !== undefined) {
      const current = validateOperation(value);
      if (!validOwner(current, normalized.viewerID, normalized.parentPostID)) {
        throw new Error('The saved reply operation belongs to another owner.');
      }
      return { status: 'occupied', operation: current };
    }
    await requestResult(store.put(normalized));
    return { status: 'claimed' };
  });
};

export const updateReplySubmissionOperation = async (
  operation: PersistedReplySubmissionOperation,
): Promise<boolean> => {
  const normalized = validateOperation(operation);
  return withStore(OPERATION_STORE, 'readwrite', async store => {
    const value = await requestResult(store.get(normalized.key));
    if (value === undefined) return false;
    const current = validateOperation(value);
    if (!validOwner(current, normalized.viewerID, normalized.parentPostID) || current.id !== normalized.id) return false;
    await requestResult(store.put(normalized));
    return true;
  });
};

export const deleteReplySubmissionOperation = async (
  viewerID: number,
  parentPostID: number,
  operationID: string,
): Promise<boolean> => {
  if (!validID(viewerID) || !validID(parentPostID) || !operationID.trim()) return false;
  return withStore(OPERATION_STORE, 'readwrite', async store => {
    const key = replyStorageKey(viewerID, parentPostID);
    const value = await requestResult(store.get(key));
    if (value === undefined) return false;
    const current = validateOperation(value);
    if (!validOwner(current, viewerID, parentPostID) || current.id !== operationID) return false;
    await requestResult(store.delete(key));
    return true;
  });
};
