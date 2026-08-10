<template>
  <main class="home-view">
    <header class="home-feed-header">
      <h1>Home</h1>
      <FeedTabs :active-tab="activeTab" @select="selectTab" />
    </header>

    <div
      :id="'feed-panel-' + activeTab"
      class="home-feed-panel"
      role="tabpanel"
      tabindex="0"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
    <section
      v-if="!authStore.isAuthenticated"
      class="home-state home-state--auth"
      aria-labelledby="home-auth-title"
    >
      <h2 id="home-auth-title">Sign in to view your financial feed</h2>
      <div class="home-state__actions">
        <RouterLink class="home-state__primary" :to="{ name: 'Login' }">Log in</RouterLink>
        <RouterLink class="home-state__secondary" :to="{ name: 'Register' }">Sign up</RouterLink>
      </div>
    </section>

    <section
      v-else-if="activeFeedStatus.loading"
      class="feed-list feed-list--loading"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
      <article v-for="skeleton in skeletonPosts" :key="skeleton" class="feed-skeleton" aria-hidden="true">
        <span class="feed-skeleton__author"></span>
        <span class="feed-skeleton__title"></span>
        <span class="feed-skeleton__line"></span>
        <span class="feed-skeleton__line feed-skeleton__line--short"></span>
      </article>
    </section>

    <section
      v-else-if="activeFeedStatus.error"
      class="home-state"
      aria-live="polite"
    >
      <h2>Feed unavailable</h2>
      <button class="home-state__primary" type="button" @click="retryActiveFeed">Retry</button>
    </section>

    <section
      v-else-if="activeFeedStatus.empty"
      class="home-state"
      aria-live="polite"
    >
      <h2>{{ activeTab === 'for-you' ? 'No recommendations yet' : 'No articles available' }}</h2>
    </section>

    <section
      v-else
      class="feed-list"
    >
      <template v-if="activeTab === 'for-you'">
        <div
          v-for="item in forYouFeed.items"
          :key="item.article.id"
          class="recommendation-card-wrapper"
          :ref="element => bindRecommendationCard(element, item)"
        >
          <PostCard
            :post="item.post"
            :like-pending="likePendingArticleIds.has(item.post.id)"
            :show-not-interested="true"
            @article-click="handleRecommendationClick(item.article)"
            @toggle-like="handleLikeToggle"
            @not-interested="handleNotInterested"
          />
        </div>
      </template>

      <PostCard
        v-for="post in latestFeed.items"
        v-else
        :key="post.id"
        :post="post"
        :like-pending="likePendingArticleIds.has(post.id)"
        @toggle-like="handleLikeToggle"
      />
    </section>

    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, watch } from 'vue';
import type { ComponentPublicInstance } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import FeedTabs from '../components/feed/FeedTabs.vue';
import PostCard from '../components/feed/PostCard.vue';
import { getArticles } from '../services/articleService';
import { getArticleRecommendations } from '../services/recommendationService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { useAuthStore } from '../store/auth';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FeedLikeStateUpdate, FeedPost, FeedTab } from '../types/Feed';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  recommendationToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';

type FeedState<T> = {
  items: T[];
  loading: boolean;
  error: boolean;
  loaded: boolean;
};

type RecommendationFeedItem = {
  article: RecommendedArticle;
  post: FeedPost;
};

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

const latestFeed = reactive<FeedState<FeedPost>>({
  items: [],
  loading: false,
  error: false,
  loaded: false,
});

const forYouFeed = reactive<FeedState<RecommendationFeedItem>>({
  items: [],
  loading: false,
  error: false,
  loaded: false,
});

const skeletonPosts = [0, 1, 2];
const recommendationCardElements = new Map<number, HTMLElement>();
let authGeneration = 0;
let latestRequestVersion = 0;
let forYouRequestVersion = 0;
const likePendingArticleIds = reactive(new Set<number>());
const likeMutationVersions = new Map<number, number>();
let homeLikeGeneration = 0;

const activeTab = computed<FeedTab>(() => route.query.tab === 'latest' ? 'latest' : 'for-you');
const activeFeedStatus = computed(() => {
  const state = activeTab.value === 'for-you' ? forYouFeed : latestFeed;
  return {
    loading: state.loading || (!state.loaded && !state.error),
    error: state.error,
    empty: state.loaded && !state.loading && !state.error && state.items.length === 0,
  };
});

const selectTab = (tab: FeedTab) => {
  if (activeTab.value === tab) {
    return;
  }
  void router.push({
    name: 'Home',
    query: {
      ...route.query,
      tab,
    },
  });
};

