<template>
  <dialog
    ref="dialogRef"
    class="post-media-viewer"
    aria-label="Post media viewer"
    aria-modal="true"
    @cancel="handleCancel"
  >
    <div
      class="post-media-viewer__surface"
      :class="{ 'post-media-viewer__surface--split': desktopSplitActive }"
    >
      <section
        class="post-media-viewer__media-pane"
        @pointerdown="handlePointerDown"
        @pointerup="handlePointerUp"
        @pointercancel="resetPointer"
        @click="handleViewerClick"
      >
      <button
        ref="closeButtonRef"
        class="post-media-viewer__close"
        :class="{ 'post-media-viewer__close--split': desktopSplitActive }"
        type="button"
        aria-label="Close image viewer"
        @click="requestClose"
      >
        <AppIcon name="close" :size="20" />
      </button>

      <div
        ref="stageRef"
        class="post-media-viewer__stage"
      >
        <button
          v-if="hasMultipleMedia"
          class="post-media-viewer__nav post-media-viewer__nav--previous"
          type="button"
          aria-label="Previous image"
          :disabled="currentIndex === 0"
          @click.stop="showPrevious"
        >
          <AppIcon name="arrow-left" :size="22" />
        </button>

        <div
          ref="imageFrameRef"
          class="post-media-viewer__image-frame"
          :aria-label="imagePositionLabel"
        >
          <img
            v-if="activeMedia && !failedMediumURLs.has(activeMedia.url)"
            class="post-media-viewer__image"
            :src="resolvedImageURL"
            :alt="imageAlt"
            loading="eager"
            decoding="async"
            draggable="false"
            @error="handleImageError"
          />
          <div
            v-else
            class="post-media-viewer__placeholder"
            role="img"
            aria-label="Image unavailable"
          >
            <AppIcon name="image-off" :size="28" />
            <span>Image unavailable</span>
          </div>
        </div>

        <button
          v-if="hasMultipleMedia"
          class="post-media-viewer__nav post-media-viewer__nav--next"
          type="button"
          aria-label="Next image"
          :disabled="currentIndex === visibleMedia.length - 1"
          @click.stop="showNext"
        >
          <AppIcon name="arrow-left" :size="22" />
        </button>
      </div>

      <output
        v-if="hasMultipleMedia"
        class="post-media-viewer__counter"
        :aria-label="imagePositionLabel"
      >
        {{ currentIndex + 1 }} / {{ visibleMedia.length }}
      </output>
      </section>

      <aside
        v-if="desktopSplitActive"
        class="post-media-viewer__context"
        aria-label="Post conversation"
      >
        <slot name="context" />
      </aside>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { PostMedia } from '../../types/Post';
import AppIcon from '../icons/AppIcon.vue';

const props = withDefaults(defineProps<{
  media: PostMedia[];
  initialIndex: number;
  desktopContext?: boolean;
}>(), {
  desktopContext: false,
});

const emit = defineEmits<{
  close: [];
}>();

const dialogRef = ref<HTMLDialogElement | null>(null);
const closeButtonRef = ref<HTMLButtonElement | null>(null);
const stageRef = ref<HTMLElement | null>(null);
const imageFrameRef = ref<HTMLElement | null>(null);
const desktopSplitActive = ref(false);
const DESKTOP_CONTEXT_QUERY = '(min-width: 1100px)';
let desktopMediaQuery: MediaQueryList | null = null;

const handleDesktopMediaQueryChange = (event: MediaQueryListEvent) => {
  desktopSplitActive.value = props.desktopContext && event.matches;
};

const stopDesktopSplitTracking = () => {
  if (!desktopMediaQuery) {
    return;
  }

  if (typeof desktopMediaQuery.removeEventListener === 'function') {
    desktopMediaQuery.removeEventListener('change', handleDesktopMediaQueryChange);
  } else {
    desktopMediaQuery.removeListener(handleDesktopMediaQueryChange);
  }
  desktopMediaQuery = null;
};

