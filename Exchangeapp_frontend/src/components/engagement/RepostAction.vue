<template>
  <button
    type="button"
    class="repost-action"
    :class="{
      'repost-action--compact': variant === 'compact',
      'repost-action--detail': variant === 'detail',
      'repost-action--reposted': visualReposted,
      'repost-action--disabled': disabled,
      'repost-action--loading': loading,
      'repost-action--pending': pending,
      'repost-action--reposting': motion === 'reposting',
      'repost-action--unreposting': motion === 'unreposting',
    }"
    :disabled="effectivelyDisabled"
    :aria-busy="loading || pending ? 'true' : undefined"
    :aria-pressed="reposted"
    :aria-label="ariaLabel"
    :data-motion="motion"
    :style="motionStyle"
    @click="activate"
  >
    <span class="repost-action__visual" aria-hidden="true">
      <span class="repost-action__icon">
        <span class="repost-action__icon-glyph">
          <AppIcon
            name="repost"
            :size="variant === 'detail' ? 20 : 18"
          />
        </span>
      </span>
    </span>

    <span
      class="repost-action__count-window"
      :data-count-transition="countTransitionName"
      aria-hidden="true"
    >
      <Transition :name="countTransitionName">
        <span :key="count" class="repost-action__count">{{ count }}</span>
      </Transition>
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';

type RepostActionVariant = 'compact' | 'detail';
type RepostMotion = 'idle' | 'reposting' | 'unreposting';
type CountIntent = 'up' | 'down' | null;
type CountTransition = 'repost-count-up' | 'repost-count-down' | 'repost-count-fade';

const repostMotionDurationMs = 400;
const unrepostMotionDurationMs = 300;

const props = withDefaults(defineProps<{
  reposted: boolean;
  count: number;
  loading?: boolean;
  pending?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  variant?: RepostActionVariant;
}>(), {
  loading: false,
  pending: false,
  disabled: false,
  variant: 'compact' as const,
});

const emit = defineEmits<{
  toggle: [];
}>();

const motion = ref<RepostMotion>('idle');
const expectedReposted = ref<boolean | null>(null);
const expectedStateObserved = ref(false);
const countIntent = ref<CountIntent>(null);
const awaitingIntentCount = ref(false);
const countTransitionName = ref<CountTransition>('repost-count-fade');
let motionTimer: ReturnType<typeof setTimeout> | null = null;

const motionStyle = computed(() => ({
  '--repost-motion-duration': `${repostMotionDurationMs}ms`,
  '--unrepost-motion-duration': `${unrepostMotionDurationMs}ms`,
}));

const visualReposted = computed(() => {
  if (motion.value === 'reposting') {
    return true;
  }

  if (motion.value === 'unreposting') {
    return false;
  }

  return props.reposted;
});

const effectivelyDisabled = computed(() => props.disabled || props.loading || props.pending);

const clearMotionTimer = () => {
  if (motionTimer !== null) {
    clearTimeout(motionTimer);
    motionTimer = null;
  }
};

const clearIntent = () => {
  countIntent.value = null;
  awaitingIntentCount.value = false;
};

const cancelMotion = () => {
  clearMotionTimer();
  motion.value = 'idle';
  expectedReposted.value = null;
  expectedStateObserved.value = false;
  clearIntent();
  countTransitionName.value = 'repost-count-fade';
};

const startMotion = (nextMotion: Exclude<RepostMotion, 'idle'>) => {
  clearMotionTimer();
  motion.value = nextMotion;
  motionTimer = setTimeout(() => {
    motion.value = 'idle';
    motionTimer = null;
    expectedReposted.value = null;
    expectedStateObserved.value = false;
    clearIntent();
  }, nextMotion === 'reposting' ? repostMotionDurationMs : unrepostMotionDurationMs);
};

const armIntentCountDirection = (intent: Exclude<CountIntent, null>) => {
  countIntent.value = intent;
  awaitingIntentCount.value = true;
  countTransitionName.value = 'repost-count-fade';
};

const activate = () => {
  if (effectivelyDisabled.value) {
    return;
  }

  const nextReposted = !props.reposted;
  expectedReposted.value = nextReposted;
  expectedStateObserved.value = false;

  startMotion(nextReposted ? 'reposting' : 'unreposting');
  armIntentCountDirection(nextReposted ? 'up' : 'down');
  emit('toggle');
};

watch(
  () => props.reposted,
  nextReposted => {
    if (motion.value === 'idle' || expectedReposted.value === null) {
      return;
    }

    if (nextReposted === expectedReposted.value) {
      expectedStateObserved.value = true;
      return;
    }

    if (expectedStateObserved.value) {
      cancelMotion();
    }
  },
);

watch(
  () => props.count,
  (nextCount, previousCount) => {
    if (nextCount === previousCount) {
      return;
    }

    const movedUp = nextCount > previousCount;
    const intentMatches = awaitingIntentCount.value
      && (
        (countIntent.value === 'up' && movedUp)
        || (countIntent.value === 'down' && !movedUp)
      );

    countTransitionName.value = intentMatches
      ? countIntent.value === 'up'
        ? 'repost-count-up'
        : 'repost-count-down'
      : 'repost-count-fade';
    clearIntent();
  },
);

onBeforeUnmount(() => {
  clearMotionTimer();
});
</script>

<style scoped>
.repost-action {
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
  color: var(--color-text-tertiary);
  cursor: pointer;
  font: inherit;
  line-height: 1;
  white-space: nowrap;
  transition: color 140ms ease, opacity 140ms ease;
}

