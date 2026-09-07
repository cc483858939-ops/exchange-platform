/* __placeholder__ */
import { computed } from 'vue';
import { useNow } from '../composables/useNow';
import { formatPostDate } from '../utils/time';
import UserAvatar from './users/UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    variant: 'compact',
});
const username = computed(() => props.author.username.trim() || '?');
const displayName = computed(() => props.author.display_name.trim() || username.value);
const now = useNow();
const postDate = computed(() => formatPostDate(props.createdAt, now.value));
const __VLS_withDefaultsArg = (function (t) { return t; })({
    variant: 'compact',
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
    const __VLS_0 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ 'onClick': {} }, ...{ class: ("author-identity") }, ...{ class: (({ 'author-identity--post': __VLS_ctx.variant === 'post' })) }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.author.id } })), "aria-label": ((`View ${__VLS_ctx.displayName}'s profile`)), }));
    const __VLS_2 = __VLS_1({ ...{ 'onClick': {} }, ...{ class: ("author-identity") }, ...{ class: (({ 'author-identity--post': __VLS_ctx.variant === 'post' })) }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.author.id } })), "aria-label": ((`View ${__VLS_ctx.displayName}'s profile`)), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({ ...{ 'onClick': {} }, ...{ class: ("author-identity") }, ...{ class: (({ 'author-identity--post': __VLS_ctx.variant === 'post' })) }, to: (({ name: 'UserProfile', params: { id: __VLS_ctx.author.id } })), "aria-label": ((`View ${__VLS_ctx.displayName}'s profile`)), }));
    __VLS_styleScopedClasses = ({ 'author-identity--post': variant === 'post' });
    let __VLS_6;
    const __VLS_7 = {
        onClick: () => { }
    };
    // @ts-ignore
    [UserAvatar,];
    const __VLS_8 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("author-avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((__VLS_ctx.variant === 'post' ? 40 : 30)), decorative: (true), }));
    const __VLS_9 = __VLS_8({ ...{ class: ("author-avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((__VLS_ctx.variant === 'post' ? 40 : 30)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_8));
    ({}({ ...{ class: ("author-avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((__VLS_ctx.variant === 'post' ? 40 : 30)), decorative: (true), }));
    // @ts-ignore
    [variant, variant, author, author, author, author, displayName,];
    const __VLS_12 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_9);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("author-copy") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("author-name") }, });
    (__VLS_ctx.displayName);
    // @ts-ignore
    [displayName,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("author-meta") }, });
    (__VLS_ctx.username);
    if (__VLS_ctx.variant === 'compact' && __VLS_ctx.postDate) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.postDate);
        // @ts-ignore
        [variant, username, postDate, postDate,];
    }
    (__VLS_5.slots).default;
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    let __VLS_3;
    let __VLS_4;
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['author-identity'];
        __VLS_styleScopedClasses['author-avatar'];
        __VLS_styleScopedClasses['author-copy'];
        __VLS_styleScopedClasses['author-name'];
        __VLS_styleScopedClasses['author-meta'];
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
                postDate: postDate,
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
