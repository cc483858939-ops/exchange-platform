/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRoute } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import UserRow from '../components/users/UserRow.vue';
import { useConnectionsSessionStore, } from '../store/connectionsSession';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const route = useRoute();
const connectionsSession = useConnectionsSessionStore();
const { viewerID, pendingMutationIDs, mutationErrors, } = storeToRefs(connectionsSession);
const targetID = computed(() => String(route.params.id ?? '').trim());
const numericTargetID = computed(() => {
    const value = Number(targetID.value);
    return Number.isSafeInteger(value) && value > 0 ? value : null;
});
const mode = computed(() => (route.name === 'UserFollowers'
    ? 'followers'
    : route.name === 'UserFollowing'
        ? 'following'
        : null));
const activeTargetSession = computed(() => (numericTargetID.value === null
    ? undefined
    : connectionsSession.getTargetSession(numericTargetID.value)));
const activeModeSession = computed(() => {
    const target = activeTargetSession.value;
    return target && mode.value ? target[mode.value] : undefined;
});
const profile = computed(() => activeTargetSession.value?.profile ?? null);
const profileLoading = computed(() => activeTargetSession.value?.profileLoading ?? false);
const profileError = computed(() => activeTargetSession.value?.profileError ?? '');
const items = computed(() => activeModeSession.value?.items ?? []);
const initialLoading = computed(() => activeModeSession.value?.initialLoading ?? false);
const initialError = computed(() => activeModeSession.value?.initialError ?? '');
const loadingMore = computed(() => activeModeSession.value?.loadingMore ?? false);
const loadMoreError = computed(() => activeModeSession.value?.loadMoreError ?? '');
const hasMore = computed(() => activeModeSession.value?.hasMore ?? false);
const loaded = computed(() => activeModeSession.value?.loaded ?? false);
const stale = computed(() => activeModeSession.value?.stale ?? false);
const revalidating = computed(() => activeModeSession.value?.revalidating ?? false);
const displayName = computed(() => profile.value?.display_name.trim() || profile.value?.username || 'Profile');
const modeLabel = computed(() => mode.value === 'followers' ? 'Followers' : 'Following');
const emptyCopy = computed(() => mode.value === 'followers' ? 'No followers yet.' : 'Not following anyone yet.');
const sentinelRef = ref(null);
let observer = null;
let mounted = false;
let entryVersion = 0;
let restoredEntryVersion = -1;
const disconnectObserver = () => {
    observer?.disconnect();
    observer = null;
};
const updateObserver = async () => {
    await nextTick();
    disconnectObserver();
    if (!mounted
        || numericTargetID.value === null
        || mode.value === null
        || !('IntersectionObserver' in window)
        || !sentinelRef.value
        || !hasMore.value
        || loadingMore.value
        || loadMoreError.value
        || stale.value
        || revalidating.value)
        return;
    observer = new IntersectionObserver((entries) => {
        if (entries.some(entry => entry.isIntersecting)) {
            void connectionsSession.loadMore(numericTargetID.value, mode.value);
        }
    }, { rootMargin: '240px 0px' });
    observer.observe(sentinelRef.value);
};
const restoreScrollOnce = async () => {
    const capturedEntryVersion = entryVersion;
    const activeSession = activeModeSession.value;
    if (!mounted
        || restoredEntryVersion === capturedEntryVersion
        || !activeSession
        || !activeSession.loaded
        || activeSession.initialLoading)
        return;
    await nextTick();
    if (!mounted
        || capturedEntryVersion !== entryVersion
        || restoredEntryVersion === capturedEntryVersion)
        return;
    if (typeof window !== 'undefined'
        && typeof window.scrollTo === 'function') {
        window.scrollTo({ top: activeSession.scrollY, behavior: 'auto' });
    }
    restoredEntryVersion = capturedEntryVersion;
};
const reload = () => {
    if (numericTargetID.value !== null && mode.value !== null) {
        connectionsSession.reload(numericTargetID.value, mode.value);
    }
};
const loadMore = () => {
    if (numericTargetID.value !== null && mode.value !== null) {
        void connectionsSession.loadMore(numericTargetID.value, mode.value);
    }
};
const toggleFollow = (userID) => {
    void connectionsSession.toggleFollow(userID);
};
watch([targetID, mode, viewerID], ([nextTargetID, nextMode, nextViewerID], previousValues) => {
    const [previousTargetID, previousMode, previousViewerID] = previousValues ?? [];
    if (previousViewerID === nextViewerID
        && previousTargetID
        && previousMode
        && typeof window !== 'undefined') {
        connectionsSession.saveScroll(previousTargetID, previousMode, window.scrollY);
    }
    entryVersion += 1;
    restoredEntryVersion = -1;
    if (nextViewerID !== null && nextTargetID && nextMode) {
        connectionsSession.activate(Number(nextTargetID), nextMode);
    }
}, { immediate: true });
watch([targetID, mode, loaded, initialLoading], () => {
    void restoreScrollOnce();
}, { flush: 'post' });
watch([targetID, mode, hasMore, loadingMore, loadMoreError, () => items.value.length, stale, revalidating], () => {
    void updateObserver();
}, { flush: 'post' });
watch([targetID, mode, loaded, stale], ([nextTargetID, nextMode, isLoaded, isStale]) => {
    if (nextTargetID && nextMode && isLoaded && isStale) {
        void connectionsSession.revalidateMode(Number(nextTargetID), nextMode);
    }
}, { flush: 'post' });
onMounted(() => {
    mounted = true;
    void restoreScrollOnce();
});
onBeforeUnmount(() => {
    mounted = false;
    if (numericTargetID.value !== null && mode.value !== null && typeof window !== 'undefined') {
        connectionsSession.saveScroll(numericTargetID.value, mode.value, window.scrollY);
    }
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("connections-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("connections-header") }, });
    if (__VLS_ctx.profile) {
        const __VLS_0 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("connections-header__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.profile.id } })), "aria-label": ((`Back to ${__VLS_ctx.displayName}'s profile`)), }));
        const __VLS_2 = __VLS_1({ ...{ class: ("connections-header__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.profile.id } })), "aria-label": ((`Back to ${__VLS_ctx.displayName}'s profile`)), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ class: ("connections-header__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.profile.id } })), "aria-label": ((`Back to ${__VLS_ctx.displayName}'s profile`)), }));
        // @ts-ignore
        [AppIcon,];
        const __VLS_6 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((22)), }));
        const __VLS_7 = __VLS_6({ name: ("arrow-left"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ name: ("arrow-left"), size: ((22)), }));
        // @ts-ignore
        [profile, profile, displayName,];
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_7);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("connections-header__copy") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.displayName);
        // @ts-ignore
        [displayName,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.profile.username);
        // @ts-ignore
        [profile,];
        (__VLS_5.slots).default;
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    }
    else if (__VLS_ctx.profileLoading) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("connections-header__skeleton") }, "aria-label": ("Loading profile"), });
        // @ts-ignore
        [profileLoading,];
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("connections-header__error") }, });
        (__VLS_ctx.profileError || 'Profile could not be loaded.');
        // @ts-ignore
        [profileError,];
    }
    if (__VLS_ctx.profile) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({ ...{ class: ("connections-tabs") }, "aria-label": ("Connections"), });
        const __VLS_11 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_12 = __VLS_asFunctionalComponent(__VLS_11, new __VLS_11({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.profile.id } })), }));
        const __VLS_13 = __VLS_12({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.profile.id } })), }, ...__VLS_functionalComponentArgsRest(__VLS_12));
        ({}({ to: (({ name: 'UserFollowing', params: { id: __VLS_ctx.profile.id } })), }));
        // @ts-ignore
        [profile, profile,];
        (__VLS_16.slots).default;
        const __VLS_16 = __VLS_pickFunctionalComponentCtx(__VLS_11, __VLS_13);
        const __VLS_17 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_18 = __VLS_asFunctionalComponent(__VLS_17, new __VLS_17({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.profile.id } })), }));
        const __VLS_19 = __VLS_18({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.profile.id } })), }, ...__VLS_functionalComponentArgsRest(__VLS_18));
        ({}({ to: (({ name: 'UserFollowers', params: { id: __VLS_ctx.profile.id } })), }));
        // @ts-ignore
        [profile,];
        (__VLS_22.slots).default;
        const __VLS_22 = __VLS_pickFunctionalComponentCtx(__VLS_17, __VLS_19);
    }
    if (__VLS_ctx.profile && __VLS_ctx.initialLoading) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("connections-state") }, "aria-live": ("polite"), });
        (__VLS_ctx.modeLabel.toLowerCase());
        // @ts-ignore
        [profile, initialLoading, modeLabel,];
    }
    else if (__VLS_ctx.profile && __VLS_ctx.initialError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("connections-state connections-state--error") }, role: ("alert"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.initialError);
        // @ts-ignore
        [profile, initialError, initialError,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.reload) }, ...{ class: ("connections-button") }, type: ("button"), });
        // @ts-ignore
        [reload,];
    }
    else if (__VLS_ctx.profile && __VLS_ctx.items.length === 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("connections-state") }, });
        (__VLS_ctx.emptyCopy);
        // @ts-ignore
        [profile, items, emptyCopy,];
    }
    else if (__VLS_ctx.profile) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("connections-list") }, "aria-label": ("User connections"), });
        for (const [item] of __VLS_getVForSourceType((__VLS_ctx.items))) {
            // @ts-ignore
            [UserRow,];
            const __VLS_23 = __VLS_asFunctionalComponent(UserRow, new UserRow({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }));
            const __VLS_24 = __VLS_23({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }, ...__VLS_functionalComponentArgsRest(__VLS_23));
            ({}({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }));
            let __VLS_28;
            const __VLS_29 = {
                onToggleFollow: (__VLS_ctx.toggleFollow)
            };
            // @ts-ignore
            [profile, items, pendingMutationIDs, mutationErrors, viewerID, toggleFollow,];
            const __VLS_27 = __VLS_pickFunctionalComponentCtx(UserRow, __VLS_24);
            let __VLS_25;
            let __VLS_26;
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("sentinelRef"), ...{ class: ("connections-sentinel") }, "aria-hidden": ("true"), });
        // @ts-ignore
        (__VLS_ctx.sentinelRef);
        // @ts-ignore
        [sentinelRef,];
        if (__VLS_ctx.loadingMore) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("connections-more") }, "aria-live": ("polite"), });
            // @ts-ignore
            [loadingMore,];
        }
        else if (__VLS_ctx.loadMoreError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("connections-more connections-more--error") }, role: ("alert"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.loadMoreError);
            // @ts-ignore
            [loadMoreError, loadMoreError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("connections-button") }, type: ("button"), });
            // @ts-ignore
            [loadMore,];
        }
        else if (__VLS_ctx.hasMore) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("connections-more") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("connections-button") }, type: ("button"), });
            // @ts-ignore
            [loadMore, hasMore,];
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['connections-view'];
        __VLS_styleScopedClasses['connections-header'];
        __VLS_styleScopedClasses['connections-header__identity'];
        __VLS_styleScopedClasses['connections-header__copy'];
        __VLS_styleScopedClasses['connections-header__skeleton'];
        __VLS_styleScopedClasses['connections-header__error'];
        __VLS_styleScopedClasses['connections-tabs'];
        __VLS_styleScopedClasses['connections-state'];
        __VLS_styleScopedClasses['connections-state'];
        __VLS_styleScopedClasses['connections-state--error'];
        __VLS_styleScopedClasses['connections-button'];
        __VLS_styleScopedClasses['connections-state'];
        __VLS_styleScopedClasses['connections-list'];
        __VLS_styleScopedClasses['connections-sentinel'];
        __VLS_styleScopedClasses['connections-more'];
        __VLS_styleScopedClasses['connections-more'];
        __VLS_styleScopedClasses['connections-more--error'];
        __VLS_styleScopedClasses['connections-button'];
        __VLS_styleScopedClasses['connections-more'];
        __VLS_styleScopedClasses['connections-button'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                UserRow: UserRow,
                viewerID: viewerID,
                pendingMutationIDs: pendingMutationIDs,
                mutationErrors: mutationErrors,
                profile: profile,
                profileLoading: profileLoading,
                profileError: profileError,
                items: items,
                initialLoading: initialLoading,
                initialError: initialError,
                loadingMore: loadingMore,
                loadMoreError: loadMoreError,
                hasMore: hasMore,
                displayName: displayName,
                modeLabel: modeLabel,
                emptyCopy: emptyCopy,
                sentinelRef: sentinelRef,
                reload: reload,
                loadMore: loadMore,
                toggleFollow: toggleFollow,
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
