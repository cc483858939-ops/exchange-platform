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
    @click.stop="activate"
  >
    <BookmarkMorphIcon
      :size="variant === 'detail' ? 20 : 18"
      :bookmarked="visualBookmarked"
      :motion="motion"
    />
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import BookmarkMorphIcon from './BookmarkMorphIcon.vue';

type BookmarkActionVariant = 'compact' | 'detail';
type BookmarkMotion = 'idle' | 'bookmarking' | 'unbookmarking';

const bookmarkMotionDurationMs = 540;
const unbookmarkMotionDurationMs = 300;

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
</style>