const startDesktopSplitTracking = () => {
  stopDesktopSplitTracking();

  if (
    !props.desktopContext
    || typeof window === 'undefined'
    || typeof window.matchMedia !== 'function'
  ) {
    desktopSplitActive.value = false;
    return;
  }

  desktopMediaQuery = window.matchMedia(DESKTOP_CONTEXT_QUERY);
  desktopSplitActive.value = desktopMediaQuery.matches;
  if (typeof desktopMediaQuery.addEventListener === 'function') {
    desktopMediaQuery.addEventListener('change', handleDesktopMediaQueryChange);
  } else {
    desktopMediaQuery.addListener(handleDesktopMediaQueryChange);
  }
};
const visibleMedia = computed(() => props.media.slice(0, 4));

const clampIndex = (index: number, length: number) => {
  if (length === 0 || !Number.isFinite(index)) {
    return 0;
  }
  return Math.min(Math.max(Math.trunc(index), 0), length - 1);
};

const currentIndex = ref(clampIndex(props.initialIndex, visibleMedia.value.length));
const failedMediumURLs = ref(new Set<string>());
const failedLargeURLs = ref(new Set<string>());
const activeMedia = computed(() => visibleMedia.value[currentIndex.value] ?? null);
const hasMultipleMedia = computed(() => visibleMedia.value.length > 1);
const resolvedImageURL = ref('');
let largeUpgradeVersion = 0;
const imageAlt = computed(() => (
  hasMultipleMedia.value
    ? `Post image ${currentIndex.value + 1} of ${visibleMedia.value.length}`
    : 'Post image'
));
const imagePositionLabel = computed(() => (
  hasMultipleMedia.value
    ? `Image ${currentIndex.value + 1} of ${visibleMedia.value.length}`
    : 'Post image'
));
let closeRequested = false;
let suppressNextViewerClick = false;

const requestClose = () => {
  if (closeRequested) {
    return;
  }
  closeRequested = true;
  emit('close');
};

const showPrevious = () => {
  if (!closeRequested && currentIndex.value > 0) {
    currentIndex.value -= 1;
  }
};

const showNext = () => {
  if (!closeRequested && currentIndex.value < visibleMedia.value.length - 1) {
    currentIndex.value += 1;
  }
};

const markMediumFailed = (url: string) => {
  failedMediumURLs.value = new Set([...failedMediumURLs.value, url]);
};

const markLargeFailed = (url: string) => {
  failedLargeURLs.value = new Set([...failedLargeURLs.value, url]);
};

const waitForImageLoad = (image: HTMLImageElement) => new Promise<void>((resolve, reject) => {
  image.onload = () => resolve();
  image.onerror = () => reject(new Error('large image failed to load'));
});

const canUpgradeLarge = (media: PostMedia, version: number) => (
  !closeRequested
  && version === largeUpgradeVersion
  && activeMedia.value === media
);

const preloadLarge = async (media: PostMedia, version: number) => {
  if (!canUpgradeLarge(media, version)) {
    return;
  }

  const image = new Image();
  image.decoding = 'async';
  image.src = media.large_url;

  try {
    if (typeof image.decode === 'function') {
      await image.decode();
    } else {
      await waitForImageLoad(image);
    }
  } catch {
    markLargeFailed(media.large_url);
    return;
  }

  if (version !== largeUpgradeVersion || activeMedia.value !== media) {
    return;
  }

  resolvedImageURL.value = media.large_url;
};

const queueLargeUpgrade = (media: PostMedia, version: number) => {
  const upgrade = () => {
    if (!canUpgradeLarge(media, version)) {
      return;
    }

    void preloadLarge(media, version);
  };
  if (typeof window.requestAnimationFrame === 'function') {
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(upgrade);
    });
  } else {
    window.setTimeout(upgrade, 0);
  }
};

