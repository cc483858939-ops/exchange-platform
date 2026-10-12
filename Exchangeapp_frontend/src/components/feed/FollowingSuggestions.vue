<template>
  <span class="following-suggestions__icon" aria-hidden="true">
    <AppIcon name="profile" :size="28" />
  </span>
  <h2 class="following-suggestions__heading">Your Following feed is quiet</h2>
  <p class="following-suggestions__description">
    {{ hasSuggestions ? 'Discover people to follow and see their posts here.' : 'Find people to follow and see their posts here.' }}
  </p>

  <div v-if="hasSuggestions" class="following-suggestions__content">
    <h3 class="following-suggestions__title">Suggested for you</h3>
    <div class="following-suggestions__list">
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
  </div>

  <RouterLink
    class="following-suggestions__primary"
    :to="{ name: 'UserSearch', query: { tab: 'people' } }"
  >
    {{ hasSuggestions ? 'Find more people' : 'Find people' }}
  </RouterLink>
  <nav class="following-suggestions__links" aria-label="Discover more">
    <RouterLink class="following-suggestions__link" :to="{ name: 'TopicDirectory' }">
      Explore topics
    </RouterLink>
    <span aria-hidden="true">·</span>
    <button class="following-suggestions__link" type="button" @click="$emit('browse-for-you')">
      Browse For You
    </button>
  </nav>
</template>

<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
import UserRow from '../users/UserRow.vue';
import {
  followUser,
  getSuggestedUsers,
  unfollowUser,
} from '../../services/userService';
import type { UserConnectionItem } from '../../services/userService';
import { syncExternalFollowState } from '../../store/sessionSync';
import { useAuthStore } from '../../store/auth';
import { useHomeTimelineStore } from '../../store/homeTimeline';

const props = defineProps<{ viewerId: number | null }>();
defineEmits<{ 'browse-for-you': [] }>();

const authStore = useAuthStore();
const homeTimeline = useHomeTimelineStore();
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
const hasSuggestions = computed(() => items.value.length > 0);

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

const currentRequest = (version: number, viewerId: number, generation: number) => (
  active.value
  && version === requestVersion
  && generation === viewerGeneration.value
  && hasCurrentViewer(viewerId)
);

const loadSuggestions = async (force = false) => {
  const viewerId = props.viewerId;
  if (
    !active.value
    || viewerId === null
    || !hasCurrentViewer(viewerId)
    || loading.value
    || (hasLoaded && !force)
  ) return;

  const version = ++requestVersion;
  const generation = viewerGeneration.value;
  loading.value = true;
  try {
    const page = await getSuggestedUsers(3);
    if (!currentRequest(version, viewerId, generation)) return;
    if (!Array.isArray(page.items)) throw new Error('Invalid suggested user response');
    items.value = page.items.slice(0, 3);
    hasLoaded = true;
  } catch {
    if (!currentRequest(version, viewerId, generation)) return;
    items.value = [];
    hasLoaded = true;
  } finally {
    if (version === requestVersion) loading.value = false;
  }
};

const toggleFollow = async (userId: number) => {
  const item = items.value.find(candidate => candidate.user.id === userId);
  const viewerId = props.viewerId;
  if (!item || viewerId === null || !hasCurrentViewer(viewerId) || pendingIDs.has(userId)) return;

  const capturedViewerGeneration = viewerGeneration.value;
  const mutationVersion = ++mutationSequence;
  mutationVersions.set(userId, mutationVersion);
  pendingIDs.add(userId);
  mutationErrors.delete(userId);
  try {
    const response = item.following
      ? await unfollowUser(userId)
      : await followUser(userId);
    if (
      !hasCurrentViewer(viewerId)
      || viewerGeneration.value !== capturedViewerGeneration
      || mutationVersions.get(userId) !== mutationVersion
    ) return;
    if (response.user_id !== userId || typeof response.following !== 'boolean') {
      throw new Error('Invalid follow response');
    }

    const currentItem = items.value.find(candidate => candidate.user.id === userId);
    if (currentItem) currentItem.following = response.following;
    syncExternalFollowState(response);
    pendingIDs.delete(userId);
    if (
      active.value
      && viewerGeneration.value === capturedViewerGeneration
      && hasCurrentViewer(viewerId)
    ) {
      try {
        await homeTimeline.revalidateFollowing();
      } catch {
        // The timeline store owns its revalidation error state.
      }
    }
  } catch {
    if (
      !hasCurrentViewer(viewerId)
      || viewerGeneration.value !== capturedViewerGeneration
      || mutationVersions.get(userId) !== mutationVersion
    ) return;
    mutationErrors.set(userId, 'Could not update follow status.');
  } finally {
    if (
      hasCurrentViewer(viewerId)
      && viewerGeneration.value === capturedViewerGeneration
      && mutationVersions.get(userId) === mutationVersion
    ) {
      pendingIDs.delete(userId);
    }
  }
};

watch([viewerIdentityID, () => props.viewerId], () => {
  viewerGeneration.value += 1;
  requestVersion += 1;
  loading.value = false;
  hasLoaded = false;
  items.value = [];
  pendingIDs.clear();
  mutationErrors.clear();
  if (active.value && props.viewerId !== null && hasCurrentViewer(props.viewerId)) {
    void loadSuggestions();
  }
});

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
});
</script>

<style scoped>
.following-suggestions__icon {
  display: grid;
  width: 56px;
  height: 56px;
  place-items: center;
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.following-suggestions__heading {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  line-height: 1.25;
}

.following-suggestions__description {
  max-width: 30ch;
  margin: 0;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.following-suggestions__content {
  display: grid;
  width: 100%;
  max-width: 430px;
  gap: var(--space-2);
  text-align: left;
}

.following-suggestions__title {
  margin: 0;
  color: var(--color-text);
  font-size: 16px;
  line-height: 1.4;
}

.following-suggestions__list {
  width: 100%;
  min-width: 0;
  overflow: hidden;
  border-top: 1px solid var(--color-border);
}

.following-suggestions__list :deep(.user-row) {
  min-width: 0;
  padding-inline: var(--space-2);
}

.following-suggestions__list :deep(.user-row__follow) {
  min-height: 44px;
}

.following-suggestions__primary {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-accent);
  color: #fff;
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.following-suggestions__primary:hover {
  border-color: var(--color-accent-hover);
  background: var(--color-accent-hover);
}

.following-suggestions__primary:focus-visible,
.following-suggestions__link:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.following-suggestions__links {
  display: flex;
  max-width: 100%;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
}

.following-suggestions__link {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-2);
  background: transparent;
  color: var(--color-accent);
  font: inherit;
  font-size: 14px;
  font-weight: 700;
  text-decoration: none;
  cursor: pointer;
}

.following-suggestions__link:hover {
  color: var(--color-accent-hover);
  text-decoration: underline;
}

@media (max-width: 520px) {
  .following-suggestions__heading {
    font-size: 22px;
  }

  .following-suggestions__list :deep(.user-row) {
    padding-inline: var(--space-1);
  }
}
</style>
