<template>
  <main class="topic-view">
    <header class="topic-view__header">
      <button class="topic-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <div class="topic-view__heading">
        <h1>{{ topicTitle }}</h1>
        <p v-if="topicSession.topic">{{ topicSession.topic.description }}</p>
      </div>
      <MobileAccountMenu v-if="topicSession.viewerID !== null" />
    </header>

    <div ref="scrollViewportRef" class="topic-view__scroll">
      <section v-if="topicSession.initialLoading && !topicSession.loaded" class="topic-view__state" aria-live="polite">
        <span class="topic-view__loading-mark" aria-hidden="true"></span>
        <p>Loading topic posts...</p>
      </section>

      <section v-else-if="topicSession.initialError" class="topic-view__state" aria-live="polite">
        <p role="alert">{{ topicSession.initialError }}</p>
        <button class="topic-view__primary" type="button" @click="topicSession.retryInitial">Retry</button>
      </section>

      <section v-else-if="topicSession.loaded && topicSession.items.length === 0" class="topic-view__state">
        <h2>No posts yet</h2>
        <p>There are no posts in this topic right now.</p>
      </section>

      <section v-else class="topic-view__feed" aria-label="Topic posts">
        <div
          class="topic-view__virtual-list"
          data-topic-virtual-list
          :style="{ height: `${topicVirtualizerTotalSize}px` }"
        >
          <div
            v-for="virtualItem in topicVirtualItems"
            :key="String(virtualItem.key)"
            class="topic-view__virtual-row"
            :data-index="virtualItem.index"
            :data-post-id="topicSession.items[virtualItem.index]?.id"
            :style="topicVirtualRowStyle(virtualItem)"
            :ref="measureTopicRow"
          >
            <PostCard
              v-if="topicSession.items[virtualItem.index]"
              :post="topicPostForIndex(virtualItem.index)"
              :track-view="authStore.isAuthenticated"
              :requires-auth-for-actions="!authStore.isAuthenticated"
              :view-session-key="viewSessionKey"
              :like-pending="topicSession.likePendingPostIDs.has(topicPostForIndex(virtualItem.index).id)"
              :repost-pending="topicSession.repostPendingPostIDs.has(topicPostForIndex(virtualItem.index).id)"
              :bookmark-pending="topicSession.bookmarkPendingPostIDs.has(topicPostForIndex(virtualItem.index).id)"
              @toggle-like="topicSession.toggleLike"
              @toggle-repost="topicSession.toggleRepost"
              @toggle-bookmark="topicSession.toggleBookmark"
            />
          </div>
        </div>

        <div
          v-if="topicSession.nextCursor || topicSession.loadingMore || topicSession.loadMoreError"
          ref="sentinelRef"
          class="topic-view__sentinel"
          aria-live="polite"
        >
          <span v-if="topicSession.loadingMore">Loading more posts...</span>
          <template v-else-if="topicSession.loadMoreError">
            <span>{{ topicSession.loadMoreError }}</span>
            <button class="topic-view__primary" type="button" @click="topicSession.retryLoadMore">Retry</button>
          </template>
          <button
            v-else-if="!intersectionObserverAvailable && topicSession.nextCursor"
            class="topic-view__primary"
            type="button"
            @click="topicSession.loadMore"
          >
            Load more posts
          </button>
        </div>
        <p v-if="firstMutationError" class="topic-view__inline-error" role="status" aria-live="polite">
          {{ firstMutationError }}
        </p>
      </section>
    </div>
  </main>
</template>

<script setup lang="ts">
import {
  computed,
  type ComponentPublicInstance,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  watch,
} from 'vue';
import {
  useVirtualizer,
  type VirtualItem,
} from '@tanstack/vue-virtual';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useAuthStore } from '../store/auth';
import { useTopicSessionStore } from '../store/topicSession';
import type { FeedPost } from '../types/Feed';
import { virtualItemsWithInitialFallback } from '../utils/virtualizer';

defineOptions({ name: 'TopicView' });

