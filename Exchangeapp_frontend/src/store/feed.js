import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { applyFeedLikeStateUpdate, applyFeedRepostStateUpdate, postToFeedPost } from '../utils/feedPost';
const maxRecentlyPublishedPosts = 5;
const normalizeViewerID = (value) => {
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
        return null;
    }
    return value;
};
export const useFeedStore = defineStore('feed', () => {
    const authStore = useAuthStore();
    const viewerID = ref(null);
    const recentlyPublishedPosts = ref([]);
    const deletedPostIDs = ref(new Set());
    const clearRecentlyPublishedPosts = () => {
        recentlyPublishedPosts.value = [];
    };
    const clearDeletedPostIDs = () => {
        deletedPostIDs.value = new Set();
    };
    const setViewer = (nextViewerID) => {
        const normalizedViewerID = normalizeViewerID(nextViewerID);
        if (normalizedViewerID === viewerID.value) {
            return;
        }
        viewerID.value = normalizedViewerID;
        clearRecentlyPublishedPosts();
        clearDeletedPostIDs();
    };
    const registerPublishedPost = (post, publisherUserID) => {
        const normalizedPublisherID = normalizeViewerID(publisherUserID);
        if (normalizedPublisherID === null
            || post?.id <= 0
            || !post?.author
            || post.author.id !== normalizedPublisherID
            || viewerID.value !== normalizedPublisherID
            || deletedPostIDs.value.has(post.id)) {
            return false;
        }
        const feedPost = postToFeedPost(post);
        recentlyPublishedPosts.value = [
            feedPost,
            ...recentlyPublishedPosts.value.filter((item) => item.id !== feedPost.id),
        ].slice(0, maxRecentlyPublishedPosts);
        return true;
    };
    const markPostDeleted = (postID, ownerUserID) => {
        const normalizedPostID = normalizeViewerID(postID);
        const normalizedOwnerID = normalizeViewerID(ownerUserID);
        if (normalizedPostID === null
            || normalizedOwnerID === null
            || viewerID.value !== normalizedOwnerID) {
            return false;
        }
        deletedPostIDs.value = new Set([
            ...deletedPostIDs.value,
            normalizedPostID,
        ]);
        recentlyPublishedPosts.value = recentlyPublishedPosts.value.filter((post) => post.id !== normalizedPostID);
        return true;
    };
    const isPostDeleted = (postID) => {
        const normalizedPostID = normalizeViewerID(postID);
        return normalizedPostID !== null && deletedPostIDs.value.has(normalizedPostID);
    };
    const replaceAuthorIdentity = (author) => {
        recentlyPublishedPosts.value = recentlyPublishedPosts.value.map((post) => (post.author.id === author.id || post.repostContext?.actor.id === author.id
            ? {
                ...post,
                author: post.author.id === author.id ? author : post.author,
                repostContext: post.repostContext?.actor.id === author.id
                    ? { actor: author }
                    : post.repostContext,
            }
            : post));
    };
    const applyLikeStateUpdate = (update) => {
        let applied = false;
        recentlyPublishedPosts.value.forEach((post) => {
            applied = applyFeedLikeStateUpdate(post, update) || applied;
        });
        return applied;
    };
    const applyRepostStateUpdate = (update) => {
        let applied = false;
        recentlyPublishedPosts.value.forEach((post) => {
            applied = applyFeedRepostStateUpdate(post, update) || applied;
        });
        return applied;
    };
    watch(() => authStore.currentIdentity?.id, (nextViewerID) => {
        setViewer(nextViewerID ?? null);
    }, { immediate: true });
    return {
        viewerID,
        recentlyPublishedPosts,
        deletedPostIDs,
        maxRecentlyPublishedPosts,
        setViewer,
        registerPublishedPost,
        markPostDeleted,
        isPostDeleted,
        replaceAuthorIdentity,
        applyLikeStateUpdate,
        applyRepostStateUpdate,
        clearRecentlyPublishedPosts,
    };
});
