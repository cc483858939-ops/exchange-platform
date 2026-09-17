<template>
  <main class="history-view">
    <header class="history-view__header">
      <button class="history-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1>History</h1>
      <MobileAccountMenu v-if="currentViewerID !== null" />
    </header>

    <nav class="history-view__tabs" role="tablist" aria-label="History sections">
      <button
        ref="bookmarksTabRef"
        id="history-bookmarks-tab"
        class="history-view__tab"
        :class="{ 'history-view__tab--active': activeTab === 'bookmarks' }"
        type="button"
        role="tab"
        aria-controls="history-bookmarks-panel"
        :aria-selected="activeTab === 'bookmarks'"
        :tabindex="activeTab === 'bookmarks' ? 0 : -1"
        @click="selectTab('bookmarks')"
        @keydown="handleTabKeydown($event, 'bookmarks')"
      >
        <AppIcon name="bookmark" :size="18" />
        <span>Bookmarks</span>
      </button>
      <button
        ref="likesTabRef"
        id="history-likes-tab"
        class="history-view__tab"
        :class="{ 'history-view__tab--active': activeTab === 'likes' }"
        type="button"
        role="tab"
        aria-controls="history-likes-panel"
        :aria-selected="activeTab === 'likes'"
        :tabindex="activeTab === 'likes' ? 0 : -1"
        @click="selectTab('likes')"
        @keydown="handleTabKeydown($event, 'likes')"
      >
        <AppIcon name="heart" :size="18" />
        <span>Likes</span>
      </button>
    </nav>

    <div ref="historyScrollViewportRef" class="history-scroll-viewport">
      <section
        v-if="currentViewerID === null"
        class="history-view__state history-view__state--auth"
        aria-labelledby="history-auth-title"
      >
        <p id="history-auth-title">Log in to view your history.</p>
        <RouterLink
          class="history-view__primary"
          :to="{ name: 'Login', query: { returnTo: route.fullPath } }"
        >
          Log in
        </RouterLink>
      </section>

      <template v-else>
        <section
          v-if="activeInitialLoading && !activeLoaded"
          class="history-view__feed history-view__feed--loading"
          :id="activePanelID"
          role="tabpanel"
          :aria-labelledby="activeTabID"
          aria-live="polite"
        >
          <template v-if="activeTab === 'bookmarks'">
            <article v-for="skeleton in skeletonPosts" :key="skeleton" class="history-skeleton" aria-hidden="true">
              <span class="history-skeleton__avatar"></span>
              <span class="history-skeleton__author"></span>
              <span class="history-skeleton__line"></span>
              <span class="history-skeleton__line history-skeleton__line--short"></span>
              <span class="history-skeleton__media"></span>
              <span class="history-skeleton__actions"></span>
            </article>
          </template>
          <template v-else>
            <article v-for="skeleton in skeletonPosts" :key="skeleton" class="history-skeleton" aria-hidden="true">
              <span class="history-skeleton__author"></span>
              <span class="history-skeleton__title"></span>
              <span class="history-skeleton__line"></span>
              <span class="history-skeleton__line history-skeleton__line--short"></span>
            </article>
          </template>
        </section>

        <section
          v-else-if="activeInitialError"
          class="history-view__state"
          :id="activePanelID"
          role="tabpanel"
          :aria-labelledby="activeTabID"
          aria-live="polite"
        >
          <p role="alert">History could not be loaded.</p>
          <button class="history-view__primary" type="button" @click="retryInitial">Retry</button>
        </section>

        <section
          v-else-if="activeShowEmpty"
          class="history-view__state"
          :id="activePanelID"
          role="tabpanel"
          :aria-labelledby="activeTabID"
          aria-live="polite"
        >
          <template v-if="activeTab === 'bookmarks'">
            <AppIcon name="bookmark" :size="40" aria-hidden="true" />
            <h2>Save posts for later</h2>
            <p>Bookmark posts to easily find them again.</p>
          </template>
          <p v-else>No liked posts yet.</p>
        </section>

        <section
          v-else
          class="history-view__feed"
          :id="activePanelID"
          role="tabpanel"
          :aria-labelledby="activeTabID"
          :aria-label="activePostAriaLabel"
        >
          <PostCard
            v-for="post in activePosts"
            :key="post.id"
            :post="post"
            :track-view="false"
            :like-pending="activeLikePendingPostIDs.has(post.id)"
            :repost-pending="activeRepostPendingPostIDs.has(post.id)"
            :bookmark-pending="activeBookmarkPendingPostIDs.has(post.id)"
            @toggle-like="handleLikeToggle"
            @toggle-repost="handleRepostToggle"
            @toggle-bookmark="handleBookmarkToggle"
          />

          <div
            v-if="activeNextCursor || activeLoadingMore || activeLoadMoreError"
            ref="historySentinelRef"
            class="history-view__sentinel"
            aria-live="polite"
          >
            <span v-if="activeLoadingMore">Loading more posts...</span>
            <template v-else-if="activeLoadMoreError">
              <span>{{ activeTab === 'bookmarks' ? 'Could not load more bookmarks.' : 'Could not load more posts.' }}</span>
              <button class="history-view__primary" type="button" @click="retryLoadMore">Retry</button>
            </template>
            <button
              v-else-if="!intersectionObserverAvailable && activeNextCursor"
              class="history-view__primary"
              type="button"
              @click="loadMore"
            >
              Load more posts
            </button>
          </div>
        </section>

        <p v-if="activeRevalidateError" class="history-view__inline-error" role="status" aria-live="polite">
          {{ activeRevalidateError }}
        </p>
        <p v-if="activeMutationError" class="history-view__inline-error" role="status" aria-live="polite">
          {{ activeMutationError }}
        </p>
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
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useBookmarksSessionStore } from '../store/bookmarksSession';
import { useHistorySessionStore } from '../store/historySession';