const TOPIC_POST_ESTIMATE_PX = 360;
const TOPIC_VIRTUAL_OVERSCAN = 8;

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const topicSession = useTopicSessionStore();
const scrollViewportRef = ref<HTMLElement | null>(null);
const sentinelRef = ref<HTMLElement | null>(null);
const topicViewActive = ref(true);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';

const topicVirtualizerOptions = computed(() => ({
  count: topicSession.items.length,
  getScrollElement: () => (topicViewActive.value ? scrollViewportRef.value : null),
  estimateSize: () => TOPIC_POST_ESTIMATE_PX,
  getItemKey: (index: number) => topicSession.items[index]?.id ?? index,
  enabled: true,
  useCachedMeasurements: !topicViewActive.value,
  overscan: TOPIC_VIRTUAL_OVERSCAN,
}));

const topicVirtualizer = useVirtualizer<HTMLElement, HTMLElement>(topicVirtualizerOptions);

const topicVirtualItems = computed(() => virtualItemsWithInitialFallback(
  topicVirtualizer.value.getVirtualItems(),
  topicSession.items.length,
  topicSession.items[0]?.id,
  TOPIC_POST_ESTIMATE_PX,
));
const topicVirtualizerTotalSize = computed(() => topicVirtualizer.value.getTotalSize());
const topicVirtualRowStyle = (virtualItem: VirtualItem) => ({
  transform: `translateY(${virtualItem.start}px)`,
});

const topicPostForIndex = (index: number): FeedPost => {
  const post = topicSession.items[index];
  if (!post) {
    throw new Error(`Unexpected Topic post index ${index}`);
  }
  return post;
};

const measureTopicRow = (element: Element | ComponentPublicInstance | null) => {
  if (element === null) {
    topicVirtualizer.value.measureElement(null);
    return;
  }
  if (element instanceof HTMLElement) {
    topicVirtualizer.value.measureElement(element);
  }
};
const firstMutationError = computed(() => Array.from(topicSession.mutationErrors.values())[0] ?? '');
const topicTitle = computed(() => `#${topicSession.topic?.label ?? topicSession.activeSlug ?? 'Topic'}`);
const viewSessionKey = computed(() => {
  const viewerID = authStore.isAuthenticated ? authStore.currentIdentity?.id : null;
  return `topic:${viewerID ?? 'anonymous'}:${topicSession.activeSlug ?? ''}`;
});

const normalizeTopicSlug = (value: unknown): string => {
  const raw = Array.isArray(value) ? value[0] : value;
  return typeof raw === 'string' ? raw.trim().toLowerCase() : '';
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }
  void router.push({ name: 'Home' });
};

let observer: IntersectionObserver | null = null;
const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const saveCurrentScroll = () => {
  const viewport = scrollViewportRef.value;
  if (viewport) topicSession.saveScrollTop(viewport.scrollTop);
};

let lastMeasuredTopicWidth: number | null = null;
let topicViewportResizeObserver: ResizeObserver | null = null;

const readResizeEntryWidth = (entry: ResizeObserverEntry) => {
  const borderBoxSize = Array.isArray(entry.borderBoxSize)
    ? entry.borderBoxSize[0]
    : entry.borderBoxSize;
  const width = borderBoxSize?.inlineSize
    ?? entry.contentRect?.width
    ?? scrollViewportRef.value?.clientWidth
    ?? 0;
  return Math.round(width);
};

const handleTopicViewportResize = (entries: ResizeObserverEntry[]) => {
  const width = entries[0] ? readResizeEntryWidth(entries[0]) : 0;
  if (width <= 0) return;
  if (lastMeasuredTopicWidth === null) {
    lastMeasuredTopicWidth = width;
    return;
  }
  if (lastMeasuredTopicWidth === width) return;

  lastMeasuredTopicWidth = width;
  topicVirtualizer.value.measure();
};

const disconnectTopicViewportResizeObserver = () => {
  topicViewportResizeObserver?.disconnect();
  topicViewportResizeObserver = null;
};

