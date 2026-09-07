/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import { updateUserProfile, uploadProfileAvatar } from '../services/userService';
import { useAuthStore } from '../store/auth';
import { useProfileSessionStore } from '../store/profileSession';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const profileDisplayNameLimit = 50;
const profileBioLimit = 160;
const profileAvatarMaxBytes = 2 * 1024 * 1024;
const profileAvatarTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const skeletonCount = 3;
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const profileStore = useProfileSessionStore();
const userId = computed(() => String(route.params.id ?? '').trim());
const numericUserID = computed(() => {
    const value = Number(userId.value);
    return Number.isSafeInteger(value) && value > 0 ? value : null;
});
const activeSession = computed(() => (numericUserID.value === null ? null : profileStore.ensureSession(numericUserID.value)));
const invalidProfileError = ref('');
const user = computed(() => activeSession.value?.user ?? null);
const profileLoading = computed(() => activeSession.value?.profileLoading ?? false);
const profileError = computed(() => invalidProfileError.value || activeSession.value?.profileError || '');
const profileNotFound = computed(() => activeSession.value?.profileNotFound ?? false);
const posts = computed(() => activeSession.value?.posts ?? []);
const postsInitialLoading = computed(() => activeSession.value?.postsInitialLoading ?? false);
const postsLoadingMore = computed(() => activeSession.value?.postsLoadingMore ?? false);
const postsInitialError = computed(() => activeSession.value?.postsInitialError ?? '');
const postsLoadMoreError = computed(() => activeSession.value?.postsLoadMoreError ?? '');
const nextCursor = computed(() => activeSession.value?.nextCursor ?? null);
const hasMore = computed(() => activeSession.value?.hasMore ?? false);
const followState = computed(() => activeSession.value?.followState ?? null);
const followLoading = computed(() => activeSession.value?.followLoading ?? false);
const followError = computed(() => activeSession.value?.followError ?? '');
const followActionError = computed(() => activeSession.value?.followActionError ?? '');
const followPending = computed(() => activeSession.value?.followPending ?? false);
const likePendingPostIds = profileStore.likePendingPostIds;
const repostPendingPostIds = profileStore.repostPendingPostIds;
const pendingDeletePostIds = profileStore.pendingDeletePostIds;
const deleteErrors = profileStore.deleteErrors;
const sentinelRef = ref(null);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let observer = null;
let profileEntryVersion = 0;
let restoredEntryVersion = -1;
const profileDisplayName = computed(() => {
    const displayName = user.value?.display_name?.trim() ?? '';
    return displayName || user.value?.username || 'Profile';
});
const headerUsername = computed(() => profileDisplayName.value);
const joinedLabel = computed(() => {
    const value = user.value?.created_at;
    if (!value)
        return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime()))
        return '';
    return date.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
});
const getErrorStatus = (error) => error.response?.status;
const currentViewerID = computed(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const isOwnProfile = computed(() => Boolean(user.value
    && currentViewerID.value !== null
    && user.value.id === currentViewerID.value));
const socialReady = computed(() => Boolean(authStore.isAuthenticated
    && currentViewerID.value !== null
    && followState.value
    && !followLoading.value
    && activeSession.value?.followLoaded));
const showFollowControl = computed(() => Boolean(authStore.isAuthenticated
    && user.value
    && currentViewerID.value !== null
    && user.value.id !== currentViewerID.value
    && socialReady.value));
const canDeletePost = (post) => authStore.isAuthenticated
    && currentViewerID.value !== null
    && post.author.id === currentViewerID.value;
const saveCurrentScroll = (targetUserID) => {
    if (typeof window !== 'undefined') {
        profileStore.setScrollY(targetUserID, window.scrollY);
    }
};
const restoreScrollOnce = async () => {
    const entryVersion = profileEntryVersion;
    if (restoredEntryVersion === entryVersion)
        return;
    const session = activeSession.value;
    const targetUserID = numericUserID.value;
    if (!session
        || targetUserID === null
        || !session.profileLoaded
        || (!session.postsLoaded && session.postsInitialLoading)
        || typeof window === 'undefined')
        return;
    await nextTick();
    if (entryVersion !== profileEntryVersion
        || targetUserID !== numericUserID.value
        || restoredEntryVersion === entryVersion)
        return;
    if (typeof window.scrollTo === 'function'
        && !window.navigator.userAgent.toLowerCase().includes('jsdom')) {
        window.scrollTo({ top: session.scrollY, behavior: 'auto' });
    }
    restoredEntryVersion = entryVersion;
};
const retryFollowState = () => {
    if (numericUserID.value !== null && currentViewerID.value !== null) {
        void profileStore.loadFollowState(numericUserID.value, true);
    }
};
const handleFollowToggle = () => {
    if (numericUserID.value !== null) {
        void profileStore.toggleFollow(numericUserID.value);
    }
};
const editDialogRef = ref(null);
const editDisplayNameInputRef = ref(null);
const profileAvatarInputRef = ref(null);
const editOriginal = ref(null);
const editDraft = reactive({ display_name: '', bio: '', avatar_url: '' });
const pendingAvatarFile = ref(null);
const pendingAvatarPreviewURL = ref('');
const editAvatarLoadFailed = ref(false);
const editAvatarError = ref('');
const editError = ref('');
const editSaving = ref(false);
const editDisplayNameLength = computed(() => Array.from(editDraft.display_name.trim()).length);
const editBioLength = computed(() => Array.from(editDraft.bio.trim()).length);
const editDisplayNameOverLimit = computed(() => editDisplayNameLength.value > profileDisplayNameLimit);
const editBioOverLimit = computed(() => editBioLength.value > profileBioLimit);
const editProfileDisplayName = computed(() => editDraft.display_name.trim() || user.value?.username || 'Profile');
const editProfileInitial = computed(() => Array.from(editProfileDisplayName.value.trim())[0]?.toUpperCase() || '?');
const editAvatarPreview = computed(() => {
    if (editAvatarLoadFailed.value)
        return '';
    return pendingAvatarPreviewURL.value || editDraft.avatar_url;
});
const editAvatarHasValue = computed(() => Boolean(pendingAvatarFile.value || editDraft.avatar_url));
const editCanSave = computed(() => {
    const original = editOriginal.value;
    if (!original || editSaving.value || editDisplayNameOverLimit.value || editBioOverLimit.value) {
        return false;
    }
    return Boolean(pendingAvatarFile.value
        || editDraft.display_name.trim() !== original.display_name
        || editDraft.bio.trim() !== original.bio
        || editDraft.avatar_url !== original.avatar_url);
});
const revokePendingAvatarPreview = () => {
    if (pendingAvatarPreviewURL.value) {
        URL.revokeObjectURL(pendingAvatarPreviewURL.value);
        pendingAvatarPreviewURL.value = '';
    }
};
const clearEditDraft = () => {
    revokePendingAvatarPreview();
    pendingAvatarFile.value = null;
    editOriginal.value = null;
    editDraft.display_name = '';
    editDraft.bio = '';
    editDraft.avatar_url = '';
    editAvatarLoadFailed.value = false;
    editAvatarError.value = '';
    editError.value = '';
    if (profileAvatarInputRef.value) {
        profileAvatarInputRef.value.value = '';
    }
};
const forceCloseEditProfile = () => {
    editSaving.value = false;
    clearEditDraft();
    if (editDialogRef.value?.open) {
        editDialogRef.value.close();
    }
};
const openEditProfile = () => {
    if (!user.value || !isOwnProfile.value || editSaving.value)
        return;
    clearEditDraft();
    editOriginal.value = {
        display_name: user.value.display_name,
        bio: user.value.bio,
        avatar_url: user.value.avatar_url,
    };
    editDraft.display_name = user.value.display_name;
    editDraft.bio = user.value.bio;
    editDraft.avatar_url = user.value.avatar_url;
    editDialogRef.value?.showModal();
    void nextTick(() => editDisplayNameInputRef.value?.focus());
};
const closeEditProfile = () => {
    if (editSaving.value)
        return;
    clearEditDraft();
    if (editDialogRef.value?.open)
        editDialogRef.value.close();
};
const handleDialogCancel = (event) => {
    if (editSaving.value) {
        event.preventDefault();
        return;
    }
    clearEditDraft();
};
const handleDialogClose = () => {
    if (!editSaving.value)
        clearEditDraft();
};
const handleAvatarSelection = (event) => {
    const input = event.target;
    const file = input.files?.[0];
    input.value = '';
    if (!file)
        return;
    if (file.size <= 0 || file.size > profileAvatarMaxBytes) {
        editAvatarError.value = 'Photo must be between 1 byte and 2 MiB.';
        return;
    }
    if (!profileAvatarTypes.has(file.type)) {
        editAvatarError.value = 'Use a JPEG, PNG, or WebP image.';
        return;
    }
    revokePendingAvatarPreview();
    pendingAvatarFile.value = file;
    pendingAvatarPreviewURL.value = URL.createObjectURL(file);
    editAvatarLoadFailed.value = false;
    editAvatarError.value = '';
    editError.value = '';
};
const removeProfileAvatar = () => {
    if (editSaving.value)
        return;
    revokePendingAvatarPreview();
    pendingAvatarFile.value = null;
    editDraft.avatar_url = '';
    editAvatarLoadFailed.value = false;
    editAvatarError.value = '';
    editError.value = '';
};
const isCurrentEditSession = (capture, profileID, viewerID) => editDialogRef.value?.open === true
    && user.value?.id === profileID
    && userId.value === String(profileID)
    && currentViewerID.value === viewerID
    && authStore.isAuthenticated
    && profileStore.isCurrentSessionCapture(capture);
const buildProfilePatch = () => {
    const original = editOriginal.value;
    if (!original)
        return {};
    const payload = {};
    const displayName = editDraft.display_name.trim();
    const bio = editDraft.bio.trim();
    if (displayName !== original.display_name)
        payload.display_name = displayName;
    if (bio !== original.bio)
        payload.bio = bio;
    if (editDraft.avatar_url !== original.avatar_url)
        payload.avatar_url = editDraft.avatar_url;
    return payload;
};
const profileEditErrorMessage = (error, action) => {
    const status = getErrorStatus(error);
    if (status === 401)
        return 'Please log in again and retry.';
    if (status === 400) {
        return action === 'upload'
            ? 'That photo could not be uploaded.'
            : 'Check your profile fields and retry.';
    }
    return action === 'upload'
        ? 'Could not upload photo. Please retry.'
        : 'Could not save profile. Please retry.';
};
const saveProfile = async () => {
    const profile = user.value;
    const viewerID = currentViewerID.value;
    if (!profile || viewerID === null || !editCanSave.value)
        return;
    const capture = profileStore.captureSession(profile.id);
    if (!capture)
        return;
    const selectedFile = pendingAvatarFile.value;
    editSaving.value = true;
    editError.value = '';
    try {
        if (selectedFile) {
            const uploadedAvatarURL = await uploadProfileAvatar(selectedFile);
            if (!isCurrentEditSession(capture, profile.id, viewerID))
                return;
            editDraft.avatar_url = uploadedAvatarURL;
            pendingAvatarFile.value = null;
            revokePendingAvatarPreview();
            editAvatarLoadFailed.value = false;
        }
        const payload = buildProfilePatch();
        if (Object.keys(payload).length === 0) {
            editSaving.value = false;
            closeEditProfile();
            return;
        }
        const updatedUser = await updateUserProfile(profile.id, payload);
        if (!isCurrentEditSession(capture, profile.id, viewerID))
            return;
        profileStore.updateUser(updatedUser);
        authStore.syncCurrentIdentityProfile(updatedUser);
        editSaving.value = false;
        clearEditDraft();
        editDialogRef.value?.close();
    }
    catch (error) {
        if (isCurrentEditSession(capture, profile.id, viewerID)) {
            editError.value = profileEditErrorMessage(error, selectedFile && pendingAvatarFile.value ? 'upload' : 'save');
        }
    }
    finally {
        if (isCurrentEditSession(capture, profile.id, viewerID)) {
            editSaving.value = false;
        }
    }
};
const loadProfile = (force = false) => {
    invalidProfileError.value = '';
    if (numericUserID.value === null) {
        invalidProfileError.value = 'This profile URL is not valid.';
        return;
    }
    forceCloseEditProfile();
    void profileStore.loadProfile(numericUserID.value, force);
};
const retryProfile = () => {
    loadProfile(true);
};
const retryInitialPosts = () => {
    if (numericUserID.value !== null) {
        void profileStore.loadPosts(numericUserID.value, true);
    }
};
const loadMorePosts = () => {
    if (numericUserID.value !== null) {
        void profileStore.loadMorePosts(numericUserID.value);
    }
};
const retryLoadMore = () => {
    if (numericUserID.value !== null) {
        profileStore.retryLoadMorePosts(numericUserID.value);
    }
};
const handleDeletePost = async (postId) => {
    const removed = await profileStore.deletePost(postId, numericUserID.value ?? undefined);
    if (removed) {
        disconnectObserver();
        await nextTick(updateObserver);
    }
};
const handleLikeToggle = (postId) => {
    void profileStore.toggleLike(postId, numericUserID.value ?? undefined);
};
const handleRepostToggle = (postId) => {
    void profileStore.toggleRepost(postId, numericUserID.value ?? undefined);
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
const disconnectObserver = () => {
    observer?.disconnect();
    observer = null;
};
const updateObserver = () => {
    disconnectObserver();
    if (!intersectionObserverAvailable
        || !sentinelRef.value
        || !hasMore.value
        || postsLoadingMore.value
        || postsLoadMoreError.value
        || !user.value) {
        return;
    }
    observer = new IntersectionObserver((entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
            loadMorePosts();
        }
    }, { rootMargin: '240px 0px' });
    observer.observe(sentinelRef.value);
};
watch(userId, (nextID, previousID) => {
    profileEntryVersion += 1;
    const previousNumericID = Number(previousID);
    if (Number.isSafeInteger(previousNumericID) && previousNumericID > 0) {
        saveCurrentScroll(previousNumericID);
        profileStore.cancelPendingDeletesForProfile(previousNumericID);
    }
    invalidProfileError.value = '';
    loadProfile();
}, { immediate: true });
watch(currentViewerID, (nextViewerID, previousViewerID) => {
    if (nextViewerID === previousViewerID)
        return;
    profileEntryVersion += 1;
    forceCloseEditProfile();
    loadProfile();
});
watch([
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.postsLoaded,
    () => activeSession.value?.postsInitialLoading,
    () => activeSession.value?.postsInitialError,
], () => { void restoreScrollOnce(); }, { flush: 'post', immediate: true });
watch([
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.postsLoaded,
    () => activeSession.value?.hasMore,
    () => activeSession.value?.postsLoadingMore,
    () => activeSession.value?.postsLoadMoreError,
    () => activeSession.value?.posts.length,
], () => { void nextTick(updateObserver); }, { flush: 'post' });
onMounted(() => {
    void nextTick(updateObserver);
});
onBeforeUnmount(() => {
    if (numericUserID.value !== null)
        saveCurrentScroll(numericUserID.value);
    forceCloseEditProfile();
    disconnectObserver();
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("profile-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("profile-header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.goBack) }, ...{ class: ("profile-header__back") }, type: ("button"), "aria-label": ("Back"), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((22)), }));
    const __VLS_1 = __VLS_0({ name: ("arrow-left"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("arrow-left"), size: ((22)), }));
    // @ts-ignore
    [goBack,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-header__copy") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.headerUsername);
    // @ts-ignore
    [headerUsername,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
    if (__VLS_ctx.isOwnProfile) {
        // @ts-ignore
        [MobileAccountMenu,];
        const __VLS_5 = __VLS_asFunctionalComponent(MobileAccountMenu, new MobileAccountMenu({}));
        const __VLS_6 = __VLS_5({}, ...__VLS_functionalComponentArgsRest(__VLS_5));
        ({}({}));
        // @ts-ignore
        [isOwnProfile,];
        const __VLS_9 = __VLS_pickFunctionalComponentCtx(MobileAccountMenu, __VLS_6);
    }
    if (__VLS_ctx.profileLoading) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("profile-identity profile-identity--loading") }, "aria-live": ("polite"), "aria-label": ("Loading profile"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--avatar") }, "aria-hidden": ("true"), });
        // @ts-ignore
        [profileLoading,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton-copy") }, "aria-hidden": ("true"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--name") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--handle") }, });
    }
    else if (__VLS_ctx.user) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("profile-identity") }, "aria-labelledby": ("profile-name"), });
        // @ts-ignore
        [UserAvatar,];
        const __VLS_10 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("profile-avatar") }, avatarUrl: ((__VLS_ctx.user.avatar_url)), displayName: ((__VLS_ctx.user.display_name)), username: ((__VLS_ctx.user.username)), size: ((76)), decorative: (true), }));
        const __VLS_11 = __VLS_10({ ...{ class: ("profile-avatar") }, avatarUrl: ((__VLS_ctx.user.avatar_url)), displayName: ((__VLS_ctx.user.display_name)), username: ((__VLS_ctx.user.username)), size: ((76)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_10));
        ({}({ ...{ class: ("profile-avatar") }, avatarUrl: ((__VLS_ctx.user.avatar_url)), displayName: ((__VLS_ctx.user.display_name)), username: ((__VLS_ctx.user.username)), size: ((76)), decorative: (true), }));
        // @ts-ignore
        [user, user, user, user,];
        const __VLS_14 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_11);
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-identity__copy") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({ id: ("profile-name"), });
        (__VLS_ctx.profileDisplayName);
        // @ts-ignore
        [profileDisplayName,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-identity__handle") }, });
        (__VLS_ctx.user.username);
        // @ts-ignore
        [user,];
        if (__VLS_ctx.user.bio) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-identity__bio") }, });
            (__VLS_ctx.user.bio);
            // @ts-ignore
            [user, user,];
        }
        if (__VLS_ctx.joinedLabel) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.time, __VLS_intrinsicElements.time)({ ...{ class: ("profile-identity__joined") }, datetime: ((__VLS_ctx.user.created_at)), });
            (__VLS_ctx.joinedLabel);
            // @ts-ignore
            [user, joinedLabel, joinedLabel,];
        }
        if (__VLS_ctx.socialReady) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-social") }, "aria-label": ("Social stats"), });
            const __VLS_15 = {}.RouterLink;
            ({}.RouterLink);
            ({}.RouterLink);
            __VLS_components.RouterLink;
            __VLS_components.RouterLink;
            // @ts-ignore
            [RouterLink, RouterLink,];
            const __VLS_16 = __VLS_asFunctionalComponent(__VLS_15, new __VLS_15({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.user.id } })), }));
            const __VLS_17 = __VLS_16({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.user.id } })), }, ...__VLS_functionalComponentArgsRest(__VLS_16));
            ({}({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.user.id } })), }));
            (__VLS_ctx.followState?.following_count ?? 0);
            // @ts-ignore
            [user, socialReady, followState,];
            (__VLS_20.slots).default;
            const __VLS_20 = __VLS_pickFunctionalComponentCtx(__VLS_15, __VLS_17);
            const __VLS_21 = {}.RouterLink;
            ({}.RouterLink);
            ({}.RouterLink);
            __VLS_components.RouterLink;
            __VLS_components.RouterLink;
            // @ts-ignore
            [RouterLink, RouterLink,];
            const __VLS_22 = __VLS_asFunctionalComponent(__VLS_21, new __VLS_21({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.user.id } })), }));
            const __VLS_23 = __VLS_22({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.user.id } })), }, ...__VLS_functionalComponentArgsRest(__VLS_22));
            ({}({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.user.id } })), }));
            (__VLS_ctx.followState?.follower_count ?? 0);
            // @ts-ignore
            [user, followState,];
            (__VLS_26.slots).default;
            const __VLS_26 = __VLS_pickFunctionalComponentCtx(__VLS_21, __VLS_23);
        }
        else if (__VLS_ctx.followLoading) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-social profile-social--loading") }, "aria-label": ("Loading social stats"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-social__skeleton") }, });
            // @ts-ignore
            [followLoading,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-social__skeleton") }, });
        }
        if (__VLS_ctx.followError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-social-error") }, "aria-live": ("polite"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            // @ts-ignore
            [followError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryFollowState) }, ...{ class: ("profile-action profile-action--compact") }, type: ("button"), });
            // @ts-ignore
            [retryFollowState,];
        }
        if (__VLS_ctx.isOwnProfile || __VLS_ctx.showFollowControl) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-identity__action") }, });
            if (__VLS_ctx.isOwnProfile) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.openEditProfile) }, ...{ class: ("profile-action profile-action--primary") }, type: ("button"), });
                // @ts-ignore
                [isOwnProfile, isOwnProfile, showFollowControl, openEditProfile,];
            }
            else {
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.handleFollowToggle) }, ...{ class: ("profile-follow-button") }, ...{ class: (({ 'profile-follow-button--following': __VLS_ctx.followState?.following })) }, type: ("button"), "aria-pressed": ((__VLS_ctx.followState?.following === true)), "aria-busy": ((__VLS_ctx.followPending)), disabled: ((__VLS_ctx.followPending)), });
                __VLS_styleScopedClasses = ({ 'profile-follow-button--following': followState?.following });
                (__VLS_ctx.followState?.following ? 'Following' : 'Follow');
                // @ts-ignore
                [followState, followState, followState, handleFollowToggle, followPending, followPending,];
                if (__VLS_ctx.followActionError) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-action-error") }, "aria-live": ("polite"), });
                    (__VLS_ctx.followActionError);
                    // @ts-ignore
                    [followActionError, followActionError,];
                }
            }
        }
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("profile-state profile-state--page") }, "aria-live": ("polite"), role: ("alert"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
        (__VLS_ctx.profileNotFound ? 'Profile not found.' : 'Profile could not be loaded.');
        // @ts-ignore
        [profileNotFound,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.profileNotFound ? 'This user does not exist.' : __VLS_ctx.profileError);
        // @ts-ignore
        [profileNotFound, profileError,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-state__actions") }, });
        if (__VLS_ctx.profileNotFound) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.goHome) }, ...{ class: ("profile-action") }, type: ("button"), });
            // @ts-ignore
            [profileNotFound, goHome,];
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryProfile) }, ...{ class: ("profile-action") }, type: ("button"), });
            // @ts-ignore
            [retryProfile,];
        }
    }
    if (__VLS_ctx.user) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({ ...{ class: ("profile-tabs") }, "aria-label": ("Profile sections"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-tab profile-tab--active") }, });
        // @ts-ignore
        [user,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("profile-posts") }, "aria-labelledby": ("profile-posts-heading"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ("profile-posts-heading"), ...{ class: ("sr-only") }, });
        if (__VLS_ctx.postsInitialLoading) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-skeleton-list") }, "aria-live": ("polite"), "aria-label": ("Loading posts"), });
            for (const [slot] of __VLS_getVForSourceType((__VLS_ctx.skeletonCount))) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ key: ((slot)), ...{ class: ("profile-skeleton-post") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--identity") }, "aria-hidden": ("true"), });
                // @ts-ignore
                [postsInitialLoading, skeletonCount,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton-post__copy") }, "aria-hidden": ("true"), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--title") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--excerpt") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-skeleton profile-skeleton--metric") }, });
            }
        }
        else if (__VLS_ctx.postsInitialError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-state profile-state--inline") }, role: ("alert"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
            // @ts-ignore
            [postsInitialError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryInitialPosts) }, ...{ class: ("profile-action") }, type: ("button"), });
            // @ts-ignore
            [retryInitialPosts,];
        }
        else if (__VLS_ctx.posts.length === 0) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-empty") }, });
            // @ts-ignore
            [posts,];
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-post-list") }, });
            for (const [post] of __VLS_getVForSourceType((__VLS_ctx.posts))) {
                // @ts-ignore
                [PostCard,];
                const __VLS_27 = __VLS_asFunctionalComponent(PostCard, new PostCard({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                const __VLS_28 = __VLS_27({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }, ...__VLS_functionalComponentArgsRest(__VLS_27));
                ({}({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                let __VLS_32;
                const __VLS_33 = {
                    onToggleLike: (__VLS_ctx.handleLikeToggle)
                };
                const __VLS_34 = {
                    onToggleRepost: (__VLS_ctx.handleRepostToggle)
                };
                const __VLS_35 = {
                    onDeletePost: (__VLS_ctx.handleDeletePost)
                };
                // @ts-ignore
                [posts, likePendingPostIds, repostPendingPostIds, canDeletePost, pendingDeletePostIds, deleteErrors, handleLikeToggle, handleRepostToggle, handleDeletePost,];
                const __VLS_31 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_28);
                let __VLS_29;
                let __VLS_30;
            }
        }
        if (__VLS_ctx.hasMore || __VLS_ctx.postsLoadingMore || __VLS_ctx.postsLoadMoreError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("sentinelRef"), ...{ class: ("profile-feed-sentinel") }, "aria-live": ("polite"), });
            // @ts-ignore
            (__VLS_ctx.sentinelRef);
            if (__VLS_ctx.postsLoadingMore) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                // @ts-ignore
                [hasMore, postsLoadingMore, postsLoadingMore, postsLoadMoreError, sentinelRef,];
            }
            else if (__VLS_ctx.postsLoadMoreError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                // @ts-ignore
                [postsLoadMoreError,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryLoadMore) }, ...{ class: ("profile-action") }, type: ("button"), });
                // @ts-ignore
                [retryLoadMore,];
            }
            else if (!__VLS_ctx.intersectionObserverAvailable && __VLS_ctx.hasMore) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMorePosts) }, ...{ class: ("profile-action") }, type: ("button"), });
                // @ts-ignore
                [hasMore, intersectionObserverAvailable, loadMorePosts,];
            }
        }
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.dialog, __VLS_intrinsicElements.dialog)({ ...{ onCancel: (__VLS_ctx.handleDialogCancel) }, ...{ onClose: (__VLS_ctx.handleDialogClose) }, ref: ("editDialogRef"), ...{ class: ("profile-edit-dialog") }, "aria-labelledby": ("profile-edit-title"), });
    // @ts-ignore
    (__VLS_ctx.editDialogRef);
    __VLS_elementAsFunction(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({ ...{ onSubmit: (__VLS_ctx.saveProfile) }, ...{ class: ("profile-edit-form") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("profile-edit-dialog__header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ("profile-edit-title"), });
    // @ts-ignore
    [handleDialogCancel, handleDialogClose, editDialogRef, saveProfile,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.closeEditProfile) }, ...{ class: ("profile-edit-dialog__close") }, type: ("button"), "aria-label": ("Close edit profile"), disabled: ((__VLS_ctx.editSaving)), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_36 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("close"), size: ((20)), }));
    const __VLS_37 = __VLS_36({ name: ("close"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_36));
    ({}({ name: ("close"), size: ((20)), }));
    // @ts-ignore
    [closeEditProfile, editSaving,];
    const __VLS_40 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_37);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-avatar") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-avatar profile-avatar--edit") }, });
    if (__VLS_ctx.editAvatarPreview) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.img)({ ...{ onError: (...[$event]) => {
                    if (!((__VLS_ctx.editAvatarPreview)))
                        return;
                    __VLS_ctx.editAvatarLoadFailed = true;
                    // @ts-ignore
                    [editAvatarPreview, editAvatarLoadFailed,];
                } }, src: ((__VLS_ctx.editAvatarPreview)), alt: ((__VLS_ctx.editProfileDisplayName + ' avatar preview')), });
        // @ts-ignore
        [editAvatarPreview, editProfileDisplayName,];
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ "aria-hidden": ("true"), });
        (__VLS_ctx.editProfileInitial);
        // @ts-ignore
        [editProfileInitial,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-avatar__copy") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-edit-field__label") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-avatar__actions") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ ...{ class: ("profile-action profile-action--compact") }, for: ("profile-avatar-input"), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_41 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("camera"), size: ((16)), }));
    const __VLS_42 = __VLS_41({ name: ("camera"), size: ((16)), }, ...__VLS_functionalComponentArgsRest(__VLS_41));
    ({}({ name: ("camera"), size: ((16)), }));
    const __VLS_45 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_42);
    __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ ...{ onChange: (__VLS_ctx.handleAvatarSelection) }, id: ("profile-avatar-input"), ref: ("profileAvatarInputRef"), ...{ class: ("sr-only") }, type: ("file"), accept: ("image/jpeg,image/png,image/webp"), disabled: ((__VLS_ctx.editSaving)), });
    // @ts-ignore
    (__VLS_ctx.profileAvatarInputRef);
    // @ts-ignore
    [editSaving, handleAvatarSelection, profileAvatarInputRef,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.removeProfileAvatar) }, ...{ class: ("profile-action profile-action--compact") }, type: ("button"), disabled: ((__VLS_ctx.editSaving || !__VLS_ctx.editAvatarHasValue)), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_46 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("image-off"), size: ((16)), }));
    const __VLS_47 = __VLS_46({ name: ("image-off"), size: ((16)), }, ...__VLS_functionalComponentArgsRest(__VLS_46));
    ({}({ name: ("image-off"), size: ((16)), }));
    // @ts-ignore
    [editSaving, removeProfileAvatar, editAvatarHasValue,];
    const __VLS_50 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_47);
    if (__VLS_ctx.editAvatarError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-edit-error") }, role: ("alert"), });
        (__VLS_ctx.editAvatarError);
        // @ts-ignore
        [editAvatarError, editAvatarError,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-field") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-field__label-row") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ for: ("profile-display-name"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.editDisplayNameLength);
    // @ts-ignore
    [editDisplayNameLength,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ id: ("profile-display-name"), ref: ("editDisplayNameInputRef"), value: ((__VLS_ctx.editDraft.display_name)), type: ("text"), autocomplete: ("name"), disabled: ((__VLS_ctx.editSaving)), "aria-describedby": ("profile-display-name-help"), });
    // @ts-ignore
    (__VLS_ctx.editDisplayNameInputRef);
    // @ts-ignore
    [editSaving, editDraft, editDisplayNameInputRef,];
    if (__VLS_ctx.editDisplayNameOverLimit) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ id: ("profile-display-name-help"), ...{ class: ("profile-edit-error") }, role: ("alert"), });
        // @ts-ignore
        [editDisplayNameOverLimit,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-field profile-edit-field--readonly") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("profile-edit-field__label") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.user?.username);
    // @ts-ignore
    [user,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-field") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("profile-edit-field__label-row") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ for: ("profile-bio"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.editBioLength);
    // @ts-ignore
    [editBioLength,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.textarea, __VLS_intrinsicElements.textarea)({ id: ("profile-bio"), value: ((__VLS_ctx.editDraft.bio)), rows: ("4"), disabled: ((__VLS_ctx.editSaving)), "aria-describedby": ("profile-bio-help"), });
    // @ts-ignore
    [editSaving, editDraft,];
    if (__VLS_ctx.editBioOverLimit) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ id: ("profile-bio-help"), ...{ class: ("profile-edit-error") }, role: ("alert"), });
        // @ts-ignore
        [editBioOverLimit,];
    }
    if (__VLS_ctx.editError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("profile-edit-error") }, role: ("alert"), "aria-live": ("polite"), });
        (__VLS_ctx.editError);
        // @ts-ignore
        [editError, editError,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.footer, __VLS_intrinsicElements.footer)({ ...{ class: ("profile-edit-dialog__actions") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.closeEditProfile) }, ...{ class: ("profile-action") }, type: ("button"), disabled: ((__VLS_ctx.editSaving)), });
    // @ts-ignore
    [closeEditProfile, editSaving,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("profile-follow-button") }, type: ("submit"), disabled: ((!__VLS_ctx.editCanSave)), "aria-busy": ((__VLS_ctx.editSaving)), });
    (__VLS_ctx.editSaving ? 'Saving…' : 'Save');
    // @ts-ignore
    [editSaving, editSaving, editCanSave,];
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['profile-view'];
        __VLS_styleScopedClasses['profile-header'];
        __VLS_styleScopedClasses['profile-header__back'];
        __VLS_styleScopedClasses['profile-header__copy'];
        __VLS_styleScopedClasses['profile-identity'];
        __VLS_styleScopedClasses['profile-identity--loading'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--avatar'];
        __VLS_styleScopedClasses['profile-skeleton-copy'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--name'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--handle'];
        __VLS_styleScopedClasses['profile-identity'];
        __VLS_styleScopedClasses['profile-avatar'];
        __VLS_styleScopedClasses['profile-identity__copy'];
        __VLS_styleScopedClasses['profile-identity__handle'];
        __VLS_styleScopedClasses['profile-identity__bio'];
        __VLS_styleScopedClasses['profile-identity__joined'];
        __VLS_styleScopedClasses['profile-social'];
        __VLS_styleScopedClasses['profile-social'];
        __VLS_styleScopedClasses['profile-social--loading'];
        __VLS_styleScopedClasses['profile-social__skeleton'];
        __VLS_styleScopedClasses['profile-social__skeleton'];
        __VLS_styleScopedClasses['profile-social-error'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action--compact'];
        __VLS_styleScopedClasses['profile-identity__action'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action--primary'];
        __VLS_styleScopedClasses['profile-follow-button'];
        __VLS_styleScopedClasses['profile-action-error'];
        __VLS_styleScopedClasses['profile-state'];
        __VLS_styleScopedClasses['profile-state--page'];
        __VLS_styleScopedClasses['profile-state__actions'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-tabs'];
        __VLS_styleScopedClasses['profile-tab'];
        __VLS_styleScopedClasses['profile-tab--active'];
        __VLS_styleScopedClasses['profile-posts'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['profile-skeleton-list'];
        __VLS_styleScopedClasses['profile-skeleton-post'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--identity'];
        __VLS_styleScopedClasses['profile-skeleton-post__copy'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--title'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--excerpt'];
        __VLS_styleScopedClasses['profile-skeleton'];
        __VLS_styleScopedClasses['profile-skeleton--metric'];
        __VLS_styleScopedClasses['profile-state'];
        __VLS_styleScopedClasses['profile-state--inline'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-empty'];
        __VLS_styleScopedClasses['profile-post-list'];
        __VLS_styleScopedClasses['profile-feed-sentinel'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-edit-dialog'];
        __VLS_styleScopedClasses['profile-edit-form'];
        __VLS_styleScopedClasses['profile-edit-dialog__header'];
        __VLS_styleScopedClasses['profile-edit-dialog__close'];
        __VLS_styleScopedClasses['profile-edit-avatar'];
        __VLS_styleScopedClasses['profile-avatar'];
        __VLS_styleScopedClasses['profile-avatar--edit'];
        __VLS_styleScopedClasses['profile-edit-avatar__copy'];
        __VLS_styleScopedClasses['profile-edit-field__label'];
        __VLS_styleScopedClasses['profile-edit-avatar__actions'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action--compact'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-action--compact'];
        __VLS_styleScopedClasses['profile-edit-error'];
        __VLS_styleScopedClasses['profile-edit-field'];
        __VLS_styleScopedClasses['profile-edit-field__label-row'];
        __VLS_styleScopedClasses['profile-edit-error'];
        __VLS_styleScopedClasses['profile-edit-field'];
        __VLS_styleScopedClasses['profile-edit-field--readonly'];
        __VLS_styleScopedClasses['profile-edit-field__label'];
        __VLS_styleScopedClasses['profile-edit-field'];
        __VLS_styleScopedClasses['profile-edit-field__label-row'];
        __VLS_styleScopedClasses['profile-edit-error'];
        __VLS_styleScopedClasses['profile-edit-error'];
        __VLS_styleScopedClasses['profile-edit-dialog__actions'];
        __VLS_styleScopedClasses['profile-action'];
        __VLS_styleScopedClasses['profile-follow-button'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                PostCard: PostCard,
                AppIcon: AppIcon,
                MobileAccountMenu: MobileAccountMenu,
                UserAvatar: UserAvatar,
                skeletonCount: skeletonCount,
                user: user,
                profileLoading: profileLoading,
                profileError: profileError,
                profileNotFound: profileNotFound,
                posts: posts,
                postsInitialLoading: postsInitialLoading,
                postsLoadingMore: postsLoadingMore,
                postsInitialError: postsInitialError,
                postsLoadMoreError: postsLoadMoreError,
                hasMore: hasMore,
                followState: followState,
                followLoading: followLoading,
                followError: followError,
                followActionError: followActionError,
                followPending: followPending,
                likePendingPostIds: likePendingPostIds,
                repostPendingPostIds: repostPendingPostIds,
                pendingDeletePostIds: pendingDeletePostIds,
                deleteErrors: deleteErrors,
                sentinelRef: sentinelRef,
                intersectionObserverAvailable: intersectionObserverAvailable,
                profileDisplayName: profileDisplayName,
                headerUsername: headerUsername,
                joinedLabel: joinedLabel,
                isOwnProfile: isOwnProfile,
                socialReady: socialReady,
                showFollowControl: showFollowControl,
                canDeletePost: canDeletePost,
                retryFollowState: retryFollowState,
                handleFollowToggle: handleFollowToggle,
                editDialogRef: editDialogRef,
                editDisplayNameInputRef: editDisplayNameInputRef,
                profileAvatarInputRef: profileAvatarInputRef,
                editDraft: editDraft,
                editAvatarLoadFailed: editAvatarLoadFailed,
                editAvatarError: editAvatarError,
                editError: editError,
                editSaving: editSaving,
                editDisplayNameLength: editDisplayNameLength,
                editBioLength: editBioLength,
                editDisplayNameOverLimit: editDisplayNameOverLimit,
                editBioOverLimit: editBioOverLimit,
                editProfileDisplayName: editProfileDisplayName,
                editProfileInitial: editProfileInitial,
                editAvatarPreview: editAvatarPreview,
                editAvatarHasValue: editAvatarHasValue,
                editCanSave: editCanSave,
                openEditProfile: openEditProfile,
                closeEditProfile: closeEditProfile,
                handleDialogCancel: handleDialogCancel,
                handleDialogClose: handleDialogClose,
                handleAvatarSelection: handleAvatarSelection,
                removeProfileAvatar: removeProfileAvatar,
                saveProfile: saveProfile,
                retryProfile: retryProfile,
                retryInitialPosts: retryInitialPosts,
                loadMorePosts: loadMorePosts,
                retryLoadMore: retryLoadMore,
                handleDeletePost: handleDeletePost,
                handleLikeToggle: handleLikeToggle,
                handleRepostToggle: handleRepostToggle,
                goHome: goHome,
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
