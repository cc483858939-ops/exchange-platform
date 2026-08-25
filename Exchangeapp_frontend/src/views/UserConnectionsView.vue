<template>
  <main class="connections-view">
    <header class="connections-header">
      <RouterLink
        v-if="profile"
        class="connections-header__identity"
        :to="{ name: 'UserProfile', params: { id: profile.id } }"
        :aria-label="`Back to ${displayName}'s profile`"
      >
        <AppIcon name="arrow-left" :size="22" />
        <span class="connections-header__copy">
          <strong>{{ displayName }}</strong>
          <span>@{{ profile.username }}</span>
        </span>
      </RouterLink>
      <div v-else-if="profileLoading" class="connections-header__skeleton" aria-label="Loading profile"></div>
      <p v-else class="connections-header__error">{{ profileError || 'Profile could not be loaded.' }}</p>
    </header>

    <nav v-if="profile" class="connections-tabs" aria-label="Connections">
      <RouterLink :to="{ name: 'UserFollowing', params: { id: profile.id } }">Following</RouterLink>
      <RouterLink :to="{ name: 'UserFollowers', params: { id: profile.id } }">Followers</RouterLink>
    </nav>

    <section v-if="profile && initialLoading" class="connections-state" aria-live="polite">Loading {{ modeLabel.toLowerCase() }}?</section>
    <section v-else-if="profile && initialError" class="connections-state connections-state--error" role="alert">
      <p>{{ initialError }}</p>
      <button class="connections-button" type="button" @click="reload">Retry</button>
    </section>
    <section v-else-if="profile && items.length === 0" class="connections-state">{{ emptyCopy }}</section>
    <section v-else-if="profile" class="connections-list" aria-label="User connections">
      <UserRow
        v-for="item in items"
        :key="item.user.id"
        :item="item"
        :pending="pendingMutationIDs.has(item.user.id)"
        :error="mutationErrors.get(item.user.id)"
        :is-self="item.user.id === viewerID"
        @toggle-follow="toggleFollow"
      />
      <div ref="sentinelRef" class="connections-sentinel" aria-hidden="true"></div>
      <div v-if="loadingMore" class="connections-more" aria-live="polite">Loading more?</div>
      <div v-else-if="loadMoreError" class="connections-more connections-more--error" role="alert">
        <span>{{ loadMoreError }}</span>
        <button class="connections-button" type="button" @click="loadMore">Retry</button>
      </div>
      <div v-else-if="hasMore" class="connections-more">
        <button class="connections-button" type="button" @click="loadMore">Load more</button>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import UserRow from '../components/users/UserRow.vue';
import { followUser, getUser, getUserFollowers, getUserFollowing, unfollowUser, type UserConnectionItem, type UserConnectionPage } from '../services/userService';
import { useAuthStore } from '../store/auth';
import { syncExternalFollowState } from '../store/sessionSync';
import type { PublicUser } from '../types/User';

const pageSize = 20;
const route = useRoute();
const authStore = useAuthStore();
const profile = ref<PublicUser | null>(null);
const profileLoading = ref(false);
const profileError = ref('');
const items = ref<UserConnectionItem[]>([]);
const initialLoading = ref(false);
const initialError = ref('');
const loadingMore = ref(false);
const loadMoreError = ref('');
const hasMore = ref(false);
const nextOffset = ref(0);
const loadedUserIDs = new Set<number>();
const pendingMutationIDs = ref(new Set<number>());
const mutationErrors = ref(new Map<number, string>());
const sentinelRef = ref<HTMLElement | null>(null);
let pageGeneration = 0;
let paginationRequestVersion = 0;
let mutationSequence = 0;
const mutationVersions = new Map<number, number>();
let observer: IntersectionObserver | null = null;

const targetID = computed(() => String(route.params.id ?? '').trim());
const mode = computed<'followers' | 'following' | null>(() => route.name === 'UserFollowers' ? 'followers' : route.name === 'UserFollowing' ? 'following' : null);
const viewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && id > 0 ? id : null;
});
const displayName = computed(() => profile.value?.display_name.trim() || profile.value?.username || 'Profile');
const modeLabel = computed(() => mode.value === 'followers' ? 'Followers' : 'Following');
const emptyCopy = computed(() => mode.value === 'followers' ? 'No followers yet.' : 'Not following anyone yet.');
const routeIsCurrent = () => mode.value !== null;

