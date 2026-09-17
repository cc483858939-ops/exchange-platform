<template>
  <main class="bookmarks-view">
    <header class="bookmarks-view__header">
      <button
        class="bookmarks-view__back"
        type="button"
        aria-label="Back"
        @click="goBack"
      >
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1>Bookmarks</h1>
      <MobileAccountMenu v-if="viewerID !== null" />
    </header>

    <div
      ref="bookmarksScrollViewportRef"
      class="bookmarks-scroll-viewport"
    >
      <section
        v-if="viewerID === null"
        class="bookmarks-view__state bookmarks-view__state--auth"
        aria-labelledby="bookmarks-auth-title"
      >
        <p id="bookmarks-auth-title">Log in to view your bookmarks.</p>
        <RouterLink
          class="bookmarks-view__primary"
          :to="{ name: 'Login', query: { returnTo: route.fullPath } }"
        >
          Log in
        </RouterLink>
      </section>

      <template v-else>
        <section
          v-if="initialLoading && !loaded"
          class="bookmarks-view__feed bookmarks-view__feed--loading"
          aria-live="polite"
          aria-label="Loading bookmarks"
        >
          <article
            v-for="skeleton in skeletonRows"
            :key="skeleton"
            class="bookmarks-skeleton"
            aria-hidden="true"
          >
            <span class="bookmarks-skeleton__avatar"></span>
            <div class="bookmarks-skeleton__body">
              <span class="bookmarks-skeleton__author"></span>
              <span class="bookmarks-skeleton__line bookmarks-skeleton__line--wide"></span>
              <span class="bookmarks-skeleton__line bookmarks-skeleton__line--medium"></span>
              <span class="bookmarks-skeleton__media"></span>
              <span class="bookmarks-skeleton__actions"></span>
            </div>
          </article>
        </section>

        <section
          v-else-if="initialError"
          class="bookmarks-view__state"
          role="alert"
          aria-live="polite"
        >
          <p>Bookmarks could not be loaded.</p>
          <button class="bookmarks-view__primary" type="button" @click="retryInitial">
            Retry
          </button>
        </section>

        <section
          v-else-if="showEmpty"
          class="bookmarks-view__state bookmarks-view__state--empty"
          aria-live="polite"
        >
          <AppIcon name="bookmark" :size="28" />
          <h2>Save posts for later</h2>
          <p>Bookmark posts to easily find them again.</p>
        </section>

        <section v-else class="bookmarks-view__feed" aria-label="Bookmarked posts">
          <PostCard
            v-for="post in bookmarkItems"
            :key="post.id"
            :post="post"
            :track-view="false"
            :like-pending="likePendingPostIDs.has(post.id)"
            :repost-pending="repostPendingPostIDs.has(post.id)"
            :bookmark-pending="bookmarkPendingPostIDs.has(post.id)"
            @toggle-like="handleLikeToggle"
            @toggle-repost="handleRepostToggle"
            @toggle-bookmark="handleBookmarkToggle"
          />

          <div
            v-if="nextCursor || loadingMore || loadMoreError"
            ref="bookmarkSentinelRef"
            class="bookmarks-view__sentinel"
            aria-live="polite"
          >
            <span v-if="loadingMore">Loading more bookmarks...</span>
            <template v-else-if="loadMoreError">
              <span role="alert">Could not load more bookmarks.</span>
              <button class="bookmarks-view__primary" type="button" @click="retryLoadMore">
                Retry
              </button>
            </template>
            <button
              v-else-if="!intersectionObserverAvailable && nextCursor"
              class="bookmarks-view__primary"
              type="button"
              @click="loadMoreBookmarks"
            >
              Load more bookmarks
            </button>
          </div>
        </section>

        <p v-if="revalidateError" class="bookmarks-view__status" role="status" aria-live="polite">
          {{ revalidateError }}
        </p>
        <p v-if="mutationError" class="bookmarks-view__status" role="status" aria-live="polite">
          {{ mutationError }}
        </p>
      </template>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useBookmarksSessionStore } from '../store/bookmarksSession';

defineOptions({
  name: 'BookmarksView',
});

const route = useRoute();
const router = useRouter();
const bookmarksSession = useBookmarksSessionStore();
const {
  viewerID,
  items: bookmarkItems,
  loaded,
  initialLoading,
  initialError,
  nextCursor,
  loadingMore,
  loadMoreError,
  stale,
  revalidating,
  revalidateError,
  scrollTop,
  likePendingPostIDs,
  repostPendingPostIDs,
  bookmarkPendingPostIDs,
  mutationErrors,
} = storeToRefs(bookmarksSession);

const skeletonRows = [0, 1, 2];
const bookmarksScrollViewportRef = ref<HTMLElement | null>(null);
const bookmarkSentinelRef = ref<HTMLElement | null>(null);
const intersectionObserverAvailable = computed(() => typeof IntersectionObserver !== 'undefined');
let observer: IntersectionObserver | null = null;
let mounted = false;
const bookmarksViewActive = ref(true);
let resumeOnActivation = false;
let entryVersion = 0;
let restoredEntryVersion = -1;

