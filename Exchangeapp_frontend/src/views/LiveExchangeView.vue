<template>
  <section class="exchange-page" aria-labelledby="exchange-title">
    <header class="exchange-header">
      <h1 id="exchange-title">Exchange</h1>
    </header>

    <div class="exchange-content">
      <div class="exchange-intro">
        <div class="exchange-intro__copy">
          <p class="eyebrow">REFERENCE RATES</p>
          <h2>Currency conversion</h2>
          <p>Convert currencies using reference rates with clear source and market-date information.</p>
        </div>
        <div v-if="market" class="market-status" :class="{ stale: market.freshness === 'stale' }">
          <strong>{{ market.freshness === 'stale' ? 'Cached rates' : 'Latest rates' }}</strong>
          <span>{{ market.source }}</span>
          <small>Market date {{ market.asOf }}</small>
        </div>
      </div>

      <el-alert v-if="market?.freshness === 'stale'" class="page-alert" title="Live market data is temporarily unavailable. Quotes are using the latest cached rates." type="warning" :closable="false" show-icon />
      <el-alert v-if="refreshError" class="page-alert" :title="refreshError" type="warning" :closable="false" show-icon />
      <div v-if="!loaded && !loadError" class="skeleton"><el-skeleton animated :rows="5" /></div>
      <el-alert v-else-if="loadError" class="page-alert" :title="loadError" type="error" :closable="false" show-icon>
        <template #default><el-button type="primary" plain @click="loadCurrencies">Reload</el-button></template>
      </el-alert>

      <div v-else class="exchange-layout">
        <el-form class="exchange-form" label-position="top" @submit.prevent="requestQuote">
          <div class="currency-grid">
            <el-form-item label="Currency">
              <el-select v-model="form.fromCurrency" filterable placeholder="Select currency">
                <el-option v-for="currency in currencies" :key="'from-' + currency" :label="currency" :value="currency" />
              </el-select>
            </el-form-item>
            <el-button class="swap-button" plain :disabled="!form.fromCurrency || !form.toCurrency" @click="swapCurrencies">Swap</el-button>
            <el-form-item label="Currency">
              <el-select v-model="form.toCurrency" filterable placeholder="Select currency">
                <el-option v-for="currency in currencies" :key="'to-' + currency" :label="currency" :value="currency" />
              </el-select>
            </el-form-item>
          </div>
          <el-form-item label="Amount"><el-input v-model="form.amount" inputmode="decimal" placeholder="e.g. 100" @keyup.enter="requestQuote" /></el-form-item>
          <div class="form-actions">
            <el-button type="primary" :loading="quoting" @click="requestQuote">Get quote</el-button>
            <el-button :loading="refreshing" @click="loadCurrencies">Refresh rates</el-button>
          </div>
        </el-form>

        <aside class="quote-panel" aria-live="polite">
          <template v-if="quote">
            <p class="quote-label">Conversion result</p>
            <p class="quote-amount">{{ displayNumber(quote.convertedAmount) }} <span>{{ quote.to }}</span></p>
            <p class="quote-equation">{{ displayNumber(quote.amount) }} {{ quote.from }} = {{ displayNumber(quote.convertedAmount) }} {{ quote.to }}</p>
            <dl>
              <div><dt>Reference rate</dt><dd>1 {{ quote.from }} = {{ displayNumber(quote.rate) }} {{ quote.to }}</dd></div>
              <div><dt>Market date</dt><dd>{{ quote.asOf }}</dd></div>
              <div><dt>Data source</dt><dd>{{ quote.source }}</dd></div>
            </dl>
          </template>
          <template v-else><p class="quote-label">Quote result</p><p class="quote-placeholder">Select two currencies and enter an amount to get a quote.</p></template>
        </aside>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import {
  ElAlert,
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElOption,
  ElSelect,
  ElSkeleton,
} from 'element-plus';
import 'element-plus/dist/index.css';
import { useExchangeSessionStore } from '../store/exchangeSession';

const exchangeSession = useExchangeSessionStore();
const {
  currencies,
  market,
  quote,
  form,
  loaded,
  loadError,
  refreshing,
  refreshError,
  quoting,
  quoteError,
} = storeToRefs(exchangeSession);
let mounted = false;
let exchangeEntryVersion = 0;
let restoredEntryVersion = -1;

const restoreScrollOnce = async () => {
  const entryVersion = exchangeEntryVersion;
  if (!mounted || restoredEntryVersion === entryVersion || !loaded.value) return;
  await Promise.resolve();
  if (!mounted || entryVersion !== exchangeEntryVersion || restoredEntryVersion === entryVersion) return;
  if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
    window.scrollTo({ top: exchangeSession.scrollY, behavior: 'auto' });
  }
  restoredEntryVersion = entryVersion;
};

const loadCurrencies = () => { void exchangeSession.loadCurrencies({ force: true }); };

