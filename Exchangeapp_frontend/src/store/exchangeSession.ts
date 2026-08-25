import { reactive, ref } from 'vue';
import { defineStore } from 'pinia';
import { isAxiosError } from 'axios';
import axios from '../axios';

export type ExchangeMarketMeta = {
  asOf: string;
  source: string;
  freshness: 'fresh' | 'stale';
};

export type CurrencyResponse = ExchangeMarketMeta & {
  currencies: string[];
};

export type QuoteResponse = ExchangeMarketMeta & {
  from: string;
  to: string;
  amount: string;
  rate: string;
  convertedAmount: string;
};

export type ExchangeForm = {
  fromCurrency: string;
  toCurrency: string;
  amount: string;
};

export type QuoteRequestResult = {
  applied: boolean;
  success: boolean;
  data?: QuoteResponse;
  error?: unknown;
};

const readError = (error: unknown, fallback: string) => {
  if (isAxiosError(error)) {
    const message = error.response?.data?.error;
    if (typeof message === 'string' && message.trim()) {
      return message;
    }
  }
  return error instanceof Error && error.message ? error.message : fallback;
};

const sameQuoteForm = (left: ExchangeForm, right: ExchangeForm) => (
  left.fromCurrency === right.fromCurrency
  && left.toCurrency === right.toCurrency
  && left.amount === right.amount
);

export const useExchangeSessionStore = defineStore('exchangeSession', () => {
  const currencies = ref<string[]>([]);
  const market = ref<ExchangeMarketMeta | null>(null);
  const quote = ref<QuoteResponse | null>(null);
  const form = reactive<ExchangeForm>({
    fromCurrency: 'CNY',
    toCurrency: 'USD',
    amount: '100',
  });
  const loaded = ref(false);
  const loading = ref(false);
  const loadError = ref('');
  const refreshing = ref(false);
  const refreshError = ref('');
  const quoting = ref(false);
  const quoteError = ref('');
  const scrollY = ref(0);
  const currencyRequestVersion = ref(0);
  const quoteRequestVersion = ref(0);

  const updateFormCurrencies = (available: string[]) => {
    if (!available.includes(form.fromCurrency)) {
      form.fromCurrency = available[0] ?? '';
    }
    if (!available.includes(form.toCurrency)) {
      form.toCurrency = available.find(code => code !== form.fromCurrency) ?? available[0] ?? '';
    }
  };

  const loadCurrencies = async (options: { force?: boolean } = {}) => {
    const force = options.force === true;
    if ((loaded.value && !force) || (!force && (loading.value || refreshing.value))) {
      return;
    }

    const requestVersion = ++currencyRequestVersion.value;
    const hasCache = loaded.value && currencies.value.length > 0 && market.value !== null;
    if (force && hasCache) {
      refreshing.value = true;
      refreshError.value = '';
    } else {
      loading.value = true;
      loadError.value = '';
    }

    try {
      const { data } = await axios.get<CurrencyResponse>('/exchange/currencies');
      if (!data.currencies?.length) {
        throw new Error('当前行情没有可用货币');
      }
      if (requestVersion !== currencyRequestVersion.value) {
        return;
      }
      currencies.value = [...data.currencies];
      market.value = {
        asOf: data.asOf,
        source: data.source,
        freshness: data.freshness,
      };
      loaded.value = true;
      loadError.value = '';
      refreshError.value = '';
      updateFormCurrencies(currencies.value);
    } catch (error) {
      if (requestVersion !== currencyRequestVersion.value) {
        return;
      }
      const message = readError(error, '汇率数据加载失败，请稍后重试。');
      if (hasCache) {
        refreshError.value = message;
      } else {
        currencies.value = [];
        market.value = null;
        quote.value = null;
        loaded.value = false;
        loadError.value = message;
      }
    } finally {
      if (requestVersion === currencyRequestVersion.value) {
        loading.value = false;
        refreshing.value = false;
      }
    }
  };

  const requestQuote = async (): Promise<QuoteRequestResult> => {
    const snapshot: ExchangeForm = { ...form };
    const requestVersion = ++quoteRequestVersion.value;
    quoting.value = true;
    quoteError.value = '';

    try {
      const { data } = await axios.get<QuoteResponse>('/exchange/quote', {
        params: {
          from: snapshot.fromCurrency,
          to: snapshot.toCurrency,
          amount: snapshot.amount,
        },
      });
      const current = requestVersion === quoteRequestVersion.value && sameQuoteForm(form, snapshot);
      if (!current) {
        return { applied: false, success: true, data };
      }
      quote.value = data;
      market.value = {
        asOf: data.asOf,
        source: data.source,
        freshness: data.freshness,
      };
      return { applied: true, success: true, data };
    } catch (error) {
      const current = requestVersion === quoteRequestVersion.value && sameQuoteForm(form, snapshot);
      if (!current) {
        return { applied: false, success: false, error };
      }
      quote.value = null;
      const message = readError(error, '暂时无法获取报价，请稍后重试。');
      quoteError.value = message;
      return { applied: true, success: false, error: message };
    } finally {
      if (requestVersion === quoteRequestVersion.value) {
        quoting.value = false;
      }
    }
  };

  const swapCurrencies = () => {
    const shouldRefreshQuote = quote.value !== null;
    [form.fromCurrency, form.toCurrency] = [form.toCurrency, form.fromCurrency];
    return shouldRefreshQuote;
  };

  const saveScroll = (value: number) => {
    if (Number.isFinite(value) && value >= 0) {
      scrollY.value = value;
    }
  };

  return {
    currencies,
    market,
    quote,
    form,
    loaded,
    loading,
    loadError,
    refreshing,
    refreshError,
    quoting,
    quoteError,
    scrollY,
    currencyRequestVersion,
    quoteRequestVersion,
    loadCurrencies,
    requestQuote,
    swapCurrencies,
    saveScroll,
  };
});