type HistoryTab = 'bookmarks' | 'likes';

const normalizeHistoryTab = (value: unknown): HistoryTab => {
  const raw = Array.isArray(value) ? value[0] : value;
  return raw === 'likes' ? 'likes' : 'bookmarks';
};

const route = useRoute();
const router = useRouter();
const historySession = useHistorySessionStore();
const bookmarksSession = useBookmarksSessionStore();
const {
  viewerID: currentViewerID,
  items: historyPosts,
  loaded: historyLoaded,
  initialLoading: historyInitialLoading,
  initialError: historyInitialError,
  nextCursor: historyNextCursor,
  loadingMore: historyLoadingMore,
  loadMoreError: historyLoadMoreError,
  stale: historyStale,
  revalidating: historyRevalidating,
  revalidateError: historyRevalidateError,
  scrollTop: historyScrollTop,
  pendingUnlikePostIDs: likePendingPostIDs,
  repostPendingPostIDs: historyRepostPendingPostIDs,
  bookmarkPendingPostIDs: historyBookmarkPendingPostIDs,
  mutationErrors: historyMutationErrors,
} = storeToRefs(historySession);
const {
  items: bookmarkPosts,
  loaded: bookmarksLoaded,
  initialLoading: bookmarksInitialLoading,
  initialError: bookmarksInitialError,
  nextCursor: bookmarksNextCursor,
  loadingMore: bookmarksLoadingMore,
  loadMoreError: bookmarksLoadMoreError,
  stale: bookmarksStale,
  revalidating: bookmarksRevalidating,
  revalidateError: bookmarksRevalidateError,
  scrollTop: bookmarksScrollTop,
  likePendingPostIDs: bookmarksLikePendingPostIDs,
  repostPendingPostIDs: bookmarksRepostPendingPostIDs,
  bookmarkPendingPostIDs: bookmarksBookmarkPendingPostIDs,
  mutationErrors: bookmarksMutationErrors,
} = storeToRefs(bookmarksSession);

