<template>  
  <el-container>  
    <el-form :model="form" class="exchange-form">  
      <el-form-item label="从货币" label-width="80px">  
        <el-select v-model="form.fromCurrency" placeholder="选择货币">  
          <el-option v-for="currency in currencies" :key="currency" :label="currency" :value="currency" />  
        </el-select>  
      </el-form-item>  
      <el-form-item label="到货币" label-width="80px">  
        <el-select v-model="form.toCurrency" placeholder="选择货币">  
          <el-option v-for="currency in currencies" :key="currency" :label="currency" :value="currency" />  
        </el-select>  
      </el-form-item>  
      <el-form-item label="金额" label-width="80px">  
        <el-input v-model="form.amount" type="number" placeholder="输入金额" />  
      </el-form-item>  
      <el-form-item>  
        <el-button type="primary" @click="exchange">兑换</el-button>  
      </el-form-item>  
    </el-form>  
    <div v-if="result" class="result">  
      <p>兑换结果: {{ result }}</p>  
    </div>  
  </el-container>  
</template>  
  
  <script setup lang="ts">
  import { ref, onMounted } from 'vue';
  import { ElMessage } from 'element-plus';
  import axios from '../axios';
  
  interface ExchangeRate {
    fromCurrency: string;
    toCurrency: string;
    rate: number;
  }
  
  const form = ref({
    fromCurrency: '',
    toCurrency: '',
    amount: 0,
  });
  
  const result = ref<number | null>(null);
  const currencies = ref<string[]>([]);
  const rates = ref<ExchangeRate[]>([]);
  
  const fetchCurrencies = async () => {
    try{
      const response = await axios.get<ExchangeRate[]>('/exchangeRates');
      rates.value = response.data;
      currencies.value = [...new Set(response.data.map((rate: ExchangeRate) => [rate.fromCurrency, rate.toCurrency]).flat())];
    }catch(error){
      console.log('Failed to load currencies', error)
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

    const rate = rates.value.find(
      (rate) => rate.fromCurrency === form.value.fromCurrency && rate.toCurrency === form.value.toCurrency
    )?.rate;
  
    if (rate) {
      result.value = amount * rate;
    } else {
      result.value = null;
      ElMessage.error('暂未找到该货币兑换路径');
    }
  };
  
  onMounted(fetchCurrencies);
  </script>
  
  <style scoped>
  .exchange-form {  
  width: 100%;  
  max-width: 600px;  
  margin: 20px auto;  
  padding: 20px;  
  background-color: #f5f5f5;  
  border-radius: 4px;  
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);  
}  
  
.result {  
  margin-top: 20px;  
  padding: 20px;  
  background-color: #f0f0f0;  
  border-radius: 4px;  
  text-align: center;  
  font-size: 18px;  
}  
  </style>
  