const mutationError = computed(() => Array.from(mutationErrors.value.values())[0] ?? '');
const showEmpty = computed(() => (
  loaded.value
  && !initialLoading.value
  && !initialError.value
  && bookmarkItems.value.length === 0
  && nextCursor.value === null
));

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const canLoadMore = () => (
  mounted
  && bookmarksViewActive.value
  && route.name === 'Bookmarks'
  && viewerID.value !== null
  && loaded.value
  && Boolean(nextCursor.value)
  && !initialLoading.value
  && !loadingMore.value
  && !loadMoreError.value
  && !stale.value
  && !revalidating.value
);

const loadMoreBookmarks = () => {
  if (canLoadMore()) {
    void bookmarksSession.loadMore();
  }
};

const updateObserver = async () => {
  if (
    !mounted
    || !bookmarksViewActive.value
    || route.name !== 'Bookmarks'
  ) {
    disconnectObserver();
    return;
  }

  await nextTick();
  if (
    !mounted
    || !bookmarksViewActive.value
    || route.name !== 'Bookmarks'
  ) {
    disconnectObserver();
    return;
  }

  disconnectObserver();
  if (
    !intersectionObserverAvailable.value
    || !canLoadMore()
    || !bookmarksScrollViewportRef.value
    || !bookmarkSentinelRef.value
  ) return;

  const root = bookmarksScrollViewportRef.value;
  observer = new IntersectionObserver((entries) => {
    if (
      canLoadMore()
      && entries.some(entry => entry.isIntersecting)
    ) {
      loadMoreBookmarks();
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(bookmarkSentinelRef.value);
};

const saveCurrentScroll = () => {
  const viewport = bookmarksScrollViewportRef.value;
  if (!viewport) return;
  bookmarksSession.saveScrollTop(viewport.scrollTop);
};

const restoreScrollOnce = async () => {
  const capturedEntryVersion = entryVersion;
  if (
    !mounted
    || !bookmarksViewActive.value
    || route.name !== 'Bookmarks'
    || restoredEntryVersion === capturedEntryVersion
    || !loaded.value
    || initialLoading.value
  ) return;

  await nextTick();
  if (
    !mounted
    || !bookmarksViewActive.value
    || route.name !== 'Bookmarks'
    || capturedEntryVersion !== entryVersion
    || restoredEntryVersion === capturedEntryVersion
  ) return;

  const viewport = bookmarksScrollViewportRef.value;
  if (!viewport) return;
  viewport.scrollTop = scrollTop.value;
  restoredEntryVersion = capturedEntryVersion;
};

const revalidateIfNeeded = () => {
  if (
    mounted
    && bookmarksViewActive.value
    && route.name === 'Bookmarks'
    && loaded.value
    && stale.value
  ) {
    void bookmarksSession.revalidateBookmarks();
  }
};

const retryInitial = () => {
  bookmarksSession.retryInitial();
};

const retryLoadMore = () => {
  bookmarksSession.retryLoadMore();
};

const handleLikeToggle = (postID: number) => {
  void bookmarksSession.toggleLike(postID);
};

const handleRepostToggle = (postID: number) => {
  void bookmarksSession.toggleRepost(postID);
};

const handleBookmarkToggle = (postID: number) => {
  void bookmarksSession.toggleBookmark(postID);
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }
  void router.push({ name: 'Home' });
};

onBeforeRouteLeave(() => {
  saveCurrentScroll();
});

watch(
  [viewerID, () => route.name],
  ([nextViewerID, routeName]) => {
    entryVersion += 1;
    restoredEntryVersion = -1;
    if (
      nextViewerID !== null
      && bookmarksViewActive.value
      && routeName === 'Bookmarks'
      && !loaded.value
      && !initialLoading.value
    ) {
      void bookmarksSession.loadInitial();
    }
  },
  { immediate: true },
);

watch([viewerID, loaded, initialLoading], () => {
  void restoreScrollOnce();
}, { flush: 'post' });

watch(
  [nextCursor, loadingMore, loadMoreError, () => bookmarkItems.value.length, stale, revalidating, loaded],
  () => { void updateObserver(); },
  { flush: 'post' },
);

watch([loaded, stale], () => {
  revalidateIfNeeded();
}, { flush: 'post' });

onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
  revalidateIfNeeded();
  void updateObserver();
});

onDeactivated(() => {
  if (!bookmarksViewActive.value) return;
  saveCurrentScroll();
  bookmarksViewActive.value = false;
  resumeOnActivation = true;
  disconnectObserver();
});

