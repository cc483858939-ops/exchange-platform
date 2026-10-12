<template>
  <span class="following-suggestions__icon" aria-hidden="true">
    <AppIcon name="profile" :size="28" />
  </span>
  <h2 class="following-suggestions__heading">Your Following feed is quiet</h2>
  <p class="following-suggestions__description">
    {{ hasSuggestions ? 'Discover people to follow and see their posts here.' : 'Find people to follow and see their posts here.' }}
  </p>

  <SuggestedUsersList
    v-if="viewerId !== null"
    class="following-suggestions__content"
    :viewer-id="viewerId"
    :limit="3"
    title="Suggested for you"
    :title-level="3"
    :revalidate-following="true"
    @availability-change="hasSuggestions = $event"
  />

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
import { ref } from 'vue';
import AppIcon from '../icons/AppIcon.vue';
import SuggestedUsersList from '../users/SuggestedUsersList.vue';

defineProps<{ viewerId: number | null }>();
defineEmits<{ 'browse-for-you': [] }>();
const hasSuggestions = ref(false);
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

.following-suggestions__content :deep(.suggested-users-list__heading) {
  margin: 0;
  color: var(--color-text);
  font-size: 16px;
  line-height: 1.4;
}

.following-suggestions__content :deep(.suggested-users-list__items) {
  width: 100%;
  min-width: 0;
  overflow: hidden;
  border-top: 1px solid var(--color-border);
}

.following-suggestions__content :deep(.user-row) {
  min-width: 0;
  padding-inline: var(--space-2);
}

.following-suggestions__content :deep(.user-row__follow) {
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

  .following-suggestions__content :deep(.user-row) {
    padding-inline: var(--space-1);
  }
}
</style>
