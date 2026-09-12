<template>
  <main class="history-view">
    <header class="history-view__header">
      <button class="history-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
        <span>History</span>
      </button>
    </header>

    <div
      ref="historyScrollViewportRef"
      class="history-scroll-viewport"
    >
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
            :like-pending="likePendingPostIDs.has(post.id)"
            :repost-pending="repostPendingPostIDs.has(post.id)"
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
    </div>
  </main>
</template>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  watch,
} from 'vue';
import { storeToRefs } from 'pinia';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useHistorySessionStore } from '../store/historySession';

const route = useRoute();
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
  scrollTop,
  pendingUnlikePostIDs: likePendingPostIDs,
  repostPendingPostIDs,
  mutationErrors,
} = storeToRefs(historySession);
const historyIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
const historyScrollViewportRef = ref<HTMLElement | null>(null);
const historySentinelRef = ref<HTMLElement | null>(null);
const skeletonPosts = [0, 1, 2];
let observer: IntersectionObserver | null = null;
let mounted = false;
const historyViewActive = ref(true);
let resumeOnActivation = false;
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
  if (
    !mounted
    || !historyViewActive.value
    || route.name !== 'History'
  ) {
    disconnectObserver();
    return;
  }

  await nextTick();
  if (
    !mounted
    || !historyViewActive.value
    || route.name !== 'History'
  ) {
    disconnectObserver();
    return;
  }

  disconnectObserver();
  if (
    !historyIntersectionObserverAvailable
    || !historyScrollViewportRef.value
    || !historySentinelRef.value
    || !nextCursor.value
    || loadingMore.value
    || loadMoreError.value
    || stale.value
    || revalidating.value
    || currentViewerID.value === null
  ) return;

  const root = historyScrollViewportRef.value;
  observer = new IntersectionObserver((entries) => {
    if (
      historyViewActive.value
      && route.name === 'History'
      && entries.some(entry => entry.isIntersecting)
    ) {
      void historySession.loadMore();
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(historySentinelRef.value);
};

const saveCurrentScroll = () => {
  const viewport = historyScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  historySession.saveScrollTop(viewport.scrollTop);
};

const restoreScrollOnce = async () => {
  const capturedEntryVersion = entryVersion;
  if (
    !mounted
    || !historyViewActive.value
    || route.name !== 'History'
    || restoredEntryVersion === capturedEntryVersion
    || !loaded.value
    || initialLoading.value
  ) return;
  await nextTick();
  if (
    !mounted
    || !historyViewActive.value
    || route.name !== 'History'
    || capturedEntryVersion !== entryVersion
    || restoredEntryVersion === capturedEntryVersion
  ) return;
  const viewport = historyScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  viewport.scrollTop = scrollTop.value;
  restoredEntryVersion = capturedEntryVersion;
};

onBeforeRouteLeave(() => {
  saveCurrentScroll();
});

const retryInitial = () => { historySession.retryInitial(); };
const retryLoadMore = () => { historySession.retryLoadMore(); };
const loadMore = () => { void historySession.loadMore(); };
const handleLikeToggle = (postID: number) => { void historySession.toggleUnlike(postID); };
const handleRepostToggle = (postID: number) => { void historySession.toggleRepost(postID); };

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
    if (
      nextViewerID !== null
      && historyViewActive.value
      && route.name === 'History'
    ) {
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
  if (
    !historyViewActive.value
    || route.name !== 'History'
  ) {
    return;
  }

  if (isLoaded && isStale) {
    void historySession.revalidateHistory();
  }
}, { flush: 'post' });

onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});

onDeactivated(() => {
  if (!historyViewActive.value) {
    return;
  }

  historyViewActive.value = false;
  resumeOnActivation = true;
  disconnectObserver();
});

onActivated(() => {
  if (!resumeOnActivation) {
    return;
  }

  resumeOnActivation = false;
  historyViewActive.value = true;

  if (route.name !== 'History') {
    return;
  }

  const shouldRevalidate = loaded.value && stale.value;

  void nextTick(async () => {
    if (
      !mounted
      || !historyViewActive.value
      || route.name !== 'History'
    ) {
      return;
    }

    void restoreScrollOnce();
    await updateObserver();

    if (
      !mounted
      || !historyViewActive.value
      || route.name !== 'History'
    ) {
      return;
    }

    if (shouldRevalidate) {
      void historySession.revalidateHistory();
    }
  });
});

onBeforeUnmount(() => {
  if (
    historyViewActive.value
    && route.name === 'History'
  ) {
    saveCurrentScroll();
  }

  mounted = false;
  historyViewActive.value = false;
  resumeOnActivation = false;
  disconnectObserver();
});
</script>

<style scoped>
.history-view { display: flex; width: 100%; height: 100vh; height: 100dvh; min-height: 0; flex-direction: column; overflow: hidden; background: var(--color-surface); color: var(--color-text); }
.history-view__header { position: relative; z-index: 5; display: flex; flex: 0 0 auto; align-items: center; min-height: 56px; padding: 0 var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
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
.history-scroll-viewport { flex: 1 1 auto; min-width: 0; min-height: 0; overflow-x: hidden; overflow-y: auto; overscroll-behavior-y: contain; overflow-anchor: auto; -webkit-overflow-scrolling: touch; }
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
@media (max-width: 799px) {
  .history-view {
    height: calc(
      100vh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
    height: calc(
      100dvh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
  }
}
@media (max-width: 420px) { .history-view__header, .history-skeleton { padding-inline: var(--space-4); } }
</style>
