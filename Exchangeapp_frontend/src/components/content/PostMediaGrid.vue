<template>
  <div
    v-if="media.length > 0"
    class="post-media-grid"
    :class="[
      `post-media-grid--count-${visibleMedia.length}`,
      {
        'post-media-grid--single-sized': hasSizedSingleMedia,
      },
    ]"
    :style="singleMediaStyle"
    role="group"
    aria-label="Post images"
  >
    <figure
      v-for="(item, index) in visibleMedia"
      :key="`${item.url}-${item.position}-${index}`"
      class="post-media-grid__item"
      :class="{
        'post-media-grid__item--featured':
          visibleMedia.length === 3
          && index === 0,
      }"
    >
      <button
        v-if="interactive && !failedURLs.has(item.url)"
        class="post-media-grid__open"
        type="button"
        :aria-label="openImageLabel(index)"
        @click.stop="emit('open', index)"
      >
        <img
          class="post-media-grid__image"
          :class="{ 'post-media-grid__image--single': visibleMedia.length === 1 }"
          :src="item.url"
          :srcset="imageSrcset(item)"
          :alt="`Post image ${index + 1}`"
          loading="lazy"
          decoding="async"
          :width="item.width > 0 ? item.width : undefined"
          :height="item.height > 0 ? item.height : undefined"
          @error="handleImageError(item)"
        />
      </button>
      <img
        v-else-if="!failedURLs.has(item.url)"
        class="post-media-grid__image"
        :class="{ 'post-media-grid__image--single': visibleMedia.length === 1 }"
        :src="item.url"
        :srcset="imageSrcset(item)"
        :alt="`Post image ${index + 1}`"
        loading="lazy"
        decoding="async"
        :width="item.width > 0 ? item.width : undefined"
        :height="item.height > 0 ? item.height : undefined"
        @error="handleImageError(item)"
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
import { computed, ref } from 'vue';
import type { PostMedia } from '../../types/Post';
import AppIcon from '../icons/AppIcon.vue';

const props = withDefaults(defineProps<{
  media: PostMedia[];
  removable?: boolean;
  disabled?: boolean;
  interactive?: boolean;
}>(), {
  removable: false,
  disabled: false,
  interactive: false,
});

const emit = defineEmits<{
  remove: [index: number];
  open: [index: number];
}>();

const SINGLE_MEDIA_MAX_WIDTH_PX = 520;
const SINGLE_MEDIA_MAX_HEIGHT_PX = 640;

const visibleMedia = computed(() => props.media.slice(0, 4));
const failedURLs = ref(new Set<string>());
const largeFallbackURLs = ref(new Set<string>());

const hasSizedSingleMedia = computed(() => {
  const item = visibleMedia.value[0];

  return (
    !props.removable
    && visibleMedia.value.length === 1
    && Boolean(item)
    && item.width > 0
    && item.height > 0
  );
});

const calculateSingleMediaWidth = (width: number, height: number) => Math.min(
  width,
  SINGLE_MEDIA_MAX_WIDTH_PX,
  SINGLE_MEDIA_MAX_HEIGHT_PX * (width / height),
);

const singleMediaStyle = computed(() => {
  if (!hasSizedSingleMedia.value) {
    return undefined;
  }

  const item = visibleMedia.value[0];
  if (!item) {
    return undefined;
  }

  const displayWidth = Math.max(
    1,
    Math.round(
      calculateSingleMediaWidth(item.width, item.height),
    ),
  );

  return {
    '--post-media-single-width': `${displayWidth}px`,
    '--post-media-single-aspect-ratio': `${item.width} / ${item.height}`,
  };
});

const canUseResponsiveSingleMedia = (item: PostMedia) => (
  !props.removable
  && visibleMedia.value.length === 1
  && hasSizedSingleMedia.value
  && Boolean(item.large_url?.trim())
  && item.large_url !== item.url
  && !largeFallbackURLs.value.has(item.url)
);

const imageSrcset = (item: PostMedia) => {
  if (!canUseResponsiveSingleMedia(item)) {
    return undefined;
  }

  return `${item.url} 1x, ${item.large_url} 2x`;
};

const markFailed = (url: string) => {
  failedURLs.value = new Set([...failedURLs.value, url]);
};

const handleImageError = (item: PostMedia) => {
  if (canUseResponsiveSingleMedia(item)) {
    largeFallbackURLs.value = new Set([
      ...largeFallbackURLs.value,
      item.url,
    ]);

    return;
  }

  markFailed(item.url);
};

const openImageLabel = (index: number) => (
  visibleMedia.value.length === 1
    ? 'Open post image'
    : `Open post image ${index + 1} of ${visibleMedia.value.length}`
);
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
  aspect-ratio: auto;
  display: flex;
  justify-content: center;
}

.post-media-grid__open {
  display: flex;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.post-media-grid--count-1 .post-media-grid__open {
  height: auto;
}

.post-media-grid--single-sized {
  width: min(100%, var(--post-media-single-width));
  aspect-ratio: var(--post-media-single-aspect-ratio);
}

.post-media-grid--single-sized .post-media-grid__open {
  width: 100%;
  height: 100%;
}

.post-media-grid__open:focus-visible {
  position: relative;
  z-index: 1;
  outline: 2px solid var(--color-accent);
  outline-offset: -2px;
}

.post-media-grid--count-3 .post-media-grid__item--featured {
  grid-row: span 2;
  aspect-ratio: auto;
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

.post-media-grid--count-1 .post-media-grid__image {
  width: auto;
  max-width: 100%;
  height: auto;
  max-height: 640px;
  object-fit: contain;
}

.post-media-grid--single-sized .post-media-grid__image {
  width: 100%;
  max-width: none;
  height: 100%;
  max-height: none;
  object-fit: cover;
}

.post-media-grid__placeholder {
  display: grid;
  min-height: 96px;
  place-items: center;
  color: var(--color-text-tertiary);
}

.post-media-grid--count-2 .post-media-grid__item,
.post-media-grid--count-4 .post-media-grid__item,
.post-media-grid--count-3 .post-media-grid__item:not(.post-media-grid__item--featured) {
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
