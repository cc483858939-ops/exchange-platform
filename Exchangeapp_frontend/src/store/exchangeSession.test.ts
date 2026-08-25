import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: { get: mocks.get },
}));

import { useExchangeSessionStore } from './exchangeSession';

const currencies = {
  currencies: ['CNY', 'USD', 'EUR'],
  asOf: '2026-08-25',
  source: 'test-market',
  freshness: 'fresh' as const,
};

const quote = (amount: string, from = 'CNY', to = 'USD') => ({
  from,
  to,
  amount,
  rate: '0.14',
  convertedAmount: amount === '100' ? '14.00' : '28.00',
  asOf: '2026-08-25',
  source: 'test-market',
  freshness: 'fresh' as const,
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

describe('exchange session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('loads currencies once and uses refreshing for a cached force reload', async () => {
    mocks.get.mockResolvedValueOnce({ data: currencies });
    const store = useExchangeSessionStore();
    await store.loadCurrencies();
    await store.loadCurrencies();
    expect(mocks.get).toHaveBeenCalledTimes(1);

    const refresh = deferred<never>();
    mocks.get.mockReturnValueOnce(refresh.promise);
    const request = store.loadCurrencies({ force: true });
    expect(store.refreshing).toBe(true);
    expect(store.loading).toBe(false);
    expect(store.currencies).toEqual(currencies.currencies);
    refresh.reject(new Error('market offline'));
    await request;

    expect(store.currencies).toEqual(currencies.currencies);
    expect(store.market?.source).toBe('test-market');
    expect(store.refreshError).toContain('market offline');
    expect(store.loaded).toBe(true);
    expect(store.refreshing).toBe(false);
  });

  it('applies only the latest currency response', async () => {
    const first = deferred<{ data: typeof currencies }>();
    const second = deferred<{ data: typeof currencies & { currencies: string[] } }>();
    mocks.get.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const store = useExchangeSessionStore();
    const firstRequest = store.loadCurrencies();
    const secondRequest = store.loadCurrencies({ force: true });
    second.resolve({ data: { ...currencies, currencies: ['JPY', 'USD'] } });
    await secondRequest;
    first.resolve({ data: currencies });
    await firstRequest;

    expect(store.currencies).toEqual(['JPY', 'USD']);
    expect(store.form.fromCurrency).toBe('JPY');
  });

  it('keeps quote cache and quoting lineage when a newer form request starts', async () => {
    const first = deferred<{ data: ReturnType<typeof quote> }>();
    const second = deferred<{ data: ReturnType<typeof quote> }>();
    mocks.get.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const store = useExchangeSessionStore();
    const firstRequest = store.requestQuote();
    store.form.amount = '200';
    const secondRequest = store.requestQuote();

    first.resolve({ data: quote('100') });
    await Promise.resolve();
    expect(store.quoting).toBe(true);
    expect(store.quote).toBeNull();

    second.resolve({ data: quote('200') });
    await Promise.all([firstRequest, secondRequest]);
    expect(store.quote?.amount).toBe('200');
    expect(store.quoting).toBe(false);
  });

  it('ignores a response after the form changes and clears the quote on the latest failure', async () => {
    const pending = deferred<{ data: ReturnType<typeof quote> }>();
    mocks.get.mockReturnValueOnce(pending.promise);
    const store = useExchangeSessionStore();
    const request = store.requestQuote();
    store.form.amount = '250';
    pending.resolve({ data: quote('100') });
    const result = await request;
    expect(result.applied).toBe(false);
    expect(store.quote).toBeNull();

    mocks.get.mockRejectedValueOnce(new Error('quote offline'));
    const failed = await store.requestQuote();
    expect(failed.applied).toBe(true);
    expect(failed.success).toBe(false);
    expect(store.quote).toBeNull();
    expect(store.quoteError).toContain('quote offline');
  });

  it('swaps currencies and requests a version-safe quote', async () => {
    const store = useExchangeSessionStore();
    store.quote = quote('100');
    mocks.get.mockResolvedValueOnce({ data: quote('100', 'USD', 'CNY') });
    const result = await store.swapCurrencies();

    expect(store.form.fromCurrency).toBe('USD');
    expect(store.form.toCurrency).toBe('CNY');
    expect(mocks.get).toHaveBeenCalledWith('/exchange/quote', {
      params: { from: 'USD', to: 'CNY', amount: '100' },
    });
    expect(result.applied).toBe(true);
    expect(store.quote?.from).toBe('USD');
  });
});
