<template>
  <button
    type="button"
    class="bookmark-action"
    :class="{
      'bookmark-action--compact': variant === 'compact',
      'bookmark-action--detail': variant === 'detail',
      'bookmark-action--bookmarked': visualBookmarked,
      'bookmark-action--disabled': disabled,
      'bookmark-action--loading': loading,
      'bookmark-action--pending': pending,
      'bookmark-action--bookmarking': motion === 'bookmarking',
      'bookmark-action--unbookmarking': motion === 'unbookmarking',
    }"
    :disabled="effectivelyDisabled"
    :aria-busy="loading || pending ? 'true' : undefined"
    :aria-pressed="bookmarked"
    :aria-label="ariaLabel"
    :data-motion="motion"
    :style="motionStyle"
    @click.stop="activate"
  >
    <span class="bookmark-action__visual" aria-hidden="true">
      <span class="bookmark-action__icon bookmark-action__icon--outline">
        <AppIcon
          name="bookmark"
          :size="variant === 'detail' ? 20 : 18"
          :filled="false"
        />
      </span>
      <span class="bookmark-action__icon bookmark-action__icon--filled">
        <AppIcon
          name="bookmark"
          :size="variant === 'detail' ? 20 : 18"
          :filled="true"
        />
      </span>
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';

type BookmarkActionVariant = 'compact' | 'detail';
type BookmarkMotion = 'idle' | 'bookmarking' | 'unbookmarking';

const bookmarkMotionDurationMs = 290;
const unbookmarkMotionDurationMs = 170;

const props = withDefaults(defineProps<{
  bookmarked: boolean;
  loading?: boolean;
  pending?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  variant?: BookmarkActionVariant;
}>(), {
  loading: false,
  pending: false,
  disabled: false,
  variant: 'compact',
});

const emit = defineEmits<{
  toggle: [];
}>();

const motion = ref<BookmarkMotion>('idle');
const expectedBookmarked = ref<boolean | null>(null);
const expectedStateObserved = ref(false);
let motionTimer: ReturnType<typeof setTimeout> | null = null;

const motionStyle = computed(() => ({
  '--bookmark-motion-duration': `${bookmarkMotionDurationMs}ms`,
  '--unbookmark-motion-duration': `${unbookmarkMotionDurationMs}ms`,
}));

const visualBookmarked = computed(() => {
  if (motion.value === 'bookmarking') {
    return true;
  }

  if (motion.value === 'unbookmarking') {
    return false;
  }

  return props.bookmarked;
});

const effectivelyDisabled = computed(() => (
  props.disabled || props.loading || props.pending
));

const clearMotionTimer = () => {
  if (motionTimer !== null) {
    clearTimeout(motionTimer);
    motionTimer = null;
  }
};

const cancelMotion = () => {
  clearMotionTimer();
  motion.value = 'idle';
  expectedBookmarked.value = null;
  expectedStateObserved.value = false;
};

const startMotion = (nextMotion: Exclude<BookmarkMotion, 'idle'>) => {
  clearMotionTimer();
  motion.value = nextMotion;
  motionTimer = setTimeout(() => {
    motion.value = 'idle';
    motionTimer = null;
    expectedBookmarked.value = null;
    expectedStateObserved.value = false;
  }, nextMotion === 'bookmarking'
    ? bookmarkMotionDurationMs
    : unbookmarkMotionDurationMs);
};

const activate = () => {
  if (effectivelyDisabled.value) {
    return;
  }

  const nextBookmarked = !props.bookmarked;
  expectedBookmarked.value = nextBookmarked;
  expectedStateObserved.value = false;

  startMotion(nextBookmarked ? 'bookmarking' : 'unbookmarking');
  emit('toggle');
};

watch(
  () => props.bookmarked,
  nextBookmarked => {
    if (motion.value === 'idle' || expectedBookmarked.value === null) {
      return;
    }

    if (nextBookmarked === expectedBookmarked.value) {
      expectedStateObserved.value = true;
      return;
    }

    if (expectedStateObserved.value) {
      cancelMotion();
    }
  },
);

onBeforeUnmount(clearMotionTimer);
</script>

<style scoped>
.bookmark-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  min-width: 40px;
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  line-height: 1;
  white-space: nowrap;
  transition: color 140ms ease, background-color 140ms ease, opacity 140ms ease;
}

.bookmark-action--compact {
  margin: -8px 0;
}

.bookmark-action--detail {
  padding: 0 var(--space-3);
}

.bookmark-action--bookmarked {
  color: var(--color-accent);
}

.bookmark-action:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .bookmark-action:hover:not(:disabled) {
    background: var(--color-surface-subtle);
    color: var(--color-accent);
  }
}

