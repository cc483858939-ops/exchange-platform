/* __placeholder__ */
import { computed } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    loading: false,
    pending: false,
    disabled: false,
    variant: 'compact',
});
const emit = defineEmits();
const effectivelyDisabled = computed(() => props.disabled || props.loading || props.pending);
const activate = () => {
    if (!effectivelyDisabled.value) {
        emit('toggle');
    }
};
const __VLS_withDefaultsArg = (function (t) { return t; })({
    loading: false,
    pending: false,
    disabled: false,
    variant: 'compact',
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.activate) }, type: ("button"), ...{ class: ("repost-action") }, ...{ class: (({
                'repost-action--compact': __VLS_ctx.variant === 'compact',
                'repost-action--detail': __VLS_ctx.variant === 'detail',
                'repost-action--reposted': __VLS_ctx.reposted,
                'repost-action--disabled': __VLS_ctx.disabled,
                'repost-action--loading': __VLS_ctx.loading,
                'repost-action--pending': __VLS_ctx.pending,
            })) }, disabled: ((__VLS_ctx.effectivelyDisabled)), "aria-busy": ((__VLS_ctx.loading || __VLS_ctx.pending ? 'true' : undefined)), "aria-pressed": ((__VLS_ctx.reposted)), "aria-label": ((__VLS_ctx.ariaLabel)), });
    __VLS_styleScopedClasses = ({
        'repost-action--compact': variant === 'compact',
        'repost-action--detail': variant === 'detail',
        'repost-action--reposted': reposted,
        'repost-action--disabled': disabled,
        'repost-action--loading': loading,
        'repost-action--pending': pending,
    });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("repost"), size: ((18)), }));
    const __VLS_1 = __VLS_0({ name: ("repost"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("repost"), size: ((18)), }));
    // @ts-ignore
    [activate, variant, variant, reposted, reposted, disabled, loading, loading, pending, pending, effectivelyDisabled, ariaLabel,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ "aria-hidden": ("true"), });
    (__VLS_ctx.count);
    // @ts-ignore
    [count,];
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['repost-action'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                effectivelyDisabled: effectivelyDisabled,
                activate: activate,
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
