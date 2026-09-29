import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  createPost,
  uploadPostMedia,
  type CreatePostPayload,
} from '../services/postService';
import { getPostBookmarkStates } from '../services/bookmarkService';
import type { Post } from '../types/Post';
import {
  deletePostPublishOperation,
  getPostPublishOperation,
  replacePostPublishOperation,
  restorePublishOperation,
  serializePublishOperation,
  updatePostPublishOperation,
  type PersistedPublishFailureKind,
  type PersistedPublishPhase,
} from '../storage/postPublishRepository';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import { usePostDraftStore } from './postDraft';
import { useProfileSessionStore } from './profileSession';
import { createClientOperationID } from '../utils/clientOperationId';
import {
  createPostDraftSnapshot,
  type DraftSnapshot,
} from '../utils/postDraftSnapshot';
import {
  captureBookmarkStateSyncVersion,
  syncHydratedPostBookmarkState,
} from './sessionSync';

export type PublishPhase = PersistedPublishPhase;
export type PublishFailureKind = PersistedPublishFailureKind;

export type PublishOperationMedia = {
  draftMediaID: string;
  file: File;
  uploadedURL: string;
};

export type PublishOperation = {
  id: string;
  publisherUserID: number;
  sourceDraftID: string | null;
  sourceDraftSnapshot: DraftSnapshot | null;
  content: string;
  media: PublishOperationMedia[];
  phase: PublishPhase;
  failureKind: PublishFailureKind;
  error: string;
  startedAt: number;
  post: Post | null;
};

export type PublishBlockReason =
  | 'another_publish_in_flight'
  | 'idempotency_conflict'
  | 'unresolved_publish'
  | 'cleanup_pending';

export type StartPublishResult =
  | {
    status: 'accepted';
    operation: PublishOperation;
  }
  | {
    status: 'blocked';
    reason: PublishBlockReason;
  }
  | {
    status: 'rejected';
    reason: 'unauthenticated' | 'persistence_unavailable';
  };

const mediaUploadConcurrency = 2;
const publishFailureMessage = 'Couldn’t confirm this post. Retry safely.';
const publishPersistenceMessage = 'Couldn’t save publish progress on this device. Retry.';
const publishConflictMessage = 'This post can’t be retried safely.';

class SupersededPublishOperationError extends Error {}
class PublishPersistenceError extends Error {}

type SuccessCleanupResult = 'resolved' | 'pending';

const normalizeViewerID = (value: unknown) => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
    ? value
    : null
);

const isIdempotencyConflict = (error: unknown) => {
  if (!error || typeof error !== 'object') return false;
  const response = (error as { response?: { status?: unknown; data?: { code?: unknown } } }).response;
  return response?.status === 409 && response.data?.code === 'POST_IDEMPOTENCY_CONFLICT';
};

