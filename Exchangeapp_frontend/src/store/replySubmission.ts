import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import { createPostReply } from '../services/replyService';
import type { Post } from '../types/Post';
import { createClientOperationID } from '../utils/clientOperationId';
import {
  claimReplySubmissionOperation,
  deleteReplyDraftIfUnchanged,
  deleteReplySubmissionOperation,
  getReplyDraft,
  getReplySubmissionOperation,
  listReplySubmissionOperations,
  replyStorageKey,
  updateReplySubmissionOperation,
  type PersistedReplySubmissionOperation,
  type ReplySubmissionFailureKind,
  type ReplySubmissionPhase,
} from '../storage/replyStorage';
import { useAuthStore } from './auth';
import {
  matchesDurableAuthOwner,
  type AuthRequestBinding,
} from '../auth/authRequestBinding';
import { useReplyDraftStore } from './replyDraft';
import {
  canAbandonDurableSubmission,
  canMarkDurableSubmissionFailed,
  canRetryDurableSubmission,
  isPostIdempotencyConflictError,
  recoveredSubmissionMustFailClosed,
} from '../utils/durableSubmissionContract';

export type ReplySubmissionOperation = {
  id: string;
  viewerID: number;
  viewerSessionID: string | null;
  parentPostID: number;
  content: string;
  sourceDraftContent: string | null;
  phase: ReplySubmissionPhase;
  failureKind: ReplySubmissionFailureKind;
  error: string;
  startedAt: number;
  post: Post | null;
  durableOwned: boolean;
  cleanupPending: boolean;
};

export type ReplySubmissionBlockReason =
  | 'another_reply_in_flight'
  | 'unresolved_reply'
  | 'idempotency_conflict'
  | 'cleanup_pending';

export type StartReplySubmissionResult =
  | { status: 'accepted'; operation: ReplySubmissionOperation }
  | { status: 'resolved_previous'; operationID: string }
  | { status: 'blocked'; reason: ReplySubmissionBlockReason }
  | { status: 'rejected'; reason: 'unauthenticated' | 'persistence_unavailable' | 'editor_changed' };

const normalizeViewerID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const restoreOperation = (record: PersistedReplySubmissionOperation): ReplySubmissionOperation => ({
  id: record.id,
  viewerID: record.viewerID,
  viewerSessionID: record.viewerSessionID ?? null,
  parentPostID: record.parentPostID,
  content: record.content,
  sourceDraftContent: record.sourceDraftContent,
  phase: record.phase,
  failureKind: record.failureKind,
  error: record.error,
  startedAt: record.startedAt,
  post: record.post,
  durableOwned: true,
  cleanupPending: record.phase === 'succeeded',
});

const persistOperation = (operation: ReplySubmissionOperation, updatedAt = Date.now()): PersistedReplySubmissionOperation => ({
  key: replyStorageKey(operation.viewerID, operation.parentPostID),
  id: operation.id,
  viewerID: operation.viewerID,
  viewerSessionID: operation.viewerSessionID,
  parentPostID: operation.parentPostID,
  content: operation.content,
  sourceDraftContent: operation.sourceDraftContent,
  phase: operation.phase,
  failureKind: operation.failureKind,
  error: operation.error,
  startedAt: operation.startedAt,
  updatedAt,
  post: operation.post,
});

