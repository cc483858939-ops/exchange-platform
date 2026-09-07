/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import UserRow from '../components/users/UserRow.vue';
import { useAuthStore } from '../store/auth';
import { normalizeSearchQuery, useSearchSessionStore } from '../store/searchSession';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const searchSession = useSearchSessionStore();
const { viewerID, query, inputValue, items, loaded, initialLoading, initialError, nextOffset, hasMore, loadingMore, loadMoreError, pendingMutationIDs, mutationErrors, } = storeToRefs(searchSession);
const sentinelRef = ref(null);
let observer = null;
let mounted = false;
let searchEntryVersion = 0;
let restoredEntryVersion = -1;
const routeQuery = computed(() => normalizeSearchQuery(typeof route.query.q === 'string' ? route.query.q : ''));
const currentViewerID = computed(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const disconnectObserver = () => { observer?.disconnect(); observer = null; };
const updateObserver = async () => {
    await nextTick();
    disconnectObserver();
    if (!query.value || !hasMore.value || loadingMore.value || loadMoreError.value || !sentinelRef.value || !('IntersectionObserver' in window))
        return;
    observer = new IntersectionObserver((entries) => { if (entries.some((entry) => entry.isIntersecting))
        void searchSession.loadMore(); }, { rootMargin: '240px 0px' });
    observer.observe(sentinelRef.value);
};
const restoreScrollOnce = async () => {
    const entryVersion = searchEntryVersion;
    if (!mounted || restoredEntryVersion === entryVersion || !query.value || !loaded.value || initialLoading.value)
        return;
    await nextTick();
    if (!mounted || entryVersion !== searchEntryVersion || restoredEntryVersion === entryVersion)
        return;
    if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
        window.scrollTo({ top: searchSession.scrollY, behavior: 'auto' });
    }
    restoredEntryVersion = entryVersion;
};
const submit = async () => {
    const submitted = normalizeSearchQuery(inputValue.value);
    if (!submitted) {
        await clearSearch();
        return;
    }
    inputValue.value = submitted;
    if (submitted === query.value) {
        reload();
        return;
    }
    await router.push({ name: 'UserSearch', query: { ...route.query, q: submitted } });
};
const clearSearch = async () => {
    inputValue.value = '';
    const nextQuery = { ...route.query };
    delete nextQuery.q;
    await router.push({ name: 'UserSearch', query: nextQuery });
};
const reload = () => { searchSession.reload(); };
const loadMore = () => { void searchSession.loadMore(); };
const toggleFollow = (userID) => { void searchSession.toggleFollow(userID); };
watch(currentViewerID, (nextID) => {
    searchEntryVersion += 1;
    searchSession.setViewer(nextID);
}, { immediate: true });
watch(routeQuery, (nextQuery) => {
    searchEntryVersion += 1;
    searchSession.activateQuery(nextQuery);
    void restoreScrollOnce();
}, { immediate: true });
watch([loaded, initialLoading, initialError], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([hasMore, loadingMore, loadMoreError, () => items.value.length], () => { void updateObserver(); }, { flush: 'post' });
onMounted(() => {
    mounted = true;
    void restoreScrollOnce();
});
onBeforeUnmount(() => {
    mounted = false;
    if (typeof window !== 'undefined')
        searchSession.saveScroll(window.scrollY);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("search-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("search-view__header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
    if (!__VLS_ctx.authStore.isAuthenticated) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__state") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        // @ts-ignore
        [authStore,];
        const __VLS_0 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("search-view__button") }, to: (({ name: 'Login' })), }));
        const __VLS_2 = __VLS_1({ ...{ class: ("search-view__button") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ class: ("search-view__button") }, to: (({ name: 'Login' })), }));
        (__VLS_5.slots).default;
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({ ...{ onSubmit: (__VLS_ctx.submit) }, ...{ class: ("search-view__form") }, role: ("search"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ ...{ class: ("search-view__field") }, });
        // @ts-ignore
        [AppIcon,];
        const __VLS_6 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("search"), size: ((20)), }));
        const __VLS_7 = __VLS_6({ name: ("search"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ name: ("search"), size: ((20)), }));
        // @ts-ignore
        [submit,];
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_7);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("sr-only") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ type: ("search"), "aria-label": ("Search people"), placeholder: ("Search people"), maxlength: ("200"), });
        (__VLS_ctx.inputValue);
        // @ts-ignore
        [inputValue,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("search-view__button") }, type: ("submit"), });
        if (__VLS_ctx.query) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.clearSearch) }, ...{ class: ("search-view__clear") }, type: ("button"), "aria-label": ("Clear search"), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_11 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("close"), size: ((18)), }));
            const __VLS_12 = __VLS_11({ name: ("close"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_11));
            ({}({ name: ("close"), size: ((18)), }));
            // @ts-ignore
            [query, clearSearch,];
            const __VLS_15 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_12);
        }
        if (!__VLS_ctx.query) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__state") }, });
            // @ts-ignore
            [query,];
        }
        else if (__VLS_ctx.initialLoading) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__state") }, "aria-live": ("polite"), });
            // @ts-ignore
            [initialLoading,];
        }
        else if (__VLS_ctx.initialError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__state search-view__state--error") }, role: ("alert"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
            (__VLS_ctx.initialError);
            // @ts-ignore
            [initialError, initialError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.reload) }, ...{ class: ("search-view__button") }, type: ("button"), });
            // @ts-ignore
            [reload,];
        }
        else if (__VLS_ctx.items.length === 0) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__state") }, });
            (__VLS_ctx.query);
            // @ts-ignore
            [query, items,];
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("search-view__results") }, "aria-label": ("People search results"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("search-view__results-heading") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
            for (const [item] of __VLS_getVForSourceType((__VLS_ctx.items))) {
                // @ts-ignore
                [UserRow,];
                const __VLS_16 = __VLS_asFunctionalComponent(UserRow, new UserRow({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }));
                const __VLS_17 = __VLS_16({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }, ...__VLS_functionalComponentArgsRest(__VLS_16));
                ({}({ ...{ 'onToggleFollow': {} }, key: ((item.user.id)), item: ((item)), pending: ((__VLS_ctx.pendingMutationIDs.has(item.user.id))), error: ((__VLS_ctx.mutationErrors.get(item.user.id))), isSelf: ((item.user.id === __VLS_ctx.viewerID)), }));
                let __VLS_21;
                const __VLS_22 = {
                    onToggleFollow: (__VLS_ctx.toggleFollow)
                };
                // @ts-ignore
                [items, pendingMutationIDs, mutationErrors, viewerID, toggleFollow,];
                const __VLS_20 = __VLS_pickFunctionalComponentCtx(UserRow, __VLS_17);
                let __VLS_18;
                let __VLS_19;
            }
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("sentinelRef"), ...{ class: ("search-view__sentinel") }, "aria-hidden": ("true"), });
            // @ts-ignore
            (__VLS_ctx.sentinelRef);
            // @ts-ignore
            [sentinelRef,];
            if (__VLS_ctx.loadingMore) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("search-view__more") }, "aria-live": ("polite"), });
                // @ts-ignore
                [loadingMore,];
            }
            else if (__VLS_ctx.loadMoreError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("search-view__more search-view__more--error") }, role: ("alert"), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                (__VLS_ctx.loadMoreError);
                // @ts-ignore
                [loadMoreError, loadMoreError,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("search-view__button") }, type: ("button"), });
                // @ts-ignore
                [loadMore,];
            }
            else if (__VLS_ctx.hasMore) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("search-view__more") }, });
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("search-view__button") }, type: ("button"), });
                // @ts-ignore
                [loadMore, hasMore,];
            }
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['search-view'];
        __VLS_styleScopedClasses['search-view__header'];
        __VLS_styleScopedClasses['search-view__state'];
        __VLS_styleScopedClasses['search-view__button'];
        __VLS_styleScopedClasses['search-view__form'];
        __VLS_styleScopedClasses['search-view__field'];
        __VLS_styleScopedClasses['sr-only'];
        __VLS_styleScopedClasses['search-view__button'];
        __VLS_styleScopedClasses['search-view__clear'];
        __VLS_styleScopedClasses['search-view__state'];
        __VLS_styleScopedClasses['search-view__state'];
        __VLS_styleScopedClasses['search-view__state'];
        __VLS_styleScopedClasses['search-view__state--error'];
        __VLS_styleScopedClasses['search-view__button'];
        __VLS_styleScopedClasses['search-view__state'];
        __VLS_styleScopedClasses['search-view__results'];
        __VLS_styleScopedClasses['search-view__results-heading'];
        __VLS_styleScopedClasses['search-view__sentinel'];
        __VLS_styleScopedClasses['search-view__more'];
        __VLS_styleScopedClasses['search-view__more'];
        __VLS_styleScopedClasses['search-view__more--error'];
        __VLS_styleScopedClasses['search-view__button'];
        __VLS_styleScopedClasses['search-view__more'];
        __VLS_styleScopedClasses['search-view__button'];
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
                authStore: authStore,
                viewerID: viewerID,
                query: query,
                inputValue: inputValue,
                items: items,
                initialLoading: initialLoading,
                initialError: initialError,
                hasMore: hasMore,
                loadingMore: loadingMore,
                loadMoreError: loadMoreError,
                pendingMutationIDs: pendingMutationIDs,
                mutationErrors: mutationErrors,
                sentinelRef: sentinelRef,
                submit: submit,
                clearSearch: clearSearch,
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
