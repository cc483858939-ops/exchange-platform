/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useHistorySessionStore } from '../store/historySession';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const router = useRouter();
const historySession = useHistorySessionStore();
const { viewerID: currentViewerID, items: historyPosts, loaded, initialLoading, initialError, nextCursor, loadingMore, loadMoreError, stale, revalidating, scrollY, pendingUnlikePostIDs: likePendingPostIDs, repostPendingPostIDs, mutationErrors, } = storeToRefs(historySession);
const historyIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
const historySentinelRef = ref(null);
const skeletonPosts = [0, 1, 2];
let observer = null;
let mounted = false;
let entryVersion = 0;
let restoredEntryVersion = -1;
const unlikeError = computed(() => Array.from(mutationErrors.value.values())[0] ?? '');
const showEmpty = computed(() => (loaded.value
    && !initialLoading.value
    && !initialError.value
    && historyPosts.value.length === 0
    && nextCursor.value === null));
const disconnectObserver = () => {
    observer?.disconnect();
    observer = null;
};
const updateObserver = async () => {
    await nextTick();
    disconnectObserver();
    if (!historyIntersectionObserverAvailable
        || !historySentinelRef.value
        || !nextCursor.value
        || loadingMore.value
        || loadMoreError.value
        || stale.value
        || revalidating.value
        || currentViewerID.value === null)
        return;
    observer = new IntersectionObserver((entries) => {
        if (entries.some(entry => entry.isIntersecting)) {
            void historySession.loadMore();
        }
    }, { rootMargin: '240px 0px' });
    observer.observe(historySentinelRef.value);
};
const restoreScrollOnce = async () => {
    const capturedEntryVersion = entryVersion;
    if (!mounted
        || restoredEntryVersion === capturedEntryVersion
        || !loaded.value
        || initialLoading.value)
        return;
    await nextTick();
    if (!mounted
        || capturedEntryVersion !== entryVersion
        || restoredEntryVersion === capturedEntryVersion)
        return;
    if (typeof window !== 'undefined'
        && typeof window.scrollTo === 'function') {
        window.scrollTo({ top: scrollY.value, behavior: 'auto' });
    }
    restoredEntryVersion = capturedEntryVersion;
};
const retryInitial = () => { historySession.retryInitial(); };
const retryLoadMore = () => { historySession.retryLoadMore(); };
const loadMore = () => { void historySession.loadMore(); };
const handleLikeToggle = (postID) => { void historySession.toggleUnlike(postID); };
const handleRepostToggle = (postID) => { void historySession.toggleRepost(postID); };
const goBack = () => {
    const historyState = window.history.state;
    if (historyState?.back) {
        router.back();
        return;
    }
    void router.push({ name: 'Home' });
};
watch(currentViewerID, (nextViewerID) => {
    entryVersion += 1;
    restoredEntryVersion = -1;
    if (nextViewerID !== null) {
        void historySession.loadInitial();
    }
}, { immediate: true });
watch([currentViewerID, loaded, initialLoading], () => {
    void restoreScrollOnce();
}, { flush: 'post' });
watch([nextCursor, loadingMore, loadMoreError, () => historyPosts.value.length, stale, revalidating], () => {
    void updateObserver();
}, { flush: 'post' });
watch([loaded, stale], ([isLoaded, isStale]) => {
    if (isLoaded && isStale) {
        void historySession.revalidateHistory();
    }
}, { flush: 'post' });
onMounted(() => {
    mounted = true;
    void restoreScrollOnce();
});
onBeforeUnmount(() => {
    mounted = false;
    if (typeof window !== 'undefined')
        historySession.saveScroll(window.scrollY);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("history-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("history-view__header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.goBack) }, ...{ class: ("history-view__back") }, type: ("button"), "aria-label": ("Back"), });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((20)), }));
    const __VLS_1 = __VLS_0({ name: ("arrow-left"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("arrow-left"), size: ((20)), }));
    // @ts-ignore
    [goBack,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    if (__VLS_ctx.currentViewerID === null) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("history-view__state history-view__state--auth") }, "aria-labelledby": ("history-auth-title"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ id: ("history-auth-title"), });
        // @ts-ignore
        [currentViewerID,];
        const __VLS_5 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_6 = __VLS_asFunctionalComponent(__VLS_5, new __VLS_5({ ...{ class: ("history-view__primary") }, to: (({ name: 'Login' })), }));
        const __VLS_7 = __VLS_6({ ...{ class: ("history-view__primary") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ ...{ class: ("history-view__primary") }, to: (({ name: 'Login' })), }));
        (__VLS_10.slots).default;
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(__VLS_5, __VLS_7);
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({ ...{ class: ("history-view__tabs") }, "aria-label": ("History sections"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("history-view__tab history-view__tab--active") }, type: ("button"), "aria-selected": ("true"), });
        if (__VLS_ctx.initialLoading && !__VLS_ctx.loaded) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("history-view__feed history-view__feed--loading") }, "aria-live": ("polite"), });
            for (const [skeleton] of __VLS_getVForSourceType((__VLS_ctx.skeletonPosts))) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ key: ((skeleton)), ...{ class: ("history-skeleton") }, "aria-hidden": ("true"), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("history-skeleton__author") }, });
                // @ts-ignore
                [initialLoading, loaded, skeletonPosts,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("history-skeleton__title") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("history-skeleton__line") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("history-skeleton__line history-skeleton__line--short") }, });
            }
        }
        else if (__VLS_ctx.initialError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("history-view__state") }, role: ("alert"), "aria-live": ("polite"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
            // @ts-ignore
            [initialError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryInitial) }, ...{ class: ("history-view__primary") }, type: ("button"), });
            // @ts-ignore
            [retryInitial,];
        }
        else if (__VLS_ctx.showEmpty) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("history-view__state") }, "aria-live": ("polite"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
            // @ts-ignore
            [showEmpty,];
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("history-view__feed") }, "aria-label": ("Liked posts"), });
            for (const [post] of __VLS_getVForSourceType((__VLS_ctx.historyPosts))) {
                // @ts-ignore
                [PostCard,];
                const __VLS_11 = __VLS_asFunctionalComponent(PostCard, new PostCard({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, key: ((post.id)), post: ((post)), trackView: ((false)), likePending: ((__VLS_ctx.likePendingPostIDs.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIDs.has(post.id))), }));
                const __VLS_12 = __VLS_11({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, key: ((post.id)), post: ((post)), trackView: ((false)), likePending: ((__VLS_ctx.likePendingPostIDs.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIDs.has(post.id))), }, ...__VLS_functionalComponentArgsRest(__VLS_11));
                ({}({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, key: ((post.id)), post: ((post)), trackView: ((false)), likePending: ((__VLS_ctx.likePendingPostIDs.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIDs.has(post.id))), }));
                let __VLS_16;
                const __VLS_17 = {
                    onToggleLike: (__VLS_ctx.handleLikeToggle)
                };
                const __VLS_18 = {
                    onToggleRepost: (__VLS_ctx.handleRepostToggle)
                };
                // @ts-ignore
                [historyPosts, likePendingPostIDs, repostPendingPostIDs, handleLikeToggle, handleRepostToggle,];
                const __VLS_15 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_12);
                let __VLS_13;
                let __VLS_14;
            }
            if (__VLS_ctx.nextCursor || __VLS_ctx.loadingMore || __VLS_ctx.loadMoreError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("historySentinelRef"), ...{ class: ("history-view__sentinel") }, "aria-live": ("polite"), });
                // @ts-ignore
                (__VLS_ctx.historySentinelRef);
                if (__VLS_ctx.loadingMore) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [nextCursor, loadingMore, loadingMore, loadMoreError, historySentinelRef,];
                }
                else if (__VLS_ctx.loadMoreError) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [loadMoreError,];
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryLoadMore) }, ...{ class: ("history-view__primary") }, type: ("button"), });
                    // @ts-ignore
                    [retryLoadMore,];
                }
                else if (!__VLS_ctx.historyIntersectionObserverAvailable && __VLS_ctx.nextCursor) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("history-view__primary") }, type: ("button"), });
                    // @ts-ignore
                    [nextCursor, historyIntersectionObserverAvailable, loadMore,];
                }
            }
            if (__VLS_ctx.unlikeError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("history-view__inline-error") }, role: ("status"), "aria-live": ("polite"), });
                (__VLS_ctx.unlikeError);
                // @ts-ignore
                [unlikeError, unlikeError,];
            }
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['history-view'];
        __VLS_styleScopedClasses['history-view__header'];
        __VLS_styleScopedClasses['history-view__back'];
        __VLS_styleScopedClasses['history-view__state'];
        __VLS_styleScopedClasses['history-view__state--auth'];
        __VLS_styleScopedClasses['history-view__primary'];
        __VLS_styleScopedClasses['history-view__tabs'];
        __VLS_styleScopedClasses['history-view__tab'];
        __VLS_styleScopedClasses['history-view__tab--active'];
        __VLS_styleScopedClasses['history-view__feed'];
        __VLS_styleScopedClasses['history-view__feed--loading'];
        __VLS_styleScopedClasses['history-skeleton'];
        __VLS_styleScopedClasses['history-skeleton__author'];
        __VLS_styleScopedClasses['history-skeleton__title'];
        __VLS_styleScopedClasses['history-skeleton__line'];
        __VLS_styleScopedClasses['history-skeleton__line'];
        __VLS_styleScopedClasses['history-skeleton__line--short'];
        __VLS_styleScopedClasses['history-view__state'];
        __VLS_styleScopedClasses['history-view__primary'];
        __VLS_styleScopedClasses['history-view__state'];
        __VLS_styleScopedClasses['history-view__feed'];
        __VLS_styleScopedClasses['history-view__sentinel'];
        __VLS_styleScopedClasses['history-view__primary'];
        __VLS_styleScopedClasses['history-view__primary'];
        __VLS_styleScopedClasses['history-view__inline-error'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                PostCard: PostCard,
                currentViewerID: currentViewerID,
                historyPosts: historyPosts,
                loaded: loaded,
                initialLoading: initialLoading,
                initialError: initialError,
                nextCursor: nextCursor,
                loadingMore: loadingMore,
                loadMoreError: loadMoreError,
                likePendingPostIDs: likePendingPostIDs,
                repostPendingPostIDs: repostPendingPostIDs,
                historyIntersectionObserverAvailable: historyIntersectionObserverAvailable,
                historySentinelRef: historySentinelRef,
                skeletonPosts: skeletonPosts,
                unlikeError: unlikeError,
                showEmpty: showEmpty,
                retryInitial: retryInitial,
                retryLoadMore: retryLoadMore,
                loadMore: loadMore,
                handleLikeToggle: handleLikeToggle,
                handleRepostToggle: handleRepostToggle,
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
