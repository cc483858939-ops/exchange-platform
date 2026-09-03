<template>
  <main class="home-view">
    <header class="home-feed-header">
      <MobileHomeHeader />
      <div class="home-feed-header__content">
        <h1>Home</h1>
        <FeedTabs :active-tab="activeTab" @select="selectTab" />
      </div>
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
      v-else-if="activeFeedStatus.loading && !hasRecentlyPublishedPosts"
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
      v-else-if="activeFeedStatus.error && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>Feed unavailable</h2>
      <button class="home-state__primary" type="button" @click="retryActiveFeed">Retry</button>
    </section>

    <section
      v-else-if="activeFeedStatus.empty && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>{{ activeTab === 'for-you' ? 'No recommendations yet' : 'No posts from people you follow yet' }}</h2>
    </section>

    <section
      v-else
      class="feed-list"
    >
      <template v-if="activeTab === 'for-you'">
        <PostCard
          v-for="post in feedStore.recentlyPublishedPosts"
          :key="'recent-' + post.id"
          :post="post"
          :like-pending="likePendingPostIds.has(post.id)"
          :repost-pending="repostPendingPostIds.has(post.id)"
          :show-delete="canDeletePost(post)"
          :delete-pending="pendingDeletePostIds.has(post.id)"
          :delete-error="deleteErrors.get(post.id) || ''"
          @toggle-like="handleLikeToggle"
          @toggle-repost="handleRepostToggle"
          @delete-post="handleDeletePost"
        />

        <div
          v-if="forYouFeed.loading && hasRecentlyPublishedPosts"
          class="home-feed-inline-state"
          aria-live="polite"
        >
          Loading recommendations...
        </div>
        <div
          v-else-if="forYouFeed.error && hasRecentlyPublishedPosts"
          class="home-feed-inline-state"
          aria-live="polite"
        >
          <span>Could not load recommendations.</span>
          <button class="home-state__primary" type="button" @click="loadForYou(true)">
            Retry
          </button>
        </div>

        <div
          v-for="item in visibleForYouItems"
          :key="item.recommendation.post.id"
          class="recommendation-card-wrapper"
          :ref="element => bindRecommendationCard(element, item)"
        >
          <PostCard
            :post="item.post"
            :like-pending="likePendingPostIds.has(item.post.id)"
            :repost-pending="repostPendingPostIds.has(item.post.id)"
            :show-not-interested="true"
            :show-delete="canDeletePost(item.post)"
            :delete-pending="pendingDeletePostIds.has(item.post.id)"
            :delete-error="deleteErrors.get(item.post.id) || ''"
            @post-click="handleRecommendationClick(item.recommendation)"
            @toggle-like="handleLikeToggle"
            @toggle-repost="handleRepostToggle"
            @not-interested="handleNotInterested"
            @delete-post="handleDeletePost"
          />
        </div>

        <div
          v-if="!forYouFeed.depleted || forYouFeed.loadingMore || forYouFeed.loadMoreError"
          ref="forYouSentinelRef"
          class="home-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="forYouFeed.loadingMore">Loading more recommendations...</span>
          <template v-else-if="forYouFeed.loadMoreError">
            <span>Could not load more recommendations.</span>
            <button class="home-state__primary" type="button" @click="retryForYouLoadMore">
              Retry
            </button>
          </template>
          <button
            v-else-if="!forYouIntersectionObserverAvailable && !forYouFeed.depleted"
            class="home-state__primary"
            type="button"
            @click="loadMoreForYou"
          >
            Load more recommendations
          </button>
        </div>
      </template>

      <template v-else>
        <PostCard
          v-for="post in followingFeed.items"
          :key="post.id"
          :post="post"
          :like-pending="likePendingPostIds.has(post.id)"
          :repost-pending="repostPendingPostIds.has(post.id)"
          :show-delete="canDeletePost(post)"
          :delete-pending="pendingDeletePostIds.has(post.id)"
          :delete-error="deleteErrors.get(post.id) || ''"
          @toggle-like="handleLikeToggle"
          @toggle-repost="handleRepostToggle"
          @delete-post="handleDeletePost"
        />

        <div
          v-if="followingFeed.nextCursor || followingFeed.loadingMore || followingFeed.loadMoreError"
          ref="followingSentinelRef"
          class="home-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="followingFeed.loadingMore">Loading more posts...</span>
          <template v-else-if="followingFeed.loadMoreError">
            <span>Could not load more posts.</span>
            <button class="home-state__primary" type="button" @click="retryFollowingLoadMore">
              Retry
            </button>
          </template>
          <button
            v-else-if="!followingIntersectionObserverAvailable && followingFeed.nextCursor"
            class="home-state__primary"
            type="button"
            @click="loadMoreFollowing"
          >
            Load more posts
          </button>
        </div>
      </template>
    </section>

    </div>

    <RouterLink
      v-if="authStore.isAuthenticated"
      class="home-compose-fab"
      :to="{ name: 'ArticleCreate' }"
      aria-label="Post"
      title="Post"
    >
      <AppIcon name="plus" :size="24" />
    </RouterLink>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import type { ComponentPublicInstance } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import FeedTabs from '../components/feed/FeedTabs.vue';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileHomeHeader from '../components/layout/MobileHomeHeader.vue';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import { useHomeTimelineStore } from '../store/homeTimeline';
