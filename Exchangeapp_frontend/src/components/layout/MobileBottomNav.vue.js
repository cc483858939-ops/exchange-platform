/* __placeholder__ */
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useAuthStore } from '../../store/auth';
import { useHomeTimelineStore } from '../../store/homeTimeline';
import { useSearchSessionStore } from '../../store/searchSession';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    notificationBadge: null,
});
const authStore = useAuthStore();
const homeTimeline = useHomeTimelineStore();
const searchSession = useSearchSessionStore();
const route = useRoute();
const currentProfileID = computed(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? String(id) : null;
});
const homeDestination = computed(() => homeTimeline.activeTab === 'following'
    ? { name: 'Home', query: { tab: 'following' } }
    : { name: 'Home' });
const searchDestination = computed(() => (searchSession.query
    ? { name: 'UserSearch', query: { q: searchSession.query } }
    : { name: 'UserSearch' }));
const navigationItems = computed(() => {
    if (!authStore.isAuthenticated) {
        return [
            { label: 'Home', routeName: 'Home', icon: 'home', to: homeDestination.value },
            { label: 'Exchange', routeName: 'CurrencyExchange', icon: 'exchange', to: { name: 'CurrencyExchange' } },
            { label: 'Log in', routeName: 'Login', icon: 'profile', to: { name: 'Login' } },
        ];
    }
    return [
        { label: 'Home', routeName: 'Home', icon: 'home', to: homeDestination.value },
        { label: 'Search', routeName: 'UserSearch', icon: 'search', to: searchDestination.value },
        { label: 'Exchange', routeName: 'CurrencyExchange', icon: 'exchange', to: { name: 'CurrencyExchange' } },
        { label: 'Notifications', routeName: 'Notifications', icon: 'notifications', to: { name: 'Notifications' } },
        {
            label: 'Profile',
            routeName: 'UserProfile',
            icon: 'profile',
            to: {
                name: 'UserProfile',
                params: { id: currentProfileID.value || '' },
            },
        },
    ];
});
const firstRouteParam = (value) => Array.isArray(value) ? value[0] || '' : value || '';
const isOwnProfileRoute = () => {
    const profileID = currentProfileID.value;
    if (!profileID) {
        return false;
    }
    const routeName = String(route.name || '');
    const isProfileSurface = routeName === 'UserProfile'
        || routeName === 'UserFollowing'
        || routeName === 'UserFollowers'
        || routeName === 'History';
    return isProfileSurface
        && (routeName === 'History' || firstRouteParam(route.params?.id) === profileID);
};
const isItemActive = (item) => {
    if (item.routeName === 'UserProfile') {
        return isOwnProfileRoute();
    }
    return route.name === item.routeName;
};
const isReselectableRoot = (item) => {
    const routeName = String(route.name || '');
    if (item.routeName === 'UserProfile') {
        const profileID = currentProfileID.value;
        return Boolean(profileID)
            && routeName === 'UserProfile'
            && firstRouteParam(route.params?.id) === profileID;
    }
    return (item.routeName === 'Home'
        || item.routeName === 'UserSearch'
        || item.routeName === 'CurrencyExchange'
        || item.routeName === 'Notifications') && routeName === item.routeName;
};
const isStandardActivation = (event) => event.button === 0
    && !event.metaKey
    && !event.ctrlKey
    && !event.shiftKey
    && !event.altKey;
