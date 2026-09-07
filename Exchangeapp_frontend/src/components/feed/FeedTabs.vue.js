import { nextTick } from 'vue';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const __VLS_props = defineProps();
const emit = defineEmits();
const tabs = [
    { value: 'for-you', label: 'For You' },
    { value: 'following', label: 'Following' },
];
const selectTab = (tab) => {
    emit('select', tab);
};
const handleKeydown = (event, index) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
        return;
    }
    event.preventDefault();
    const nextIndex = event.key === 'Home'
        ? 0
        : event.key === 'End'
            ? tabs.length - 1
            : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    const nextTab = tabs[nextIndex].value;
    emit('select', nextTab);
    void nextTick(() => {
        document.querySelector('[data-feed-tab="' + nextTab + '"]')?.focus();
    });
};
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
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("feed-tabs") }, role: ("tablist"), "aria-label": ("Home feed"), });
    for (const [tab, index] of __VLS_getVForSourceType((__VLS_ctx.tabs))) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                    __VLS_ctx.selectTab(tab.value);
                    // @ts-ignore
                    [tabs, selectTab,];
                } }, ...{ onKeydown: (...[$event]) => {
                    __VLS_ctx.handleKeydown($event, index);
                    // @ts-ignore
                    [handleKeydown,];
                } }, key: ((tab.value)), id: (('feed-tab-' + tab.value)), ...{ class: ("feed-tab") }, ...{ class: (({ 'feed-tab--active': __VLS_ctx.activeTab === tab.value })) }, type: ("button"), role: ("tab"), "aria-selected": ((__VLS_ctx.activeTab === tab.value)), "aria-controls": (('feed-panel-' + tab.value)), tabindex: ((__VLS_ctx.activeTab === tab.value ? 0 : -1)), "data-feed-tab": ((tab.value)), });
        __VLS_styleScopedClasses = ({ 'feed-tab--active': activeTab === tab.value });
        (tab.label);
        // @ts-ignore
        [activeTab, activeTab, activeTab,];
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['feed-tabs'];
        __VLS_styleScopedClasses['feed-tab'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                tabs: tabs,
                selectTab: selectTab,
                handleKeydown: handleKeydown,
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
