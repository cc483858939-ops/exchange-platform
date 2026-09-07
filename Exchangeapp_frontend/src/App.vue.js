/* __placeholder__ */
import { useRoute } from 'vue-router';
import AppShell from './components/layout/AppShell.vue';
import { initializePostViewTelemetry } from './services/postViewTelemetry';
import { useAuthStore } from './store/auth';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const route = useRoute();
const authStore = useAuthStore();
initializePostViewTelemetry(() => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && id > 0 ? id : null;
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
    let __VLS_resolvedLocalAndGlobalComponents;
    const __VLS_0 = {}.RouterView;
    ({}.RouterView);
    ({}.RouterView);
    __VLS_components.RouterView;
    __VLS_components.RouterView;
    // @ts-ignore
    [RouterView, RouterView,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({}));
    const __VLS_2 = __VLS_1({}, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({}));
    {
        const [{ Component }] = __VLS_getSlotParams((__VLS_5.slots).default);
        if (__VLS_ctx.route.meta.layout === 'auth') {
            const __VLS_6 = (Component);
            const __VLS_7 = __VLS_asFunctionalComponent(__VLS_6, new __VLS_6({}));
            const __VLS_8 = __VLS_7({}, ...__VLS_functionalComponentArgsRest(__VLS_7));
            ({}({}));
            // @ts-ignore
            [route,];
            const __VLS_11 = __VLS_pickFunctionalComponentCtx(__VLS_6, __VLS_8);
        }
        else {
            // @ts-ignore
            [AppShell, AppShell,];
            const __VLS_12 = __VLS_asFunctionalComponent(AppShell, new AppShell({}));
            const __VLS_13 = __VLS_12({}, ...__VLS_functionalComponentArgsRest(__VLS_12));
            ({}({}));
            const __VLS_17 = (Component);
            const __VLS_18 = __VLS_asFunctionalComponent(__VLS_17, new __VLS_17({}));
            const __VLS_19 = __VLS_18({}, ...__VLS_functionalComponentArgsRest(__VLS_18));
            ({}({}));
            const __VLS_22 = __VLS_pickFunctionalComponentCtx(__VLS_17, __VLS_19);
            (__VLS_16.slots).default;
            const __VLS_16 = __VLS_pickFunctionalComponentCtx(AppShell, __VLS_13);
        }
        __VLS_5.slots['' /* empty slot name completion */];
    }
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AppShell: AppShell,
                route: route,
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
