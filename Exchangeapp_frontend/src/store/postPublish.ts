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
  claimPostPublishOperation,
  deletePostPublishOperation,
  deleteRejectedPostPublishOperation,
  getPostPublishOperation,
  restorePublishOperation,
  serializePublishOperation,
  serializePublishOperationCheckpoint,
  updatePostPublishOperationCheckpoint,
  type PersistedPublishFailureKind,
  type PersistedPublishPhase,
  type PublishOperationMediaValue,
  type PublishOperationValue,
  type PublishSourceDraft,
} from '../storage/postPublishRepository';
import {
  saveRecoveredPostDraft,
  type PersistedPostDraft,
} from '../storage/postDraftRepository';
import { useAuthStore } from './auth';
import {
  matchesDurableAuthOwner,
  type AuthRequestBinding,
} from '../auth/authRequestBinding';
import { useFeedStore } from './feed';
import { usePostDraftStore } from './postDraft';
import { useProfileSessionStore } from './profileSession';
import { createClientOperationID } from '../utils/clientOperationId';
import { refreshAndSyncPostQuoteCount } from './postQuoteCountReconciliation';
import {
  canAbandonDurableSubmission,
  canMarkDurableSubmissionFailed,
  canRetryDurableSubmission,
  classifySubmissionFailure,
} from '../utils/durableSubmissionContract';
import {
  createPostDraftSnapshot,
  postDraftSnapshotsEqual,
} from '../utils/postDraftSnapshot';
import {
  captureBookmarkStateSyncVersion,
  syncHydratedPostBookmarkState,
} from './sessionSync';
import {
  PublishCoordinationUnavailableError,
  withPostPublishExecutionLock,
} from '../utils/postPublishExecutionLock';

export type PublishPhase = PersistedPublishPhase;
export type PublishFailureKind = PersistedPublishFailureKind;

export type PublishOperationMedia = PublishOperationMediaValue;
export type PublishOperation = PublishOperationValue;

export type PublishSuccessNotice = Readonly<{
  operationID: string;
  publisherUserID: number;
  postID: number;
  completedAt: number;
}>;

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
    reason: 'unauthenticated' | 'persistence_unavailable' | 'coordination_unavailable';
  };

const mediaUploadConcurrency = 2;
const publishSuccessNoticeDurationMs = 4000;
const publishPersistenceMessage = 'Couldn’t save publish progress on this device. Retry.';
const publishRecoveryUnavailableMessage = 'Couldn’t restore the pending post from this device. Publishing is paused to prevent duplicate submissions.';
const publishCoordinationUnavailableMessage = 'This browser cannot safely coordinate publishing across tabs. Publishing is paused; use a browser that supports cross-tab locks and try again.';

class SupersededPublishOperationError extends Error {}
class PublishPersistenceError extends Error {}
class PublishAuthContextChangedError extends Error {
  constructor() {
    super('Publish authentication context changed');
    this.name = 'PublishAuthContextChangedError';
  }
}

type SuccessCleanupResult = 'resolved' | 'pending';

type LockedStart<T> = {
  result: T;
  completion?: () => Promise<void>;
};

const normalizeViewerID = (value: unknown) => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
    ? value
    : null
);

