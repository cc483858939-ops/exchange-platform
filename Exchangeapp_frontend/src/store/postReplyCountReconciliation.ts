import { getPostById } from '../services/postService';
import {
  beginReplyCountRefresh,
  commitReplyCountRefresh,
  isReplyCountReconciliationNeeded,
} from './sessionSync';

const retryDelaysMs = [1_000, 3_000, 10_000] as const;

type ReconciliationTask = {
  inFlight: Promise<boolean> | null;
  retryTimer: ReturnType<typeof setTimeout> | null;
  retryIndex: number;
};

const reconciliationTasks = new Map<number, ReconciliationTask>();

const getTask = (postID: number) => {
  let task = reconciliationTasks.get(postID);
  if (!task) {
    task = { inFlight: null, retryTimer: null, retryIndex: 0 };
    reconciliationTasks.set(postID, task);
  }
  return task;
};

const clearRetryTimer = (task: ReconciliationTask) => {
  if (task.retryTimer === null) return;
  clearTimeout(task.retryTimer);
  task.retryTimer = null;
};

const scheduleRetry = (postID: number, task: ReconciliationTask) => {
  if (!isReplyCountReconciliationNeeded(postID) || task.retryIndex >= retryDelaysMs.length) {
    return;
  }

  const delay = retryDelaysMs[task.retryIndex];
  task.retryIndex += 1;
  task.retryTimer = setTimeout(() => {
    task.retryTimer = null;
    void runAttempt(postID, task);
  }, delay);
};

const runAttempt = (postID: number, task: ReconciliationTask): Promise<boolean> => {
  if (!isReplyCountReconciliationNeeded(postID)) {
    clearRetryTimer(task);
    reconciliationTasks.delete(postID);
    return Promise.resolve(false);
  }
  if (task.inFlight) return task.inFlight;

  const token = beginReplyCountRefresh(postID);
  if (!token) return Promise.resolve(false);

  const request = (async () => {
    try {
      const post = await getPostById(String(postID));
      if (
        post.id !== postID
        || !Number.isSafeInteger(post.reply_count)
        || post.reply_count < 0
      ) {
        return false;
      }
      return commitReplyCountRefresh(token, {
        postId: postID,
        replyCount: post.reply_count,
      });
    } catch {
      return false;
    }
  })();

  let trackedRequest: Promise<boolean>;
  trackedRequest = request.then(succeeded => {
    if (succeeded || !isReplyCountReconciliationNeeded(postID)) {
      clearRetryTimer(task);
      task.retryIndex = 0;
      reconciliationTasks.delete(postID);
      return succeeded;
    }
    scheduleRetry(postID, task);
    return false;
  }).finally(() => {
    if (task.inFlight === trackedRequest) task.inFlight = null;
  });
  task.inFlight = trackedRequest;
  return trackedRequest;
};

/** Reconcile a marked post from a server-authoritative count snapshot. */
export const reconcilePostReplyCount = (rawPostID: unknown): Promise<boolean> => {
  const postID = typeof rawPostID === 'number'
    ? rawPostID
    : typeof rawPostID === 'string' && rawPostID.trim()
      ? Number(rawPostID.trim())
      : Number.NaN;
  if (!Number.isSafeInteger(postID) || postID <= 0 || !isReplyCountReconciliationNeeded(postID)) {
    return Promise.resolve(false);
  }

  const task = getTask(postID);
  if (task.inFlight) return task.inFlight;
  clearRetryTimer(task);
  task.retryIndex = 0;
  return runAttempt(postID, task);
};

const resumePendingReconciliations = () => {
  for (const postID of reconciliationTasks.keys()) {
    if (isReplyCountReconciliationNeeded(postID)) {
      void reconcilePostReplyCount(postID);
    } else {
      const task = reconciliationTasks.get(postID);
      if (task) clearRetryTimer(task);
      reconciliationTasks.delete(postID);
    }
  }
};

const resumeWhenVisible = () => {
  if (document.visibilityState === 'visible') resumePendingReconciliations();
};

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  window.addEventListener('online', resumePendingReconciliations);
  document.addEventListener('visibilitychange', resumeWhenVisible);

  if (import.meta.hot) {
    import.meta.hot.dispose(() => {
      window.removeEventListener('online', resumePendingReconciliations);
      document.removeEventListener('visibilitychange', resumeWhenVisible);
    });
  }
}
