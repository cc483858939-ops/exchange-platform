import { defineStore } from 'pinia';
import { ref } from 'vue';

const normalizeViewerID = (value: number | null): number | null => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
    ? value
    : null
);

const normalizeArticleID = (value: number | string): number | null => {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
};

export const useReplyDraftStore = defineStore('replyDraft', () => {
  const viewerID = ref<number | null>(null);
  const drafts = ref<Record<string, string>>({});

  const clearAll = () => {
    drafts.value = {};
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

  const getDraft = (articleID: number | string): string => {
    if (viewerID.value === null) {
      return '';
    }

    const normalized = normalizeArticleID(articleID);
    if (normalized === null) {
      return '';
    }

    return drafts.value[String(normalized)] ?? '';
  };

  const setDraft = (articleID: number | string, content: string) => {
    if (viewerID.value === null) {
      return;
    }

    const normalized = normalizeArticleID(articleID);
    if (normalized === null) {
      return;
    }

    if (content === '') {
      clearDraft(normalized);
      return;
    }

    drafts.value[String(normalized)] = content;
  };

  const clearDraft = (articleID: number | string) => {
    if (viewerID.value === null) {
      return;
    }

    const normalized = normalizeArticleID(articleID);
    if (normalized === null) {
      return;
    }

    delete drafts.value[String(normalized)];
  };

  return {
    viewerID,
    drafts,
    setViewer,
    getDraft,
    setDraft,
    clearDraft,
    clearAll,
  };
});
