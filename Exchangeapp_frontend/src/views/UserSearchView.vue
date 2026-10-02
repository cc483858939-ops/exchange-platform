<template>
  <main class="search-view">
    <header class="search-view__header"><h1>Search</h1></header>
    <div
      ref="searchScrollViewportRef"
      class="search-scroll-viewport"
    >
      <section v-if="!authStore.isAuthenticated" class="search-view__state">
        <p>Log in to search posts and people.</p>
        <RouterLink
          class="search-view__button"
          :to="{ name: 'Login', query: { returnTo: route.fullPath || '/search' } }"
        >
          Log in
        </RouterLink>
      </section>
      <template v-else>
        <div class="search-view__tabs" role="tablist" aria-label="Search type">
          <button
            class="search-view__tab"
            :class="{ 'search-view__tab--active': activeTab === 'posts' }"
            type="button"
            role="tab"
            :aria-selected="activeTab === 'posts'"
            @click="switchTab('posts')"
          >Posts</button>
          <button
            class="search-view__tab"
            :class="{ 'search-view__tab--active': activeTab === 'people' }"
            type="button"
            role="tab"
            :aria-selected="activeTab === 'people'"
            @click="switchTab('people')"
          >People</button>
        </div>
        <form class="search-view__form" role="search" @submit.prevent="submit">
          <label class="search-view__field"><AppIcon name="search" :size="20" /><span class="sr-only">Search {{ activeTab }}</span><input v-model="inputValue" type="search" :aria-label="`Search ${activeTab}`" :placeholder="activeTab === 'posts' ? 'Search posts' : 'Search people'" maxlength="200" /></label>
          <button class="search-view__button" type="submit">Search</button>
          <button v-if="activeQuery" class="search-view__clear" type="button" aria-label="Clear search" @click="clearSearch"><AppIcon name="close" :size="18" /></button>
        </form>

        <template v-if="activeTab === 'posts'">
          <section class="post-search__filters" aria-label="Post search filters">
            <div class="post-search__author-picker">
              <label class="post-search__filter-label" for="post-search-author">Author</label>
              <input
                id="post-search-author"
                v-model="authorSearchInput"
                type="search"
                autocomplete="off"
                placeholder="Find a user"
                aria-label="Filter posts by author"
                :aria-expanded="authorSuggestions.length > 0"
                aria-controls="post-search-author-suggestions"
                @input="handleAuthorInput"
                @keydown.esc="clearAuthorSuggestions"
              />
              <span v-if="selectedAuthor" class="post-search__selected-author">
                {{ selectedAuthorLabel }}
                <button type="button" aria-label="Clear author filter" @click="clearAuthor">×</button>
              </span>
              <ul v-if="authorSuggestions.length > 0" id="post-search-author-suggestions" class="post-search__suggestions" role="listbox" aria-label="Author suggestions">
                <li v-for="item in authorSuggestions" :key="item.user.id">
                  <button type="button" role="option" @click="selectAuthor(item.user)">
                    <span>@{{ item.user.username }}</span>
                    <small>{{ item.user.display_name }}</small>
                  </button>
                </li>
              </ul>
              <small v-if="authorSuggestionLoading" class="post-search__hint" aria-live="polite">Finding people…</small>
            </div>
            <label class="post-search__filter-label" for="post-search-time">Time</label>
            <select id="post-search-time" v-model="timePreset" aria-label="Filter posts by time">
              <option value="any">Any time</option>
              <option value="24h">Past 24 hours</option>
              <option value="7d">Past 7 days</option>
              <option value="30d">Past 30 days</option>
              <option value="custom">Custom</option>
            </select>
            <div v-if="timePreset === 'custom'" class="post-search__custom-time">
              <label>From <input v-model="customFromLocal" type="datetime-local" /></label>
              <label>To <input v-model="customToLocal" type="datetime-local" /></label>
            </div>
            <p v-if="postCriteriaError" class="post-search__validation" role="alert">{{ postCriteriaError }}</p>
          </section>

          <section v-if="!postCriteria.query" class="search-view__state">Search posts by text.</section>
          <section v-else-if="postQueryError" class="search-view__state search-view__state--error" role="alert">{{ postQueryError }}</section>
          <section v-else-if="postInitialLoading" class="search-view__state" aria-live="polite">Searching posts…</section>
          <section v-else-if="postInitialError" class="search-view__state search-view__state--error" role="alert"><p>{{ postInitialError }}</p><button class="search-view__button" type="button" @click="reload">Retry</button></section>
          <section v-else-if="postItems.length === 0" class="search-view__state">No posts found for “{{ postCriteria.query }}”.</section>
          <section v-else class="post-search__results" aria-label="Post search results">
            <header class="search-view__results-heading"><h2>Posts</h2></header>
            <template v-for="post in postItems" :key="post.id">
              <PostCard
                :post="post"
                :track-view="false"
                :like-pending="likePendingPostIDs.has(post.id)"
                :repost-pending="repostPendingPostIDs.has(post.id)"
                :bookmark-pending="bookmarkPendingPostIDs.has(post.id)"
                :requires-auth-for-actions="true"
                @toggle-like="toggleLike"
                @toggle-repost="toggleRepost"
                @toggle-bookmark="toggleBookmark"
              />
              <p v-if="postMutationErrors.get(post.id)" class="post-search__mutation-error" role="alert">{{ postMutationErrors.get(post.id) }}</p>
            </template>
            <div ref="sentinelRef" class="search-view__sentinel" aria-hidden="true"></div>
            <div v-if="postLoadingMore" class="search-view__more" aria-live="polite">Loading more…</div>
            <div v-else-if="postLoadMoreError" class="search-view__more search-view__more--error" role="alert"><span>{{ postLoadMoreError }}</span><button class="search-view__button" type="button" @click="loadMore">Retry</button></div>
            <div v-else-if="postHasMore" class="search-view__more"><button class="search-view__button" type="button" @click="loadMore">Load more</button></div>
          </section>
        </template>

        <template v-else>
          <section v-if="!peopleQuery" class="search-view__state">Search for people by name or @username.</section>
          <section v-else-if="peopleInitialLoading" class="search-view__state" aria-live="polite">Searching people…</section>
          <section v-else-if="peopleInitialError" class="search-view__state search-view__state--error" role="alert"><p>{{ peopleInitialError }}</p><button class="search-view__button" type="button" @click="reload">Retry</button></section>
          <section v-else-if="peopleItems.length === 0" class="search-view__state">No people found for “{{ peopleQuery }}”.</section>
          <section v-else class="search-view__results" aria-label="People search results">
            <header class="search-view__results-heading"><h2>People</h2></header>
            <UserRow v-for="item in peopleItems" :key="item.user.id" :item="item" :pending="pendingMutationIDs.has(item.user.id)" :error="followMutationErrors.get(item.user.id)" :is-self="viewerID === item.user.id" @toggle-follow="toggleFollow" />
            <div ref="sentinelRef" class="search-view__sentinel" aria-hidden="true"></div>
            <div v-if="peopleLoadingMore" class="search-view__more" aria-live="polite">Loading more…</div>
            <div v-else-if="peopleLoadMoreError" class="search-view__more search-view__more--error" role="alert"><span>{{ peopleLoadMoreError }}</span><button class="search-view__button" type="button" @click="loadMore">Retry</button></div>
            <div v-else-if="peopleHasMore" class="search-view__more"><button class="search-view__button" type="button" @click="loadMore">Load more</button></div>
          </section>
        </template>
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
import PostCard from '../components/feed/PostCard.vue';
import { useAuthStore } from '../store/auth';
import { normalizeSearchQuery, useSearchSessionStore } from '../store/searchSession';
import { postSearchQueryError, usePostSearchSessionStore, type PostSearchCriteriaV1 } from '../store/postSearchSession';
import { getUser, searchUsers, type UserConnectionItem } from '../services/userService';
import type { PublicAuthor } from '../types/User';

