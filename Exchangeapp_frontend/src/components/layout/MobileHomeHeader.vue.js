/* __placeholder__ */
import { computed } from 'vue';
import { useAuthStore } from '../../store/auth';
import AppIcon from '../icons/AppIcon.vue';
import UserAvatar from '../users/UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const authStore = useAuthStore();
const identity = computed(() => authStore.currentIdentity);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("mobile-home-header") }, });
    if (__VLS_ctx.authStore.isAuthenticated && __VLS_ctx.identity) {
        const __VLS_0 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'UserProfile', params: { id: String(__VLS_ctx.identity.id) } })), "aria-label": ("Profile"), }));
        const __VLS_2 = __VLS_1({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'UserProfile', params: { id: String(__VLS_ctx.identity.id) } })), "aria-label": ("Profile"), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'UserProfile', params: { id: String(__VLS_ctx.identity.id) } })), "aria-label": ("Profile"), }));
        // @ts-ignore
        [UserAvatar,];
        const __VLS_6 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("mobile-home-header__avatar") }, avatarUrl: ((__VLS_ctx.identity.avatar_url)), displayName: ((__VLS_ctx.identity.display_name)), username: ((__VLS_ctx.identity.username)), size: ((36)), decorative: (true), }));
        const __VLS_7 = __VLS_6({ ...{ class: ("mobile-home-header__avatar") }, avatarUrl: ((__VLS_ctx.identity.avatar_url)), displayName: ((__VLS_ctx.identity.display_name)), username: ((__VLS_ctx.identity.username)), size: ((36)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ ...{ class: ("mobile-home-header__avatar") }, avatarUrl: ((__VLS_ctx.identity.avatar_url)), displayName: ((__VLS_ctx.identity.display_name)), username: ((__VLS_ctx.identity.username)), size: ((36)), decorative: (true), }));
        // @ts-ignore
        [authStore, identity, identity, identity, identity, identity,];
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_7);
        (__VLS_5.slots).default;
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    }
    else {
        const __VLS_11 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_12 = __VLS_asFunctionalComponent(__VLS_11, new __VLS_11({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'Login' })), "aria-label": ("Log in"), }));
        const __VLS_13 = __VLS_12({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'Login' })), "aria-label": ("Log in"), }, ...__VLS_functionalComponentArgsRest(__VLS_12));
        ({}({ ...{ class: ("mobile-home-header__profile") }, to: (({ name: 'Login' })), "aria-label": ("Log in"), }));
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("mobile-home-header__avatar mobile-home-header__avatar--anonymous") }, });
        // @ts-ignore
        [AppIcon,];
        const __VLS_17 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("profile"), size: ((22)), }));
        const __VLS_18 = __VLS_17({ name: ("profile"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_17));
        ({}({ name: ("profile"), size: ((22)), }));
        const __VLS_21 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_18);
        (__VLS_16.slots).default;
        const __VLS_16 = __VLS_pickFunctionalComponentCtx(__VLS_11, __VLS_13);
    }
    const __VLS_22 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_23 = __VLS_asFunctionalComponent(__VLS_22, new __VLS_22({ ...{ class: ("mobile-home-header__brand") }, to: (({ name: 'Home' })), "aria-label": ("Exchange home"), }));
    const __VLS_24 = __VLS_23({ ...{ class: ("mobile-home-header__brand") }, to: (({ name: 'Home' })), "aria-label": ("Exchange home"), }, ...__VLS_functionalComponentArgsRest(__VLS_23));
    ({}({ ...{ class: ("mobile-home-header__brand") }, to: (({ name: 'Home' })), "aria-label": ("Exchange home"), }));
    (__VLS_27.slots).default;
    const __VLS_27 = __VLS_pickFunctionalComponentCtx(__VLS_22, __VLS_24);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("mobile-home-header__spacer") }, "aria-hidden": ("true"), });
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['mobile-home-header'];
        __VLS_styleScopedClasses['mobile-home-header__profile'];
        __VLS_styleScopedClasses['mobile-home-header__avatar'];
        __VLS_styleScopedClasses['mobile-home-header__profile'];
        __VLS_styleScopedClasses['mobile-home-header__avatar'];
        __VLS_styleScopedClasses['mobile-home-header__avatar--anonymous'];
        __VLS_styleScopedClasses['mobile-home-header__brand'];
        __VLS_styleScopedClasses['mobile-home-header__spacer'];
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
                identity: identity,
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
