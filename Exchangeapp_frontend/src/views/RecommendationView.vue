<template>
  <main class="recommendation-page">
    <section ref="heroRef" class="recommendation-hero">
      <div class="hero-copy">
        <p class="hero-kicker">AI 推荐流</p>
        <h1>把你真正关心的文章排到前面</h1>
        <p class="hero-text">
          推荐页会结合你的浏览、点赞和文章标签，优先展示更接近当前兴趣的内容。
        </p>
        <div class="hero-actions">
          <button class="primary-action" type="button" :disabled="!authStore.isAuthenticated" @click="fetchRecommendations">
            刷新推荐
          </button>
          <button class="secondary-action" type="button" @click="goNews">浏览全部新闻</button>
        </div>
      </div>

      <div class="hero-visual" aria-hidden="true">
        <div class="score-orbit">
          <span>{{ topScoreLabel }}</span>
          <small>最高分</small>
        </div>
        <div class="topic-stack">
          <span v-for="topic in heroTopics" :key="topic">{{ topic }}</span>
        </div>
      </div>
    </section>

    <section v-if="!authStore.isAuthenticated" class="state-panel auth-panel">
      <div>
        <h2>登录后查看你的个性化推荐</h2>
        <p>推荐系统需要读取你的浏览和点赞记录，登录后会按分数从高到低展示文章。</p>
      </div>
      <button class="primary-action" type="button" @click="goLogin">去登录</button>
    </section>

    <section v-else class="recommendation-body">
      <div class="body-heading">
        <h2>为你排序</h2>
        <p>分数越高，说明文章的分类、标签、热度和新鲜度越贴近你的近期习惯。</p>
      </div>

      <div v-if="loading" class="masonry-grid" aria-label="推荐加载中">
        <article v-for="item in skeletons" :key="item" class="skeleton-card">
          <span class="skeleton-image"></span>
          <span class="skeleton-line wide"></span>
          <span class="skeleton-line"></span>
          <span class="skeleton-line short"></span>
        </article>
      </div>

      <div v-else-if="errorMessage" class="state-panel">
        <div>
          <h2>推荐暂时不可用</h2>
          <p>{{ errorMessage }}</p>
        </div>
        <button class="primary-action" type="button" @click="fetchRecommendations">重试</button>
      </div>

      <div v-else-if="articles.length === 0" class="state-panel">
        <div>
          <h2>还没有足够的推荐信号</h2>
          <p>先阅读或点赞几篇文章，系统会开始学习你的兴趣。</p>
        </div>
        <button class="secondary-action" type="button" @click="goNews">去看新闻</button>
      </div>

      <div v-else ref="cardsRef" class="masonry-grid">
        <article
          v-for="(article, index) in articles"
          :key="article.id"
          :ref="element => bindRecommendationCard(element, article)"
          class="recommendation-card"
          :class="{ 'tall-card': index % 5 === 0, 'compact-card': index % 4 === 2 }"
          @click="openArticle(article)"
        >
          <div class="image-wrap">
            <img v-if="article.cover_image_url" :src="article.cover_image_url" :alt="article.title" loading="lazy" />
            <div v-else class="cover-placeholder" aria-hidden="true"></div>
            <span class="score-chip">{{ formatScore(article.score) }}</span>
          </div>

          <div class="card-content">
            <AuthorIdentity :author="article.author" :created-at="article.created_at" />
            <div class="card-meta">
              <span>{{ article.category || '未分类' }}</span>
              <span>{{ formatRelativeTime(article.created_at) }}</span>
            </div>
            <h3>{{ article.title }}</h3>
            <p>{{ article.summary || article.preview }}</p>
            <div class="tag-row">
              <span v-for="tag in article.tags.slice(0, 3)" :key="tag">{{ tag }}</span>
            </div>
            <div class="card-footer">
              <span>{{ article.like_count }} 点赞</span>
              <button type="button" @click.stop="markNotInterested(article)">不感兴趣</button>`n              <button type="button" @click.stop="openArticle(article)">阅读</button>
            </div>
          </div>
        </article>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { ComponentPublicInstance } from 'vue';
import { useRouter } from 'vue-router';
import gsap from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';
import axios from '../axios';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { useAuthStore } from '../store/auth';
import type { RecommendedArticle } from '../types/Recommendation';
import AuthorIdentity from '../components/AuthorIdentity.vue';
import { formatRelativeTime } from '../utils/time';

gsap.registerPlugin(ScrollTrigger);

const router = useRouter();
const authStore = useAuthStore();
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);
const articles = ref<RecommendedArticle[]>([]);
const loading = ref(false);
const errorMessage = ref('');
const heroRef = ref<HTMLElement | null>(null);
const cardsRef = ref<HTMLElement | null>(null);
const skeletons = [1, 2, 3, 4, 5, 6];

let heroContext: ReturnType<typeof gsap.context> | null = null;
let cardsContext: ReturnType<typeof gsap.context> | null = null;

const heroTopics = computed(() => {
  const topics = articles.value
    .flatMap((article) => [article.category, ...article.tags])
    .filter(Boolean)
    .slice(0, 6);
  return topics.length ? topics : ['趋势', '深度', '汇率', '宏观', '科技'];
});

const topScoreLabel = computed(() => {
  if (!articles.value.length) {
    return '0.00';
  }
  return formatScore(Math.max(...articles.value.map((article) => article.score)));
});


const normalizeRecommendationTags = (value: unknown): string[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((tag): tag is string => typeof tag === 'string');
};

const fetchRecommendations = async () => {
  if (!authStore.isAuthenticated) {
    return;
  }

  loading.value = true;
  errorMessage.value = '';
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);

  try {
    const response = await axios.get<RecommendedArticle[]>('/recommendations/articles?limit=50');
    articles.value = response.data.map((article) => ({
      ...article,
      tags: normalizeRecommendationTags(article.tags),
    }));
    await nextTick();
    animateCards();
  } catch (error) {
    console.error('Failed to load recommendations:', error);
    errorMessage.value = '推荐加载失败，请稍后重试；如果仍然失败，请重新登录。';
  } finally {
    loading.value = false;
  }
};

const animateHero = () => {
  heroContext?.revert();
  const root = heroRef.value;
  if (!root) {
    return;
  }

  heroContext = gsap.context(() => {
    gsap.from('.hero-copy > *', {
      y: 28,
      opacity: 0,
      duration: 0.8,
      stagger: 0.09,
      ease: 'power3.out',
    });
    gsap.from('.hero-visual', {
      scale: 0.92,
      opacity: 0,
      duration: 1,
      ease: 'power3.out',
    });
  }, root);
};

const animateCards = () => {
  cardsContext?.revert();
  const root = cardsRef.value;
  if (!root || !articles.value.length) {
    return;
  }

  cardsContext = gsap.context(() => {
    const cards = gsap.utils.toArray<HTMLElement>('.recommendation-card');
    gsap.from(cards, {
      y: 48,
      opacity: 0,
      duration: 0.72,
      stagger: 0.055,
      ease: 'power3.out',
      scrollTrigger: {
        trigger: root,
        start: 'top 82%',
      },
    });

    cards.forEach((card) => {
      gsap.fromTo(
        card,
        { scale: 0.96, filter: 'brightness(0.94)' },
        {
          scale: 1,
          filter: 'brightness(1)',
          ease: 'none',
          scrollTrigger: {
            trigger: card,
            start: 'top 96%',
            end: 'bottom 24%',
            scrub: true,
          },
        },
      );
    });
  }, root);
};


const formatScore = (score: number) => score.toFixed(2);


const bindRecommendationCard = (
  element: Element | ComponentPublicInstance | null,
  article: RecommendedArticle,
) => {
  if (element instanceof HTMLElement) {
    recommendationTelemetry.observeCard(element, article.id, article.tracking);
  }
};

const markNotInterested = (article: RecommendedArticle) => {
  recommendationTelemetry.recordNotInterested(article.id, article.tracking);
  articles.value = articles.value.filter((item) => item.id !== article.id);
};

const openArticle = (article: RecommendedArticle) => {
  savePendingRecommendationAttribution(article.id, article.tracking);
  recommendationTelemetry.recordClick(article.id, article.tracking);
  router.push({ name: 'NewsDetail', params: { id: String(article.id) } });
};

const goLogin = () => {
  router.push({ name: 'Login' });
};

const goNews = () => {
  router.push({ name: 'News' });
};

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated) => {
    if (isAuthenticated) {
      fetchRecommendations();
    } else {
      recommendationTelemetry.clearSession();
      articles.value = [];
    }
  },
);

onMounted(() => {
  animateHero();
  fetchRecommendations();
});

onBeforeUnmount(() => {
  heroContext?.revert();
  cardsContext?.revert();
});
</script>

<style scoped>
.recommendation-page {
  container-type: inline-size;
  width: 100%;
  max-width: 100%;
  overflow-x: hidden;
  padding: clamp(28px, 4vw, 56px);
  color: #0f172a;
}

.recommendation-hero {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(320px, 0.9fr);
  align-items: stretch;
  gap: clamp(24px, 5vw, 72px);
  max-width: 1360px;
  min-height: min(720px, calc(100dvh - 128px));
  margin: 0 auto;
  padding: clamp(36px, 6vw, 82px);
  border: 1px solid rgba(15, 23, 42, 0.08);
  border-radius: 28px;
  background:
    radial-gradient(circle at 76% 30%, rgba(96, 165, 250, 0.28), transparent 28rem),
    radial-gradient(circle at 18% 78%, rgba(37, 99, 235, 0.18), transparent 22rem),
    linear-gradient(135deg, rgba(15, 23, 42, 0.98), rgba(20, 47, 77, 0.94));
  box-shadow: 0 40px 110px rgba(15, 23, 42, 0.22);
  color: #ffffff;
}

.hero-copy {
  display: flex;
  flex-direction: column;
  justify-content: center;
  max-width: 780px;
}

.hero-kicker {
  width: fit-content;
  margin: 0 0 22px;
  padding: 7px 12px;
  border: 1px solid rgba(255, 255, 255, 0.24);
  border-radius: 999px;
  color: #dbeafe;
  font-size: 13px;
  font-weight: 800;
  letter-spacing: 0.08em;
}

.hero-copy h1 {
  max-width: 12ch;
  margin: 0;
  font-size: clamp(48px, 7vw, 96px);
  line-height: 0.98;
  letter-spacing: -0.055em;
}

.hero-text {
  max-width: 52ch;
  margin: 26px 0 0;
  color: rgba(241, 245, 249, 0.8);
  font-size: clamp(16px, 1.8vw, 20px);
  line-height: 1.7;
}

.hero-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 34px;
}

.primary-action,
.secondary-action {
  min-height: 46px;
  border: 0;
  border-radius: 999px;
  padding: 0 20px;
  cursor: pointer;
  font: inherit;
  font-weight: 800;
  white-space: nowrap;
  transition: transform 180ms ease, opacity 180ms ease, box-shadow 180ms ease;
}

.primary-action {
  background: #ffffff;
  color: #0f172a;
  box-shadow: 0 18px 42px rgba(255, 255, 255, 0.22);
}

.primary-action:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.secondary-action {
  border: 1px solid rgba(255, 255, 255, 0.28);
  background: rgba(255, 255, 255, 0.08);
  color: #ffffff;
}

.primary-action:hover,
.secondary-action:hover {
  transform: translateY(-1px);
}

.hero-visual {
  position: relative;
  min-height: 480px;
  overflow: hidden;
  border-radius: 24px;
  background:
    radial-gradient(circle at 30% 22%, rgba(96, 165, 250, 0.5), transparent 18rem),
    linear-gradient(180deg, rgba(255, 255, 255, 0.18), rgba(255, 255, 255, 0.05));
  border: 1px solid rgba(255, 255, 255, 0.16);
  backdrop-filter: blur(22px);
}

.score-orbit {
  position: absolute;
  right: 32px;
  top: 32px;
  display: grid;
  place-items: center;
  width: 154px;
  height: 154px;
  border-radius: 50%;
  background: #dbeafe;
  color: #0f172a;
  box-shadow: 0 30px 80px rgba(15, 23, 42, 0.28);
}

.score-orbit span {
  font-size: 42px;
  font-weight: 900;
  letter-spacing: -0.05em;
}

.score-orbit small {
  margin-top: -36px;
  color: #2563eb;
  font-size: 10px;
  font-weight: 900;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.topic-stack {
  position: absolute;
  left: 32px;
  right: 32px;
  bottom: 34px;
  display: grid;
  gap: 12px;
}

.topic-stack span {
  width: fit-content;
  max-width: 100%;
  border-radius: 999px;
  padding: 12px 16px;
  background: rgba(255, 255, 255, 0.88);
  color: #0f172a;
  font-weight: 900;
  box-shadow: 0 18px 50px rgba(15, 23, 42, 0.22);
}

.topic-stack span:nth-child(even) {
  justify-self: end;
  background: rgba(191, 219, 254, 0.94);
}

.recommendation-body {
  max-width: 1360px;
  margin: clamp(64px, 9vw, 130px) auto 0;
}

.body-heading {
  max-width: 780px;
  margin-bottom: 34px;
}

.body-heading h2,
.state-panel h2 {
  margin: 0;
  color: #0f172a;
  font-size: clamp(34px, 5vw, 64px);
  line-height: 1;
  letter-spacing: -0.045em;
}

.body-heading p,
.state-panel p {
  margin: 16px 0 0;
  max-width: 62ch;
  color: #64748b;
  font-size: 16px;
  line-height: 1.7;
}

.masonry-grid {
  column-count: 3;
  column-gap: 22px;
}

.recommendation-card,
.skeleton-card {
  display: inline-block;
  width: 100%;
  margin: 0 0 22px;
  overflow: hidden;
  break-inside: avoid;
  border: 1px solid rgba(15, 23, 42, 0.08);
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 24px 70px rgba(15, 23, 42, 0.12);
}

.recommendation-card {
  cursor: pointer;
}

.image-wrap {
  position: relative;
  overflow: hidden;
  aspect-ratio: 1.2;
  background: #dbeafe;
}

.tall-card .image-wrap {
  aspect-ratio: 0.92;
}

.compact-card .image-wrap {
  aspect-ratio: 1.65;
}

.image-wrap img {
.cover-placeholder {
  width: 100%;
  height: 100%;
  background:
    radial-gradient(circle at 20% 25%, rgba(191, 219, 254, 0.9), transparent 36%),
    linear-gradient(135deg, #e2e8f0, #cbd5e1);
}

  width: 100%;
  height: 100%;
  display: block;
  object-fit: cover;
  filter: grayscale(0.18) contrast(1.08) saturate(0.85);
  transition: transform 700ms ease, filter 700ms ease;
}

.recommendation-card:hover img {
  transform: scale(1.06);
  filter: grayscale(0) contrast(1.12) saturate(0.98);
}

.score-chip {
  position: absolute;
  right: 14px;
  top: 14px;
  border-radius: 999px;
  padding: 8px 11px;
  background: #ffffff;
  color: #1d4ed8;
  font-size: 13px;
  font-weight: 900;
  box-shadow: 0 16px 36px rgba(15, 23, 42, 0.18);
}

.card-content {
  padding: 22px;

.card-content > .author-identity {
  margin-bottom: 16px;
}
}

.card-meta,
.card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: #64748b;
  font-size: 12px;
  font-weight: 800;
}

.card-content h3 {
  margin: 16px 0 0;
  color: #0f172a;
  font-size: clamp(23px, 2.2vw, 31px);
  line-height: 1.05;
  letter-spacing: -0.035em;
}

.card-content p {
  margin: 14px 0 0;
  color: #475569;
  line-height: 1.65;
}

.tag-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 18px;
}

.tag-row span {
  max-width: 100%;
  border-radius: 999px;
  padding: 7px 10px;
  background: #eef2ff;
  color: #1d4ed8;
  font-size: 12px;
  font-weight: 800;
}

.card-footer {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid rgba(15, 23, 42, 0.08);
}

.card-footer button {
  border: 0;
  border-radius: 999px;
  padding: 9px 13px;
  background: #0f172a;
  color: #ffffff;
  cursor: pointer;
  font-weight: 800;
}

.state-panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  max-width: 1040px;
  margin: clamp(54px, 8vw, 112px) auto 0;
  padding: clamp(28px, 5vw, 56px);
  border: 1px solid rgba(15, 23, 42, 0.08);
  border-radius: 26px;
  background: rgba(255, 255, 255, 0.82);
  box-shadow: 0 30px 90px rgba(15, 23, 42, 0.12);
}

.state-panel .primary-action {
  background: #1d4ed8;
  color: #ffffff;
}

.state-panel .secondary-action {
  border-color: rgba(15, 23, 42, 0.14);
  background: #0f172a;
  color: #ffffff;
}

.skeleton-card {
  padding: 18px;
}

.skeleton-image,
.skeleton-line {
  display: block;
  border-radius: 18px;
  background: linear-gradient(90deg, rgba(226, 232, 240, 0.9), rgba(248, 250, 252, 0.9), rgba(226, 232, 240, 0.9));
  background-size: 220% 100%;
  animation: shimmer 1.4s ease-in-out infinite;
}

.skeleton-image {
  height: 220px;
}

.skeleton-line {
  height: 15px;
  margin-top: 15px;
}

.skeleton-line.wide {
  width: 86%;
}

.skeleton-line.short {
  width: 46%;
}

@keyframes shimmer {
  0% {
    background-position: 100% 0;
  }
  100% {
    background-position: 0 0;
  }
}

@media (max-width: 1080px) {
  .recommendation-hero {
    grid-template-columns: 1fr;
  }

  .hero-visual {
    min-height: 360px;
  }

  .masonry-grid {
    column-count: 2;
  }
}

@media (max-width: 720px) {
  .recommendation-page {
    padding: 18px;
  }

  .recommendation-hero {
    min-height: auto;
    padding: 28px;
    border-radius: 22px;
  }

  .hero-copy h1 {
    max-width: 10ch;
    font-size: clamp(42px, 13vw, 64px);
  }

  .hero-actions,
  .state-panel {
    align-items: stretch;
    flex-direction: column;
  }

  .hero-visual {
    min-height: 300px;
  }

  .score-orbit {
    width: 120px;
    height: 120px;
  }

  .score-orbit span {
    font-size: 32px;
  }

  .masonry-grid {
    column-count: 1;
  }
}
@container (max-width: 720px) {
  .recommendation-hero {
    grid-template-columns: 1fr;
    min-height: auto;
    gap: 28px;
    padding: 28px;
  }

  .hero-copy h1 {
    font-size: clamp(42px, 10vw, 64px);
  }

  .hero-visual {
    min-height: 300px;
  }

  .masonry-grid {
    column-count: 1;
  }

  .state-panel {
    align-items: stretch;
    flex-direction: column;
    padding: 28px;
  }
}
</style>
