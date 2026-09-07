import { defineStore } from 'pinia';
import { ref } from 'vue';
const normalizeViewerID = (value) => (typeof value === 'number'
    && Number.isSafeInteger(value)
    && value > 0
    ? value
    : null);
const normalizePostID = (value) => {
    const parsed = typeof value === 'number' ? value : Number(value);
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
};
export const useReplyDraftStore = defineStore('replyDraft', () => {
    const viewerID = ref(null);
    const drafts = ref({});
    const clearAll = () => {
        drafts.value = {};
    };
    const setViewer = (nextViewerID) => {
        const normalized = normalizeViewerID(nextViewerID);
        if (normalized === viewerID.value) {
            return false;
        }
        viewerID.value = normalized;
        clearAll();
        return true;
    };
    const getDraft = (postID) => {
        if (viewerID.value === null) {
            return '';
        }
        const normalized = normalizePostID(postID);
        if (normalized === null) {
            return '';
        }
        return drafts.value[String(normalized)] ?? '';
    };
    const setDraft = (postID, content) => {
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
        drafts.value[String(normalized)] = content;
    };
    const clearDraft = (postID) => {
        if (viewerID.value === null) {
            return;
        }
        const normalized = normalizePostID(postID);
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
