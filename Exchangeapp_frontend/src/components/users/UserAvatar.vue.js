/* __placeholder__ */
import { computed, ref, watch } from 'vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    avatarUrl: '',
    displayName: '',
    username: '',
    size: 40,
    decorative: false,
});
const imageLoaded = ref(false);
const imageFailed = ref(false);
const trim = (value) => value?.trim() || '';
const firstCodePoint = (value) => Array.from(value)[0]?.toUpperCase() || '';
const avatarURL = computed(() => trim(props.avatarUrl));
const displayName = computed(() => trim(props.displayName));
const username = computed(() => trim(props.username));
const initial = computed(() => (firstCodePoint(displayName.value)
    || firstCodePoint(username.value)
    || '?'));
const accessibleLabel = computed(() => {
    if (props.alt !== undefined) {
        return props.alt.trim();
    }
    return (displayName.value ? `${displayName.value} avatar` : '')
        || (username.value ? `${username.value} avatar` : 'Avatar');
});
const resetImageState = () => {
    imageLoaded.value = false;
    imageFailed.value = false;
};
const handleLoad = () => {
    imageFailed.value = false;
    imageLoaded.value = true;
};
const handleError = () => {
    imageLoaded.value = false;
    imageFailed.value = true;
};
watch([avatarURL, displayName, username], resetImageState);
const __VLS_withDefaultsArg = (function (t) { return t; })({
    avatarUrl: '',
    displayName: '',
    username: '',
    size: 40,
    decorative: false,
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("user-avatar") }, ...{ class: (({ 'user-avatar--decorative': __VLS_ctx.decorative })) }, ...{ style: (({ '--user-avatar-size': `${__VLS_ctx.size}px` })) }, "aria-hidden": ((__VLS_ctx.decorative ? 'true' : undefined)), "aria-label": ((!__VLS_ctx.decorative && !__VLS_ctx.avatarURL ? __VLS_ctx.accessibleLabel : undefined)), role: ((!__VLS_ctx.decorative ? 'img' : undefined)), });
    __VLS_styleScopedClasses = ({ 'user-avatar--decorative': decorative });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("user-avatar__fallback") }, "aria-hidden": ("true"), });
    (__VLS_ctx.initial);
    // @ts-ignore
    [decorative, decorative, decorative, decorative, size, avatarURL, accessibleLabel, initial,];
    if (__VLS_ctx.avatarURL && !__VLS_ctx.imageFailed) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.img)({ ...{ onLoad: (__VLS_ctx.handleLoad) }, ...{ onError: (__VLS_ctx.handleError) }, ...{ class: ("user-avatar__image") }, ...{ class: (({ 'user-avatar__image--loaded': __VLS_ctx.imageLoaded })) }, src: ((__VLS_ctx.avatarURL)), alt: ((__VLS_ctx.decorative ? '' : __VLS_ctx.accessibleLabel)), });
        __VLS_styleScopedClasses = ({ 'user-avatar__image--loaded': imageLoaded });
        // @ts-ignore
        [decorative, avatarURL, avatarURL, accessibleLabel, imageFailed, handleLoad, handleError, imageLoaded,];
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['user-avatar'];
        __VLS_styleScopedClasses['user-avatar__fallback'];
        __VLS_styleScopedClasses['user-avatar__image'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                imageLoaded: imageLoaded,
                imageFailed: imageFailed,
                avatarURL: avatarURL,
                initial: initial,
                accessibleLabel: accessibleLabel,
                handleLoad: handleLoad,
                handleError: handleError,
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
