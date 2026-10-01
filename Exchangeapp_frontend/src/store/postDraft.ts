import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import { createClientOperationID } from '../utils/clientOperationId';
import {
  deletePostDraft,
  deletePostDraftIfUnchanged,
  getPostDraft,
  listPostDrafts,
  savePostDraft,
  type PersistedPostDraft,
  type PersistedPostDraftMedia,
} from '../storage/postDraftRepository';
import {
  createPostDraftSnapshot,
  isValidQuotePostID,
  postDraftSnapshotsEqual,
  type DraftSnapshot,
  type DraftSnapshotMedia,
} from '../utils/postDraftSnapshot';

export type { DraftSnapshot, DraftSnapshotMedia } from '../utils/postDraftSnapshot';

export type DraftPostMedia = {
  id: string;
  file: File;
  uploadedURL: string;
};

export type LoadSavedDraftResult =
  | { status: 'loaded' }
  | { status: 'not_found' }
  | { status: 'stale' };

const normalizeViewerID = (value: number | null): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0
    ? value
    : null
);

const createSnapshot = (
  content: string,
  media: DraftPostMedia[],
  quotePostID: number | null,
): DraftSnapshot => (
  createPostDraftSnapshot(content, media.map(item => ({
    id: item.id,
    name: item.file.name,
    type: item.file.type,
    size: item.file.size,
    lastModified: item.file.lastModified,
  })), quotePostID)
);

const restoreFile = (item: PersistedPostDraftMedia): File => new File(
  [item.blob],
  item.name,
  { type: item.type, lastModified: item.lastModified },
);

