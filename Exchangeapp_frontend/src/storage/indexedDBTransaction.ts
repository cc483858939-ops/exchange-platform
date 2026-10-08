export const indexedDBRequestResult = <T>(request: IDBRequest<T>, failureMessage: string): Promise<T> => (
  new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error(failureMessage));
  })
);

// A successful request is provisional until the owning transaction commits.
export const withIndexedDBStore = <T>(
  database: IDBDatabase,
  storeName: string | string[],
  mode: IDBTransactionMode,
  execute: (store: IDBObjectStore, transaction: IDBTransaction) => Promise<T>,
  errors: { failed: string; aborted: string },
): Promise<T> => new Promise((resolve, reject) => {
  let transaction: IDBTransaction;
  try {
    transaction = database.transaction(storeName, mode);
  } catch (error) {
    reject(error);
    return;
  }
  let result: T;
  let settled = false;
  const fail = (error: unknown) => {
    if (settled) return;
    settled = true;
    reject(error);
  };
  transaction.oncomplete = () => {
    if (settled) return;
    settled = true;
    resolve(result);
  };
  transaction.onerror = () => fail(transaction.error || new Error(errors.failed));
  transaction.onabort = () => fail(transaction.error || new Error(errors.aborted));
  try {
    void execute(transaction.objectStore(typeof storeName === 'string' ? storeName : storeName[0]!), transaction).then(value => {
      result = value;
    }).catch(error => {
      try { transaction.abort(); } catch { /* transaction may already be complete */ }
      fail(error);
    });
  } catch (error) {
    try { transaction.abort(); } catch { /* transaction may already be complete */ }
    fail(error);
  }
});