const disconnectObserver = () => { observer?.disconnect(); observer = null; };
const invalidatePage = () => {
  pageGeneration += 1;
  paginationRequestVersion += 1;
  mutationSequence += 1;
  disconnectObserver();
  profile.value = null;
  profileLoading.value = false;
  profileError.value = '';
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
const requestPage = (offset: number) => mode.value === 'followers'
  ? getUserFollowers(targetID.value, { limit: pageSize, offset })
  : getUserFollowing(targetID.value, { limit: pageSize, offset });
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
const current = (generation: number, requestVersion: number, capturedMode: typeof mode.value, capturedViewer: number) =>
  pageGeneration === generation && paginationRequestVersion === requestVersion && mode.value === capturedMode && viewerID.value === capturedViewer && routeIsCurrent();

const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (!routeIsCurrent() || !('IntersectionObserver' in window) || !sentinelRef.value || !hasMore.value || loadingMore.value || loadMoreError.value) return;
  observer = new IntersectionObserver((entries) => { if (entries.some((entry) => entry.isIntersecting)) void loadMore(); }, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};

const loadInitial = async () => {
  if (!routeIsCurrent() || viewerID.value === null || !targetID.value) return;
  const generation = pageGeneration;
  const requestVersion = ++paginationRequestVersion;
  const capturedMode = mode.value;
  const capturedViewer = viewerID.value;
  profileLoading.value = true;
  initialLoading.value = true;
  profileError.value = '';
  initialError.value = '';
  const profileRequest = getUser(targetID.value)
    .then((result) => { if (current(generation, requestVersion, capturedMode, capturedViewer)) profile.value = result; })
    .catch(() => { if (current(generation, requestVersion, capturedMode, capturedViewer)) profileError.value = 'Profile could not be loaded.'; });
  const listRequest = requestPage(0)
    .then((page) => { if (current(generation, requestVersion, capturedMode, capturedViewer)) appendPage(page); })
    .catch(() => { if (current(generation, requestVersion, capturedMode, capturedViewer)) initialError.value = 'Connections could not be loaded.'; });
  await Promise.all([profileRequest, listRequest]);
  if (current(generation, requestVersion, capturedMode, capturedViewer)) {
    profileLoading.value = false;
    initialLoading.value = false;
    void updateObserver();
  }
};
const reload = () => { invalidatePage(); void loadInitial(); };
const loadMore = async () => {
  if (!routeIsCurrent() || viewerID.value === null || !hasMore.value || loadingMore.value || initialLoading.value) return;
  const generation = pageGeneration;
  const requestVersion = ++paginationRequestVersion;
  const offset = nextOffset.value;
  const capturedMode = mode.value;
  const capturedViewer = viewerID.value;
  loadingMore.value = true;
  loadMoreError.value = '';
  try {
    const page = await requestPage(offset);
    if (!current(generation, requestVersion, capturedMode, capturedViewer) || nextOffset.value !== offset) return;
    appendPage(page);
  } catch {
    if (current(generation, requestVersion, capturedMode, capturedViewer)) loadMoreError.value = 'Could not load more users.';
  } finally {
    if (current(generation, requestVersion, capturedMode, capturedViewer)) { loadingMore.value = false; void updateObserver(); }
  }
};
const toggleFollow = async (userID: number) => {
  const index = items.value.findIndex((item) => item.user.id === userID);
  const capturedViewer = viewerID.value;
  if (index < 0 || capturedViewer === null || userID === capturedViewer || pendingMutationIDs.value.has(userID) || mode.value === null) return;
  const previous = items.value[index].following;
  const generation = pageGeneration;
  const capturedTarget = targetID.value;
  const capturedMode = mode.value;
  const version = ++mutationSequence;
  mutationVersions.set(userID, version);
  pendingMutationIDs.value = new Set(pendingMutationIDs.value).add(userID);
  mutationErrors.value = new Map(mutationErrors.value);
  mutationErrors.value.delete(userID);
  items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: !previous } : item);
  const mutationCurrent = () => pageGeneration === generation && targetID.value === capturedTarget && mode.value === capturedMode && viewerID.value === capturedViewer && mutationVersions.get(userID) === version && pendingMutationIDs.value.has(userID) && routeIsCurrent();
  try {
    const response = previous ? await unfollowUser(userID) : await followUser(userID);
    if (!mutationCurrent()) return;
    if (response.user_id !== userID) throw new Error('invalid follow response');
    const ownFollowingRemoval = previous && capturedViewer === Number(capturedTarget) && capturedMode === 'following';
    if (ownFollowingRemoval) {
      items.value = items.value.filter((item) => item.user.id !== userID);
      loadedUserIDs.delete(userID);
      nextOffset.value = Math.max(0, nextOffset.value - 1);
      paginationRequestVersion += 1;
      loadingMore.value = false;
    } else {
      items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: response.following } : item);
    }
    const nextPending = new Set(pendingMutationIDs.value); nextPending.delete(userID); pendingMutationIDs.value = nextPending;
    mutationErrors.value.delete(userID);
    syncExternalFollowState(response);
    void updateObserver();
  } catch {
    if (!mutationCurrent()) return;
    items.value = items.value.map((item) => item.user.id === userID ? { ...item, following: previous } : item);
    const nextPending = new Set(pendingMutationIDs.value); nextPending.delete(userID); pendingMutationIDs.value = nextPending;
    mutationErrors.value = new Map(mutationErrors.value).set(userID, 'Could not update follow status.');
  }
};
watch([targetID, mode, viewerID], () => { invalidatePage(); void loadInitial(); }, { immediate: true });
watch([hasMore, loadingMore, loadMoreError, () => items.value.length], () => { void updateObserver(); }, { flush: 'post' });
onBeforeUnmount(() => { invalidatePage(); });
</script>

