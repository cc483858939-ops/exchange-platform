/* __placeholder__ */
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    size: 24,
    filled: false,
});
const __VLS_withDefaultsArg = (function (t) { return t; })({
    size: 24,
    filled: false,
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.svg, __VLS_intrinsicElements.svg)({ ...{ class: ("app-icon") }, width: ((props.size)), height: ((props.size)), viewBox: ("0 0 24 24"), "aria-hidden": ("true"), focusable: ("false"), });
    if (props.name === 'home') {
        if (props.filled) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M3.75 10.5 12 3.75l8.25 6.75v8.25a1.5 1.5 0 0 1-1.5 1.5H5.25a1.5 1.5 0 0 1-1.5-1.5V10.5Z"), fill: ("currentColor"), stroke: ("none"), });
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M3.75 10.5 12 3.75l8.25 6.75v8.25a1.5 1.5 0 0 1-1.5 1.5H5.25a1.5 1.5 0 0 1-1.5-1.5V10.5Z"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        }
        if (!props.filled) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M9.25 20.25v-5.5h5.5v5.5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        }
    }
    else if (props.name === 'exchange') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M4 8h14m-3-3 3 3-3 3M20 16H6m3 3-3-3 3-3"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'search') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("10.75"), cy: ("10.75"), r: ("5.5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m15 15 4.25 4.25"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'history') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("12"), cy: ("12"), r: ("8.25"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M12 7.5v4.75l3.25 2"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'notifications') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M6.25 10.5a5.75 5.75 0 0 1 11.5 0v3.25l1.75 2.25H4.5l1.75-2.25V10.5Z"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M9.75 19h4.5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), });
    }
    else if (props.name === 'profile') {
        if (props.filled) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M12 3.5a4.25 4.25 0 1 1 0 8.5 4.25 4.25 0 0 1 0-8.5Zm-8 16.75a8 8 0 0 1 16 0H4Z"), fill: ("currentColor"), stroke: ("none"), });
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("12"), cy: ("8"), r: ("3.75"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M4.25 20.25a7.75 7.75 0 0 1 15.5 0"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        }
    }
    else if (props.name === 'compose') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M13.25 4.25H6.5A1.75 1.75 0 0 0 4.75 6v12A1.75 1.75 0 0 0 6.5 19.75h11A1.75 1.75 0 0 0 19.25 18v-5.25"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m9 15 .75-3.25L16.6 4.9a2.05 2.05 0 0 1 2.9 2.9l-6.85 6.85L9 15Z"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'logout') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M10 4.25H6.5A1.75 1.75 0 0 0 4.75 6v12a1.75 1.75 0 0 0 1.75 1.75H10"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M10.5 12h8.75m-3.25-3.25L19.25 12 16 15.25"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'reply') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M18.25 5.25H5.75A2.25 2.25 0 0 0 3.5 7.5v5a2.25 2.25 0 0 0 2.25 2.25H8v3.5l3.75-3.5h6.5a2.25 2.25 0 0 0 2.25-2.25v-5a2.25 2.25 0 0 0-2.25-2.25Z"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'heart') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M20.5 8.75c0 5.05-8.5 9.95-8.5 9.95s-8.5-4.9-8.5-9.95A4.55 4.55 0 0 1 12 6.55a4.55 4.55 0 0 1 8.5 2.2Z"), fill: ((props.filled ? 'currentColor' : 'none')), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'more') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("5"), cy: ("12"), r: ("1.5"), fill: ("currentColor"), stroke: ("none"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("12"), cy: ("12"), r: ("1.5"), fill: ("currentColor"), stroke: ("none"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("19"), cy: ("12"), r: ("1.5"), fill: ("currentColor"), stroke: ("none"), });
    }
    else if (props.name === 'link') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m9.5 14.5-1.25 1.25a3.18 3.18 0 1 1-4.5-4.5L6.5 8.5a3.18 3.18 0 0 1 4.5 0m3.5 1 1.25-1.25a3.18 3.18 0 1 1 4.5 4.5L17.5 15.5a3.18 3.18 0 0 1-4.5 0m-3.5-.5 5-5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'analytics') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M5 18v-4"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M12 18V9"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M19 18V5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), });
    }
    else if (props.name === 'eye-off') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M3.75 3.75 20.25 20.25M10.25 10.25a2.5 2.5 0 0 0 3.5 3.5M6.25 6.75C4.75 8 3.75 9.75 3.25 12c1.25 4.25 4.5 6.75 8.75 6.75 1.3 0 2.5-.25 3.55-.75M9 5.45A10.1 10.1 0 0 1 12 5.25c4.25 0 7.5 2.5 8.75 6.75a11.25 11.25 0 0 1-2.1 3.8"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'trash') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M5 7h14M10 11v6M14 11v6M8 7l1-3h6l1 3m-9 0 .8 13h6.4L15 7"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'arrow-left') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m15.5 5.5-6.5 6.5 6.5 6.5M9.5 12h10"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'close') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m6.5 6.5 11 11m0-11-11 11"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'repost') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M4 8.25h15m0 0-2.75-2.75M19 8.25l-2.75 2.75M20 15.75H5m0 0 2.75-2.75M5 15.75l2.75 2.75"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'plus') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M12 5v14M5 12h14"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), });
    }
    else if (props.name === 'camera') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("M5.25 7.5h3l1.25-2h5l1.25 2h3A1.75 1.75 0 0 1 20.5 9.25v8.5a1.75 1.75 0 0 1-1.75 1.75h-14A1.75 1.75 0 0 1 3 17.75v-8.5A1.75 1.75 0 0 1 4.75 7.5Z"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("12"), cy: ("13.5"), r: ("3"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), });
    }
    else if (props.name === 'image') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.rect)({ x: ("3.5"), y: ("4"), width: ("17"), height: ("16"), rx: ("2"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.circle)({ cx: ("8.5"), cy: ("9"), r: ("1.35"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m4.75 17 4.5-4.5 3 3 2-2 5 4.5"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    else if (props.name === 'image-off') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.rect)({ x: ("3.5"), y: ("4"), width: ("17"), height: ("16"), rx: ("2"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.path)({ d: ("m4.75 17 4.5-4.5 3 3 2-2 5 4.5M4 4l16 16"), fill: ("none"), stroke: ("currentColor"), "stroke-width": ("1.8"), "stroke-linecap": ("round"), "stroke-linejoin": ("round"), });
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['app-icon'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {};
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
