<template>
  <RouterLink
    class="author-identity"
    :to="{ name: 'UserProfile', params: { id: author.id } }"
    :aria-label="`View ${displayName}'s profile`"
    @click.stop
  >
    <span class="author-avatar" aria-hidden="true">
      <img
        v-if="avatarURL && !avatarLoadFailed"
        :src="avatarURL"
        alt=""
        @error="avatarLoadFailed = true"
      />
      <span v-else>{{ initial }}</span>
    </span>
    <span class="author-copy">
      <span class="author-name">{{ displayName }}</span>
      <span class="author-meta">@{{ username }}<span v-if="relativeTime"> · {{ relativeTime }}</span></span>
    </span>
  </RouterLink>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { PublicAuthor } from '../types/User';
import { formatRelativeTime } from '../utils/time';

const props = defineProps<{
  author: PublicAuthor;
  createdAt?: string;
}>();

const username = computed(() => props.author.username.trim() || '?');
const displayName = computed(() => props.author.display_name.trim() || username.value);
const initial = computed(() => Array.from(displayName.value.trim())[0]?.toUpperCase() || '?');
const avatarURL = computed(() => props.author.avatar_url.trim());
const avatarLoadFailed = ref(false);
const relativeTime = computed(() => formatRelativeTime(props.createdAt));

watch(() => [props.author.id, props.author.avatar_url], () => {
  avatarLoadFailed.value = false;
});
</script>

<style scoped>
.author-identity {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  width: fit-content;
  min-width: 0;
  color: var(--color-text-secondary);
  text-decoration: none;
}

.author-identity:hover .author-name,
.author-identity:focus-visible .author-name {
  color: var(--color-accent);
}

.author-avatar {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  overflow: hidden;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: 800;
}

.author-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.author-copy {
  display: grid;
  min-width: 0;
  gap: 1px;
  line-height: 1.12;
}

.author-name,
.author-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.author-name {
  color: var(--color-text);
  font-size: 13px;
  font-weight: 750;
  transition: color var(--transition-fast);
}

.author-meta {
  color: var(--color-text-tertiary);
  font-size: 11px;
}
</style>
