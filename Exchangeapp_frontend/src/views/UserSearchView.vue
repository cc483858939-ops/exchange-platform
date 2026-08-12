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
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import UserRow from '../components/users/UserRow.vue';
import { followUser, searchUsers, unfollowUser, type UserConnectionItem, type UserConnectionPage } from '../services/userService';
import { useAuthStore } from '../store/auth';

const pageSize = 20;
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const inputValue = ref('');
const items = ref<UserConnectionItem[]>([]);
const initialLoading = ref(false);
const initialError = ref('');
const loadingMore = ref(false);
const loadMoreError = ref('');
const hasMore = ref(false);
const nextOffset = ref(0);
const sentinelRef = ref<HTMLElement | null>(null);
const pendingMutationIDs = ref(new Set<number>());
const mutationErrors = ref(new Map<number, string>());
const loadedUserIDs = new Set<number>();
let pageGeneration = 0;
let paginationRequestVersion = 0;
let mutationSequence = 0;
const mutationVersions = new Map<number, number>();
let observer: IntersectionObserver | null = null;

const normalize = (value: string) => {
  let normalized = value.trim();
  if (normalized.startsWith('@')) normalized = normalized.slice(1).trim();
  return normalized;
};
const query = computed(() => normalize(typeof route.query.q === 'string' ? route.query.q : ''));
const viewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const viewerKey = computed(() => `${authStore.token ?? ''}:${viewerID.value ?? ''}`);
const disconnectObserver = () => { observer?.disconnect(); observer = null; };
const invalidate = () => {
  pageGeneration += 1;
  paginationRequestVersion += 1;
  mutationSequence += 1;
  disconnectObserver();
  items.value = [];
  initialLoading.value = false;
  initialError.value = '';
  loadingMore.value = false;
  loadMoreError.value = '';
  hasMore.value = false;
  nextOffset.value = 0;
  loadedUserIDs.clear();
  pendingMutationIDs.value = new Set();
  mutationErrors.value = new Map();
  mutationVersions.clear();
};
const appendPage = (page: UserConnectionPage) => {
  nextOffset.value += page.items.length;
  const additions = page.items.filter((item) => {
    if (loadedUserIDs.has(item.user.id)) return false;
    loadedUserIDs.add(item.user.id);
    return true;
  });
  items.value = [...items.value, ...additions];
  hasMore.value = page.has_more;
};
const current = (generation: number, version: number, capturedQuery: string, capturedViewer: string) => generation === pageGeneration && version === paginationRequestVersion && query.value === capturedQuery && viewerKey.value === capturedViewer && authStore.isAuthenticated;
const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (!query.value || !hasMore.value || loadingMore.value || loadMoreError.value || !sentinelRef.value || !('IntersectionObserver' in window)) return;
  observer = new IntersectionObserver((entries) => { if (entries.some((entry) => entry.isIntersecting)) void loadMore(); }, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};
const loadInitial = async () => {
  if (!authStore.isAuthenticated || viewerID.value === null || !query.value) return;
  const generation = pageGeneration;
  const version = ++paginationRequestVersion;
  const capturedQuery = query.value;
  const capturedViewer = viewerKey.value;
  initialLoading.value = true;
  initialError.value = '';
  try {
    const page = await searchUsers({ q: capturedQuery, limit: pageSize, offset: 0 });
    if (!current(generation, version, capturedQuery, capturedViewer)) return;
    appendPage(page);
  } catch {
    if (current(generation, version, capturedQuery, capturedViewer)) initialError.value = 'Could not search people.';
  } finally {
    if (current(generation, version, capturedQuery, capturedViewer)) { initialLoading.value = false; void updateObserver(); }
  }
};
const reload = () => { invalidate(); void loadInitial(); };
const loadMore = async () => {
  if (!authStore.isAuthenticated || viewerID.value === null || !query.value || !hasMore.value || loadingMore.value || initialLoading.value) return;
  const generation = pageGeneration;
  const version = ++paginationRequestVersion;
  const offset = nextOffset.value;
  const capturedQuery = query.value;
  const capturedViewer = viewerKey.value;
  loadingMore.value = true;
  loadMoreError.value = '';
  try {
    const page = await searchUsers({ q: capturedQuery, limit: pageSize, offset });
    if (!current(generation, version, capturedQuery, capturedViewer) || nextOffset.value !== offset) return;
    appendPage(page);
  } catch {
    if (current(generation, version, capturedQuery, capturedViewer)) loadMoreError.value = 'Could not load more users.';
  } finally {
    if (current(generation, version, capturedQuery, capturedViewer)) { loadingMore.value = false; void updateObserver(); }
  }
};
const submit = async () => {
  const submitted = normalize(inputValue.value);
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
const toggleFollow = async (userID: number) => {
  const index = items.value.findIndex((item) => item.user.id === userID);
  const capturedViewerID = viewerID.value;
  if (index < 0 || capturedViewerID === null || userID === capturedViewerID || pendingMutationIDs.value.has(userID)) return;
  const previous = items.value[index].following;
  const generation = pageGeneration;
  const capturedQuery = query.value;
  const capturedViewer = viewerKey.value;
  const version = ++mutationSequence;
  mutationVersions.set(userID, version);
  pendingMutationIDs.value = new Set(pendingMutationIDs.value).add(userID);
  mutationErrors.value = new Map(mutationErrors.value);
  mutationErrors.value.delete(userID);
  items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: !previous } : item);
  const mutationCurrent = () => generation === pageGeneration && query.value === capturedQuery && viewerKey.value === capturedViewer && mutationVersions.get(userID) === version && pendingMutationIDs.value.has(userID);
  try {
    const response = previous ? await unfollowUser(userID) : await followUser(userID);
    if (!mutationCurrent()) return;
    if (response.user_id !== userID) throw new Error('invalid follow response');
    items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: response.following } : item);
    const nextPending = new Set(pendingMutationIDs.value); nextPending.delete(userID); pendingMutationIDs.value = nextPending;
    const nextErrors = new Map(mutationErrors.value); nextErrors.delete(userID); mutationErrors.value = nextErrors;
  } catch {
    if (!mutationCurrent()) return;
    items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: previous } : item);
    const nextPending = new Set(pendingMutationIDs.value); nextPending.delete(userID); pendingMutationIDs.value = nextPending;
    mutationErrors.value = new Map(mutationErrors.value).set(userID, 'Could not update follow status.');
  }
};
watch([query, viewerKey], () => { inputValue.value = query.value; invalidate(); void loadInitial(); }, { immediate: true });
watch([hasMore, loadingMore, loadMoreError, () => items.value.length], () => { void updateObserver(); }, { flush: 'post' });
onBeforeUnmount(invalidate);
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
@media (max-width: 799px) { .search-view__header { top: var(--app-mobile-nav-offset, 0px); } }
@media (max-width: 420px) { .search-view__header, .search-view__form, .search-view__results-heading { padding-inline: var(--space-4); } .search-view__form { grid-template-columns: minmax(0, 1fr) auto; } .search-view__clear { grid-column: 1 / -1; justify-self: end; width: auto; padding-inline: var(--space-3); } .search-view__button { padding-inline: var(--space-3); } }
</style>
