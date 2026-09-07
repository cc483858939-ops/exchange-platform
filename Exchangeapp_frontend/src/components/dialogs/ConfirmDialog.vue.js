/* __placeholder__ */
import { onBeforeUnmount, onMounted, ref } from 'vue';
let confirmDialogSequence = 0;
const nextConfirmDialogInstanceID = () => `confirm-dialog-${++confirmDialogSequence}`;
export default await (async () => {
    const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
    let __VLS_typeProps;
    const props = withDefaults(defineProps(), {
        confirmLabel: 'Confirm',
        cancelLabel: 'Cancel',
        danger: false,
        busy: false,
        error: '',
    });
    const emit = defineEmits();
    const dialogRef = ref(null);
    const cancelButtonRef = ref(null);
    const instanceID = nextConfirmDialogInstanceID();
    const titleID = `${instanceID}-title`;
    const descriptionID = `${instanceID}-description`;
    const handleCancel = (event) => {
        event.preventDefault();
        if (!props.busy) {
            emit('cancel');
        }
    };
    onMounted(() => {
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
        cancelButtonRef.value?.focus();
    });
    onBeforeUnmount(() => {
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
    const __VLS_withDefaultsArg = (function (t) { return t; })({
        confirmLabel: 'Confirm',
        cancelLabel: 'Cancel',
        danger: false,
        busy: false,
        error: '',
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
        __VLS_elementAsFunction(__VLS_intrinsicElements.dialog, __VLS_intrinsicElements.dialog)({ ...{ onCancel: (__VLS_ctx.handleCancel) }, ref: ("dialogRef"), ...{ class: ("confirm-dialog") }, "aria-modal": ("true"), "aria-labelledby": ((__VLS_ctx.titleID)), "aria-describedby": ((__VLS_ctx.descriptionID)), });
        // @ts-ignore
        (__VLS_ctx.dialogRef);
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("confirm-dialog__panel") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ((__VLS_ctx.titleID)), ...{ class: ("confirm-dialog__title") }, });
        (__VLS_ctx.title);
        // @ts-ignore
        [handleCancel, titleID, titleID, descriptionID, dialogRef, title,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ id: ((__VLS_ctx.descriptionID)), ...{ class: ("confirm-dialog__description") }, });
        (__VLS_ctx.description);
        // @ts-ignore
        [descriptionID, description,];
        if (__VLS_ctx.error) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("confirm-dialog__error") }, role: ("alert"), "aria-live": ("polite"), });
            (__VLS_ctx.error);
            // @ts-ignore
            [error, error,];
        }
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("confirm-dialog__actions") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    __VLS_ctx.emit('cancel');
                    // @ts-ignore
                    [emit,];
                } }, ref: ("cancelButtonRef"), ...{ class: ("confirm-dialog__button confirm-dialog__button--cancel") }, type: ("button"), disabled: ((__VLS_ctx.busy)), });
        // @ts-ignore
        (__VLS_ctx.cancelButtonRef);
        (__VLS_ctx.cancelLabel);
        // @ts-ignore
        [busy, cancelButtonRef, cancelLabel,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    __VLS_ctx.emit('confirm');
                    // @ts-ignore
                    [emit,];
                } }, ...{ class: ("confirm-dialog__button confirm-dialog__button--confirm") }, ...{ class: (({ 'confirm-dialog__button--danger': __VLS_ctx.danger })) }, type: ("button"), disabled: ((__VLS_ctx.busy)), "aria-busy": ((__VLS_ctx.busy ? 'true' : undefined)), });
        __VLS_styleScopedClasses = ({ 'confirm-dialog__button--danger': danger });
        (__VLS_ctx.busy ? 'Deleting…' : __VLS_ctx.confirmLabel);
        // @ts-ignore
        [busy, busy, busy, danger, confirmLabel,];
        if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
            __VLS_styleScopedClasses['confirm-dialog'];
            __VLS_styleScopedClasses['confirm-dialog__panel'];
            __VLS_styleScopedClasses['confirm-dialog__title'];
            __VLS_styleScopedClasses['confirm-dialog__description'];
            __VLS_styleScopedClasses['confirm-dialog__error'];
            __VLS_styleScopedClasses['confirm-dialog__actions'];
            __VLS_styleScopedClasses['confirm-dialog__button'];
            __VLS_styleScopedClasses['confirm-dialog__button--cancel'];
            __VLS_styleScopedClasses['confirm-dialog__button'];
            __VLS_styleScopedClasses['confirm-dialog__button--confirm'];
        }
        var __VLS_slots;
        return __VLS_slots;
        const __VLS_componentsOption = {};
        let __VLS_name;
        const __VLS_internalComponent = (await import('vue')).defineComponent({
            setup() {
                return {
                    emit: emit,
                    dialogRef: dialogRef,
                    cancelButtonRef: cancelButtonRef,
                    titleID: titleID,
                    descriptionID: descriptionID,
                    handleCancel: handleCancel,
                };
            },
            props: {},
            emits: {},
        });
    }
    return (await import('vue')).defineComponent({
        setup() {
            return {};
        },
        props: {},
        emits: {},
    });
})();
