/* __placeholder__ */
import AuthorIdentity from '../AuthorIdentity.vue';
import LinkifiedText from '../content/LinkifiedText.vue';
import PostMediaGrid from '../content/PostMediaGrid.vue';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const emit = defineEmits();
const handleOpenMedia = (index) => {
    emit('openMedia', props.reply.media, index);
};
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ ...{ class: ("reply-item") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("reply-item__header") }, });
    // @ts-ignore
    [AuthorIdentity,];
    const __VLS_0 = __VLS_asFunctionalComponent(AuthorIdentity, new AuthorIdentity({ author: ((__VLS_ctx.reply.author)), createdAt: ((__VLS_ctx.reply.created_at)), }));
    const __VLS_1 = __VLS_0({ author: ((__VLS_ctx.reply.author)), createdAt: ((__VLS_ctx.reply.created_at)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ author: ((__VLS_ctx.reply.author)), createdAt: ((__VLS_ctx.reply.created_at)), }));
    // @ts-ignore
    [reply, reply,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AuthorIdentity, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("reply-item__tools") }, });
    if (__VLS_ctx.canDelete) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    if (!((__VLS_ctx.canDelete)))
                        return;
                    __VLS_ctx.emit('requestDelete', __VLS_ctx.reply.id);
                    // @ts-ignore
                    [reply, canDelete, emit,];
                } }, ...{ class: ("reply-item__delete") }, type: ("button"), disabled: ((__VLS_ctx.deleting)), "aria-label": ("Delete reply"), title: ("Delete reply"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_5 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("trash"), size: ((16)), }));
        const __VLS_6 = __VLS_5({ name: ("trash"), size: ((16)), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
        ({}({ name: ("trash"), size: ((16)), }));
        // @ts-ignore
        [deleting,];
        const __VLS_9 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_6);
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("reply-item__content") }, });
    // @ts-ignore
    [LinkifiedText,];
    const __VLS_10 = __VLS_asFunctionalComponent(LinkifiedText, new LinkifiedText({ text: ((__VLS_ctx.reply.content)), }));
    const __VLS_11 = __VLS_10({ text: ((__VLS_ctx.reply.content)), }, ...__VLS_functionalComponentArgsRest(__VLS_10));
    ({}({ text: ((__VLS_ctx.reply.content)), }));
    // @ts-ignore
    [reply,];
    const __VLS_14 = __VLS_pickFunctionalComponentCtx(LinkifiedText, __VLS_11);
    if (__VLS_ctx.reply.media.length > 0) {
        // @ts-ignore
        [PostMediaGrid,];
        const __VLS_15 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.reply.media)), interactive: (true), }));
        const __VLS_16 = __VLS_15({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.reply.media)), interactive: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_15));
        ({}({ ...{ 'onOpen': {} }, media: ((__VLS_ctx.reply.media)), interactive: (true), }));
        let __VLS_20;
        const __VLS_21 = {
            onOpen: (__VLS_ctx.handleOpenMedia)
        };
        // @ts-ignore
        [reply, reply, handleOpenMedia,];
        const __VLS_19 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_16);
        let __VLS_17;
        let __VLS_18;
    }
    if (__VLS_ctx.deleting) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("reply-item__status") }, });
        // @ts-ignore
        [deleting,];
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['reply-item'];
        __VLS_styleScopedClasses['reply-item__header'];
        __VLS_styleScopedClasses['reply-item__tools'];
        __VLS_styleScopedClasses['reply-item__delete'];
        __VLS_styleScopedClasses['reply-item__content'];
        __VLS_styleScopedClasses['reply-item__status'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AuthorIdentity: AuthorIdentity,
                LinkifiedText: LinkifiedText,
                PostMediaGrid: PostMediaGrid,
                AppIcon: AppIcon,
                emit: emit,
                handleOpenMedia: handleOpenMedia,
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