const requestQuote = async () => {
  if (!form.value.fromCurrency || !form.value.toCurrency) {
    ElMessage.error('Select both currencies.');
    return;
  }
  if (!/^\d+(\.\d+)?$/.test(form.value.amount) || Number(form.value.amount) <= 0) {
    ElMessage.error('Enter an amount greater than zero.');
    return;
  }
  const result = await exchangeSession.requestQuote();
  if (!mounted || !result.applied) return;
  if (!result.success) {
    ElMessage.error(quoteError.value || 'Could not get a quote. Please try again.');
  } else if (result.data?.freshness === 'stale') {
    ElMessage.warning('This quote is using the latest cached rates.');
  }
};

const swapCurrencies = async () => {
  const shouldRefreshQuote = exchangeSession.swapCurrencies();
  if (shouldRefreshQuote) {
    await requestQuote();
  }
};

const displayNumber = (value: string) => {
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
  if (typeof window !== 'undefined') exchangeSession.saveScroll(window.scrollY);
});
</script>

<style scoped>
.exchange-page {
  width: 100%;
  min-height: 100vh;
  margin: 0;
  padding: 0;
  background: var(--color-surface);
  color: var(--color-text);
}

.exchange-header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.exchange-header h1 {
  margin: 0;
  color: var(--color-text);
  font-size: 22px;
  line-height: 1.2;
  letter-spacing: -.02em;
}

.exchange-content {
  padding: var(--space-5);
}

.exchange-intro {
  display: flex;
  align-items: end;
  justify-content: space-between;
  gap: var(--space-5);
  margin-bottom: var(--space-5);
}

.exchange-intro__copy {
  min-width: 0;
}

.eyebrow,
.quote-label {
  margin: 0 0 var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 12px;
  font-weight: 800;
  letter-spacing: .12em;
}

.exchange-intro h2 {
  margin: 0;
  color: var(--color-text);
  font-size: clamp(24px, 3vw, 28px);
  line-height: 1.2;
  letter-spacing: -.03em;
}

.exchange-intro__copy > p:last-child {
  max-width: 620px;
  margin: var(--space-3) 0 0;
  color: var(--color-text-secondary);
  font-size: 15px;
  line-height: 1.6;
}

.market-status {
  display: grid;
  flex: 0 1 240px;
  min-width: 0;
  gap: 4px;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
}

.market-status strong {
  color: var(--color-text);
}

.market-status.stale {
  border-color: color-mix(in srgb, var(--color-danger) 38%, var(--color-border));
  background: color-mix(in srgb, var(--color-danger) 8%, var(--color-surface));
}

.market-status span,
.market-status small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.market-status small {
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.page-alert {
  margin-bottom: var(--space-4);
}

.skeleton,
.exchange-layout {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
}

.skeleton {
  padding: var(--space-5);
}

.exchange-layout {
  overflow: hidden;
}

.exchange-form {
  padding: var(--space-5);
}

.currency-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: end;
  gap: var(--space-3);
}

.swap-button {
  min-width: 64px;
  margin-bottom: 18px;
}

.form-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-4);
}

.quote-panel {
  min-height: auto;
  padding: var(--space-5);
  border-top: 1px solid var(--color-border);
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.quote-panel .quote-label {
  color: var(--color-text-tertiary);
}

.quote-amount {
  margin: 0;
  color: var(--color-text);
  font-size: clamp(30px, 4vw, 36px);
  font-weight: 800;
  letter-spacing: -.045em;
  line-height: 1.1;
}

.quote-amount span {
  color: var(--color-text-secondary);
  font-size: .42em;
  letter-spacing: 0;
}

.quote-equation,
.quote-placeholder {
  margin: var(--space-3) 0 0;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

dl {
  display: grid;
  gap: var(--space-3);
  margin: var(--space-5) 0 0;
}

dl div {
  display: grid;
  gap: 4px;
}

dt {
  color: var(--color-text-tertiary);
  font-size: 12px;
}

dd {
  margin: 0;
  color: var(--color-text);
  font-size: 14px;
  word-break: break-word;
}

:deep(.el-form-item__label) {
  color: var(--color-text-secondary);
  font-weight: 700;
}

:deep(.el-input__wrapper),
:deep(.el-select__wrapper) {
  min-height: 44px;
  border-radius: var(--radius-sm);
}

:deep(.el-button) {
  min-height: 42px;
  border-radius: var(--radius-sm);
  font-weight: 700;
}

:deep(.el-button:active) {
  transform: translateY(1px);
}

@media (max-width: 799px) {
  .exchange-header {
    top: var(--mobile-safe-top);
  }

  .exchange-content {
    padding: var(--space-4);
  }

  .exchange-intro {
    align-items: stretch;
    flex-direction: column;
    gap: var(--space-4);
  }

  .market-status {
    flex-basis: auto;
    width: 100%;
  }

  .currency-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }

  .swap-button {
    width: 100%;
    margin: -2px 0 18px;
  }
}
</style>
