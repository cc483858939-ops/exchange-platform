<template>
  <main class="search-view">
    <header class="search-view__header"><h1>Search</h1></header>
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
      <section v-else-if="initialLoading" class="search-view__state" aria-live="polite">Searching people?</section>
      <section v-else-if="initialError" class="search-view__state search-view__state--error" role="alert"><p>{{ initialError }}</p><button class="search-view__button" type="button" @click="reload">Retry</button></section>
      <section v-else-if="items.length === 0" class="search-view__state">No people found for ?{{ query }}?.</section>
      <section v-else class="search-view__results" aria-label="People search results">
        <header class="search-view__results-heading"><h2>People</h2></header>
        <UserRow v-for="item in items" :key="item.user.id" :item="item" :pending="pendingMutationIDs.has(item.user.id)" :error="mutationErrors.get(item.user.id)" :is-self="item.user.id === viewerID" @toggle-follow="toggleFollow" />
        <div ref="sentinelRef" class="search-view__sentinel" aria-hidden="true"></div>
        <div v-if="loadingMore" class="search-view__more" aria-live="polite">Loading more?</div>
        <div v-else-if="loadMoreError" class="search-view__more search-view__more--error" role="alert"><span>{{ loadMoreError }}</span><button class="search-view__button" type="button" @click="loadMore">Retry</button></div>
        <div v-else-if="hasMore" class="search-view__more"><button class="search-view__button" type="button" @click="loadMore">Load more</button></div>
      </section>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRoute, useRouter } from 'vue-router';
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
let observer: IntersectionObserver | null = null;
let mounted = false;
let searchEntryVersion = 0;
let restoredEntryVersion = -1;

const routeQuery = computed(() => normalizeSearchQuery(typeof route.query.q === 'string' ? route.query.q : ''));
const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const disconnectObserver = () => { observer?.disconnect(); observer = null; };
const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (!query.value || !hasMore.value || loadingMore.value || loadMoreError.value || !sentinelRef.value || !('IntersectionObserver' in window)) return;
  observer = new IntersectionObserver((entries) => { if (entries.some((entry) => entry.isIntersecting)) void searchSession.loadMore(); }, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};
const restoreScrollOnce = async () => {
  const entryVersion = searchEntryVersion;
  if (!mounted || restoredEntryVersion === entryVersion || !query.value || !loaded.value || initialLoading.value) return;
  await nextTick();
  if (!mounted || entryVersion !== searchEntryVersion || restoredEntryVersion === entryVersion) return;
  if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') {
    window.scrollTo({ top: searchSession.scrollY, behavior: 'auto' });
  }
  restoredEntryVersion = entryVersion;
};
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
const reload = () => { searchSession.reload(); };
const loadMore = () => { void searchSession.loadMore(); };
const toggleFollow = (userID: number) => { void searchSession.toggleFollow(userID); };

watch(currentViewerID, (nextID) => {
  searchEntryVersion += 1;
  searchSession.setViewer(nextID);
}, { immediate: true });
watch(routeQuery, (nextQuery) => {
  searchEntryVersion += 1;
  searchSession.activateQuery(nextQuery);
  void restoreScrollOnce();
}, { immediate: true });
watch([loaded, initialLoading, initialError], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([hasMore, loadingMore, loadMoreError, () => items.value.length], () => { void updateObserver(); }, { flush: 'post' });
onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});
onBeforeUnmount(() => {
  mounted = false;
  if (typeof window !== 'undefined') searchSession.saveScroll(window.scrollY);
  disconnectObserver();
});
</script>

<style scoped>
.search-view { min-height: 100vh; background: var(--color-surface); color: var(--color-text); }
.search-view__header { position: sticky; top: 0; z-index: 5; display: flex; align-items: center; min-height: 56px; padding: 0 var(--space-5); border-bottom: 1px solid var(--color-border); background: color-mix(in srgb, var(--color-surface) 94%, transparent); backdrop-filter: blur(10px); }
.search-view__header h1 { margin: 0; font-size: 22px; line-height: 1.2; }
.search-view__form { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-2); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.search-view__field { display: flex; min-width: 0; align-items: center; gap: var(--space-2); min-height: 42px; padding: 0 var(--space-3); border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); color: var(--color-text-secondary); background: var(--color-surface-subtle); }
.search-view__field:focus-within { border-color: var(--color-accent); color: var(--color-accent); }
.search-view__field input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--color-text); font: inherit; font-size: 15px; }
.search-view__button, .search-view__clear { min-height: 42px; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); background: var(--color-accent); color: #fff; font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.search-view__button { padding: 0 var(--space-4); text-decoration: none; }.search-view__clear { display: grid; width: 42px; place-items: center; border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text-secondary); }
.search-view__state, .search-view__more { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }.search-view__state p { margin: 0; }.search-view__state--error, .search-view__more--error { color: var(--color-danger); }
.search-view__results-heading { padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }.search-view__results-heading h2 { margin: 0; font-size: 16px; }.search-view__sentinel { min-height: 1px; }.search-view__more { min-height: 64px; padding: var(--space-4) var(--space-5); border-top: 1px solid var(--color-border); }
@media (max-width: 799px) { .search-view__header { top: var(--mobile-safe-top); } }
@media (max-width: 420px) { .search-view__header, .search-view__form, .search-view__results-heading { padding-inline: var(--space-4); } .search-view__form { grid-template-columns: minmax(0, 1fr) auto; } .search-view__clear { grid-column: 1 / -1; justify-self: end; width: auto; padding-inline: var(--space-3); } .search-view__button { padding-inline: var(--space-3); } }
</style>
