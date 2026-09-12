<template>
  <main class="search-view">
    <header class="search-view__header"><h1>Search</h1></header>
    <div
      ref="searchScrollViewportRef"
      class="search-scroll-viewport"
    >
      <section v-if="!authStore.isAuthenticated" class="search-view__state">
        <p>Log in to search people.</p>
        <RouterLink class="search-view__button" :to="{ name: 'Login' }">Log in</RouterLink>
      </section>
      <template v-else>
        <form class="search-view__form" role="search" @submit.prevent="submit">
          <label class="search-view__field"><AppIcon name="search" :size="20" /><span class="sr-only">Search people</span><input v-model="inputValue" type="search" aria-label="Search people" placeholder="Search people" maxlength="200" /></label>
          <button class="search-view__button" type="submit">Search</button>
          <button v-if="query" class="search-view__clear" type="button" aria-label="Clear search" @click="clearSearch"><AppIcon name="close" :size="18" /></button>
        </form>
        <section v-if="!query" class="search-view__state">Search for people by name or @username.</section>
        <section v-else-if="initialLoading" class="search-view__state" aria-live="polite">Searching people…</section>
        <section v-else-if="initialError" class="search-view__state search-view__state--error" role="alert"><p>{{ initialError }}</p><button class="search-view__button" type="button" @click="reload">Retry</button></section>
        <section v-else-if="items.length === 0" class="search-view__state">No people found for “{{ query }}”.</section>
        <section v-else class="search-view__results" aria-label="People search results">
          <header class="search-view__results-heading"><h2>People</h2></header>
          <UserRow v-for="item in items" :key="item.user.id" :item="item" :pending="pendingMutationIDs.has(item.user.id)" :error="mutationErrors.get(item.user.id)" :is-self="item.user.id === viewerID" @toggle-follow="toggleFollow" />
          <div ref="sentinelRef" class="search-view__sentinel" aria-hidden="true"></div>
          <div v-if="loadingMore" class="search-view__more" aria-live="polite">Loading more…</div>
          <div v-else-if="loadMoreError" class="search-view__more search-view__more--error" role="alert"><span>{{ loadMoreError }}</span><button class="search-view__button" type="button" @click="loadMore">Retry</button></div>
          <div v-else-if="hasMore" class="search-view__more"><button class="search-view__button" type="button" @click="loadMore">Load more</button></div>
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
import UserRow from '../components/users/UserRow.vue';
import { useAuthStore } from '../store/auth';
import { normalizeSearchQuery, useSearchSessionStore } from '../store/searchSession';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const searchSession = useSearchSessionStore();
const {
  viewerID,
  query,
  inputValue,
  items,
  loaded,
  initialLoading,
  initialError,
  nextOffset,
  hasMore,
  loadingMore,
  loadMoreError,
  pendingMutationIDs,
  mutationErrors,
} = storeToRefs(searchSession);
const sentinelRef = ref<HTMLElement | null>(null);
const searchScrollViewportRef = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;
let mounted = false;
const searchViewActive = ref(true);
let resumeOnActivation = false;
let searchEntryVersion = 0;
let restoredEntryVersion = -1;

