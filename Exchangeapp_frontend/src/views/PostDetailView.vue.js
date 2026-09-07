/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AuthorIdentity from '../components/AuthorIdentity.vue';
import LinkifiedText from '../components/content/LinkifiedText.vue';
import PostMediaGrid from '../components/content/PostMediaGrid.vue';
import PostMediaViewer from '../components/content/PostMediaViewer.vue';
import ConfirmDialog from '../components/dialogs/ConfirmDialog.vue';
import LikeAction from '../components/engagement/LikeAction.vue';
import RepostAction from '../components/engagement/RepostAction.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import ReplyComposer from '../components/replies/ReplyComposer.vue';
import ReplyList from '../components/replies/ReplyList.vue';
import { createPostReply, deletePostReply, getPostReplies } from '../services/replyService';
import { deletePost, getPostById } from '../services/postService';
import { getPostLikeState, likePost, unlikePost } from '../services/likeService';
import { getPostRepostState, repostPost, undoRepostPost } from '../services/repostService';
import { consumePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { createPostViewEventID, getPostViewTelemetry } from '../services/postViewTelemetry';
import { PostReadTracker, createPostReadGeometry } from '../services/postReadTracker';
import { useAuthStore } from '../store/auth';
import { usePostDetailHandoffStore } from '../store/postDetailHandoff';
import { useFeedStore } from '../store/feed';
import { useReplyDraftStore } from '../store/replyDraft';
import { syncExternalPostLikeState, syncExternalPostRepostState, syncExternalPostRemoval, syncExternalReplyCount, } from '../store/sessionSync';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../utils/engagementCount';
import { formatPostDetailTimestamp } from '../utils/time';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const postDetailHandoff = usePostDetailHandoffStore();
const feedStore = useFeedStore();
const replyDraftStore = useReplyDraftStore();
const currentIdentity = computed(() => authStore.currentIdentity);
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);
const postId = computed(() => String(route.params.id ?? '').trim());
const post = ref(null);
const postLoading = ref(false);
const postError = ref('');
const handoffPost = ref(null);
const deletePending = ref(false);
const deleteError = ref('');
const deletePostConfirmOpen = ref(false);
const deletePostButtonRef = ref(null);
const postBodyRef = ref(null);
const liked = ref(false);
const likeCount = ref(0);
const likeStateLoading = ref(false);
const likeSubmitting = ref(false);
const likeError = ref('');
const reposted = ref(false);
const repostCount = ref(0);
const repostStateLoading = ref(false);
const repostSubmitting = ref(false);
const repostError = ref('');
const repostStateUnavailable = ref(false);
const replies = ref([]);
const nextCursor = ref(null);
const repliesInitialLoading = ref(false);
const repliesLoadingMore = ref(false);
const repliesError = ref('');
const repliesLoadMoreError = ref('');
const replySubmitting = ref(false);
const replyError = ref('');
const deletingReplyId = ref(null);
const deleteReplyCandidateId = ref(null);
const replyDeleteError = ref('');
const composerRef = ref(null);
const replyCount = ref(0);
const viewCount = ref(0);
const mediaViewer = ref(null);
let detailRequestVersion = 0;
let deleteRequestVersion = 0;
let likeRequestVersion = 0;
let likeMutationVersion = 0;
let repostRequestVersion = 0;
let repostMutationVersion = 0;
let repliesRequestVersion = 0;
let replyDeleteRequestVersion = 0;
let replyIntentTask = null;
let replyIntentRetryRequested = false;
let tracking = null;
let trackedPostID = '';
let readEndSent = false;
let readTracker = null;
let readResizeObserver = null;
const clampCount = (value) => {
    const count = Number(value);
    return Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
};
const isValidPostID = (value) => {
    const parsed = Number(value);
    return /^\d+$/.test(value) && Number.isSafeInteger(parsed) && parsed > 0;
};
const detailPresentation = computed(() => {
    if (post.value) {
        return {
            kind: 'post',
            author: post.value.author,
            body: post.value.content,
            media: post.value.media,
            createdAt: post.value.published_at || post.value.created_at,
            likeCount: likeCount.value,
            repostCount: repostCount.value,
            reposted: reposted.value,
            replyCount: replyCount.value,
            viewCount: viewCount.value,
        };
    }
    if (postLoading.value && handoffPost.value) {
        return {
            kind: 'warm',
            author: handoffPost.value.author,
            body: handoffPost.value.content,
            media: handoffPost.value.media,
            createdAt: handoffPost.value.createdAt,
            likeCount: handoffPost.value.likeCount,
            repostCount: handoffPost.value.repostCount,
            reposted: handoffPost.value.reposted,
            replyCount: handoffPost.value.replyCount,
            viewCount: handoffPost.value.viewCount,
        };
    }
    return null;
});
const detailReference = computed(() => (post.value?.quote_post ?? post.value?.reply_to_post ?? null));
const detailReferenceDestination = computed(() => {
    const reference = detailReference.value;
    if (!reference || reference.deleted) {
        return undefined;
    }
    return {
        name: 'PostDetail',
        params: { id: String(reference.id) },
    };
});
const detailReferenceLabel = computed(() => (post.value?.quote_post ? 'Quoted post' : 'Replying to'));
const detailReferenceContent = computed(() => {
    const reference = detailReference.value;
    if (!reference || reference.deleted) {
        return '';
    }
    return reference.content?.trim()
        || 'Post';
});
const detailReferenceAuthor = computed(() => {
    const reference = detailReference.value;
    return reference && !reference.deleted ? reference.author : null;
});
const detailReferenceMedia = computed(() => {
    const reference = detailReference.value;
    return reference && !reference.deleted ? reference.media : [];
});
const detailReferenceMessage = 'Post unavailable';
const openMediaViewer = (media, index) => {
    const visibleMedia = media.slice(0, 4);
    if (visibleMedia.length === 0) {
        return;
    }
    mediaViewer.value = {
        media,
        index: Math.min(Math.max(Math.trunc(index), 0), visibleMedia.length - 1),
    };
};
const closeMediaViewer = () => {
    mediaViewer.value = null;
};
const presentationLikeLabel = computed(() => {
    const count = detailPresentation.value?.likeCount ?? 0;
    return String(count) + (count === 1 ? ' like' : ' likes');
});
const presentationReplyLabel = computed(() => {
    const count = detailPresentation.value?.replyCount ?? 0;
    return String(count) + (count === 1 ? ' reply' : ' replies');
});
const presentationRepostLabel = computed(() => {
    const count = detailPresentation.value?.repostCount ?? 0;
    return String(count) + (count === 1 ? ' repost' : ' reposts');
});
const postFailureTitle = computed(() => {
    if (!authStore.isAuthenticated) {
        return 'Log in to view this post';
    }
    return 'Post unavailable';
});
const postFailureMessage = computed(() => {
    if (!authStore.isAuthenticated) {
        return 'Sign in to open this post and join the conversation.';
    }
    return postError.value || 'The post could not be loaded.';
});
const currentViewerID = computed(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null;
});
const replyComposerAuthor = computed(() => {
    const identity = currentIdentity.value;
    const viewerID = currentViewerID.value;
    if (!identity || viewerID === null) {
        return null;
    }
    return {
        id: viewerID,
        username: identity.username,
        display_name: identity.display_name,
        avatar_url: identity.avatar_url,
    };
});
const replyDraftContent = computed({
    get: () => replyDraftStore.getDraft(Number(postId.value)),
    set: value => replyDraftStore.setDraft(Number(postId.value), value),
});
const focusReplyComposer = async () => {
    if (!post.value || !authStore.isAuthenticated) {
        return;
    }
    await composerRef.value?.focus();
};
const postViewTelemetry = getPostViewTelemetry();
const detailPostTimestamp = computed(() => (formatPostDetailTimestamp(detailPresentation.value?.createdAt)));
const formattedViews = computed(() => (formatCompactEngagementCount(detailPresentation.value?.viewCount ?? 0)));
const postViewsLabel = computed(() => (formatAccessibleEngagementCount(detailPresentation.value?.viewCount ?? 0, 'views')));
const detailReplyLabel = computed(() => {
    const count = replyCount.value;
    return 'Reply to post, ' + count + (count === 1 ? ' reply' : ' replies');
});
const detailLikeLabel = computed(() => {
    const count = String(likeCount.value)
        + (likeCount.value === 1 ? ' like' : ' likes');
    return liked.value
        ? 'Unlike post, ' + count
        : 'Like post, ' + count;
});
const detailRepostLabel = computed(() => {
    const count = String(repostCount.value)
        + (repostCount.value === 1 ? ' repost' : ' reposts');
    return reposted.value
        ? 'Undo repost, ' + count
        : 'Repost post, ' + count;
});
const canDeletePost = computed(() => Boolean(post.value
    && authStore.isAuthenticated
    && currentViewerID.value !== null
    && post.value.author.id === currentViewerID.value));
