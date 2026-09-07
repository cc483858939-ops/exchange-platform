/* __placeholder__ */
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import UserAvatar from '../users/UserAvatar.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    author: null,
    disabled: false,
    modelValue: '',
    submitting: false,
});
const emit = defineEmits();
const textareaRef = ref(null);
const maxContentLength = 1000;
const content = computed({
    get: () => props.modelValue,
    set: value => emit('update:modelValue', value),
});
const trimmedContent = computed(() => content.value.trim());
const contentLength = computed(() => Array.from(trimmedContent.value).length);
const exceedsMaxLength = computed(() => contentLength.value > maxContentLength);
const resizeTextarea = () => {
    const textarea = textareaRef.value;
    if (!textarea) {
        return;
    }
    textarea.style.height = 'auto';
    textarea.style.height = Math.min(textarea.scrollHeight, 180) + 'px';
};
const clear = () => {
    emit('update:modelValue', '');
    void nextTick(resizeTextarea);
};
const focus = async () => {
    await nextTick();
    const textarea = textareaRef.value;
    if (!textarea || textarea.disabled) {
        return false;
    }
    textarea.scrollIntoView({
        behavior: 'auto',
        block: 'center',
    });
    textarea.focus({ preventScroll: true });
    return document.activeElement === textarea;
};
const submitReply = () => {
    if (props.disabled || props.submitting || !trimmedContent.value || exceedsMaxLength.value) {
        return;
    }
    emit('submit', trimmedContent.value);
};
const __VLS_exposed = { clear, focus };
defineExpose({ clear, focus });
onMounted(resizeTextarea);
watch(() => props.modelValue, () => {
    void nextTick(resizeTextarea);
});
const __VLS_withDefaultsArg = (function (t) { return t; })({
    author: null,
    disabled: false,
    modelValue: '',
    submitting: false,
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({ ...{ onSubmit: (__VLS_ctx.submitReply) }, ...{ class: ("reply-composer") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("reply-composer__body") }, });
    if (__VLS_ctx.author) {
        // @ts-ignore
        [UserAvatar,];
        const __VLS_0 = __VLS_asFunctionalComponent(UserAvatar, new UserAvatar({ ...{ class: ("reply-composer__avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((30)), decorative: (true), }));
        const __VLS_1 = __VLS_0({ ...{ class: ("reply-composer__avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((30)), decorative: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
        ({}({ ...{ class: ("reply-composer__avatar") }, avatarUrl: ((__VLS_ctx.author.avatar_url)), displayName: ((__VLS_ctx.author.display_name)), username: ((__VLS_ctx.author.username)), size: ((30)), decorative: (true), }));
        // @ts-ignore
        [submitReply, author, author, author, author,];
        const __VLS_4 = __VLS_pickFunctionalComponentCtx(UserAvatar, __VLS_1);
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.textarea)({ ...{ onInput: (__VLS_ctx.resizeTextarea) }, ref: ("textareaRef"), value: ((__VLS_ctx.content)), ...{ class: ("reply-composer__textarea") }, rows: ("1"), disabled: ((__VLS_ctx.disabled || __VLS_ctx.submitting)), placeholder: ("Post your reply..."), "aria-label": ("Reply content"), });
    // @ts-ignore
    (__VLS_ctx.textareaRef);
    // @ts-ignore
    [resizeTextarea, content, disabled, submitting, textareaRef,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("reply-composer__footer") }, });
    if (__VLS_ctx.exceedsMaxLength) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("reply-composer__validation") }, role: ("alert"), });
        (__VLS_ctx.contentLength);
        (__VLS_ctx.maxContentLength);
        // @ts-ignore
        [exceedsMaxLength, contentLength, maxContentLength,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("reply-composer__submit") }, type: ("submit"), disabled: ((__VLS_ctx.disabled || __VLS_ctx.submitting || !__VLS_ctx.trimmedContent || __VLS_ctx.exceedsMaxLength)), });
    (__VLS_ctx.submitting ? 'Replying...' : 'Reply');
    // @ts-ignore
    [disabled, submitting, submitting, exceedsMaxLength, trimmedContent,];
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['reply-composer'];
        __VLS_styleScopedClasses['reply-composer__body'];
        __VLS_styleScopedClasses['reply-composer__avatar'];
        __VLS_styleScopedClasses['reply-composer__textarea'];
        __VLS_styleScopedClasses['reply-composer__footer'];
        __VLS_styleScopedClasses['reply-composer__validation'];
        __VLS_styleScopedClasses['reply-composer__submit'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                UserAvatar: UserAvatar,
                textareaRef: textareaRef,
                maxContentLength: maxContentLength,
                content: content,
                trimmedContent: trimmedContent,
                contentLength: contentLength,
                exceedsMaxLength: exceedsMaxLength,
                resizeTextarea: resizeTextarea,
                submitReply: submitReply,
            };
        },
        props: {},
        emits: {},
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {
            ...__VLS_exposed,
        };
    },
    props: {},
    emits: {},
});
;
