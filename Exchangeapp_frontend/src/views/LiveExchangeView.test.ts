// @vitest-environment jsdom

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { ElMessage } from 'element-plus';
import LiveExchangeView from './LiveExchangeView.vue';
import { useExchangeSessionStore } from '../store/exchangeSession';

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

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const setScrollY = (value: number) => {
  Object.defineProperty(window, 'scrollY', { configurable: true, value });
};

const mountExchange = () => mount(LiveExchangeView, {
  global: {
    stubs: {
      ElAlert: {
        props: ['title'],
        template: '<div class="el-alert"><span v-if="title">{{ title }}</span><slot /></div>',
      },
      ElButton: {
        emits: ['click'],
        template: '<button type="button" @click="$emit(\'click\', $event)"><slot /></button>',
      },
      ElForm: {
        inheritAttrs: false,
        template: '<form @submit="$emit(\'submit\', $event)"><slot /></form>',
      },
      ElFormItem: {
        props: ['label'],
        template: '<label><span class="form-item-label">{{ label }}</span><slot /></label>',
      },
      ElInput: {
        props: ['modelValue', 'placeholder'],
        emits: ['update:modelValue', 'keyup'],
        template: '<input :value="modelValue" :placeholder="placeholder" @input="$emit(\'update:modelValue\', $event.target.value)" />',
      },
      ElOption: { template: '<option><slot /></option>' },
      ElSelect: {
        props: ['modelValue', 'placeholder'],
        emits: ['update:modelValue'],
        template: '<span><span v-if="placeholder">{{ placeholder }}</span><select :value="modelValue"><slot /></select></span>',
      },
      ElSkeleton: { template: '<div class="el-skeleton" />' },
    },
  },
});