export const usePostPublishStore = defineStore('postPublish', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const postDraft = usePostDraftStore();
  const profileSessionStore = useProfileSessionStore();
  const operations = ref<PublishOperation[]>([]);
  const recoveryErrors = ref(new Map<number, string>());
  const unresolvedOperationByViewer = ref(new Map<number, string>());
  const runningOperationIDs = new Set<string>();
  const abandoningOperationIDs = new Set<string>();
  const hydrationPromises = new Map<number, Promise<void>>();
  const hydratedViewerIDs = new Set<number>();

  const currentViewerID = () => (
    authStore.isAuthenticated
      ? normalizeViewerID(authStore.currentIdentity?.id)
      : null
  );

  const getOperation = (operationID: string | null | undefined) => (
    operationID ? operations.value.find(operation => operation.id === operationID) : undefined
  );

  const getViewerOperation = (viewerID: number) => (
    operations.value.find(operation => operation.publisherUserID === viewerID)
  );

  const markOperationUnresolved = (operation: PublishOperation) => {
    unresolvedOperationByViewer.value.set(operation.publisherUserID, operation.id);
  };

  const releaseOperationOwnership = (viewerID: number, operationID: string) => {
    if (unresolvedOperationByViewer.value.get(viewerID) !== operationID) {
      return false;
    }
    unresolvedOperationByViewer.value.delete(viewerID);
    return true;
  };

  const removeOperationIfCurrent = (viewerID: number, operationID: string) => {
    const current = getViewerOperation(viewerID);
    if (current?.id !== operationID) {
      return false;
    }
    operations.value = operations.value.filter(operation => (
      operation.publisherUserID !== viewerID || operation.id !== operationID
    ));
    return true;
  };

  const latestOperation = computed<PublishOperation | null>(() => {
    const viewerID = currentViewerID();
    if (viewerID === null) return null;
    return operations.value.reduce<PublishOperation | null>((latest, operation) => (
      operation.publisherUserID === viewerID
      && (!latest || operation.startedAt >= latest.startedAt)
        ? operation
        : latest
    ), null);
  });

  const recoveryError = computed(() => {
    const viewerID = currentViewerID();
    return viewerID === null ? '' : recoveryErrors.value.get(viewerID) || '';
  });

  const upsertOperation = (operation: PublishOperation) => {
    operations.value = [
      ...operations.value.filter(item => item.publisherUserID !== operation.publisherUserID),
      operation,
    ];
  };

  const isInFlight = (operation: PublishOperation) => (
    operation.phase === 'uploading' || operation.phase === 'publishing'
  );

  const findInFlightForViewer = (viewerID: number) => (
    operations.value.find(operation => (
      operation.publisherUserID === viewerID && isInFlight(operation)
    ))
  );

  const serialize = (operation: PublishOperation) => serializePublishOperation(operation);

  const persistRequired = async (operation: PublishOperation) => {
    let updated: boolean;
    try {
      updated = await updatePostPublishOperation(serialize(operation));
    } catch (error) {
      throw new PublishPersistenceError(error instanceof Error ? error.message : 'Publish persistence failed.');
    }
    if (!updated) throw new SupersededPublishOperationError('A newer publish operation replaced this one.');
  };

  const persistBestEffort = async (operation: PublishOperation) => {
    try {
      await updatePostPublishOperation(serialize(operation));
    } catch {
      // A durable prior phase remains safe to recover or replay with the same key.
    }
  };

  const hydratePublishedPostBookmarkState = async (
    operation: PublishOperation,
    post: Post,
  ) => {
    const capturedVersion = captureBookmarkStateSyncVersion(post.id);
    const isCurrent = () => (
      operation.phase === 'succeeded'
      && currentViewerID() === operation.publisherUserID
    );

    try {
      const response = await getPostBookmarkStates([post.id]);
      if (!isCurrent()) return;
      const item = response.items.find(candidate => candidate.post_id === post.id);
      const update = {
        postId: post.id,
        bookmarked: item?.bookmarked ?? false,
        status: item ? 'ready' : 'unavailable',
      } as const;
      if (syncHydratedPostBookmarkState(update, capturedVersion)) {
        try {
          feedStore.applyBookmarkStateUpdate(update);
        } catch {
          // A local cache failure must not turn a successful publish into an unhandled rejection.
        }
      }
    } catch {
      if (!isCurrent()) return;
      const update = {
        postId: post.id,
        bookmarked: false,
        status: 'unavailable',
      } as const;
      if (syncHydratedPostBookmarkState(update, capturedVersion)) {
        try {
          feedStore.applyBookmarkStateUpdate(update);
        } catch {
          // A local cache failure must not turn a successful publish into an unhandled rejection.
        }
      }
    }
  };

  const draftMatchesOperation = (operation: PublishOperation) => {
    const sameBoundWorkingSubmission = postDraft.publishOperationID === operation.id;
    const sameSavedSource = operation.sourceDraftID !== null
      && postDraft.isSavedDraft
      && postDraft.draftID === operation.sourceDraftID;

    if (
      postDraft.viewerID !== operation.publisherUserID
      || (operation.sourceDraftID === null && !sameBoundWorkingSubmission)
      || (operation.sourceDraftID !== null && !sameSavedSource)
      || postDraft.content.trim() !== operation.content
      || postDraft.media.length !== operation.media.length
    ) {
      return false;
    }

    return operation.media.every((operationMedia, index) => {
      const draftMedia = postDraft.media[index];
      return Boolean(
        draftMedia
        && draftMedia.id === operationMedia.draftMediaID
        && draftMedia.file.name === operationMedia.file.name
        && draftMedia.file.type === operationMedia.file.type
        && draftMedia.file.size === operationMedia.file.size
        && draftMedia.file.lastModified === operationMedia.file.lastModified,
      );
    });
  };

  const getDraftPublishBlockReason = (
    viewerID: number,
    boundOperationID: string | null | undefined = null,
  ): PublishBlockReason | null => {
    const normalizedViewerID = normalizeViewerID(viewerID);
    if (normalizedViewerID === null) return null;

    const operationID = unresolvedOperationByViewer.value.get(normalizedViewerID);
    const operation = operationID ? getOperation(operationID) : undefined;
    if (!operation) {
      if (operationID) return 'unresolved_publish';
      // Running operations are always durable in normal execution. This fallback also
      // keeps proactive blocking safe if the in-memory phase changes before its owner map.
      return findInFlightForViewer(normalizedViewerID)
        ? 'another_publish_in_flight'
        : null;
    }

    const sameSubmission = draftMatchesOperation(operation);
    if (abandoningOperationIDs.has(operation.id)) return 'unresolved_publish';
    if (isInFlight(operation)) {
      return operation.id === boundOperationID && sameSubmission
        ? null
        : 'another_publish_in_flight';
    }
    if (operation.phase === 'failed' && sameSubmission) {
      return operation.failureKind === 'idempotency_conflict'
        ? 'idempotency_conflict'
        : operation.failureKind === 'retryable'
          ? null
          : 'unresolved_publish';
    }
    if (operation.phase === 'succeeded') return 'cleanup_pending';
    return 'unresolved_publish';
  };

  const isDraftBlockedByAnotherPublish = (
    viewerID: number,
    boundOperationID: string | null | undefined = null,
  ) => getDraftPublishBlockReason(viewerID, boundOperationID) === 'another_publish_in_flight';

  const syncUploadedURLToDraft = (
    operation: PublishOperation,
    media: PublishOperationMedia,
    uploadedURL: string,
  ) => {
    if (
      postDraft.publishOperationID === operation.id
      && postDraft.viewerID === operation.publisherUserID
    ) {
      postDraft.setUploadedURL(media.draftMediaID, uploadedURL);
    }
  };

  const uploadPendingMedia = async (operation: PublishOperation) => {
    const pending = operation.media.filter(media => !media.uploadedURL.trim());

    for (let start = 0; start < pending.length; start += mediaUploadConcurrency) {
      const batch = pending.slice(start, start + mediaUploadConcurrency);
      const results = await Promise.allSettled(batch.map(async media => {
        const uploadedURL = (await uploadPostMedia(media.file)).trim();
        if (!uploadedURL) throw new Error('The media upload returned no URL.');
        media.uploadedURL = uploadedURL;
        syncUploadedURLToDraft(operation, media, uploadedURL);
        return uploadedURL;
      }));

      // Checkpoint all successes from the batch together before another upload starts
      // or the operation is marked failed.
      await persistRequired(operation);
      const failure = results.find(result => result.status === 'rejected');
      if (failure?.status === 'rejected') throw failure.reason;
    }
  };

  const finalizeSuccessfulOperation = async (
    operation: PublishOperation,
    post: Post,
  ): Promise<SuccessCleanupResult> => {
    let sourceDraftClean = operation.sourceDraftID === null;
    if (operation.sourceDraftID) {
      try {
        await postDraft.resolvePublishedSourceDraft(
          operation.publisherUserID,
          operation.sourceDraftID,
          operation.sourceDraftSnapshot,
        );
        // Deleted, missing, changed, and legacy-without-snapshot all resolve ownership.
        sourceDraftClean = true;
      } catch {
        sourceDraftClean = false;
      }
    }

    if (currentViewerID() === operation.publisherUserID) {
      // Cache reconciliation is best effort. The server response is authoritative.
      try {
        feedStore.registerPublishedPost(post, operation.publisherUserID);
      } catch {
        // Keep the confirmed server result successful.
      }
      try {
        profileSessionStore.registerPublishedTimelinePost(post, operation.publisherUserID);
      } catch {
        // Keep the confirmed server result successful.
      }
      try {
        authStore.syncCurrentIdentityProfile(post.author);
      } catch {
        // Keep the confirmed server result successful.
      }
      void hydratePublishedPostBookmarkState(operation, post);
    }

    postDraft.clearIfBoundTo(operation.id, operation.publisherUserID);
    if (!sourceDraftClean) {
      return 'pending';
    }

    let deleted = false;
    try {
      deleted = await deletePostPublishOperation(operation.publisherUserID, operation.id);
    } catch {
      // A confirmed server success remains succeeded; cleanup can be retried later.
    }
    if (deleted) {
      releaseOperationOwnership(operation.publisherUserID, operation.id);
      return 'resolved';
    }

    // A false conditional delete is ambiguous until a read proves this operation no
    // longer owns the durable slot. Never mistake a newer operation for this one.
    try {
      const persisted = await getPostPublishOperation(operation.publisherUserID);
      if (!persisted || persisted.id !== operation.id) {
        if (persisted) {
          if (persisted.publisherUserID !== operation.publisherUserID) {
            return 'pending';
          }
        }
        releaseOperationOwnership(operation.publisherUserID, operation.id);
        if (persisted) {
          const replacement = restorePublishOperation(persisted) as PublishOperation;
          upsertOperation(replacement);
          markOperationUnresolved(replacement);
        }
        return 'resolved';
      }
    } catch {
      // Keep the succeeded operation unresolved until storage confirms it is retired.
    }
    return 'pending';
  };

  const markFailed = async (
    operation: PublishOperation,
    error: unknown,
    failureKind: Exclude<PublishFailureKind, null> = 'retryable',
  ) => {
    operation.phase = 'failed';
    operation.failureKind = failureKind;
    operation.error = failureKind === 'idempotency_conflict'
      ? publishConflictMessage
      : error instanceof PublishPersistenceError
        ? publishPersistenceMessage
        : publishFailureMessage;
    await persistBestEffort(operation);
  };

  const runOperation = async (operation: PublishOperation) => {
    if (runningOperationIDs.has(operation.id)) return;
    runningOperationIDs.add(operation.id);
    let posting = false;
    try {
      if (operation.media.some(media => !media.uploadedURL)) {
        operation.phase = 'uploading';
        await uploadPendingMedia(operation);
      }
      if (operation.media.some(media => !media.uploadedURL.trim())) {
        throw new Error('The publish media set is incomplete.');
      }

      // The complete URL set and exact payload must be durable before POST starts.
      operation.phase = 'publishing';
      operation.failureKind = null;
      operation.error = '';
      await persistRequired(operation);
      const payload: CreatePostPayload = {
        content: operation.content,
        media: operation.media.map(media => ({
          type: 'image' as const,
          url: media.uploadedURL,
        })),
      };
      posting = true;
      const post = await createPost(payload, { idempotencyKey: operation.id });

      operation.post = post;
      operation.error = '';
      operation.failureKind = null;
      operation.phase = 'succeeded';
      // If this checkpoint fails, keep the in-memory result successful and continue cleanup.
      await persistBestEffort(operation);
      await finalizeSuccessfulOperation(operation, post);
    } catch (error) {
      if (error instanceof SupersededPublishOperationError) return;
      const conflict = posting && isIdempotencyConflict(error);
      await markFailed(operation, error, conflict ? 'idempotency_conflict' : 'retryable');
    } finally {
      runningOperationIDs.delete(operation.id);
    }
  };

  const resumeCurrentViewerOperation = (viewerID: number) => {
    if (currentViewerID() !== viewerID) return;
    const operation = getViewerOperation(viewerID);
    if (!operation) return;
    if (operation.phase === 'succeeded' && operation.post) {
      void finalizeSuccessfulOperation(operation, operation.post);
    } else if (operation.phase === 'uploading' || operation.phase === 'publishing') {
      void runOperation(operation);
    }
  };

  const ensureHydratedForViewer = (viewerID: number): Promise<void> => {
    if (hydratedViewerIDs.has(viewerID)) {
      resumeCurrentViewerOperation(viewerID);
      return Promise.resolve();
    }
    const pending = hydrationPromises.get(viewerID);
    if (pending) return pending;

    const hydration = (async () => {
      try {
        const persisted = await getPostPublishOperation(viewerID);
        if (persisted) {
          if (persisted.publisherUserID !== viewerID) {
            throw new Error('The saved publish operation belongs to another viewer.');
          }
          const operation = restorePublishOperation(persisted) as PublishOperation;
          if (operation.phase === 'succeeded' && !operation.post) {
            throw new Error('The successful publish record is incomplete.');
          }
          markOperationUnresolved(operation);
          upsertOperation(operation);
          recoveryErrors.value.delete(viewerID);
          if (operation.phase === 'succeeded' && operation.post) {
            await finalizeSuccessfulOperation(operation, operation.post);
          } else if (operation.phase === 'uploading' || operation.phase === 'publishing') {
            resumeCurrentViewerOperation(viewerID);
          }
        } else {
          unresolvedOperationByViewer.value.delete(viewerID);
          recoveryErrors.value.delete(viewerID);
        }
        hydratedViewerIDs.add(viewerID);
      } catch {
        recoveryErrors.value.set(
          viewerID,
          'Couldn’t restore the pending post from this device. Publishing is paused until storage is available.',
        );
        throw new PublishPersistenceError('Could not restore the pending publish operation.');
      }
    })().finally(() => {
      hydrationPromises.delete(viewerID);
    });

    hydrationPromises.set(viewerID, hydration);
    return hydration;
  };

  const activateViewer = async (viewerID: number | null) => {
    const normalizedViewerID = normalizeViewerID(viewerID);
    if (normalizedViewerID === null) return;
    try {
      await ensureHydratedForViewer(normalizedViewerID);
    } catch {
      // Expose the scoped recovery error; the composer will fail closed if a publish is attempted.
    }
  };

  const retry = async (operationID: string): Promise<boolean> => {
    const operation = getOperation(operationID);
    if (
      !operation
      || operation.phase !== 'failed'
      || operation.failureKind !== 'retryable'
      || currentViewerID() !== operation.publisherUserID
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
      || abandoningOperationIDs.has(operation.id)
    ) {
      return false;
    }
    const conflictingInFlight = operations.value.find(candidate => (
      candidate.id !== operation.id
      && candidate.publisherUserID === operation.publisherUserID
      && isInFlight(candidate)
    ));
    if (conflictingInFlight) return false;
    const shouldRebindCurrentDraft = draftMatchesOperation(operation);

    const previous = {
      phase: operation.phase,
      failureKind: operation.failureKind,
      error: operation.error,
    };
    operation.error = '';
    operation.failureKind = null;
    operation.phase = operation.media.some(media => !media.uploadedURL.trim())
      ? 'uploading'
      : 'publishing';
    try {
      await persistRequired(operation);
    } catch {
      operation.phase = previous.phase;
      operation.failureKind = previous.failureKind;
      operation.error = publishPersistenceMessage;
      return false;
    }
    if (shouldRebindCurrentDraft) {
      postDraft.bindPublishOperation(operation.id);
    }
    void runOperation(operation);
    return true;
  };

  const startOrRetryDraft = async (): Promise<StartPublishResult> => {
    const publisherUserID = currentViewerID();
    if (publisherUserID === null) {
      return { status: 'rejected', reason: 'unauthenticated' };
    }

    try {
      await ensureHydratedForViewer(publisherUserID);
    } catch {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
    if (currentViewerID() !== publisherUserID) {
      return { status: 'rejected', reason: 'unauthenticated' };
    }

    const unresolvedOperationID = unresolvedOperationByViewer.value.get(publisherUserID);
    const unresolvedOperation = unresolvedOperationID
      ? getOperation(unresolvedOperationID)
      : undefined;
    if (unresolvedOperationID && !unresolvedOperation) {
      // The durable owner must be represented in memory before a new key can be made.
      return { status: 'blocked', reason: 'unresolved_publish' };
    }

    if (unresolvedOperation) {
      if (isInFlight(unresolvedOperation)) {
        if (
          postDraft.publishOperationID === unresolvedOperation.id
          && draftMatchesOperation(unresolvedOperation)
        ) {
          return { status: 'accepted', operation: unresolvedOperation };
        }
        return { status: 'blocked', reason: 'another_publish_in_flight' };
      }

      if (unresolvedOperation.phase === 'failed') {
        if (abandoningOperationIDs.has(unresolvedOperation.id)) {
          return { status: 'blocked', reason: 'unresolved_publish' };
        }
        const sameSubmission = draftMatchesOperation(unresolvedOperation);
        if (unresolvedOperation.failureKind === 'idempotency_conflict') {
          return {
            status: 'blocked',
            reason: sameSubmission ? 'idempotency_conflict' : 'unresolved_publish',
          };
        }
        if (
          unresolvedOperation.failureKind !== 'retryable'
          || !sameSubmission
        ) {
          return { status: 'blocked', reason: 'unresolved_publish' };
        }
        if (!(await retry(unresolvedOperation.id))) {
          return unresolvedOperation.error === publishPersistenceMessage
            ? { status: 'rejected', reason: 'persistence_unavailable' }
            : { status: 'blocked', reason: 'another_publish_in_flight' };
        }
        postDraft.bindPublishOperation(unresolvedOperation.id);
        return { status: 'accepted', operation: unresolvedOperation };
      }

      if (unresolvedOperation.phase === 'succeeded' && unresolvedOperation.post) {
        const cleanup = await finalizeSuccessfulOperation(
          unresolvedOperation,
          unresolvedOperation.post,
        );
        if (cleanup === 'pending') {
          return { status: 'blocked', reason: 'cleanup_pending' };
        }
        if (currentViewerID() !== publisherUserID) {
          return { status: 'rejected', reason: 'unauthenticated' };
        }
        if (unresolvedOperationByViewer.value.has(publisherUserID)) {
          const currentReason = getDraftPublishBlockReason(publisherUserID);
          return {
            status: 'blocked',
            reason: currentReason || 'unresolved_publish',
          };
        }
      } else {
        return { status: 'rejected', reason: 'persistence_unavailable' };
      }
    } else {
      const existingInFlight = findInFlightForViewer(publisherUserID);
      if (existingInFlight) {
        return { status: 'blocked', reason: 'another_publish_in_flight' };
      }
    }

    const ownsSavedSource = postDraft.isSavedDraft
      && !postDraft.hasUnsavedChanges
      && postDraft.draftID !== null
      && postDraft.savedSnapshot !== null;
    const sourceDraftSnapshot = ownsSavedSource && postDraft.savedSnapshot
      ? createPostDraftSnapshot(
        postDraft.savedSnapshot.content,
        postDraft.savedSnapshot.media,
      )
      : null;
    const operation: PublishOperation = {
      id: createClientOperationID(),
      publisherUserID,
      sourceDraftID: sourceDraftSnapshot ? postDraft.draftID : null,
      sourceDraftSnapshot,
      content: postDraft.content.trim(),
      media: postDraft.media.map(media => ({
        draftMediaID: media.id,
        file: media.file,
        uploadedURL: media.uploadedURL.trim(),
      })),
      phase: postDraft.media.some(media => !media.uploadedURL.trim()) ? 'uploading' : 'publishing',
      failureKind: null,
      error: '',
      startedAt: Date.now(),
      post: null,
    };

    try {
      await replacePostPublishOperation(serialize(operation));
    } catch {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }

    // The durable record exists before the working draft is bound or any network starts.
    markOperationUnresolved(operation);
    postDraft.bindPublishOperation(operation.id);
    upsertOperation(operation);
    recoveryErrors.value.delete(publisherUserID);
    void runOperation(operation);
    return { status: 'accepted', operation };
  };

  const abandonFailedOperation = async (operationID: string): Promise<boolean> => {
    const operation = getOperation(operationID);
    if (
      !operation
      || operation.phase !== 'failed'
      || currentViewerID() !== operation.publisherUserID
      || runningOperationIDs.has(operation.id)
      || abandoningOperationIDs.has(operation.id)
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
    ) {
      return false;
    }

    abandoningOperationIDs.add(operation.id);
    let deleted: boolean;
    try {
      deleted = await deletePostPublishOperation(operation.publisherUserID, operation.id);
    } catch {
      return false;
    } finally {
      abandoningOperationIDs.delete(operation.id);
    }
    if (!deleted) {
      return false;
    }

    releaseOperationOwnership(operation.publisherUserID, operation.id);
    postDraft.releasePublishOperationBinding(operation.id, operation.publisherUserID);
    removeOperationIfCurrent(operation.publisherUserID, operation.id);
    return true;
  };

  return {
    operations,
    latestOperation,
    recoveryError,
    getOperation,
    getDraftPublishBlockReason,
    isDraftBlockedByAnotherPublish,
    activateViewer,
    startOrRetryDraft,
    retry,
    abandonFailedOperation,
  };
});