export const usePostDraftStore = defineStore('postDraft', () => {
  const viewerID = ref<number | null>(null);
  const content = ref('');
  const quotePostID = ref<number | null>(null);
  const media = ref<DraftPostMedia[]>([]);
  const draftID = ref<string | null>(null);
  const draftCreatedAt = ref<number | null>(null);
  const savedSnapshot = ref<DraftSnapshot | null>(null);
  const publishOperationID = ref<string | null>(null);
  const hasContent = computed(() => content.value.length > 0 || media.value.length > 0);
  const currentSnapshot = computed(() => createSnapshot(content.value, media.value, quotePostID.value));
  const hasUnsavedChanges = computed(() => {
    if (!savedSnapshot.value) {
      return hasContent.value;
    }
    return !postDraftSnapshotsEqual(currentSnapshot.value, savedSnapshot.value);
  });
  const isSavedDraft = computed(() => draftID.value !== null && savedSnapshot.value !== null);

  let workingStateVersion = 0;
  let loadRequestVersion = 0;

  const clear = () => {
    workingStateVersion += 1;
    loadRequestVersion += 1;
    content.value = '';
    quotePostID.value = null;
    media.value = [];
    draftID.value = null;
    draftCreatedAt.value = null;
    savedSnapshot.value = null;
    publishOperationID.value = null;
  };

  const closeWorkingDraft = () => {
    if (publishOperationID.value !== null) {
      return false;
    }
    clear();
    return true;
  };

  const setViewer = (nextViewerID: number | null) => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }

    viewerID.value = normalized;
    clear();
    return true;
  };

  const setContent = (value: string) => {
    if (content.value === value) {
      return;
    }
    workingStateVersion += 1;
    publishOperationID.value = null;
    content.value = value;
  };

  const setQuotePostID = (postID: number | null) => {
    const normalized = postID === null || isValidQuotePostID(postID) ? postID : null;
    if (quotePostID.value === normalized) {
      return false;
    }
    workingStateVersion += 1;
    publishOperationID.value = null;
    quotePostID.value = normalized;
    return true;
  };

  const addMedia = (file: File) => {
    const item: DraftPostMedia = {
      id: createClientOperationID(),
      file,
      uploadedURL: '',
    };
    media.value = [...media.value, item];
    workingStateVersion += 1;
    publishOperationID.value = null;
    return item.id;
  };

  const removeMedia = (id: string) => {
    const next = media.value.filter(item => item.id !== id);
    if (next.length === media.value.length) {
      return false;
    }
    media.value = next;
    workingStateVersion += 1;
    publishOperationID.value = null;
    return true;
  };

  const setUploadedURL = (id: string, url: string) => {
    const item = media.value.find(candidate => candidate.id === id);
    if (!item) {
      return false;
    }
    item.uploadedURL = url;
    return true;
  };

  const saveCurrentDraft = async (): Promise<string> => {
    const saveViewerID = normalizeViewerID(viewerID.value);
    if (saveViewerID === null) {
      throw new Error('Sign in before saving a draft.');
    }
    if (!hasContent.value) {
      throw new Error('There is no post content or media to save.');
    }

    const stateVersion = workingStateVersion;
    const snapshot = createSnapshot(content.value, media.value, quotePostID.value);
    const id = draftID.value || createClientOperationID();
    const now = Date.now();
    const record: PersistedPostDraft = {
      id,
      viewerID: saveViewerID,
      content: snapshot.content,
      quotePostID: snapshot.quotePostID,
      media: media.value.map(item => ({
        id: item.id,
        blob: item.file.slice(0, item.file.size, item.file.type),
        name: item.file.name,
        type: item.file.type,
        size: item.file.size,
        lastModified: item.file.lastModified,
        uploadedURL: item.uploadedURL,
      })),
      createdAt: draftCreatedAt.value ?? now,
      updatedAt: now,
    };

    await savePostDraft(record);
    if (viewerID.value === saveViewerID && workingStateVersion === stateVersion) {
      draftID.value = id;
      draftCreatedAt.value = record.createdAt;
      savedSnapshot.value = snapshot;
    }
    return id;
  };

  const loadSavedDraft = async (id: string): Promise<LoadSavedDraftResult> => {
    const loadViewerID = normalizeViewerID(viewerID.value);
    if (loadViewerID === null || !id.trim()) {
      return { status: 'not_found' };
    }

    const requestVersion = ++loadRequestVersion;
    const stateVersion = workingStateVersion;
    const record = await getPostDraft(loadViewerID, id);

    if (
      viewerID.value !== loadViewerID
      || loadRequestVersion !== requestVersion
      || workingStateVersion !== stateVersion
    ) {
      return { status: 'stale' };
    }

    if (
      !record
      || record.id !== id
      || record.viewerID !== loadViewerID
      || !Array.isArray(record.media)
    ) {
      return { status: 'not_found' };
    }

    let restoredMedia: DraftPostMedia[];
    try {
      restoredMedia = record.media.map(item => ({
        id: item.id,
        file: restoreFile(item),
        uploadedURL: item.uploadedURL,
      }));
    } catch {
      return { status: 'not_found' };
    }

    workingStateVersion += 1;
    content.value = record.content;
    quotePostID.value = record.quotePostID ?? null;
    media.value = restoredMedia;
    draftID.value = record.id;
    draftCreatedAt.value = record.createdAt;
    savedSnapshot.value = createSnapshot(content.value, media.value, quotePostID.value);
    publishOperationID.value = null;
    return { status: 'loaded' };
  };

  const listSavedDrafts = async (): Promise<PersistedPostDraft[]> => {
    const listViewerID = normalizeViewerID(viewerID.value);
    if (listViewerID === null) {
      return [];
    }
    const records = await listPostDrafts(listViewerID);
    return viewerID.value === listViewerID ? records : [];
  };

  const deleteSavedDraft = async (id: string): Promise<boolean> => {
    const deleteViewerID = normalizeViewerID(viewerID.value);
    if (deleteViewerID === null || publishOperationID.value !== null) {
      return false;
    }
    const removed = await deletePostDraft(deleteViewerID, id);
    if (removed && viewerID.value === deleteViewerID && draftID.value === id) {
      workingStateVersion += 1;
      draftID.value = null;
      draftCreatedAt.value = null;
      savedSnapshot.value = null;
    }
    return removed;
  };

  const resolvePublishedSourceDraft = async (
    sourceViewerID: number,
    id: string,
    expectedSnapshot: DraftSnapshot | null,
    shouldReconcileWorkingState: () => boolean = () => true,
  ): Promise<'deleted' | 'missing' | 'changed'> => {
    const normalizedViewerID = normalizeViewerID(sourceViewerID);
    if (normalizedViewerID === null || !id.trim()) {
      return 'missing';
    }
    if (expectedSnapshot === null) {
      // Legacy operations have no safe version identity, so preserve the draft.
      return 'changed';
    }

    const result = await deletePostDraftIfUnchanged(normalizedViewerID, id, expectedSnapshot);
    if (
      result === 'deleted'
      && shouldReconcileWorkingState()
      && viewerID.value === normalizedViewerID
      && draftID.value === id
      && postDraftSnapshotsEqual(savedSnapshot.value, expectedSnapshot)
    ) {
      workingStateVersion += 1;
      draftID.value = null;
      draftCreatedAt.value = null;
      savedSnapshot.value = null;
    }
    return result;
  };

  const bindPublishOperation = (operationID: string) => {
    const normalized = operationID.trim();
    if (!normalized) {
      return false;
    }
    publishOperationID.value = normalized;
    return true;
  };

  const releasePublishOperationBinding = (
    operationID: string,
    expectedViewerID: number,
  ): boolean => {
    const normalizedViewerID = normalizeViewerID(expectedViewerID);
    if (
      normalizedViewerID === null
      || normalizedViewerID !== viewerID.value
      || publishOperationID.value !== operationID
    ) {
      return false;
    }
    publishOperationID.value = null;
    return true;
  };

  const clearIfBoundTo = (operationID: string, expectedViewerID: number): boolean => {
    const normalizedViewerID = normalizeViewerID(expectedViewerID);
    if (
      publishOperationID.value !== operationID
      || normalizedViewerID === null
      || normalizedViewerID !== viewerID.value
    ) {
      return false;
    }
    clear();
    return true;
  };

  return {
    viewerID,
    content,
    quotePostID,
    media,
    draftID,
    draftCreatedAt,
    savedSnapshot,
    publishOperationID,
    hasContent,
    hasUnsavedChanges,
    isSavedDraft,
    dirty: hasUnsavedChanges,
    clear,
    closeWorkingDraft,
    setViewer,
    setContent,
    setQuotePostID,
    addMedia,
    removeMedia,
    setUploadedURL,
    saveCurrentDraft,
    loadSavedDraft,
    listSavedDrafts,
    deleteSavedDraft,
    resolvePublishedSourceDraft,
    bindPublishOperation,
    releasePublishOperationBinding,
    clearIfBoundTo,
  };
});
