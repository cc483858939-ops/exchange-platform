<template>
  <RouterLink
    class="author-identity"
    :class="{ 'author-identity--post': variant === 'post' }"
    :to="{ name: 'UserProfile', params: { id: author.id } }"
    :aria-label="`View ${displayName}'s profile`"
    @click.stop
  >
    <UserAvatar
      class="author-avatar"
      :avatar-url="author.avatar_url"
      :display-name="author.display_name"
      :username="author.username"
      :size="variant === 'post' ? 40 : 30"
      decorative
    />
    <span class="author-copy">
      <span class="author-name">{{ displayName }}</span>
      <span class="author-meta">@{{ username }}<span v-if="variant === 'compact' && postDate"> · {{ postDate }}</span></span>
    </span>
  </RouterLink>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { PublicAuthor } from '../types/User';
import { useNow } from '../composables/useNow';
import { formatPostDate } from '../utils/time';
import UserAvatar from './users/UserAvatar.vue';

const props = withDefaults(defineProps<{
  author: PublicAuthor;
  createdAt?: string;
  variant?: 'compact' | 'post';
}>(), {
  variant: 'compact',
});

const username = computed(() => props.author.username.trim() || '?');
const displayName = computed(() => props.author.display_name.trim() || username.value);
const now = useNow();
const postDate = computed(() => formatPostDate(props.createdAt, now.value));
</script>

<style scoped>
.author-identity {
  --author-avatar-size: 30px;
  --author-name-size: 13px;
  --author-meta-size: 11px;
  --author-gap: 9px;
  display: inline-flex;
  align-items: center;
  gap: var(--author-gap);
  width: fit-content;
  min-width: 0;
  color: var(--color-text-secondary);
  text-decoration: none;
}

.author-identity--post {
  --author-avatar-size: 40px;
  --author-name-size: 15px;
  --author-meta-size: 13px;
  --author-gap: 12px;
}

.author-identity:hover .author-name,
.author-identity:focus-visible .author-name {
  color: var(--color-accent);
}

.author-avatar {
  display: grid;
  width: var(--author-avatar-size);
  height: var(--author-avatar-size);
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
  font-size: var(--author-name-size);
  font-weight: 750;
  transition: color var(--transition-fast);
}

.author-meta {
  color: var(--color-text-tertiary);
  font-size: var(--author-meta-size);
}
</style>
