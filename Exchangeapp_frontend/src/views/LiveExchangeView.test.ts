// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { ElMessage } from 'element-plus';
import LiveExchangeView from './LiveExchangeView.vue';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    get: mocks.get,
  },
}));

const currencies = {
  currencies: ['CNY', 'USD', 'EUR'],
  asOf: '2026-08-25',
  source: 'test-market',
  freshness: 'fresh' as const,
};

const quote = {
  from: 'CNY',
  to: 'USD',
  amount: '100',
  rate: '0.14',
  convertedAmount: '14.00',
  asOf: '2026-08-25',
  source: 'test-market',
  freshness: 'fresh' as const,
};

const swappedQuote = {
  ...quote,
  from: 'USD',
  to: 'CNY',
  rate: '7.12',
  convertedAmount: '712.00',
};

const mountExchange = () => mount(LiveExchangeView, {
  global: {
    stubs: {
      ElAlert: { template: '<div class="el-alert"><slot /></div>' },
      ElButton: {
        emits: ['click'],
        template: '<button type="button" @click="$emit(\'click\', $event)"><slot /></button>',
      },
      ElForm: {
        inheritAttrs: false,
        template: '<form @submit="$emit(\'submit\', $event)"><slot /></form>',
      },
      ElFormItem: { template: '<label><slot /></label>' },
      ElInput: {
        props: ['modelValue'],
        emits: ['update:modelValue', 'keyup'],
        template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
      },
      ElOption: { template: '<option><slot /></option>' },
      ElSelect: {
        props: ['modelValue'],
        emits: ['update:modelValue'],
        template: '<select :value="modelValue"><slot /></select>',
      },
      ElSkeleton: { template: '<div class="el-skeleton" />' },
    },
  },
});

describe('LiveExchangeView', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: vi.fn() }));
    vi.spyOn(ElMessage, 'warning').mockImplementation(() => ({ close: vi.fn() }));
    mocks.get
      .mockResolvedValueOnce({ data: currencies })
      .mockResolvedValueOnce({ data: quote });
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.restoreAllMocks();
  });

  it('loads currencies and requests a quote through the existing exchange flow', async () => {
    wrapper = mountExchange();
    await flushPromises();

    expect(mocks.get).toHaveBeenNthCalledWith(1, '/exchange/currencies');
    expect(wrapper.text()).toContain('汇率换算');
    expect(wrapper.get('input').element).toBeTruthy();

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(mocks.get).toHaveBeenNthCalledWith(2, '/exchange/quote', {
      params: { from: 'CNY', to: 'USD', amount: '100' },
    });
    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('14.00 USD');
  });

  it('reuses the cached currencies and quote when the route is re-entered', async () => {
    wrapper = mountExchange();
    await flushPromises();
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    const firstWrapper = wrapper;
    firstWrapper.unmount();
    vi.mocked(window.scrollTo).mockClear();

    wrapper = mountExchange();
    await flushPromises();

    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('14.00 USD');
    expect(window.scrollTo).toHaveBeenCalledTimes(1);
  });

  it('validates a swap in the View before requesting the swapped quote', async () => {
    mocks.get.mockResolvedValueOnce({ data: swappedQuote });
    wrapper = mountExchange();
    await flushPromises();
    await wrapper.get('form').trigger('submit');
    await flushPromises();

    await wrapper.find('.swap-button').trigger('click');
    await flushPromises();

    expect(mocks.get).toHaveBeenNthCalledWith(3, '/exchange/quote', {
      params: { from: 'USD', to: 'CNY', amount: '100' },
    });
    expect(wrapper.text()).toContain('712.00 CNY');
    expect(ElMessage.error).not.toHaveBeenCalled();
  });

  it.each(['abc', '0'])('does not request a quote when swapping with invalid amount %s', async (amount) => {
    wrapper = mountExchange();
    await flushPromises();
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    await wrapper.get('input').setValue(amount);
    mocks.get.mockClear();

    await wrapper.find('.swap-button').trigger('click');
    await flushPromises();

    expect(mocks.get).not.toHaveBeenCalled();
    expect(ElMessage.error).toHaveBeenCalledWith('请输入大于零的金额');
  });
});
