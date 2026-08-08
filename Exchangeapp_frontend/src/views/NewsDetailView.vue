<template>
  <el-container>
    <el-main class="detail-main">
      <div v-if="loading" class="no-data">文章加载中...</div>
      <el-card v-else-if="article" class="article-detail">
        <h1>{{ article.title }}</h1>

        <div v-if="article.expired_at" class="expire-info">
          本文将于 {{ formatDate(article.expired_at) }} 过期
        </div>

        <p ref="contentRef" class="content">{{ article.content }}</p>

        <div class="actions">
          <el-button :type="liked ? 'primary' : 'default'" :disabled="likeSubmitting" @click="likeArticle">{{ liked ? '取消点赞' : '点赞' }}</el-button>
          <span class="likes-count">点赞数: {{ likes }}</span>
        </div>

        <figure v-if="article.cover_image_url" class="detail-cover">
          <img :src="article.cover_image_url" :alt="article.title" loading="lazy" />
        </figure>
      </el-card>
      <div v-else class="no-data">{{ detailMessage }}</div>
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { onBeforeRouteLeave, useRoute } from 'vue-router';
import { ElMessage } from 'element-plus';
import axios from '../axios';
import { consumePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { useAuthStore } from '../store/auth';
import type { Article, Like } from '../types/Article';
import type { RecommendationTracking } from '../types/Recommendation';

const article = ref<Article | null>(null);
const route = useRoute();
const articleID = computed(() => String(route.params.id ?? ''));
const likes = ref(0); const liked = ref(false); const likeSubmitting = ref(false);
const loading = ref(false); const loadError = ref(false); const contentRef = ref<HTMLElement | null>(null);
const authStore = useAuthStore();
const telemetry = getRecommendationTelemetry(() => authStore.token);
let tracking: RecommendationTracking | null = null;
let activeID = ''; let readEndSent = false; let foregroundStartedAt: number | null = null;
let foregroundTimeMS = 0; let maxScrollDepth = 0;

const detailMessage = computed(() => !authStore.isAuthenticated ? '登录后可以阅读文章' : loadError.value ? '文章加载失败，请稍后重试' : '文章不存在或已下架');
const formatDate = (dateStr: string) => dateStr ? new Date(dateStr).toLocaleString() : '';
const pauseForeground = () => { if (foregroundStartedAt !== null) { foregroundTimeMS += Date.now() - foregroundStartedAt; foregroundStartedAt = null; } };
const updateDepth = () => { const el = contentRef.value; if (!el) return; const rect = el.getBoundingClientRect(); const height = Math.max(rect.height, 1); const viewed = Math.min(height, Math.max(0, window.innerHeight - Math.max(rect.top, 0))); maxScrollDepth = Math.max(maxScrollDepth, Math.min(100, Math.round(viewed / height * 100))); };
const finishRead = (exitType: string) => { if (!tracking || readEndSent || !activeID) return; pauseForeground(); readEndSent = true; telemetry.recordReadEnd(Number(activeID), tracking, { foreground_time_ms: Math.max(0, Math.round(foregroundTimeMS)), max_scroll_depth: maxScrollDepth, exit_type: exitType }); };
const handleVisibility = () => { if (document.visibilityState === 'hidden') { finishRead('page_hide'); void telemetry.flush(true); } else if (tracking && !readEndSent) foregroundStartedAt = Date.now(); };
const startRead = () => { tracking = consumePendingRecommendationAttribution(Number(articleID.value)); activeID = articleID.value; readEndSent = false; foregroundTimeMS = 0; maxScrollDepth = 0; if (tracking && document.visibilityState === 'visible') foregroundStartedAt = Date.now(); updateDepth(); };

const fetchArticle = async () => { if (!authStore.isAuthenticated) { article.value = null; loadError.value = false; return; } loading.value = true; loadError.value = false; try { const response = await axios.get<Article>(`/articles/${articleID.value}`); article.value = response.data; startRead(); } catch (error) { article.value = null; loadError.value = (error as { response?: { status?: number } }).response?.status !== 404; } finally { loading.value = false; } };
const likeArticle = async () => { if (likeSubmitting.value) return; likeSubmitting.value = true; const previousLiked = liked.value; const previousLikes = likes.value; liked.value = !previousLiked; likes.value = Math.max(0, previousLikes + (liked.value ? 1 : -1)); try { const res = previousLiked ? await axios.delete<Like>(`articles/${articleID.value}/like`) : await axios.put<Like>(`articles/${articleID.value}/like`); likes.value = res.data.likes; liked.value = res.data.liked; } catch { liked.value = previousLiked; likes.value = previousLikes; ElMessage.error('点赞失败，请稍后重试'); } finally { likeSubmitting.value = false; } };
const fetchLike = async () => { if (!authStore.isAuthenticated) return; try { const res = await axios.get<Like>(`articles/${articleID.value}/like`); likes.value = res.data.likes; liked.value = res.data.liked; } catch { /* optional */ } };
watch(articleID, () => { finishRead('navigate_to_article'); void fetchArticle(); void fetchLike(); });
onBeforeRouteLeave((to) => { finishRead(to.name === 'Recommendations' ? 'back_to_recommendation' : 'route_leave'); });
onMounted(() => { document.addEventListener('visibilitychange', handleVisibility); window.addEventListener('scroll', updateDepth, { passive: true }); window.addEventListener('pagehide', () => finishRead('page_hide')); void fetchArticle(); void fetchLike(); });
onBeforeUnmount(() => { finishRead('route_leave'); document.removeEventListener('visibilitychange', handleVisibility); window.removeEventListener('scroll', updateDepth); });
</script>

<style scoped>
.detail-main {
  padding: 32px clamp(18px, 4vw, 48px);
}

.article-detail {
  max-width: 880px;
  margin: 0 auto;
  border-radius: 8px;
}

.article-detail h1 {
  margin: 0 0 18px;
  font-size: 34px;
  line-height: 1.2;
  color: #111827;
}

.content {
  white-space: pre-wrap;
  line-height: 1.8;
  margin-bottom: 20px;
  color: #1f2937;
}

.expire-info {
  display: inline-block;
  margin-bottom: 18px;
  padding: 8px 10px;
  border-radius: 4px;
  background-color: #fdf6ec;
  color: #b45309;
  font-size: 14px;
}

.actions {
  display: flex;
  align-items: center;
  gap: 15px;
  margin-top: 20px;
}

.likes-count {
  font-size: 14px;
  color: #666;
}

.detail-cover {
  margin: 28px 0 0;
  overflow: hidden;
  border-radius: 8px;
  background: #e2e8f0;
}

.detail-cover img {
  display: block;
  width: 100%;
  max-height: 520px;
  object-fit: cover;
}

.no-data {
  margin-top: 50px;
  text-align: center;
  font-size: 18px;
  color: #64748b;
}
</style>