export const useReplySubmissionStore = defineStore('replySubmission', () => {
  const authStore = useAuthStore();
  const replyDraftStore = useReplyDraftStore();
  const operations = ref<ReplySubmissionOperation[]>([]);
  const activeViewerID = ref<number | null>(null);
  const recoveryErrors = ref<Record<string, string>>({});
  const currentViewerID = computed(() => (
    authStore.isAuthenticated ? normalizeViewerID(authStore.currentIdentity?.id) : null
  ));
  const captureOperationAuthBinding = (operation: ReplySubmissionOperation): AuthRequestBinding => {
    const binding = authStore.captureRequestAuthBinding();
    if (!binding || !matchesDurableAuthOwner(binding, operation.viewerID, operation.viewerSessionID)) {
      throw Object.assign(new Error('Authentication session changed'), { name: 'AuthSessionChangedError' });
    }
    return binding;
  };
  const operationMatchesCurrentSession = (operation: ReplySubmissionOperation) => {
    const binding = authStore.captureRequestAuthBinding();
    return matchesDurableAuthOwner(binding, operation.viewerID, operation.viewerSessionID);
  };
  const mayMutateCurrentReplySession = (operation: ReplySubmissionOperation) => (
    activeViewerID.value === operation.viewerID
    && operationMatchesCurrentSession(operation)
  );
  const recoveryError = computed(() => (
    activeViewerID.value === null ? '' : recoveryErrors.value[String(activeViewerID.value)] || ''
  ));

  const hydrationPromises = new Map<number, Promise<void>>();
  const hydratedViewers = new Set<number>();
  const runningOperationIDs = new Set<string>();
  const abandoningOperationIDs = new Set<string>();
  const reconciledOperationIDs = new Set<string>();
  const completedSucceededOperationIDs = new Set<string>();
  const finalizationTasks = new Map<string, Promise<boolean>>();

  const getOperation = (viewerID: number, parentPostID: number): ReplySubmissionOperation | null => {
    const owned = operations.value.find(operation => (
      operation.viewerID === viewerID
      && operation.parentPostID === parentPostID
      && operation.durableOwned
    ));
    if (owned) return owned;
    return operations.value.find(operation => (
      operation.viewerID === viewerID
      && operation.parentPostID === parentPostID
      && operation.phase === 'succeeded'
      && !reconciledOperationIDs.has(operation.id)
    )) ?? null;
  };

  const getOwnedOperation = (viewerID: number, parentPostID: number) => operations.value.find(operation => (
    operation.viewerID === viewerID
    && operation.parentPostID === parentPostID
    && operation.durableOwned
  ));

  const upsertOperation = (operation: ReplySubmissionOperation) => {
    if (activeViewerID.value !== operation.viewerID) return;
    const existingIndex = operations.value.findIndex(item => item.id === operation.id);
    if (existingIndex < 0) {
      operations.value = operations.value.concat(operation);
    } else {
      const next = operations.value.slice();
      next[existingIndex] = operation;
      operations.value = next;
    }
  };

  const forgetViewerMemory = () => {
    operations.value = [];
    reconciledOperationIDs.clear();
    abandoningOperationIDs.clear();
    completedSucceededOperationIDs.clear();
  };

  const markDraftSourceResolved = async (operation: ReplySubmissionOperation) => {
    const expected = operation.sourceDraftContent;
    if (expected === null || !mayMutateCurrentReplySession(operation)) return true;
    try {
      const result = await deleteReplyDraftIfUnchanged(
        operation.viewerID,
        operation.parentPostID,
        expected,
        () => mayMutateCurrentReplySession(operation),
      );
      if (result === 'deleted') {
        if (mayMutateCurrentReplySession(operation)) {
          replyDraftStore.markSourceDraftDeleted(operation.parentPostID, expected);
        }
        return true;
      }
      if (result === 'missing' || result === 'changed') {
        if (mayMutateCurrentReplySession(operation)) {
          const latest = await getReplyDraft(operation.viewerID, operation.parentPostID);
          if (mayMutateCurrentReplySession(operation)) {
            replyDraftStore.refreshSavedBaseline(
              operation.parentPostID,
              latest?.content ?? null,
              expected,
              latest?.createdAt ?? null,
            );
          }
        }
        return true;
      }
      return false;
    } catch {
      return false;
    }
  };

  const performSuccessfulOperationFinalization = async (operation: ReplySubmissionOperation): Promise<boolean> => {
    if (!operation.durableOwned || operation.phase !== 'succeeded') return !operation.durableOwned;
    if (mayMutateCurrentReplySession(operation)
      && await replyDraftStore.awaitPendingSave(operation.parentPostID) === 'failed') return false;
    operation.cleanupPending = true;
    upsertOperation(operation);

    const sourceResolved = await markDraftSourceResolved(operation);
    if (!sourceResolved) {
      // The confirmed server success remains succeeded; only local cleanup is pending.
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* retry cleanup later */ }
      return false;
    }

    let deleted = false;
    try {
      deleted = await deleteReplySubmissionOperation(
        operation.viewerID,
        operation.parentPostID,
        operation.id,
      );
    } catch {
      deleted = false;
    }
    if (!deleted) {
      try {
        const current = await getReplySubmissionOperation(operation.viewerID, operation.parentPostID);
        if (current?.id === operation.id) return false;
      } catch {
        return false;
      }
    }

    operation.durableOwned = false;
    operation.cleanupPending = false;
    if (mayMutateCurrentReplySession(operation)) {
      replyDraftStore.finishBoundSubmission(operation.parentPostID, operation.id, operation.content);
    }
    upsertOperation(operation);
    return true;
  };

  const finalizeSuccessfulOperation = (operation: ReplySubmissionOperation): Promise<boolean> => {
    const pending = finalizationTasks.get(operation.id);
    if (pending) return pending;
    const task = performSuccessfulOperationFinalization(operation);
    finalizationTasks.set(operation.id, task);
    const clearTask = () => {
      if (finalizationTasks.get(operation.id) === task) finalizationTasks.delete(operation.id);
    };
    void task.then(clearTask, clearTask);
    return task;
  };

  const runOperation = async (operation: ReplySubmissionOperation) => {
    if (runningOperationIDs.has(operation.id) || operation.phase !== 'publishing') return;
    if (currentViewerID.value !== operation.viewerID) return;
    runningOperationIDs.add(operation.id);
    upsertOperation(operation);
    try {
      const authBinding = captureOperationAuthBinding(operation);
      const created = await createPostReply(operation.parentPostID, operation.content, {
        idempotencyKey: operation.id,
        authBinding,
      });
      if (created.author.id !== operation.viewerID) {
        throw Object.assign(new Error('Authentication session changed'), { name: 'AuthSessionChangedError' });
      }
      operation.phase = 'succeeded';
      completedSucceededOperationIDs.add(operation.id);
      operation.failureKind = null;
      operation.error = '';
      operation.post = created;
      operation.cleanupPending = true;
      upsertOperation(operation);
      try {
        const persisted = await updateReplySubmissionOperation(persistOperation(operation));
        // Whether this update found its record or not, the server success remains authoritative.
        void persisted;
      } catch {
        // A durable publishing record safely replays the same idempotency key after a crash.
      }
      await finalizeSuccessfulOperation(operation);
    } catch (error) {
      if (!canMarkDurableSubmissionFailed(operation.phase)) {
        operation.cleanupPending = true;
      } else {
        operation.phase = 'failed';
        const authContextChanged = (error instanceof Error && error.name === 'AuthSessionChangedError')
          || !operationMatchesCurrentSession(operation);
        operation.failureKind = authContextChanged
          ? 'auth_context_changed'
          : isPostIdempotencyConflictError(error) ? 'idempotency_conflict' : 'retryable';
        operation.error = operation.failureKind === 'idempotency_conflict'
          ? 'This reply can’t be retried safely.'
          : 'Reply failed. Retry safely.';
        operation.cleanupPending = false;
      }
      upsertOperation(operation);
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* keep in-memory unresolved owner */ }
    } finally {
      runningOperationIDs.delete(operation.id);
      upsertOperation(operation);
    }
  };

  const activateViewer = async (viewerID: number | null): Promise<void> => {
    const normalized = normalizeViewerID(viewerID);
    if (normalized !== activeViewerID.value) {
      const previousViewerID = activeViewerID.value;
      if (previousViewerID !== null) hydratedViewers.delete(previousViewerID);
      activeViewerID.value = normalized;
      replyDraftStore.setViewer(normalized);
      forgetViewerMemory();
    }
    if (normalized === null || hydratedViewers.has(normalized)) return;
    const existing = hydrationPromises.get(normalized);
    if (existing) return existing;

    const task = (async () => {
      try {
        const records = await listReplySubmissionOperations(normalized);
        if (activeViewerID.value !== normalized) return;
        const restored = records.map(restoreOperation);
        operations.value = operations.value.filter(operation => operation.viewerID !== normalized).concat(restored);
        for (const operation of restored) {
          if (recoveredSubmissionMustFailClosed(operation.phase, operation.viewerSessionID)) {
            operation.phase = 'failed';
            operation.failureKind = 'auth_context_changed';
            operation.error = 'Reply failed. Retry safely.';
            operation.cleanupPending = false;
            try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* legacy record remains fail-closed */ }
          }
          if (operation.phase === 'succeeded') completedSucceededOperationIDs.add(operation.id);
        }
        hydratedViewers.add(normalized);
        delete recoveryErrors.value[String(normalized)];
        for (const operation of restored) {
          if (operation.phase === 'publishing') void runOperation(operation);
          else if (operation.phase === 'succeeded') void finalizeSuccessfulOperation(operation);
        }
      } catch {
        if (activeViewerID.value === normalized) {
          recoveryErrors.value[String(normalized)] = 'Reply recovery could not be checked on this device. Try again.';
        }
        throw new Error('Reply operation hydration failed.');
      }
    })();
    hydrationPromises.set(normalized, task);
    try {
      await task;
    } finally {
      if (hydrationPromises.get(normalized) === task) hydrationPromises.delete(normalized);
    }
  };

  const ensureContextHydrated = async (viewerID: number, parentPostID: number) => {
    await activateViewer(viewerID);
    if (activeViewerID.value !== viewerID || !hydratedViewers.has(viewerID)) {
      throw new Error('Reply operation storage is not available.');
    }
    const existing = getOwnedOperation(viewerID, parentPostID);
    if (existing) return { ...existing };
    // Viewer listing is authoritative, but read the exact slot as a defense against a
    // concurrent tab creating an operation after the list was hydrated.
    const record = await getReplySubmissionOperation(viewerID, parentPostID);
    if (!record || activeViewerID.value !== viewerID) return null;
    const operation = restoreOperation(record);
    if (operation.phase === 'succeeded') completedSucceededOperationIDs.add(operation.id);
    upsertOperation(operation);
    if (operation.phase === 'publishing') void runOperation(operation);
    else if (operation.phase === 'succeeded') void finalizeSuccessfulOperation(operation);
    return { ...operation };
  };

  const getBlockReason = (viewerID: number, parentPostID: number): ReplySubmissionBlockReason | null => {
    const operation = getOwnedOperation(viewerID, parentPostID);
    if (!operation) return null;
    if (operation.phase === 'succeeded') return 'cleanup_pending';
    if (operation.phase === 'publishing') return 'another_reply_in_flight';
    if (operation.failureKind === 'idempotency_conflict') return 'idempotency_conflict';
    return 'unresolved_reply';
  };

  const transitionToPublishing = async (operation: ReplySubmissionOperation): Promise<boolean> => {
    if (!canRetryDurableSubmission(operation.phase, operation.failureKind)) return false;
    if (!operationMatchesCurrentSession(operation)) {
      operation.phase = 'failed';
      operation.failureKind = 'auth_context_changed';
      operation.error = 'Reply failed. Retry safely.';
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* retain fail-closed state */ }
      upsertOperation(operation);
      return false;
    }
    operation.phase = 'publishing';
    operation.failureKind = null;
    operation.error = '';
    operation.cleanupPending = false;
    try {
      const updated = await updateReplySubmissionOperation(persistOperation(operation));
      if (!updated) {
        operation.phase = 'failed';
        operation.failureKind = 'retryable';
        operation.error = 'Reply failed. Retry safely.';
        return false;
      }
    } catch {
      operation.phase = 'failed';
      operation.failureKind = 'retryable';
      operation.error = 'Reply failed. Retry safely.';
      return false;
    }
    upsertOperation(operation);
    void runOperation(operation);
    return true;
  };

  const startOrRetry = async (parentPostID: number, rawContent: string): Promise<StartReplySubmissionResult> => {
    const authBinding = authStore.captureRequestAuthBinding();
    if (!authBinding) return { status: 'rejected', reason: 'unauthenticated' };
    const viewerID = authBinding.userID;
    if (!Number.isSafeInteger(parentPostID) || parentPostID <= 0) {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }

    let operationAtContext: ReplySubmissionOperation | null;
    try {
      operationAtContext = await ensureContextHydrated(viewerID, parentPostID);
    } catch {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
    if (recoveryErrors.value[String(viewerID)]) {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
    if (await replyDraftStore.awaitPendingSave(parentPostID) === 'failed') {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
    if (!authStore.matchesRequestAuthBinding(authBinding)) return { status: 'rejected', reason: 'unauthenticated' };

    let existing = getOwnedOperation(viewerID, parentPostID);
    if (existing && existing.phase !== 'succeeded' && !operationMatchesCurrentSession(existing)) {
      existing.phase = 'failed';
      existing.failureKind = 'auth_context_changed';
      existing.error = 'Reply failed. Retry safely.';
      try { await updateReplySubmissionOperation(persistOperation(existing)); } catch { /* retain fail-closed state */ }
      upsertOperation(existing);
      return { status: 'blocked', reason: 'unresolved_reply' };
    }
    const operationFinishedDuringThisAction = operationAtContext?.durableOwned === true
      && completedSucceededOperationIDs.has(operationAtContext.id);
    const succeededOperation = existing?.phase === 'succeeded'
      ? existing
      : operationAtContext?.durableOwned && operationAtContext.phase === 'succeeded'
        ? getOperation(viewerID, parentPostID) ?? operationAtContext
        : operationFinishedDuringThisAction
          ? getOperation(viewerID, parentPostID) ?? operationAtContext
          : null;
    if (succeededOperation || operationFinishedDuringThisAction) {
      const operationID = succeededOperation?.id ?? operationAtContext!.id;
      const cleanupTarget = succeededOperation?.phase === 'succeeded' && succeededOperation.durableOwned
        ? succeededOperation
        : null;
      const resolved = cleanupTarget
        ? await finalizeSuccessfulOperation(cleanupTarget)
        : true;
      if (!resolved || getOwnedOperation(viewerID, parentPostID)) {
        return { status: 'blocked', reason: 'cleanup_pending' };
      }
      return { status: 'resolved_previous', operationID };
    }

    const currentRaw = replyDraftStore.getDraft(parentPostID);
    if (currentRaw.trim() !== rawContent.trim()) {
      return { status: 'rejected', reason: 'editor_changed' };
    }
    const canonicalContent = currentRaw.trim();
    if (!canonicalContent) return { status: 'rejected', reason: 'persistence_unavailable' };

    if (existing?.phase === 'publishing') {
      return canonicalContent === existing.content
        ? { status: 'accepted', operation: existing }
        : { status: 'blocked', reason: 'another_reply_in_flight' };
    }
    if (existing?.phase === 'failed') {
      if (existing.failureKind === 'idempotency_conflict') {
        return { status: 'blocked', reason: 'idempotency_conflict' };
      }
      if (canonicalContent !== existing.content) return { status: 'blocked', reason: 'unresolved_reply' };
      const retried = await transitionToPublishing(existing);
      return retried
        ? { status: 'accepted', operation: existing }
        : { status: 'rejected', reason: 'persistence_unavailable' };
    }

    const savedContent = replyDraftStore.getSavedContent(parentPostID);
    const sourceDraftContent = savedContent !== undefined
      && savedContent !== null
      && currentRaw === savedContent
        ? savedContent
        : null;
    const now = Date.now();
    const operation: ReplySubmissionOperation = {
      id: createClientOperationID(),
      viewerID,
      viewerSessionID: authBinding.sessionID,
      parentPostID,
      content: canonicalContent,
      sourceDraftContent,
      phase: 'publishing',
      failureKind: null,
      error: '',
      startedAt: now,
      post: null,
      durableOwned: true,
      cleanupPending: false,
    };
    let claim: Awaited<ReturnType<typeof claimReplySubmissionOperation>>;
    try {
      claim = await claimReplySubmissionOperation(persistOperation(operation, now));
    } catch {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }

    if (claim.status === 'occupied') {
      if (activeViewerID.value !== viewerID || currentViewerID.value !== viewerID) {
        return { status: 'rejected', reason: 'unauthenticated' };
      }
      const existing = restoreOperation(claim.operation);
      if (existing.phase === 'succeeded') completedSucceededOperationIDs.add(existing.id);
      if (existing.phase !== 'succeeded' && !operationMatchesCurrentSession(existing)) {
        existing.phase = 'failed';
        existing.failureKind = 'auth_context_changed';
        existing.error = 'Reply failed. Retry safely.';
        try { await updateReplySubmissionOperation(persistOperation(existing)); } catch { /* retain fail-closed state */ }
      }
      upsertOperation(existing);
      if (!authStore.matchesRequestAuthBinding(authBinding)) {
        return { status: 'rejected', reason: 'unauthenticated' };
      }
      if (existing.phase === 'succeeded') {
        return { status: 'blocked', reason: 'cleanup_pending' };
      }
      if (existing.phase === 'publishing') {
        return { status: 'blocked', reason: 'another_reply_in_flight' };
      }
      if (existing.failureKind === 'idempotency_conflict') {
        return { status: 'blocked', reason: 'idempotency_conflict' };
      }
      return { status: 'blocked', reason: 'unresolved_reply' };
    }

    if (!authStore.matchesRequestAuthBinding(authBinding)) {
      operation.phase = 'failed';
      operation.failureKind = 'auth_context_changed';
      operation.error = 'Reply failed. Retry safely.';
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* preserve draft if storage is unavailable */ }
      upsertOperation(operation);
      return { status: 'rejected', reason: 'unauthenticated' };
    }
    replyDraftStore.bindSubmission(parentPostID, operation.id, operation.content);
    upsertOperation(operation);
    void runOperation(operation);
    return { status: 'accepted', operation };
  };

  const retry = async (operationID: string): Promise<boolean> => {
    const operation = operations.value.find(item => item.id === operationID);
    if (
      !operation
      || operation.viewerID !== currentViewerID.value
      || runningOperationIDs.has(operation.id)
      || !canRetryDurableSubmission(operation.phase, operation.failureKind)
      || !operation.durableOwned
    ) return false;
    if (!operationMatchesCurrentSession(operation)) {
      operation.phase = 'failed';
      operation.failureKind = 'auth_context_changed';
      operation.error = 'Reply failed. Retry safely.';
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* keep fail-closed in memory */ }
      upsertOperation(operation);
      return false;
    }
    if (await replyDraftStore.awaitPendingSave(operation.parentPostID) === 'failed'
      || currentViewerID.value !== operation.viewerID
      || runningOperationIDs.has(operation.id)
      || !canRetryDurableSubmission(operation.phase, operation.failureKind)
      || !operation.durableOwned) return false;
    if (!operationMatchesCurrentSession(operation)) {
      operation.phase = 'failed';
      operation.failureKind = 'auth_context_changed';
      operation.error = 'Reply failed. Retry safely.';
      try { await updateReplySubmissionOperation(persistOperation(operation)); } catch { /* keep fail-closed in memory */ }
      upsertOperation(operation);
      return false;
    }
    return transitionToPublishing(operation);
  };

  const abandonFailedOperation = async (operationID: string): Promise<boolean> => {
    const operation = operations.value.find(item => item.id === operationID);
    if (
      !operation
      || operation.viewerID !== currentViewerID.value
      || !canAbandonDurableSubmission(operation.phase, operation.failureKind)
      || !operation.durableOwned
      || runningOperationIDs.has(operation.id)
      || abandoningOperationIDs.has(operation.id)
    ) return false;
    if (await replyDraftStore.awaitPendingSave(operation.parentPostID) === 'failed'
      || currentViewerID.value !== operation.viewerID
      || !canAbandonDurableSubmission(operation.phase, operation.failureKind)
      || !operation.durableOwned
      || runningOperationIDs.has(operation.id)
      || abandoningOperationIDs.has(operation.id)) return false;
    abandoningOperationIDs.add(operation.id);
    let deleted = false;
    try {
      deleted = await deleteReplySubmissionOperation(
        operation.viewerID,
        operation.parentPostID,
        operation.id,
      );
    } catch {
      deleted = false;
    } finally {
      abandoningOperationIDs.delete(operation.id);
    }
    if (!deleted) return false;
    operation.durableOwned = false;
    if (mayMutateCurrentReplySession(operation)) {
      replyDraftStore.clearSubmissionBinding(operation.parentPostID, operation.id);
    }
    upsertOperation(operation);
    return true;
  };

  const adoptHydratedOperation = (viewerID: number, parentPostID: number, expectedWorkingRevision: number) => {
    const operation = getOperation(viewerID, parentPostID);
    return operation && operation.phase !== 'succeeded'
      ? replyDraftStore.adoptHydratedSubmission(parentPostID, operation, expectedWorkingRevision)
      : false;
  };

  const acknowledgeSucceededOperation = (operationID: string) => {
    const operation = operations.value.find(item => item.id === operationID);
    if (!operation || operation.phase !== 'succeeded') return false;
    reconciledOperationIDs.add(operationID);
    operations.value = operations.value.filter(item => item.id !== operationID);
    return true;
  };

  const retryCleanup = async (viewerID: number, parentPostID: number) => {
    const operation = getOwnedOperation(viewerID, parentPostID);
    if (!operation || operation.phase !== 'succeeded') return true;
    return finalizeSuccessfulOperation(operation);
  };

  return {
    operations,
    activeViewerID,
    recoveryError,
    currentViewerID,
    getOperation,
    getBlockReason,
    ensureContextHydrated,
    activateViewer,
    startOrRetry,
    retry,
    retryCleanup,
    abandonFailedOperation,
    adoptHydratedOperation,
    acknowledgeSucceededOperation,
  };
});
