<template>
  <RouterLink
    class="author-identity"
    :to="{ name: 'UserProfile', params: { id: author.id } }"
    :aria-label="`View ${displayName}'s profile`"
    @click.stop
  >
    <UserAvatar
      class="author-avatar"
      :avatar-url="author.avatar_url"
      :display-name="author.display_name"
      :username="author.username"
      :size="30"
      decorative
    />
    <span class="author-copy">
      <span class="author-name">{{ displayName }}</span>
      <span class="author-meta">@{{ username }}<span v-if="postDate"> · {{ postDate }}</span></span>
    </span>
  </RouterLink>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { PublicAuthor } from '../types/User';
import { useNow } from '../composables/useNow';
import { formatPostDate } from '../utils/time';
import UserAvatar from './users/UserAvatar.vue';

const props = defineProps<{
  author: PublicAuthor;
  createdAt?: string;
}>();

const username = computed(() => props.author.username.trim() || '?');
const displayName = computed(() => props.author.display_name.trim() || username.value);
const now = useNow();
const postDate = computed(() => formatPostDate(props.createdAt, now.value));
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
