<template>
  <main class="history-view">
    <header class="history-view__header">
      <button class="history-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
        <span>History</span>
      </button>
    </header>

    <section
      v-if="currentViewerID === null"
      class="history-view__state history-view__state--auth"
      aria-labelledby="history-auth-title"
    >
      <p id="history-auth-title">Log in to view your history.</p>
      <RouterLink class="history-view__primary" :to="{ name: 'Login' }">Log in</RouterLink>
    </section>

    <template v-else>
      <nav class="history-view__tabs" aria-label="History sections">
        <button class="history-view__tab history-view__tab--active" type="button" aria-selected="true">
          Likes
        </button>
      </nav>

      <section v-if="initialLoading && !loaded" class="history-view__feed history-view__feed--loading" aria-live="polite">
        <article v-for="skeleton in skeletonPosts" :key="skeleton" class="history-skeleton" aria-hidden="true">
          <span class="history-skeleton__author"></span>
          <span class="history-skeleton__title"></span>
          <span class="history-skeleton__line"></span>
          <span class="history-skeleton__line history-skeleton__line--short"></span>
        </article>
      </section>

      <section v-else-if="initialError" class="history-view__state" role="alert" aria-live="polite">
        <p>History could not be loaded.</p>
        <button class="history-view__primary" type="button" @click="retryInitial">Retry</button>
      </section>

      <section v-else-if="showEmpty" class="history-view__state" aria-live="polite">
        <p>No liked posts yet.</p>
      </section>

      <section v-else class="history-view__feed" aria-label="Liked posts">
        <PostCard
          v-for="post in historyPosts"
          :key="post.id"
          :post="post"
          :track-view="false"
          :like-pending="likePendingArticleIDs.has(post.id)"
          :repost-pending="repostPendingArticleIDs.has(post.id)"
          @toggle-like="handleLikeToggle"
          @toggle-repost="handleRepostToggle"
        />

        <div
          v-if="nextCursor || loadingMore || loadMoreError"
          ref="historySentinelRef"
          class="history-view__sentinel"
          aria-live="polite"
        >
          <span v-if="loadingMore">Loading more posts...</span>
          <template v-else-if="loadMoreError">
            <span>Could not load more posts.</span>
            <button class="history-view__primary" type="button" @click="retryLoadMore">Retry</button>
          </template>
          <button
            v-else-if="!historyIntersectionObserverAvailable && nextCursor"
            class="history-view__primary"
            type="button"
            @click="loadMore"
          >
            Load more posts
          </button>
        </div>

        <p v-if="unlikeError" class="history-view__inline-error" role="status" aria-live="polite">
          {{ unlikeError }}
        </p>
      </section>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useHistorySessionStore } from '../store/historySession';

const router = useRouter();
const historySession = useHistorySessionStore();
const {
  viewerID: currentViewerID,
  items: historyPosts,
  loaded,
  initialLoading,
  initialError,
  nextCursor,
  loadingMore,
  loadMoreError,
  stale,
  revalidating,
  scrollY,
  pendingUnlikeArticleIDs: likePendingArticleIDs,
  repostPendingArticleIDs,
  mutationErrors,
} = storeToRefs(historySession);
const historyIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
const historySentinelRef = ref<HTMLElement | null>(null);
const skeletonPosts = [0, 1, 2];
let observer: IntersectionObserver | null = null;
let mounted = false;
let entryVersion = 0;
let restoredEntryVersion = -1;

const unlikeError = computed(() => Array.from(mutationErrors.value.values())[0] ?? '');
const showEmpty = computed(() => (
  loaded.value
  && !initialLoading.value
  && !initialError.value
  && historyPosts.value.length === 0
  && nextCursor.value === null
));

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (
    !historyIntersectionObserverAvailable
    || !historySentinelRef.value
    || !nextCursor.value
    || loadingMore.value
    || loadMoreError.value
    || stale.value
    || revalidating.value
    || currentViewerID.value === null
  ) return;

  observer = new IntersectionObserver((entries) => {
    if (entries.some(entry => entry.isIntersecting)) {
      void historySession.loadMore();
    }
  }, { rootMargin: '240px 0px' });
  observer.observe(historySentinelRef.value);
};