const SEARCH_RESELECT_TOP_THRESHOLD_PX = 8;

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const searchSession = useSearchSessionStore();
const postSearchSession = usePostSearchSessionStore();
const {
  viewerID,
  inputValue,
  items: peopleItems,
  loaded: peopleLoaded,
  initialLoading: peopleInitialLoading,
  initialError: peopleInitialError,
  hasMore: peopleHasMore,
  loadingMore: peopleLoadingMore,
  loadMoreError: peopleLoadMoreError,
  scrollTop: peopleScrollTop,
  searchReselectVersion: peopleReselectVersion,
  pendingMutationIDs,
  mutationErrors: followMutationErrors,
} = storeToRefs(searchSession);
const {
  criteria: postCriteria,
  items: postItems,
  loaded: postLoaded,
  initialLoading: postInitialLoading,
  initialError: postInitialError,
  hasMore: postHasMore,
  loadingMore: postLoadingMore,
  loadMoreError: postLoadMoreError,
  scrollTop: postScrollTop,
  searchReselectVersion: postReselectVersion,
  likePendingPostIDs,
  repostPendingPostIDs,
  bookmarkPendingPostIDs,
  mutationErrors: postMutationErrors,
} = storeToRefs(postSearchSession);
const sentinelRef = ref<HTMLElement | null>(null);
const searchScrollViewportRef = ref<HTMLElement | null>(null);
const authorSearchInput = ref('');
const authorSuggestions = ref<UserConnectionItem[]>([]);
const selectedAuthor = ref<PublicAuthor | null>(null);
const authorIDDraft = ref<number | null>(null);
const authorSuggestionLoading = ref(false);
const timePreset = ref<'any' | '24h' | '7d' | '30d' | 'custom'>('any');
const customFromLocal = ref('');
const customToLocal = ref('');
let observer: IntersectionObserver | null = null;
let mounted = false;
const searchViewActive = ref(true);
let resumeOnActivation = false;
let searchEntryVersion = 0;
let restoredEntryVersion = -1;
let authorSuggestionTimer: number | null = null;
let authorSuggestionVersion = 0;
let authorResolutionVersion = 0;