const prefersReducedMotion = () => typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
const scrollToTop = () => {
    if (typeof window === 'undefined' || typeof window.scrollTo !== 'function') {
        return;
    }
    window.scrollTo({
        top: 0,
        behavior: prefersReducedMotion() ? 'auto' : 'smooth',
    });
};
const handleNavigationClick = (event, item) => {
    if (!isStandardActivation(event) || !isReselectableRoot(item)) {
        return;
    }
    event.preventDefault();
    scrollToTop();
};
const notificationBadge = computed(() => props.notificationBadge);
const __VLS_withDefaultsArg = (function (t) { return t; })({
    notificationBadge: null,
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({ ...{ class: ("mobile-bottom-nav") }, "aria-label": ("Mobile navigation"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("mobile-bottom-nav__items") }, ...{ class: (({ 'mobile-bottom-nav__items--anonymous': !__VLS_ctx.authStore.isAuthenticated })) }, });
    __VLS_styleScopedClasses = ({ 'mobile-bottom-nav__items--anonymous': !authStore.isAuthenticated });
    for (const [item] of __VLS_getVForSourceType((__VLS_ctx.navigationItems))) {
        const __VLS_0 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ 'onClick': {} }, key: ((item.label)), ...{ class: ("mobile-bottom-nav__item") }, ...{ class: (({ 'mobile-bottom-nav__item--active': __VLS_ctx.isItemActive(item) })) }, to: ((item.to)), "aria-label": ((item.label)), title: ((item.label)), "aria-current": ((__VLS_ctx.isItemActive(item) ? 'page' : undefined)), }));
        const __VLS_2 = __VLS_1({ ...{ 'onClick': {} }, key: ((item.label)), ...{ class: ("mobile-bottom-nav__item") }, ...{ class: (({ 'mobile-bottom-nav__item--active': __VLS_ctx.isItemActive(item) })) }, to: ((item.to)), "aria-label": ((item.label)), title: ((item.label)), "aria-current": ((__VLS_ctx.isItemActive(item) ? 'page' : undefined)), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ 'onClick': {} }, key: ((item.label)), ...{ class: ("mobile-bottom-nav__item") }, ...{ class: (({ 'mobile-bottom-nav__item--active': __VLS_ctx.isItemActive(item) })) }, to: ((item.to)), "aria-label": ((item.label)), title: ((item.label)), "aria-current": ((__VLS_ctx.isItemActive(item) ? 'page' : undefined)), }));
        __VLS_styleScopedClasses = ({ 'mobile-bottom-nav__item--active': isItemActive(item) });
        let __VLS_6;
        const __VLS_7 = {
            onClick: (...[$event]) => {
                __VLS_ctx.handleNavigationClick($event, item);
                // @ts-ignore
                [authStore, navigationItems, isItemActive, isItemActive, handleNavigationClick,];
            }
        };
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("mobile-bottom-nav__icon") }, });
        // @ts-ignore
        [AppIcon,];
        const __VLS_8 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ((item.icon)), size: ((27)), filled: ((__VLS_ctx.isItemActive(item))), }));
        const __VLS_9 = __VLS_8({ name: ((item.icon)), size: ((27)), filled: ((__VLS_ctx.isItemActive(item))), }, ...__VLS_functionalComponentArgsRest(__VLS_8));
        ({}({ name: ((item.icon)), size: ((27)), filled: ((__VLS_ctx.isItemActive(item))), }));
        // @ts-ignore
        [isItemActive,];
        const __VLS_12 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_9);
        if (item.routeName === 'Notifications' && __VLS_ctx.notificationBadge) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("mobile-bottom-nav__badge") }, "aria-label": ("Unread notifications"), });
            (__VLS_ctx.notificationBadge);
            // @ts-ignore
            [notificationBadge, notificationBadge,];
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("mobile-bottom-nav__label") }, });
        (item.label);
        (__VLS_5.slots).default;
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
        let __VLS_3;
        let __VLS_4;
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['mobile-bottom-nav'];
        __VLS_styleScopedClasses['mobile-bottom-nav__items'];
        __VLS_styleScopedClasses['mobile-bottom-nav__item'];
        __VLS_styleScopedClasses['mobile-bottom-nav__icon'];
        __VLS_styleScopedClasses['mobile-bottom-nav__badge'];
        __VLS_styleScopedClasses['mobile-bottom-nav__label'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                authStore: authStore,
                navigationItems: navigationItems,
                isItemActive: isItemActive,
                handleNavigationClick: handleNavigationClick,
                notificationBadge: notificationBadge,
            };
        },
        props: {},
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    props: {},
});
;