const mountKeepAliveExchange = () => {
  const state = reactive({ showExchange: true });
  const Host = defineComponent({
    setup() {
      return () => h(KeepAlive, { max: 1 }, {
        default: () => (state.showExchange ? h(LiveExchangeView) : null),
      });
    },
  });
  return {
    state,
    wrapper: mount(Host, {
      global: {
        stubs: {
          ElAlert: {
            props: ['title'],
            template: '<div class="el-alert"><span v-if="title">{{ title }}</span><slot /></div>',
          },
          ElButton: {
            emits: ['click'],
            template: '<button type="button" @click="$emit(\'click\', $event)"><slot /></button>',
          },
          ElForm: {
            inheritAttrs: false,
            template: '<form @submit="$emit(\'submit\', $event)"><slot /></form>',
          },
          ElFormItem: {
            props: ['label'],
            template: '<label><span class="form-item-label">{{ label }}</span><slot /></label>',
          },
          ElInput: {
            props: ['modelValue', 'placeholder'],
            emits: ['update:modelValue', 'keyup'],
            template: '<input :value="modelValue" :placeholder="placeholder" @input="$emit(\'update:modelValue\', $event.target.value)" />',
          },
          ElOption: { template: '<option><slot /></option>' },
          ElSelect: {
            props: ['modelValue', 'placeholder'],
            emits: ['update:modelValue'],
            template: '<span><span v-if="placeholder">{{ placeholder }}</span><select :value="modelValue"><slot /></select></span>',
          },
          ElSkeleton: { template: '<div class="el-skeleton" />' },
        },
      },
    }),
  };
};

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
    expect(wrapper.text()).toContain('Currency conversion');
    expect(wrapper.get('input').element).toBeTruthy();

    await wrapper.get('form').trigger('submit');
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(mocks.get).toHaveBeenNthCalledWith(2, '/exchange/quote', {
      params: { from: 'CNY', to: 'USD', amount: '100' },
    });
    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('14.00 USD');
    expect(wrapper.text()).toContain('Conversion result');
    expect(wrapper.text()).toContain('Reference rate');
    expect(wrapper.text()).toContain('Market date');
    expect(wrapper.text()).toContain('Data source');
  });

  it('renders the active Exchange surface in English', async () => {
    wrapper = mountExchange();
    await flushPromises();

    const text = wrapper.text();
    expect(text).toContain('Currency conversion');
    expect(text).toContain('Convert currencies using reference rates with clear source and market-date information.');
    expect(text).toContain('Latest rates');
    expect(text).toContain('Market date 2026-08-25');
    expect(text).toContain('Currency');
    expect(text).toContain('Select currency');
    expect(text).toContain('Swap');
    expect(text).toContain('Amount');
    expect(wrapper.get('input').attributes('placeholder')).toBe('e.g. 100');
    expect(text).toContain('Get quote');
    expect(text).toContain('Refresh rates');
    expect(text).toContain('Quote result');
    expect(text).not.toMatch(/[\u4E00-\u9FFF]/);
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

  it('validates missing currencies with English copy', async () => {
    wrapper = mountExchange();
    await flushPromises();
    useExchangeSessionStore().form.fromCurrency = '';

    await wrapper.get('form').trigger('submit');

    expect(ElMessage.error).toHaveBeenCalledWith('Select both currencies.');
    expect(mocks.get).toHaveBeenCalledTimes(1);
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
    expect(ElMessage.error).toHaveBeenCalledWith('Enter an amount greater than zero.');
  });

  it('uses English stale-market copy and warning', async () => {
    mocks.get
      .mockReset()
      .mockResolvedValueOnce({ data: { ...currencies, freshness: 'stale' } })
      .mockResolvedValueOnce({ data: { ...quote, freshness: 'stale' } });
    wrapper = mountExchange();
    await flushPromises();

    expect(wrapper.text()).toContain('Cached rates');
    expect(wrapper.text()).toContain('Live market data is temporarily unavailable. Quotes are using the latest cached rates.');

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(ElMessage.warning).toHaveBeenCalledWith('This quote is using the latest cached rates.');
  });

  it('uses the frontend quote fallback instead of raw API error copy', async () => {
    mocks.get
      .mockReset()
      .mockResolvedValueOnce({ data: currencies })
      .mockRejectedValueOnce({
        isAxiosError: true,
        response: { data: { error: '任意后端文本' } },
      });
    wrapper = mountExchange();
    await flushPromises();

    await wrapper.get('form').trigger('submit');
    await flushPromises();

    expect(ElMessage.error).toHaveBeenCalledWith('Could not get a quote. Please try again.');
  });

  it('saves scroll on deactivation and restores it without reloading currencies', async () => {
    const mounted = mountKeepAliveExchange();
    wrapper = mounted.wrapper;
    await flushPromises();
    vi.mocked(window.scrollTo).mockClear();
    setScrollY(900);
    mounted.state.showExchange = false;
    await nextTick();
    setScrollY(300);

    mounted.state.showExchange = true;
    await flushPromises();
    await nextTick();

    expect(useExchangeSessionStore().scrollY).toBe(900);
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 900, behavior: 'auto' });
  });

  it('does not restore scroll when currencies finish loading while hidden', async () => {
    const pending = deferred<{ data: typeof currencies }>();
    mocks.get.mockReset().mockReturnValueOnce(pending.promise);
    const mounted = mountKeepAliveExchange();
    wrapper = mounted.wrapper;
    await nextTick();

    mounted.state.showExchange = false;
    await nextTick();
    setScrollY(300);
    vi.mocked(window.scrollTo).mockClear();
    pending.resolve({ data: currencies });
    await flushPromises();
    await nextTick();

    expect(window.scrollTo).not.toHaveBeenCalled();
  });

  it('preserves form and quote state across activation', async () => {
    const mounted = mountKeepAliveExchange();
    wrapper = mounted.wrapper;
    await flushPromises();
    const store = useExchangeSessionStore();
    store.form.amount = '123';
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    const cachedQuote = store.quote;

    mounted.state.showExchange = false;
    await nextTick();
    mounted.state.showExchange = true;
    await flushPromises();
    await nextTick();

    expect(store.form.fromCurrency).toBe('CNY');
    expect(store.form.toCurrency).toBe('USD');
    expect(store.form.amount).toBe('123');
    expect(store.quote).toBe(cachedQuote);
  });
});
