/* __placeholder__ */
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { resolveSafeLoginReturnTarget } from '../router/loginReturnTarget';
import { useAuthStore } from '../store/auth';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const form = ref({
    username: '',
    password: '',
});
const submitting = ref(false);
const formError = ref('');
const authStore = useAuthStore();
const route = useRoute();
const router = useRouter();
const returnTarget = computed(() => resolveSafeLoginReturnTarget(router, route.query.returnTo));
onMounted(() => {
    if (authStore.isAuthenticated) {
        void router.replace(returnTarget.value ?? { name: 'Home' });
    }
});
const formatLoginError = (error) => {
    const message = error instanceof Error ? error.message : '';
    if (message === 'Invalid username or password') {
        return 'Invalid username or password.';
    }
    return 'Could not log in. Please try again.';
};
const login = async () => {
    if (submitting.value) {
        return;
    }
    formError.value = '';
    if (form.value.username.length === 0) {
        formError.value = 'Enter your username.';
        return;
    }
    if (form.value.password.length === 0) {
        formError.value = 'Enter your password.';
        return;
    }
    submitting.value = true;
    try {
        await authStore.login(form.value.username, form.value.password);
        void router.replace(returnTarget.value ?? { name: 'Home' });
    }
    catch (error) {
        formError.value = formatLoginError(error);
    }
    finally {
        submitting.value = false;
    }
};
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("auth-page") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("auth-layout") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("auth-content") }, "aria-labelledby": ("login-title"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("auth-brand") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("auth-brand__mobile-mark") }, "aria-hidden": ("true"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("auth-brand__name") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("auth-heading") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({ id: ("login-title"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({ ...{ onSubmit: (__VLS_ctx.login) }, ...{ class: ("auth-form") }, "aria-busy": ((__VLS_ctx.submitting)), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("auth-field") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ for: ("login-username"), });
    // @ts-ignore
    [login, submitting,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ id: ("login-username"), value: ((__VLS_ctx.form.username)), name: ("username"), type: ("text"), autocomplete: ("username"), autocapitalize: ("none"), spellcheck: ("false"), placeholder: ("Enter your username"), disabled: ((__VLS_ctx.submitting)), });
    // @ts-ignore
    [submitting, form,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("auth-field") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({ for: ("login-password"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.input)({ id: ("login-password"), name: ("password"), type: ("password"), autocomplete: ("current-password"), placeholder: ("Enter your password"), disabled: ((__VLS_ctx.submitting)), });
    (__VLS_ctx.form.password);
    // @ts-ignore
    [submitting, form,];
    if (__VLS_ctx.formError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("auth-error") }, role: ("alert"), "aria-live": ("assertive"), });
        (__VLS_ctx.formError);
        // @ts-ignore
        [formError, formError,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ class: ("auth-submit") }, type: ("submit"), disabled: ((__VLS_ctx.submitting)), "aria-busy": ((__VLS_ctx.submitting)), });
    (__VLS_ctx.submitting ? 'Signing in…' : 'Log in');
    // @ts-ignore
    [submitting, submitting, submitting,];
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("auth-switch") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    const __VLS_0 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ to: (({ name: 'Register' })), }));
    const __VLS_2 = __VLS_1({ to: (({ name: 'Register' })), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({ to: (({ name: 'Register' })), }));
    (__VLS_5.slots).default;
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("auth-visual") }, "aria-hidden": ("true"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("auth-visual__mark") }, });
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['auth-page'];
        __VLS_styleScopedClasses['auth-layout'];
        __VLS_styleScopedClasses['auth-content'];
        __VLS_styleScopedClasses['auth-brand'];
        __VLS_styleScopedClasses['auth-brand__mobile-mark'];
        __VLS_styleScopedClasses['auth-brand__name'];
        __VLS_styleScopedClasses['auth-heading'];
        __VLS_styleScopedClasses['auth-form'];
        __VLS_styleScopedClasses['auth-field'];
        __VLS_styleScopedClasses['auth-field'];
        __VLS_styleScopedClasses['auth-error'];
        __VLS_styleScopedClasses['auth-submit'];
        __VLS_styleScopedClasses['auth-switch'];
        __VLS_styleScopedClasses['auth-visual'];
        __VLS_styleScopedClasses['auth-visual__mark'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                form: form,
                submitting: submitting,
                formError: formError,
                login: login,
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
