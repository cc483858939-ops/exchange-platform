/* __placeholder__ */
import { ref, onMounted } from 'vue';
import { ElMessage } from 'element-plus';
import axios from '../axios';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const form = ref({
    fromCurrency: '',
    toCurrency: '',
    amount: 0,
});
const result = ref(null);
const currencies = ref([]);
const rates = ref([]);
const fetchCurrencies = async () => {
    try {
        const response = await axios.get('/exchangeRates');
        rates.value = response.data;
        currencies.value = [...new Set(response.data.map((rate) => [rate.fromCurrency, rate.toCurrency]).flat())];
    }
    catch (error) {
        console.log('Failed to load currencies', error);
        ElMessage.error('汇率数据加载失败，请稍后重试');
    }
};
const exchange = () => {
    if (!form.value.fromCurrency) {
        ElMessage.error('请选择要兑换的货币');
        result.value = null;
        return;
    }
    if (!form.value.toCurrency) {
        ElMessage.error('请选择兑换后的货币');
        result.value = null;
        return;
    }
    const amount = Number(form.value.amount);
    if (!amount || amount <= 0) {
        ElMessage.error('请输入有效金额');
        result.value = null;
        return;
    }
    const rate = rates.value.find((rate) => rate.fromCurrency === form.value.fromCurrency && rate.toCurrency === form.value.toCurrency)?.rate;
    if (rate) {
        result.value = amount * rate;
    }
    else {
        result.value = null;
        ElMessage.error('暂未找到该货币兑换路径');
    }
};
onMounted(fetchCurrencies);
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
    const __VLS_0 = {}.ElContainer;
    ({}.ElContainer);
    ({}.ElContainer);
    __VLS_components.ElContainer;
    __VLS_components.elContainer;
    __VLS_components.ElContainer;
    __VLS_components.elContainer;
    // @ts-ignore
    [ElContainer, ElContainer,];
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({}));
    const __VLS_2 = __VLS_1({}, ...__VLS_functionalComponentArgsRest(__VLS_1));
    ({}({}));
    const __VLS_6 = {}.ElForm;
    ({}.ElForm);
    ({}.ElForm);
    __VLS_components.ElForm;
    __VLS_components.elForm;
    __VLS_components.ElForm;
    __VLS_components.elForm;
    // @ts-ignore
    [ElForm, ElForm,];
    const __VLS_7 = __VLS_asFunctionalComponent(__VLS_6, new __VLS_6({ model: ((__VLS_ctx.form)), ...{ class: ("exchange-form") }, }));
    const __VLS_8 = __VLS_7({ model: ((__VLS_ctx.form)), ...{ class: ("exchange-form") }, }, ...__VLS_functionalComponentArgsRest(__VLS_7));
    ({}({ model: ((__VLS_ctx.form)), ...{ class: ("exchange-form") }, }));
    const __VLS_12 = {}.ElFormItem;
    ({}.ElFormItem);
    ({}.ElFormItem);
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    // @ts-ignore
    [ElFormItem, ElFormItem,];
    const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({ label: ("从货币"), labelWidth: ("80px"), }));
    const __VLS_14 = __VLS_13({ label: ("从货币"), labelWidth: ("80px"), }, ...__VLS_functionalComponentArgsRest(__VLS_13));
    ({}({ label: ("从货币"), labelWidth: ("80px"), }));
    const __VLS_18 = {}.ElSelect;
    ({}.ElSelect);
    ({}.ElSelect);
    __VLS_components.ElSelect;
    __VLS_components.elSelect;
    __VLS_components.ElSelect;
    __VLS_components.elSelect;
    // @ts-ignore
    [ElSelect, ElSelect,];
    const __VLS_19 = __VLS_asFunctionalComponent(__VLS_18, new __VLS_18({ modelValue: ((__VLS_ctx.form.fromCurrency)), placeholder: ("选择货币"), }));
    const __VLS_20 = __VLS_19({ modelValue: ((__VLS_ctx.form.fromCurrency)), placeholder: ("选择货币"), }, ...__VLS_functionalComponentArgsRest(__VLS_19));
    ({}({ modelValue: ((__VLS_ctx.form.fromCurrency)), placeholder: ("选择货币"), }));
    for (const [currency] of __VLS_getVForSourceType((__VLS_ctx.currencies))) {
        const __VLS_24 = {}.ElOption;
        ({}.ElOption);
        __VLS_components.ElOption;
        __VLS_components.elOption;
        // @ts-ignore
        [ElOption,];
        const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({ key: ((currency)), label: ((currency)), value: ((currency)), }));
        const __VLS_26 = __VLS_25({ key: ((currency)), label: ((currency)), value: ((currency)), }, ...__VLS_functionalComponentArgsRest(__VLS_25));
        ({}({ key: ((currency)), label: ((currency)), value: ((currency)), }));
        // @ts-ignore
        [form, form, currencies,];
        const __VLS_29 = __VLS_pickFunctionalComponentCtx(__VLS_24, __VLS_26);
    }
    (__VLS_23.slots).default;
    const __VLS_23 = __VLS_pickFunctionalComponentCtx(__VLS_18, __VLS_20);
    (__VLS_17.slots).default;
    const __VLS_17 = __VLS_pickFunctionalComponentCtx(__VLS_12, __VLS_14);
    const __VLS_30 = {}.ElFormItem;
    ({}.ElFormItem);
    ({}.ElFormItem);
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    // @ts-ignore
    [ElFormItem, ElFormItem,];
    const __VLS_31 = __VLS_asFunctionalComponent(__VLS_30, new __VLS_30({ label: ("到货币"), labelWidth: ("80px"), }));
    const __VLS_32 = __VLS_31({ label: ("到货币"), labelWidth: ("80px"), }, ...__VLS_functionalComponentArgsRest(__VLS_31));
    ({}({ label: ("到货币"), labelWidth: ("80px"), }));
    const __VLS_36 = {}.ElSelect;
    ({}.ElSelect);
    ({}.ElSelect);
    __VLS_components.ElSelect;
    __VLS_components.elSelect;
    __VLS_components.ElSelect;
    __VLS_components.elSelect;
    // @ts-ignore
    [ElSelect, ElSelect,];
    const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({ modelValue: ((__VLS_ctx.form.toCurrency)), placeholder: ("选择货币"), }));
    const __VLS_38 = __VLS_37({ modelValue: ((__VLS_ctx.form.toCurrency)), placeholder: ("选择货币"), }, ...__VLS_functionalComponentArgsRest(__VLS_37));
    ({}({ modelValue: ((__VLS_ctx.form.toCurrency)), placeholder: ("选择货币"), }));
    for (const [currency] of __VLS_getVForSourceType((__VLS_ctx.currencies))) {
        const __VLS_42 = {}.ElOption;
        ({}.ElOption);
        __VLS_components.ElOption;
        __VLS_components.elOption;
        // @ts-ignore
        [ElOption,];
        const __VLS_43 = __VLS_asFunctionalComponent(__VLS_42, new __VLS_42({ key: ((currency)), label: ((currency)), value: ((currency)), }));
        const __VLS_44 = __VLS_43({ key: ((currency)), label: ((currency)), value: ((currency)), }, ...__VLS_functionalComponentArgsRest(__VLS_43));
        ({}({ key: ((currency)), label: ((currency)), value: ((currency)), }));
        // @ts-ignore
        [form, currencies,];
        const __VLS_47 = __VLS_pickFunctionalComponentCtx(__VLS_42, __VLS_44);
    }
    (__VLS_41.slots).default;
    const __VLS_41 = __VLS_pickFunctionalComponentCtx(__VLS_36, __VLS_38);
    (__VLS_35.slots).default;
    const __VLS_35 = __VLS_pickFunctionalComponentCtx(__VLS_30, __VLS_32);
    const __VLS_48 = {}.ElFormItem;
    ({}.ElFormItem);
    ({}.ElFormItem);
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    // @ts-ignore
    [ElFormItem, ElFormItem,];
    const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({ label: ("金额"), labelWidth: ("80px"), }));
    const __VLS_50 = __VLS_49({ label: ("金额"), labelWidth: ("80px"), }, ...__VLS_functionalComponentArgsRest(__VLS_49));
    ({}({ label: ("金额"), labelWidth: ("80px"), }));
    const __VLS_54 = {}.ElInput;
    ({}.ElInput);
    __VLS_components.ElInput;
    __VLS_components.elInput;
    // @ts-ignore
    [ElInput,];
    const __VLS_55 = __VLS_asFunctionalComponent(__VLS_54, new __VLS_54({ modelValue: ((__VLS_ctx.form.amount)), type: ("number"), placeholder: ("输入金额"), }));
    const __VLS_56 = __VLS_55({ modelValue: ((__VLS_ctx.form.amount)), type: ("number"), placeholder: ("输入金额"), }, ...__VLS_functionalComponentArgsRest(__VLS_55));
    ({}({ modelValue: ((__VLS_ctx.form.amount)), type: ("number"), placeholder: ("输入金额"), }));
    // @ts-ignore
    [form,];
    const __VLS_59 = __VLS_pickFunctionalComponentCtx(__VLS_54, __VLS_56);
    (__VLS_53.slots).default;
    const __VLS_53 = __VLS_pickFunctionalComponentCtx(__VLS_48, __VLS_50);
    const __VLS_60 = {}.ElFormItem;
    ({}.ElFormItem);
    ({}.ElFormItem);
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    __VLS_components.ElFormItem;
    __VLS_components.elFormItem;
    // @ts-ignore
    [ElFormItem, ElFormItem,];
    const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({}));
    const __VLS_62 = __VLS_61({}, ...__VLS_functionalComponentArgsRest(__VLS_61));
    ({}({}));
    const __VLS_66 = {}.ElButton;
    ({}.ElButton);
    ({}.ElButton);
    __VLS_components.ElButton;
    __VLS_components.elButton;
    __VLS_components.ElButton;
    __VLS_components.elButton;
    // @ts-ignore
    [ElButton, ElButton,];
    const __VLS_67 = __VLS_asFunctionalComponent(__VLS_66, new __VLS_66({ ...{ 'onClick': {} }, type: ("primary"), }));
    const __VLS_68 = __VLS_67({ ...{ 'onClick': {} }, type: ("primary"), }, ...__VLS_functionalComponentArgsRest(__VLS_67));
    ({}({ ...{ 'onClick': {} }, type: ("primary"), }));
    let __VLS_72;
    const __VLS_73 = {
        onClick: (__VLS_ctx.exchange)
    };
    // @ts-ignore
    [exchange,];
    (__VLS_71.slots).default;
    const __VLS_71 = __VLS_pickFunctionalComponentCtx(__VLS_66, __VLS_68);
    let __VLS_69;
    let __VLS_70;
    (__VLS_65.slots).default;
    const __VLS_65 = __VLS_pickFunctionalComponentCtx(__VLS_60, __VLS_62);
    (__VLS_11.slots).default;
    const __VLS_11 = __VLS_pickFunctionalComponentCtx(__VLS_6, __VLS_8);
    if (__VLS_ctx.result) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("result") }, });
        __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
        (__VLS_ctx.result);
        // @ts-ignore
        [result, result,];
    }
    (__VLS_5.slots).default;
    const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['exchange-form'];
        __VLS_styleScopedClasses['result'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                form: form,
                result: result,
                currencies: currencies,
                exchange: exchange,
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