const handleImageError = () => {
  const media = activeMedia.value;
  if (!media) {
    return;
  }
  if (resolvedImageURL.value === media.large_url && media.large_url !== media.url) {
    markLargeFailed(media.large_url);
    resolvedImageURL.value = media.url;
    return;
  }
  markMediumFailed(media.url);
};

const handleKeydown = (event: KeyboardEvent) => {
  if (closeRequested) {
    return;
  }

  if (event.key === 'ArrowLeft') {
    event.preventDefault();
    showPrevious();
  } else if (event.key === 'ArrowRight') {
    event.preventDefault();
    showNext();
  } else if (event.key === 'Escape') {
    event.preventDefault();
    requestClose();
  }
};

let pointerStart: { x: number; y: number } | null = null;
const VIEWER_CLICK_DRAG_THRESHOLD_PX = 8;

const handlePointerDown = (event: PointerEvent) => {
  suppressNextViewerClick = false;
  pointerStart = { x: event.clientX, y: event.clientY };
};

const resetPointer = () => {
  pointerStart = null;
};

const handlePointerUp = (event: PointerEvent) => {
  const start = pointerStart;
  resetPointer();

  if (!start) {
    return;
  }

  const deltaX = event.clientX - start.x;
  const deltaY = event.clientY - start.y;
  if (Math.hypot(deltaX, deltaY) > VIEWER_CLICK_DRAG_THRESHOLD_PX) {
    suppressNextViewerClick = true;
  }
  if (!hasMultipleMedia.value) {
    return;
  }
  if (Math.abs(deltaX) < 50 || Math.abs(deltaX) <= Math.abs(deltaY)) {
    return;
  }

  if (deltaX < 0) {
    showNext();
  } else {
    showPrevious();
  }
};

const handleViewerClick = (event: MouseEvent) => {
  if (closeRequested) {
    return;
  }

  if (suppressNextViewerClick) {
    suppressNextViewerClick = false;
    return;
  }

  const target = event.target;
  if (
    target === event.currentTarget
    || target === stageRef.value
    || target === imageFrameRef.value
  ) {
    requestClose();
  }
};

const handleCancel = (event: Event) => {
  event.preventDefault();
  requestClose();
};

watch(
  [visibleMedia, () => props.initialIndex],
  ([items, initialIndex]) => {
    currentIndex.value = clampIndex(initialIndex, items.length);
  },
);

watch(
  activeMedia,
  media => {
    largeUpgradeVersion += 1;
    const version = largeUpgradeVersion;

    if (!media) {
      resolvedImageURL.value = '';
      return;
    }

    resolvedImageURL.value = media.url;
    if (!media.large_url || media.large_url === media.url || failedLargeURLs.value.has(media.large_url)) {
      return;
    }
    queueLargeUpgrade(media, version);
  },
  { immediate: true },
);

watch(
  () => props.desktopContext,
  () => {
    if (dialogRef.value) {
      startDesktopSplitTracking();
    }
  },
);

onMounted(async () => {
  startDesktopSplitTracking();
  const dialog = dialogRef.value;
  if (!dialog) {
    return;
  }

  if (typeof dialog.showModal === 'function') {
    try {
      if (!dialog.open) {
        dialog.showModal();
      }
    } catch {
      dialog.setAttribute('open', '');
    }
  } else {
    dialog.setAttribute('open', '');
  }

  window.addEventListener('keydown', handleKeydown);
  await nextTick();
  closeButtonRef.value?.focus();
});

onBeforeUnmount(() => {
  closeRequested = true;
  largeUpgradeVersion += 1;
  stopDesktopSplitTracking();
  window.removeEventListener('keydown', handleKeydown);
  const dialog = dialogRef.value;
  if (!dialog) {
    return;
  }

  if (dialog.open && typeof dialog.close === 'function') {
    dialog.close();
  } else {
    dialog.removeAttribute('open');
  }
});
</script>