const observeTopicViewportResize = () => {
  disconnectTopicViewportResizeObserver();
  if (
    !topicViewActive.value
    || typeof ResizeObserver === 'undefined'
    || !scrollViewportRef.value
  ) return;

  const initialWidth = Math.round(scrollViewportRef.value.clientWidth);
  if (initialWidth > 0 && lastMeasuredTopicWidth === null) {
    lastMeasuredTopicWidth = initialWidth;
  }
  topicViewportResizeObserver = new ResizeObserver(handleTopicViewportResize);
  topicViewportResizeObserver.observe(scrollViewportRef.value);
};

const resetTopicScroll = () => {
  topicVirtualizer.value.scrollToOffset(0, { align: 'start', behavior: 'auto' });
  if (scrollViewportRef.value) scrollViewportRef.value.scrollTop = 0;
  topicSession.saveScrollTop(0);
};

const restoreTopicScroll = async () => {
  await nextTick();
  if (!topicViewActive.value || route.name !== 'Topic') return;

  const viewport = scrollViewportRef.value;
  if (!viewport) return;

  const target = topicSession.scrollTop;
  topicVirtualizer.value.scrollToOffset(target, { align: 'start', behavior: 'auto' });
  if (Math.abs(viewport.scrollTop - target) > 1) {
    viewport.scrollTop = target;
  }
};

const updateObserver = () => {
  disconnectObserver();
  if (
    !topicViewActive.value
    || route.name !== 'Topic'
    || !intersectionObserverAvailable
    || !topicSession.nextCursor
    || topicSession.loadingMore
    || topicSession.loadMoreError
    || !sentinelRef.value
    || !scrollViewportRef.value
  ) return;
  observer = new IntersectionObserver((entries) => {
    if (
      topicViewActive.value
      && route.name === 'Topic'
      && entries.some(entry => entry.isIntersecting)
    ) {
      void topicSession.loadMore();
    }
  }, { root: scrollViewportRef.value, rootMargin: '320px 0px' });
  observer.observe(sentinelRef.value);
};

const enterTopic = async (slug: string) => {
  const changed = slug !== topicSession.activeSlug;
  const topicRequest = topicSession.setTopic(slug);

  if (changed) {
    await nextTick();
    if (
      !topicViewActive.value
      || route.name !== 'Topic'
      || slug !== topicSession.activeSlug
    ) {
      await topicRequest;
      return;
    }

    resetTopicScroll();
  }

  await topicRequest;
  if (
    !topicViewActive.value
    || route.name !== 'Topic'
    || slug !== topicSession.activeSlug
  ) return;

  await nextTick();
  if (
    topicViewActive.value
    && route.name === 'Topic'
    && slug === topicSession.activeSlug
  ) {
    if (changed) topicVirtualizer.value.measure();
    updateObserver();
  }
};

onBeforeRouteLeave(() => {
  saveCurrentScroll();
});

onMounted(() => {
  topicViewActive.value = true;
  observeTopicViewportResize();
  void enterTopic(normalizeTopicSlug(route.params.slug));
});

watch(
  [() => route.name, () => route.params.slug],
  ([routeName, rawSlug]) => {
    if (!topicViewActive.value || routeName !== 'Topic') return;

    const slug = normalizeTopicSlug(rawSlug);
    if (slug === topicSession.activeSlug) return;

    void enterTopic(slug);
  },
);

let firstActivation = true;
onActivated(async () => {
  topicViewActive.value = true;
  observeTopicViewportResize();
  if (firstActivation) {
    firstActivation = false;
    return;
  }
  if (route.name !== 'Topic') return;

  const slug = normalizeTopicSlug(route.params.slug);
  if (slug !== topicSession.activeSlug) {
    await enterTopic(slug);
    return;
  }

  await nextTick();
  await restoreTopicScroll();
  updateObserver();
});

