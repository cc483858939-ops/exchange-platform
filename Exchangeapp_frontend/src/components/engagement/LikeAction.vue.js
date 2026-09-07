/* __placeholder__ */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    disabled: false,
    loading: false,
    pending: false,
    variant: 'compact',
    ariaPressed: undefined,
});
const emit = defineEmits();
const particles = [
    { id: 1, shape: 'dot' },
    { id: 2, shape: 'dot' },
    { id: 3, shape: 'dot' },
    { id: 4, shape: 'dot' },
    { id: 5, shape: 'diamond' },
    { id: 6, shape: 'diamond' },
    { id: 7, shape: 'diamond' },
    { id: 8, shape: 'diamond' },
];
const motion = ref('idle');
const expectedLiked = ref(null);
const expectedStateObserved = ref(false);
const countIntent = ref(null);
const awaitingIntentCount = ref(false);
const countTransitionName = ref('like-count-fade');
let motionTimer = null;
const effectivelyDisabled = computed(() => props.disabled || props.loading || props.pending);
const resolvedAriaPressed = computed(() => {
    if (props.ariaPressed === null) {
        return undefined;
    }
    if (typeof props.ariaPressed === 'boolean') {
        return props.ariaPressed;
    }
    return props.liked;
});
const clearMotionTimer = () => {
    if (motionTimer !== null) {
        clearTimeout(motionTimer);
        motionTimer = null;
    }
};
const clearIntent = () => {
    countIntent.value = null;
    awaitingIntentCount.value = false;
};
const cancelMotion = () => {
    clearMotionTimer();
    motion.value = 'idle';
    expectedLiked.value = null;
    expectedStateObserved.value = false;
    clearIntent();
    countTransitionName.value = 'like-count-fade';
};
const startMotion = (nextMotion) => {
    clearMotionTimer();
    motion.value = nextMotion;
    motionTimer = setTimeout(() => {
        motion.value = 'idle';
        motionTimer = null;
        expectedLiked.value = null;
        expectedStateObserved.value = false;
        clearIntent();
    }, nextMotion === 'liking' ? 300 : 160);
};
const armIntentCountDirection = (intent) => {
    countIntent.value = intent;
    awaitingIntentCount.value = true;
    countTransitionName.value = 'like-count-fade';
};
const activate = () => {
    if (effectivelyDisabled.value) {
        return;
    }
    const nextLiked = !props.liked;
    expectedLiked.value = nextLiked;
    expectedStateObserved.value = false;
    startMotion(nextLiked ? 'liking' : 'unliking');
    armIntentCountDirection(nextLiked ? 'up' : 'down');
    emit('toggle');
};
watch(() => props.liked, nextLiked => {
    if (motion.value === 'idle' || expectedLiked.value === null) {
        return;
    }
    if (nextLiked === expectedLiked.value) {
        expectedStateObserved.value = true;
        return;
    }
    if (expectedStateObserved.value) {
        cancelMotion();
    }
});
watch(() => props.count, (nextCount, previousCount) => {
    if (nextCount === previousCount) {
        return;
    }
    const movedUp = nextCount > previousCount;
    const intentMatches = awaitingIntentCount.value
        && ((countIntent.value === 'up' && movedUp)
            || (countIntent.value === 'down' && !movedUp));
    countTransitionName.value = intentMatches
        ? countIntent.value === 'up'
            ? 'like-count-up'
            : 'like-count-down'
        : 'like-count-fade';
    clearIntent();
});
onBeforeUnmount(() => {
    clearMotionTimer();
});
const __VLS_withDefaultsArg = (function (t) { return t; })({
    disabled: false,
    loading: false,
    pending: false,
    variant: 'compact',
    ariaPressed: undefined,
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.activate) }, type: ("button"), ...{ class: ("like-action") }, ...{ class: (({
                'like-action--compact': __VLS_ctx.variant === 'compact',
                'like-action--detail': __VLS_ctx.variant === 'detail',
                'like-action--liked': __VLS_ctx.liked,
                'like-action--disabled': __VLS_ctx.disabled,
                'like-action--loading': __VLS_ctx.loading,
                'like-action--pending': __VLS_ctx.pending,
                'like-action--liking': __VLS_ctx.motion === 'liking',
                'like-action--unliking': __VLS_ctx.motion === 'unliking',
            })) }, disabled: ((__VLS_ctx.effectivelyDisabled)), "aria-busy": ((__VLS_ctx.loading || __VLS_ctx.pending ? 'true' : undefined)), "aria-pressed": ((__VLS_ctx.resolvedAriaPressed)), "aria-label": ((__VLS_ctx.ariaLabel)), "data-motion": ((__VLS_ctx.motion)), });
    __VLS_styleScopedClasses = ({
        'like-action--compact': variant === 'compact',
        'like-action--detail': variant === 'detail',
        'like-action--liked': liked,
        'like-action--disabled': disabled,
        'like-action--loading': loading,
        'like-action--pending': pending,
        'like-action--liking': motion === 'liking',
        'like-action--unliking': motion === 'unliking',
    });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("like-action__visual") }, "aria-hidden": ("true"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("like-action__halo") }, });
    // @ts-ignore
    [activate, variant, variant, liked, disabled, loading, loading, pending, pending, motion, motion, motion, effectivelyDisabled, resolvedAriaPressed, ariaLabel,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("like-action__particles") }, });
    for (const [particle] of __VLS_getVForSourceType((__VLS_ctx.particles))) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ key: ((particle.id)), ...{ class: ("like-action__particle") }, ...{ class: (('like-action__particle--' + particle.shape)) }, });
        __VLS_styleScopedClasses = ('like-action__particle--' + particle.shape);
        // @ts-ignore
        [particles,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("like-action__heart") }, });
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("heart"), size: ((__VLS_ctx.variant === 'detail' ? 20 : 18)), filled: ((__VLS_ctx.liked)), }));
    const __VLS_1 = __VLS_0({ name: ("heart"), size: ((__VLS_ctx.variant === 'detail' ? 20 : 18)), filled: ((__VLS_ctx.liked)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("heart"), size: ((__VLS_ctx.variant === 'detail' ? 20 : 18)), filled: ((__VLS_ctx.liked)), }));
    // @ts-ignore
    [variant, liked,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("like-action__count-window") }, "data-count-transition": ((__VLS_ctx.countTransitionName)), "aria-hidden": ("true"), });
    const __VLS_5 = {}.Transition;
    ({}.Transition);
    ({}.Transition);
    __VLS_components.Transition;
    __VLS_components.Transition;
    // @ts-ignore
    [Transition, Transition,];
    const __VLS_6 = __VLS_asFunctionalComponent(__VLS_5, new __VLS_5({ name: ((__VLS_ctx.countTransitionName)), }));
    const __VLS_7 = __VLS_6({ name: ((__VLS_ctx.countTransitionName)), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
    ({}({ name: ((__VLS_ctx.countTransitionName)), }));
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ key: ((__VLS_ctx.count)), ...{ class: ("like-action__count") }, });
    (__VLS_ctx.count);
    // @ts-ignore
    [countTransitionName, countTransitionName, count, count,];
    (__VLS_10.slots).default;
    const __VLS_10 = __VLS_pickFunctionalComponentCtx(__VLS_5, __VLS_7);
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['like-action'];
        __VLS_styleScopedClasses['like-action__visual'];
        __VLS_styleScopedClasses['like-action__halo'];
        __VLS_styleScopedClasses['like-action__particles'];
        __VLS_styleScopedClasses['like-action__particle'];
        __VLS_styleScopedClasses['like-action__heart'];
        __VLS_styleScopedClasses['like-action__count-window'];
        __VLS_styleScopedClasses['like-action__count'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                particles: particles,
                motion: motion,
                countTransitionName: countTransitionName,
                effectivelyDisabled: effectivelyDisabled,
                resolvedAriaPressed: resolvedAriaPressed,
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
