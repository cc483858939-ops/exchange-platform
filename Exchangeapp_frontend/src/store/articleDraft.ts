import { shallowRef, ref } from 'vue';
import { defineStore } from 'pinia';

const normalizeViewerID = (value: number | null): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0
    ? value
    : null
);

export const useArticleDraftStore = defineStore('articleDraft', () => {
  const viewerID = ref<number | null>(null);
  const title = ref('');
  const preview = ref('');
  const content = ref('');
  const coverFile = shallowRef<File | null>(null);
  const uploadedCoverURL = ref('');
  const dirty = ref(false);

  const clear = () => {
    title.value = '';
    preview.value = '';
    content.value = '';
    coverFile.value = null;
    uploadedCoverURL.value = '';
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

  const setTitle = (value: string) => {
    title.value = value;
    dirty.value = true;
  };

  const setPreview = (value: string) => {
    preview.value = value;
    dirty.value = true;
  };

  const setContent = (value: string) => {
    content.value = value;
    dirty.value = true;
  };

  const setCoverFile = (file: File) => {
    coverFile.value = file;
    uploadedCoverURL.value = '';
    dirty.value = true;
  };

  const removeCover = () => {
    coverFile.value = null;
    uploadedCoverURL.value = '';
    dirty.value = true;
  };

  const setUploadedCoverURL = (url: string) => {
    uploadedCoverURL.value = url;
  };

  return {
    viewerID,
    title,
    preview,
    content,
    coverFile,
    uploadedCoverURL,
    dirty,
    clear,
    setViewer,
    setTitle,
    setPreview,
    setContent,
    setCoverFile,
    removeCover,
    setUploadedCoverURL,
  };
});