const restoreScrollOnce = async () => {
  const capturedEntryVersion = entryVersion;
  if (
    !mounted
    || restoredEntryVersion === capturedEntryVersion
    || !loaded.value
    || initialLoading.value
  ) return;
  await nextTick();
  if (
    !mounted
    || capturedEntryVersion !== entryVersion
    || restoredEntryVersion === capturedEntryVersion
  ) return;
  if (
    typeof window !== 'undefined'
    && typeof window.scrollTo === 'function'
  ) {
    window.scrollTo({ top: scrollY.value, behavior: 'auto' });
  }
  restoredEntryVersion = capturedEntryVersion;
};

const retryInitial = () => { historySession.retryInitial(); };
const retryLoadMore = () => { historySession.retryLoadMore(); };
const loadMore = () => { void historySession.loadMore(); };
const handleLikeToggle = (articleID: number) => { void historySession.toggleUnlike(articleID); };
const handleRepostToggle = (articleID: number) => { void historySession.toggleRepost(articleID); };

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }
  void router.push({ name: 'Home' });
};

watch(
  currentViewerID,
  (nextViewerID) => {
    entryVersion += 1;
    restoredEntryVersion = -1;
    if (nextViewerID !== null) {
      void historySession.loadInitial();
    }
  },
  { immediate: true },
);

watch([currentViewerID, loaded, initialLoading], () => {
  void restoreScrollOnce();
}, { flush: 'post' });

watch([nextCursor, loadingMore, loadMoreError, () => historyPosts.value.length, stale, revalidating], () => {
  void updateObserver();
}, { flush: 'post' });

watch([loaded, stale], ([isLoaded, isStale]) => {
  if (isLoaded && isStale) {
    void historySession.revalidateHistory();
  }
}, { flush: 'post' });

onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});

onBeforeUnmount(() => {
  mounted = false;
  if (typeof window !== 'undefined') historySession.saveScroll(window.scrollY);
  disconnectObserver();
});
</script>

<style scoped>
.history-view { min-height: 100vh; background: var(--color-surface); color: var(--color-text); }
.history-view__header { position: sticky; top: 0; z-index: 5; display: flex; align-items: center; min-height: 56px; padding: 0 var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
.history-view__back { display: inline-flex; align-items: center; gap: var(--space-2); min-height: 40px; margin: 0; border: 0; padding: 0; background: transparent; color: var(--color-text); cursor: pointer; font: inherit; font-size: 22px; font-weight: 750; }
.history-view__back:hover, .history-view__back:focus-visible { color: var(--color-accent); }
.history-view__tabs { display: flex; min-height: 48px; border-bottom: 1px solid var(--color-border); }
.history-view__tab { position: relative; min-width: 92px; border: 0; padding: 0 var(--space-4); background: transparent; color: var(--color-text-secondary); cursor: default; font: inherit; font-size: 14px; font-weight: 700; }
.history-view__tab--active { color: var(--color-text); }
.history-view__tab--active::after { position: absolute; right: var(--space-4); bottom: -1px; left: var(--space-4); height: 2px; background: var(--color-accent); content: ''; }
.history-view__state { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }
.history-view__state p { margin: 0; }
.history-view__primary { display: inline-flex; min-height: 40px; align-items: center; justify-content: center; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); padding: 0 var(--space-5); background: var(--color-accent); color: #fff; cursor: pointer; font: inherit; font-size: 14px; font-weight: 750; text-decoration: none; }
.history-view__primary:hover, .history-view__primary:focus-visible { border-color: var(--color-accent-hover); background: var(--color-accent-hover); }
.history-view__feed { min-width: 0; }
.history-view__sentinel { display: flex; min-height: 72px; align-items: center; justify-content: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); color: var(--color-text-secondary); text-align: center; }
.history-view__inline-error { margin: 0; padding: 0 var(--space-5) var(--space-4); color: var(--color-danger); font-size: 13px; text-align: center; }
.history-skeleton { display: grid; gap: var(--space-2); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.history-skeleton span { display: block; height: 12px; border-radius: var(--radius-sm); background: var(--color-surface-subtle); animation: history-shimmer 1.2s ease-in-out infinite; }
.history-skeleton__author { width: 36%; height: 14px !important; }
.history-skeleton__title { width: 74%; }
.history-skeleton__line--short { width: 52%; }
@keyframes history-shimmer { 0%, 100% { opacity: 0.55; } 50% { opacity: 1; } }
@media (prefers-reduced-motion: reduce) { .history-skeleton span { animation: none; } }
@media (max-width: 799px) { .history-view__header { top: var(--mobile-safe-top); } }
@media (max-width: 420px) { .history-view__header, .history-skeleton { padding-inline: var(--space-4); } }
</style>