import type { RecommendedPost } from '../types/Recommendation';
import type { FeedPost, FeedTab } from '../types/Feed';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const homeTimeline = useHomeTimelineStore();
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

const skeletonPosts = [0, 1, 2];
const recommendationCardElements = new Map<number, HTMLElement>();
const forYouSentinelRef = ref<HTMLElement | null>(null);
const forYouIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let forYouObserver: IntersectionObserver | null = null;
const followingSentinelRef = ref<HTMLElement | null>(null);
const followingIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let followingObserver: IntersectionObserver | null = null;

const forYouFeed = homeTimeline.forYou;
const followingFeed = homeTimeline.following;
const likePendingPostIds = homeTimeline.likePendingPostIds;
const repostPendingPostIds = homeTimeline.repostPendingPostIds;
const pendingDeletePostIds = homeTimeline.pendingDeletePostIds;
const deleteErrors = homeTimeline.deleteErrors;

const activeTab = computed<FeedTab>(() => homeTimeline.activeTab);
const activeFeedStatus = computed(() => {
  const state = activeTab.value === 'for-you' ? forYouFeed : followingFeed;
  return {
    loading: state.loading || (!state.loaded && !state.error),
    error: state.error,
    empty: state.loaded && !state.loading && !state.error && state.items.length === 0,
  };
});

const hasRecentlyPublishedPosts = computed(
  () => activeTab.value === 'for-you' && feedStore.recentlyPublishedPosts.length > 0,
);
const recentlyPublishedIDs = computed(
  () => new Set(feedStore.recentlyPublishedPosts.map((post) => post.id)),
);
const visibleForYouItems = computed(() => forYouFeed.items.filter((item) =>
  !recentlyPublishedIDs.value.has(item.post.id)
  && !feedStore.isPostDeleted(item.post.id)
));

const currentViewerID = () => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
};

const canDeletePost = (post: FeedPost) =>
  authStore.isAuthenticated
  && currentViewerID() !== null
  && post.author.id === currentViewerID();

const saveCurrentScroll = (tab: FeedTab) => {
  if (typeof window !== 'undefined') {
    homeTimeline.setScrollY(tab, window.scrollY);
  }
};

const restoreScroll = (tab: FeedTab) => {
  void nextTick(() => {
    const state = tab === 'for-you' ? forYouFeed : followingFeed;
    if (!state.loaded || typeof window === 'undefined') {
      return;
    }
    if (
      typeof window.scrollTo === 'function'
      && !window.navigator.userAgent.toLowerCase().includes('jsdom')
    ) {
      window.scrollTo({ top: homeTimeline.scrollY[tab], behavior: 'auto' });
    }
  });
};

const selectTab = (tab: FeedTab) => {
  if (activeTab.value === tab) {
    return;
  }
  saveCurrentScroll(activeTab.value);
  homeTimeline.setActiveTab(tab);
  void router.push({
    name: 'Home',
    query: tab === 'following' ? { tab } : {},
  });
};

