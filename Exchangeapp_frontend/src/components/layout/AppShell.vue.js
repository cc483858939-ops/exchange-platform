/* __placeholder__ */
import { onBeforeUnmount, onMounted, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useAuthStore } from '../../store/auth';
import { useNotificationStore } from '../../store/notification';
import { useSearchSessionStore } from '../../store/searchSession';
import LeftSidebar from './LeftSidebar.vue';
import MobileBottomNav from './MobileBottomNav.vue';
import RightRail from './RightRail.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const authStore = useAuthStore();
const route = useRoute();
const notificationStore = useNotificationStore();
const searchSession = useSearchSessionStore();
const currentViewerID = () => (authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null);
const refreshUnreadAndMaybeRevalidate = () => {
    const capture = notificationStore.captureViewer();
    if (!capture) {
        return;
    }
    void notificationStore.refreshUnreadCount(capture)
        .then(() => {
        if (route.name === 'Notifications' && notificationStore.listStale) {
            void notificationStore.revalidateNotifications();
        }
    })
        .catch(() => undefined);
};
const syncSessionViewers = () => {
    const nextViewerID = currentViewerID();
    notificationStore.setViewer(nextViewerID);
    searchSession.setViewer(nextViewerID);
    refreshUnreadAndMaybeRevalidate();
};
// AppShell is the sole unread coordinator. Identity changes invalidate old
// requests; access-token rotation for the same identity does not refetch.
watch(() => currentViewerID(), syncSessionViewers, { immediate: true });
watch(() => route.name, (name) => {
    if (name === 'Notifications' && authStore.isAuthenticated) {
        refreshUnreadAndMaybeRevalidate();
    }
});
const handleVisibilityChange = () => {
    if (document.visibilityState === 'visible' && authStore.isAuthenticated) {
        refreshUnreadAndMaybeRevalidate();
    }
};
onMounted(() => {
    document.addEventListener('visibilitychange', handleVisibilityChange);
});
onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', handleVisibilityChange);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("app-layout") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({ ...{ class: ("app-layout__left") }, });
    // @ts-ignore
    [LeftSidebar,];
    const __VLS_0 = __VLS_asFunctionalComponent(LeftSidebar, new LeftSidebar({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }));
    const __VLS_1 = __VLS_0({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }));
    // @ts-ignore
    [notificationStore,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(LeftSidebar, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("app-layout__main") }, });
    var __VLS_5 = {};
    __VLS_elementAsFunction(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({ ...{ class: ("app-layout__right") }, });
    // @ts-ignore
    [RightRail,];
    const __VLS_6 = __VLS_asFunctionalComponent(RightRail, new RightRail({}));
    const __VLS_7 = __VLS_6({}, ...__VLS_functionalComponentArgsRest(__VLS_6));
    ({}({}));
    const __VLS_10 = __VLS_pickFunctionalComponentCtx(RightRail, __VLS_7);
    // @ts-ignore
    [MobileBottomNav,];
    const __VLS_11 = __VLS_asFunctionalComponent(MobileBottomNav, new MobileBottomNav({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }));
    const __VLS_12 = __VLS_11({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }, ...__VLS_functionalComponentArgsRest(__VLS_11));
    ({}({ notificationBadge: ((__VLS_ctx.notificationStore.unreadBadge)), }));
    // @ts-ignore
    [notificationStore,];
    const __VLS_15 = __VLS_pickFunctionalComponentCtx(MobileBottomNav, __VLS_12);
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['app-layout'];
        __VLS_styleScopedClasses['app-layout__left'];
        __VLS_styleScopedClasses['app-layout__main'];
        __VLS_styleScopedClasses['app-layout__right'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                LeftSidebar: LeftSidebar,
                MobileBottomNav: MobileBottomNav,
                RightRail: RightRail,
                notificationStore: notificationStore,
            };
        },
    });
}
const __VLS_component = (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
export default {};
;
