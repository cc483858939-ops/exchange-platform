<template>
  <main class="profile-page">
    <div v-if="profileLoading" class="profile-state">正在加载作者主页…</div>
    <section v-else-if="notFound" class="profile-state">
      <h1>未找到这位作者</h1>
      <p>该用户不存在，或其公开资料已不可用。</p>
      <el-button text @click="goBack">返回</el-button>
    </section>
    <section v-else-if="errorMessage" class="profile-state">
      <h1>作者主页暂时无法加载</h1>
      <p>{{ errorMessage }}</p>
      <el-button type="primary" @click="loadProfile">重试</el-button>
    </section>
    <section v-else-if="user" class="profile-shell">
      <button class="back-link" type="button" @click="goBack">← 返回</button>

      <header class="profile-header">
        <span class="profile-avatar" aria-hidden="true">{{ profileInitial }}</span>
        <div>
          <p class="profile-label">AUTHOR / PROFILE</p>
          <h1>{{ user.username || '?' }}</h1>
          <p class="profile-handle">@{{ user.username || '?' }}</p>
          <p class="profile-joined">加入于 {{ joinedAt }}</p>
        </div>
      </header>

      <section class="profile-articles" aria-labelledby="author-articles-title">
        <div class="section-heading">
          <div>
            <p class="profile-label">WRITING DESK</p>
            <h2 id="author-articles-title">发布的文章</h2>
          </div>
          <span>{{ articles.length }} 篇</span>
        </div>

        <div v-if="articlesLoading && articles.length === 0" class="article-state">正在加载文章…</div>
        <div v-else-if="articleError" class="article-state">{{ articleError }}</div>
        <div v-else-if="articles.length === 0" class="article-state">这位作者还没有发布可阅读的文章。</div>
        <div v-else class="author-article-list">
          <RouterLink
            v-for="article in articles"
            :key="article.ID"
            class="author-article"
            :to="{ name: 'NewsDetail', params: { id: String(article.ID) } }"
          >
            <p class="article-date">{{ formatRelativeTime(article.CreatedAt) }}</p>
            <div>
              <h3>{{ article.title }}</h3>
              <p>{{ article.preview }}</p>
            </div>
            <span aria-hidden="true">↗</span>
          </RouterLink>
        </div>

        <div v-if="articlesLoading && articles.length > 0" class="article-state">正在加载更多文章…</div>
        <button v-else-if="hasMore" class="load-more" type="button" @click="loadMore">加载更多</button>
      </section>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import axios from '../axios';
import type { Article } from '../types/Article';
import type { PublicUser } from '../types/User';
import { formatRelativeTime } from '../utils/time';

const pageSize = 20;
const route = useRoute();
const router = useRouter();
const user = ref<PublicUser | null>(null);
const articles = ref<Article[]>([]);
const profileLoading = ref(false);
const articlesLoading = ref(false);
const notFound = ref(false);
const errorMessage = ref('');
const articleError = ref('');
const hasMore = ref(false);
let requestVersion = 0;

const userID = computed(() => String(route.params.id ?? ''));
const profileInitial = computed(() => Array.from(user.value?.username.trim() ?? '')[0]?.toUpperCase() || '?');
const joinedAt = computed(() => {
  const value = user.value?.created_at;
  if (!value || Number.isNaN(new Date(value).getTime())) {
    return '';
  }
  return new Date(value).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' });
});

const goBack = () => router.back();

const loadMore = async () => {
  const version = requestVersion;
  if (!user.value || articlesLoading.value || !hasMore.value && articles.value.length > 0) {
    return;
  }

  const offset = articles.value.length;
  articlesLoading.value = true;
  articleError.value = '';
  try {
    const response = await axios.get<Article[]>(`/users/${userID.value}/articles`, {
      params: { limit: pageSize, offset },
    });
    if (version !== requestVersion) {
      return;
    }
    articles.value = offset === 0 ? response.data : [...articles.value, ...response.data];
    hasMore.value = response.data.length === pageSize;
  } catch (error) {
    if (version !== requestVersion) {
      return;
    }
    articleError.value = (error as { response?: { status?: number } }).response?.status === 404
      ? '该用户不存在，或其公开资料已不可用。'
      : '文章加载失败，请稍后重试。';
  } finally {
    if (version === requestVersion) {
      articlesLoading.value = false;
    }
  }
};

const loadProfile = async () => {
  const version = ++requestVersion;
  user.value = null;
  articles.value = [];
  profileLoading.value = true;
  articlesLoading.value = false;
  notFound.value = false;
  errorMessage.value = '';
  articleError.value = '';
  hasMore.value = false;

  try {
    const response = await axios.get<PublicUser>(`/users/${userID.value}`);
    if (version !== requestVersion) {
      return;
    }
    user.value = response.data;
    profileLoading.value = false;
    await loadMore();
  } catch (error) {
    if (version !== requestVersion) {
      return;
    }
    notFound.value = (error as { response?: { status?: number } }).response?.status === 404;
    errorMessage.value = notFound.value ? '' : '请检查网络后重试。';
  } finally {
    if (version === requestVersion) {
      profileLoading.value = false;
    }
  }
};

