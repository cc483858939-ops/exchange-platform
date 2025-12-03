<template>
  <el-container>
    <el-main>
      <el-card v-if="article" class="article-detail">
        <h1>{{ article.Title }}</h1>
        
        <!-- 【新增】这里开始：显示过期时间 -->
        <div v-if="article.expired_at" class="expire-info">
           ⏰ 本文将于: {{ formatDate(article.expired_at) }} 过期
        </div>
        <!-- 【新增】结束 -->

        <p class="content">{{ article.Content }}</p>
        
        <div class="actions">
          <el-button type="primary" @click="likeArticle">点赞</el-button>
          <span class="likes-count">点赞数: {{ likes }}</span>
        </div>
      </el-card>
      <div v-else class="no-data">您必须登录/注册才可以阅读文章，或者文章不存在</div>
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useRoute } from "vue-router";
import axios from "../axios";
import type { Article, Like } from "../types/Article";

const article = ref<Article | null>(null);
const route = useRoute();
const likes = ref<number>(0)

const { id } = route.params;

// 【新增】格式化时间的函数
const formatDate = (dateStr: string) => {
  if (!dateStr) return '';
  // 转换为本地易读的时间格式
  return new Date(dateStr).toLocaleString();
};

const fetchArticle = async () => {
  try {
    const response = await axios.get<Article>(`/articles/${id}`);
    article.value = response.data;
  } catch (error) {
    console.error("Failed to load article:", error);
  }
};

const likeArticle = async () => {
  try {
    const res = await axios.post<Like>(`articles/${id}/like`)
    likes.value = res.data.likes
    await fetchLike()
  } catch (error) {
    console.log('Error Liking article:', error)
  }
};

const fetchLike = async ()=>{
  try{
    const res = await axios.get<Like>(`articles/${id}/like`)
    likes.value = res.data.likes
  }catch(error){
    console.log('Error fetching likes:', error)
  }
}

onMounted(fetchArticle);
onMounted(fetchLike)
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
