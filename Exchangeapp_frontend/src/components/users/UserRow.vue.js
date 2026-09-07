/* __placeholder__ */
import { computed } from 'vue';
import UserAvatar from './UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const __VLS_emit = defineEmits();
const username = computed(() => props.item.user.username.trim() || '?');
const displayName = computed(() => props.item.user.display_name.trim() || username.value);
const __VLS_fnComponent = (await import('vue')).defineComponent({
    emits: {},
});
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ ...{ class: ("user-row") }, });
    const __VLS_0 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("user-row__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.item.user.id } })), }));
    const __VLS_2 = __VLS_1({ ...{ class: ("user-row__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.item.user.id } })), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({ ...{ class: ("user-row__identity") }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.item.user.id } })), }));
    // @ts-ignore
    [UserAvatar,];
    const __VLS_6 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("user-row__avatar") }, avatarUrl: ((__VLS_ctx.item.user.avatar_url)), displayName: ((__VLS_ctx.item.user.display_name)), username: ((__VLS_ctx.item.user.username)), size: ((46)), decorative: (true), }));
    const __VLS_7 = __VLS_6({ ...{ class: ("user-row__avatar") }, avatarUrl: ((__VLS_ctx.item.user.avatar_url)), displayName: ((__VLS_ctx.item.user.display_name)), username: ((__VLS_ctx.item.user.username)), size: ((46)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
    ({}({ ...{ class: ("user-row__avatar") }, avatarUrl: ((__VLS_ctx.item.user.avatar_url)), displayName: ((__VLS_ctx.item.user.display_name)), username: ((__VLS_ctx.item.user.username)), size: ((46)), decorative: (true), }));
    // @ts-ignore
    [item, item, item, item,];
    const __VLS_10 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_7);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("user-row__copy") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.displayName);
    // @ts-ignore
    [displayName,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.username);
    // @ts-ignore
    [username,];
    if (__VLS_ctx.item.user.bio) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
        (__VLS_ctx.item.user.bio);
        // @ts-ignore
        [item, item,];
    }
    (__VLS_5.slots).default;
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("user-row__action") }, });
    if (!__VLS_ctx.isSelf) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    if (!((!__VLS_ctx.isSelf)))
                        return;
                    __VLS_ctx.$emit('toggle-follow', __VLS_ctx.item.user.id);
                    // @ts-ignore
                    [item, isSelf, $emit,];
                } }, ...{ class: ("user-row__follow") }, ...{ class: (({ 'user-row__follow--following': __VLS_ctx.item.following })) }, type: ("button"), "aria-pressed": ((__VLS_ctx.item.following)), "aria-busy": ((__VLS_ctx.pending)), disabled: ((__VLS_ctx.pending)), });
        __VLS_styleScopedClasses = ({ 'user-row__follow--following': item.following });
        (__VLS_ctx.item.following ? 'Following' : 'Follow');
        // @ts-ignore
        [item, item, item, pending, pending,];
    }
    if (__VLS_ctx.error) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("user-row__error") }, "aria-live": ("polite"), });
        (__VLS_ctx.error);
        // @ts-ignore
        [error, error,];
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['user-row'];
        __VLS_styleScopedClasses['user-row__identity'];
        __VLS_styleScopedClasses['user-row__avatar'];
        __VLS_styleScopedClasses['user-row__copy'];
        __VLS_styleScopedClasses['user-row__action'];
        __VLS_styleScopedClasses['user-row__follow'];
        __VLS_styleScopedClasses['user-row__error'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                UserAvatar: UserAvatar,
                username: username,
                displayName: displayName,
            };
        },
        props: {},
        emits: {},
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    props: {},
    emits: {},
});
;
