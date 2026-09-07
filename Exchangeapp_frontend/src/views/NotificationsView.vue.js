/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';
import { useNotificationStore } from '../store/notification';
import AppIcon from '../components/icons/AppIcon.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const authStore = useAuthStore();
const notificationStore = useNotificationStore();
const router = useRouter();
const { items, nextCursor, loaded, loading, error, loadingMore, loadMoreError, pendingReadIDs, markAllPending, } = storeToRefs(notificationStore);
const sentinel = ref(null);
const observerAvailable = ref(typeof IntersectionObserver !== 'undefined');
let observer = null;
let mounted = false;
let notificationEntryVersion = 0;
let restoredEntryVersion = -1;
const hasUnread = computed(() => items.value.some((item) => !item.read));
const currentViewerID = computed(() => (authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null));
const disconnectObserver = () => {
    observer?.disconnect();
    observer = null;
};
const setupObserver = async () => {
    disconnectObserver();
    if (!observerAvailable.value || !nextCursor.value || !sentinel.value || notificationStore.listStale || notificationStore.revalidating) {
        return;
    }
    await nextTick();
    if (!sentinel.value) {
        return;
    }
    observer = new IntersectionObserver((entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
            void notificationStore.loadMore();
        }
    }, { rootMargin: '240px 0px' });
    observer.observe(sentinel.value);
};
const loadInitial = () => { void notificationStore.loadInitial(true); };
const loadMore = () => { void notificationStore.loadMore(); };
const restoreScrollOnce = async () => {
    const entryVersion = notificationEntryVersion;
    if (!mounted || restoredEntryVersion === entryVersion || !loaded.value || loading.value) {
        return;
    }
    await nextTick();
    if (!mounted || entryVersion !== notificationEntryVersion || restoredEntryVersion === entryVersion) {
        return;
    }
    if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
        window.scrollTo({ top: notificationStore.scrollY, behavior: 'auto' });
    }
    restoredEntryVersion = entryVersion;
};
const openNotification = (item) => {
    void notificationStore.markNotificationRead(item.id).catch(() => undefined);
    if (item.type === 'user_followed') {
        void router.push({ name: 'UserProfile', params: { id: String(item.actor.id) } });
    }
    else if (item.post_id !== null) {
        void router.push({ name: 'PostDetail', params: { id: String(item.post_id) } });
    }
};
const markAll = () => { void notificationStore.markAllRead().catch(() => undefined); };
const notificationCopy = (item) => {
    switch (item.type) {
        case 'post_liked': return 'liked your post.';
        case 'post_replied': return 'replied to your post.';
        case 'user_followed': return 'followed you.';
    }
};
const formatActivityAt = (value) => {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};
watch(currentViewerID, () => {
    notificationEntryVersion += 1;
    void notificationStore.loadInitial();
}, { immediate: true });
watch([loaded, loading, error], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([
    nextCursor,
    loadingMore,
    loadMoreError,
    () => items.value.length,
    () => notificationStore.listStale,
    () => notificationStore.revalidating,
], () => { void setupObserver(); }, { flush: 'post' });
onMounted(() => {
    mounted = true;
    void restoreScrollOnce();
});
onBeforeUnmount(() => {
    mounted = false;
    if (typeof window !== 'undefined')
        notificationStore.saveScroll(window.scrollY);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("notifications-page") }, "aria-labelledby": ("notifications-title"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("notifications-page__header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("notifications-page__eyebrow") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({ id: ("notifications-title"), });
    if (__VLS_ctx.hasUnread && !__VLS_ctx.markAllPending) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.markAll) }, ...{ class: ("notifications-page__mark-all") }, type: ("button"), });
        // @ts-ignore
        [hasUnread, markAllPending, markAll,];
    }
    else if (__VLS_ctx.markAllPending) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notifications-page__pending-label") }, });
        // @ts-ignore
        [markAllPending,];
    }
    if (!__VLS_ctx.authStore.isAuthenticated) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__state") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        // @ts-ignore
        [authStore,];
        const __VLS_0 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("notifications-page__action") }, to: (({ name: 'Login' })), }));
        const __VLS_2 = __VLS_1({ ...{ class: ("notifications-page__action") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ class: ("notifications-page__action") }, to: (({ name: 'Login' })), }));
        (__VLS_5.slots).default;
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    }
    else if (__VLS_ctx.loading && __VLS_ctx.items.length === 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__state") }, "aria-live": ("polite"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        // @ts-ignore
        [loading, items,];
    }
    else if (__VLS_ctx.error && __VLS_ctx.items.length === 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__state notifications-page__state--error") }, role: ("alert"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        // @ts-ignore
        [items, error,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadInitial) }, ...{ class: ("notifications-page__action") }, type: ("button"), });
        // @ts-ignore
        [loadInitial,];
    }
    else if (__VLS_ctx.items.length === 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__state") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notifications-page__empty-icon") }, "aria-hidden": ("true"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_6 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("notifications"), size: ((28)), }));
        const __VLS_7 = __VLS_6({ name: ("notifications"), size: ((28)), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ name: ("notifications"), size: ((28)), }));
        // @ts-ignore
        [items,];
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_7);
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__list") }, "aria-live": ("polite"), });
        for (const [item] of __VLS_getVForSourceType((__VLS_ctx.items))) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ key: ((item.id)), ...{ class: ("notification-card") }, ...{ class: (({ 'notification-card--unread': !item.read })) }, });
            __VLS_styleScopedClasses = ({ 'notification-card--unread': !item.read });
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                        if (!(!((!__VLS_ctx.authStore.isAuthenticated))))
                            return;
                        if (!(!((__VLS_ctx.loading && __VLS_ctx.items.length === 0))))
                            return;
                        if (!(!((__VLS_ctx.error && __VLS_ctx.items.length === 0))))
                            return;
                        if (!(!((__VLS_ctx.items.length === 0))))
                            return;
                        __VLS_ctx.openNotification(item);
                        // @ts-ignore
                        [items, openNotification,];
                    } }, ...{ class: ("notification-card__open") }, type: ("button"), });
            // @ts-ignore
            [UserAvatar,];
            const __VLS_11 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("notification-card__avatar") }, avatarUrl: ((item.actor.avatar_url)), displayName: ((item.actor.display_name)), username: ((item.actor.username)), size: ((44)), decorative: (true), }));
            const __VLS_12 = __VLS_11({ ...{ class: ("notification-card__avatar") }, avatarUrl: ((item.actor.avatar_url)), displayName: ((item.actor.display_name)), username: ((item.actor.username)), size: ((44)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_11));
            ({}({ ...{ class: ("notification-card__avatar") }, avatarUrl: ((item.actor.avatar_url)), displayName: ((item.actor.display_name)), username: ((item.actor.username)), size: ((44)), decorative: (true), }));
            const __VLS_15 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_12);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notification-card__body") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notification-card__title") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
            (item.actor.display_name || item.actor.username);
            (__VLS_ctx.notificationCopy(item));
            // @ts-ignore
            [notificationCopy,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notification-card__meta") }, });
            (__VLS_ctx.formatActivityAt(item.activity_at));
            // @ts-ignore
            [formatActivityAt,];
            if (!item.read) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.span)({ ...{ class: ("notification-card__dot") }, "aria-label": ("Unread"), });
            }
            if (__VLS_ctx.pendingReadIDs.has(item.id)) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("notification-card__pending") }, "aria-label": ("Saving"), });
                // @ts-ignore
                [pendingReadIDs,];
            }
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div)({ ref: ("sentinel"), ...{ class: ("notifications-page__sentinel") }, "aria-hidden": ("true"), });
        // @ts-ignore
        (__VLS_ctx.sentinel);
        // @ts-ignore
        [sentinel,];
        if (__VLS_ctx.loadingMore) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__load-state") }, "aria-live": ("polite"), });
            // @ts-ignore
            [loadingMore,];
        }
        if (__VLS_ctx.loadMoreError) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("notifications-page__load-state notifications-page__load-state--error") }, role: ("alert"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            // @ts-ignore
            [loadMoreError,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("notifications-page__action") }, type: ("button"), });
            // @ts-ignore
            [loadMore,];
        }
        if (__VLS_ctx.nextCursor && !__VLS_ctx.observerAvailable && !__VLS_ctx.loadingMore) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMore) }, ...{ class: ("notifications-page__load-more") }, type: ("button"), });
            // @ts-ignore
            [loadingMore, loadMore, nextCursor, observerAvailable,];
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['notifications-page'];
        __VLS_styleScopedClasses['notifications-page__header'];
        __VLS_styleScopedClasses['notifications-page__eyebrow'];
        __VLS_styleScopedClasses['notifications-page__mark-all'];
        __VLS_styleScopedClasses['notifications-page__pending-label'];
        __VLS_styleScopedClasses['notifications-page__state'];
        __VLS_styleScopedClasses['notifications-page__action'];
        __VLS_styleScopedClasses['notifications-page__state'];
        __VLS_styleScopedClasses['notifications-page__state'];
        __VLS_styleScopedClasses['notifications-page__state--error'];
        __VLS_styleScopedClasses['notifications-page__action'];
        __VLS_styleScopedClasses['notifications-page__state'];
        __VLS_styleScopedClasses['notifications-page__empty-icon'];
        __VLS_styleScopedClasses['notifications-page__list'];
        __VLS_styleScopedClasses['notification-card'];
        __VLS_styleScopedClasses['notification-card__open'];
        __VLS_styleScopedClasses['notification-card__avatar'];
        __VLS_styleScopedClasses['notification-card__body'];
        __VLS_styleScopedClasses['notification-card__title'];
        __VLS_styleScopedClasses['notification-card__meta'];
        __VLS_styleScopedClasses['notification-card__dot'];
        __VLS_styleScopedClasses['notification-card__pending'];
        __VLS_styleScopedClasses['notifications-page__sentinel'];
        __VLS_styleScopedClasses['notifications-page__load-state'];
        __VLS_styleScopedClasses['notifications-page__load-state'];
        __VLS_styleScopedClasses['notifications-page__load-state--error'];
        __VLS_styleScopedClasses['notifications-page__action'];
        __VLS_styleScopedClasses['notifications-page__load-more'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                UserAvatar: UserAvatar,
                authStore: authStore,
                items: items,
                nextCursor: nextCursor,
                loading: loading,
                error: error,
                loadingMore: loadingMore,
                loadMoreError: loadMoreError,
                pendingReadIDs: pendingReadIDs,
                markAllPending: markAllPending,
                sentinel: sentinel,
                observerAvailable: observerAvailable,
                hasUnread: hasUnread,
                loadInitial: loadInitial,
                loadMore: loadMore,
                openNotification: openNotification,
                markAll: markAll,
                notificationCopy: notificationCopy,
                formatActivityAt: formatActivityAt,
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
