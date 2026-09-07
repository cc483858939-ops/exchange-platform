import { computed, ref } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    removable: false,
    disabled: false,
    interactive: false,
});
const emit = defineEmits();
const visibleMedia = computed(() => props.media.slice(0, 4));
const failedURLs = ref(new Set());
const markFailed = (url) => {
    failedURLs.value = new Set([...failedURLs.value, url]);
};
const openImageLabel = (index) => (visibleMedia.value.length === 1
    ? 'Open post image'
    : `Open post image ${index + 1} of ${visibleMedia.value.length}`);
const __VLS_withDefaultsArg = (function (t) { return t; })({
    removable: false,
    disabled: false,
    interactive: false,
});
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
    if (__VLS_ctx.media.length > 0) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-media-grid") }, ...{ class: ((`post-media-grid--count-${__VLS_ctx.visibleMedia.length}`)) }, role: ("group"), "aria-label": ("Post images"), });
        __VLS_styleScopedClasses = (`post-media-grid--count-${visibleMedia.length}`);
        for (const [item, index] of __VLS_getVForSourceType((__VLS_ctx.visibleMedia))) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.figure, __VLS_intrinsicElements.figure)({ key: ((`${item.url}-${item.position}-${index}`)), ...{ class: ("post-media-grid__item") }, });
            if (__VLS_ctx.interactive && !__VLS_ctx.failedURLs.has(item.url)) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                            if (!((__VLS_ctx.media.length > 0)))
                                return;
                            if (!((__VLS_ctx.interactive && !__VLS_ctx.failedURLs.has(item.url))))
                                return;
                            __VLS_ctx.emit('open', index);
                            // @ts-ignore
                            [media, visibleMedia, visibleMedia, interactive, failedURLs, emit,];
                        } }, ...{ class: ("post-media-grid__open") }, type: ("button"), "aria-label": ((__VLS_ctx.openImageLabel(index))), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.img)({ ...{ onError: (...[$event]) => {
                            if (!((__VLS_ctx.media.length > 0)))
                                return;
                            if (!((__VLS_ctx.interactive && !__VLS_ctx.failedURLs.has(item.url))))
                                return;
                            __VLS_ctx.markFailed(item.url);
                            // @ts-ignore
                            [openImageLabel, markFailed,];
                        } }, ...{ class: ("post-media-grid__image") }, ...{ class: (({ 'post-media-grid__image--single': __VLS_ctx.visibleMedia.length === 1 })) }, src: ((item.url)), alt: ((`Post image ${index + 1}`)), loading: ("lazy"), });
                __VLS_styleScopedClasses = ({ 'post-media-grid__image--single': visibleMedia.length === 1 });
                // @ts-ignore
                [visibleMedia,];
            }
            else if (!__VLS_ctx.failedURLs.has(item.url)) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.img)({ ...{ onError: (...[$event]) => {
                            if (!((__VLS_ctx.media.length > 0)))
                                return;
                            if (!(!((__VLS_ctx.interactive && !__VLS_ctx.failedURLs.has(item.url)))))
                                return;
                            if (!((!__VLS_ctx.failedURLs.has(item.url))))
                                return;
                            __VLS_ctx.markFailed(item.url);
                            // @ts-ignore
                            [failedURLs, markFailed,];
                        } }, ...{ class: ("post-media-grid__image") }, ...{ class: (({ 'post-media-grid__image--single': __VLS_ctx.visibleMedia.length === 1 })) }, src: ((item.url)), alt: ((`Post image ${index + 1}`)), loading: ("lazy"), });
                __VLS_styleScopedClasses = ({ 'post-media-grid__image--single': visibleMedia.length === 1 });
                // @ts-ignore
                [visibleMedia,];
            }
            else {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-media-grid__placeholder") }, role: ("img"), "aria-label": ((`Post image ${index + 1} unavailable`)), });
                // @ts-ignore
                [AppIcon,];
                const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("image-off"), size: ((22)), }));
                const __VLS_1 = __VLS_0({ name: ("image-off"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
                ({}({ name: ("image-off"), size: ((22)), }));
                const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
            }
            if (__VLS_ctx.removable) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                            if (!((__VLS_ctx.media.length > 0)))
                                return;
                            if (!((__VLS_ctx.removable)))
                                return;
                            __VLS_ctx.emit('remove', index);
                            // @ts-ignore
                            [emit, removable,];
                        } }, ...{ class: ("post-media-grid__remove") }, type: ("button"), "aria-label": ((`Remove image ${index + 1}`)), disabled: ((__VLS_ctx.disabled)), });
                // @ts-ignore
                [AppIcon,];
                const __VLS_5 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("image-off"), size: ((16)), }));
                const __VLS_6 = __VLS_5({ name: ("image-off"), size: ((16)), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
                ({}({ name: ("image-off"), size: ((16)), }));
                // @ts-ignore
                [disabled,];
                const __VLS_9 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_6);
            }
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['post-media-grid'];
        __VLS_styleScopedClasses['post-media-grid__item'];
        __VLS_styleScopedClasses['post-media-grid__open'];
        __VLS_styleScopedClasses['post-media-grid__image'];
        __VLS_styleScopedClasses['post-media-grid__image'];
        __VLS_styleScopedClasses['post-media-grid__placeholder'];
        __VLS_styleScopedClasses['post-media-grid__remove'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                emit: emit,
                visibleMedia: visibleMedia,
                failedURLs: failedURLs,
                markFailed: markFailed,
                openImageLabel: openImageLabel,
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