watch(
  () => route.params.id,
  () => {
    void loadProfile();
  },
  { immediate: true },
);
</script>

<style scoped>
.profile-page {
  min-height: calc(100vh - 80px);
  padding: clamp(28px, 5vw, 76px) clamp(18px, 6vw, 100px);
  background:
    linear-gradient(115deg, rgba(219, 234, 254, 0.58), transparent 34rem),
    #f8fafc;
  color: #0f172a;
}

.profile-shell,
.profile-state {
  max-width: 980px;
  margin: 0 auto;
}

.profile-state {
  padding: 72px 0;
  text-align: center;
  color: #475569;
}

.profile-state h1 {
  margin: 0;
  color: #0f172a;
  font-size: clamp(30px, 5vw, 48px);
  letter-spacing: -0.045em;
}

.profile-state p {
  margin: 14px auto 22px;
}

.back-link {
  margin: 0 0 28px;
  border: 0;
  padding: 0;
  background: transparent;
  color: #475569;
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.back-link:hover,
.back-link:focus-visible {
  color: #1d4ed8;
}

.profile-header {
  display: flex;
  align-items: center;
  gap: clamp(20px, 4vw, 40px);
  padding: clamp(26px, 5vw, 52px);
  border: 1px solid rgba(15, 23, 42, 0.1);
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.86);
  box-shadow: 0 26px 70px rgba(15, 23, 42, 0.08);
}

.profile-avatar {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: clamp(76px, 13vw, 122px);
  height: clamp(76px, 13vw, 122px);
  border: 1px solid rgba(37, 99, 235, 0.16);
  border-radius: 50%;
  background: linear-gradient(135deg, #bfdbfe, #eff6ff);
  color: #1d4ed8;
  font-size: clamp(30px, 5vw, 48px);
  font-weight: 900;
}

.profile-label {
  margin: 0;
  color: #64748b;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.14em;
}

.profile-header h1,
.section-heading h2 {
  margin: 7px 0 0;
  color: #0f172a;
  letter-spacing: -0.05em;
}

.profile-header h1 {
  font-size: clamp(34px, 6vw, 62px);
  line-height: 0.98;
}

.profile-handle,
.profile-joined {
  margin: 8px 0 0;
  color: #64748b;
}

.profile-joined {
  font-size: 13px;
}

.profile-articles {
  margin-top: clamp(44px, 7vw, 82px);
}

.section-heading {
  display: flex;
  align-items: end;
  justify-content: space-between;
  gap: 18px;
  margin-bottom: 18px;
}

.section-heading h2 {
  font-size: clamp(28px, 4vw, 42px);
}

.section-heading > span {
  color: #64748b;
  font-size: 13px;
  font-weight: 700;
}

.author-article-list {
  border-top: 1px solid rgba(15, 23, 42, 0.12);
}

.author-article {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr) auto;
  gap: 18px;
  align-items: start;
  padding: 24px 4px;
  border-bottom: 1px solid rgba(15, 23, 42, 0.12);
  color: inherit;
  text-decoration: none;
  transition: padding 170ms ease, color 170ms ease;
}

.author-article:hover,
.author-article:focus-visible {
  padding-right: 12px;
  color: #1d4ed8;
}

.author-article:focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 4px;
}

.author-article h3 {
  margin: 0;
  color: #0f172a;
  font-size: 20px;
  letter-spacing: -0.025em;
}

.author-article p {
  margin: 8px 0 0;
  color: #64748b;
  line-height: 1.6;
}

.author-article .article-date {
  margin: 3px 0 0;
  color: #64748b;
  font-size: 12px;
  font-weight: 800;
}

.author-article > span {
  color: #1d4ed8;
  font-size: 20px;
}

.article-state {
  padding: 34px 0;
  color: #64748b;
}

.load-more {
  display: block;
  margin: 30px auto 0;
  border: 1px solid rgba(29, 78, 216, 0.24);
  border-radius: 999px;
  padding: 11px 18px;
  background: #ffffff;
  color: #1d4ed8;
  cursor: pointer;
  font: inherit;
  font-weight: 800;
}

.load-more:hover,
.load-more:focus-visible {
  border-color: #1d4ed8;
  box-shadow: 0 12px 30px rgba(29, 78, 216, 0.15);
}

@media (max-width: 620px) {
  .profile-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .author-article {
    grid-template-columns: 1fr auto;
  }

  .author-article .article-date {
    grid-column: 1 / -1;
  }
}

</style>
