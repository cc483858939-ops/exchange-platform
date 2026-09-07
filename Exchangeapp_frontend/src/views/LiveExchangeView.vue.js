/* __placeholder__ */
import { onBeforeUnmount, onMounted, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { ElAlert, ElButton, ElForm, ElFormItem, ElInput, ElMessage, ElOption, ElSelect, ElSkeleton, } from 'element-plus';
import 'element-plus/dist/index.css';
import { useExchangeSessionStore } from '../store/exchangeSession';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const exchangeSession = useExchangeSessionStore();
const { currencies, market, quote, form, loaded, loadError, refreshing, refreshError, quoting, quoteError, } = storeToRefs(exchangeSession);
let mounted = false;
let exchangeEntryVersion = 0;
let restoredEntryVersion = -1;
const restoreScrollOnce = async () => {
    const entryVersion = exchangeEntryVersion;
    if (!mounted || restoredEntryVersion === entryVersion || !loaded.value)
        return;
    await Promise.resolve();
    if (!mounted || entryVersion !== exchangeEntryVersion || restoredEntryVersion === entryVersion)
        return;
    if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
        window.scrollTo({ top: exchangeSession.scrollY, behavior: 'auto' });
    }
    restoredEntryVersion = entryVersion;
};
const loadCurrencies = () => { void exchangeSession.loadCurrencies({ force: true }); };
const requestQuote = async () => {
    if (!form.value.fromCurrency || !form.value.toCurrency) {
        ElMessage.error('请选择要兑换的两种货币');
        return;
    }
    if (!/^\d+(\.\d+)?$/.test(form.value.amount) || Number(form.value.amount) <= 0) {
        ElMessage.error('请输入大于零的金额');
        return;
    }
    const result = await exchangeSession.requestQuote();
    if (!mounted || !result.applied)
        return;
    if (!result.success) {
        ElMessage.error(quoteError.value || '暂时无法获取报价，请稍后重试。');
    }
    else if (result.data?.freshness === 'stale') {
        ElMessage.warning('当前结果使用最近缓存行情');
    }
};
const swapCurrencies = async () => {
    const shouldRefreshQuote = exchangeSession.swapCurrencies();
    if (shouldRefreshQuote) {
        await requestQuote();
    }
};
const displayNumber = (value) => {
    const [whole, fraction] = value.split('.');
    const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    return fraction ? `${grouped}.${fraction}` : grouped;
};
watch(loaded, () => { void restoreScrollOnce(); }, { flush: 'post' });
onMounted(() => {
    mounted = true;
    void exchangeSession.loadCurrencies();
    void restoreScrollOnce();
});
onBeforeUnmount(() => {
    mounted = false;
    exchangeEntryVersion += 1;
    if (typeof window !== 'undefined')
        exchangeSession.saveScroll(window.scrollY);
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("exchange-page") }, "aria-labelledby": ("exchange-title"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("exchange-header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({ id: ("exchange-title"), });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("exchange-content") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("exchange-intro") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("exchange-intro__copy") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("eyebrow") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({});
    if (__VLS_ctx.market) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("market-status") }, ...{ class: (({ stale: __VLS_ctx.market.freshness === 'stale' })) }, });
        __VLS_styleScopedClasses = ({ stale: market.freshness === 'stale' });
        __VLS_elementAsFunction(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
        (__VLS_ctx.market.freshness === 'stale' ? '缓存行情' : '最新行情');
        // @ts-ignore
        [market, market, market,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.market.source);
        // @ts-ignore
        [market,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
        (__VLS_ctx.market.asOf);
        // @ts-ignore
        [market,];
    }
    if (__VLS_ctx.market?.freshness === 'stale') {
        const __VLS_0 = {}.ElAlert;
        ({}.ElAlert);
        __VLS_components.ElAlert;
        __VLS_components.elAlert;
        // @ts-ignore
        [ElAlert,];
        const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({ ...{ class: ("page-alert") }, title: ("上游行情暂时不可用，当前报价使用最近缓存。"), type: ("warning"), closable: ((false)), showIcon: (true), }));
        const __VLS_2 = __VLS_1({ ...{ class: ("page-alert") }, title: ("上游行情暂时不可用，当前报价使用最近缓存。"), type: ("warning"), closable: ((false)), showIcon: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_1));
        ({}({ ...{ class: ("page-alert") }, title: ("上游行情暂时不可用，当前报价使用最近缓存。"), type: ("warning"), closable: ((false)), showIcon: (true), }));
        // @ts-ignore
        [market,];
        const __VLS_5 = __VLS_pickFunctionalComponentCtx(__VLS_0, __VLS_2);
    }
    if (__VLS_ctx.refreshError) {
        const __VLS_6 = {}.ElAlert;
        ({}.ElAlert);
        __VLS_components.ElAlert;
        __VLS_components.elAlert;
        // @ts-ignore
        [ElAlert,];
        const __VLS_7 = __VLS_asFunctionalComponent(__VLS_6, new __VLS_6({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.refreshError)), type: ("warning"), closable: ((false)), showIcon: (true), }));
        const __VLS_8 = __VLS_7({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.refreshError)), type: ("warning"), closable: ((false)), showIcon: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_7));
        ({}({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.refreshError)), type: ("warning"), closable: ((false)), showIcon: (true), }));
        // @ts-ignore
        [refreshError, refreshError,];
        const __VLS_11 = __VLS_pickFunctionalComponentCtx(__VLS_6, __VLS_8);
    }
    if (!__VLS_ctx.loaded && !__VLS_ctx.loadError) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("skeleton") }, });
        const __VLS_12 = {}.ElSkeleton;
        ({}.ElSkeleton);
        __VLS_components.ElSkeleton;
        __VLS_components.elSkeleton;
        // @ts-ignore
        [ElSkeleton,];
        const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({ animated: (true), rows: ((5)), }));
        const __VLS_14 = __VLS_13({ animated: (true), rows: ((5)), }, ...__VLS_functionalComponentArgsRest(__VLS_13));
        ({}({ animated: (true), rows: ((5)), }));
        // @ts-ignore
        [loaded, loadError,];
        const __VLS_17 = __VLS_pickFunctionalComponentCtx(__VLS_12, __VLS_14);
    }
    else if (__VLS_ctx.loadError) {
        const __VLS_18 = {}.ElAlert;
        ({}.ElAlert);
        ({}.ElAlert);
        __VLS_components.ElAlert;
        __VLS_components.elAlert;
        __VLS_components.ElAlert;
        __VLS_components.elAlert;
        // @ts-ignore
        [ElAlert, ElAlert,];
        const __VLS_19 = __VLS_asFunctionalComponent(__VLS_18, new __VLS_18({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.loadError)), type: ("error"), closable: ((false)), showIcon: (true), }));
        const __VLS_20 = __VLS_19({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.loadError)), type: ("error"), closable: ((false)), showIcon: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_19));
        ({}({ ...{ class: ("page-alert") }, title: ((__VLS_ctx.loadError)), type: ("error"), closable: ((false)), showIcon: (true), }));
        __VLS_elementAsFunction(__VLS_intrinsicElements.template, __VLS_intrinsicElements.template)({});
        {
            (__VLS_23.slots).default;
            const __VLS_24 = {}.ElButton;
            ({}.ElButton);
            ({}.ElButton);
            __VLS_components.ElButton;
            __VLS_components.elButton;
            __VLS_components.ElButton;
            __VLS_components.elButton;
            // @ts-ignore
            [ElButton, ElButton,];
            const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({ ...{ 'onClick': {} }, type: ("primary"), plain: (true), }));
            const __VLS_26 = __VLS_25({ ...{ 'onClick': {} }, type: ("primary"), plain: (true), }, ...__VLS_functionalComponentArgsRest(__VLS_25));
            ({}({ ...{ 'onClick': {} }, type: ("primary"), plain: (true), }));
            let __VLS_30;
            const __VLS_31 = {
                onClick: (__VLS_ctx.loadCurrencies)
            };
            // @ts-ignore
            [loadError, loadError, loadCurrencies,];
            (__VLS_29.slots).default;
            const __VLS_29 = __VLS_pickFunctionalComponentCtx(__VLS_24, __VLS_26);
            let __VLS_27;
            let __VLS_28;
        }
        const __VLS_23 = __VLS_pickFunctionalComponentCtx(__VLS_18, __VLS_20);
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("exchange-layout") }, });
        const __VLS_32 = {}.ElForm;
        ({}.ElForm);
        ({}.ElForm);
        __VLS_components.ElForm;
        __VLS_components.elForm;
        __VLS_components.ElForm;
        __VLS_components.elForm;
        // @ts-ignore
        [ElForm, ElForm,];
        const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({ ...{ 'onSubmit': {} }, ...{ class: ("exchange-form") }, labelPosition: ("top"), }));
        const __VLS_34 = __VLS_33({ ...{ 'onSubmit': {} }, ...{ class: ("exchange-form") }, labelPosition: ("top"), }, ...__VLS_functionalComponentArgsRest(__VLS_33));
        ({}({ ...{ 'onSubmit': {} }, ...{ class: ("exchange-form") }, labelPosition: ("top"), }));
        let __VLS_38;
        const __VLS_39 = {
            onSubmit: (__VLS_ctx.requestQuote)
        };
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("currency-grid") }, });
        const __VLS_40 = {}.ElFormItem;
        ({}.ElFormItem);
        ({}.ElFormItem);
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        // @ts-ignore
        [ElFormItem, ElFormItem,];
        const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({ label: ("币种"), }));
        const __VLS_42 = __VLS_41({ label: ("币种"), }, ...__VLS_functionalComponentArgsRest(__VLS_41));
        ({}({ label: ("币种"), }));
        const __VLS_46 = {}.ElSelect;
        ({}.ElSelect);
        ({}.ElSelect);
        __VLS_components.ElSelect;
        __VLS_components.elSelect;
        __VLS_components.ElSelect;
        __VLS_components.elSelect;
        // @ts-ignore
        [ElSelect, ElSelect,];
        const __VLS_47 = __VLS_asFunctionalComponent(__VLS_46, new __VLS_46({ modelValue: ((__VLS_ctx.form.fromCurrency)), filterable: (true), placeholder: ("选择货币"), }));
        const __VLS_48 = __VLS_47({ modelValue: ((__VLS_ctx.form.fromCurrency)), filterable: (true), placeholder: ("选择货币"), }, ...__VLS_functionalComponentArgsRest(__VLS_47));
        ({}({ modelValue: ((__VLS_ctx.form.fromCurrency)), filterable: (true), placeholder: ("选择货币"), }));
        for (const [currency] of __VLS_getVForSourceType((__VLS_ctx.currencies))) {
            const __VLS_52 = {}.ElOption;
            ({}.ElOption);
            __VLS_components.ElOption;
            __VLS_components.elOption;
            // @ts-ignore
            [ElOption,];
            const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({ key: (('from-' + currency)), label: ((currency)), value: ((currency)), }));
            const __VLS_54 = __VLS_53({ key: (('from-' + currency)), label: ((currency)), value: ((currency)), }, ...__VLS_functionalComponentArgsRest(__VLS_53));
            ({}({ key: (('from-' + currency)), label: ((currency)), value: ((currency)), }));
            // @ts-ignore
            [requestQuote, form, currencies,];
            const __VLS_57 = __VLS_pickFunctionalComponentCtx(__VLS_52, __VLS_54);
        }
        (__VLS_51.slots).default;
        const __VLS_51 = __VLS_pickFunctionalComponentCtx(__VLS_46, __VLS_48);
        (__VLS_45.slots).default;
        const __VLS_45 = __VLS_pickFunctionalComponentCtx(__VLS_40, __VLS_42);
        const __VLS_58 = {}.ElButton;
        ({}.ElButton);
        ({}.ElButton);
        __VLS_components.ElButton;
        __VLS_components.elButton;
        __VLS_components.ElButton;
        __VLS_components.elButton;
        // @ts-ignore
        [ElButton, ElButton,];
        const __VLS_59 = __VLS_asFunctionalComponent(__VLS_58, new __VLS_58({ ...{ 'onClick': {} }, ...{ class: ("swap-button") }, plain: (true), disabled: ((!__VLS_ctx.form.fromCurrency || !__VLS_ctx.form.toCurrency)), }));
        const __VLS_60 = __VLS_59({ ...{ 'onClick': {} }, ...{ class: ("swap-button") }, plain: (true), disabled: ((!__VLS_ctx.form.fromCurrency || !__VLS_ctx.form.toCurrency)), }, ...__VLS_functionalComponentArgsRest(__VLS_59));
        ({}({ ...{ 'onClick': {} }, ...{ class: ("swap-button") }, plain: (true), disabled: ((!__VLS_ctx.form.fromCurrency || !__VLS_ctx.form.toCurrency)), }));
        let __VLS_64;
        const __VLS_65 = {
            onClick: (__VLS_ctx.swapCurrencies)
        };
        // @ts-ignore
        [form, form, swapCurrencies,];
        (__VLS_63.slots).default;
        const __VLS_63 = __VLS_pickFunctionalComponentCtx(__VLS_58, __VLS_60);
        let __VLS_61;
        let __VLS_62;
        const __VLS_66 = {}.ElFormItem;
        ({}.ElFormItem);
        ({}.ElFormItem);
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        // @ts-ignore
        [ElFormItem, ElFormItem,];
        const __VLS_67 = __VLS_asFunctionalComponent(__VLS_66, new __VLS_66({ label: ("币种"), }));
        const __VLS_68 = __VLS_67({ label: ("币种"), }, ...__VLS_functionalComponentArgsRest(__VLS_67));
        ({}({ label: ("币种"), }));
        const __VLS_72 = {}.ElSelect;
        ({}.ElSelect);
        ({}.ElSelect);
        __VLS_components.ElSelect;
        __VLS_components.elSelect;
        __VLS_components.ElSelect;
        __VLS_components.elSelect;
        // @ts-ignore
        [ElSelect, ElSelect,];
        const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({ modelValue: ((__VLS_ctx.form.toCurrency)), filterable: (true), placeholder: ("选择货币"), }));
        const __VLS_74 = __VLS_73({ modelValue: ((__VLS_ctx.form.toCurrency)), filterable: (true), placeholder: ("选择货币"), }, ...__VLS_functionalComponentArgsRest(__VLS_73));
        ({}({ modelValue: ((__VLS_ctx.form.toCurrency)), filterable: (true), placeholder: ("选择货币"), }));
        for (const [currency] of __VLS_getVForSourceType((__VLS_ctx.currencies))) {
            const __VLS_78 = {}.ElOption;
            ({}.ElOption);
            __VLS_components.ElOption;
            __VLS_components.elOption;
            // @ts-ignore
            [ElOption,];
            const __VLS_79 = __VLS_asFunctionalComponent(__VLS_78, new __VLS_78({ key: (('to-' + currency)), label: ((currency)), value: ((currency)), }));
            const __VLS_80 = __VLS_79({ key: (('to-' + currency)), label: ((currency)), value: ((currency)), }, ...__VLS_functionalComponentArgsRest(__VLS_79));
            ({}({ key: (('to-' + currency)), label: ((currency)), value: ((currency)), }));
            // @ts-ignore
            [form, currencies,];
            const __VLS_83 = __VLS_pickFunctionalComponentCtx(__VLS_78, __VLS_80);
        }
        (__VLS_77.slots).default;
        const __VLS_77 = __VLS_pickFunctionalComponentCtx(__VLS_72, __VLS_74);
        (__VLS_71.slots).default;
        const __VLS_71 = __VLS_pickFunctionalComponentCtx(__VLS_66, __VLS_68);
        const __VLS_84 = {}.ElFormItem;
        ({}.ElFormItem);
        ({}.ElFormItem);
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        __VLS_components.ElFormItem;
        __VLS_components.elFormItem;
        // @ts-ignore
        [ElFormItem, ElFormItem,];
        const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({ label: ("金额"), }));
        const __VLS_86 = __VLS_85({ label: ("金额"), }, ...__VLS_functionalComponentArgsRest(__VLS_85));
        ({}({ label: ("金额"), }));
        const __VLS_90 = {}.ElInput;
        ({}.ElInput);
        __VLS_components.ElInput;
        __VLS_components.elInput;
        // @ts-ignore
        [ElInput,];
        const __VLS_91 = __VLS_asFunctionalComponent(__VLS_90, new __VLS_90({ ...{ 'onKeyup': {} }, modelValue: ((__VLS_ctx.form.amount)), inputmode: ("decimal"), placeholder: ("例如 100"), }));
        const __VLS_92 = __VLS_91({ ...{ 'onKeyup': {} }, modelValue: ((__VLS_ctx.form.amount)), inputmode: ("decimal"), placeholder: ("例如 100"), }, ...__VLS_functionalComponentArgsRest(__VLS_91));
        ({}({ ...{ 'onKeyup': {} }, modelValue: ((__VLS_ctx.form.amount)), inputmode: ("decimal"), placeholder: ("例如 100"), }));
        let __VLS_96;
        const __VLS_97 = {
            onKeyup: (__VLS_ctx.requestQuote)
        };
        // @ts-ignore
        [requestQuote, form,];
        const __VLS_95 = __VLS_pickFunctionalComponentCtx(__VLS_90, __VLS_92);
        let __VLS_93;
        let __VLS_94;
        (__VLS_89.slots).default;
        const __VLS_89 = __VLS_pickFunctionalComponentCtx(__VLS_84, __VLS_86);
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("form-actions") }, });
        const __VLS_98 = {}.ElButton;
        ({}.ElButton);
        ({}.ElButton);
        __VLS_components.ElButton;
        __VLS_components.elButton;
        __VLS_components.ElButton;
        __VLS_components.elButton;
        // @ts-ignore
        [ElButton, ElButton,];
        const __VLS_99 = __VLS_asFunctionalComponent(__VLS_98, new __VLS_98({ ...{ 'onClick': {} }, type: ("primary"), loading: ((__VLS_ctx.quoting)), }));
        const __VLS_100 = __VLS_99({ ...{ 'onClick': {} }, type: ("primary"), loading: ((__VLS_ctx.quoting)), }, ...__VLS_functionalComponentArgsRest(__VLS_99));
        ({}({ ...{ 'onClick': {} }, type: ("primary"), loading: ((__VLS_ctx.quoting)), }));
        let __VLS_104;
        const __VLS_105 = {
            onClick: (__VLS_ctx.requestQuote)
        };
        // @ts-ignore
        [requestQuote, quoting,];
        (__VLS_103.slots).default;
        const __VLS_103 = __VLS_pickFunctionalComponentCtx(__VLS_98, __VLS_100);
        let __VLS_101;
        let __VLS_102;
        const __VLS_106 = {}.ElButton;
        ({}.ElButton);
        ({}.ElButton);
        __VLS_components.ElButton;
        __VLS_components.elButton;
        __VLS_components.ElButton;
        __VLS_components.elButton;
        // @ts-ignore
        [ElButton, ElButton,];
        const __VLS_107 = __VLS_asFunctionalComponent(__VLS_106, new __VLS_106({ ...{ 'onClick': {} }, loading: ((__VLS_ctx.refreshing)), }));
        const __VLS_108 = __VLS_107({ ...{ 'onClick': {} }, loading: ((__VLS_ctx.refreshing)), }, ...__VLS_functionalComponentArgsRest(__VLS_107));
        ({}({ ...{ 'onClick': {} }, loading: ((__VLS_ctx.refreshing)), }));
        let __VLS_112;
        const __VLS_113 = {
            onClick: (__VLS_ctx.loadCurrencies)
        };
        // @ts-ignore
        [loadCurrencies, refreshing,];
        (__VLS_111.slots).default;
        const __VLS_111 = __VLS_pickFunctionalComponentCtx(__VLS_106, __VLS_108);
        let __VLS_109;
        let __VLS_110;
        (__VLS_37.slots).default;
        const __VLS_37 = __VLS_pickFunctionalComponentCtx(__VLS_32, __VLS_34);
        let __VLS_35;
        let __VLS_36;
        __VLS_elementAsFunction(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({ ...{ class: ("quote-panel") }, "aria-live": ("polite"), });
        if (__VLS_ctx.quote) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("quote-label") }, });
            // @ts-ignore
            [quote,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("quote-amount") }, });
            (__VLS_ctx.displayNumber(__VLS_ctx.quote.convertedAmount));
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (__VLS_ctx.quote.to);
            // @ts-ignore
            [quote, quote, displayNumber,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("quote-equation") }, });
            (__VLS_ctx.displayNumber(__VLS_ctx.quote.amount));
            (__VLS_ctx.quote.from);
            (__VLS_ctx.displayNumber(__VLS_ctx.quote.convertedAmount));
            (__VLS_ctx.quote.to);
            // @ts-ignore
            [quote, quote, quote, quote, displayNumber, displayNumber,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
            (__VLS_ctx.quote.from);
            (__VLS_ctx.displayNumber(__VLS_ctx.quote.rate));
            (__VLS_ctx.quote.to);
            // @ts-ignore
            [quote, quote, quote, displayNumber,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
            (__VLS_ctx.quote.asOf);
            // @ts-ignore
            [quote,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
            __VLS_elementAsFunction(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
            (__VLS_ctx.quote.source);
            // @ts-ignore
            [quote,];
        }
        else {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("quote-label") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("quote-placeholder") }, });
        }
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['exchange-page'];
        __VLS_styleScopedClasses['exchange-header'];
        __VLS_styleScopedClasses['exchange-content'];
        __VLS_styleScopedClasses['exchange-intro'];
        __VLS_styleScopedClasses['exchange-intro__copy'];
        __VLS_styleScopedClasses['eyebrow'];
        __VLS_styleScopedClasses['market-status'];
        __VLS_styleScopedClasses['page-alert'];
        __VLS_styleScopedClasses['page-alert'];
        __VLS_styleScopedClasses['skeleton'];
        __VLS_styleScopedClasses['page-alert'];
        __VLS_styleScopedClasses['exchange-layout'];
        __VLS_styleScopedClasses['exchange-form'];
        __VLS_styleScopedClasses['currency-grid'];
        __VLS_styleScopedClasses['swap-button'];
        __VLS_styleScopedClasses['form-actions'];
        __VLS_styleScopedClasses['quote-panel'];
        __VLS_styleScopedClasses['quote-label'];
        __VLS_styleScopedClasses['quote-amount'];
        __VLS_styleScopedClasses['quote-equation'];
        __VLS_styleScopedClasses['quote-label'];
        __VLS_styleScopedClasses['quote-placeholder'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                ElAlert: ElAlert,
                ElButton: ElButton,
                ElForm: ElForm,
                ElFormItem: ElFormItem,
                ElInput: ElInput,
                ElOption: ElOption,
                ElSelect: ElSelect,
                ElSkeleton: ElSkeleton,
                currencies: currencies,
                market: market,
                quote: quote,
                form: form,
                loaded: loaded,
                loadError: loadError,
                refreshing: refreshing,
                refreshError: refreshError,
                quoting: quoting,
                loadCurrencies: loadCurrencies,
                requestQuote: requestQuote,
                swapCurrencies: swapCurrencies,
                displayNumber: displayNumber,
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
