import { defineStore } from 'pinia';
import { ref } from 'vue';
import { createClientOperationID } from '../utils/clientOperationId';

export type ReplySubmissionOperation = {
  id: string;
  content: string;
};

export type PreparedReplySubmission = {
  operation: ReplySubmissionOperation;
  reused: boolean;
};

const normalizeViewerID = (value: number | null): number | null => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
    ? value
    : null
);

const normalizePostID = (value: number | string): number | null => {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
};

export const useReplyDraftStore = defineStore('replyDraft', () => {
  const viewerID = ref<number | null>(null);
  const drafts = ref<Record<string, string>>({});
  const submissionOperations = ref<Record<string, ReplySubmissionOperation>>({});

  const clearAll = () => {
    drafts.value = {};
    submissionOperations.value = {};
  };

  const setViewer = (nextViewerID: number | null): boolean => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }

    viewerID.value = normalized;
    clearAll();
    return true;
  };

  const getDraft = (postID: number | string): string => {
    if (viewerID.value === null) {
      return '';
    }

    const normalized = normalizePostID(postID);
    if (normalized === null) {
      return '';
    }

    return drafts.value[String(normalized)] ?? '';
  };

  const setDraft = (postID: number | string, content: string) => {
    if (viewerID.value === null) {
      return;
    }

    const normalized = normalizePostID(postID);
    if (normalized === null) {
      return;
    }

    if (content === '') {
      clearDraft(normalized);
      return;
    }

    const key = String(normalized);
    const operation = submissionOperations.value[key];
    if (operation && content.trim() !== operation.content) {
      delete submissionOperations.value[key];
    }
    drafts.value[key] = content;
  };

  const clearDraft = (postID: number | string) => {
    if (viewerID.value === null) {
      return;
    }

    const normalized = normalizePostID(postID);
    if (normalized === null) {
      return;
    }

    const key = String(normalized);
    delete drafts.value[key];
    delete submissionOperations.value[key];
  };

  const prepareSubmission = (
    postID: number | string,
    content: string,
  ): PreparedReplySubmission => {
    const normalized = normalizePostID(postID);
    if (viewerID.value === null || normalized === null) {
      throw new Error('Reply submission requires an authenticated viewer and valid post ID');
    }

    const key = String(normalized);
    const canonicalContent = content.trim();
    const existing = submissionOperations.value[key];
    if (existing?.content === canonicalContent) {
      return { operation: existing, reused: true };
    }

    const operation = {
      id: createClientOperationID(),
      content: canonicalContent,
    };
    submissionOperations.value[key] = operation;
    return { operation, reused: false };
  };

  const clearSubmissionOperation = (
    postID: number | string,
    operationID?: string,
  ) => {
    if (viewerID.value === null) {
      return;
    }

    const normalized = normalizePostID(postID);
    if (normalized === null) {
      return;
    }

    const key = String(normalized);
    const existing = submissionOperations.value[key];
    if (!existing || (operationID !== undefined && existing.id !== operationID)) {
      return;
    }
    delete submissionOperations.value[key];
  };

  return {
    viewerID,
    drafts,
    submissionOperations,
    setViewer,
    getDraft,
    setDraft,
    clearDraft,
    prepareSubmission,
    clearSubmissionOperation,
    clearAll,
  };
});