const mergeReplies = (items) => {
    const seen = new Set();
    return items.filter(reply => {
        if (seen.has(reply.id)) {
            return false;
        }
        seen.add(reply.id);
        return true;
    });
};
const disconnectReadGeometryObserver = () => {
    readResizeObserver?.disconnect();
    readResizeObserver = null;
};
const getCurrentPostReadGeometry = () => {
    const element = postBodyRef.value;
    if (!element) {
        return null;
    }
    const rect = element.getBoundingClientRect();
    return {
        postTopDoc: window.scrollY + rect.top,
        postHeight: Math.max(rect.height, 1),
        currentViewportBottomDoc: window.scrollY + window.innerHeight,
    };
};
const updateReadGeometry = () => {
    const geometry = getCurrentPostReadGeometry();
    if (geometry) {
        readTracker?.updateGeometry(geometry);
    }
};
const handleReadScroll = () => {
    if (readTracker) {
        readTracker.recordScroll(window.scrollY + window.innerHeight);
    }
};
const finishRead = (exitType) => {
    if (!tracking || readEndSent || !trackedPostID || !readTracker) {
        return false;
    }
    const payload = readTracker.finish(exitType);
    if (!payload) {
        return false;
    }
    readEndSent = true;
    return recommendationTelemetry.recordReadEnd(Number(trackedPostID), tracking, payload);
};
const handleVisibilityChange = () => {
    if (document.visibilityState === 'hidden') {
        readTracker?.pause();
        void recommendationTelemetry.flush(true);
    }
    else if (tracking && !readEndSent) {
        readTracker?.resume();
    }
};
const handlePageHide = () => {
    finishRead('page_hide');
    void recommendationTelemetry.flush(true);
};
const startRead = (id, detailVersion) => {
    disconnectReadGeometryObserver();
    readTracker = null;
    tracking = null;
    trackedPostID = id;
    readEndSent = false;
    if (detailVersion !== detailRequestVersion
        || postId.value !== id
        || post.value?.id !== Number(id)) {
        return;
    }
    const element = postBodyRef.value;
    if (!element) {
        return;
    }
    tracking = consumePendingRecommendationAttribution(Number(id));
    if (!tracking) {
        return;
    }
    const rect = element.getBoundingClientRect();
    readTracker = new PostReadTracker();
    readTracker.start(createPostReadGeometry({ top: rect.top, height: rect.height }, window.scrollY, window.innerHeight), document.visibilityState === 'visible');
    if (typeof ResizeObserver !== 'undefined') {
        readResizeObserver = new ResizeObserver(updateReadGeometry);
        readResizeObserver.observe(element);
    }
};
const resetLikeState = () => {
    likeRequestVersion += 1;
    likeMutationVersion += 1;
    liked.value = false;
    likeCount.value = 0;
    likeStateLoading.value = false;
    likeSubmitting.value = false;
    likeError.value = '';
};
const resetRepostState = () => {
    repostRequestVersion += 1;
    repostMutationVersion += 1;
    reposted.value = false;
    repostCount.value = 0;
    repostStateLoading.value = false;
    repostSubmitting.value = false;
    repostError.value = '';
    repostStateUnavailable.value = false;
};
const resetRepliesState = () => {
    repliesRequestVersion += 1;
    replyDeleteRequestVersion += 1;
    replies.value = [];
    nextCursor.value = null;
    repliesInitialLoading.value = false;
    repliesLoadingMore.value = false;
    repliesError.value = '';
    repliesLoadMoreError.value = '';
    replySubmitting.value = false;
    replyError.value = '';
    deletingReplyId.value = null;
    deleteReplyCandidateId.value = null;
    replyDeleteError.value = '';
    replyCount.value = 0;
};
const resetPostState = () => {
    deleteRequestVersion += 1;
    post.value = null;
    postLoading.value = false;
    postError.value = '';
    viewCount.value = 0;
    deletePending.value = false;
    deleteError.value = '';
    deletePostConfirmOpen.value = false;
};
const getErrorStatus = (error) => error.response?.status;
const requestDeletePost = () => {
    if (!post.value
        || currentViewerID.value === null
        || !canDeletePost.value
        || deletePending.value) {
        return;
    }
    deleteError.value = '';
    deletePostConfirmOpen.value = true;
};
const cancelDeletePost = () => {
    if (deletePending.value) {
        return;
    }
    deletePostConfirmOpen.value = false;
    deleteError.value = '';
    void nextTick(() => deletePostButtonRef.value?.focus());
};
const confirmDeletePost = async () => {
    const currentPost = post.value;
    const viewerID = currentViewerID.value;
    if (!currentPost
        || viewerID === null
        || !canDeletePost.value
        || deletePending.value) {
        return;
    }
    const detailVersion = detailRequestVersion;
    const requestVersion = ++deleteRequestVersion;
    const postID = currentPost.id;
    deletePending.value = true;
    deleteError.value = '';
    const isCurrentDelete = () => requestVersion === deleteRequestVersion
        && detailVersion === detailRequestVersion
        && authStore.isAuthenticated
        && currentViewerID.value === viewerID
        && feedStore.viewerID === viewerID
        && post.value?.id === postID;
    const finishTerminalDelete = () => {
        if (!isCurrentDelete() || !feedStore.markPostDeleted(postID, viewerID)) {
            return false;
        }
        syncExternalPostRemoval(postID);
        replyDraftStore.clearDraft(postID);
        finishRead('route_leave');
        void recommendationTelemetry.flush(false);
        deletePostConfirmOpen.value = false;
        deletePending.value = false;
        deleteError.value = '';
        void router.replace({
            name: 'UserProfile',
            params: { id: String(viewerID) },
        });
        return true;
    };
    try {
        await deletePost(postID);
        finishTerminalDelete();
    }
    catch (error) {
        if (!isCurrentDelete()) {
            return;
        }
        const status = getErrorStatus(error);
        if (status === 404) {
            finishTerminalDelete();
            return;
        }
        deleteError.value = status === 403
            ? 'You can only delete your own posts.'
            : status === 401
                ? 'Please log in again to delete this post.'
                : 'Could not delete post. Please try again.';
        deletePending.value = false;
    }
    finally {
        if (requestVersion === deleteRequestVersion && detailVersion === detailRequestVersion) {
            deletePending.value = false;
        }
    }
};
const loadLikeState = async (id, detailVersion) => {
    if (!authStore.isAuthenticated) {
        return;
    }
    const requestVersion = ++likeRequestVersion;
    const mutationVersionAtStart = likeMutationVersion;
    likeStateLoading.value = true;
    likeError.value = '';
    try {
        const response = await getPostLikeState(id);
        if (detailVersion !== detailRequestVersion ||
            requestVersion !== likeRequestVersion ||
            mutationVersionAtStart !== likeMutationVersion) {
            return;
        }
        liked.value = response.liked;
        likeCount.value = clampCount(response.likes);
    }
    catch {
        if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
            likeError.value = 'Like status is unavailable. You can still try again.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
            likeStateLoading.value = false;
        }
    }
};
const loadRepostState = async (id, detailVersion) => {
    if (!authStore.isAuthenticated) {
        return;
    }
    const requestVersion = ++repostRequestVersion;
    const mutationVersionAtStart = repostMutationVersion;
    repostStateLoading.value = true;
    repostError.value = '';
    repostStateUnavailable.value = false;
    try {
        const response = await getPostRepostState(id);
        if (detailVersion !== detailRequestVersion
            || requestVersion !== repostRequestVersion
            || mutationVersionAtStart !== repostMutationVersion) {
            return;
        }
        reposted.value = response.reposted;
        repostCount.value = clampCount(response.reposts);
        repostStateUnavailable.value = false;
    }
    catch {
        if (detailVersion === detailRequestVersion
            && requestVersion === repostRequestVersion
            && mutationVersionAtStart === repostMutationVersion) {
            repostError.value = 'Repost status is unavailable. You can still try again.';
            repostStateUnavailable.value = true;
        }
    }
    finally {
        if (detailVersion === detailRequestVersion
            && requestVersion === repostRequestVersion
            && mutationVersionAtStart === repostMutationVersion) {
            repostStateLoading.value = false;
        }
    }
};
const toggleLike = async () => {
    if (!post.value ||
        !authStore.isAuthenticated ||
        likeStateLoading.value ||
        likeSubmitting.value) {
        return;
    }
    const detailVersion = detailRequestVersion;
    const id = postId.value;
    const mutationVersion = ++likeMutationVersion;
    const previousLiked = liked.value;
    const previousCount = likeCount.value;
    likeSubmitting.value = true;
    likeError.value = '';
    liked.value = !previousLiked;
    likeCount.value = Math.max(0, previousCount + (liked.value ? 1 : -1));
    try {
        const response = previousLiked ? await unlikePost(id) : await likePost(id);
        if (detailVersion !== detailRequestVersion || mutationVersion !== likeMutationVersion) {
            return;
        }
        liked.value = response.liked;
        likeCount.value = clampCount(response.likes);
        syncExternalPostLikeState({
            postId: Number(id),
            likes: likeCount.value,
            liked: response.liked,
            status: 'ready',
        });
    }
    catch {
        if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
            liked.value = previousLiked;
            likeCount.value = Math.max(0, previousCount);
            likeError.value = 'Like failed. Please try again.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
            likeSubmitting.value = false;
        }
    }
};
const toggleRepost = async () => {
    if (!post.value
        || !authStore.isAuthenticated
        || repostStateLoading.value
        || repostSubmitting.value
        || repostStateUnavailable.value) {
        return;
    }
    const detailVersion = detailRequestVersion;
    const id = postId.value;
    const mutationVersion = ++repostMutationVersion;
    const previousReposted = reposted.value;
    const previousCount = repostCount.value;
    repostSubmitting.value = true;
    repostError.value = '';
    repostStateUnavailable.value = false;
    reposted.value = !previousReposted;
    repostCount.value = Math.max(0, previousCount + (reposted.value ? 1 : -1));
    try {
        const response = previousReposted ? await undoRepostPost(id) : await repostPost(id);
        if (detailVersion !== detailRequestVersion || mutationVersion !== repostMutationVersion) {
            return;
        }
        reposted.value = response.reposted;
        repostCount.value = clampCount(response.reposts);
        syncExternalPostRepostState({
            postId: Number(id),
            reposts: repostCount.value,
            reposted: response.reposted,
            status: 'ready',
        });
    }
    catch {
        if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
            reposted.value = previousReposted;
            repostCount.value = Math.max(0, previousCount);
            repostError.value = 'Could not update repost. Please try again.';
            repostStateUnavailable.value = false;
        }
    }
    finally {
        if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
            repostSubmitting.value = false;
        }
    }
};
const loadInitialReplies = async (id, detailVersion) => {
    const requestVersion = ++repliesRequestVersion;
    repliesInitialLoading.value = true;
    repliesLoadingMore.value = false;
    repliesError.value = '';
    repliesLoadMoreError.value = '';
    replies.value = [];
    nextCursor.value = null;
    try {
        const page = await getPostReplies(id, { limit: 20 });
        if (detailVersion !== detailRequestVersion || requestVersion !== repliesRequestVersion) {
            return;
        }
        replies.value = mergeReplies(replies.value.concat(page.items));
        nextCursor.value = page.next_cursor || null;
    }
    catch {
        if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
            repliesError.value = 'The replies could not be loaded.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
            repliesInitialLoading.value = false;
        }
    }
};
const loadMoreReplies = async () => {
    if (!nextCursor.value ||
        repliesInitialLoading.value ||
        repliesLoadingMore.value ||
        !post.value) {
        return;
    }
    const id = postId.value;
    const detailVersion = detailRequestVersion;
    const requestVersion = ++repliesRequestVersion;
    const cursor = nextCursor.value;
    repliesLoadingMore.value = true;
    repliesLoadMoreError.value = '';
    try {
        const page = await getPostReplies(id, { limit: 20, cursor });
        if (detailVersion !== detailRequestVersion || requestVersion !== repliesRequestVersion) {
            return;
        }
        replies.value = mergeReplies(replies.value.concat(page.items));
        nextCursor.value = page.next_cursor || null;
    }
    catch {
        if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
            repliesLoadMoreError.value = 'Could not load more replies.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
            repliesLoadingMore.value = false;
        }
    }
};
const retryInitialReplies = () => {
    if (post.value) {
        void loadInitialReplies(postId.value, detailRequestVersion);
    }
};
const retryLoadMoreReplies = () => {
    void loadMoreReplies();
};
const handleCreateReply = async (content) => {
    if (!post.value || !authStore.isAuthenticated || replySubmitting.value) {
        return;
    }
    const id = postId.value;
    const numericPostID = Number(id);
    const submittingViewerID = currentViewerID.value;
    const submittedDraftSnapshot = replyDraftStore.getDraft(numericPostID);
    const detailVersion = detailRequestVersion;
    replySubmitting.value = true;
    replyError.value = '';
    try {
        const created = await createPostReply(id, content);
        if (replyDraftStore.viewerID === submittingViewerID
            && replyDraftStore.getDraft(numericPostID) === submittedDraftSnapshot) {
            replyDraftStore.clearDraft(numericPostID);
        }
        if (detailVersion !== detailRequestVersion || postId.value !== id) {
            return;
        }
        replies.value = mergeReplies([created].concat(replies.value));
        repliesError.value = '';
        replyCount.value = clampCount(replyCount.value + 1);
        syncExternalReplyCount({
            postId: Number(id),
            replyCount: replyCount.value,
        });
    }
    catch {
        if (detailVersion === detailRequestVersion) {
            replyError.value = 'Reply failed. Please try again.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion) {
            replySubmitting.value = false;
        }
    }
};
const requestDeleteReply = (replyID) => {
    const viewerID = currentViewerID.value;
    const reply = replies.value.find(item => item.id === replyID);
    if (!authStore.isAuthenticated ||
        viewerID === null ||
        deletingReplyId.value !== null ||
        deleteReplyCandidateId.value !== null ||
        !reply ||
        reply.author.id !== viewerID) {
        return;
    }
    deleteReplyCandidateId.value = replyID;
    replyDeleteError.value = '';
};
const cancelDeleteReply = () => {
    if (deletingReplyId.value !== null) {
        return;
    }
    deleteReplyCandidateId.value = null;
    replyDeleteError.value = '';
};
const confirmDeleteReply = async () => {
    const replyID = deleteReplyCandidateId.value;
    const viewerID = currentViewerID.value;
    const reply = replyID === null
        ? null
        : replies.value.find(item => item.id === replyID);
    if (replyID === null ||
        !authStore.isAuthenticated ||
        viewerID === null ||
        deletingReplyId.value !== null ||
        !reply ||
        reply.author.id !== viewerID) {
        if (deletingReplyId.value === null) {
            cancelDeleteReply();
        }
        return;
    }
    const detailVersion = detailRequestVersion;
    const requestVersion = ++replyDeleteRequestVersion;
    deletingReplyId.value = replyID;
    replyDeleteError.value = '';
    try {
        await deletePostReply(replyID);
        if (detailVersion !== detailRequestVersion
            || requestVersion !== replyDeleteRequestVersion) {
            return;
        }
        replies.value = replies.value.filter(reply => reply.id !== replyID);
        replyCount.value = Math.max(0, replyCount.value - 1);
        syncExternalReplyCount({
            postId: Number(postId.value),
            replyCount: replyCount.value,
        });
        deletingReplyId.value = null;
        deleteReplyCandidateId.value = null;
        replyDeleteError.value = '';
    }
    catch {
        if (detailVersion === detailRequestVersion
            && requestVersion === replyDeleteRequestVersion
            && deleteReplyCandidateId.value === replyID) {
            deletingReplyId.value = null;
            replyDeleteError.value = 'Reply could not be deleted. Please try again.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion
            && requestVersion === replyDeleteRequestVersion
            && deletingReplyId.value === replyID) {
            deletingReplyId.value = null;
        }
    }
};
const consumeReplyIntent = async () => {
    if (replyIntentTask) {
        replyIntentRetryRequested = true;
        return;
    }
    const task = (async () => {
        if (route.query.reply !== '1' || !authStore.isAuthenticated || !post.value) {
            return;
        }
        const detailVersion = detailRequestVersion;
        const id = postId.value;
        await nextTick();
        const isCurrentIntent = () => route.query.reply === '1'
            && authStore.isAuthenticated
            && detailVersion === detailRequestVersion
            && id === postId.value
            && Boolean(post.value);
        if (!isCurrentIntent()) {
            return;
        }
        const composer = composerRef.value;
        if (!composer || !await composer.focus()) {
            return;
        }
        if (!isCurrentIntent()) {
            return;
        }
        const query = { ...route.query };
        delete query.reply;
        try {
            await router.replace({
                name: 'PostDetail',
                params: route.params,
                query,
                hash: route.hash,
            });
        }
        catch {
            // Keep the one-shot intent in the URL if the replacement is rejected.
        }
    })();
    replyIntentTask = task;
    try {
        await task;
    }
    finally {
        if (replyIntentTask !== task) {
            return;
        }
        replyIntentTask = null;
        const shouldRetry = replyIntentRetryRequested;
        replyIntentRetryRequested = false;
        if (shouldRetry && route.query.reply === '1') {
            void consumeReplyIntent();
        }
    }
};
const loadDetail = async (id, isAuthenticated) => {
    const detailVersion = ++detailRequestVersion;
    closeMediaViewer();
    finishRead('navigate_to_post');
    void recommendationTelemetry.flush(false);
    resetPostState();
    resetLikeState();
    resetRepostState();
    resetRepliesState();
    handoffPost.value = null;
    if (!isAuthenticated) {
        return;
    }
    if (!isValidPostID(id)) {
        postError.value = 'This post URL is not valid.';
        return;
    }
    handoffPost.value = postDetailHandoff.consume(Number(id));
    postLoading.value = true;
    try {
        const loadedPost = await getPostById(id);
        if (detailVersion !== detailRequestVersion) {
            return;
        }
        if (loadedPost.id !== Number(id)) {
            throw new Error('post response id mismatch');
        }
        handoffPost.value = null;
        post.value = loadedPost;
        likeCount.value = clampCount(loadedPost.like_count);
        replyCount.value = clampCount(loadedPost.reply_count);
        viewCount.value = clampCount(loadedPost.view_count);
        postLoading.value = false;
        await nextTick();
        if (detailVersion !== detailRequestVersion
            || postId.value !== id
            || post.value?.id !== Number(id)) {
            return;
        }
        postViewTelemetry.enqueue(Number(id), createPostViewEventID(), 'post_detail');
        if (postBodyRef.value) {
            startRead(id, detailVersion);
        }
        void loadLikeState(id, detailVersion);
        void loadRepostState(id, detailVersion);
        void loadInitialReplies(id, detailVersion);
    }
    catch (error) {
        if (detailVersion === detailRequestVersion) {
            handoffPost.value = null;
            const status = error.response?.status;
            if (status === 404) {
                replyDraftStore.clearDraft(Number(id));
            }
            postError.value = status === 404
                ? 'This post does not exist.'
                : 'The post could not be loaded.';
        }
    }
    finally {
        if (detailVersion === detailRequestVersion) {
            postLoading.value = false;
        }
    }
};
const retryPost = () => {
    void loadDetail(postId.value, authStore.isAuthenticated);
};
const goBack = () => {
    const historyState = window.history.state;
    if (historyState?.back) {
        router.back();
        return;
    }
    void router.push({ name: 'Home' });
};
watch([postId, () => authStore.isAuthenticated], ([id, isAuthenticated]) => {
    void loadDetail(id, isAuthenticated);
}, { immediate: true });
watch(currentViewerID, (viewerID, previousViewerID) => {
    if (viewerID === previousViewerID) {
        return;
    }
    deleteRequestVersion += 1;
    deletePending.value = false;
    deleteError.value = '';
    deletePostConfirmOpen.value = false;
    replyDeleteRequestVersion += 1;
    deletingReplyId.value = null;
    deleteReplyCandidateId.value = null;
    replyDeleteError.value = '';
});
watch(currentViewerID, viewerID => {
    replyDraftStore.setViewer(viewerID);
}, { immediate: true });
watch([() => route.query.reply, post, () => authStore.isAuthenticated, replySubmitting], () => {
    if (route.query.reply === '1') {
        void consumeReplyIntent();
    }
}, { flush: 'post' });
onBeforeRouteLeave(to => {
    finishRead(to.name === 'Recommendations' ? 'back_to_recommendation' : 'route_leave');
    void recommendationTelemetry.flush(false);
});
onMounted(() => {
    document.addEventListener('visibilitychange', handleVisibilityChange);
    window.addEventListener('scroll', handleReadScroll, { passive: true });
    window.addEventListener('resize', updateReadGeometry);
    window.addEventListener('pagehide', handlePageHide);
});
onBeforeUnmount(() => {
    finishRead('route_leave');
    void recommendationTelemetry.flush(false);
    disconnectReadGeometryObserver();
    document.removeEventListener('visibilitychange', handleVisibilityChange);
    window.removeEventListener('scroll', handleReadScroll);
    window.removeEventListener('resize', updateReadGeometry);
    window.removeEventListener('pagehide', handlePageHide);
});
const __VLS_fnComponent = (await import('vue')).defineComponent({});
let __VLS_functionalComponentProps;
let __VLS_modelEmitsType;
function __VLS_template() {
    let __VLS_ctx;
    /* Components */
    let __VLS_otherComponents;
    let __VLS_own;
    let __VLS_localComponents;
    let __VLS_components;
    let __VLS_styleScopedClasses;
    // CSS variable injection 
    // CSS variable injection end 
    let __VLS_resolvedLocalAndGlobalComponents;
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("detail-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("detail-header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.goBack) }, ...{ class: ("detail-header__back") }, type: ("button"), "aria-label": ("Back"), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((20)), }));
    const __VLS_1 = __VLS_0({ name: ("arrow-left"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("arrow-left"), size: ((20)), }));
    // @ts-ignore
    [goBack,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({ ...{ class: ("detail-header__title") }, });
    if (__VLS_ctx.detailPresentation) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ ...{ class: ("post-detail") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-detail__author-row") }, });
        // @ts-ignore
        [AuthorIdentity,];
        const __VLS_5 = __VLS_asFunctionalComponent(AuthorIdentity, new AuthorIdentity({ author: ((__VLS_ctx.detailPresentation.author)), variant: ("post"), }));
        const __VLS_6 = __VLS_5({ author: ((__VLS_ctx.detailPresentation.author)), variant: ("post"), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
        ({}({ author: ((__VLS_ctx.detailPresentation.author)), variant: ("post"), }));
        // @ts-ignore
        [detailPresentation, detailPresentation,];
        const __VLS_9 = __VLS_pickFunctionalComponentCtx(AuthorIdentity, __VLS_6);
        if (__VLS_ctx.detailPresentation.kind === 'post' && __VLS_ctx.canDeletePost) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.requestDeletePost) }, ref: ("deletePostButtonRef"), ...{ class: ("post-detail__delete") }, type: ("button"), "aria-label": ("Delete post"), title: ("Delete post"), disabled: ((__VLS_ctx.deletePending)), "aria-busy": ((__VLS_ctx.deletePending)), });
            // @ts-ignore
            (__VLS_ctx.deletePostButtonRef);
            // @ts-ignore
            [AppIcon,];
            const __VLS_10 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("trash"), size: ((18)), }));
            const __VLS_11 = __VLS_10({ name: ("trash"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_10));
            ({}({ name: ("trash"), size: ((18)), }));
            // @ts-ignore
            [detailPresentation, canDeletePost, requestDeletePost, deletePending, deletePending, deletePostButtonRef,];
            const __VLS_14 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_11);
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("postBodyRef"), ...{ class: ("post-detail__body") }, ...{ class: (({ 'post-detail__body--loading': __VLS_ctx.detailPresentation.kind === 'warm' })) }, "aria-busy": ((__VLS_ctx.detailPresentation.kind === 'warm' ? 'true' : undefined)), });
        // @ts-ignore
        (__VLS_ctx.postBodyRef);
        __VLS_styleScopedClasses = ({ 'post-detail__body--loading': detailPresentation.kind === 'warm' });
        // @ts-ignore
        [LinkifiedText,];
        const __VLS_15 = __VLS_asFunctionalComponent(LinkifiedText, new LinkifiedText({ text: ((__VLS_ctx.detailPresentation.body)), }));
        const __VLS_16 = __VLS_15({ text: ((__VLS_ctx.detailPresentation.body)), }, ...__VLS_functionalComponentArgsRest(__VLS_15));
        ({}({ text: ((__VLS_ctx.detailPresentation.body)), }));
        // @ts-ignore
        [detailPresentation, detailPresentation, detailPresentation, postBodyRef,];
        const __VLS_19 = __VLS_pickFunctionalComponentCtx(LinkifiedText, __VLS_16);
        if (__VLS_ctx.detailPresentation.media.length > 0) {
            // @ts-ignore
            [PostMediaGrid,];
            const __VLS_20 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailPresentation.media)), interactive: (true), }));
            const __VLS_21 = __VLS_20({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailPresentation.media)), interactive: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_20));
            ({}({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailPresentation.media)), interactive: (true), }));
            let __VLS_25;
            const __VLS_26 = {
                onOpen: (...[$event]) => {
                    if (!((__VLS_ctx.detailPresentation)))
                        return;
                    if (!((__VLS_ctx.detailPresentation.media.length > 0)))
                        return;
                    __VLS_ctx.openMediaViewer(__VLS_ctx.detailPresentation.media, $event);
                    // @ts-ignore
                    [detailPresentation, detailPresentation, detailPresentation, openMediaViewer,];
                }
            };
            const __VLS_24 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_21);
            let __VLS_22;
            let __VLS_23;
        }
        if (__VLS_ctx.detailReference) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({ ...{ class: ("post-detail__reference") }, "aria-label": ("Referenced post"), });
            if (__VLS_ctx.detailReferenceDestination) {
                const __VLS_27 = {}.RouterLink;
                ({}.RouterLink);
                ({}.RouterLink);
                __VLS_components.RouterLink;
                __VLS_components.RouterLink;
                // @ts-ignore
                [RouterLink, RouterLink,];
                const __VLS_28 = __VLS_asFunctionalComponent(__VLS_27, new __VLS_27({ ...{ class: ("post-detail__reference-label post-detail__reference-link") }, to: ((__VLS_ctx.detailReferenceDestination)), }));
                const __VLS_29 = __VLS_28({ ...{ class: ("post-detail__reference-label post-detail__reference-link") }, to: ((__VLS_ctx.detailReferenceDestination)), }, ...__VLS_functionalComponentArgsRest(__VLS_28));
                ({}({ ...{ class: ("post-detail__reference-label post-detail__reference-link") }, to: ((__VLS_ctx.detailReferenceDestination)), }));
                (__VLS_ctx.detailReferenceLabel);
                // @ts-ignore
                [detailReference, detailReferenceDestination, detailReferenceDestination, detailReferenceLabel,];
                (__VLS_32.slots).default;
                const __VLS_32 = __VLS_pickFunctionalComponentCtx(__VLS_27, __VLS_29);
            }
            else {
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-detail__reference-label") }, });
                (__VLS_ctx.detailReferenceLabel);
                // @ts-ignore
                [detailReferenceLabel,];
            }
            if (__VLS_ctx.detailReference.deleted) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("post-detail__reference-tombstone") }, });
                (__VLS_ctx.detailReferenceMessage);
                // @ts-ignore
                [detailReference, detailReferenceMessage,];
            }
            else {
                if (__VLS_ctx.detailReferenceAuthor) {
                    // @ts-ignore
                    [AuthorIdentity,];
                    const __VLS_33 = __VLS_asFunctionalComponent(AuthorIdentity, new AuthorIdentity({ author: ((__VLS_ctx.detailReferenceAuthor)), variant: ("compact"), }));
                    const __VLS_34 = __VLS_33({ author: ((__VLS_ctx.detailReferenceAuthor)), variant: ("compact"), }, ...__VLS_functionalComponentArgsRest(__VLS_33));
                    ({}({ author: ((__VLS_ctx.detailReferenceAuthor)), variant: ("compact"), }));
                    // @ts-ignore
                    [detailReferenceAuthor, detailReferenceAuthor,];
                    const __VLS_37 = __VLS_pickFunctionalComponentCtx(AuthorIdentity, __VLS_34);
                }
                __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("post-detail__reference-content") }, });
                // @ts-ignore
                [LinkifiedText,];
                const __VLS_38 = __VLS_asFunctionalComponent(LinkifiedText, new LinkifiedText({ text: ((__VLS_ctx.detailReferenceContent)), to: ((__VLS_ctx.detailReferenceDestination)), }));
                const __VLS_39 = __VLS_38({ text: ((__VLS_ctx.detailReferenceContent)), to: ((__VLS_ctx.detailReferenceDestination)), }, ...__VLS_functionalComponentArgsRest(__VLS_38));
                ({}({ text: ((__VLS_ctx.detailReferenceContent)), to: ((__VLS_ctx.detailReferenceDestination)), }));
                // @ts-ignore
                [detailReferenceDestination, detailReferenceContent,];
                const __VLS_42 = __VLS_pickFunctionalComponentCtx(LinkifiedText, __VLS_39);
                if (__VLS_ctx.detailReferenceMedia.length > 0) {
                    // @ts-ignore
                    [PostMediaGrid,];
                    const __VLS_43 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailReferenceMedia)), interactive: (true), }));
                    const __VLS_44 = __VLS_43({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailReferenceMedia)), interactive: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_43));
                    ({}({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.detailReferenceMedia)), interactive: (true), }));
                    let __VLS_48;
                    const __VLS_49 = {
                        onOpen: (...[$event]) => {
                            if (!((__VLS_ctx.detailPresentation)))
                                return;
                            if (!((__VLS_ctx.detailReference)))
                                return;
                            if (!(!((__VLS_ctx.detailReference.deleted))))
                                return;
                            if (!((__VLS_ctx.detailReferenceMedia.length > 0)))
                                return;
                            __VLS_ctx.openMediaViewer(__VLS_ctx.detailReferenceMedia, $event);
                            // @ts-ignore
                            [openMediaViewer, detailReferenceMedia, detailReferenceMedia, detailReferenceMedia,];
                        }
                    };
                    const __VLS_47 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_44);
                    let __VLS_45;
                    let __VLS_46;
                }
            }
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-detail__meta") }, });
        if (__VLS_ctx.detailPostTimestamp) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.detailPostTimestamp);
            // @ts-ignore
            [detailPostTimestamp, detailPostTimestamp,];
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-detail__views") }, "aria-label": ((__VLS_ctx.postViewsLabel)), title: ((__VLS_ctx.postViewsLabel)), });
        (__VLS_ctx.formattedViews);
        // @ts-ignore
        [postViewsLabel, postViewsLabel, formattedViews,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-detail__engagement") }, "aria-label": ("Post engagement"), });
        if (__VLS_ctx.detailPresentation.kind === 'post') {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.focusReplyComposer) }, ...{ class: ("post-detail__metric post-detail__reply") }, type: ("button"), "aria-label": ((__VLS_ctx.detailReplyLabel)), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_50 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("reply"), size: ((18)), }));
            const __VLS_51 = __VLS_50({ name: ("reply"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_50));
            ({}({ name: ("reply"), size: ((18)), }));
            // @ts-ignore
            [detailPresentation, focusReplyComposer, detailReplyLabel,];
            const __VLS_54 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_51);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.replyCount);
            // @ts-ignore
            [replyCount,];
            // @ts-ignore
            [RepostAction,];
            const __VLS_55 = __VLS_asFunctionalComponent(RepostAction, new RepostAction({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), reposted: ((__VLS_ctx.reposted)), count: ((__VLS_ctx.repostCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated || __VLS_ctx.repostStateUnavailable)), loading: ((__VLS_ctx.repostStateLoading)), pending: ((__VLS_ctx.repostSubmitting)), ariaLabel: ((__VLS_ctx.detailRepostLabel)), variant: ("detail"), }));
            const __VLS_56 = __VLS_55({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), reposted: ((__VLS_ctx.reposted)), count: ((__VLS_ctx.repostCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated || __VLS_ctx.repostStateUnavailable)), loading: ((__VLS_ctx.repostStateLoading)), pending: ((__VLS_ctx.repostSubmitting)), ariaLabel: ((__VLS_ctx.detailRepostLabel)), variant: ("detail"), }, ...__VLS_functionalComponentArgsRest(__VLS_55));
            ({}({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), reposted: ((__VLS_ctx.reposted)), count: ((__VLS_ctx.repostCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated || __VLS_ctx.repostStateUnavailable)), loading: ((__VLS_ctx.repostStateLoading)), pending: ((__VLS_ctx.repostSubmitting)), ariaLabel: ((__VLS_ctx.detailRepostLabel)), variant: ("detail"), }));
            let __VLS_60;
            const __VLS_61 = {
                onToggle: (__VLS_ctx.toggleRepost)
            };
            // @ts-ignore
            [postId, reposted, repostCount, authStore, repostStateUnavailable, repostStateLoading, repostSubmitting, detailRepostLabel, toggleRepost,];
            const __VLS_59 = __VLS_pickFunctionalComponentCtx(RepostAction, __VLS_56);
            let __VLS_57;
            let __VLS_58;
            // @ts-ignore
            [LikeAction,];
            const __VLS_62 = __VLS_asFunctionalComponent(LikeAction, new LikeAction({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), liked: ((__VLS_ctx.liked)), count: ((__VLS_ctx.likeCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated)), loading: ((__VLS_ctx.likeStateLoading)), pending: ((__VLS_ctx.likeSubmitting)), ariaLabel: ((__VLS_ctx.detailLikeLabel)), variant: ("detail"), }));
            const __VLS_63 = __VLS_62({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), liked: ((__VLS_ctx.liked)), count: ((__VLS_ctx.likeCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated)), loading: ((__VLS_ctx.likeStateLoading)), pending: ((__VLS_ctx.likeSubmitting)), ariaLabel: ((__VLS_ctx.detailLikeLabel)), variant: ("detail"), }, ...__VLS_functionalComponentArgsRest(__VLS_62));
            ({}({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.postId)), liked: ((__VLS_ctx.liked)), count: ((__VLS_ctx.likeCount)), disabled: ((!__VLS_ctx.authStore.isAuthenticated)), loading: ((__VLS_ctx.likeStateLoading)), pending: ((__VLS_ctx.likeSubmitting)), ariaLabel: ((__VLS_ctx.detailLikeLabel)), variant: ("detail"), }));
            let __VLS_67;
            const __VLS_68 = {
                onToggle: (__VLS_ctx.toggleLike)
            };
            // @ts-ignore
            [postId, authStore, liked, likeCount, likeStateLoading, likeSubmitting, detailLikeLabel, toggleLike,];
            const __VLS_66 = __VLS_pickFunctionalComponentCtx(LikeAction, __VLS_63);
            let __VLS_64;
            let __VLS_65;
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-detail__metric post-detail__reply") }, "aria-label": ((__VLS_ctx.presentationReplyLabel)), title: ((__VLS_ctx.presentationReplyLabel)), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_69 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("reply"), size: ((18)), }));
            const __VLS_70 = __VLS_69({ name: ("reply"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_69));
            ({}({ name: ("reply"), size: ((18)), }));
            // @ts-ignore
            [presentationReplyLabel, presentationReplyLabel,];
            const __VLS_73 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_70);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.detailPresentation.replyCount);
            // @ts-ignore
            [detailPresentation,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-detail__metric post-detail__repost") }, "aria-label": ((__VLS_ctx.presentationRepostLabel)), title: ((__VLS_ctx.presentationRepostLabel)), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_74 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("repost"), size: ((18)), }));
            const __VLS_75 = __VLS_74({ name: ("repost"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_74));
            ({}({ name: ("repost"), size: ((18)), }));
            // @ts-ignore
            [presentationRepostLabel, presentationRepostLabel,];
            const __VLS_78 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_75);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.detailPresentation.repostCount);
            // @ts-ignore
            [detailPresentation,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-detail__metric post-detail__like") }, "aria-label": ((__VLS_ctx.presentationLikeLabel)), title: ((__VLS_ctx.presentationLikeLabel)), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_79 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("heart"), size: ((18)), }));
            const __VLS_80 = __VLS_79({ name: ("heart"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_79));
            ({}({ name: ("heart"), size: ((18)), }));
            // @ts-ignore
            [presentationLikeLabel, presentationLikeLabel,];
            const __VLS_83 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_80);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.detailPresentation.likeCount);
            // @ts-ignore
            [detailPresentation,];
        }
        if (__VLS_ctx.detailPresentation.kind === 'post' && __VLS_ctx.likeError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("detail-inline-error") }, role: ("status"), });
            (__VLS_ctx.likeError);
            // @ts-ignore
            [detailPresentation, likeError, likeError,];
        }
        if (__VLS_ctx.detailPresentation.kind === 'post' && __VLS_ctx.repostError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("detail-inline-error") }, role: ("status"), });
            (__VLS_ctx.repostError);
            // @ts-ignore
            [detailPresentation, repostError, repostError,];
        }
        if (__VLS_ctx.detailPresentation.kind === 'warm') {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("detail-warm-loading") }, role: ("status"), "aria-live": ("polite"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("detail-loading__spinner") }, "aria-hidden": ("true"), });
            // @ts-ignore
            [detailPresentation,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("sr-only") }, });
        }
        if (__VLS_ctx.detailPresentation.kind === 'post') {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("post-conversation") }, "aria-label": ("Conversation"), });
            // @ts-ignore
            [ReplyComposer,];
            const __VLS_84 = __VLS_asFunctionalComponent(ReplyComposer, new ReplyComposer({ ...{ 'onSubmit': {} }, key: ((__VLS_ctx.postId)), ref: ("composerRef"), author: ((__VLS_ctx.replyComposerAuthor)), modelValue: ((__VLS_ctx.replyDraftContent)), submitting: ((__VLS_ctx.replySubmitting)), }));
            const __VLS_85 = __VLS_84({ ...{ 'onSubmit': {} }, key: ((__VLS_ctx.postId)), ref: ("composerRef"), author: ((__VLS_ctx.replyComposerAuthor)), modelValue: ((__VLS_ctx.replyDraftContent)), submitting: ((__VLS_ctx.replySubmitting)), }, ...__VLS_functionalComponentArgsRest(__VLS_84));
            ({}({ ...{ 'onSubmit': {} }, key: ((__VLS_ctx.postId)), ref: ("composerRef"), author: ((__VLS_ctx.replyComposerAuthor)), modelValue: ((__VLS_ctx.replyDraftContent)), submitting: ((__VLS_ctx.replySubmitting)), }));
            // @ts-ignore
            (__VLS_ctx.composerRef);
            let __VLS_89;
            const __VLS_90 = {
                onSubmit: (__VLS_ctx.handleCreateReply)
            };
            // @ts-ignore
            [detailPresentation, postId, replyComposerAuthor, replyDraftContent, replySubmitting, composerRef, handleCreateReply,];
            const __VLS_88 = __VLS_pickFunctionalComponentCtx(ReplyComposer, __VLS_85);
            let __VLS_86;
            let __VLS_87;
            if (__VLS_ctx.replyError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("reply-error") }, role: ("alert"), });
                (__VLS_ctx.replyError);
                // @ts-ignore
                [replyError, replyError,];
            }
            if (__VLS_ctx.repliesInitialLoading) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("replies-state") }, "aria-live": ("polite"), });
                // @ts-ignore
                [repliesInitialLoading,];
            }
            else if (__VLS_ctx.repliesError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("replies-state replies-state--error") }, role: ("alert"), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
                // @ts-ignore
                [repliesError,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                (__VLS_ctx.repliesError);
                // @ts-ignore
                [repliesError,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryInitialReplies) }, type: ("button"), });
                // @ts-ignore
                [retryInitialReplies,];
            }
            else {
                // @ts-ignore
                [ReplyList,];
                const __VLS_91 = __VLS_asFunctionalComponent(ReplyList, new ReplyList({ ...{ 'onLoadMore': {} }, ...{ 'onRetry': {} }, ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((__VLS_ctx.postId)), replies: ((__VLS_ctx.replies)), currentIdentity: ((__VLS_ctx.currentIdentity)), deletingReplyId: ((__VLS_ctx.deletingReplyId)), hasNext: ((Boolean(__VLS_ctx.nextCursor))), loadingMore: ((__VLS_ctx.repliesLoadingMore)), loadMoreError: ((__VLS_ctx.repliesLoadMoreError)), }));
                const __VLS_92 = __VLS_91({ ...{ 'onLoadMore': {} }, ...{ 'onRetry': {} }, ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((__VLS_ctx.postId)), replies: ((__VLS_ctx.replies)), currentIdentity: ((__VLS_ctx.currentIdentity)), deletingReplyId: ((__VLS_ctx.deletingReplyId)), hasNext: ((Boolean(__VLS_ctx.nextCursor))), loadingMore: ((__VLS_ctx.repliesLoadingMore)), loadMoreError: ((__VLS_ctx.repliesLoadMoreError)), }, ...__VLS_functionalComponentArgsRest(__VLS_91));
                ({}({ ...{ 'onLoadMore': {} }, ...{ 'onRetry': {} }, ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((__VLS_ctx.postId)), replies: ((__VLS_ctx.replies)), currentIdentity: ((__VLS_ctx.currentIdentity)), deletingReplyId: ((__VLS_ctx.deletingReplyId)), hasNext: ((Boolean(__VLS_ctx.nextCursor))), loadingMore: ((__VLS_ctx.repliesLoadingMore)), loadMoreError: ((__VLS_ctx.repliesLoadMoreError)), }));
                let __VLS_96;
                const __VLS_97 = {
                    onLoadMore: (__VLS_ctx.loadMoreReplies)
                };
                const __VLS_98 = {
                    onRetry: (__VLS_ctx.retryLoadMoreReplies)
                };
                const __VLS_99 = {
                    onRequestDelete: (__VLS_ctx.requestDeleteReply)
                };
                const __VLS_100 = {
                    onOpenMedia: (__VLS_ctx.openMediaViewer)
                };
                // @ts-ignore
                [openMediaViewer, postId, replies, currentIdentity, deletingReplyId, nextCursor, repliesLoadingMore, repliesLoadMoreError, loadMoreReplies, retryLoadMoreReplies, requestDeleteReply,];
                const __VLS_95 = __VLS_pickFunctionalComponentCtx(ReplyList, __VLS_92);
                let __VLS_93;
                let __VLS_94;
            }
        }
    }
    else if (__VLS_ctx.postLoading) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("detail-loading") }, role: ("status"), "aria-live": ("polite"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("detail-loading__spinner") }, "aria-hidden": ("true"), });
        // @ts-ignore
        [postLoading,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("sr-only") }, });
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("detail-state detail-state--error") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        (__VLS_ctx.postFailureTitle);
        // @ts-ignore
        [postFailureTitle,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.postFailureMessage);
        // @ts-ignore
        [postFailureMessage,];
        if (!__VLS_ctx.authStore.isAuthenticated) {
            const __VLS_101 = {}.RouterLink;
            ({}.RouterLink);
            ({}.RouterLink);
            __VLS_components.RouterLink;
            __VLS_components.RouterLink;
            // @ts-ignore
            [RouterLink, RouterLink,];
            const __VLS_102 = __VLS_asFunctionalComponent(__VLS_101, new __VLS_101({ ...{ class: ("detail-state__link") }, to: (({ name: 'Login' })), }));
            const __VLS_103 = __VLS_102({ ...{ class: ("detail-state__link") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_102));
            ({}({ ...{ class: ("detail-state__link") }, to: (({ name: 'Login' })), }));
            // @ts-ignore
            [authStore,];
            (__VLS_106.slots).default;
            const __VLS_106 = __VLS_pickFunctionalComponentCtx(__VLS_101, __VLS_103);
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryPost) }, type: ("button"), });
            // @ts-ignore
            [retryPost,];
        }
    }
    if (__VLS_ctx.mediaViewer) {
        // @ts-ignore
        [PostMediaViewer,];
        const __VLS_107 = __VLS_asFunctionalComponent(PostMediaViewer, new PostMediaViewer({ ...{ 'onClose': {} }, media: ((__VLS_ctx.mediaViewer.media)), initialIndex: ((__VLS_ctx.mediaViewer.index)), }));
        const __VLS_108 = __VLS_107({ ...{ 'onClose': {} }, media: ((__VLS_ctx.mediaViewer.media)), initialIndex: ((__VLS_ctx.mediaViewer.index)), }, ...__VLS_functionalComponentArgsRest(__VLS_107));
        ({}({ ...{ 'onClose': {} }, media: ((__VLS_ctx.mediaViewer.media)), initialIndex: ((__VLS_ctx.mediaViewer.index)), }));
        let __VLS_112;
        const __VLS_113 = {
            onClose: (__VLS_ctx.closeMediaViewer)
        };
        // @ts-ignore
        [mediaViewer, mediaViewer, mediaViewer, closeMediaViewer,];
        const __VLS_111 = __VLS_pickFunctionalComponentCtx(PostMediaViewer, __VLS_108);
        let __VLS_109;
        let __VLS_110;
    }
    if (__VLS_ctx.deletePostConfirmOpen) {
        // @ts-ignore
        [ConfirmDialog,];
        const __VLS_114 = __VLS_asFunctionalComponent(ConfirmDialog, new ConfirmDialog({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }));
        const __VLS_115 = __VLS_114({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }, ...__VLS_functionalComponentArgsRest(__VLS_114));
        ({}({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }));
        let __VLS_119;
        const __VLS_120 = {
            onConfirm: (__VLS_ctx.confirmDeletePost)
        };
        const __VLS_121 = {
            onCancel: (__VLS_ctx.cancelDeletePost)
        };
        // @ts-ignore
        [deletePending, deletePostConfirmOpen, deleteError, confirmDeletePost, cancelDeletePost,];
        const __VLS_118 = __VLS_pickFunctionalComponentCtx(ConfirmDialog, __VLS_115);
        let __VLS_116;
        let __VLS_117;
    }
    if (__VLS_ctx.deleteReplyCandidateId !== null) {
        // @ts-ignore
        [ConfirmDialog,];
        const __VLS_122 = __VLS_asFunctionalComponent(ConfirmDialog, new ConfirmDialog({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete reply?"), description: ("This reply will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletingReplyId !== null)), error: ((__VLS_ctx.replyDeleteError)), }));
        const __VLS_123 = __VLS_122({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete reply?"), description: ("This reply will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletingReplyId !== null)), error: ((__VLS_ctx.replyDeleteError)), }, ...__VLS_functionalComponentArgsRest(__VLS_122));
        ({}({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete reply?"), description: ("This reply will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletingReplyId !== null)), error: ((__VLS_ctx.replyDeleteError)), }));
        let __VLS_127;
        const __VLS_128 = {
            onConfirm: (__VLS_ctx.confirmDeleteReply)
        };
        const __VLS_129 = {
            onCancel: (__VLS_ctx.cancelDeleteReply)
        };
        // @ts-ignore
        [deletingReplyId, deleteReplyCandidateId, replyDeleteError, confirmDeleteReply, cancelDeleteReply,];
        const __VLS_126 = __VLS_pickFunctionalComponentCtx(ConfirmDialog, __VLS_123);
        let __VLS_124;
        let __VLS_125;
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['detail-view'];
        __VLS_styleScopedClasses['detail-header'];
        __VLS_styleScopedClasses['detail-header__back'];
        __VLS_styleScopedClasses['detail-header__title'];
        __VLS_styleScopedClasses['post-detail'];
        __VLS_styleScopedClasses['post-detail__author-row'];
        __VLS_styleScopedClasses['post-detail__delete'];
        __VLS_styleScopedClasses['post-detail__body'];
        __VLS_styleScopedClasses['post-detail__reference'];
        __VLS_styleScopedClasses['post-detail__reference-label'];
        __VLS_styleScopedClasses['post-detail__reference-link'];
        __VLS_styleScopedClasses['post-detail__reference-label'];
        __VLS_styleScopedClasses['post-detail__reference-tombstone'];
        __VLS_styleScopedClasses['post-detail__reference-content'];
        __VLS_styleScopedClasses['post-detail__meta'];
        __VLS_styleScopedClasses['post-detail__views'];
        __VLS_styleScopedClasses['post-detail__engagement'];
        __VLS_styleScopedClasses['post-detail__metric'];
        __VLS_styleScopedClasses['post-detail__reply'];
        __VLS_styleScopedClasses['post-detail__metric'];
        __VLS_styleScopedClasses['post-detail__reply'];
        __VLS_styleScopedClasses['post-detail__metric'];
        __VLS_styleScopedClasses['post-detail__repost'];
        __VLS_styleScopedClasses['post-detail__metric'];
        __VLS_styleScopedClasses['post-detail__like'];
        __VLS_styleScopedClasses['detail-inline-error'];
        __VLS_styleScopedClasses['detail-inline-error'];
        __VLS_styleScopedClasses['detail-warm-loading'];
        __VLS_styleScopedClasses['detail-loading__spinner'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['post-conversation'];
        __VLS_styleScopedClasses['reply-error'];
        __VLS_styleScopedClasses['replies-state'];
        __VLS_styleScopedClasses['replies-state'];
        __VLS_styleScopedClasses['replies-state--error'];
        __VLS_styleScopedClasses['detail-loading'];
        __VLS_styleScopedClasses['detail-loading__spinner'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['detail-state'];
        __VLS_styleScopedClasses['detail-state--error'];
        __VLS_styleScopedClasses['detail-state__link'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AuthorIdentity: AuthorIdentity,
                LinkifiedText: LinkifiedText,
                PostMediaGrid: PostMediaGrid,
                PostMediaViewer: PostMediaViewer,
                ConfirmDialog: ConfirmDialog,
                LikeAction: LikeAction,
                RepostAction: RepostAction,
                AppIcon: AppIcon,
                ReplyComposer: ReplyComposer,
                ReplyList: ReplyList,
                authStore: authStore,
                currentIdentity: currentIdentity,
                postId: postId,
                postLoading: postLoading,
                deletePending: deletePending,
                deleteError: deleteError,
                deletePostConfirmOpen: deletePostConfirmOpen,
                deletePostButtonRef: deletePostButtonRef,
                postBodyRef: postBodyRef,
                liked: liked,
                likeCount: likeCount,
                likeStateLoading: likeStateLoading,
                likeSubmitting: likeSubmitting,
                likeError: likeError,
                reposted: reposted,
                repostCount: repostCount,
                repostStateLoading: repostStateLoading,
                repostSubmitting: repostSubmitting,
                repostError: repostError,
                repostStateUnavailable: repostStateUnavailable,
                replies: replies,
                nextCursor: nextCursor,
                repliesInitialLoading: repliesInitialLoading,
                repliesLoadingMore: repliesLoadingMore,
                repliesError: repliesError,
                repliesLoadMoreError: repliesLoadMoreError,
                replySubmitting: replySubmitting,
                replyError: replyError,
                deletingReplyId: deletingReplyId,
                deleteReplyCandidateId: deleteReplyCandidateId,
                replyDeleteError: replyDeleteError,
                composerRef: composerRef,
                replyCount: replyCount,
                mediaViewer: mediaViewer,
                detailPresentation: detailPresentation,
                detailReference: detailReference,
                detailReferenceDestination: detailReferenceDestination,
                detailReferenceLabel: detailReferenceLabel,
                detailReferenceContent: detailReferenceContent,
                detailReferenceAuthor: detailReferenceAuthor,
                detailReferenceMedia: detailReferenceMedia,
                detailReferenceMessage: detailReferenceMessage,
                openMediaViewer: openMediaViewer,
                closeMediaViewer: closeMediaViewer,
                presentationLikeLabel: presentationLikeLabel,
                presentationReplyLabel: presentationReplyLabel,
                presentationRepostLabel: presentationRepostLabel,
                postFailureTitle: postFailureTitle,
                postFailureMessage: postFailureMessage,
                replyComposerAuthor: replyComposerAuthor,
                replyDraftContent: replyDraftContent,
                focusReplyComposer: focusReplyComposer,
                detailPostTimestamp: detailPostTimestamp,
                formattedViews: formattedViews,
                postViewsLabel: postViewsLabel,
                detailReplyLabel: detailReplyLabel,
                detailLikeLabel: detailLikeLabel,
                detailRepostLabel: detailRepostLabel,
                canDeletePost: canDeletePost,
                requestDeletePost: requestDeletePost,
                cancelDeletePost: cancelDeletePost,
                confirmDeletePost: confirmDeletePost,
                toggleLike: toggleLike,
                toggleRepost: toggleRepost,
                loadMoreReplies: loadMoreReplies,
                retryInitialReplies: retryInitialReplies,
                retryLoadMoreReplies: retryLoadMoreReplies,
                handleCreateReply: handleCreateReply,
                requestDeleteReply: requestDeleteReply,
                cancelDeleteReply: cancelDeleteReply,
                confirmDeleteReply: confirmDeleteReply,
                retryPost: retryPost,
                goBack: goBack,
            };
        },
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
;
