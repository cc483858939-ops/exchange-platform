<template>
  <el-container>
    <el-main>
      <div v-if="loading" class="no-data">文章加载中...</div>
      <div v-else-if="articles && articles.length">
        <el-card v-for="article in articles" :key="article.ID" class="article-card">
          <h2>{{ article.Title }}</h2>
          <p>{{ article.Preview }}</p>
          <el-button text @click="viewDetail(article.ID)">阅读更多</el-button>
        </el-card>
      </div>
      <div v-else class="no-data">{{ emptyMessage }}</div>
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import axios from '../axios';
import { useAuthStore } from '../store/auth';
import type { Article } from "../types/Article";

const articles = ref<Article[]>([]);
const loading = ref(false);
const loadError = ref(false);
const router = useRouter();
const authStore = useAuthStore();

const emptyMessage = computed(() => {
  if (!authStore.isAuthenticated) {
    return '登录后可以查看文章内容';
  }
  if (loadError.value) {
    return '文章加载失败，请稍后重试';
  }
  return '暂无可阅读文章';
});

const fetchArticles = async () => {
  if (!authStore.isAuthenticated) {
    articles.value = [];
    loadError.value = false;
    return;
  }

  loading.value = true;
  loadError.value = false;

  try {
    const response = await axios.get<Article[]>('/articles');
    articles.value = response.data;
  } catch (error) {
    console.error('Failed to load articles:', error);
    loadError.value = true;
  } finally {
    loading.value = false;
  }
};

const viewDetail = (id: string) => {
  if (!authStore.isAuthenticated) {
    ElMessage.error('请先登录后再阅读文章');
    return;
  }
  router.push({ name: 'NewsDetail', params: { id } });
};

onMounted(fetchArticles);
</script>

<style scoped>
.article-card {
  margin: 20px 0;
}

.no-data {
  text-align: center;
  font-size: 1.2em;
  color: #999;
}
</style>