const beginSearchRestoreEpoch = () => {
  searchEntryVersion += 1;
  restoredEntryVersion = -1;
};

const activeTab = computed(() => route.query.tab === 'people' ? 'people' : 'posts');
const rawRouteQuery = computed(() => typeof route.query.q === 'string' ? route.query.q : '');
const routeQuery = computed(() => normalizeSearchQuery(rawRouteQuery.value));
const postRouteQuery = computed(() => rawRouteQuery.value.trim());
const peopleQuery = storeToRefs(searchSession).query;
const activeQuery = computed(() => activeTab.value === 'posts' ? postCriteria.value.query : peopleQuery.value);
const activeLoaded = computed(() => activeTab.value === 'posts' ? postLoaded.value : peopleLoaded.value);
const activeInitialLoading = computed(() => activeTab.value === 'posts' ? postInitialLoading.value : peopleInitialLoading.value);
const activeScrollTop = computed(() => activeTab.value === 'posts' ? postScrollTop.value : peopleScrollTop.value);
const activeHasMore = computed(() => activeTab.value === 'posts' ? postHasMore.value : peopleHasMore.value);
const activeLoadingMore = computed(() => activeTab.value === 'posts' ? postLoadingMore.value : peopleLoadingMore.value);
const activeLoadMoreError = computed(() => activeTab.value === 'posts' ? postLoadMoreError.value : peopleLoadMoreError.value);
const activeItemsLength = computed(() => activeTab.value === 'posts' ? postItems.value.length : peopleItems.value.length);
const postQueryError = computed(() => postSearchQueryError(inputValue.value));
const selectedAuthorLabel = computed(() => selectedAuthor.value?.username === 'Author unavailable'
  ? 'Author unavailable'
  : selectedAuthor.value ? `@${selectedAuthor.value.username}` : '');