const activeTab = computed<HistoryTab>(() => normalizeHistoryTab(route.query.tab));
const activeTabID = computed(() => `history-${activeTab.value}-tab`);
const activePanelID = computed(() => `history-${activeTab.value}-panel`);
const activePosts = computed(() => activeTab.value === 'bookmarks' ? bookmarkPosts.value : historyPosts.value);
const activeLoaded = computed(() => activeTab.value === 'bookmarks' ? bookmarksLoaded.value : historyLoaded.value);
const activeInitialLoading = computed(() => (
  activeTab.value === 'bookmarks' ? bookmarksInitialLoading.value : historyInitialLoading.value
));
const activeInitialError = computed(() => activeTab.value === 'bookmarks' ? bookmarksInitialError.value : historyInitialError.value);
const activeNextCursor = computed(() => activeTab.value === 'bookmarks' ? bookmarksNextCursor.value : historyNextCursor.value);
const activeLoadingMore = computed(() => activeTab.value === 'bookmarks' ? bookmarksLoadingMore.value : historyLoadingMore.value);
const activeLoadMoreError = computed(() => activeTab.value === 'bookmarks' ? bookmarksLoadMoreError.value : historyLoadMoreError.value);
const activeStale = computed(() => activeTab.value === 'bookmarks' ? bookmarksStale.value : historyStale.value);
const activeRevalidating = computed(() => activeTab.value === 'bookmarks' ? bookmarksRevalidating.value : historyRevalidating.value);
const activeRevalidateError = computed(() => activeTab.value === 'bookmarks' ? bookmarksRevalidateError.value : historyRevalidateError.value);
const activeScrollTop = computed(() => activeTab.value === 'bookmarks' ? bookmarksScrollTop.value : historyScrollTop.value);
const activeLikePendingPostIDs = computed(() => activeTab.value === 'bookmarks' ? bookmarksLikePendingPostIDs.value : likePendingPostIDs.value);
const activeRepostPendingPostIDs = computed(() => activeTab.value === 'bookmarks' ? bookmarksRepostPendingPostIDs.value : historyRepostPendingPostIDs.value);
const activeBookmarkPendingPostIDs = computed(() => activeTab.value === 'bookmarks' ? bookmarksBookmarkPendingPostIDs.value : historyBookmarkPendingPostIDs.value);
const activeMutationError = computed(() => {
  const errors = activeTab.value === 'bookmarks' ? bookmarksMutationErrors.value : historyMutationErrors.value;
  return Array.from(errors.values())[0] ?? '';
});
const activeShowEmpty = computed(() => (
  activeLoaded.value
  && !activeInitialLoading.value
  && !activeInitialError.value
  && activePosts.value.length === 0
  && activeNextCursor.value === null
));
const activePostAriaLabel = computed(() => activeTab.value === 'bookmarks' ? 'Bookmarked posts' : 'Liked posts');
const intersectionObserverAvailable = computed(() => typeof IntersectionObserver !== 'undefined');
const historyScrollViewportRef = ref<HTMLElement | null>(null);
const historySentinelRef = ref<HTMLElement | null>(null);
const bookmarksTabRef = ref<HTMLButtonElement | null>(null);
const likesTabRef = ref<HTMLButtonElement | null>(null);
const skeletonPosts = [0, 1, 2];
let observer: IntersectionObserver | null = null;
let mounted = false;
const historyViewActive = ref(true);
let resumeOnActivation = false;
let entryVersion = 0;
let restoredEntryVersion = -1;
const savedScrollTopByTab = new Map<HistoryTab, number>();

const isHistoryRouteActive = () => mounted
  && historyViewActive.value
  && route.name === 'History';

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const saveTabScroll = (tab: HistoryTab, value: number) => {
  const storedValue = tab === 'bookmarks' ? bookmarksScrollTop.value : historyScrollTop.value;
  if (savedScrollTopByTab.get(tab) === value && storedValue === value) {
    return;
  }

  if (tab === 'bookmarks') {
    bookmarksSession.saveScrollTop(value);
  } else {
    historySession.saveScrollTop(value);
  }
  savedScrollTopByTab.set(tab, value);
};

const saveCurrentScroll = () => {
  const viewport = historyScrollViewportRef.value;
  if (viewport) {
    saveTabScroll(activeTab.value, viewport.scrollTop);
  }
};

const loadActiveInitial = () => {
  if (!isHistoryRouteActive() || currentViewerID.value === null) {
    return;
  }

  if (activeTab.value === 'bookmarks') {
    if (!bookmarksLoaded.value && !bookmarksInitialLoading.value) {
      void bookmarksSession.loadInitial();
    }
    return;
  }

  if (!historyLoaded.value && !historyInitialLoading.value) {
    void historySession.loadInitial();
  }
};

