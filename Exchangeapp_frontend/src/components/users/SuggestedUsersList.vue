<template>
  <section
    v-if="items.length > 0"
    class="suggested-users-list"
    :aria-label="title"
    :aria-busy="loading"
  >
    <component :is="titleLevel === 3 ? 'h3' : 'h2'" class="suggested-users-list__heading">
      {{ title }}
    </component>
    <div class="suggested-users-list__items">
      <UserRow
        v-for="item in items"
        :key="item.user.id"
        :item="item"
        :pending="pendingIDs.has(item.user.id)"
        :error="mutationErrors.get(item.user.id)"
        :is-self="item.user.id === viewerId"
        @toggle-follow="toggleFollow"
      />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue';
import { followUser, getSuggestedUsers, unfollowUser, type UserConnectionItem } from '../../services/userService';
import { registerSuggestedUsersSessionSync, syncExternalFollowState } from '../../store/sessionSync';
import { useAuthStore } from '../../store/auth';
import { useHomeTimelineStore } from '../../store/homeTimeline';
import UserRow from './UserRow.vue';

const props = withDefaults(defineProps<{
  viewerId: number | null;
  limit: number;
  title: string;
  titleLevel?: 2 | 3;
  enabled?: boolean;
  revalidateFollowing?: boolean;
}>(), {
  titleLevel: 2,
  enabled: true,
  revalidateFollowing: false,
});

const emit = defineEmits<{ 'availability-change': [hasSuggestions: boolean] }>();
const authStore = useAuthStore();
const items = ref<UserConnectionItem[]>([]);
const loading = ref(false);
const active = ref(false);
const viewerGeneration = ref(0);
const pendingIDs = reactive(new Set<number>());
const mutationErrors = reactive(new Map<number, string>());
const viewerIdentityID = computed(() => {
  if (!authStore.isAuthenticated) return null;
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const sessionVersion = computed(() => (
  Number.isSafeInteger(authStore.sessionVersion) ? authStore.sessionVersion : 0
));
const effectiveLimit = computed(() => (
  Number.isSafeInteger(props.limit) ? Math.min(5, Math.max(1, props.limit)) : 3
));

let requestVersion = 0;
let mutationSequence = 0;
let hasLoaded = false;
let hasActivated = false;
const mutationVersions = new Map<number, number>();

const hasCurrentViewer = (viewerId: number) => (
  authStore.isAuthenticated
  && viewerIdentityID.value === viewerId
  && props.viewerId === viewerId
);

const currentRequest = (
  version: number,
  viewerId: number,
  generation: number,
  capturedSessionVersion: number,
) => (
  active.value
  && props.enabled
  && version === requestVersion
  && generation === viewerGeneration.value
  && capturedSessionVersion === sessionVersion.value
  && hasCurrentViewer(viewerId)
);

const loadSuggestions = async (force = false) => {
  const viewerId = props.viewerId;
  if (
    !active.value
    || !props.enabled
    || viewerId === null
    || !hasCurrentViewer(viewerId)
    || loading.value
    || (hasLoaded && !force)
  ) return;

  const version = ++requestVersion;
  const generation = viewerGeneration.value;
  const capturedSessionVersion = sessionVersion.value;
  loading.value = true;
  try {
    const page = await getSuggestedUsers(effectiveLimit.value);
    if (!currentRequest(version, viewerId, generation, capturedSessionVersion)) return;
    if (!Array.isArray(page.items)) throw new Error('Invalid suggested user response');
    items.value = page.items.slice(0, effectiveLimit.value);
    hasLoaded = true;
    emit('availability-change', items.value.length > 0);
  } catch {
    if (!currentRequest(version, viewerId, generation, capturedSessionVersion)) return;
    items.value = [];
    hasLoaded = true;
    emit('availability-change', false);
  } finally {
    if (version === requestVersion) loading.value = false;
  }
};

const applyExternalFollowStateLocal = (state: { user_id: number; following: boolean }) => {
  const item = items.value.find(candidate => candidate.user.id === state.user_id);
  if (!item) return false;
  item.following = state.following;
  return true;
};
const unregisterSuggestedUsersSync = registerSuggestedUsersSessionSync({ applyExternalFollowStateLocal });

const toggleFollow = async (userId: number) => {
  const item = items.value.find(candidate => candidate.user.id === userId);
  const viewerId = props.viewerId;
  if (!item || viewerId === null || !hasCurrentViewer(viewerId) || pendingIDs.has(userId)) return;

  const capturedViewerGeneration = viewerGeneration.value;
  const capturedSessionVersion = sessionVersion.value;
  const mutationVersion = ++mutationSequence;
  mutationVersions.set(userId, mutationVersion);
  const isCurrentMutation = () => (
    hasCurrentViewer(viewerId)
    && viewerGeneration.value === capturedViewerGeneration
    && sessionVersion.value === capturedSessionVersion
    && mutationVersions.get(userId) === mutationVersion
  );

  pendingIDs.add(userId);
  mutationErrors.delete(userId);
  try {
    const response = item.following
      ? await unfollowUser(userId)
      : await followUser(userId);
    if (!isCurrentMutation()) return;
    if (response.user_id !== userId || typeof response.following !== 'boolean') {
      throw new Error('Invalid follow response');
    }

    applyExternalFollowStateLocal(response);
    syncExternalFollowState(response);
    if (props.revalidateFollowing && active.value && isCurrentMutation()) {
      try {
        await useHomeTimelineStore().revalidateFollowing();
      } catch {
        // The timeline store owns its revalidation error state.
      }
    }
  } catch {
    if (isCurrentMutation()) {
      mutationErrors.set(userId, 'Could not update follow status.');
    }
  } finally {
    if (isCurrentMutation()) {
      pendingIDs.delete(userId);
    }
  }
};

watch([viewerIdentityID, () => props.viewerId, sessionVersion], () => {
  viewerGeneration.value += 1;
  requestVersion += 1;
  loading.value = false;
  hasLoaded = false;
  items.value = [];
  pendingIDs.clear();
  mutationErrors.clear();
  mutationVersions.clear();
  emit('availability-change', false);
  if (active.value && props.enabled && props.viewerId !== null && hasCurrentViewer(props.viewerId)) {
    void loadSuggestions();
  }
}, { flush: 'sync' });

watch(() => props.enabled, (enabled) => {
  if (!enabled) {
    requestVersion += 1;
    loading.value = false;
    return;
  }
  if (active.value && !hasLoaded) void loadSuggestions();
}, { flush: 'sync' });

onMounted(() => {
  active.value = true;
  if (!hasActivated) void loadSuggestions();
});

onActivated(() => {
  active.value = true;
  if (hasActivated) {
    hasLoaded = false;
    void loadSuggestions(true);
  } else if (!loading.value && !hasLoaded) {
    void loadSuggestions();
  }
  hasActivated = true;
});

onDeactivated(() => {
  active.value = false;
  requestVersion += 1;
  loading.value = false;
  hasLoaded = false;
});

onBeforeUnmount(() => {
  active.value = false;
  requestVersion += 1;
  unregisterSuggestedUsersSync();
  emit('availability-change', false);
});
</script>

<style scoped>
.suggested-users-list { width: 100%; min-width: 0; text-align: left; }
.suggested-users-list__heading { margin: 0; padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); color: var(--color-text); font-size: 16px; line-height: 1.4; }
.suggested-users-list__items { width: 100%; min-width: 0; overflow: hidden; }
.suggested-users-list__items :deep(.user-row__follow) { min-height: 44px; }
</style>
