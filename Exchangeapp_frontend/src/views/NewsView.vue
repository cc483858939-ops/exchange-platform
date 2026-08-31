<template>
  <el-container>
    <el-main class="news-main">
      <header class="news-toolbar">
        <div>
          <h1>新闻</h1>
          <p>浏览最新文章</p>
        </div>
        <el-button v-if="authStore.isAuthenticated" type="primary" @click="publishArticle">发布文章</el-button>
      </header>

      <div v-if="loading" class="no-data">文章加载中...</div>
      <div v-else-if="posts && posts.length" class="article-list">
        <el-card v-for="recommendation in posts" :key="recommendation.post.id" class="article-card" shadow="hover">
          <div class="article-card-content" :class="{ 'without-cover': !recommendation.post.article?.cover_image_url }">
            <div class="article-copy">
              <AuthorIdentity :author="recommendation.post.author" :created-at="recommendation.post.published_at" />
              <h2>{{ recommendation.post.article?.title || 'Post' }}</h2>
              <p>{{ recommendation.post.article?.preview || recommendation.post.content }}</p>
              <el-button text @click="viewDetail(recommendation.post.id)">阅读更多</el-button>
            </div>
            <figure v-if="recommendation.post.article?.cover_image_url" class="article-cover">
              <img :src="recommendation.post.article.cover_image_url" :alt="recommendation.post.article.title" loading="lazy" />
            </figure>
          </div>
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
import { getPostRecommendations } from '../services/recommendationService';
import { useAuthStore } from '../store/auth';
import type { RecommendedPost } from '../types/Recommendation';
import AuthorIdentity from '../components/AuthorIdentity.vue';

const posts = ref<RecommendedPost[]>([]);
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
    posts.value = [];
    loadError.value = false;
    return;
  }

  loading.value = true;
  loadError.value = false;

  try {
    posts.value = await getPostRecommendations(50);
  } catch (error) {
    console.error('Failed to load articles:', error);
    loadError.value = true;
  } finally {
    loading.value = false;
  }
};

const viewDetail = (id: number) => {
  if (!authStore.isAuthenticated) {
    ElMessage.error('请先登录后再阅读文章');
    return;
  }
  router.push({ name: 'PostDetail', params: { id } });
};

const publishArticle = () => {
  router.push({ name: 'ArticleCreate' });
};

onMounted(fetchArticles);
</script>

<style scoped>
.news-main {
  container-type: inline-size;
  padding: 32px clamp(18px, 4vw, 48px);
}

.news-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  max-width: 1320px;
  margin: 0 auto 18px;
}

.news-toolbar h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.2;
}

.news-toolbar p {
  margin: 6px 0 0;
  color: #64748b;
}

.article-list {
  display: grid;
  gap: 20px;
  max-width: 1320px;
  margin: 0 auto;
}

.article-card {
  border-radius: 8px;
}

.article-card-content {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(220px, 30%);
  align-items: stretch;
  gap: 28px;
  min-height: 180px;
}

.article-card-content.without-cover {
  grid-template-columns: minmax(0, 1fr);
}

.article-copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  justify-content: center;
  gap: 14px;
}

.article-copy h2 {
  margin: 0;
  font-size: 26px;
  line-height: 1.25;
  color: #1f2937;
}

.article-copy p {
  margin: 0;
  color: #334155;
  line-height: 1.7;
}

.article-cover {
  margin: 0;
  overflow: hidden;
  border-radius: 8px;
  background: #e2e8f0;
}

.article-cover img {
  display: block;
  width: 100%;
  height: 100%;
  min-height: 180px;
  object-fit: cover;
}

.no-data {
  margin-top: 64px;
  text-align: center;
  font-size: 18px;
  color: #64748b;
}

@media (max-width: 760px) {
  .news-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .article-card-content {
    grid-template-columns: 1fr;
  }

  .article-cover img {
    aspect-ratio: 16 / 9;
    min-height: 0;
  }
}
@container (max-width: 700px) {
  .news-main {
    padding: 24px 18px;
  }

  .news-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .article-card-content {
    grid-template-columns: 1fr;
    gap: 18px;
  }

  .article-cover img {
    aspect-ratio: 16 / 9;
    min-height: 0;
  }
}
</style>
