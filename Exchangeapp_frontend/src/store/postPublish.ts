import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  createPost,
  uploadPostMedia,
  type CreatePostPayload,
} from '../services/postService';
import type { Post } from '../types/Post';
import { useAuthStore } from './auth';
import { useFeedStore } from './feed';
import { usePostDraftStore } from './postDraft';
import { useProfileSessionStore } from './profileSession';

export type PublishPhase = 'uploading' | 'publishing' | 'failed' | 'succeeded';

export type PublishOperationMedia = {
  draftMediaID: string;
  file: File;
  uploadedURL: string;
};

export type PublishOperation = {
  id: string;
  publisherUserID: number;
  content: string;
  media: PublishOperationMedia[];
  phase: PublishPhase;
  error: string;
  startedAt: number;
  post: Post | null;
};

export type StartPublishResult =
  | {
    status: 'accepted';
    operation: PublishOperation;
  }
  | {
    status: 'blocked';
    reason: 'another_publish_in_flight';
  }
  | {
    status: 'rejected';
    reason: 'unauthenticated';
  };

const mediaUploadConcurrency = 2;
const publishFailureMessage = 'Couldn’t confirm this post. Retry safely.';

const normalizeViewerID = (value: unknown): number | null => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
    ? value
    : null
);

const formatUUIDBytes = (bytes: Uint8Array): string => {
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0'));
  return `${hex.slice(0, 4).join('')}-${hex.slice(4, 6).join('')}-${hex.slice(6, 8).join('')}-${hex.slice(8, 10).join('')}-${hex.slice(10, 16).join('')}`;
};

const createPublishOperationID = (): string => {
  const cryptoAPI = globalThis.crypto;
  if (cryptoAPI && typeof cryptoAPI.randomUUID === 'function') {
    return cryptoAPI.randomUUID();
  }
  const bytes = new Uint8Array(16);
  if (cryptoAPI && typeof cryptoAPI.getRandomValues === 'function') {
    cryptoAPI.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }
  return formatUUIDBytes(bytes);
};