.bookmark-action:disabled {
  cursor: default;
}

.bookmark-action--disabled,
.bookmark-action--loading {
  opacity: 0.64;
}

.bookmark-action--pending {
  opacity: 1;
}

.bookmark-action__visual {
  position: relative;
  display: inline-flex;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  align-items: center;
  justify-content: center;
  overflow: visible;
  isolation: isolate;
}

.bookmark-action__icon {
  position: absolute;
  inset: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  z-index: 1;
}

.bookmark-action__visual::after {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 0;
  width: 30px;
  height: 30px;
  box-sizing: border-box;
  border: 2px solid var(--color-accent);
  border-radius: 50%;
  content: '';
  opacity: 0;
  pointer-events: none;
  transform: translate(-50%, -50%) scale(0.45);
}

.bookmark-action__icon--outline {
  opacity: 1;
  transition: opacity 70ms ease;
}

.bookmark-action__icon--filled {
  opacity: 0;
}

.bookmark-action--bookmarked .bookmark-action__icon--outline {
  opacity: 0;
}

.bookmark-action--bookmarked .bookmark-action__icon--filled {
  opacity: 1;
}

.bookmark-action--bookmarking .bookmark-action__icon--outline {
  animation: nexus-bookmark-outline-press 85ms ease-out both;
}

.bookmark-action--bookmarking .bookmark-action__icon--filled {
  animation: nexus-bookmark-pop-in var(--bookmark-motion-duration) cubic-bezier(0.22, 1, 0.36, 1) both;
}

.bookmark-action--bookmarking .bookmark-action__visual::after {
  animation: nexus-bookmark-ring 210ms ease-out both;
}

.bookmark-action--unbookmarking .bookmark-action__icon--outline {
  opacity: 1;
  transition: opacity 80ms ease 70ms;
}

.bookmark-action--unbookmarking .bookmark-action__icon--filled {
  color: var(--color-accent);
  opacity: 1;
  animation: nexus-bookmark-pop-out var(--unbookmark-motion-duration) cubic-bezier(0.4, 0, 0.2, 1) both;
}

.bookmark-action__icon :deep(.app-icon) {
  display: block;
}

@keyframes nexus-bookmark-outline-press {
  0% {
    opacity: 1;
    transform: scale(1);
  }

  55% {
    opacity: 1;
    transform: translateY(1px) scale(0.78);
  }

  100% {
    opacity: 0;
    transform: translateY(1px) scale(0.78);
  }
}

@keyframes nexus-bookmark-pop-in {
  0% {
    opacity: 0;
    transform: translateY(1px) scale(0.78);
  }

  38% {
    opacity: 1;
    transform: translateY(-2px) scale(1.24);
  }

  68% {
    opacity: 1;
    transform: translateY(1px) scale(0.95);
  }

  84% {
    opacity: 1;
    transform: translateY(-0.5px) scale(1.04);
  }

  100% {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@keyframes nexus-bookmark-pop-out {
  0% {
    opacity: 1;
    transform: scale(1);
  }

  55% {
    opacity: 1;
    transform: translateY(1px) scale(0.86);
  }

  100% {
    opacity: 0;
    transform: translateY(0) scale(0.9);
  }
}

@keyframes nexus-bookmark-ring {
  0% {
    opacity: 0;
    transform: translate(-50%, -50%) scale(0.45);
  }

  20% {
    opacity: 0.22;
  }

  100% {
    opacity: 0;
    transform: translate(-50%, -50%) scale(1.35);
  }
}

@media (prefers-reduced-motion: reduce) {
  .bookmark-action {
    transition: none;
  }

  .bookmark-action__icon,
  .bookmark-action--bookmarking .bookmark-action__icon--outline,
  .bookmark-action--bookmarking .bookmark-action__icon--filled,
  .bookmark-action--unbookmarking .bookmark-action__icon--outline,
  .bookmark-action--unbookmarking .bookmark-action__icon--filled {
    transform: none;
    transition: none;
    animation: none;
  }

  .bookmark-action--bookmarking .bookmark-action__icon--outline {
    opacity: 0;
  }

  .bookmark-action--bookmarking .bookmark-action__icon--filled {
    opacity: 1;
  }

  .bookmark-action--bookmarking .bookmark-action__visual::after {
    animation: none;
    opacity: 0;
    transform: none;
  }

  .bookmark-action--unbookmarking .bookmark-action__icon--outline {
    opacity: 1;
  }

  .bookmark-action--unbookmarking .bookmark-action__icon--filled {
    opacity: 0;
  }
}
</style>
