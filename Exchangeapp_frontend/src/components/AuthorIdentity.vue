<template>
  <RouterLink
    class="author-identity"
    :to="{ name: 'UserProfile', params: { id: author.id } }"
    :aria-label="`查看 ${displayName} 的主页`"
    @click.stop
  >
    <span class="author-avatar" aria-hidden="true">{{ initial }}</span>
    <span class="author-copy">
      <span class="author-name">{{ displayName }}</span>
      <span class="author-meta">@{{ displayName }}<span v-if="relativeTime"> · {{ relativeTime }}</span></span>
    </span>
  </RouterLink>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { PublicAuthor } from '../types/User';
import { formatRelativeTime } from '../utils/time';

const props = defineProps<{
  author: PublicAuthor;
  createdAt?: string;
}>();

const displayName = computed(() => props.author.username.trim() || '?');
const initial = computed(() => Array.from(props.author.username.trim())[0]?.toUpperCase() || '?');
const relativeTime = computed(() => formatRelativeTime(props.createdAt));
</script>

<style scoped>
.author-identity {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  width: fit-content;
  min-width: 0;
  color: #475569;
  text-decoration: none;
}

.author-identity:hover .author-name,
.author-identity:focus-visible .author-name {
  color: #1d4ed8;
}

.author-identity:focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 3px;
  border-radius: 6px;
}

.author-avatar {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 30px;
  height: 30px;
  border: 1px solid rgba(37, 99, 235, 0.18);
  border-radius: 50%;
  background: linear-gradient(135deg, #dbeafe, #eff6ff);
  color: #1d4ed8;
  font-size: 13px;
  font-weight: 800;
}

.author-copy {
  display: grid;
  min-width: 0;
  gap: 1px;
  line-height: 1.12;
}

.author-name {
  overflow: hidden;
  color: #1e293b;
  font-size: 13px;
  font-weight: 750;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color 160ms ease;
}

.author-meta {
  overflow: hidden;
  color: #94a3b8;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

