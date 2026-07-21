<template>
  <section class="exchange-page" aria-labelledby="exchange-title">
    <header class="page-header">
      <div>
        <p class="eyebrow">REFERENCE RATES</p>
        <h1 id="exchange-title">汇率换算</h1>
        <p>由后端统一报价，清楚展示数据来源与行情日期。</p>
      </div>
      <div v-if="market" class="market-status" :class="{ stale: market.freshness === 'stale' }">
        <strong>{{ market.freshness === 'stale' ? '缓存行情' : '最新行情' }}</strong>
        <span>{{ market.source }}</span>
        <small>行情日期 {{ market.asOf }}</small>
      </div>
    </header>

    <el-alert v-if="market?.freshness === 'stale'" class="page-alert" title="上游行情暂时不可用，当前报价使用最近缓存。" type="warning" :closable="false" show-icon />
    <div v-if="loading" class="skeleton"><el-skeleton animated :rows="5" /></div>
    <el-alert v-else-if="loadError" class="page-alert" :title="loadError" type="error" :closable="false" show-icon>
      <template #default><el-button type="primary" plain @click="loadCurrencies">重新加载</el-button></template>
    </el-alert>

    <div v-else class="exchange-layout">
      <el-form class="exchange-form" label-position="top" @submit.prevent="requestQuote">
        <div class="currency-grid">
          <el-form-item label="币种">
            <el-select v-model="form.fromCurrency" filterable placeholder="选择货币">
              <el-option v-for="currency in currencies" :key="`from-${currency}`" :label="currency" :value="currency" />
            </el-select>
          </el-form-item>
          <el-button class="swap-button" plain :disabled="!form.fromCurrency || !form.toCurrency" @click="swapCurrencies">交换</el-button>
          <el-form-item label="币种">
            <el-select v-model="form.toCurrency" filterable placeholder="选择货币">
              <el-option v-for="currency in currencies" :key="`to-${currency}`" :label="currency" :value="currency" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item label="金额"><el-input v-model="form.amount" inputmode="decimal" placeholder="例如 100" @keyup.enter="requestQuote" /></el-form-item>
        <div class="form-actions">
          <el-button type="primary" :loading="quoting" @click="requestQuote">获取报价</el-button>
          <el-button :loading="loading" @click="loadCurrencies">刷新行情</el-button>
        </div>
      </el-form>

      <aside class="quote-panel" aria-live="polite">
        <template v-if="quote">
          <p class="quote-label">兑换结果</p>
          <p class="quote-amount">{{ displayNumber(quote.convertedAmount) }} <span>{{ quote.to }}</span></p>
          <p class="quote-equation">{{ displayNumber(quote.amount) }} {{ quote.from }} = {{ displayNumber(quote.convertedAmount) }} {{ quote.to }}</p>
          <dl>
            <div><dt>参考汇率</dt><dd>1 {{ quote.from }} = {{ displayNumber(quote.rate) }} {{ quote.to }}</dd></div>
            <div><dt>行情日期</dt><dd>{{ quote.asOf }}</dd></div>
            <div><dt>数据来源</dt><dd>{{ quote.source }}</dd></div>
          </dl>
        </template>
        <template v-else><p class="quote-label">报价结果</p><p class="quote-placeholder">选择两种货币并输入金额后获取报价。</p></template>
      </aside>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue';
import { ElMessage } from 'element-plus';
import { isAxiosError } from 'axios';
import axios from '../axios';

interface MarketMeta { asOf: string; source: string; freshness: 'fresh' | 'stale'; }
interface CurrencyResponse extends MarketMeta { currencies: string[]; }
interface QuoteResponse extends MarketMeta { from: string; to: string; amount: string; rate: string; convertedAmount: string; }

const form = reactive({ fromCurrency: 'CNY', toCurrency: 'USD', amount: '100' });
const currencies = ref<string[]>([]);
const market = ref<MarketMeta | null>(null);
const quote = ref<QuoteResponse | null>(null);
const loading = ref(false);
const quoting = ref(false);
const loadError = ref('');

const readError = (error: unknown, fallback: string) => {
  if (isAxiosError(error)) {
    const message = error.response?.data?.error;
    if (typeof message === 'string' && message.trim()) return message;
  }
  return error instanceof Error && error.message ? error.message : fallback;
};

const loadCurrencies = async () => {
  loading.value = true;
  loadError.value = '';
  try {
    const { data } = await axios.get<CurrencyResponse>('/exchange/currencies');
    if (!data.currencies?.length) throw new Error('当前行情没有可用货币');
    currencies.value = data.currencies;
    market.value = { asOf: data.asOf, source: data.source, freshness: data.freshness };
    if (!currencies.value.includes(form.fromCurrency)) form.fromCurrency = currencies.value[0];
    if (!currencies.value.includes(form.toCurrency)) form.toCurrency = currencies.value.find(code => code !== form.fromCurrency) ?? currencies.value[0];
  } catch (error) {
    currencies.value = [];
    market.value = null;
    quote.value = null;
    loadError.value = readError(error, '汇率数据加载失败，请稍后重试。');
  } finally {
    loading.value = false;
  }
};