.repost-action--compact {
  margin: -8px 0;
}

.repost-action--detail {
  padding-inline: var(--space-3);
}

.repost-action--reposted {
  color: var(--color-repost);
}

.repost-action:focus-visible {
  background: transparent;
  color: var(--color-repost);
  outline: 2px solid var(--color-repost);
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .repost-action:hover:not(:disabled) {
    background: transparent;
    color: var(--color-repost);
  }
}

.repost-action__visual {
  isolation: isolate;
  position: relative;
  display: inline-flex;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  align-items: center;
  justify-content: center;
  overflow: visible;
}

.repost-action__visual::before,
.repost-action__visual::after {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 0;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  content: '';
  pointer-events: none;
  transform: translate(-50%, -50%);
}

.repost-action__visual::before {
  background: transparent;
  transition: background-color 140ms ease;
}

.repost-action__visual::after {
  background: var(--color-repost);
  opacity: 0;
}

@media (hover: hover) and (pointer: fine) {
  .repost-action:hover:not(:disabled) .repost-action__visual::before {
    background: color-mix(in srgb, var(--color-repost) 10%, transparent);
  }
}

.repost-action:focus-visible .repost-action__visual::before {
  background: color-mix(in srgb, var(--color-repost) 10%, transparent);
}

.repost-action__icon {
  position: relative;
  z-index: 1;
  display: inline-flex;
  transform-origin: center;
}

.repost-action__icon-glyph {
  display: inline-flex;
  transform-origin: center;
}

.repost-action__icon :deep(.app-icon) {
  display: block;
}

.repost-action--reposting .repost-action__icon {
  animation: nexus-repost-rotate-in var(--repost-motion-duration) cubic-bezier(0.25, 0.6, 0.35, 1) both;
}

.repost-action--reposting .repost-action__icon-glyph {
  animation: nexus-repost-scale-in var(--repost-motion-duration) cubic-bezier(0.25, 0.6, 0.35, 1) both;
}

.repost-action--unreposting .repost-action__icon {
  animation: nexus-repost-rotate-out var(--unrepost-motion-duration) cubic-bezier(0.25, 0.6, 0.35, 1) both;
}

.repost-action--unreposting .repost-action__icon-glyph {
  animation: nexus-repost-scale-out var(--unrepost-motion-duration) cubic-bezier(0.25, 0.6, 0.35, 1) both;
}

.repost-action--reposting .repost-action__visual::after {
  animation: nexus-repost-halo var(--repost-motion-duration) ease-out both;
}

.repost-action__count-window {
  display: inline-grid;
  min-width: 1.5ch;
  align-items: center;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.repost-action__count {
  grid-area: 1 / 1;
  text-align: left;
}

.repost-action:disabled {
  cursor: default;
}

.repost-action--pending {
  opacity: 1;
}

.repost-action--disabled,
.repost-action--loading {
  opacity: 0.64;
}

@keyframes nexus-repost-rotate-in {
  0% {
    transform: rotate(0turn);
  }

  18% {
    transform: rotate(0.16turn);
  }

  100% {
    transform: rotate(1turn);
  }
}

@keyframes nexus-repost-rotate-out {
  0% {
    transform: rotate(0turn);
  }

  25% {
    transform: rotate(0.18turn);
  }

  100% {
    transform: rotate(1turn);
  }
}

@keyframes nexus-repost-scale-in {
  0% {
    transform: scale(1);
  }

  20% {
    transform: scale(0.96);
  }

  55% {
    transform: scale(1.03);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes nexus-repost-scale-out {
  0% {
    transform: scale(1);
  }

  25% {
    transform: scale(0.97);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes nexus-repost-halo {
  0% {
    opacity: 0;
    transform: translate(-50%, -50%) scale(0.65);
  }

  35% {
    opacity: 0.16;
    transform: translate(-50%, -50%) scale(1);
  }

  100% {
    opacity: 0;
    transform: translate(-50%, -50%) scale(1.35);
  }
}

.repost-count-up-enter-active,
.repost-count-up-leave-active,
.repost-count-down-enter-active,
.repost-count-down-leave-active {
  transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1), opacity 160ms ease;
}

.repost-count-up-enter-from {
  opacity: 0;
  transform: translateY(4px);
}

.repost-count-up-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.repost-count-down-enter-from {
  opacity: 0;
  transform: translateY(-4px);
}

.repost-count-down-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

.repost-count-fade-enter-active,
.repost-count-fade-leave-active {
  transition: opacity 120ms ease;
}

.repost-count-fade-enter-from,
.repost-count-fade-leave-to {
  opacity: 0.65;
}

@media (prefers-reduced-motion: reduce) {
  .repost-action {
    transition: none;
  }

  .repost-action__visual::before {
    transition: none;
  }

  .repost-action--reposting .repost-action__icon,
  .repost-action--unreposting .repost-action__icon,
  .repost-action--reposting .repost-action__icon-glyph,
  .repost-action--unreposting .repost-action__icon-glyph,
  .repost-action--reposting .repost-action__visual::after {
    animation: none;
  }

  .repost-action--reposting .repost-action__visual::after {
    opacity: 0;
  }

  .repost-count-up-enter-active,
  .repost-count-up-leave-active,
  .repost-count-down-enter-active,
  .repost-count-down-leave-active,
  .repost-count-fade-enter-active,
  .repost-count-fade-leave-active {
    transition: none;
  }
}
</style>