export const usePostPublishStore = defineStore('postPublish', () => {
  const authStore = useAuthStore();
  const feedStore = useFeedStore();
  const postDraft = usePostDraftStore();
  const profileSessionStore = useProfileSessionStore();
  const operations = ref<PublishOperation[]>([]);
  const runningOperationIDs = new Set<string>();

  const currentViewerID = () => (
    authStore.isAuthenticated
      ? normalizeViewerID(authStore.currentIdentity?.id)
      : null
  );

  const getOperation = (operationID: string | null | undefined) => (
    operationID ? operations.value.find(operation => operation.id === operationID) : undefined
  );

  const latestOperation = computed<PublishOperation | null>(() => (
    operations.value.length > 0
      ? operations.value[operations.value.length - 1] || null
      : null
  ));

  const isInFlight = (operation: PublishOperation) => (
    operation.phase === 'uploading' || operation.phase === 'publishing'
  );

  const findInFlightForViewer = (viewerID: number) => (
    operations.value.find(operation => (
      operation.publisherUserID === viewerID && isInFlight(operation)
    ))
  );

  const isDraftBlockedByAnotherPublish = (
    viewerID: number,
    boundOperationID: string | null | undefined = null,
  ) => {
    const normalizedViewerID = normalizeViewerID(viewerID);
    if (normalizedViewerID === null) {
      return false;
    }
    const inFlight = findInFlightForViewer(normalizedViewerID);
    return Boolean(inFlight && inFlight.id !== boundOperationID);
  };

  const draftMatchesOperation = (operation: PublishOperation) => (
    postDraft.viewerID === operation.publisherUserID
    && postDraft.content.trim() === operation.content
    && postDraft.media.length === operation.media.length
    && operation.media.every((media, index) => {
      const draftMedia = postDraft.media[index];
      return draftMedia?.id === media.draftMediaID && draftMedia.file === media.file;
    })
  );

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
    const pending = operation.media.filter(media => !media.uploadedURL);
    if (pending.length === 0) {
      return;
    }

    for (let start = 0; start < pending.length; start += mediaUploadConcurrency) {
      const batch = pending.slice(start, start + mediaUploadConcurrency);
      const results = await Promise.allSettled(batch.map(async media => {
        const uploadedURL = (await uploadPostMedia(media.file)).trim();
        if (!uploadedURL) {
          throw new Error('The media upload returned no URL.');
        }
        media.uploadedURL = uploadedURL;
        syncUploadedURLToDraft(operation, media, uploadedURL);
        return uploadedURL;
      }));
      const failure = results.find(result => result.status === 'rejected');
      if (failure?.status === 'rejected') {
        throw failure.reason;
      }
    }
  };

  const reconcileSuccessfulPost = (operation: PublishOperation, post: Post) => {
    if (currentViewerID() !== operation.publisherUserID) {
      return;
    }

    // Cache reconciliation is best effort. The server response has already
    // made the operation successful, so a local cache failure must not cause a
    // retry that could create a second post.
    try {
      feedStore.registerPublishedPost(post, operation.publisherUserID);
    } catch {
      // Keep the authoritative server result successful.
    }
    try {
      profileSessionStore.registerPublishedTimelinePost(post, operation.publisherUserID);
    } catch {
      // Keep the authoritative server result successful.
    }
    try {
      authStore.syncCurrentIdentityProfile(post.author);
    } catch {
      // Keep the authoritative server result successful.
    }
    postDraft.clearIfBoundTo(operation.id, operation.publisherUserID);
  };

  const runOperation = async (operation: PublishOperation) => {
    if (runningOperationIDs.has(operation.id)) {
      return;
    }
    runningOperationIDs.add(operation.id);
    try {
      if (operation.media.some(media => !media.uploadedURL)) {
        operation.phase = 'uploading';
        await uploadPendingMedia(operation);
      }
      if (operation.media.some(media => !media.uploadedURL)) {
        throw new Error('The publish media set is incomplete.');
      }

      operation.phase = 'publishing';
      const payload: CreatePostPayload = {
        content: operation.content,
        media: operation.media.map(media => ({
          type: 'image' as const,
          url: media.uploadedURL,
        })),
      };
      const post = await createPost(payload, { idempotencyKey: operation.id });
      operation.post = post;
      operation.error = '';
      operation.phase = 'succeeded';
      reconcileSuccessfulPost(operation, post);
    } catch {
      operation.phase = 'failed';
      operation.error = publishFailureMessage;
    } finally {
      runningOperationIDs.delete(operation.id);
    }
  };

  const retry = (operationID: string): boolean => {
    const operation = getOperation(operationID);
    if (
      !operation
      || operation.phase !== 'failed'
      || currentViewerID() !== operation.publisherUserID
    ) {
      return false;
    }
    const conflictingInFlight = operations.value.find(candidate => (
      candidate.id !== operation.id
      && candidate.publisherUserID === operation.publisherUserID
      && isInFlight(candidate)
    ));
    if (conflictingInFlight) {
      return false;
    }
    operation.error = '';
    operation.phase = operation.media.some(media => !media.uploadedURL)
      ? 'uploading'
      : 'publishing';
    void runOperation(operation);
    return true;
  };

  const startOrRetryDraft = (): StartPublishResult => {
    const publisherUserID = currentViewerID();
    if (publisherUserID === null) {
      return { status: 'rejected', reason: 'unauthenticated' };
    }

    const boundOperation = getOperation(postDraft.publishOperationID);
    if (
      boundOperation
      && boundOperation.publisherUserID === publisherUserID
      && draftMatchesOperation(boundOperation)
    ) {
      if (boundOperation.phase === 'failed') {
        if (!retry(boundOperation.id)) {
          return { status: 'blocked', reason: 'another_publish_in_flight' };
        }
        return { status: 'accepted', operation: boundOperation };
      } else if (boundOperation.phase === 'succeeded') {
        // A successful operation normally clears its draft binding. Treat a
        // stale binding as unbound so it cannot be reported as a new submit.
        // The next accepted operation will replace the stale binding.
      } else {
        return { status: 'accepted', operation: boundOperation };
      }
    }

    const existingInFlight = findInFlightForViewer(publisherUserID);
    if (existingInFlight) {
      return { status: 'blocked', reason: 'another_publish_in_flight' };
    }

    const operation: PublishOperation = {
      id: createPublishOperationID(),
      publisherUserID,
      content: postDraft.content.trim(),
      media: postDraft.media.map(media => ({
        draftMediaID: media.id,
        file: media.file,
        uploadedURL: media.uploadedURL.trim(),
      })),
      phase: postDraft.media.some(media => !media.uploadedURL) ? 'uploading' : 'publishing',
      error: '',
      startedAt: Date.now(),
      post: null,
    };
    postDraft.bindPublishOperation(operation.id);
    operations.value.push(operation);
    void runOperation(operation);
    return { status: 'accepted', operation };
  };

  return {
    operations,
    latestOperation,
    getOperation,
    isDraftBlockedByAnotherPublish,
    startOrRetryDraft,
    retry,
  };
});
