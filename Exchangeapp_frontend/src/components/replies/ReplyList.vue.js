/* __placeholder__ */
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import ReplyItem from './ReplyItem.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const emit = defineEmits();
const handleOpenMedia = (media, index) => {
    emit('openMedia', media, index);
};
const sentinelRef = ref(null);
let observer = null;
const disconnectObserver = () => {
    observer?.disconnect();
    observer = null;
};
const connectObserver = () => {
    disconnectObserver();
    if (!props.hasNext ||
        props.loadingMore ||
        props.loadMoreError ||
        typeof IntersectionObserver === 'undefined' ||
        !sentinelRef.value) {
        return;
    }
    const handleIntersections = entries => {
        if (entries.some(entry => entry.isIntersecting)) {
            emit('loadMore');
        }
    };
    observer = new IntersectionObserver(handleIntersections, { rootMargin: '240px 0px' });
    observer.observe(sentinelRef.value);
};
watch([() => props.hasNext, () => props.loadingMore, () => props.loadMoreError], () => {
    void nextTick(connectObserver);
}, { flush: 'post' });
onMounted(() => {
    void nextTick(connectObserver);
});
onBeforeUnmount(disconnectObserver);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("reply-list") }, "aria-label": ("Replies"), });
    if (__VLS_ctx.replies.length === 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("reply-list__empty") }, });
        // @ts-ignore
        [replies,];
    }
    else {
        for (const [reply] of __VLS_getVForSourceType((__VLS_ctx.replies))) {
            // @ts-ignore
            [ReplyItem,];
            const __VLS_0 = __VLS_asFunctionalComponent(ReplyItem, new ReplyItem({ ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((reply.id)), reply: ((reply)), canDelete: ((Boolean(__VLS_ctx.currentIdentity && __VLS_ctx.currentIdentity.id === reply.author.id))), deleting: ((__VLS_ctx.deletingReplyId === reply.id)), }));
            const __VLS_1 = __VLS_0({ ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((reply.id)), reply: ((reply)), canDelete: ((Boolean(__VLS_ctx.currentIdentity && __VLS_ctx.currentIdentity.id === reply.author.id))), deleting: ((__VLS_ctx.deletingReplyId === reply.id)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
            ({}({ ...{ 'onRequestDelete': {} }, ...{ 'onOpenMedia': {} }, key: ((reply.id)), reply: ((reply)), canDelete: ((Boolean(__VLS_ctx.currentIdentity && __VLS_ctx.currentIdentity.id === reply.author.id))), deleting: ((__VLS_ctx.deletingReplyId === reply.id)), }));
            let __VLS_5;
            const __VLS_6 = {
                onRequestDelete: (...[$event]) => {
                    if (!(!((__VLS_ctx.replies.length === 0))))
                        return;
                    __VLS_ctx.emit('requestDelete', $event);
                    // @ts-ignore
                    [replies, currentIdentity, currentIdentity, deletingReplyId, emit,];
                }
            };
            const __VLS_7 = {
                onOpenMedia: (__VLS_ctx.handleOpenMedia)
            };
            // @ts-ignore
            [handleOpenMedia,];
            const __VLS_4 = __VLS_pickFunctionalComponentCtx(ReplyItem, __VLS_1);
            let __VLS_2;
            let __VLS_3;
        }
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("sentinelRef"), ...{ class: ("reply-list__sentinel") }, "aria-live": ("polite"), });
    // @ts-ignore
    (__VLS_ctx.sentinelRef);
    if (__VLS_ctx.loadingMore) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("reply-list__status") }, });
        // @ts-ignore
        [sentinelRef, loadingMore,];
    }
    else if (__VLS_ctx.loadMoreError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("reply-list__error") }, });
        (__VLS_ctx.loadMoreError);
        // @ts-ignore
        [loadMoreError, loadMoreError,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    if (!(!((__VLS_ctx.loadingMore))))
                        return;
                    if (!((__VLS_ctx.loadMoreError)))
                        return;
                    __VLS_ctx.emit('retry');
                    // @ts-ignore
                    [emit,];
                } }, ...{ class: ("reply-list__retry") }, type: ("button"), });
    }
    else if (__VLS_ctx.hasNext) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    if (!(!((__VLS_ctx.loadingMore))))
                        return;
                    if (!(!((__VLS_ctx.loadMoreError))))
                        return;
                    if (!((__VLS_ctx.hasNext)))
                        return;
                    __VLS_ctx.emit('loadMore');
                    // @ts-ignore
                    [emit, hasNext,];
                } }, ...{ class: ("reply-list__load-more") }, type: ("button"), });
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['reply-list'];
        __VLS_styleScopedClasses['reply-list__empty'];
        __VLS_styleScopedClasses['reply-list__sentinel'];
        __VLS_styleScopedClasses['reply-list__status'];
        __VLS_styleScopedClasses['reply-list__error'];
        __VLS_styleScopedClasses['reply-list__retry'];
        __VLS_styleScopedClasses['reply-list__load-more'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                ReplyItem: ReplyItem,
                emit: emit,
                handleOpenMedia: handleOpenMedia,
                sentinelRef: sentinelRef,
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
