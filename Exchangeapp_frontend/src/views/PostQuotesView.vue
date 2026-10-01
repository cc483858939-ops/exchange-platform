<template>
  <main class="quotes-view">
    <header class="quotes-view__header">
      <button class="quotes-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1>Quotes</h1>
      <MobileAccountMenu v-if="authStore.isAuthenticated" />
    </header>

    <section v-if="quotesSession.initialLoading && !quotesSession.loaded" class="quotes-view__skeletons" aria-label="Loading quotes" aria-live="polite">
      <article v-for="index in 3" :key="index" class="quotes-skeleton" aria-hidden="true">
        <span class="quotes-skeleton__avatar"></span>
        <span class="quotes-skeleton__line quotes-skeleton__line--name"></span>
        <span class="quotes-skeleton__line quotes-skeleton__line--body"></span>
      </article>
    </section>

    <section v-else-if="quotesSession.targetUnavailable" class="quotes-view__state" role="alert">
      <h2>Post unavailable</h2>
      <p>This post may have been deleted or is no longer available.</p>
    </section>

    <section v-else-if="quotesSession.initialError" class="quotes-view__state" role="alert">
      <p>Quotes could not be loaded.</p>
      <button class="quotes-view__button" type="button" @click="quotesSession.retryInitial">Retry</button>
    </section>

    <section v-else-if="quotesSession.loaded && quotesSession.items.length === 0" class="quotes-view__state">
      <h2>No quotes are available.</h2>
    </section>

    <section v-else-if="quotesSession.loaded" class="quotes-view__feed" aria-label="Quotes">
      <PostCard
        v-for="post in quotesSession.items"
        :key="post.id"
        :post="post"
        :track-view="false"
        :requires-auth-for-actions="!authStore.isAuthenticated"
        :like-pending="quotesSession.likePendingPostIDs.has(post.id)"
        :repost-pending="quotesSession.repostPendingPostIDs.has(post.id)"
        :bookmark-pending="quotesSession.bookmarkPendingPostIDs.has(post.id)"
        @toggle-like="quotesSession.toggleLike"
        @toggle-repost="quotesSession.toggleRepost"
        @toggle-bookmark="quotesSession.toggleBookmark"
      />

      <div
        v-if="quotesSession.nextCursor || quotesSession.loadingMore || quotesSession.loadMoreError"
        ref="sentinelRef"
        class="quotes-view__sentinel"
        aria-live="polite"
      >
        <span v-if="quotesSession.loadingMore">Loading more quotes...</span>
        <template v-else-if="quotesSession.loadMoreError">
          <span>Could not load more quotes.</span>
          <button class="quotes-view__button" type="button" @click="quotesSession.retryLoadMore">Retry</button>
        </template>
        <button
          v-else-if="!intersectionObserverAvailable && quotesSession.nextCursor"
          class="quotes-view__button"
          type="button"
          @click="quotesSession.loadMore"
        >
          Load more quotes
        </button>
      </div>

      <p v-if="firstMutationError" class="quotes-view__mutation-error" role="status" aria-live="polite">
        {{ firstMutationError }}
      </p>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import PostCard from '../components/feed/PostCard.vue';
import { useAuthStore } from '../store/auth';
import { useQuotesSessionStore } from '../store/quotesSession';

defineOptions({ name: 'PostQuotesView' });

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const quotesSession = useQuotesSessionStore();
const sentinelRef = ref<HTMLElement | null>(null);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let observer: IntersectionObserver | null = null;

const firstMutationError = computed(() => Array.from(quotesSession.mutationErrors.values())[0] ?? '');

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }

  const routeID = Array.isArray(route.params.id) ? route.params.id[0] : route.params.id;
  const id = typeof routeID === 'string' && /^[1-9]\d*$/.test(routeID) && Number.isSafeInteger(Number(routeID))
    ? String(Number(routeID))
    : null;
  if (id) {
    void router.push({ name: 'PostDetail', params: { id } });
  } else {
    void router.push({ name: 'Home' });
  }
};

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const updateObserver = () => {
  disconnectObserver();
  if (
    !intersectionObserverAvailable
    || !quotesSession.nextCursor
    || quotesSession.loadingMore
    || quotesSession.loadMoreError
    || !sentinelRef.value
  ) return;

  observer = new IntersectionObserver((entries) => {
    if (entries.some(entry => entry.isIntersecting)) {
      void quotesSession.loadMore();
    }
  }, { rootMargin: '320px 0px' });
  observer.observe(sentinelRef.value);
};

watch(
  () => route.params.id,
  rawID => { void quotesSession.setTarget(rawID); },
  { immediate: true },
);

watch(
  () => [quotesSession.nextCursor, quotesSession.loadingMore, quotesSession.loadMoreError, quotesSession.items.length],
  () => { void nextTick(updateObserver); },
  { flush: 'post' },
);

onMounted(() => {
  void nextTick(updateObserver);
});

onBeforeUnmount(() => {
  disconnectObserver();
  quotesSession.reset();
});
</script>

<style scoped>
.quotes-view { min-height: 100%; background: var(--color-surface); color: var(--color-text); }
.quotes-view__header { position: sticky; top: 0; z-index: 5; display: grid; grid-template-columns: 44px minmax(0, 1fr) auto; align-items: center; min-height: 64px; gap: var(--space-2); padding: var(--space-2) var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
.quotes-view__header h1 { margin: 0; font-size: 20px; font-weight: 780; letter-spacing: -0.02em; }
.quotes-view__back { display: grid; width: 44px; height: 44px; place-items: center; border: 0; border-radius: 50%; padding: 0; background: transparent; color: var(--color-text); cursor: pointer; }
.quotes-view__back:focus-visible { background: var(--color-surface-subtle); color: var(--color-accent); outline: none; }
.quotes-view__feed { min-width: 0; }
.quotes-view__state { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }
.quotes-view__state h2, .quotes-view__state p { margin: 0; }
.quotes-view__state h2 { color: var(--color-text); font-size: 20px; }
.quotes-view__button { display: inline-flex; min-height: 40px; align-items: center; justify-content: center; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); padding: 0 var(--space-5); background: var(--color-accent); color: #fff; cursor: pointer; font: inherit; font-size: 14px; font-weight: 750; }
.quotes-view__button:focus-visible { border-color: var(--color-accent-hover); background: var(--color-accent-hover); outline: 2px solid var(--color-accent); outline-offset: 2px; }
.quotes-view__sentinel { display: flex; min-height: 72px; align-items: center; justify-content: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); color: var(--color-text-secondary); text-align: center; }
.quotes-view__mutation-error { margin: 0; padding: 0 var(--space-5) var(--space-4); color: var(--color-danger); font-size: 13px; text-align: center; }
.quotes-view__skeletons { display: grid; }
.quotes-skeleton { position: relative; display: grid; min-height: 148px; grid-template-columns: 40px minmax(0, 1fr); align-content: start; gap: var(--space-3); padding: var(--space-5); border-bottom: 1px solid var(--color-border); }
.quotes-skeleton__avatar, .quotes-skeleton__line { display: block; border-radius: var(--radius-pill); background: var(--color-surface-subtle); }
.quotes-skeleton__avatar { width: 40px; height: 40px; grid-row: span 2; }
.quotes-skeleton__line { height: 12px; align-self: center; }
.quotes-skeleton__line--name { width: min(42%, 180px); }
.quotes-skeleton__line--body { width: 88%; height: 42px; border-radius: var(--radius-sm); }
@media (max-width: 799px) { .quotes-view__button { min-height: 44px; } .quotes-view__header { padding-inline: var(--space-3); } }
</style>
