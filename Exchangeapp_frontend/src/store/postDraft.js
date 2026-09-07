import { ref } from 'vue';
import { defineStore } from 'pinia';
const normalizeViewerID = (value) => (typeof value === 'number' && Number.isSafeInteger(value) && value > 0
    ? value
    : null);
let nextMediaID = 0;
const createMediaID = () => {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
        return crypto.randomUUID();
    }
    nextMediaID += 1;
    return `post-media-${nextMediaID}`;
};
export const usePostDraftStore = defineStore('postDraft', () => {
    const viewerID = ref(null);
    const content = ref('');
    const media = ref([]);
    const dirty = ref(false);
    const clear = () => {
        content.value = '';
        media.value = [];
        dirty.value = false;
    };
    const setViewer = (nextViewerID) => {
        const normalized = normalizeViewerID(nextViewerID);
        if (normalized === viewerID.value) {
            return false;
        }
        viewerID.value = normalized;
        clear();
        return true;
    };
    const setContent = (value) => {
        content.value = value;
        dirty.value = true;
    };
    const addMedia = (file) => {
        const item = {
            id: createMediaID(),
            file,
            uploadedURL: '',
        };
        media.value = [...media.value, item];
        dirty.value = true;
        return item.id;
    };
    const removeMedia = (id) => {
        const next = media.value.filter(item => item.id !== id);
        if (next.length === media.value.length) {
            return false;
        }
        media.value = next;
        dirty.value = true;
        return true;
    };
    const setUploadedURL = (id, url) => {
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
