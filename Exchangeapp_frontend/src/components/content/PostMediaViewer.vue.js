/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const emit = defineEmits();
const dialogRef = ref(null);
const closeButtonRef = ref(null);
const visibleMedia = computed(() => props.media.slice(0, 4));
const clampIndex = (index, length) => {
    if (length === 0 || !Number.isFinite(index)) {
        return 0;
    }
    return Math.min(Math.max(Math.trunc(index), 0), length - 1);
};
const currentIndex = ref(clampIndex(props.initialIndex, visibleMedia.value.length));
const failedURLs = ref(new Set());
const activeMedia = computed(() => visibleMedia.value[currentIndex.value] ?? null);
const hasMultipleMedia = computed(() => visibleMedia.value.length > 1);
const imageAlt = computed(() => (hasMultipleMedia.value
    ? `Post image ${currentIndex.value + 1} of ${visibleMedia.value.length}`
    : 'Post image'));
const imagePositionLabel = computed(() => (hasMultipleMedia.value
    ? `Image ${currentIndex.value + 1} of ${visibleMedia.value.length}`
    : 'Post image'));
let closeRequested = false;
const requestClose = () => {
    if (closeRequested) {
        return;
    }
    closeRequested = true;
    emit('close');
};
const showPrevious = () => {
    if (!closeRequested && currentIndex.value > 0) {
        currentIndex.value -= 1;
    }
};
const showNext = () => {
    if (!closeRequested && currentIndex.value < visibleMedia.value.length - 1) {
        currentIndex.value += 1;
    }
};
const markFailed = (url) => {
    failedURLs.value = new Set([...failedURLs.value, url]);
};
const handleImageError = () => {
    if (activeMedia.value) {
        markFailed(activeMedia.value.url);
    }
};
const handleKeydown = (event) => {
    if (closeRequested) {
        return;
    }
    if (event.key === 'ArrowLeft') {
        event.preventDefault();
        showPrevious();
    }
    else if (event.key === 'ArrowRight') {
        event.preventDefault();
        showNext();
    }
    else if (event.key === 'Escape') {
        event.preventDefault();
        requestClose();
    }
};
let pointerStart = null;
const handlePointerDown = (event) => {
    pointerStart = { x: event.clientX, y: event.clientY };
};
const resetPointer = () => {
    pointerStart = null;
};
const handlePointerUp = (event) => {
    const start = pointerStart;
    resetPointer();
    if (!start || !hasMultipleMedia.value) {
        return;
    }
    const deltaX = event.clientX - start.x;
    const deltaY = event.clientY - start.y;
    if (Math.abs(deltaX) < 50 || Math.abs(deltaX) <= Math.abs(deltaY)) {
        return;
    }
    if (deltaX < 0) {
        showNext();
    }
    else {
        showPrevious();
    }
};
const handleCancel = (event) => {
    event.preventDefault();
    requestClose();
};
watch([visibleMedia, () => props.initialIndex], ([items, initialIndex]) => {
    currentIndex.value = clampIndex(initialIndex, items.length);
});
onMounted(async () => {
    const dialog = dialogRef.value;
    if (!dialog) {
        return;
    }
    if (typeof dialog.showModal === 'function') {
        try {
            if (!dialog.open) {
                dialog.showModal();
            }
        }
        catch {
            dialog.setAttribute('open', '');
        }
    }
    else {
        dialog.setAttribute('open', '');
    }
    window.addEventListener('keydown', handleKeydown);
    await nextTick();
    closeButtonRef.value?.focus();
});
onBeforeUnmount(() => {
    closeRequested = true;
    window.removeEventListener('keydown', handleKeydown);
    const dialog = dialogRef.value;
    if (!dialog) {
        return;
    }
    if (dialog.open && typeof dialog.close === 'function') {
        dialog.close();
    }
    else {
        dialog.removeAttribute('open');
    }
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.dialog, __VLS_intrinsicElements.dialog)({ ...{ onCancel: (__VLS_ctx.handleCancel) }, ref: ("dialogRef"), ...{ class: ("post-media-viewer") }, "aria-label": ("Post media viewer"), "aria-modal": ("true"), });
    // @ts-ignore
    (__VLS_ctx.dialogRef);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ onPointerdown: (__VLS_ctx.handlePointerDown) }, ...{ onPointerup: (__VLS_ctx.handlePointerUp) }, ...{ onPointercancel: (__VLS_ctx.resetPointer) }, ...{ class: ("post-media-viewer__surface") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.requestClose) }, ref: ("closeButtonRef"), ...{ class: ("post-media-viewer__close") }, type: ("button"), "aria-label": ("Close image viewer"), });
    // @ts-ignore
    (__VLS_ctx.closeButtonRef);
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("close"), size: ((20)), }));
    const __VLS_1 = __VLS_0({ name: ("close"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("close"), size: ((20)), }));
    // @ts-ignore
    [handleCancel, dialogRef, handlePointerDown, handlePointerUp, resetPointer, requestClose, closeButtonRef,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-media-viewer__stage") }, });
    if (__VLS_ctx.hasMultipleMedia) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.showPrevious) }, ...{ class: ("post-media-viewer__nav post-media-viewer__nav--previous") }, type: ("button"), "aria-label": ("Previous image"), disabled: ((__VLS_ctx.currentIndex === 0)), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_5 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((22)), }));
        const __VLS_6 = __VLS_5({ name: ("arrow-left"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
        ({}({ name: ("arrow-left"), size: ((22)), }));
        // @ts-ignore
        [hasMultipleMedia, showPrevious, currentIndex,];
        const __VLS_9 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_6);
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-media-viewer__image-frame") }, "aria-label": ((__VLS_ctx.imagePositionLabel)), });
    if (__VLS_ctx.activeMedia && !__VLS_ctx.failedURLs.has(__VLS_ctx.activeMedia.url)) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.img)({ ...{ onError: (__VLS_ctx.handleImageError) }, ...{ class: ("post-media-viewer__image") }, src: ((__VLS_ctx.activeMedia.url)), alt: ((__VLS_ctx.imageAlt)), });
        // @ts-ignore
        [imagePositionLabel, activeMedia, activeMedia, activeMedia, failedURLs, handleImageError, imageAlt,];
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-media-viewer__placeholder") }, role: ("img"), "aria-label": ("Image unavailable"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_10 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("image-off"), size: ((28)), }));
        const __VLS_11 = __VLS_10({ name: ("image-off"), size: ((28)), }, ...__VLS_functionalComponentArgsRest(__VLS_10));
        ({}({ name: ("image-off"), size: ((28)), }));
        const __VLS_14 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_11);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    }
    if (__VLS_ctx.hasMultipleMedia) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.showNext) }, ...{ class: ("post-media-viewer__nav post-media-viewer__nav--next") }, type: ("button"), "aria-label": ("Next image"), disabled: ((__VLS_ctx.currentIndex === __VLS_ctx.visibleMedia.length - 1)), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_15 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("arrow-left"), size: ((22)), }));
        const __VLS_16 = __VLS_15({ name: ("arrow-left"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_15));
        ({}({ name: ("arrow-left"), size: ((22)), }));
        // @ts-ignore
        [hasMultipleMedia, currentIndex, showNext, visibleMedia,];
        const __VLS_19 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_16);
    }
    if (__VLS_ctx.hasMultipleMedia) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.output, __VLS_intrinsicElements.output)({ ...{ class: ("post-media-viewer__counter") }, "aria-label": ((__VLS_ctx.imagePositionLabel)), });
        (__VLS_ctx.currentIndex + 1);
        (__VLS_ctx.visibleMedia.length);
        // @ts-ignore
        [hasMultipleMedia, currentIndex, imagePositionLabel, visibleMedia,];
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['post-media-viewer'];
        __VLS_styleScopedClasses['post-media-viewer__surface'];
        __VLS_styleScopedClasses['post-media-viewer__close'];
        __VLS_styleScopedClasses['post-media-viewer__stage'];
        __VLS_styleScopedClasses['post-media-viewer__nav'];
        __VLS_styleScopedClasses['post-media-viewer__nav--previous'];
        __VLS_styleScopedClasses['post-media-viewer__image-frame'];
        __VLS_styleScopedClasses['post-media-viewer__image'];
        __VLS_styleScopedClasses['post-media-viewer__placeholder'];
        __VLS_styleScopedClasses['post-media-viewer__nav'];
        __VLS_styleScopedClasses['post-media-viewer__nav--next'];
        __VLS_styleScopedClasses['post-media-viewer__counter'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                dialogRef: dialogRef,
                closeButtonRef: closeButtonRef,
                visibleMedia: visibleMedia,
                currentIndex: currentIndex,
                failedURLs: failedURLs,
                activeMedia: activeMedia,
                hasMultipleMedia: hasMultipleMedia,
                imageAlt: imageAlt,
                imagePositionLabel: imagePositionLabel,
                requestClose: requestClose,
                showPrevious: showPrevious,
                showNext: showNext,
                handleImageError: handleImageError,
                handlePointerDown: handlePointerDown,
                resetPointer: resetPointer,
                handlePointerUp: handlePointerUp,
                handleCancel: handleCancel,
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
