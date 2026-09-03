import { ref } from 'vue';
import { defineStore } from 'pinia';

export type DraftPostMedia = {
  id: string;
  file: File;
  uploadedURL: string;
};

const normalizeViewerID = (value: number | null): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0
    ? value
    : null
);

let nextMediaID = 0;

const createMediaID = () => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  nextMediaID += 1;
  return `post-media-${nextMediaID}`;
};

export const usePostDraftStore = defineStore('postDraft', () => {
  const viewerID = ref<number | null>(null);
  const content = ref('');
  const media = ref<DraftPostMedia[]>([]);
  const dirty = ref(false);

  const clear = () => {
    content.value = '';
    media.value = [];
    dirty.value = false;
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
    content.value = value;
    dirty.value = true;
  };

  const addMedia = (file: File) => {
    const item: DraftPostMedia = {
      id: createMediaID(),
      file,
      uploadedURL: '',
    };
    media.value = [...media.value, item];
    dirty.value = true;
    return item.id;
  };

  const removeMedia = (id: string) => {
    const next = media.value.filter(item => item.id !== id);
    if (next.length === media.value.length) {
      return false;
    }
    media.value = next;
    dirty.value = true;
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

  return {
    viewerID,
    content,
    media,
    dirty,
    clear,
    setViewer,
    setContent,
    addMedia,
    removeMedia,
    setUploadedURL,
  };
});
