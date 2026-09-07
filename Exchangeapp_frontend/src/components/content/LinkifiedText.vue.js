/* __placeholder__ */
import { computed } from 'vue';
import { linkifyText } from '../../utils/linkifyText';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = defineProps();
const emit = defineEmits();
const segments = computed(() => linkifyText(props.text));
const hasInternalTarget = computed(() => props.to !== undefined);
const internalTarget = computed(() => props.to);
const handleInternalActivation = (event) => {
    emit('internal-activate', event);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("linkified-text") }, });
    for (const [segment, index] of __VLS_getVForSourceType((__VLS_ctx.segments))) {
        (`${segment.type}-${index}`);
        if (segment.type === 'link') {
            __VLS_elementAsFunction(__VLS_intrinsicElements.a, __VLS_intrinsicElements.a)({ ...{ onClick: () => { } }, ...{ class: ("linkified-text__external") }, href: ((segment.href)), target: ("_blank"), rel: ("noopener noreferrer"), });
            (segment.text);
            // @ts-ignore
            [segments,];
        }
        else if (__VLS_ctx.hasInternalTarget) {
            const __VLS_0 = {}.RouterLink;
            ({}.RouterLink);
            ({}.RouterLink);
            __VLS_components.RouterLink;
            __VLS_components.RouterLink;
            // @ts-ignore
            [RouterLink, RouterLink,];
            const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ 'onClick': {} }, ...{ class: ("linkified-text__internal") }, to: ((__VLS_ctx.internalTarget)), }));
            const __VLS_2 = __VLS_1({ ...{ 'onClick': {} }, ...{ class: ("linkified-text__internal") }, to: ((__VLS_ctx.internalTarget)), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
            ({}({ ...{ 'onClick': {} }, ...{ class: ("linkified-text__internal") }, to: ((__VLS_ctx.internalTarget)), }));
            let __VLS_6;
            const __VLS_7 = {
                onClick: (__VLS_ctx.handleInternalActivation)
            };
            (segment.text);
            // @ts-ignore
            [hasInternalTarget, internalTarget, handleInternalActivation,];
            (__VLS_5.slots).default;
            const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
            let __VLS_3;
            let __VLS_4;
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (segment.text);
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['linkified-text'];
        __VLS_styleScopedClasses['linkified-text__external'];
        __VLS_styleScopedClasses['linkified-text__internal'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                segments: segments,
                hasInternalTarget: hasInternalTarget,
                internalTarget: internalTarget,
                handleInternalActivation: handleInternalActivation,
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