const routeQuery = computed(() => normalizeSearchQuery(typeof route.query.q === 'string' ? route.query.q : ''));
const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const disconnectObserver = () => { observer?.disconnect(); observer = null; };
const saveCurrentScroll = () => {
  const viewport = searchScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  searchSession.saveScrollTop(viewport.scrollTop);
};
const updateObserver = async () => {
  if (!mounted || !searchViewActive.value) {
    disconnectObserver();
    return;
  }
  await nextTick();
  if (!mounted || !searchViewActive.value) {
    return;
  }
  disconnectObserver();
  if (
    route.name !== 'UserSearch'
    || !query.value
    || !hasMore.value
    || loadingMore.value
    || loadMoreError.value
    || !searchScrollViewportRef.value
    || !sentinelRef.value
    || typeof IntersectionObserver === 'undefined'
  ) return;
  const root = searchScrollViewportRef.value;
  observer = new IntersectionObserver((entries) => {
    if (searchViewActive.value && entries.some((entry) => entry.isIntersecting)) {
      void searchSession.loadMore();
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};
const restoreScrollOnce = async () => {
  const entryVersion = searchEntryVersion;
  if (
    !mounted
    || !searchViewActive.value
    || route.name !== 'UserSearch'
    || restoredEntryVersion === entryVersion
    || !query.value
    || !loaded.value
    || initialLoading.value
  ) return;
  await nextTick();
  if (
    !mounted
    || !searchViewActive.value
    || route.name !== 'UserSearch'
    || entryVersion !== searchEntryVersion
    || restoredEntryVersion === entryVersion
    || !query.value
    || !loaded.value
    || initialLoading.value
  ) return;
  const viewport = searchScrollViewportRef.value;
  if (!viewport) {
    return;
  }
  viewport.scrollTop = searchSession.scrollTop;
  restoredEntryVersion = entryVersion;
};
const resetViewportScroll = () => {
  const viewport = searchScrollViewportRef.value;
  if (viewport) {
    viewport.scrollTop = 0;
  }
};
const syncSearchRouteQuery = (nextQuery: string) => {
  const changed = nextQuery !== query.value;

  if (changed) {
    searchEntryVersion += 1;
    restoredEntryVersion = -1;
  }

  searchSession.activateQuery(nextQuery);

  if (changed) {
    resetViewportScroll();
    void restoreScrollOnce();
  }

  return changed;
};
onBeforeRouteLeave(() => {
  saveCurrentScroll();
});
const submit = async () => {
  const submitted = normalizeSearchQuery(inputValue.value);
  if (!submitted) { await clearSearch(); return; }
  inputValue.value = submitted;
  if (submitted === query.value) { reload(); return; }
  await router.push({ name: 'UserSearch', query: { ...route.query, q: submitted } });
};
const clearSearch = async () => {
  inputValue.value = '';
  const nextQuery = { ...route.query };
  delete nextQuery.q;
  await router.push({ name: 'UserSearch', query: nextQuery });
};
const reload = () => {
  resetViewportScroll();
  searchSession.reload();
};
const loadMore = () => { void searchSession.loadMore(); };
const toggleFollow = (userID: number) => { void searchSession.toggleFollow(userID); };

watch(currentViewerID, (nextID) => {
  searchEntryVersion += 1;
  searchSession.setViewer(nextID);
  resetViewportScroll();
}, { immediate: true });
watch(routeQuery, (nextQuery) => {
  if (!searchViewActive.value || route.name !== 'UserSearch') {
    return;
  }

  syncSearchRouteQuery(nextQuery);
}, { immediate: true });
watch([loaded, initialLoading, initialError], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([hasMore, loadingMore, loadMoreError, () => items.value.length], () => { void updateObserver(); }, { flush: 'post' });
onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});
onDeactivated(() => {
  if (!searchViewActive.value) {
    return;
  }

  searchViewActive.value = false;
  resumeOnActivation = true;
  disconnectObserver();
});
onActivated(() => {
  if (!resumeOnActivation) {
    return;
  }

  resumeOnActivation = false;
  searchViewActive.value = true;

  if (route.name !== 'UserSearch') {
    return;
  }

  const nextQuery = routeQuery.value;
  const returningToSameQuery = nextQuery === query.value;
  if (!returningToSameQuery) {
    syncSearchRouteQuery(nextQuery);
  }

  void nextTick(() => {
    if (
      !mounted
      || !searchViewActive.value
      || route.name !== 'UserSearch'
    ) {
      return;
    }

    if (returningToSameQuery) {
      void restoreScrollOnce();
    }

    void updateObserver();
  });
});
onBeforeUnmount(() => {
  if (searchViewActive.value && route.name === 'UserSearch') {
    saveCurrentScroll();
  }
  mounted = false;
  searchViewActive.value = false;
  resumeOnActivation = false;
  disconnectObserver();
});
</script>

<style scoped>
.search-view {
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

.search-view__header {
  position: relative;
  z-index: 5;
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  min-height: 56px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.search-scroll-viewport {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  overflow-anchor: auto;
  -webkit-overflow-scrolling: touch;
}

.search-view__header h1 { margin: 0; font-size: 22px; line-height: 1.2; }
.search-view__form { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-2); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.search-view__field { display: flex; min-width: 0; align-items: center; gap: var(--space-2); min-height: 42px; padding: 0 var(--space-3); border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); color: var(--color-text-secondary); background: var(--color-surface-subtle); }
.search-view__field:focus-within { border-color: var(--color-accent); color: var(--color-accent); }
.search-view__field input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--color-text); font: inherit; font-size: 15px; }
.search-view__button, .search-view__clear { min-height: 42px; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); background: var(--color-accent); color: #fff; font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.search-view__button { padding: 0 var(--space-4); text-decoration: none; }.search-view__clear { display: grid; width: 42px; place-items: center; border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text-secondary); }
.search-view__state, .search-view__more { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }.search-view__state p { margin: 0; }.search-view__state--error, .search-view__more--error { color: var(--color-danger); }
.search-view__results-heading { padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }.search-view__results-heading h2 { margin: 0; font-size: 16px; }.search-view__sentinel { min-height: 1px; }.search-view__more { min-height: 64px; padding: var(--space-4) var(--space-5); border-top: 1px solid var(--color-border); }
@media (max-width: 799px) {
  .search-view {
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

@media (max-width: 420px) { .search-view__header, .search-view__form, .search-view__results-heading { padding-inline: var(--space-4); } .search-view__form { grid-template-columns: minmax(0, 1fr) auto; } .search-view__clear { grid-column: 1 / -1; justify-self: end; width: auto; padding-inline: var(--space-3); } .search-view__button { padding-inline: var(--space-3); } }
</style>
