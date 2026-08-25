<template>
  <span
    class="user-avatar"
    :class="{ 'user-avatar--decorative': decorative }"
    :style="{ '--user-avatar-size': `${size}px` }"
    :aria-hidden="decorative ? 'true' : undefined"
    :aria-label="!decorative && !avatarURL ? accessibleLabel : undefined"
    :role="!decorative ? 'img' : undefined"
  >
    <span class="user-avatar__fallback" aria-hidden="true">{{ initial }}</span>
    <img
      v-if="avatarURL && !imageFailed"
      class="user-avatar__image"
      :class="{ 'user-avatar__image--loaded': imageLoaded }"
      :src="avatarURL"
      :alt="decorative ? '' : accessibleLabel"
      @load="handleLoad"
      @error="handleError"
    />
  </span>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';

const props = withDefaults(defineProps<{
  avatarUrl?: string | null;
  displayName?: string | null;
  username?: string | null;
  size?: number;
  alt?: string;
  decorative?: boolean;
}>(), {
  avatarUrl: '',
  displayName: '',
  username: '',
  size: 40,
  decorative: false,
});

const imageLoaded = ref(false);
const imageFailed = ref(false);

const trim = (value: string | null | undefined) => value?.trim() || '';
const firstCodePoint = (value: string) => Array.from(value)[0]?.toUpperCase() || '';

const avatarURL = computed(() => trim(props.avatarUrl));
const displayName = computed(() => trim(props.displayName));
const username = computed(() => trim(props.username));
const initial = computed(() => (
  firstCodePoint(displayName.value)
  || firstCodePoint(username.value)
  || '?'
));
const accessibleLabel = computed(() => {
  if (props.alt !== undefined) {
    return props.alt.trim();
  }
  return (displayName.value ? `${displayName.value} avatar` : '')
    || (username.value ? `${username.value} avatar` : 'Avatar');
});

const resetImageState = () => {
  imageLoaded.value = false;
  imageFailed.value = false;
};

const handleLoad = () => {
  imageFailed.value = false;
  imageLoaded.value = true;
};

const handleError = () => {
  imageLoaded.value = false;
  imageFailed.value = true;
};

watch(
  [avatarURL, displayName, username],
  resetImageState,
);
</script>

<style scoped>
.user-avatar {
  --user-avatar-size: 40px;
  position: relative;
  display: grid;
  width: var(--user-avatar-size);
  height: var(--user-avatar-size);
  flex: 0 0 auto;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: calc(var(--user-avatar-size) * 0.38);
  font-weight: 800;
}

.user-avatar__fallback,
.user-avatar__image {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.user-avatar__fallback {
  display: grid;
  place-items: center;
}

.user-avatar__image {
  display: block;
  object-fit: cover;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.user-avatar__image--loaded {
  opacity: 1;
}
</style>
