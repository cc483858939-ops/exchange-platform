/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { createPost, uploadPostMedia } from '../services/postService';
import { useAuthStore } from '../store/auth';
import { usePostDraftStore } from '../store/postDraft';
import { useFeedStore } from '../store/feed';
import { useProfileSessionStore } from '../store/profileSession';
import AppIcon from '../components/icons/AppIcon.vue';
import PostMediaGrid from '../components/content/PostMediaGrid.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const maxContentLength = 10000;
const maxMediaCount = 4;
const maxMediaBytes = 5 * 1024 * 1024;
const maxContentHeight = 360;
const allowedMediaTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const router = useRouter();
const authStore = useAuthStore();
const postDraft = usePostDraftStore();
const feedStore = useFeedStore();
const profileSessionStore = useProfileSessionStore();
const phase = ref('idle');
const validationAttempted = ref(false);
const mediaError = ref('');
const uploadError = ref('');
const publishError = ref('');
const contentInput = ref(null);
const previewEntries = ref(new Map());
let publishAttemptVersion = 0;
const currentIdentity = computed(() => authStore.currentIdentity);
const currentUserID = computed(() => (authStore.isAuthenticated ? currentIdentity.value?.id ?? null : null));
const content = computed({
    get: () => postDraft.content,
    set: (value) => postDraft.setContent(value),
});
const contentLength = computed(() => Array.from(content.value.trim()).length);
const authorUsername = computed(() => currentIdentity.value?.username.trim() || '');
const authorDisplayName = computed(() => (currentIdentity.value?.display_name.trim() || authorUsername.value || 'Current user'));
const authorHandle = computed(() => (authorUsername.value ? '@' + authorUsername.value : 'Signed-in account'));
const isSubmitting = computed(() => phase.value !== 'idle');
const publishLabel = computed(() => {
    if (phase.value === 'uploading') {
        return 'Uploading...';
    }
    if (phase.value === 'publishing') {
        return 'Posting...';
    }
    return 'Post';
});
const contentError = computed(() => {
    if (contentLength.value > maxContentLength) {
        return 'Post must be ' + maxContentLength + ' characters or fewer.';
    }
    if (validationAttempted.value && !content.value.trim()) {
        return 'Post is required.';
    }
    return '';
});
const canPublish = computed(() => (authStore.isAuthenticated
    && Boolean(content.value.trim())
    && contentLength.value <= maxContentLength));
const previewMedia = computed(() => postDraft.media
    .map((item, index) => ({
    type: 'image',
    url: previewEntries.value.get(item.id)?.url || '',
    position: index,
}))
    .filter(item => Boolean(item.url)));
const revokePreview = (entry) => {
    if (entry.url
        && typeof URL !== 'undefined'
        && typeof URL.revokeObjectURL === 'function') {
        URL.revokeObjectURL(entry.url);
    }
};
const syncPreviews = () => {
    const currentItems = new Map(postDraft.media.map(item => [item.id, item]));
    const nextEntries = new Map(previewEntries.value);
    for (const [id, entry] of nextEntries) {
        const item = currentItems.get(id);
        if (!item || item.file !== entry.file) {
            revokePreview(entry);
            nextEntries.delete(id);
        }
    }
    for (const item of postDraft.media) {
        if (nextEntries.has(item.id)) {
            continue;
        }
        const createObjectURL = typeof URL !== 'undefined'
            && typeof URL.createObjectURL === 'function'
            ? URL.createObjectURL.bind(URL)
            : null;
        if (createObjectURL) {
            nextEntries.set(item.id, { file: item.file, url: createObjectURL(item.file) });
        }
    }
    previewEntries.value = nextEntries;
};
const revokeAllPreviews = () => {
    previewEntries.value.forEach(revokePreview);
    previewEntries.value = new Map();
};
const validateMediaFile = (file) => {
    if (file.size <= 0) {
        return 'Choose a non-empty image file.';
    }
    if (!allowedMediaTypes.has(file.type)) {
        return 'Images must be JPEG, PNG, or WebP.';
    }
    if (file.size > maxMediaBytes) {
        return 'Each image must be 5 MB or smaller.';
    }
    return '';
};
const handleMediaChange = (event) => {
    if (isSubmitting.value) {
        return;
    }
    const input = event.target;
    const files = Array.from(input.files ?? []);
    let firstError = '';
    let overflow = false;
    for (const file of files) {
        if (postDraft.media.length >= maxMediaCount) {
            overflow = true;
            break;
        }
        const error = validateMediaFile(file);
        if (error) {
            firstError ||= error;
            continue;
        }
        postDraft.addMedia(file);
    }
    mediaError.value = overflow
        ? 'You can attach up to 4 images.'
        : firstError;
    uploadError.value = '';
    publishError.value = '';
    input.value = '';
};
const removeMedia = (index) => {
    if (isSubmitting.value) {
        return;
    }
    const item = postDraft.media[index];
    if (item) {
        postDraft.removeMedia(item.id);
    }
    mediaError.value = '';
    uploadError.value = '';
    publishError.value = '';
};
const resizeContent = () => {
    const input = contentInput.value;
    if (!input) {
        return;
    }
    input.style.height = 'auto';
    input.style.height = String(Math.min(input.scrollHeight, maxContentHeight)) + 'px';
    input.style.overflowY = input.scrollHeight > maxContentHeight ? 'auto' : 'hidden';
};
const goHome = () => {
    void router.push({ name: 'Home' });
};
const goBack = () => {
    const historyState = window.history.state;
    if (historyState?.back) {
        router.back();
        return;
    }
    goHome();
};
const isCurrentPublishAttempt = (attemptVersion, publisherUserID, selectedMedia) => (publishAttemptVersion === attemptVersion
    && authStore.isAuthenticated
    && authStore.currentIdentity?.id === publisherUserID
    && postDraft.viewerID === publisherUserID
    && postDraft.media.length === selectedMedia.length
    && selectedMedia.every((item, index) => (postDraft.media[index]?.id === item.id
        && postDraft.media[index]?.file === item.file)));
