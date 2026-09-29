export type PersistedPostDraftMedia = {
  id: string;
  blob: Blob;
  name: string;
  type: string;
  size: number;
  lastModified: number;
  uploadedURL: string;
};

export type PersistedPostDraft = {
  id: string;
  viewerID: number;
  content: string;
  media: PersistedPostDraftMedia[];
  createdAt: number;
  updatedAt: number;
};

const databaseName = 'exchangeapp-drafts';
const databaseVersion = 1;
const objectStoreName = 'post_drafts';
const viewerIndexName = 'viewerID';

const assertViewerID = (viewerID: number) => {
  if (!Number.isSafeInteger(viewerID) || viewerID <= 0) {
    throw new TypeError('A valid viewer ID is required to access post drafts.');
  }
};

const isPersistedPostDraft = (value: unknown): value is PersistedPostDraft => {
  if (!value || typeof value !== 'object') return false;
  const draft = value as Partial<PersistedPostDraft>;
  return Boolean(
    typeof draft.id === 'string'
    && draft.id.trim()
    && Number.isSafeInteger(draft.viewerID)
    && (draft.viewerID as number) > 0
    && typeof draft.content === 'string'
    && Number.isFinite(draft.createdAt)
    && Number.isFinite(draft.updatedAt)
    && Array.isArray(draft.media)
    && draft.media.every(item => (
      Boolean(item)
      && typeof item.id === 'string'
      && item.id.length > 0
      && item.blob instanceof Blob
      && typeof item.name === 'string'
      && typeof item.type === 'string'
      && Number.isFinite(item.size)
      && item.size >= 0
      && item.blob.size === item.size
      && Number.isFinite(item.lastModified)
      && typeof item.uploadedURL === 'string'
    )),
  );
};

const openDatabase = (): Promise<IDBDatabase> => new Promise((resolve, reject) => {
  if (typeof indexedDB === 'undefined') {
    reject(new Error('IndexedDB is unavailable in this browser.'));
    return;
  }

  let request: IDBOpenDBRequest;
  try {
    request = indexedDB.open(databaseName, databaseVersion);
  } catch (error) {
    reject(error);
    return;
  }

  request.onupgradeneeded = () => {
    const database = request.result;
    if (!database.objectStoreNames.contains(objectStoreName)) {
      const store = database.createObjectStore(objectStoreName, { keyPath: 'id' });
      store.createIndex(viewerIndexName, viewerIndexName, { unique: false });
      store.createIndex('updatedAt', 'updatedAt', { unique: false });
      return;
    }

    const store = request.transaction?.objectStore(objectStoreName);
    if (store && !store.indexNames.contains(viewerIndexName)) {
      store.createIndex(viewerIndexName, viewerIndexName, { unique: false });
    }
    if (store && !store.indexNames.contains('updatedAt')) {
      store.createIndex('updatedAt', 'updatedAt', { unique: false });
    }
  };
  request.onsuccess = () => resolve(request.result);
  request.onerror = () => reject(request.error || new Error('Could not open the post draft database.'));
  request.onblocked = () => reject(new Error('The post draft database is blocked by another tab.'));
});

const transactionCompletion = (transaction: IDBTransaction): Promise<void> => new Promise((resolve, reject) => {
  transaction.oncomplete = () => resolve();
  transaction.onerror = () => reject(transaction.error || new Error('The post draft transaction failed.'));
  transaction.onabort = () => reject(transaction.error || new Error('The post draft transaction was aborted.'));
});

const withDatabase = async <T>(action: (database: IDBDatabase) => Promise<T>): Promise<T> => {
  const database = await openDatabase();
  try {
    return await action(database);
  } finally {
    database.close();
  }
};

export const listPostDrafts = (viewerID: number): Promise<PersistedPostDraft[]> => {
  assertViewerID(viewerID);
  return withDatabase(async database => {
    const transaction = database.transaction(objectStoreName, 'readonly');
    const completed = transactionCompletion(transaction);
    const request = transaction.objectStore(objectStoreName)
      .index(viewerIndexName)
      .getAll(IDBKeyRange.only(viewerID));
    const records = await new Promise<PersistedPostDraft[]>((resolve, reject) => {
      request.onsuccess = () => resolve(request.result as PersistedPostDraft[]);
      request.onerror = () => reject(request.error || new Error('Could not list post drafts.'));
    });
    await completed;
    return records
      .filter(record => isPersistedPostDraft(record) && record.viewerID === viewerID)
      .sort((left, right) => right.updatedAt - left.updatedAt || left.id.localeCompare(right.id));
  });
};

export const getPostDraft = (
  viewerID: number,
  draftID: string,
): Promise<PersistedPostDraft | null> => {
  assertViewerID(viewerID);
  return withDatabase(async database => {
    const transaction = database.transaction(objectStoreName, 'readonly');
    const completed = transactionCompletion(transaction);
    const request = transaction.objectStore(objectStoreName).get(draftID);
    const record = await new Promise<PersistedPostDraft | undefined>((resolve, reject) => {
      request.onsuccess = () => resolve(request.result as PersistedPostDraft | undefined);
      request.onerror = () => reject(request.error || new Error('Could not read the post draft.'));
    });
    await completed;
    return isPersistedPostDraft(record) && record.viewerID === viewerID ? record : null;
  });
};

export const savePostDraft = (draft: PersistedPostDraft): Promise<void> => {
  assertViewerID(draft.viewerID);
  if (!draft.id.trim()) {
    throw new TypeError('A post draft ID is required.');
  }
  if (!isPersistedPostDraft(draft)) {
    throw new TypeError('The post draft record is invalid.');
  }

  return withDatabase(async database => {
    const transaction = database.transaction(objectStoreName, 'readwrite');
    const completed = transactionCompletion(transaction);
    const store = transaction.objectStore(objectStoreName);
    const belongsToViewer = await new Promise<boolean>((resolve) => {
      const request = store.get(draft.id);
      request.onerror = () => resolve(true);
      request.onsuccess = () => {
        const existing = request.result as PersistedPostDraft | undefined;
        if (existing && existing.viewerID !== draft.viewerID) {
          resolve(false);
          return;
        }
        store.put(draft);
        resolve(true);
      };
    });
    await completed;
    if (!belongsToViewer) {
      throw new Error('This post draft belongs to another viewer.');
    }
  });
};

export const deletePostDraft = (viewerID: number, draftID: string): Promise<boolean> => {
  assertViewerID(viewerID);
  return withDatabase(async database => {
    const transaction = database.transaction(objectStoreName, 'readwrite');
    const completed = transactionCompletion(transaction);
    const store = transaction.objectStore(objectStoreName);
    const deleted = await new Promise<boolean>((resolve, reject) => {
      const getRequest = store.get(draftID);
      getRequest.onerror = () => reject(getRequest.error || new Error('Could not check the post draft owner.'));
      getRequest.onsuccess = () => {
        const record = getRequest.result as PersistedPostDraft | undefined;
        if (!isPersistedPostDraft(record) || record.viewerID !== viewerID) {
          resolve(false);
          return;
        }
        const deleteRequest = store.delete(draftID);
        deleteRequest.onerror = () => reject(deleteRequest.error || new Error('Could not delete the post draft.'));
        deleteRequest.onsuccess = () => resolve(true);
      };
    });
    await completed;
    return deleted;
  });
};