const requestQuote = async () => {
  if (!form.fromCurrency || !form.toCurrency) return ElMessage.error('请选择要兑换的两种货币');
  if (!/^\d+(\.\d+)?$/.test(form.amount) || Number(form.amount) <= 0) return ElMessage.error('请输入大于零的金额');
  quoting.value = true;
  try {
    const { data } = await axios.get<QuoteResponse>('/exchange/quote', { params: { from: form.fromCurrency, to: form.toCurrency, amount: form.amount } });
    quote.value = data;
    market.value = { asOf: data.asOf, source: data.source, freshness: data.freshness };
    if (data.freshness === 'stale') ElMessage.warning('当前结果使用最近缓存行情');
  } catch (error) {
    quote.value = null;
    ElMessage.error(readError(error, '暂时无法获取报价，请稍后重试。'));
  } finally {
    quoting.value = false;
  }
};

const swapCurrencies = () => {
  [form.fromCurrency, form.toCurrency] = [form.toCurrency, form.fromCurrency];
  if (quote.value) void requestQuote();
};

const displayNumber = (value: string) => {
  const [whole, fraction] = value.split('.');
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return fraction ? `${grouped}.${fraction}` : grouped;
};

onMounted(loadCurrencies);
</script>

<style scoped>
.exchange-page { width: min(100% - 36px, 1040px); margin: 0 auto; padding: clamp(34px, 6vw, 78px) 0 72px; color: #172033; }
.page-header { display: flex; align-items: end; justify-content: space-between; gap: 28px; margin-bottom: 30px; }
.eyebrow, .quote-label { margin: 0 0 10px; color: #49637f; font-size: 12px; font-weight: 800; letter-spacing: .12em; }
h1 { margin: 0; color: #172033; font-size: clamp(32px, 5vw, 48px); line-height: 1.1; letter-spacing: -.045em; }
.page-header p:not(.eyebrow) { margin: 12px 0 0; color: #607089; font-size: 16px; line-height: 1.7; }
.market-status { display: grid; min-width: 186px; gap: 4px; padding: 14px 16px; border: 1px solid #c7d9d4; border-radius: 14px; background: #f5fbf9; color: #245747; }
.market-status.stale { border-color: #f1d6a1; background: #fffbf2; color: #875915; }
.market-status span, .market-status small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.market-status small { font-size: 12px; }
.page-alert { margin-bottom: 22px; }
.skeleton, .exchange-layout { border: 1px solid #dbe4ef; border-radius: 20px; background: rgba(255, 255, 255, .9); box-shadow: 0 22px 60px rgba(30, 52, 80, .08); }
.skeleton { padding: 32px; }
.exchange-layout { display: grid; grid-template-columns: minmax(0, 1.1fr) minmax(290px, .9fr); overflow: hidden; }
.exchange-form { padding: clamp(24px, 4vw, 42px); }
.currency-grid { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); align-items: end; gap: 14px; }
.swap-button { min-width: 64px; margin-bottom: 18px; }
.form-actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 28px; }
.quote-panel { display: flex; min-height: 330px; flex-direction: column; justify-content: center; padding: clamp(26px, 4vw, 44px); background: #1d2d44; color: #f7fbff; }
.quote-panel .quote-label { color: #9db4ce; }
.quote-amount { margin: 0; font-size: clamp(34px, 5vw, 54px); font-weight: 800; letter-spacing: -.055em; line-height: 1.05; }
.quote-amount span { color: #bdd5ee; font-size: .42em; letter-spacing: 0; }
.quote-equation, .quote-placeholder { margin: 16px 0 0; color: #c4d2e1; line-height: 1.65; }
dl { display: grid; gap: 14px; margin: 34px 0 0; }
dl div { display: grid; gap: 4px; }
dt { color: #9db4ce; font-size: 12px; }
dd { margin: 0; font-size: 14px; word-break: break-word; }
:deep(.el-form-item__label) { color: #42546f; font-weight: 700; }
:deep(.el-input__wrapper), :deep(.el-select__wrapper) { min-height: 44px; border-radius: 10px; }
:deep(.el-button) { min-height: 42px; border-radius: 10px; font-weight: 700; }
:deep(.el-button:active) { transform: translateY(1px); }
@media (max-width: 760px) {
  .exchange-page { width: min(100% - 28px, 1040px); padding-top: 30px; }
  .page-header, .exchange-layout { display: grid; grid-template-columns: 1fr; }
  .market-status { width: 100%; }
  .currency-grid { grid-template-columns: 1fr; gap: 0; }
  .swap-button { width: 100%; margin: -2px 0 18px; }
  .quote-panel { min-height: 280px; }
}
</style>