const normalizeRouteTab = (value: unknown) => {
  const tab: FeedTab = value === 'following' ? 'following' : 'for-you';
  homeTimeline.setActiveTab(tab);
  if (value === undefined || value === 'for-you' || value === 'following') {
    return;
  }
  void router.replace({
    name: 'Home',
    query: { tab: 'for-you' },
  });
};

const disconnectFollowingObserver = () => {
  followingObserver?.disconnect();
  followingObserver = null;
};

const disconnectForYouObserver = () => {
  forYouObserver?.disconnect();
  forYouObserver = null;
};

const updateForYouObserver = () => {
  disconnectForYouObserver();
  if (
    !forYouIntersectionObserverAvailable
    || activeTab.value !== 'for-you'
    || !forYouSentinelRef.value
    || !authStore.isAuthenticated
    || !forYouFeed.loaded
    || forYouFeed.loading
    || forYouFeed.loadingMore
    || forYouFeed.loadMoreError
    || forYouFeed.depleted
  ) {
    return;
  }

  forYouObserver = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      void homeTimeline.loadMoreForYou();
    }
  }, { rootMargin: '800px 0px' });
  forYouObserver.observe(forYouSentinelRef.value);
};

const updateFollowingObserver = () => {
  disconnectFollowingObserver();
  if (
    !followingIntersectionObserverAvailable
    || activeTab.value !== 'following'
    || !followingSentinelRef.value
    || !followingFeed.nextCursor
    || followingFeed.loadingMore
    || followingFeed.stale
    || followingFeed.revalidating
    || followingFeed.loadMoreError
    || !authStore.isAuthenticated
  ) {
    return;
  }

  followingObserver = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      void homeTimeline.loadMoreFollowing();
    }
  }, { rootMargin: '240px 0px' });
  followingObserver.observe(followingSentinelRef.value);
};

const bindCurrentRecommendationCards = async () => {
  await nextTick();
  if (!authStore.isAuthenticated || activeTab.value !== 'for-you') {
    return;
  }
  visibleForYouItems.value.forEach((item) => {
    const element = recommendationCardElements.get(item.recommendation.post.id);
    if (element) {
      recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
    }
  });
};

const resetRecommendationObservation = () => {
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
  recommendationCardElements.clear();
};

const loadForYou = async (force = false) => {
  await homeTimeline.loadForYou(force);
  await bindCurrentRecommendationCards();
};

const loadFollowing = async (force = false) => {
  if (!force && followingFeed.loaded && followingFeed.stale) {
    await homeTimeline.revalidateFollowing();
  } else {
    await homeTimeline.loadFollowing(force);
  }
  await nextTick(updateFollowingObserver);
};

const loadMoreFollowing = () => {
  void homeTimeline.loadMoreFollowing();
};

const loadMoreForYou = () => {
  void homeTimeline.loadMoreForYou();
};

const retryForYouLoadMore = () => {
  homeTimeline.retryForYouLoadMore();
};

const retryFollowingLoadMore = () => {
  homeTimeline.retryFollowingLoadMore();
};

const loadActiveFeed = (tab: FeedTab = activeTab.value) => {
  if (tab === 'for-you') {
    void loadForYou();
  } else {
    void loadFollowing();
  }
};

const retryActiveFeed = () => {
  if (activeTab.value === 'for-you') {
    void loadForYou(true);
  } else {
    void loadFollowing(true);
  }
};

const bindRecommendationCard = (
  element: Element | ComponentPublicInstance | null,
  item: { recommendation: RecommendedPost },
) => {
  if (element instanceof HTMLElement) {
    recommendationCardElements.set(item.recommendation.post.id, element);
    recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
    return;
  }

  recommendationCardElements.delete(item.recommendation.post.id);
  recommendationTelemetry.detachFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  queueMicrotask(() => {
    if (recommendationCardElements.has(item.recommendation.post.id)) {
      return;
    }
    const stillRendered = visibleForYouItems.value.some(
      visibleItem => visibleItem.recommendation.post.id === item.recommendation.post.id,
    );
    if (!stillRendered) {
      recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
    }
  });
};

const handleRecommendationClick = (recommendation: RecommendedPost) => {
  savePendingRecommendationAttribution(recommendation.post.id, recommendation.tracking);
  recommendationTelemetry.recordClick(recommendation.post.id, recommendation.tracking);
};