onDeactivated(() => {
  if (!topicViewActive.value) return;
  saveCurrentScroll();
  topicViewActive.value = false;
  disconnectObserver();
  disconnectTopicViewportResizeObserver();
});

watch(
  () => [topicSession.nextCursor, topicSession.loadingMore, topicSession.loadMoreError, topicSession.items.length],
  () => {
    if (topicViewActive.value) void nextTick(updateObserver);
  },
  { flush: 'post' },
);

onBeforeUnmount(() => {
  disconnectObserver();
  disconnectTopicViewportResizeObserver();
  topicSession.reset();
});
</script>

<style scoped>
.topic-view { display: flex; width: 100%; height: 100vh; height: 100dvh; min-height: 0; flex-direction: column; overflow: hidden; background: var(--color-surface); color: var(--color-text); }
.topic-view__header { position: relative; z-index: 5; display: grid; flex: 0 0 auto; grid-template-columns: 44px minmax(0, 1fr) auto; align-items: center; min-height: 72px; gap: var(--space-2); padding: var(--space-2) var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
.topic-view__heading { min-width: 0; padding-block: var(--space-2); }
.topic-view__heading h1 { min-width: 0; margin: 0; overflow: hidden; font-size: 21px; font-weight: 780; letter-spacing: -0.02em; text-overflow: ellipsis; }
.topic-view__heading p { margin: var(--space-1) 0 0; color: var(--color-text-secondary); font-size: 13px; line-height: 1.4; }
.topic-view__back { display: grid; width: 44px; height: 44px; place-items: center; border: 0; border-radius: 50%; padding: 0; background: transparent; color: var(--color-text); cursor: pointer; }
.topic-view__back:focus-visible { background: var(--color-surface-subtle); color: var(--color-accent); outline: none; }
@media (hover: hover) and (pointer: fine) { .topic-view__back:hover { background: var(--color-surface-subtle); color: var(--color-accent); } }
.topic-view__scroll { flex: 1 1 auto; min-width: 0; min-height: 0; overflow-x: hidden; overflow-y: auto; overscroll-behavior-y: contain; -webkit-overflow-scrolling: touch; }
.topic-view__feed { min-width: 0; }
.topic-view__virtual-list { position: relative; width: 100%; }
.topic-view__virtual-row { position: absolute; top: 0; left: 0; width: 100%; }
.topic-view__state { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }
.topic-view__state p, .topic-view__state h2 { margin: 0; }
.topic-view__state h2 { color: var(--color-text); font-size: 20px; }
.topic-view__loading-mark { width: 22px; height: 22px; border: 2px solid var(--color-border); border-top-color: var(--color-accent); border-radius: 50%; animation: topic-spin 800ms linear infinite; }
@keyframes topic-spin { to { transform: rotate(360deg); } }
.topic-view__primary { display: inline-flex; min-height: 40px; align-items: center; justify-content: center; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); padding: 0 var(--space-5); background: var(--color-accent); color: #fff; cursor: pointer; font: inherit; font-size: 14px; font-weight: 750; }
.topic-view__primary:focus-visible { border-color: var(--color-accent-hover); background: var(--color-accent-hover); }
@media (hover: hover) and (pointer: fine) { .topic-view__primary:hover { border-color: var(--color-accent-hover); background: var(--color-accent-hover); } }
.topic-view__sentinel { display: flex; min-height: 72px; align-items: center; justify-content: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); color: var(--color-text-secondary); text-align: center; }
.topic-view__inline-error { margin: 0; padding: 0 var(--space-5) var(--space-4); color: var(--color-danger); font-size: 13px; text-align: center; }
@media (prefers-reduced-motion: reduce) { .topic-view__loading-mark { animation: none; } }
@media (max-width: 799px) {
  .topic-view { height: calc(100dvh - var(--mobile-safe-top) - var(--mobile-bottom-nav-height) - var(--mobile-safe-bottom)); }
  .topic-view__primary { min-height: 44px; }
}
@media (max-width: 420px) { .topic-view__header { padding-inline: var(--space-4); } }
</style>
