<template>
  <div
    v-if="media.length > 0"
    class="post-media-grid"
    :class="`post-media-grid--count-${Math.min(media.length, 4)}`"
    role="group"
    aria-label="Post images"
  >
    <figure
      v-for="(item, index) in media.slice(0, 4)"
      :key="`${item.url}-${item.position}-${index}`"
      class="post-media-grid__item"
    >
      <img
        v-if="!failedURLs.has(item.url)"
        class="post-media-grid__image"
        :src="item.url"
        :alt="`Post image ${index + 1}`"
        loading="lazy"
        @error="markFailed(item.url)"
      />
      <div
        v-else
        class="post-media-grid__placeholder"
        role="img"
        :aria-label="`Post image ${index + 1} unavailable`"
      >
        <AppIcon name="image-off" :size="22" />
      </div>
      <button
        v-if="removable"
        class="post-media-grid__remove"
        type="button"
        :aria-label="`Remove image ${index + 1}`"
        :disabled="disabled"
        @click.stop="emit('remove', index)"
      >
        <AppIcon name="image-off" :size="16" />
      </button>
    </figure>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import type { PostMedia } from '../../types/Post';
import AppIcon from '../icons/AppIcon.vue';

withDefaults(defineProps<{
  media: PostMedia[];
  removable?: boolean;
  disabled?: boolean;
}>(), {
  removable: false,
  disabled: false,
});

const emit = defineEmits<{
  remove: [index: number];
}>();

const failedURLs = ref(new Set<string>());

const markFailed = (url: string) => {
  failedURLs.value = new Set([...failedURLs.value, url]);
};
</script>

<style scoped>
.post-media-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 2px;
  width: 100%;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-border-strong);
}

.post-media-grid__item {
  position: relative;
  min-width: 0;
  min-height: 0;
  margin: 0;
  overflow: hidden;
  background: var(--color-surface-subtle);
}

.post-media-grid--count-1 {
  grid-template-columns: minmax(0, 1fr);
}

.post-media-grid--count-1 .post-media-grid__item {
  aspect-ratio: 16 / 9;
}

.post-media-grid--count-3 .post-media-grid__item:first-child {
  grid-row: span 2;
}

.post-media-grid__image,
.post-media-grid__placeholder {
  display: block;
  width: 100%;
  height: 100%;
  min-height: 0;
}

.post-media-grid__image {
  object-fit: cover;
}

.post-media-grid__placeholder {
  display: grid;
  min-height: 96px;
  place-items: center;
  color: var(--color-text-tertiary);
}

.post-media-grid--count-2 .post-media-grid__item,
.post-media-grid--count-3 .post-media-grid__item,
.post-media-grid--count-4 .post-media-grid__item {
  aspect-ratio: 1;
}

.post-media-grid__remove {
  position: absolute;
  top: var(--space-2);
  right: var(--space-2);
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: color-mix(in srgb, var(--color-text) 72%, transparent);
  color: var(--color-surface);
  cursor: pointer;
}

.post-media-grid__remove:hover:not(:disabled),
.post-media-grid__remove:focus-visible {
  background: var(--color-danger);
}

.post-media-grid__remove:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

@media (max-width: 420px) {
  .post-media-grid__remove {
    top: var(--space-1);
    right: var(--space-1);
  }
}
</style>
