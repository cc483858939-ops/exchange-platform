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
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRoute } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import UserRow from '../components/users/UserRow.vue';
import {
  useConnectionsSessionStore,
  type ConnectionsMode,
} from '../store/connectionsSession';

const route = useRoute();
const connectionsSession = useConnectionsSessionStore();
const {
  viewerID,
  pendingMutationIDs,
  mutationErrors,
} = storeToRefs(connectionsSession);

const targetID = computed(() => String(route.params.id ?? '').trim());
const numericTargetID = computed(() => {
  const value = Number(targetID.value);
  return Number.isSafeInteger(value) && value > 0 ? value : null;
});
const mode = computed<ConnectionsMode | null>(() => (
  route.name === 'UserFollowers'
    ? 'followers'
    : route.name === 'UserFollowing'
      ? 'following'
      : null
));
const activeTargetSession = computed(() => (
  numericTargetID.value === null
    ? undefined
    : connectionsSession.getTargetSession(numericTargetID.value)
));
const activeModeSession = computed(() => {
  const target = activeTargetSession.value;
  return target && mode.value ? target[mode.value] : undefined;
});
const profile = computed(() => activeTargetSession.value?.profile ?? null);
const profileLoading = computed(() => activeTargetSession.value?.profileLoading ?? false);
const profileError = computed(() => activeTargetSession.value?.profileError ?? '');
const items = computed(() => activeModeSession.value?.items ?? []);
const initialLoading = computed(() => activeModeSession.value?.initialLoading ?? false);
const initialError = computed(() => activeModeSession.value?.initialError ?? '');
const loadingMore = computed(() => activeModeSession.value?.loadingMore ?? false);
const loadMoreError = computed(() => activeModeSession.value?.loadMoreError ?? '');
const hasMore = computed(() => activeModeSession.value?.hasMore ?? false);
const loaded = computed(() => activeModeSession.value?.loaded ?? false);
const stale = computed(() => activeModeSession.value?.stale ?? false);
const revalidating = computed(() => activeModeSession.value?.revalidating ?? false);
const displayName = computed(() => profile.value?.display_name.trim() || profile.value?.username || 'Profile');
const modeLabel = computed(() => mode.value === 'followers' ? 'Followers' : 'Following');
const emptyCopy = computed(() => mode.value === 'followers' ? 'No followers yet.' : 'Not following anyone yet.');
const sentinelRef = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;
let mounted = false;
let entryVersion = 0;
let restoredEntryVersion = -1;

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (
    !mounted
    || numericTargetID.value === null
    || mode.value === null
    || !('IntersectionObserver' in window)
    || !sentinelRef.value
    || !hasMore.value
    || loadingMore.value
    || loadMoreError.value
    || stale.value
    || revalidating.value
  ) return;
  observer = new IntersectionObserver((entries) => {
    if (entries.some(entry => entry.isIntersecting)) {
      void connectionsSession.loadMore(numericTargetID.value!, mode.value!);
    }
  }, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};

const restoreScrollOnce = async () => {
  const capturedEntryVersion = entryVersion;
  const activeSession = activeModeSession.value;
  if (
    !mounted
    || restoredEntryVersion === capturedEntryVersion
    || !activeSession
    || !activeSession.loaded
    || activeSession.initialLoading
  ) return;
  await nextTick();
  if (
    !mounted
    || capturedEntryVersion !== entryVersion
    || restoredEntryVersion === capturedEntryVersion
  ) return;
  if (
    typeof window !== 'undefined'
    && typeof window.scrollTo === 'function'
  ) {
    window.scrollTo({ top: activeSession.scrollY, behavior: 'auto' });
  }
  restoredEntryVersion = capturedEntryVersion;
};

const reload = () => {
  if (numericTargetID.value !== null && mode.value !== null) {
    connectionsSession.reload(numericTargetID.value, mode.value);
  }
};

const loadMore = () => {
  if (numericTargetID.value !== null && mode.value !== null) {
    void connectionsSession.loadMore(numericTargetID.value, mode.value);
  }
};

const toggleFollow = (userID: number) => {
  void connectionsSession.toggleFollow(userID);
};

watch(
  [targetID, mode, viewerID],
  ([nextTargetID, nextMode, nextViewerID], previousValues) => {
    const [previousTargetID, previousMode, previousViewerID] = previousValues ?? [];
    if (
      previousViewerID === nextViewerID
      && previousTargetID
      && previousMode
      && typeof window !== 'undefined'
    ) {
      connectionsSession.saveScroll(previousTargetID, previousMode, window.scrollY);
    }
    entryVersion += 1;
    restoredEntryVersion = -1;
    if (nextViewerID !== null && nextTargetID && nextMode) {
      connectionsSession.activate(Number(nextTargetID), nextMode);
    }
  },
  { immediate: true },
);

watch([targetID, mode, loaded, initialLoading], () => {
  void restoreScrollOnce();
}, { flush: 'post' });

watch([targetID, mode, hasMore, loadingMore, loadMoreError, () => items.value.length, stale, revalidating], () => {
  void updateObserver();
}, { flush: 'post' });

watch([targetID, mode, loaded, stale], ([nextTargetID, nextMode, isLoaded, isStale]) => {
  if (nextTargetID && nextMode && isLoaded && isStale) {
    void connectionsSession.revalidateMode(Number(nextTargetID), nextMode);
  }
}, { flush: 'post' });

onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});

onBeforeUnmount(() => {
  mounted = false;
  if (numericTargetID.value !== null && mode.value !== null && typeof window !== 'undefined') {
    connectionsSession.saveScroll(numericTargetID.value, mode.value, window.scrollY);
  }
  disconnectObserver();
});
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