const normalizeRouteTab = (value: unknown) => {
  if (value === undefined || value === 'for-you' || value === 'latest') {
    return;
  }
  void router.replace({
    name: 'Home',
    query: {
      ...route.query,
      tab: 'for-you',
    },
  });
};

const resetLatest = () => {
  latestFeed.items = [];
  latestFeed.loading = false;
  latestFeed.error = false;
  latestFeed.loaded = false;
};

const resetForYou = () => {
  forYouFeed.items = [];
  forYouFeed.loading = false;
  forYouFeed.error = false;
  forYouFeed.loaded = false;
};

const invalidateAfterLogout = () => {
  authGeneration += 1;
  invalidateHomeLikeWork();
  latestRequestVersion += 1;
  forYouRequestVersion += 1;
  resetLatest();
  resetForYou();
  recommendationCardElements.clear();
  recommendationTelemetry.resetObservedCards();
  recommendationTelemetry.clearSession();
};

const isCurrentLatestRequest = (version: number, generation: number) =>
  version === latestRequestVersion
  && generation === authGeneration
  && authStore.isAuthenticated;

const isCurrentForYouRequest = (version: number, generation: number) =>
  version === forYouRequestVersion
  && generation === authGeneration
  && authStore.isAuthenticated;

const getLikeMutationVersion = (articleId: number) =>
  likeMutationVersions.get(articleId) ?? 0;

const bumpLikeMutationVersion = (articleId: number) => {
  const nextVersion = getLikeMutationVersion(articleId) + 1;
  likeMutationVersions.set(articleId, nextVersion);
  return nextVersion;
};

const invalidateHomeLikeWork = () => {
  homeLikeGeneration += 1;
  likePendingArticleIds.clear();
  likeMutationVersions.clear();
};

const findHomePost = (articleId: number): FeedPost | undefined => {
  const latestPost = latestFeed.items.find((post) => post.id === articleId);
  const forYouPost = forYouFeed.items.find((item) => item.post.id === articleId)?.post;
  return activeTab.value === 'for-you'
    ? forYouPost || latestPost
    : latestPost || forYouPost;
};

const forEachHomePost = (articleId: number, callback: (post: FeedPost) => void) => {
  latestFeed.items.forEach((post) => {
    if (post.id === articleId) {
      callback(post);
    }
  });
  forYouFeed.items.forEach((item) => {
    if (item.post.id === articleId) {
      callback(item.post);
    }
  });
};

const applyHomeLikeUpdate = (update: FeedLikeStateUpdate, expectedVersion?: number) => {
  if (
    expectedVersion !== undefined
    && getLikeMutationVersion(update.articleId) !== expectedVersion
  ) {
    return false;
  }

  let applied = false;
  forEachHomePost(update.articleId, (post) => {
    applied = applyFeedLikeStateUpdate(post, update) || applied;
  });
  return applied;
};

const markHomeHydrationUnavailable = (articleIds: number[], versions: Map<number, number>) => {
  articleIds.forEach((articleId) => {
    const capturedVersion = versions.get(articleId);
    if (capturedVersion === undefined || getLikeMutationVersion(articleId) !== capturedVersion) {
      return;
    }
    forEachHomePost(articleId, (post) => {
      if (post.likeStatus === 'unknown') {
        setFeedPostLikeUnavailable(post);
      }
    });
  });
};

const hydrateHomeLikeStates = async (
  articleIds: number[],
  isCurrent: () => boolean,
) => {
  const uniqueIds = Array.from(new Set(articleIds));
  if (uniqueIds.length === 0) {
    return;
  }

  const versions = new Map(
    uniqueIds.map((articleId) => [articleId, getLikeMutationVersion(articleId)]),
  );

  try {
    const response = await getArticleLikeStates(uniqueIds);
    if (!isCurrent()) {
      return;
    }

    const readyIds = new Set<number>();
    response.items.forEach((item) => {
      const capturedVersion = versions.get(item.article_id);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(item.article_id) !== capturedVersion
        || !findHomePost(item.article_id)
      ) {
        return;
      }

      readyIds.add(item.article_id);
      applyHomeLikeUpdate({
        articleId: item.article_id,
        likes: item.likes,
        liked: item.liked,
        status: 'ready',
      }, capturedVersion);
    });

    response.unavailable_article_ids.forEach((articleId) => {
      if (readyIds.has(articleId)) {
        return;
      }
      const capturedVersion = versions.get(articleId);
      if (
        capturedVersion !== undefined
        && getLikeMutationVersion(articleId) === capturedVersion
        && findHomePost(articleId)
      ) {
        applyHomeLikeUpdate({
          articleId,
          likes: 0,
          liked: false,
          status: 'unavailable',
        }, capturedVersion);
      }
    });
  } catch {
    if (isCurrent()) {
      markHomeHydrationUnavailable(uniqueIds, versions);
    }
  }
};

const getLikeErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;
const loadLatest = async (force = false) => {
  if (!authStore.isAuthenticated || latestFeed.loading && !force) {
    return;
  }
  if (force) {
    invalidateHomeLikeWork();
  }
  if (latestFeed.loaded && !force) {
    return;
  }

  const version = ++latestRequestVersion;
  const generation = authGeneration;
  latestFeed.loading = true;
  latestFeed.error = false;

  try {
    const articles = await getArticles();
    if (!isCurrentLatestRequest(version, generation)) {
      return;
    }
    latestFeed.items = articles.map(articleToFeedPost);
    latestFeed.loaded = true;
    const likeGeneration = homeLikeGeneration;
    void hydrateHomeLikeStates(
      latestFeed.items.map((post) => post.id),
      () => isCurrentLatestRequest(version, generation) && homeLikeGeneration === likeGeneration,
    );
  } catch {
    if (!isCurrentLatestRequest(version, generation)) {
      return;
    }
    latestFeed.error = true;
  } finally {
    if (version === latestRequestVersion && generation === authGeneration) {
      latestFeed.loading = false;
    }
  }
};

const handleLikeToggle = async (articleId: number) => {
  const post = findHomePost(articleId);
  if (
    !post
    || post.likeStatus !== 'ready'
    || likePendingArticleIds.has(articleId)
  ) {
    return;
  }

  const previousLiked = post.liked;
  const previousLikes = post.likeCount;
  const mutationVersion = bumpLikeMutationVersion(articleId);
  const likeGeneration = homeLikeGeneration;
  const generation = authGeneration;
  likePendingArticleIds.add(articleId);

  applyHomeLikeUpdate({
    articleId,
    likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
    liked: !previousLiked,
    status: 'ready',
  }, mutationVersion);

  const isCurrentMutation = () =>
    authStore.isAuthenticated
    && authGeneration === generation
    && homeLikeGeneration === likeGeneration
    && getLikeMutationVersion(articleId) === mutationVersion
    && likePendingArticleIds.has(articleId);

  try {
    const result = previousLiked
      ? await unlikeArticle(articleId)
      : await likeArticle(articleId);

    if (isCurrentMutation()) {
      const settledVersion = bumpLikeMutationVersion(articleId);
      applyHomeLikeUpdate({
        articleId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      }, settledVersion);
      likePendingArticleIds.delete(articleId);
    }
  } catch (error) {
    if (!isCurrentMutation()) {
      return;
    }

    const settledVersion = bumpLikeMutationVersion(articleId);
    applyHomeLikeUpdate({
      articleId,
      likes: previousLikes,
      liked: previousLiked,
      status: 'ready',
    }, settledVersion);

    if (getLikeErrorStatus(error) === 503) {
      applyHomeLikeUpdate({
        articleId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'unavailable',
      }, settledVersion);
    }
    likePendingArticleIds.delete(articleId);
  }
};
const bindCurrentRecommendationCards = async () => {
  await nextTick();
  if (!authStore.isAuthenticated || activeTab.value !== 'for-you') {
    return;
  }
  forYouFeed.items.forEach((item) => {
    const element = recommendationCardElements.get(item.article.id);
    if (element) {
      recommendationTelemetry.observeCard(element, item.article.id, item.article.tracking);
    }
  });
};

const resetRecommendationObservation = () => {
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
  recommendationCardElements.clear();
};

const loadForYou = async (force = false) => {
  if (!authStore.isAuthenticated || forYouFeed.loading && !force) {
    return;
  }
  if (force) {
    invalidateHomeLikeWork();
  }
  if (forYouFeed.loaded && !force) {
    await bindCurrentRecommendationCards();
    return;
  }

  const version = ++forYouRequestVersion;
  const generation = authGeneration;
  forYouFeed.loading = true;
  forYouFeed.error = false;
  resetRecommendationObservation();

  try {
    const articles = await getArticleRecommendations(50);
    if (!isCurrentForYouRequest(version, generation)) {
      return;
    }
    forYouFeed.items = articles.map((article) => ({
      article,
      post: recommendationToFeedPost(article),
    }));
    forYouFeed.loaded = true;
    const likeGeneration = homeLikeGeneration;
    void hydrateHomeLikeStates(
      forYouFeed.items.map((item) => item.post.id),
      () => isCurrentForYouRequest(version, generation) && homeLikeGeneration === likeGeneration,
    );
    forYouFeed.loading = false;
    await bindCurrentRecommendationCards();
  } catch {
    if (!isCurrentForYouRequest(version, generation)) {
      return;
    }
    forYouFeed.error = true;
  } finally {
    if (version === forYouRequestVersion && generation === authGeneration) {
      forYouFeed.loading = false;
    }
  }
};

