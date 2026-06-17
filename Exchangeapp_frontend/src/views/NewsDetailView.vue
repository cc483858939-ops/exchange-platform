<template>
  <el-container>
    <el-main>
      <div v-if="loading" class="no-data">文章加载中...</div>
      <el-card v-else-if="article" class="article-detail">
        <h1>{{ article.title }}</h1>
        
        <!-- 【新增】这里开始：显示过期时间 -->
        <div v-if="article.expired_at" class="expire-info">
           ⏰ 本文将于: {{ formatDate(article.expired_at) }} 过期
        </div>
        <!-- 【新增】结束 -->

        <p class="content">{{ article.content }}</p>
        
        <div class="actions">
          <el-button :type="liked ? 'primary' : 'default'" :disabled="likeSubmitting" @click="likeArticle">{{ liked ? '取消点赞' : '点赞' }}</el-button>
          <span class="likes-count">点赞数: {{ likes }}</span>
        </div>
      </el-card>
      <div v-else class="no-data">{{ detailMessage }}</div>
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from "vue";
import { useRoute } from "vue-router";
import { ElMessage } from 'element-plus';
import axios from "../axios";
import { useAuthStore } from "../store/auth";
import type { Article, Like } from "../types/Article";

const article = ref<Article | null>(null);
const route = useRoute();
const likes = ref<number>(0)
const liked = ref(false);
const likeSubmitting = ref(false);
const loading = ref(false);
const loadError = ref(false);
const authStore = useAuthStore();

const { id } = route.params;

const detailMessage = computed(() => {
  if (!authStore.isAuthenticated) {
    return '登录后可以阅读文章';
  }
  if (loadError.value) {
    return '文章加载失败，请稍后重试';
  }
  return '文章不存在或已下架';
});

// 【新增】格式化时间的函数
const formatDate = (dateStr: string) => {
  if (!dateStr) return '';
  // 转换为本地易读的时间格式
  return new Date(dateStr).toLocaleString();
};

const fetchArticle = async () => {
  if (!authStore.isAuthenticated) {
    article.value = null;
    loadError.value = false;
    return;
  }

  loading.value = true;
  loadError.value = false;

  try {
    const response = await axios.get<Article>(`/articles/${id}`);
    article.value = response.data;
  } catch (error) {
    console.error("Failed to load article:", error);
    const status = (error as { response?: { status?: number } }).response?.status;
    loadError.value = status !== 404;
  } finally {
    loading.value = false;
  }
};

const likeArticle = async () => {
  if (likeSubmitting.value) {
    return;
  }

  likeSubmitting.value = true;
  const previousLiked = liked.value;
  const previousLikes = likes.value;
  const nextLiked = !previousLiked;

  liked.value = nextLiked;
  likes.value = Math.max(0, previousLikes + (nextLiked ? 1 : -1));

  try {
    const res = previousLiked
      ? await axios.delete<Like>(`articles/${id}/like`)
      : await axios.put<Like>(`articles/${id}/like`)
    likes.value = res.data.likes
    liked.value = res.data.liked
  } catch (error) {
    liked.value = previousLiked;
    likes.value = previousLikes;
    console.log('Error Liking article:', error)
    ElMessage.error('点赞失败，请稍后重试');
  } finally {
    likeSubmitting.value = false;
  }
};

const fetchLike = async ()=>{
  if (!authStore.isAuthenticated) {
    return;
  }

  try{
    const res = await axios.get<Like>(`articles/${id}/like`)
    likes.value = res.data.likes
    liked.value = res.data.liked
  }catch(error){
    console.log('Error fetching likes:', error)
  }
}

onMounted(() => {
  fetchArticle();
  fetchLike();
});
</script>

<style scoped>
.article-detail {
  margin: 20px auto;
  max-width: 800px; /* 限制一下最大宽度更好看 */
}

.content {
  line-height: 1.6;
  margin-bottom: 20px;
}

/* 【新增】过期时间的样式 */
.expire-info {
  font-size: 14px;
  color: #e6a23c; /* 橙色警告色 */
  background-color: #fdf6ec;
  padding: 8px 10px;
  border-radius: 4px;
  margin-bottom: 15px;
  display: inline-block;
}

.actions {
  margin-top: 20px;
  display: flex;
  align-items: center;
  gap: 15px;
}

.likes-count {
  font-size: 14px;
  color: #666;
}

.no-data {
  text-align: center;
  font-size: 1.2em;
  color: #999;
  margin-top: 50px;
}
</style>