const submitPost = async () => {
    if (isSubmitting.value) {
        return;
    }
    validationAttempted.value = true;
    if (!canPublish.value) {
        return;
    }
    const publisherUserID = authStore.currentIdentity?.id;
    if (typeof publisherUserID !== 'number'
        || !Number.isSafeInteger(publisherUserID)
        || publisherUserID <= 0) {
        publishError.value = 'Your account could not be verified. Your draft was preserved.';
        return;
    }
    const publishAttempt = ++publishAttemptVersion;
    const selectedMedia = postDraft.media.map(item => ({
        id: item.id,
        file: item.file,
        uploadedURL: item.uploadedURL.trim(),
    }));
    const draftContent = content.value.trim();
    const attemptMediaIdentity = selectedMedia.map(item => ({ id: item.id, file: item.file }));
    const uploadedURLs = [];
    uploadError.value = '';
    publishError.value = '';
    for (const item of selectedMedia) {
        let uploadedURL = item.uploadedURL;
        if (!uploadedURL) {
            phase.value = 'uploading';
            try {
                uploadedURL = (await uploadPostMedia(item.file)).trim();
                if (!uploadedURL) {
                    throw new Error('The media upload returned no URL.');
                }
                if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
                    return;
                }
                postDraft.setUploadedURL(item.id, uploadedURL);
            }
            catch {
                if (publishAttemptVersion === publishAttempt) {
                    uploadError.value = 'Image upload failed. Your draft was preserved.';
                }
                phase.value = 'idle';
                return;
            }
        }
        uploadedURLs.push(uploadedURL);
    }
    if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
        publishError.value = 'Your account changed while posting. No post was created, and your draft was preserved.';
        return;
    }
    phase.value = 'publishing';
    try {
        const post = await createPost({
            content: draftContent,
            media: uploadedURLs.map(url => ({ type: 'image', url })),
        });
        if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
            return;
        }
        if (post.author?.id !== publisherUserID) {
            publishError.value = 'The post was saved, but your account changed during posting. It was not added to this Home feed.';
            return;
        }
        if (!feedStore.registerPublishedPost(post, publisherUserID)) {
            publishError.value = 'The post was posted, but Home could not update for this account. Your draft was preserved.';
            return;
        }
        profileSessionStore.registerPublishedPost(post, publisherUserID);
        authStore.syncCurrentIdentityProfile(post.author);
        postDraft.clear();
        await router.replace({
            name: 'Home',
            query: { tab: 'for-you' },
        });
    }
    catch {
        publishError.value = 'Post could not be posted. Try again.';
    }
    finally {
        if (publishAttemptVersion === publishAttempt) {
            phase.value = 'idle';
        }
    }
};
watch(content, () => {
    void nextTick(resizeContent);
});
watch(currentUserID, viewerID => {
    publishAttemptVersion += 1;
    postDraft.setViewer(viewerID);
    phase.value = 'idle';
}, { immediate: true });
watch(() => postDraft.media, syncPreviews, { deep: true, immediate: true });
onMounted(() => {
    void nextTick(resizeContent);
});
onBeforeUnmount(() => {
    publishAttemptVersion += 1;
    revokeAllPreviews();
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("composer-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("composer-header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.goBack) }, ...{ class: ("composer-header__back") }, type: ("button"), "aria-label": ("Back"), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((20)), }));
    const __VLS_1 = __VLS_0({ name: ("arrow-left"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("arrow-left"), size: ((20)), }));
    // @ts-ignore
    [goBack,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("publish-button") }, type: ("submit"), form: ("composer-form"), disabled: ((!__VLS_ctx.canPublish || __VLS_ctx.isSubmitting)), "aria-busy": ((__VLS_ctx.isSubmitting)), });
    (__VLS_ctx.publishLabel);
    // @ts-ignore
    [canPublish, isSubmitting, isSubmitting, publishLabel,];
    if (!__VLS_ctx.authStore.isAuthenticated) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("composer-auth-state") }, "aria-labelledby": ("composer-login-heading"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ("composer-login-heading"), });
        // @ts-ignore
        [authStore,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        const __VLS_5 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_6 = __VLS_asFunctionalComponent(__VLS_5, new __VLS_5({ ...{ class: ("composer-action") }, to: (({ name: 'Login' })), }));
        const __VLS_7 = __VLS_6({ ...{ class: ("composer-action") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ ...{ class: ("composer-action") }, to: (({ name: 'Login' })), }));
        (__VLS_10.slots).default;
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(__VLS_5, __VLS_7);
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({ ...{ onSubmit: (__VLS_ctx.submitPost) }, id: ("composer-form"), ...{ class: ("composer-form") }, novalidate: (true), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("composer-section") }, "aria-labelledby": ("post-content-heading"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ("post-content-heading"), ...{ class: ("sr-only") }, });
        // @ts-ignore
        [submitPost,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("composer-main") }, });
        // @ts-ignore
        [UserAvatar,];
        const __VLS_11 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("composer-author__avatar") }, avatarUrl: ((__VLS_ctx.currentIdentity?.avatar_url)), displayName: ((__VLS_ctx.currentIdentity?.display_name)), username: ((__VLS_ctx.currentIdentity?.username)), size: ((42)), decorative: (true), }));
        const __VLS_12 = __VLS_11({ ...{ class: ("composer-author__avatar") }, avatarUrl: ((__VLS_ctx.currentIdentity?.avatar_url)), displayName: ((__VLS_ctx.currentIdentity?.display_name)), username: ((__VLS_ctx.currentIdentity?.username)), size: ((42)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_11));
        ({}({ ...{ class: ("composer-author__avatar") }, avatarUrl: ((__VLS_ctx.currentIdentity?.avatar_url)), displayName: ((__VLS_ctx.currentIdentity?.display_name)), username: ((__VLS_ctx.currentIdentity?.username)), size: ((42)), decorative: (true), }));
        // @ts-ignore
        [currentIdentity, currentIdentity, currentIdentity,];
        const __VLS_15 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_12);
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("composer-main__content") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("composer-author__copy") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.authorDisplayName);
        // @ts-ignore
        [authorDisplayName,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
        (__VLS_ctx.authorHandle);
        // @ts-ignore
        [authorHandle,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ ...{ class: ("sr-only") }, for: ("post-content"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.textarea, __VLS_intrinsicElements.textarea)({ id: ("post-content"), ref: ("contentInput"), value: ((__VLS_ctx.content)), ...{ class: ("composer-input") }, rows: ("5"), placeholder: ("What's happening?"), disabled: ((__VLS_ctx.isSubmitting)), "aria-describedby": ("post-content-help post-content-error"), });
        // @ts-ignore
        (__VLS_ctx.contentInput);
        // @ts-ignore
        [isSubmitting, content, contentInput,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ id: ("post-content-help"), ...{ class: ("composer-field__meta") }, });
        if (__VLS_ctx.contentError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ id: ("post-content-error"), ...{ class: ("field-error") }, role: ("alert"), });
            (__VLS_ctx.contentError);
            // @ts-ignore
            [contentError, contentError,];
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: (({ 'field-count--over': __VLS_ctx.contentLength > __VLS_ctx.maxContentLength })) }, });
        __VLS_styleScopedClasses = ({ 'field-count--over': contentLength > maxContentLength });
        (__VLS_ctx.contentLength);
        (__VLS_ctx.maxContentLength);
        // @ts-ignore
        [contentLength, contentLength, maxContentLength, maxContentLength,];
        if (__VLS_ctx.previewMedia.length > 0) {
            // @ts-ignore
            [PostMediaGrid,];
            const __VLS_16 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ ...{ 'onRemove': {} }, media: ((__VLS_ctx.previewMedia)), removable: (true), disabled: ((__VLS_ctx.isSubmitting)), }));
            const __VLS_17 = __VLS_16({ ...{ 'onRemove': {} }, media: ((__VLS_ctx.previewMedia)), removable: (true), disabled: ((__VLS_ctx.isSubmitting)), }, ...__VLS_functionalComponentArgsRest(__VLS_16));
            ({}({ ...{ 'onRemove': {} }, media: ((__VLS_ctx.previewMedia)), removable: (true), disabled: ((__VLS_ctx.isSubmitting)), }));
            let __VLS_21;
            const __VLS_22 = {
                onRemove: (__VLS_ctx.removeMedia)
            };
            // @ts-ignore
            [isSubmitting, previewMedia, previewMedia, removeMedia,];
            const __VLS_20 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_17);
            let __VLS_18;
            let __VLS_19;
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ ...{ class: ("composer-action composer-action--secondary media-picker") }, ...{ class: (({ 'composer-action--disabled': __VLS_ctx.isSubmitting })) }, "aria-disabled": ((__VLS_ctx.isSubmitting)), for: ("post-media-input"), });
        __VLS_styleScopedClasses = ({ 'composer-action--disabled': isSubmitting });
        // @ts-ignore
        [AppIcon,];
        const __VLS_23 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("image"), size: ((18)), }));
        const __VLS_24 = __VLS_23({ name: ("image"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_23));
        ({}({ name: ("image"), size: ((18)), }));
        // @ts-ignore
        [isSubmitting, isSubmitting,];
        const __VLS_27 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_24);
        __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ ...{ onChange: (__VLS_ctx.handleMediaChange) }, id: ("post-media-input"), ...{ class: ("media-input") }, type: ("file"), multiple: (true), accept: ("image/jpeg,image/png,image/webp"), disabled: ((__VLS_ctx.isSubmitting)), });
        // @ts-ignore
        [isSubmitting, handleMediaChange,];
        if (__VLS_ctx.mediaError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("field-error media-error") }, role: ("alert"), });
            (__VLS_ctx.mediaError);
            // @ts-ignore
            [mediaError, mediaError,];
        }
        if (__VLS_ctx.uploadError || __VLS_ctx.publishError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("composer-status") }, role: ("alert"), "aria-live": ("polite"), });
            (__VLS_ctx.uploadError || __VLS_ctx.publishError);
            // @ts-ignore
            [uploadError, uploadError, publishError, publishError,];
        }
        if (__VLS_ctx.isSubmitting) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("composer-progress") }, "aria-live": ("polite"), });
            (__VLS_ctx.publishLabel);
            // @ts-ignore
            [isSubmitting, publishLabel,];
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['composer-view'];
        __VLS_styleScopedClasses['composer-header'];
        __VLS_styleScopedClasses['composer-header__back'];
        __VLS_styleScopedClasses['publish-button'];
        __VLS_styleScopedClasses['composer-auth-state'];
        __VLS_styleScopedClasses['composer-action'];
        __VLS_styleScopedClasses['composer-form'];
        __VLS_styleScopedClasses['composer-section'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['composer-main'];
        __VLS_styleScopedClasses['composer-author__avatar'];
        __VLS_styleScopedClasses['composer-main__content'];
        __VLS_styleScopedClasses['composer-author__copy'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['composer-input'];
        __VLS_styleScopedClasses['composer-field__meta'];
        __VLS_styleScopedClasses['field-error'];
        __VLS_styleScopedClasses['composer-action'];
        __VLS_styleScopedClasses['composer-action--secondary'];
        __VLS_styleScopedClasses['media-picker'];
        __VLS_styleScopedClasses['media-input'];
        __VLS_styleScopedClasses['field-error'];
        __VLS_styleScopedClasses['media-error'];
        __VLS_styleScopedClasses['composer-status'];
        __VLS_styleScopedClasses['composer-progress'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                PostMediaGrid: PostMediaGrid,
                UserAvatar: UserAvatar,
                maxContentLength: maxContentLength,
                authStore: authStore,
                mediaError: mediaError,
                uploadError: uploadError,
                publishError: publishError,
                contentInput: contentInput,
                currentIdentity: currentIdentity,
                content: content,
                contentLength: contentLength,
                authorDisplayName: authorDisplayName,
                authorHandle: authorHandle,
                isSubmitting: isSubmitting,
                publishLabel: publishLabel,
                contentError: contentError,
                canPublish: canPublish,
                previewMedia: previewMedia,
                handleMediaChange: handleMediaChange,
                removeMedia: removeMedia,
                goBack: goBack,
                submitPost: submitPost,
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