const loadActiveFeed = async (tab: FeedTab = activeTab.value) => {
  if (tab === 'for-you') {
    await loadForYou();
    return;
  }
  await loadLatest();
};

const retryActiveFeed = () => {
  if (activeTab.value === 'for-you') {
    void loadForYou(true);
  } else {
    void loadLatest(true);
  }
};

const bindRecommendationCard = (
  element: Element | ComponentPublicInstance | null,
  item: RecommendationFeedItem,
) => {
  if (element instanceof HTMLElement) {
    recommendationCardElements.set(item.article.id, element);
    recommendationTelemetry.observeCard(element, item.article.id, item.article.tracking);
    return;
  }
  recommendationCardElements.delete(item.article.id);
};

const handleRecommendationClick = (article: RecommendedArticle) => {
  savePendingRecommendationAttribution(article.id, article.tracking);
  recommendationTelemetry.recordClick(article.id, article.tracking);
};

const handleNotInterested = (articleId: number) => {
  const item = forYouFeed.items.find((feedItem) => feedItem.article.id === articleId);
  if (!item) {
    return;
  }

  recommendationTelemetry.recordNotInterested(item.article.id, item.article.tracking);
  forYouFeed.items = forYouFeed.items.filter((feedItem) => feedItem.article.id !== articleId);
  recommendationCardElements.delete(articleId);
};

watch(
  () => route.query.tab,
  normalizeRouteTab,
  { immediate: true },
);

watch(
  activeTab,
  (tab, previousTab) => {
    if (previousTab === 'for-you' && tab !== 'for-you') {
      resetRecommendationObservation();
    }
    void loadActiveFeed(tab);
  },
  { immediate: true },
);

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated) => {
    if (!isAuthenticated) {
      invalidateAfterLogout();
      return;
    }
    void loadActiveFeed();
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  invalidateHomeLikeWork();
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
  recommendationCardElements.clear();
});
</script>

<style scoped>
.home-view {
  min-height: 100vh;
  background: var(--color-surface);
  color: var(--color-text);
}

.home-feed-header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

@media (max-width: 799px) {
  .home-feed-header {
    top: var(--app-mobile-nav-offset, 0px);
  }
}

.home-feed-header h1 {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.02em;
}

.home-feed-body {
  min-width: 0;
}

.home-feed-panel,
.feed-list {
  min-width: 0;
}

.recommendation-card-wrapper {
  min-width: 0;
}

.home-state {
  display: grid;
  justify-items: center;
  gap: var(--space-5);
  max-width: 480px;
  margin: 0 auto;
  padding: 72px var(--space-5);
  text-align: center;
}

.home-state h2 {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  line-height: 1.25;
}

.home-state__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--space-3);
}

.home-state__primary,
.home-state__secondary {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.home-state__primary {
  border: 1px solid var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.home-state__secondary {
  border: 1px solid var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-text);
}

.home-state__primary:hover,
.home-state__primary:focus-visible,
.home-state__secondary:hover,
.home-state__secondary:focus-visible {
  border-color: var(--color-accent);
}

.feed-skeleton {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.feed-skeleton span {
  display: block;
  height: 12px;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
  animation: feed-shimmer 1.2s ease-in-out infinite;
}

.feed-skeleton__author {
  width: 132px;
}

.feed-skeleton__title {
  width: 74%;
  height: 20px !important;
}

.feed-skeleton__line {
  width: 92%;
}

.feed-skeleton__line--short {
  width: 58% !important;
}

@keyframes feed-shimmer {
  0%,
  100% {
    opacity: 0.55;
  }
  50% {
    opacity: 1;
  }
}

@media (max-width: 620px) {
  .home-feed-header {
    align-items: stretch;
    flex-direction: column;
    padding: var(--space-3) var(--space-4);
  }

  .home-feed-header h1 {
    font-size: 20px;
  }

  .home-state {
    padding-inline: var(--space-4);
  }
}

@media (prefers-reduced-motion: reduce) {
  .feed-skeleton span {
    animation: none;
  }
}
</style>