onActivated(() => {
  if (!resumeOnActivation) return;

  resumeOnActivation = false;
  bookmarksViewActive.value = true;
  entryVersion += 1;
  restoredEntryVersion = -1;

  if (route.name !== 'Bookmarks') return;
  const shouldRevalidate = loaded.value && stale.value;

  void nextTick(async () => {
    if (!mounted || !bookmarksViewActive.value || route.name !== 'Bookmarks') return;
    void restoreScrollOnce();
    await updateObserver();
    if (
      shouldRevalidate
      && mounted
      && bookmarksViewActive.value
      && route.name === 'Bookmarks'
    ) {
      void bookmarksSession.revalidateBookmarks();
    }
  });
});

onBeforeUnmount(() => {
  if (bookmarksViewActive.value && route.name === 'Bookmarks') {
    saveCurrentScroll();
  }
  mounted = false;
  bookmarksViewActive.value = false;
  resumeOnActivation = false;
  disconnectObserver();
});
</script>

<style scoped>
.bookmarks-view {
  display: flex;
  width: 100%;
  height: 100vh;
  height: 100dvh;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--color-surface);
  color: var(--color-text);
}

.bookmarks-view__header {
  position: relative;
  z-index: 5;
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr) auto;
  flex: 0 0 auto;
  min-height: 56px;
  align-items: center;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.bookmarks-view__header h1 {
  margin: 0;
  font-size: 20px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.bookmarks-view__back {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 50%;
  padding: 0;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
}

.bookmarks-view__back:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .bookmarks-view__back:hover {
    background: var(--color-surface-subtle);
    color: var(--color-accent);
  }
}

.bookmarks-scroll-viewport {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  overflow-anchor: auto;
  -webkit-overflow-scrolling: touch;
}

.bookmarks-view__feed {
  width: 100%;
  max-width: 600px;
  min-width: 0;
  margin-inline: auto;
}

.bookmarks-view__state {
  display: grid;
  max-width: 320px;
  min-height: 260px;
  align-content: center;
  justify-items: center;
  gap: var(--space-3);
  margin-inline: auto;
  padding: 56px var(--space-5);
  color: var(--color-text-secondary);
  text-align: center;
}

.bookmarks-view__state p,
.bookmarks-view__state h2 {
  margin: 0;
}

.bookmarks-view__state h2 {
  color: var(--color-text);
  font-size: 20px;
  letter-spacing: -0.02em;
}

.bookmarks-view__state--empty .app-icon {
  color: var(--color-accent);
}

.bookmarks-view__primary {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.bookmarks-view__primary:focus-visible {
  border-color: var(--color-accent-hover);
  background: var(--color-accent-hover);
  outline: 2px solid color-mix(in srgb, var(--color-accent) 32%, transparent);
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .bookmarks-view__primary:hover {
    border-color: var(--color-accent-hover);
    background: var(--color-accent-hover);
  }
}

.bookmarks-view__sentinel {
  display: flex;
  min-height: 72px;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  color: var(--color-text-secondary);
  font-size: 13px;
  text-align: center;
}

.bookmarks-view__sentinel .bookmarks-view__primary {
  flex: 0 0 auto;
}

.bookmarks-view__status {
  max-width: 600px;
  margin: 0 auto;
  padding: 0 var(--space-5) var(--space-4);
  color: var(--color-danger);
  font-size: 13px;
  text-align: center;
}

.bookmarks-skeleton {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr);
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.bookmarks-skeleton__avatar,
.bookmarks-skeleton__body span {
  display: block;
  background: var(--color-surface-subtle);
  animation: bookmarks-shimmer 1.2s ease-in-out infinite;
}

.bookmarks-skeleton__avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
}

.bookmarks-skeleton__body {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}

.bookmarks-skeleton__body span {
  height: 12px;
  border-radius: var(--radius-sm);
}

.bookmarks-skeleton__author {
  width: 38%;
  height: 14px !important;
}

.bookmarks-skeleton__line--wide {
  width: 92%;
}

.bookmarks-skeleton__line--medium {
  width: 66%;
}

.bookmarks-skeleton__media {
  width: 100%;
  height: 112px !important;
  margin-top: var(--space-2);
  border-radius: var(--radius-md);
}

.bookmarks-skeleton__actions {
  width: 58%;
  margin-top: var(--space-1);
}

@keyframes bookmarks-shimmer {
  0%, 100% { opacity: 0.55; }
  50% { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .bookmarks-skeleton__avatar,
  .bookmarks-skeleton__body span {
    animation: none;
  }
}

@media (max-width: 799px) {
  .bookmarks-view {
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

  .bookmarks-view__header {
    grid-template-columns: 44px minmax(0, 1fr) auto;
    padding-inline: var(--space-3);
  }
}

@media (min-width: 800px) {
  .bookmarks-view__back {
    visibility: hidden;
    pointer-events: none;
  }
}

@media (max-width: 420px) {
  .bookmarks-view__header,
  .bookmarks-skeleton {
    padding-inline: var(--space-4);
  }

  .bookmarks-view__state,
  .bookmarks-view__sentinel,
  .bookmarks-view__status {
    padding-inline: var(--space-4);
  }
}
</style>
