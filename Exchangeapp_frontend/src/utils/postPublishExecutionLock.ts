type PostPublishLockManager = {
  request<T>(
    name: string,
    options: { mode: 'exclusive' },
    callback: () => T | Promise<T>,
  ): Promise<T>;
};

export class PublishCoordinationUnavailableError extends Error {
  constructor() {
    super('Cross-tab publish coordination is unavailable.');
    this.name = 'PublishCoordinationUnavailableError';
  }
}

export const withPostPublishExecutionLock = async <T>(
  viewerID: number,
  action: () => T | Promise<T>,
): Promise<T> => {
  const lockManager = typeof navigator === 'undefined'
    ? undefined
    : (navigator as Navigator & { locks?: PostPublishLockManager }).locks;
  if (
    !Number.isSafeInteger(viewerID)
    || viewerID <= 0
    || typeof lockManager?.request !== 'function'
  ) {
    throw new PublishCoordinationUnavailableError();
  }

  let callbackStarted = false;
  try {
    return await lockManager.request(
      `exchange-post-publish:${viewerID}`,
      { mode: 'exclusive' },
      async () => {
        callbackStarted = true;
        return await action();
      },
    );
  } catch (error) {
    if (!callbackStarted) {
      throw new PublishCoordinationUnavailableError();
    }
    throw error;
  }
};
