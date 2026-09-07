/* __placeholder__ */
import { onBeforeUnmount, ref, watch } from 'vue';
import { useLogout } from '../../composables/useLogout';
import AppIcon from '../icons/AppIcon.vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const { handleLogout } = useLogout();
const rootRef = ref(null);
const triggerRef = ref(null);
const menuRef = ref(null);
const isOpen = ref(false);
const removeDocumentListeners = () => {
    document.removeEventListener('pointerdown', handleDocumentPointerDown);
    document.removeEventListener('keydown', handleDocumentKeydown);
};
const closeMenu = (restoreFocus = false) => {
    const wasOpen = isOpen.value;
    isOpen.value = false;
    removeDocumentListeners();
    if (restoreFocus && wasOpen) {
        triggerRef.value?.focus();
    }
};
const handleDocumentPointerDown = (event) => {
    const target = event.target;
    if (!(target instanceof Node) || rootRef.value?.contains(target)) {
        return;
    }
    closeMenu();
};
const handleFocusOut = (event) => {
    if (!isOpen.value) {
        return;
    }
    const nextTarget = event.relatedTarget;
    if (nextTarget instanceof Node && rootRef.value?.contains(nextTarget)) {
        return;
    }
    closeMenu();
};
const handleDocumentKeydown = (event) => {
    if (event.key === 'Escape') {
        event.preventDefault();
        closeMenu(true);
    }
};
const addDocumentListeners = () => {
    document.addEventListener('pointerdown', handleDocumentPointerDown);
    document.addEventListener('keydown', handleDocumentKeydown);
};
const toggleMenu = () => {
    if (isOpen.value) {
        closeMenu(true);
        return;
    }
    isOpen.value = true;
    addDocumentListeners();
};
const handleLogoutClick = () => {
    closeMenu();
    handleLogout();
};
watch(isOpen, (open) => {
    if (!open) {
        removeDocumentListeners();
    }
});
onBeforeUnmount(() => {
    removeDocumentListeners();
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ onFocusout: (__VLS_ctx.handleFocusOut) }, ref: ("rootRef"), ...{ class: ("mobile-account-menu") }, });
    // @ts-ignore
    (__VLS_ctx.rootRef);
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.toggleMenu) }, ref: ("triggerRef"), ...{ class: ("mobile-account-menu__trigger") }, type: ("button"), "aria-label": ("Account menu"), "aria-haspopup": ("menu"), "aria-expanded": ((__VLS_ctx.isOpen)), });
    // @ts-ignore
    (__VLS_ctx.triggerRef);
    // @ts-ignore
    [AppIcon,];
    const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("more"), size: ((22)), }));
    const __VLS_1 = __VLS_0({ name: ("more"), size: ((22)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({ name: ("more"), size: ((22)), }));
    // @ts-ignore
    [handleFocusOut, rootRef, toggleMenu, isOpen, triggerRef,];
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
    if (__VLS_ctx.isOpen) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("menuRef"), ...{ class: ("mobile-account-menu__popover") }, role: ("menu"), });
        // @ts-ignore
        (__VLS_ctx.menuRef);
        const __VLS_5 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_6 = __VLS_asFunctionalComponent(__VLS_5, new __VLS_5({ ...{ 'onClick': {} }, ...{ class: ("mobile-account-menu__item") }, role: ("menuitem"), to: (({ name: 'History' })), }));
        const __VLS_7 = __VLS_6({ ...{ 'onClick': {} }, ...{ class: ("mobile-account-menu__item") }, role: ("menuitem"), to: (({ name: 'History' })), }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        ({}({ ...{ 'onClick': {} }, ...{ class: ("mobile-account-menu__item") }, role: ("menuitem"), to: (({ name: 'History' })), }));
        let __VLS_11;
        const __VLS_12 = {
            onClick: (...[$event]) => {
                if (!((__VLS_ctx.isOpen)))
                    return;
                __VLS_ctx.closeMenu();
                // @ts-ignore
                [isOpen, menuRef, closeMenu,];
            }
        };
        // @ts-ignore
        [AppIcon,];
        const __VLS_13 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("history"), size: ((18)), }));
        const __VLS_14 = __VLS_13({ name: ("history"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_13));
        ({}({ name: ("history"), size: ((18)), }));
        const __VLS_17 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_14);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_10.slots).default;
        const __VLS_10 = __VLS_pickFunctionalComponentCtx(__VLS_5, __VLS_7);
        let __VLS_8;
        let __VLS_9;
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.handleLogoutClick) }, ...{ class: ("mobile-account-menu__item mobile-account-menu__item--logout") }, type: ("button"), role: ("menuitem"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_18 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("logout"), size: ((18)), }));
        const __VLS_19 = __VLS_18({ name: ("logout"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_18));
        ({}({ name: ("logout"), size: ((18)), }));
        // @ts-ignore
        [handleLogoutClick,];
        const __VLS_22 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_19);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['mobile-account-menu'];
        __VLS_styleScopedClasses['mobile-account-menu__trigger'];
        __VLS_styleScopedClasses['mobile-account-menu__popover'];
        __VLS_styleScopedClasses['mobile-account-menu__item'];
        __VLS_styleScopedClasses['mobile-account-menu__item'];
        __VLS_styleScopedClasses['mobile-account-menu__item--logout'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppIcon: AppIcon,
                rootRef: rootRef,
                triggerRef: triggerRef,
                menuRef: menuRef,
                isOpen: isOpen,
                closeMenu: closeMenu,
                handleFocusOut: handleFocusOut,
                toggleMenu: toggleMenu,
                handleLogoutClick: handleLogoutClick,
            };
        },
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
;