const handleLikeToggle = (postId: number) => {
  void homeTimeline.toggleLike(postId);
};

const handleRepostToggle = (postId: number) => {
  void homeTimeline.toggleRepost(postId);
};

const handleDeletePost = async (postId: number) => {
  const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
  if (item) {
    recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  }
  await homeTimeline.deletePost(postId);
};

const handleNotInterested = (postId: number) => {
  const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
  if (!item) {
    return;
  }
  recommendationTelemetry.recordNotInterested(item.recommendation.post.id, item.recommendation.tracking);
  recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  homeTimeline.dismissRecommendation(postId);
  recommendationCardElements.delete(postId);
};

watch(() => route.query.tab, normalizeRouteTab, { immediate: true });

watch(
  activeTab,
  (tab, previousTab) => {
    if (previousTab && previousTab !== tab) {
      saveCurrentScroll(previousTab);
      if (previousTab === 'for-you') {
        resetRecommendationObservation();
        disconnectForYouObserver();
      } else {
        disconnectFollowingObserver();
      }
    }
    loadActiveFeed(tab);
    restoreScroll(tab);
  },
  { immediate: true },
);

watch(
  [
    activeTab,
    () => followingFeed.nextCursor,
    () => followingFeed.loadingMore,
    () => followingFeed.loadMoreError,
    () => followingFeed.loading,
    () => followingFeed.stale,
    () => followingFeed.revalidating,
  ],
  () => {
    void nextTick(updateFollowingObserver);
  },
  { flush: 'post' },
);

watch(
  [
    activeTab,
    () => forYouFeed.items.length,
    () => forYouFeed.loaded,
    () => forYouFeed.loading,
    () => forYouFeed.loadingMore,
    () => forYouFeed.loadMoreError,
    () => forYouFeed.depleted,
    () => authStore.isAuthenticated,
  ],
  () => {
    void nextTick(updateForYouObserver);
  },
  { flush: 'post', immediate: true },
);

watch(
  () => forYouFeed.items.map((item) => item.recommendation.post.id).join(','),
  () => {
    void bindCurrentRecommendationCards();
  },
  { flush: 'post' },
);

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated) => {
    if (isAuthenticated) {
      loadActiveFeed();
    }
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  saveCurrentScroll(activeTab.value);
  disconnectForYouObserver();
  disconnectFollowingObserver();
  resetRecommendationObservation();
});
</script>

<style scoped>
.home-view {
  min-height: 100vh;
  min-height: 100dvh;
  background: var(--color-surface);
  color: var(--color-text);
}

.home-feed-header {
  position: sticky;
  top: 0;
  z-index: 20;
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.home-feed-header__content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
}

.home-feed-header h1 {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.02em;
}

.home-compose-fab {
  display: none;
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

.home-feed-inline-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 56px;
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.home-feed-sentinel {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  min-height: 64px;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

@media (max-width: 620px) {
  .home-state {
    padding-inline: var(--space-4);
  }
}

@media (max-width: 799px) {
  .home-feed-header {
    top: var(--mobile-safe-top);
    padding: 0;
  }

  .home-feed-header__content {
    display: block;
    padding: 0;
  }

  .home-feed-header h1 {
    display: none;
  }

  .home-compose-fab {
    position: fixed;
    right: 16px;
    bottom: calc(
      var(--mobile-bottom-nav-height)
      + var(--mobile-safe-bottom)
      + 16px
    );
    z-index: 30;
    display: grid;
    width: var(--mobile-fab-size);
    height: var(--mobile-fab-size);
    place-items: center;
    border-radius: 50%;
    background: var(--color-accent);
    color: var(--color-surface);
    text-decoration: none;
    box-shadow: 0 6px 18px color-mix(in srgb, var(--color-text) 18%, transparent);
    transition: background-color var(--transition-fast), transform var(--transition-fast);
  }

  .home-compose-fab:hover,
  .home-compose-fab:focus-visible {
    background: var(--color-accent-hover);
  }

  .home-compose-fab:active {
    transform: scale(0.96);
  }
}

@media (prefers-reduced-motion: reduce) {
  .feed-skeleton span {
    animation: none;
  }
}
</style>