const updateObserver = async () => {
  if (!isHistoryRouteActive()) {
    disconnectObserver();
    return;
  }

  await nextTick();
  if (!isHistoryRouteActive()) {
    disconnectObserver();
    return;
  }

  disconnectObserver();
  if (
    !intersectionObserverAvailable.value
    || !historyScrollViewportRef.value
    || !historySentinelRef.value
    || !activeNextCursor.value
    || activeLoadingMore.value
    || activeLoadMoreError.value
    || activeStale.value
    || activeRevalidating.value
    || currentViewerID.value === null
  ) return;

  const root = historyScrollViewportRef.value;
  const observedTab = activeTab.value;
  observer = new IntersectionObserver((entries) => {
    if (
      activeTab.value === observedTab
      && isHistoryRouteActive()
      && entries.some(entry => entry.isIntersecting)
    ) {
      if (observedTab === 'bookmarks') {
        void bookmarksSession.loadMore();
      } else {
        void historySession.loadMore();
      }
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(historySentinelRef.value);
};

const restoreScrollOnce = async () => {
  const capturedEntryVersion = entryVersion;
  if (
    !isHistoryRouteActive()
    || restoredEntryVersion === capturedEntryVersion
    || !activeLoaded.value
    || activeInitialLoading.value
  ) return;
  await nextTick();
  if (
    !isHistoryRouteActive()
    || capturedEntryVersion !== entryVersion
    || restoredEntryVersion === capturedEntryVersion
  ) return;
  const viewport = historyScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  viewport.scrollTop = activeScrollTop.value;
  restoredEntryVersion = capturedEntryVersion;
};

const beginHistoryEntry = () => {
  entryVersion += 1;
  restoredEntryVersion = -1;
};

onBeforeRouteLeave(() => {
  saveCurrentScroll();
});

const retryInitial = () => {
  if (activeTab.value === 'bookmarks') {
    bookmarksSession.retryInitial();
  } else {
    historySession.retryInitial();
  }
};

const retryLoadMore = () => {
  if (activeTab.value === 'bookmarks') {
    bookmarksSession.retryLoadMore();
  } else {
    historySession.retryLoadMore();
  }
};

const loadMore = () => {
  if (activeTab.value === 'bookmarks') {
    void bookmarksSession.loadMore();
  } else {
    void historySession.loadMore();
  }
};

const handleLikeToggle = (postID: number) => {
  if (activeTab.value === 'bookmarks') {
    void bookmarksSession.toggleLike(postID);
  } else {
    void historySession.toggleUnlike(postID);
  }
};

const handleRepostToggle = (postID: number) => {
  if (activeTab.value === 'bookmarks') {
    void bookmarksSession.toggleRepost(postID);
  } else {
    void historySession.toggleRepost(postID);
  }
};

const handleBookmarkToggle = (postID: number) => {
  if (activeTab.value === 'bookmarks') {
    void bookmarksSession.toggleBookmark(postID);
  } else {
    void historySession.toggleBookmark(postID);
  }
};

const selectTab = (tab: HistoryTab) => {
  if (tab === activeTab.value) {
    return;
  }

  void router.replace({
    name: 'History',
    query: tab === 'likes' ? { tab: 'likes' } : {},
  });
};

const focusTab = (tab: HistoryTab) => {
  if (tab === 'bookmarks') {
    bookmarksTabRef.value?.focus();
  } else {
    likesTabRef.value?.focus();
  }
};

const handleTabKeydown = (event: KeyboardEvent, tab: HistoryTab) => {
  const order: HistoryTab[] = ['bookmarks', 'likes'];
  let nextTab: HistoryTab | null = null;

  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
    nextTab = order[(order.indexOf(tab) + 1) % order.length];
  } else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
    nextTab = order[(order.indexOf(tab) - 1 + order.length) % order.length];
  } else if (event.key === 'Home') {
    nextTab = order[0];
  } else if (event.key === 'End') {
    nextTab = order[order.length - 1];
  }

  if (!nextTab) {
    return;
  }

  event.preventDefault();
  selectTab(nextTab);
  void nextTick(() => focusTab(nextTab));
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }
  void router.push({ name: 'Home' });
};

watch(
  [currentViewerID, () => route.name],
  ([nextViewerID]) => {
    beginHistoryEntry();
    if (nextViewerID !== null && route.name === 'History') {
      loadActiveInitial();
    }
  },
  { immediate: true },
);

watch(activeTab, (nextTab, previousTab) => {
  if (nextTab === previousTab) {
    return;
  }

  if (isHistoryRouteActive()) {
    const viewport = historyScrollViewportRef.value;
    if (viewport) {
      saveTabScroll(previousTab, viewport.scrollTop);
    }
  }

  disconnectObserver();
  beginHistoryEntry();
  loadActiveInitial();
  void nextTick(() => {
    void restoreScrollOnce();
    void updateObserver();
  });
}, { flush: 'sync' });

watch(
  [currentViewerID, activeLoaded, activeInitialLoading, activeTab],
  () => {
    void restoreScrollOnce();
  },
  { flush: 'post' },
);

watch(
  [activeNextCursor, activeLoadingMore, activeLoadMoreError, () => activePosts.value.length, activeStale, activeRevalidating, activeTab],
  () => {
    void updateObserver();
  },
  { flush: 'post' },
);

watch(
  [activeLoaded, activeStale, activeTab],
  ([isLoaded, isStale]) => {
    if (!isHistoryRouteActive() || !isLoaded || !isStale) {
      return;
    }

    if (activeTab.value === 'bookmarks') {
      void bookmarksSession.revalidateBookmarks();
    } else {
      void historySession.revalidateHistory();
    }
  },
  { flush: 'post' },
);

onMounted(() => {
  mounted = true;
  loadActiveInitial();
  void restoreScrollOnce();
  void updateObserver();
});

