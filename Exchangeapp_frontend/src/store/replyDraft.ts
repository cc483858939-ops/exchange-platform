import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  deleteReplyDraftIfUnchanged,
  getReplyDraft,
  saveReplyDraft,
} from '../storage/replyStorage';

const normalizeViewerID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

const normalizePostID = (value: number | string): number | null => {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
};

const keyFor = (postID: number) => String(postID);

export type ReplyDraftHydrationResult = 'loaded' | 'empty' | 'stale';
export type SaveReplyDraftResult = 'saved' | 'deleted' | 'changed';

export const useReplyDraftStore = defineStore('replyDraft', () => {
  const viewerID = ref<number | null>(null);
  const draftsByPost = ref<Record<string, string>>({});
  const savedContentByPost = ref<Record<string, string | null>>({});
  const createdAtByPost = ref<Record<string, number | null>>({});
  const boundOperationIDByPost = ref<Record<string, string>>({});
  const boundOperationContentByPost = ref<Record<string, string>>({});

  const hasAnyUnsavedChanges = computed(() => Object.keys(draftsByPost.value).some(postID => (
    hasUnsavedChanges(postID)
  )));

  const workingRevisions = new Map<string, number>();
  const hydrationRevisions = new Map<string, number>();
  const savedWriteRevisions = new Map<string, number>();
  let saveSequence = 0;

  const bumpWorkingRevision = (key: string) => {
    const next = (workingRevisions.get(key) ?? 0) + 1;
    workingRevisions.set(key, next);
    return next;
  };

  const clearInMemory = () => {
    draftsByPost.value = {};
    savedContentByPost.value = {};
    createdAtByPost.value = {};
    boundOperationIDByPost.value = {};
    boundOperationContentByPost.value = {};
    workingRevisions.clear();
    hydrationRevisions.clear();
    savedWriteRevisions.clear();
  };

  const setViewer = (nextViewerID: number | null): boolean => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) return false;
    viewerID.value = normalized;
    clearInMemory();
    return true;
  };

  const getDraft = (postID: number | string): string => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return '';
    return draftsByPost.value[keyFor(normalized)] ?? '';
  };

  const getSavedContent = (postID: number | string): string | null | undefined => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return undefined;
    return savedContentByPost.value[keyFor(normalized)];
  };

  const hasSavedDraft = (postID: number | string) => getSavedContent(postID) !== undefined
    && getSavedContent(postID) !== null;

  const getBoundOperationID = (postID: number | string): string | null => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return null;
    return boundOperationIDByPost.value[keyFor(normalized)] ?? null;
  };

  const hasUnsavedChanges = (postID: number | string): boolean => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    const content = draftsByPost.value[key] ?? '';
    const boundContent = boundOperationContentByPost.value[key];
    if (boundOperationIDByPost.value[key] && boundContent !== undefined
      && content.trim() === boundContent) return false;
    const saved = savedContentByPost.value[key];
    if (saved === undefined || saved === null) return content.length > 0;
    return content !== saved;
  };

  const setDraft = (postID: number | string, content: string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return;
    const key = keyFor(normalized);
    const current = draftsByPost.value[key] ?? '';
    if (current === content) return;
    bumpWorkingRevision(key);
    const boundContent = boundOperationContentByPost.value[key];
    if (boundContent !== undefined && content.trim() !== boundContent) {
      delete boundOperationIDByPost.value[key];
      delete boundOperationContentByPost.value[key];
    }
    draftsByPost.value[key] = content;
  };

  const captureWorkingRevision = (postID: number | string): number | null => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return null;
    return workingRevisions.get(keyFor(normalized)) ?? 0;
  };

  const hydrateSavedDraft = async (postID: number | string): Promise<ReplyDraftHydrationResult> => {
    const normalized = normalizePostID(postID);
    const loadViewerID = viewerID.value;
    if (loadViewerID === null || normalized === null) return 'empty';
    const key = keyFor(normalized);
    const requestRevision = (hydrationRevisions.get(key) ?? 0) + 1;
    hydrationRevisions.set(key, requestRevision);
    const workingRevision = workingRevisions.get(key) ?? 0;
    const record = await getReplyDraft(loadViewerID, normalized);
    if (
      viewerID.value !== loadViewerID
      || hydrationRevisions.get(key) !== requestRevision
      || (workingRevisions.get(key) ?? 0) !== workingRevision
    ) return 'stale';
    savedContentByPost.value[key] = record?.content ?? null;
    createdAtByPost.value[key] = record?.createdAt ?? null;
    draftsByPost.value[key] = record?.content ?? '';
    delete boundOperationIDByPost.value[key];
    delete boundOperationContentByPost.value[key];
    bumpWorkingRevision(key);
    return record ? 'loaded' : 'empty';
  };

  const saveDraft = async (postID: number | string): Promise<SaveReplyDraftResult> => {
    const normalized = normalizePostID(postID);
    const saveViewerID = viewerID.value;
    if (saveViewerID === null || normalized === null) throw new Error('Sign in before saving a reply draft.');
    const key = keyFor(normalized);
    const content = draftsByPost.value[key] ?? '';
    const baseline = savedContentByPost.value[key] ?? null;
    const writeRevision = ++saveSequence;
    savedWriteRevisions.set(key, writeRevision);
    const now = Date.now();

    if (content.length === 0) {
      if (baseline === null) {
        savedContentByPost.value[key] = null;
        createdAtByPost.value[key] = null;
        return 'deleted';
      }
      const result = await deleteReplyDraftIfUnchanged(saveViewerID, normalized, baseline);
      if (viewerID.value !== saveViewerID || savedWriteRevisions.get(key) !== writeRevision) return 'changed';
      if (result === 'deleted' || result === 'missing') {
        savedContentByPost.value[key] = null;
        createdAtByPost.value[key] = null;
        return 'deleted';
      }
      const latest = await getReplyDraft(saveViewerID, normalized);
      if (viewerID.value === saveViewerID && savedWriteRevisions.get(key) === writeRevision) {
        savedContentByPost.value[key] = latest?.content ?? null;
        createdAtByPost.value[key] = latest?.createdAt ?? null;
      }
      return 'changed';
    }

    await saveReplyDraft({
      key: `${saveViewerID}:${normalized}`,
      viewerID: saveViewerID,
      parentPostID: normalized,
      content,
      createdAt: createdAtByPost.value[key] ?? now,
      updatedAt: now,
    });
    if (viewerID.value === saveViewerID && savedWriteRevisions.get(key) === writeRevision) {
      savedContentByPost.value[key] = content;
      createdAtByPost.value[key] ??= now;
    }
    return 'saved';
  };

  const discardChanges = (postID: number | string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    const saved = savedContentByPost.value[key] ?? '';
    bumpWorkingRevision(key);
    const boundContent = boundOperationContentByPost.value[key];
    if (boundContent !== undefined && saved.trim() !== boundContent) {
      delete boundOperationIDByPost.value[key];
      delete boundOperationContentByPost.value[key];
    }
    draftsByPost.value[key] = saved;
    return true;
  };

  const bindSubmission = (postID: number | string, operationID: string, canonicalContent: string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null || !operationID.trim()) return false;
    const key = keyFor(normalized);
    if ((draftsByPost.value[key] ?? '').trim() !== canonicalContent) return false;
    boundOperationIDByPost.value[key] = operationID;
    boundOperationContentByPost.value[key] = canonicalContent;
    return true;
  };

  const adoptHydratedSubmission = (
    postID: number | string,
    operation: { id: string; content: string; sourceDraftContent: string | null },
    expectedWorkingRevision: number,
  ) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    if ((workingRevisions.get(key) ?? 0) !== expectedWorkingRevision) return false;
    const saved = savedContentByPost.value[key] ?? null;
    const current = draftsByPost.value[key] ?? '';
    if (saved !== null) {
      if (saved === operation.sourceDraftContent && saved.trim() === operation.content) {
        return bindSubmission(normalized, operation.id, operation.content);
      }
      return false;
    }
    if (current && current.trim() !== operation.content) return false;
    if (!current) {
      draftsByPost.value[key] = operation.content;
      bumpWorkingRevision(key);
    }
    return bindSubmission(normalized, operation.id, operation.content);
  };

  const finishBoundSubmission = (postID: number | string, operationID: string, canonicalContent: string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    if (
      boundOperationIDByPost.value[key] !== operationID
      || boundOperationContentByPost.value[key] !== canonicalContent
      || (draftsByPost.value[key] ?? '').trim() !== canonicalContent
    ) return false;
    delete boundOperationIDByPost.value[key];
    delete boundOperationContentByPost.value[key];
    bumpWorkingRevision(key);
    draftsByPost.value[key] = savedContentByPost.value[key] ?? '';
    return true;
  };

  const markSourceDraftDeleted = (postID: number | string, expectedContent: string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    if (savedContentByPost.value[key] !== expectedContent) return false;
    savedContentByPost.value[key] = null;
    createdAtByPost.value[key] = null;
    return true;
  };

  const refreshSavedBaseline = (
    postID: number | string,
    latestContent: string | null,
    previousContent: string,
    latestCreatedAt: number | null = null,
  ) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    const currentSaved = savedContentByPost.value[key];
    if (currentSaved !== previousContent && currentSaved !== latestContent) return false;
    const workingWasSaved = (draftsByPost.value[key] ?? '') === currentSaved;
    savedContentByPost.value[key] = latestContent;
    createdAtByPost.value[key] = latestContent === null ? null : latestCreatedAt;
    if (workingWasSaved) {
      bumpWorkingRevision(key);
      draftsByPost.value[key] = latestContent ?? '';
    }
    return true;
  };

  const clearSubmissionBinding = (postID: number | string, operationID: string) => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) return false;
    const key = keyFor(normalized);
    if (boundOperationIDByPost.value[key] !== operationID) return false;
    delete boundOperationIDByPost.value[key];
    delete boundOperationContentByPost.value[key];
    return true;
  };

  return {
    viewerID,
    draftsByPost,
    savedContentByPost,
    boundOperationIDByPost,
    hasAnyUnsavedChanges,
    setViewer,
    getDraft,
    getSavedContent,
    hasSavedDraft,
    getBoundOperationID,
    hasUnsavedChanges,
    setDraft,
    captureWorkingRevision,
    hydrateSavedDraft,
    saveDraft,
    discardChanges,
    bindSubmission,
    adoptHydratedSubmission,
    finishBoundSubmission,
    markSourceDraftDeleted,
    refreshSavedBaseline,
    clearSubmissionBinding,
  };
});