const postCriteriaError = computed(() => (timePreset.value === 'custom' ? validateCustomTime() : '') || postQueryError.value);
const currentRouteStateKey = computed(() => JSON.stringify([
  route.name,
  activeTab.value,
  rawRouteQuery.value,
  route.query.author ?? '',
  route.query.time ?? '',
  route.query.from ?? '',
  route.query.to ?? '',
]));
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

  if (activeTab.value === 'posts') {
    postSearchSession.saveScrollTop(viewport.scrollTop);
  } else {
    searchSession.saveScrollTop(viewport.scrollTop);
  }
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
    || !activeQuery.value
    || !activeHasMore.value
    || activeLoadingMore.value
    || activeLoadMoreError.value
    || !searchScrollViewportRef.value
    || !sentinelRef.value
    || typeof IntersectionObserver === 'undefined'
  ) return;
  const root = searchScrollViewportRef.value;
  observer = new IntersectionObserver((entries) => {
    if (searchViewActive.value && entries.some((entry) => entry.isIntersecting)) {
      void loadMore();
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
    || !activeQuery.value
    || !activeLoaded.value
    || activeInitialLoading.value
  ) return;
  await nextTick();
  if (
    !mounted
    || !searchViewActive.value
    || route.name !== 'UserSearch'
    || entryVersion !== searchEntryVersion
    || restoredEntryVersion === entryVersion
    || !activeQuery.value
    || !activeLoaded.value
    || activeInitialLoading.value
  ) return;
  const viewport = searchScrollViewportRef.value;
  if (!viewport) {
    return;
  }
  viewport.scrollTop = activeScrollTop.value;
  restoredEntryVersion = entryVersion;
};
const resetViewportScroll = () => {
  const viewport = searchScrollViewportRef.value;
  if (viewport) {
    viewport.scrollTop = 0;
  }
};
const prefersReducedMotion = () => typeof window !== 'undefined'
  && typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
const handleSearchReselect = () => {
  if (
    !searchViewActive.value
    || route.name !== 'UserSearch'
  ) {
    return;
  }

  const viewport = searchScrollViewportRef.value;
  if (!viewport || viewport.scrollTop <= SEARCH_RESELECT_TOP_THRESHOLD_PX) {
    return;
  }

  viewport.scrollTo({
    top: 0,
    behavior: prefersReducedMotion() ? 'auto' : 'smooth',
  });
  if (activeTab.value === 'posts') {
    postSearchSession.saveScrollTop(0);
  } else {
    searchSession.saveScrollTop(0);
  }
};
const routeAuthorID = () => {
  const value = typeof route.query.author === 'string' ? route.query.author : '';
  const id = Number(value);
  return value !== '' && Number.isSafeInteger(id) && id > 0 ? id : null;
};
const toLocalDateTime = (value: unknown) => {
  if (typeof value !== 'string') return '';
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return '';
  const local = new Date(timestamp - new Date(timestamp).getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
};
const fromLocalDateTime = (value: string) => {
  if (!value) return null;
  const timestamp = new Date(value).getTime();
  return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : undefined;
};
const readRoutePostTime = (): PostSearchCriteriaV1['time'] => {
  const value = typeof route.query.time === 'string' ? route.query.time : 'any';
  if (value === '24h' || value === '7d' || value === '30d') return { kind: 'relative', duration: value };
  if (value === 'custom') {
    return {
      kind: 'custom',
      from: typeof route.query.from === 'string' ? route.query.from : null,
      to: typeof route.query.to === 'string' ? route.query.to : null,
    };
  }
  return { kind: 'any' };
};
const authorFromUser = (user: PublicAuthor): PublicAuthor => ({
  id: user.id,
  username: user.username,
  display_name: user.display_name,
  avatar_url: user.avatar_url,
});
const resolveRouteAuthor = async (authorID: number) => {
  if (selectedAuthor.value?.id === authorID && authorIDDraft.value === authorID) return;
  const version = ++authorResolutionVersion;
  authorIDDraft.value = authorID;
  selectedAuthor.value = null;
  authorSearchInput.value = '';
  try {
    const author = await getUser(authorID);
    if (version !== authorResolutionVersion || routeAuthorID() !== authorID) return;
    selectedAuthor.value = authorFromUser(author);
    authorSearchInput.value = `@${author.username}`;
  } catch {
    if (version !== authorResolutionVersion || routeAuthorID() !== authorID) return;
    selectedAuthor.value = { id: authorID, username: 'Author unavailable', display_name: 'Author unavailable', avatar_url: '' };
    authorSearchInput.value = 'Author unavailable';
  }
};
const activateRouteCriteria = () => {
  if (route.name !== 'UserSearch' || !searchViewActive.value) return;
  beginSearchRestoreEpoch();
  if (activeTab.value === 'people') {
    const nextQuery = routeQuery.value;
    inputValue.value = nextQuery;
    searchSession.activateQuery(nextQuery);
  } else {
    inputValue.value = postRouteQuery.value;
    const time = readRoutePostTime();
    timePreset.value = time.kind === 'any' ? 'any' : time.kind === 'relative' ? time.duration : 'custom';
    customFromLocal.value = time.kind === 'custom' ? toLocalDateTime(time.from) : '';
    customToLocal.value = time.kind === 'custom' ? toLocalDateTime(time.to) : '';
    const authorID = routeAuthorID();
    if (authorID === null) {
      authorResolutionVersion += 1;
      authorIDDraft.value = null;
      selectedAuthor.value = null;
      authorSearchInput.value = '';
    } else {
      void resolveRouteAuthor(authorID);
    }
    postSearchSession.activateCriteria({
      query: postRouteQuery.value,
      authorId: authorID,
      time,
      sort: 'latest',
    });
  }
  resetViewportScroll();
  void restoreScrollOnce();
  void updateObserver();
};
onBeforeRouteLeave(() => {
  saveCurrentScroll();
});
const submit = async () => {
  if (activeTab.value === 'people') {
    const submitted = normalizeSearchQuery(inputValue.value);
    if (!submitted) { await clearSearch(); return; }
    inputValue.value = submitted;
    if (submitted === peopleQuery.value && activeTab.value === 'people') { reload(); return; }
    await router.push({ name: 'UserSearch', query: { ...route.query, tab: 'people', q: submitted } });
    return;
  }
  const submitted = inputValue.value.trim();
  if (!submitted) { await clearSearch(); return; }
  inputValue.value = submitted;
  if (postSearchQueryError(submitted)) return;
  if (validateCustomTime()) return;
  const query: Record<string, string | string[] | null | undefined> = { ...route.query, tab: 'posts', q: submitted };
  delete query.author;
  delete query.from;
  delete query.to;
  if (authorIDDraft.value !== null) query.author = String(authorIDDraft.value);
  if (timePreset.value === 'any') {
    delete query.time;
  } else if (timePreset.value === 'custom') {
    query.time = 'custom';
    const from = fromLocalDateTime(customFromLocal.value);
    const to = fromLocalDateTime(customToLocal.value);
    if (from) query.from = from;
    if (to) query.to = to;
  } else {
    query.time = timePreset.value;
  }
  const sameRoute = currentRouteStateKey.value === JSON.stringify([
    'UserSearch', 'posts', submitted, query.author ?? '', query.time ?? '', query.from ?? '', query.to ?? '',
  ]);
  if (sameRoute) {
    postSearchSession.reload();
  } else {
    await router.push({ name: 'UserSearch', query });
  }
};
const clearSearch = async () => {
  inputValue.value = '';
  const nextQuery = { ...route.query };
  delete nextQuery.q;
  await router.push({ name: 'UserSearch', query: { ...nextQuery, tab: activeTab.value } });
};
const reload = () => {
  resetViewportScroll();
  if (activeTab.value === 'posts') postSearchSession.reload();
  else searchSession.reload();
};
const loadMore = () => { if (activeTab.value === 'posts') void postSearchSession.loadMore(); else void searchSession.loadMore(); };
const toggleFollow = (userID: number) => { void searchSession.toggleFollow(userID); };
const toggleLike = (postID: number) => { void postSearchSession.toggleLike(postID); };
const toggleRepost = (postID: number) => { void postSearchSession.toggleRepost(postID); };
const toggleBookmark = (postID: number) => { void postSearchSession.toggleBookmark(postID); };
const switchTab = async (tab: 'posts' | 'people') => {
  if (tab === activeTab.value) return;
  saveCurrentScroll();
  const query = { ...route.query, tab };
  await router.push({ name: 'UserSearch', query });
};
const validateCustomTime = () => {
  if (timePreset.value !== 'custom') return '';
  const from = fromLocalDateTime(customFromLocal.value);
  const to = fromLocalDateTime(customToLocal.value);
  if (from === undefined || to === undefined) return 'Enter valid custom dates.';
  if (from && to && Date.parse(from) >= Date.parse(to)) return 'The start date must be before the end date.';
  return '';
};
const clearAuthorSuggestions = () => {
  authorSuggestionVersion += 1;
  if (authorSuggestionTimer !== null) window.clearTimeout(authorSuggestionTimer);
  authorSuggestionTimer = null;
  authorSuggestions.value = [];
  authorSuggestionLoading.value = false;
};
const handleAuthorInput = () => {
  const value = authorSearchInput.value.trim();
  if (selectedAuthor.value && value === selectedAuthorLabel.value) return;
  selectedAuthor.value = null;
  authorIDDraft.value = null;
  clearAuthorSuggestions();
  if (Array.from(value).length < 2) return;
  const version = ++authorSuggestionVersion;
  authorSuggestionLoading.value = true;
  authorSuggestionTimer = window.setTimeout(async () => {
    authorSuggestionTimer = null;
    try {
      const page = await searchUsers({ q: value, limit: 8, offset: 0 });
      if (version !== authorSuggestionVersion || value !== authorSearchInput.value.trim()) return;
      authorSuggestions.value = (page.items ?? []).slice(0, 8);
    } catch {
      if (version === authorSuggestionVersion) authorSuggestions.value = [];
    } finally {
      if (version === authorSuggestionVersion) authorSuggestionLoading.value = false;
    }
  }, 250);
};
const selectAuthor = (author: PublicAuthor) => {
  clearAuthorSuggestions();
  authorIDDraft.value = author.id;
  selectedAuthor.value = authorFromUser(author);
  authorSearchInput.value = `@${author.username}`;
};
const clearAuthor = () => {
  authorResolutionVersion += 1;
  authorIDDraft.value = null;
  selectedAuthor.value = null;
  authorSearchInput.value = '';
  clearAuthorSuggestions();
};

watch(currentViewerID, (nextID) => {
  beginSearchRestoreEpoch();
  searchSession.setViewer(nextID);
  postSearchSession.setViewer(nextID);
  resetViewportScroll();
}, { immediate: true });
watch(currentRouteStateKey, activateRouteCriteria, { immediate: true });
watch(
  () => peopleReselectVersion.value,
  () => {
    if (activeTab.value === 'people') handleSearchReselect();
  },
);
watch(
  () => postReselectVersion.value,
  () => { if (activeTab.value === 'posts') handleSearchReselect(); },
);
watch([activeLoaded, activeInitialLoading], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([activeHasMore, activeLoadingMore, activeLoadMoreError, activeItemsLength], () => { void updateObserver(); }, { flush: 'post' });
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

  activateRouteCriteria();

  void nextTick(() => {
    if (
      !mounted
      || !searchViewActive.value
      || route.name !== 'UserSearch'
    ) {
      return;
    }

    void restoreScrollOnce();
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
  clearAuthorSuggestions();
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
.search-view__tabs { display: flex; gap: var(--space-5); padding: 0 var(--space-5); border-bottom: 1px solid var(--color-border); }
.search-view__tab { position: relative; min-height: 46px; padding: 0 2px; border: 0; background: transparent; color: var(--color-text-secondary); font: inherit; font-size: 14px; font-weight: 700; cursor: pointer; }
.search-view__tab--active { color: var(--color-accent); }
.search-view__tab--active::after { position: absolute; right: 0; bottom: -1px; left: 0; height: 3px; border-radius: 3px 3px 0 0; background: var(--color-accent); content: ''; }
.search-view__form { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-2); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.search-view__field { display: flex; min-width: 0; align-items: center; gap: var(--space-2); min-height: 42px; padding: 0 var(--space-3); border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); color: var(--color-text-secondary); background: var(--color-surface-subtle); }
.search-view__field:focus-within { border-color: var(--color-accent); color: var(--color-accent); }
.search-view__field input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--color-text); font: inherit; font-size: 15px; }
.search-view__button, .search-view__clear { min-height: 42px; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); background: var(--color-accent); color: #fff; font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.search-view__button { padding: 0 var(--space-4); text-decoration: none; }.search-view__clear { display: grid; width: 42px; place-items: center; border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text-secondary); }
.search-view__state, .search-view__more { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }.search-view__state p { margin: 0; }.search-view__state--error, .search-view__more--error { color: var(--color-danger); }
.search-view__results-heading { padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }.search-view__results-heading h2 { margin: 0; font-size: 16px; }.search-view__sentinel { min-height: 1px; }.search-view__more { min-height: 64px; padding: var(--space-4) var(--space-5); border-top: 1px solid var(--color-border); }
.post-search__filters { display: grid; grid-template-columns: minmax(180px, 1fr) minmax(150px, 220px); align-items: end; gap: var(--space-3); padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.post-search__author-picker { position: relative; display: grid; gap: var(--space-2); }
.post-search__filter-label, .post-search__custom-time label { display: grid; gap: 6px; color: var(--color-text-secondary); font-size: 12px; font-weight: 700; }
.post-search__author-picker > input, .post-search__filters select, .post-search__custom-time input { min-width: 0; min-height: 40px; padding: 0 var(--space-3); border: 1px solid var(--color-border-strong); border-radius: var(--radius-md); background: var(--color-surface); color: var(--color-text); font: inherit; }
.post-search__selected-author { display: flex; align-items: center; gap: var(--space-2); color: var(--color-text); font-size: 13px; }
.post-search__selected-author button { width: 24px; height: 24px; border: 0; border-radius: 50%; background: var(--color-surface-subtle); color: var(--color-text-secondary); cursor: pointer; }
.post-search__suggestions { position: absolute; z-index: 10; top: 100%; right: 0; left: 0; display: grid; gap: 2px; max-height: 240px; margin: 4px 0 0; padding: 4px; overflow: auto; border: 1px solid var(--color-border); border-radius: var(--radius-md); background: var(--color-surface); box-shadow: var(--shadow-popover, 0 8px 24px rgb(0 0 0 / 14%)); list-style: none; }
.post-search__suggestions button { display: grid; width: 100%; gap: 3px; padding: 9px 10px; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--color-text); text-align: left; cursor: pointer; }
.post-search__suggestions button:hover, .post-search__suggestions button:focus-visible { background: var(--color-surface-subtle); }
.post-search__suggestions small, .post-search__hint { color: var(--color-text-secondary); font-size: 12px; }
.post-search__custom-time { display: grid; grid-column: 1 / -1; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-3); }
.post-search__validation, .post-search__mutation-error { grid-column: 1 / -1; margin: 0; color: var(--color-danger); font-size: 13px; }
.post-search__results { border-bottom: 1px solid var(--color-border); }
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

@media (max-width: 520px) { .post-search__filters { grid-template-columns: minmax(0, 1fr); } .post-search__custom-time { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 420px) { .search-view__header, .search-view__tabs, .search-view__form, .search-view__results-heading, .post-search__filters { padding-inline: var(--space-4); } .search-view__form { grid-template-columns: minmax(0, 1fr) auto; } .search-view__clear { grid-column: 1 / -1; justify-self: end; width: auto; padding-inline: var(--space-3); } .search-view__button { padding-inline: var(--space-3); } }
</style>