onDeactivated(() => {
  if (!historyViewActive.value) {
    return;
  }

  saveCurrentScroll();
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
  beginHistoryEntry();

  if (route.name !== 'History') {
    return;
  }

  const shouldRevalidate = activeLoaded.value && activeStale.value;
  loadActiveInitial();

  void nextTick(async () => {
    if (!isHistoryRouteActive()) {
      return;
    }

    void restoreScrollOnce();
    await updateObserver();

    if (!isHistoryRouteActive()) {
      return;
    }

    if (shouldRevalidate) {
      if (activeTab.value === 'bookmarks') {
        void bookmarksSession.revalidateBookmarks();
      } else {
        void historySession.revalidateHistory();
      }
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
.history-view__header { position: relative; z-index: 5; display: grid; flex: 0 0 auto; grid-template-columns: 44px minmax(0, 1fr) auto; align-items: center; min-height: 56px; padding: 0 var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
.history-view__header h1 { min-width: 0; margin: 0; overflow: hidden; font-size: 21px; font-weight: 780; letter-spacing: -0.02em; text-overflow: ellipsis; }
.history-view__back { display: grid; width: 44px; height: 44px; place-items: center; border: 0; border-radius: 50%; padding: 0; background: transparent; color: var(--color-text); cursor: pointer; }
.history-view__back:focus-visible { background: var(--color-surface-subtle); color: var(--color-accent); outline: none; }
@media (hover: hover) and (pointer: fine) { .history-view__back:hover { background: var(--color-surface-subtle); color: var(--color-accent); } }
.history-view__tabs { display: flex; flex: 0 0 auto; min-height: 52px; border-bottom: 1px solid var(--color-border); }
.history-view__tab { position: relative; display: inline-flex; flex: 1 1 50%; min-width: 0; min-height: 52px; align-items: center; justify-content: center; gap: var(--space-2); border: 0; padding: 0 var(--space-3); background: transparent; color: var(--color-text-secondary); cursor: pointer; font: inherit; font-size: 14px; font-weight: 700; }
.history-view__tab:focus-visible { background: var(--color-surface-subtle); color: var(--color-text); outline: none; }
.history-view__tab--active { color: var(--color-text); }
.history-view__tab--active::after { position: absolute; right: 28%; bottom: -1px; left: 28%; height: 3px; border-radius: var(--radius-pill); background: var(--color-accent); content: ''; }
@media (hover: hover) and (pointer: fine) { .history-view__tab:not(.history-view__tab--active):hover { background: var(--color-surface-subtle); color: var(--color-text); } }
.history-view__state { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }
.history-view__state p { margin: 0; }
.history-view__state h2 { margin: 0; color: var(--color-text); font-size: 20px; }
.history-view__state .app-icon { color: var(--color-text-secondary); }
.history-view__primary { display: inline-flex; min-height: 40px; align-items: center; justify-content: center; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); padding: 0 var(--space-5); background: var(--color-accent); color: #fff; cursor: pointer; font: inherit; font-size: 14px; font-weight: 750; text-decoration: none; }
.history-view__primary:focus-visible { border-color: var(--color-accent-hover); background: var(--color-accent-hover); }
@media (hover: hover) and (pointer: fine) { .history-view__primary:hover { border-color: var(--color-accent-hover); background: var(--color-accent-hover); } }
.history-scroll-viewport { flex: 1 1 auto; min-width: 0; min-height: 0; overflow-x: hidden; overflow-y: auto; overscroll-behavior-y: contain; overflow-anchor: auto; -webkit-overflow-scrolling: touch; }
.history-view__feed { min-width: 0; }
.history-view__sentinel { display: flex; min-height: 72px; align-items: center; justify-content: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); color: var(--color-text-secondary); text-align: center; }
.history-view__inline-error { margin: 0; padding: 0 var(--space-5) var(--space-4); color: var(--color-danger); font-size: 13px; text-align: center; }
.history-skeleton { position: relative; display: grid; gap: var(--space-2); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.history-skeleton span { display: block; height: 12px; border-radius: var(--radius-sm); background: var(--color-surface-subtle); animation: history-shimmer 1.2s ease-in-out infinite; }
.history-skeleton__avatar { width: 36px; height: 36px !important; border-radius: 50% !important; }
.history-skeleton__author { width: 36%; height: 14px !important; }
.history-skeleton__title { width: 74%; }
.history-skeleton__line--short { width: 52%; }
.history-skeleton__media { width: min(100%, 360px); height: 120px !important; margin-top: var(--space-1); border-radius: var(--radius-sm) !important; }
.history-skeleton__actions { width: 44%; height: 18px !important; margin-top: var(--space-1); }
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
