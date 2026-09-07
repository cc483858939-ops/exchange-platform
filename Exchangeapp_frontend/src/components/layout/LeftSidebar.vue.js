/* __placeholder__ */
import { computed } from 'vue';
import { useLogout } from '../../composables/useLogout';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const __VLS_props = withDefaults(defineProps(), {
    notificationBadge: null,
});
const { authStore, handleLogout } = useLogout();
const currentProfileID = computed(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const navigation = [
    { name: 'Home', label: 'Home', icon: 'home', compactOnly: false, authOnly: false },
    { name: 'UserSearch', label: 'Search', icon: 'search', compactOnly: false, authOnly: true },
    { name: 'Notifications', label: 'Notifications', icon: 'notifications', compactOnly: false, authOnly: true },
    { name: 'History', label: 'History', icon: 'history', compactOnly: false, authOnly: true },
    { name: 'CurrencyExchange', label: 'Exchange', icon: 'exchange', compactOnly: true, authOnly: false },
];
const visibleNavigation = computed(() => navigation.filter((item) => !item.authOnly || authStore.isAuthenticated));
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("left-sidebar") }, });
    const __VLS_0 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.routerLink;
    __VLS_components.RouterLink;
    __VLS_components.routerLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("left-sidebar__brand") }, to: (({ name: 'Home' })), "aria-label": ("Go Exchange home"), }));
    const __VLS_2 = __VLS_1({ ...{ class: ("left-sidebar__brand") }, to: (({ name: 'Home' })), "aria-label": ("Go Exchange home"), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({ ...{ class: ("left-sidebar__brand") }, to: (({ name: 'Home' })), "aria-label": ("Go Exchange home"), }));
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__brand-mark") }, "aria-hidden": ("true"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__brand-name") }, });
    (__VLS_5.slots).default;
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    __VLS_elementAsFunction(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({ ...{ class: ("left-sidebar__nav") }, "aria-label": ("Main navigation"), });
    for (const [item] of __VLS_getVForSourceType((__VLS_ctx.visibleNavigation))) {
        const __VLS_6 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_7 = __VLS_asFunctionalComponent(__VLS_6, new __VLS_6({ key: ((item.name)), ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, ...{ class: (({
                    'left-sidebar__link--compact-only': item.compactOnly,
                })) }, to: (({ name: item.name })), "aria-label": ((item.label)), title: ((item.label)), }));
        const __VLS_8 = __VLS_7({ key: ((item.name)), ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, ...{ class: (({
                    'left-sidebar__link--compact-only': item.compactOnly,
                })) }, to: (({ name: item.name })), "aria-label": ((item.label)), title: ((item.label)), }, ...__VLS_functionalComponentArgsRest(__VLS_7));
        ({}({ key: ((item.name)), ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, ...{ class: (({
                    'left-sidebar__link--compact-only': item.compactOnly,
                })) }, to: (({ name: item.name })), "aria-label": ((item.label)), title: ((item.label)), }));
        __VLS_styleScopedClasses = ({
            'left-sidebar__link--compact-only': item.compactOnly,
        });
        // @ts-ignore
        [AppIcon,];
        const __VLS_12 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ((item.icon)), size: ((24)), }));
        const __VLS_13 = __VLS_12({ name: ((item.icon)), size: ((24)), }, ...__VLS_functionalComponentArgsRest(__VLS_12));
        ({}({ name: ((item.icon)), size: ((24)), }));
        // @ts-ignore
        [visibleNavigation,];
        const __VLS_16 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_13);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
        (item.label);
        if (item.name === 'Notifications' && __VLS_ctx.notificationBadge) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__badge") }, "aria-label": ("Unread notifications"), });
            (__VLS_ctx.notificationBadge);
            // @ts-ignore
            [notificationBadge, notificationBadge,];
        }
        (__VLS_11.slots).default;
        const __VLS_11 = __VLS_pickFunctionalComponentCtx(__VLS_6, __VLS_8);
    }
    if (__VLS_ctx.authStore.isAuthenticated && __VLS_ctx.currentProfileID !== null) {
        const __VLS_17 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_18 = __VLS_asFunctionalComponent(__VLS_17, new __VLS_17({ ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, to: (({
                name: 'UserProfile',
                params: { id: String(__VLS_ctx.currentProfileID) },
            })), "aria-label": ("Profile"), title: ("Profile"), }));
        const __VLS_19 = __VLS_18({ ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, to: (({
                name: 'UserProfile',
                params: { id: String(__VLS_ctx.currentProfileID) },
            })), "aria-label": ("Profile"), title: ("Profile"), }, ...__VLS_functionalComponentArgsRest(__VLS_18));
        ({}({ ...{ class: ("left-sidebar__link left-sidebar__link--icon") }, to: (({
                name: 'UserProfile',
                params: { id: String(__VLS_ctx.currentProfileID) },
            })), "aria-label": ("Profile"), title: ("Profile"), }));
        // @ts-ignore
        [AppIcon,];
        const __VLS_23 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("profile"), size: ((24)), }));
        const __VLS_24 = __VLS_23({ name: ("profile"), size: ((24)), }, ...__VLS_functionalComponentArgsRest(__VLS_23));
        ({}({ name: ("profile"), size: ((24)), }));
        // @ts-ignore
        [authStore, currentProfileID, currentProfileID,];
        const __VLS_27 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_24);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
        (__VLS_22.slots).default;
        const __VLS_22 = __VLS_pickFunctionalComponentCtx(__VLS_17, __VLS_19);
    }
    if (__VLS_ctx.authStore.isAuthenticated) {
        const __VLS_28 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({ ...{ class: ("left-sidebar__link left-sidebar__link--icon left-sidebar__link--primary") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }));
        const __VLS_30 = __VLS_29({ ...{ class: ("left-sidebar__link left-sidebar__link--icon left-sidebar__link--primary") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }, ...__VLS_functionalComponentArgsRest(__VLS_29));
        ({}({ ...{ class: ("left-sidebar__link left-sidebar__link--icon left-sidebar__link--primary") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }));
        // @ts-ignore
        [AppIcon,];
        const __VLS_34 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("compose"), size: ((22)), }));
        const __VLS_35 = __VLS_34({ name: ("compose"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_34));
        ({}({ name: ("compose"), size: ((22)), }));
        // @ts-ignore
        [authStore,];
        const __VLS_38 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_35);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
        (__VLS_33.slots).default;
        const __VLS_33 = __VLS_pickFunctionalComponentCtx(__VLS_28, __VLS_30);
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("left-sidebar__account") }, });
    if (__VLS_ctx.authStore.isAuthenticated) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.handleLogout) }, ...{ class: ("left-sidebar__link left-sidebar__link--icon left-sidebar__logout") }, type: ("button"), "aria-label": ("Log out"), title: ("Log out"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_39 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("logout"), size: ((24)), }));
        const __VLS_40 = __VLS_39({ name: ("logout"), size: ((24)), }, ...__VLS_functionalComponentArgsRest(__VLS_39));
        ({}({ name: ("logout"), size: ((24)), }));
        // @ts-ignore
        [authStore, handleLogout,];
        const __VLS_43 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_40);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
    }
    else {
        const __VLS_44 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({ ...{ class: ("left-sidebar__link") }, to: (({ name: 'Login' })), }));
        const __VLS_46 = __VLS_45({ ...{ class: ("left-sidebar__link") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_45));
        ({}({ ...{ class: ("left-sidebar__link") }, to: (({ name: 'Login' })), }));
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
        (__VLS_49.slots).default;
        const __VLS_49 = __VLS_pickFunctionalComponentCtx(__VLS_44, __VLS_46);
        const __VLS_50 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        __VLS_components.RouterLink;
        __VLS_components.routerLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_51 = __VLS_asFunctionalComponent(__VLS_50, new __VLS_50({ ...{ class: ("left-sidebar__link left-sidebar__signup") }, to: (({ name: 'Register' })), }));
        const __VLS_52 = __VLS_51({ ...{ class: ("left-sidebar__link left-sidebar__signup") }, to: (({ name: 'Register' })), }, ...__VLS_functionalComponentArgsRest(__VLS_51));
        ({}({ ...{ class: ("left-sidebar__link left-sidebar__signup") }, to: (({ name: 'Register' })), }));
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("left-sidebar__label") }, });
        (__VLS_55.slots).default;
        const __VLS_55 = __VLS_pickFunctionalComponentCtx(__VLS_50, __VLS_52);
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['left-sidebar'];
        __VLS_styleScopedClasses['left-sidebar__brand'];
        __VLS_styleScopedClasses['left-sidebar__brand-mark'];
        __VLS_styleScopedClasses['left-sidebar__brand-name'];
        __VLS_styleScopedClasses['left-sidebar__nav'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__link--icon'];
        __VLS_styleScopedClasses['left-sidebar__label'];
        __VLS_styleScopedClasses['left-sidebar__badge'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__link--icon'];
        __VLS_styleScopedClasses['left-sidebar__label'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__link--icon'];
        __VLS_styleScopedClasses['left-sidebar__link--primary'];
        __VLS_styleScopedClasses['left-sidebar__label'];
        __VLS_styleScopedClasses['left-sidebar__account'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__link--icon'];
        __VLS_styleScopedClasses['left-sidebar__logout'];
        __VLS_styleScopedClasses['left-sidebar__label'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__label'];
        __VLS_styleScopedClasses['left-sidebar__link'];
        __VLS_styleScopedClasses['left-sidebar__signup'];
        __VLS_styleScopedClasses['left-sidebar__label'];
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
                handleLogout: handleLogout,
                currentProfileID: currentProfileID,
                visibleNavigation: visibleNavigation,
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