<style scoped>
.connections-view { min-height: 100vh; background: var(--color-surface); color: var(--color-text); }
.connections-header { padding: var(--space-5); border-bottom: 1px solid var(--color-border); }
.connections-header__identity { display: flex; align-items: center; gap: var(--space-3); min-width: 0; color: inherit; text-decoration: none; }
.connections-header__identity:focus-visible { outline: 2px solid var(--color-accent); outline-offset: var(--space-2); border-radius: var(--radius-sm); }
.connections-header__identity:hover strong, .connections-header__identity:focus-visible strong { color: var(--color-accent); text-decoration: underline; text-underline-offset: 3px; }
.connections-header__copy { display: grid; min-width: 0; gap: 2px; }
.connections-header__copy strong, .connections-header__copy span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.connections-header__copy strong { font-size: 20px; line-height: 1.25; }
.connections-header__copy span { color: var(--color-text-secondary); font-size: 14px; }
.connections-header__skeleton { width: 160px; height: 42px; border-radius: var(--radius-sm); background: var(--color-surface-subtle); }
.connections-header__error { margin: 0; color: var(--color-danger); }
.connections-tabs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border-bottom: 1px solid var(--color-border); }
.connections-tabs a { padding: var(--space-4) var(--space-3); color: var(--color-text-secondary); font-size: 14px; font-weight: 750; text-align: center; text-decoration: none; }
.connections-tabs a.router-link-active { border-bottom: 2px solid var(--color-accent); color: var(--color-text); }
.connections-state, .connections-more { display: grid; justify-items: center; gap: var(--space-3); padding: 56px var(--space-5); color: var(--color-text-secondary); text-align: center; }
.connections-state--error, .connections-more--error { color: var(--color-danger); }
.connections-state--error p { margin: 0; }
.connections-sentinel { min-height: 1px; }
.connections-more { min-height: 64px; padding: var(--space-4) var(--space-5); border-top: 1px solid var(--color-border); }
.connections-button { min-height: 36px; border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); padding: 0 var(--space-4); background: var(--color-surface); color: var(--color-text); font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.connections-button:hover, .connections-button:focus-visible { border-color: var(--color-accent); color: var(--color-accent); }
@media (max-width: 380px) { .connections-header { padding-inline: var(--space-4); } .connections-tabs a { font-size: 13px; } }
</style>