export const usePostPublishStore = defineStore('postPublish', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const postDraft = usePostDraftStore();
  const profileSessionStore = useProfileSessionStore();
  const operations = ref<PublishOperation[]>([]);
  const successNotice = ref<PublishSuccessNotice | null>(null);
  const recoveryErrors = ref(new Map<number, string>());
  const unresolvedOperationByViewer = ref(new Map<number, string>());
  const runningOperationIDs = new Set<string>();
  const abandoningOperationIDs = new Set<string>();
  const recoveringOperationIDs = new Set<string>();
  const startingViewerIDs = new Set<number>();
  let successNoticeTimer: ReturnType<typeof setTimeout> | null = null;
  const hydrationPromises = new Map<number, Promise<void>>();
  const hydratedViewerIDs = new Set<number>();

  const currentViewerID = () => (
    authStore.isAuthenticated
      ? normalizeViewerID(authStore.currentIdentity?.id)
      : null
  );

  const captureOperationAuthBinding = (operation: PublishOperation): AuthRequestBinding => {
    const binding = authStore.captureRequestAuthBinding();
    if (!binding || !matchesDurableAuthOwner(binding, operation.publisherUserID, operation.publisherSessionID)) {
      throw new PublishAuthContextChangedError();
    }
    return binding;
  };

  const operationMatchesCurrentSession = (operation: PublishOperation) => {
    const binding = authStore.captureRequestAuthBinding();
    return matchesDurableAuthOwner(binding, operation.publisherUserID, operation.publisherSessionID);
  };

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

  const retireResolvedOperation = (viewerID: number, operationID: string) => {
    removeOperationIfCurrent(viewerID, operationID);
  };

  const clearSuccessNotice = (operationID?: string) => {
    if (operationID && successNotice.value?.operationID !== operationID) {
      return false;
    }

    successNotice.value = null;
    if (successNoticeTimer !== null) {
      clearTimeout(successNoticeTimer);
      successNoticeTimer = null;
    }
    return true;
  };

  const recordPublishSuccessNotice = (operation: PublishOperation, post: Post) => {
    if (successNoticeTimer !== null) {
      clearTimeout(successNoticeTimer);
    }

    const notice = Object.freeze({
      operationID: operation.id,
      publisherUserID: operation.publisherUserID,
      postID: post.id,
      completedAt: Date.now(),
    });
    successNotice.value = notice;
    successNoticeTimer = setTimeout(
      () => clearSuccessNotice(notice.operationID),
      publishSuccessNoticeDurationMs,
    );
  };

  const setCoordinationUnavailable = (viewerID: number) => {
    recoveryErrors.value.set(viewerID, publishCoordinationUnavailableMessage);
  };

  const setRecoveryUnavailable = (viewerID: number) => {
    recoveryErrors.value.set(viewerID, publishRecoveryUnavailableMessage);
  };

  const withLockedStartResult = <T>(
    viewerID: number,
    prepare: () => Promise<LockedStart<T>>,
    onReleased?: () => void,
  ): Promise<T> => new Promise<T>((resolve, reject) => {
    let resultSettled = false;
    const execution = withPostPublishExecutionLock(viewerID, async () => {
      const started = await prepare();
      resultSettled = true;
      resolve(started.result);
      if (started.completion) await started.completion();
    });

    void execution
      .catch(error => {
        if (!resultSettled) {
          resultSettled = true;
          reject(error);
        } else {
          setRecoveryUnavailable(viewerID);
        }
      })
      .finally(() => onReleased?.());
  });

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

  const currentSuccessNotice = computed<PublishSuccessNotice | null>(() => {
    const viewerID = currentViewerID();
    if (viewerID === null) return null;
    const notice = successNotice.value;
    return notice?.publisherUserID === viewerID ? notice : null;
  });

  const recoveryError = computed(() => {
    const viewerID = currentViewerID();
    return viewerID === null ? '' : recoveryErrors.value.get(viewerID) || '';
  });

  const upsertOperation = (operation: PublishOperation) => {
    const existing = getViewerOperation(operation.publisherUserID);
    const current = existing?.id === operation.id && existing !== operation
      ? Object.assign(existing, operation)
      : operation;
    operations.value = [
      ...operations.value.filter(item => item.publisherUserID !== current.publisherUserID),
      current,
    ];
    return current;
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
      updated = await updatePostPublishOperationCheckpoint(serializePublishOperationCheckpoint(operation));
    } catch (error) {
      throw new PublishPersistenceError(error instanceof Error ? error.message : 'Publish persistence failed.');
    }
    if (!updated) throw new SupersededPublishOperationError('A newer publish operation replaced this one.');
  };

  const persistBestEffort = async (operation: PublishOperation) => {
    try {
      await updatePostPublishOperationCheckpoint(serializePublishOperationCheckpoint(operation));
    } catch {
      // A durable prior phase remains safe to recover or replay with the same key.
    }
  };

  const hydratePublishedPostBookmarkState = async (
    operation: PublishOperation,
    post: Post,
  ) => {
    if (!operationMatchesCurrentSession(operation)) return;
    const capturedVersion = captureBookmarkStateSyncVersion(post.id);
    const isCurrent = () => (
      operation.phase === 'succeeded'
      && operationMatchesCurrentSession(operation)
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
      || postDraft.quotePostID !== operation.quotePostID
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

  const hasConflictingWorkingDraft = (operation: PublishOperation) => {
    if (postDraft.viewerID !== operation.publisherUserID) return false;
    const hasWorkingDraft = postDraft.draftID !== null
      || postDraft.hasContent
      || postDraft.quotePostID !== null
      || postDraft.publishOperationID !== null;
    return hasWorkingDraft && !draftMatchesOperation(operation);
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
        : canRetryDurableSubmission(operation.phase, operation.failureKind)
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
        const authBinding = captureOperationAuthBinding(operation);
        const uploadedURL = (await uploadPostMedia(media.file, { authBinding })).trim();
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

  const finalizeSuccessfulOperationUnderLock = async (
    operation: PublishOperation,
    post: Post,
  ): Promise<SuccessCleanupResult> => {
    if (post.author.id !== operation.publisherUserID) {
      await markFailed(operation, new PublishAuthContextChangedError());
      return 'pending';
    }
    if (operation.quotePostID !== null) {
      void refreshAndSyncPostQuoteCount(operation.quotePostID);
    }
    let sourceDraftClean = operation.sourceDraftID === null;
    if (operation.sourceDraftID !== null) {
      try {
        await postDraft.resolvePublishedSourceDraft(
          operation.publisherUserID,
          operation.sourceDraftID,
          operation.sourceDraftSnapshot,
          () => operationMatchesCurrentSession(operation),
        );
        // Deleted, missing, and changed drafts all resolve this operation's ownership.
        sourceDraftClean = true;
      } catch {
        sourceDraftClean = false;
      }
    }

    if (operationMatchesCurrentSession(operation)) {
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

    if (operationMatchesCurrentSession(operation)) {
      postDraft.clearIfBoundTo(operation.id, operation.publisherUserID);
    }
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
      recordPublishSuccessNotice(operation, post);
      retireResolvedOperation(operation.publisherUserID, operation.id);
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
        recordPublishSuccessNotice(operation, post);
        retireResolvedOperation(operation.publisherUserID, operation.id);
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
    classification = classifySubmissionFailure(error, 'post'),
  ) => {
    if (!canMarkDurableSubmissionFailed(operation.phase)) return;
    if (classification.code === 'INVALID_MEDIA') {
      for (const media of operation.media) {
        media.uploadedURL = '';
        if (
          postDraft.publishOperationID === operation.id
          && postDraft.viewerID === operation.publisherUserID
        ) {
          postDraft.setUploadedURL(media.draftMediaID, '');
        }
      }
    }
    operation.phase = 'failed';
    operation.failureKind = classification.failureKind;
    operation.error = error instanceof PublishPersistenceError
      ? publishPersistenceMessage
      : classification.message;
    await persistBestEffort(operation);
  };

  const readLatestOperationUnderLock = async (
    viewerID: number,
    expectedOperationID?: string,
  ): Promise<PublishOperation | null> => {
    let persisted: Awaited<ReturnType<typeof getPostPublishOperation>>;
    try {
      persisted = await getPostPublishOperation(viewerID);
    } catch (error) {
      throw new PublishPersistenceError(
        error instanceof Error ? error.message : 'Could not read publish storage.',
      );
    }

    if (!persisted) {
      const previous = getViewerOperation(viewerID);
      if (previous && (!expectedOperationID || previous.id === expectedOperationID)) {
        releaseOperationOwnership(viewerID, previous.id);
        postDraft.releasePublishOperationBinding(previous.id, viewerID);
        removeOperationIfCurrent(viewerID, previous.id);
      }
      recoveryErrors.value.delete(viewerID);
      return null;
    }
    if (persisted.publisherUserID !== viewerID) {
      throw new PublishPersistenceError('The saved publish operation belongs to another viewer.');
    }

    const latest = restorePublishOperation(persisted) as PublishOperation;
    if (latest.phase === 'succeeded' && !latest.post) {
      throw new PublishPersistenceError('The successful publish record is incomplete.');
    }

    const previous = getViewerOperation(viewerID);
    if (expectedOperationID && latest.id !== expectedOperationID) {
      if (previous?.id === expectedOperationID) {
        postDraft.releasePublishOperationBinding(expectedOperationID, viewerID);
      }
      const replacement = upsertOperation(latest);
      markOperationUnresolved(replacement);
      recoveryErrors.value.delete(viewerID);
      return null;
    }

    const current = upsertOperation(latest);
    markOperationUnresolved(current);
    recoveryErrors.value.delete(viewerID);
    return current;
  };

  const executeOperationUnderLock = async (operation: PublishOperation) => {
    try {
      if (operation.phase !== 'uploading' && operation.phase !== 'publishing') return;
      if (!operationMatchesCurrentSession(operation)) {
        throw new PublishAuthContextChangedError();
      }
      if (operation.phase === 'uploading') {
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
        ...(operation.quotePostID !== null ? { quote_post_id: operation.quotePostID } : {}),
        media: operation.media.map(media => ({
          type: 'image' as const,
          url: media.uploadedURL,
        })),
      };
      const authBinding = captureOperationAuthBinding(operation);
      const post = await createPost(payload, { idempotencyKey: operation.id, authBinding });
      if (post.author.id !== operation.publisherUserID) {
        throw new PublishAuthContextChangedError();
      }

      operation.post = post;
      operation.error = '';
      operation.failureKind = null;
      operation.phase = 'succeeded';
      // If this checkpoint fails, keep the in-memory result successful and continue cleanup.
      await persistBestEffort(operation);
      await finalizeSuccessfulOperationUnderLock(operation, post);
    } catch (error) {
      if (error instanceof SupersededPublishOperationError) return;
      const authContextChanged = error instanceof PublishAuthContextChangedError
        || (error instanceof Error && error.name === 'AuthSessionChangedError')
        || !operationMatchesCurrentSession(operation);
      await markFailed(
        operation,
        error,
        classifySubmissionFailure(
          authContextChanged ? new PublishAuthContextChangedError() : error,
          'post',
        ),
      );
    }
  };

  const executeTrackedOperationUnderLock = async (operation: PublishOperation) => {
    runningOperationIDs.add(operation.id);
    try {
      await executeOperationUnderLock(operation);
    } finally {
      runningOperationIDs.delete(operation.id);
    }
  };

  const runOperation = (operation: PublishOperation) => {
    if (runningOperationIDs.has(operation.id)) return;
    runningOperationIDs.add(operation.id);
    let replacementOperationID: string | null = null;

    const execution = withPostPublishExecutionLock(operation.publisherUserID, async () => {
      const latest = await readLatestOperationUnderLock(operation.publisherUserID, operation.id);
      if (!latest) {
        const replacement = getViewerOperation(operation.publisherUserID);
        if (replacement && replacement.id !== operation.id) {
          replacementOperationID = replacement.id;
        }
        return;
      }

      if (latest.phase === 'uploading' || latest.phase === 'publishing') {
        if (!operationMatchesCurrentSession(latest)) {
          await markFailed(latest, new PublishAuthContextChangedError());
          return;
        }
        await executeOperationUnderLock(latest);
      } else if (latest.phase === 'succeeded' && latest.post) {
        await finalizeSuccessfulOperationUnderLock(latest, latest.post);
      } else if (
        canAbandonDurableSubmission(latest.phase, latest.failureKind)
        && !operationMatchesCurrentSession(latest)
      ) {
        await markFailed(latest, new PublishAuthContextChangedError());
      }
    });

    void execution
      .catch(error => {
        if (error instanceof PublishCoordinationUnavailableError) {
          setCoordinationUnavailable(operation.publisherUserID);
        } else {
          setRecoveryUnavailable(operation.publisherUserID);
        }
      })
      .finally(() => {
        runningOperationIDs.delete(operation.id);
        if (replacementOperationID) {
          const replacement = getOperation(replacementOperationID);
          if (replacement && (
            isInFlight(replacement)
            || replacement.phase === 'succeeded'
            || (canAbandonDurableSubmission(replacement.phase, replacement.failureKind)
              && !operationMatchesCurrentSession(replacement))
          )) {
            runOperation(replacement);
          }
        }
      });
  };

  const resumeCurrentViewerOperation = (viewerID: number) => {
    if (currentViewerID() !== viewerID) return;
    const operation = getViewerOperation(viewerID);
    if (!operation) return;
    if (
      isInFlight(operation)
      || operation.phase === 'succeeded'
      || (canAbandonDurableSubmission(operation.phase, operation.failureKind)
        && !operationMatchesCurrentSession(operation))
    ) {
      runOperation(operation);
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
        // A late IndexedDB read for a viewer that is no longer active must not be
        // adopted or classified as a session failure. A later activation re-reads it.
        if (currentViewerID() !== viewerID) return;
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
          if (
            isInFlight(operation)
            || operation.phase === 'succeeded'
            || (canAbandonDurableSubmission(operation.phase, operation.failureKind)
              && !operationMatchesCurrentSession(operation))
          ) {
            resumeCurrentViewerOperation(viewerID);
          }
        } else {
          unresolvedOperationByViewer.value.delete(viewerID);
          recoveryErrors.value.delete(viewerID);
        }
        hydratedViewerIDs.add(viewerID);
      } catch (error) {
        console.error('[postPublish] Recovery failed', error);
        recoveryErrors.value.set(viewerID, publishRecoveryUnavailableMessage);
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

  const prepareRetryUnderLock = async (operation: PublishOperation): Promise<boolean> => {
    if (
      !canRetryDurableSubmission(operation.phase, operation.failureKind)
      || currentViewerID() !== operation.publisherUserID
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
      || abandoningOperationIDs.has(operation.id)
    ) {
      return false;
    }
    if (!operationMatchesCurrentSession(operation)) {
      await markFailed(operation, new PublishAuthContextChangedError());
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
    clearSuccessNotice();
    if (shouldRebindCurrentDraft) {
      postDraft.bindPublishOperation(operation.id);
    }
    upsertOperation(operation);
    return true;
  };

  const retry = async (operationID: string): Promise<boolean> => {
    const operation = getOperation(operationID);
    if (
      !operation
      || !canRetryDurableSubmission(operation.phase, operation.failureKind)
      || currentViewerID() !== operation.publisherUserID
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
      || runningOperationIDs.has(operation.id)
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

    runningOperationIDs.add(operation.id);
    try {
      return await withLockedStartResult(
        operation.publisherUserID,
        async () => {
          const latest = await readLatestOperationUnderLock(
            operation.publisherUserID,
            operation.id,
          );
          if (!latest || !(await prepareRetryUnderLock(latest))) {
            return { result: false };
          }
          return {
            result: true,
            completion: () => executeOperationUnderLock(latest),
          };
        },
        () => runningOperationIDs.delete(operation.id),
      );
    } catch (error) {
      if (error instanceof PublishCoordinationUnavailableError) {
        setCoordinationUnavailable(operation.publisherUserID);
      } else {
        setRecoveryUnavailable(operation.publisherUserID);
      }
      return false;
    }
  };

  const createOperationFromCurrentDraft = (
    authBinding: AuthRequestBinding,
  ): PublishOperation => {
    const sourceDraft: PublishSourceDraft = postDraft.isSavedDraft
      && !postDraft.hasUnsavedChanges
      && postDraft.draftID !== null
      && postDraft.savedSnapshot !== null
      ? {
        sourceDraftID: postDraft.draftID,
        sourceDraftSnapshot: createPostDraftSnapshot(
          postDraft.savedSnapshot.content,
          postDraft.savedSnapshot.media,
          postDraft.savedSnapshot.quotePostID,
        ),
      }
      : { sourceDraftID: null, sourceDraftSnapshot: null };
    return {
      ...sourceDraft,
      id: createClientOperationID(),
      publisherUserID: authBinding.userID,
      publisherSessionID: authBinding.sessionID,
      content: postDraft.content.trim(),
      quotePostID: postDraft.quotePostID,
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
  };

  const handleExistingOperationUnderLock = async (
    operation: PublishOperation,
  ): Promise<LockedStart<StartPublishResult> | null> => {
    if (operation.phase === 'succeeded') {
      if (!operation.post) setRecoveryUnavailable(operation.publisherUserID);
      else await finalizeSuccessfulOperationUnderLock(operation, operation.post);
      return {
        result: operation.post
          ? { status: 'blocked', reason: 'cleanup_pending' }
          : { status: 'rejected', reason: 'persistence_unavailable' },
      };
    }

    if (isInFlight(operation)) {
      if (!operationMatchesCurrentSession(operation)) {
        await markFailed(operation, new PublishAuthContextChangedError());
        return { result: { status: 'blocked', reason: 'unresolved_publish' } };
      }
      const sameBoundSubmission = postDraft.publishOperationID === operation.id
        && draftMatchesOperation(operation);
      return {
        result: sameBoundSubmission
          ? { status: 'accepted', operation }
          : { status: 'blocked', reason: 'another_publish_in_flight' },
      };
    }

    if (operation.phase === 'failed') {
      if (!operationMatchesCurrentSession(operation)) {
        await markFailed(operation, new PublishAuthContextChangedError());
        return { result: { status: 'blocked', reason: 'unresolved_publish' } };
      }
      const sameSubmission = draftMatchesOperation(operation);
      if (operation.failureKind === 'idempotency_conflict') {
        return {
          result: {
            status: 'blocked',
            reason: sameSubmission ? 'idempotency_conflict' : 'unresolved_publish',
          },
        };
      }
      if (!sameSubmission || !canRetryDurableSubmission(operation.phase, operation.failureKind)) {
        return { result: { status: 'blocked', reason: 'unresolved_publish' } };
      }
      if (!(await prepareRetryUnderLock(operation))) {
        return operation.error === publishPersistenceMessage
          ? { result: { status: 'rejected', reason: 'persistence_unavailable' } }
          : { result: { status: 'blocked', reason: 'another_publish_in_flight' } };
      }
      return {
        result: { status: 'accepted', operation },
        completion: () => executeTrackedOperationUnderLock(operation),
      };
    }

    return { result: { status: 'blocked', reason: 'unresolved_publish' } };
  };

  const startOrRetryDraft = async (): Promise<StartPublishResult> => {
    const authBinding = authStore.captureRequestAuthBinding();
    if (!authBinding) {
      return { status: 'rejected', reason: 'unauthenticated' };
    }
    const publisherUserID = authBinding.userID;

    try {
      await ensureHydratedForViewer(publisherUserID);
    } catch {
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
    if (!authStore.matchesRequestAuthBinding(authBinding)) {
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
        if (recoveryErrors.value.get(publisherUserID) === publishCoordinationUnavailableMessage) {
          return { status: 'rejected', reason: 'coordination_unavailable' };
        }
        if (
          postDraft.publishOperationID === unresolvedOperation.id
          && draftMatchesOperation(unresolvedOperation)
        ) {
          return { status: 'accepted', operation: unresolvedOperation };
        }
        return { status: 'blocked', reason: 'another_publish_in_flight' };
      }
    }

    if (startingViewerIDs.has(publisherUserID)) {
      return { status: 'blocked', reason: 'another_publish_in_flight' };
    }
    startingViewerIDs.add(publisherUserID);
    try {
      return await withLockedStartResult(
        publisherUserID,
        async () => {
          if (!authStore.matchesRequestAuthBinding(authBinding)) {
            return { result: { status: 'rejected', reason: 'unauthenticated' } };
          }

          let existing = await readLatestOperationUnderLock(publisherUserID);
          if (existing) {
            const existingResult = await handleExistingOperationUnderLock(existing);
            if (existingResult) return existingResult;
            existing = await readLatestOperationUnderLock(publisherUserID);
            if (existing) {
              return { result: { status: 'blocked', reason: 'unresolved_publish' } };
            }
          }

          if (!authStore.matchesRequestAuthBinding(authBinding)) {
            return { result: { status: 'rejected', reason: 'unauthenticated' } };
          }
          const candidate = createOperationFromCurrentDraft(authBinding);
          let claim: Awaited<ReturnType<typeof claimPostPublishOperation>>;
          try {
            claim = await claimPostPublishOperation(serialize(candidate));
          } catch (error) {
            throw new PublishPersistenceError(
              error instanceof Error ? error.message : 'Could not save publish progress.',
            );
          }

          if (claim.status === 'occupied') {
            const occupied = await readLatestOperationUnderLock(publisherUserID);
            if (!occupied) {
              return { result: { status: 'rejected', reason: 'persistence_unavailable' } };
            }
            if (occupied.phase === 'succeeded') {
              if (!occupied.post) setRecoveryUnavailable(publisherUserID);
              return {
                result: occupied.post
                  ? { status: 'blocked', reason: 'cleanup_pending' }
                  : { status: 'rejected', reason: 'persistence_unavailable' },
              };
            }
            const occupiedResult = await handleExistingOperationUnderLock(occupied);
            if (occupiedResult) return occupiedResult;
            const current = await readLatestOperationUnderLock(publisherUserID);
            if (current) {
              return { result: { status: 'blocked', reason: 'unresolved_publish' } };
            }
            try {
              claim = await claimPostPublishOperation(serialize(candidate));
            } catch (error) {
              throw new PublishPersistenceError(
                error instanceof Error ? error.message : 'Could not save publish progress.',
              );
            }
            if (claim.status !== 'claimed') {
              return { result: { status: 'blocked', reason: 'unresolved_publish' } };
            }
          }

          const claimed = await readLatestOperationUnderLock(publisherUserID, candidate.id);
          if (!claimed) {
            return { result: { status: 'blocked', reason: 'unresolved_publish' } };
          }
          clearSuccessNotice();
          if (!authStore.matchesRequestAuthBinding(authBinding)) {
            await markFailed(
              claimed,
              new PublishAuthContextChangedError(),
            );
            return { result: { status: 'rejected', reason: 'unauthenticated' } };
          }
          postDraft.bindPublishOperation(claimed.id);
          recoveryErrors.value.delete(publisherUserID);
          return {
            result: { status: 'accepted', operation: claimed },
            completion: () => executeTrackedOperationUnderLock(claimed),
          };
        },
        () => startingViewerIDs.delete(publisherUserID),
      );
    } catch (error) {
      if (error instanceof PublishCoordinationUnavailableError) {
        setCoordinationUnavailable(publisherUserID);
        return { status: 'rejected', reason: 'coordination_unavailable' };
      }
      setRecoveryUnavailable(publisherUserID);
      return { status: 'rejected', reason: 'persistence_unavailable' };
    }
  };

  const recoverRejectedPostForEdit = async (operationID: string): Promise<string | null> => {
    const operation = getOperation(operationID);
    if (
      !operation
      || operation.phase !== 'failed'
      || operation.failureKind !== 'validation_error'
      || currentViewerID() !== operation.publisherUserID
      || !operationMatchesCurrentSession(operation)
      || runningOperationIDs.has(operation.id)
      || abandoningOperationIDs.has(operation.id)
      || recoveringOperationIDs.has(operation.id)
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
      || hasConflictingWorkingDraft(operation)
    ) return null;

    recoveringOperationIDs.add(operation.id);
    try {
      return await withPostPublishExecutionLock(operation.publisherUserID, async () => {
        const latest = await readLatestOperationUnderLock(operation.publisherUserID, operation.id);
        if (
          !latest
          || latest.id !== operation.id
          || latest.phase !== 'failed'
          || latest.failureKind !== 'validation_error'
          || currentViewerID() !== latest.publisherUserID
          || !operationMatchesCurrentSession(latest)
          || runningOperationIDs.has(latest.id)
          || abandoningOperationIDs.has(latest.id)
          || !recoveringOperationIDs.has(latest.id)
          || hasConflictingWorkingDraft(latest)
        ) return null;

        const recoveredDraftID = `rejected-post-${latest.id}`;
        const recoveredDraft: PersistedPostDraft = {
          id: recoveredDraftID,
          viewerID: latest.publisherUserID,
          recoveryOperationID: latest.id,
          content: latest.content,
          quotePostID: latest.quotePostID,
          media: latest.media.map(item => ({
            id: item.draftMediaID,
            blob: item.file.slice(0, item.file.size, item.file.type),
            name: item.file.name,
            type: item.file.type,
            size: item.file.size,
            lastModified: item.file.lastModified,
            uploadedURL: item.uploadedURL,
          })),
          createdAt: latest.startedAt,
          updatedAt: Date.now(),
        };
        const savedDraft = await saveRecoveredPostDraft(recoveredDraft, latest.id);
        if (savedDraft === 'collision') return null;

        if (
          currentViewerID() !== latest.publisherUserID
          || !operationMatchesCurrentSession(latest)
          || hasConflictingWorkingDraft(latest)
          || runningOperationIDs.has(latest.id)
        ) return null;

        const persisted = await getPostPublishOperation(latest.publisherUserID);
        if (
          !persisted
          || persisted.id !== latest.id
          || persisted.publisherSessionID !== latest.publisherSessionID
          || persisted.phase !== 'failed'
          || persisted.failureKind !== 'validation_error'
        ) return null;
        const verified = restorePublishOperation(persisted) as PublishOperation;
        const sameSourceSnapshot = latest.sourceDraftSnapshot === null
          ? verified.sourceDraftSnapshot === null
          : verified.sourceDraftSnapshot !== null
            && postDraftSnapshotsEqual(verified.sourceDraftSnapshot, latest.sourceDraftSnapshot);
        const samePayload = verified.content === latest.content
          && verified.quotePostID === latest.quotePostID
          && verified.sourceDraftID === latest.sourceDraftID
          && sameSourceSnapshot
          && verified.media.length === latest.media.length
          && verified.media.every((item, index) => {
            const expected = latest.media[index];
            return Boolean(expected)
              && item.draftMediaID === expected!.draftMediaID
              && item.file.name === expected!.file.name
              && item.file.type === expected!.file.type
              && item.file.size === expected!.file.size
              && item.file.lastModified === expected!.file.lastModified
              && item.uploadedURL === expected!.uploadedURL;
          });
        if (!samePayload) return null;

        const deleted = await deleteRejectedPostPublishOperation(
          latest.publisherUserID,
          latest.id,
          latest.publisherSessionID,
        );
        if (!deleted) return null;

        releaseOperationOwnership(latest.publisherUserID, latest.id);
        postDraft.releasePublishOperationBinding(latest.id, latest.publisherUserID);
        removeOperationIfCurrent(latest.publisherUserID, latest.id);
        recoveryErrors.value.delete(latest.publisherUserID);
        return recoveredDraftID;
      });
    } catch (error) {
      if (error instanceof PublishCoordinationUnavailableError) {
        setCoordinationUnavailable(operation.publisherUserID);
      } else {
        setRecoveryUnavailable(operation.publisherUserID);
      }
      return null;
    } finally {
      recoveringOperationIDs.delete(operation.id);
    }
  };

  const abandonFailedOperation = async (operationID: string): Promise<boolean> => {
    const operation = getOperation(operationID);
    if (
      !operation
      || !canAbandonDurableSubmission(operation.phase, operation.failureKind)
      || currentViewerID() !== operation.publisherUserID
      || runningOperationIDs.has(operation.id)
      || abandoningOperationIDs.has(operation.id)
      || recoveringOperationIDs.has(operation.id)
      || unresolvedOperationByViewer.value.get(operation.publisherUserID) !== operation.id
    ) {
      return false;
    }

    abandoningOperationIDs.add(operation.id);
    try {
      return await withPostPublishExecutionLock(operation.publisherUserID, async () => {
        const latest = await readLatestOperationUnderLock(
          operation.publisherUserID,
          operation.id,
        );
        if (
          !latest
          || latest.id !== operation.id
          || !canAbandonDurableSubmission(latest.phase, latest.failureKind)
          || currentViewerID() !== latest.publisherUserID
          || recoveringOperationIDs.has(latest.id)
        ) {
          return false;
        }

        let deleted = false;
        try {
          deleted = await deletePostPublishOperation(latest.publisherUserID, latest.id);
        } catch {
          setRecoveryUnavailable(latest.publisherUserID);
          return false;
        }
        if (!deleted) return false;

        releaseOperationOwnership(latest.publisherUserID, latest.id);
        postDraft.releasePublishOperationBinding(latest.id, latest.publisherUserID);
        removeOperationIfCurrent(latest.publisherUserID, latest.id);
        recoveryErrors.value.delete(latest.publisherUserID);
        return true;
      });
    } catch (error) {
      if (error instanceof PublishCoordinationUnavailableError) {
        setCoordinationUnavailable(operation.publisherUserID);
      } else {
        setRecoveryUnavailable(operation.publisherUserID);
      }
      return false;
    } finally {
      abandoningOperationIDs.delete(operation.id);
    }
  };

  return {
    operations,
    latestOperation,
    successNotice,
    currentSuccessNotice,
    recoveryError,
    getOperation,
    getDraftPublishBlockReason,
    isDraftBlockedByAnotherPublish,
    activateViewer,
    startOrRetryDraft,
    retry,
    recoverRejectedPostForEdit,
    abandonFailedOperation,
  };
});