<style scoped>
.post-media-viewer {
  box-sizing: border-box;
  position: fixed;
  inset: 0;
  width: 100vw;
  max-width: none;
  height: 100vh;
  height: 100dvh;
  max-height: none;
  margin: 0;
  padding: 0;
  border: 0;
  background: rgb(8 10 15 / 96%);
  color: var(--color-surface);
  overflow: hidden;
  overscroll-behavior: contain;
  user-select: none;
  -webkit-user-select: none;
}

.post-media-viewer::backdrop {
  background: rgb(0 0 0 / 78%);
}

.post-media-viewer__surface {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  overflow: hidden;
}

.post-media-viewer__surface--split {
  display: grid;
  grid-template-columns: minmax(0, 1fr) clamp(360px, 28vw, 420px);
}

.post-media-viewer__media-pane {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: max(12px, env(safe-area-inset-top)) max(12px, env(safe-area-inset-right))
    max(12px, env(safe-area-inset-bottom)) max(12px, env(safe-area-inset-left));
  box-sizing: border-box;
  background: rgb(8 10 15 / 96%);
}

.post-media-viewer__context {
  height: 100dvh;
  min-width: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  border-left: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-text);
  box-sizing: border-box;
  user-select: text;
  -webkit-user-select: text;
}

.post-media-viewer__stage {
  position: relative;
  display: flex;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  align-items: center;
  justify-content: center;
  padding: 56px 64px 48px;
  box-sizing: border-box;
  touch-action: pan-y;
}

.post-media-viewer__image-frame {
  display: flex;
  width: 100%;
  height: 100%;
  max-width: 100%;
  max-height: 100%;
  min-width: 0;
  min-height: 0;
  align-items: center;
  justify-content: center;
}

.post-media-viewer__image {
  display: block;
  width: auto;
  height: auto;
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.post-media-viewer__placeholder {
  display: grid;
  min-width: 180px;
  min-height: 120px;
  place-items: center;
  gap: var(--space-2);
  padding: var(--space-6);
  border: 1px solid rgb(255 255 255 / 20%);
  border-radius: var(--radius-md);
  background: rgb(255 255 255 / 8%);
  color: rgb(255 255 255 / 76%);
  text-align: center;
}

.post-media-viewer__close,
.post-media-viewer__nav {
  position: absolute;
  z-index: 1;
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  padding: 0;
  border: 1px solid rgb(255 255 255 / 20%);
  border-radius: 50%;
  background: rgb(255 255 255 / 12%);
  color: inherit;
  cursor: pointer;
}

.post-media-viewer__close {
  top: max(12px, env(safe-area-inset-top));
  right: max(12px, env(safe-area-inset-right));
}

.post-media-viewer__close--split {
  right: auto;
  left: max(12px, env(safe-area-inset-left));
}

.post-media-viewer__nav {
  top: 50%;
  transform: translateY(-50%);
}

.post-media-viewer__nav--previous {
  left: max(12px, env(safe-area-inset-left));
}

.post-media-viewer__nav--next {
  right: max(12px, env(safe-area-inset-right));
  transform: translateY(-50%) rotate(180deg);
}

.post-media-viewer__close:hover,
.post-media-viewer__close:focus-visible,
.post-media-viewer__nav:hover:not(:disabled),
.post-media-viewer__nav:focus-visible {
  background: rgb(255 255 255 / 22%);
}

.post-media-viewer__close:focus-visible,
.post-media-viewer__nav:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.post-media-viewer__nav:disabled {
  cursor: default;
  opacity: 0.35;
}

.post-media-viewer__counter {
  position: absolute;
  bottom: max(8px, env(safe-area-inset-bottom));
  left: 50%;
  color: rgb(255 255 255 / 78%);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  transform: translateX(-50%);
}

@media (max-width: 600px) {
  .post-media-viewer__stage {
    padding: 56px 8px 48px;
  }
}
</style>
