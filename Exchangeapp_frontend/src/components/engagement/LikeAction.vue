<template>
  <button
    type="button"
    class="like-action"
    :class="{
      'like-action--compact': variant === 'compact',
      'like-action--detail': variant === 'detail',
      'like-action--liked': liked,
      'like-action--disabled': disabled,
      'like-action--loading': loading,
      'like-action--pending': pending,
      'like-action--liking': motion === 'liking',
      'like-action--unliking': motion === 'unliking',
    }"
    :disabled="effectivelyDisabled"
    :aria-busy="loading || pending ? 'true' : undefined"
    :aria-pressed="resolvedAriaPressed"
    :aria-label="ariaLabel"
    :data-motion="motion"
    @click="activate"
  >
    <span class="like-action__visual" aria-hidden="true">
      <span class="like-action__halo"></span>
      <span class="like-action__particles">
        <span
          v-for="particle in particles"
          :key="particle.id"
          class="like-action__particle"
          :class="'like-action__particle--' + particle.shape"
        ></span>
      </span>
      <span class="like-action__heart">
        <AppIcon
          name="heart"
          :size="variant === 'detail' ? 20 : 18"
          :filled="liked"
        />
      </span>
    </span>

    <span
      class="like-action__count-window"
      :data-count-transition="countTransitionName"
      aria-hidden="true"
    >
      <Transition :name="countTransitionName">
        <span :key="count" class="like-action__count">{{ count }}</span>
      </Transition>
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import AppIcon from '../icons/AppIcon.vue';

type LikeActionVariant = 'compact' | 'detail';
type LikeMotion = 'idle' | 'liking' | 'unliking';
type CountIntent = 'up' | 'down' | null;
type CountTransition = 'like-count-up' | 'like-count-down' | 'like-count-fade';

type ParticleShape = 'dot' | 'diamond';

const props = withDefaults(defineProps<{
  liked: boolean;
  count: number;
  disabled?: boolean;
  loading?: boolean;
  pending?: boolean;
  variant?: LikeActionVariant;
  ariaLabel: string;
  ariaPressed?: boolean | null;
}>(), {
  disabled: false,
  loading: false,
  pending: false,
  variant: 'compact' as const,
  ariaPressed: undefined,
});

const emit = defineEmits<{
  toggle: [];
}>();

const particles: Array<{ id: number; shape: ParticleShape }> = [
  { id: 1, shape: 'dot' },
  { id: 2, shape: 'dot' },
  { id: 3, shape: 'dot' },
  { id: 4, shape: 'dot' },
  { id: 5, shape: 'diamond' },
  { id: 6, shape: 'diamond' },
  { id: 7, shape: 'diamond' },
  { id: 8, shape: 'diamond' },
];

const motion = ref<LikeMotion>('idle');
const expectedLiked = ref<boolean | null>(null);
const expectedStateObserved = ref(false);
const countIntent = ref<CountIntent>(null);
const awaitingIntentCount = ref(false);
const countTransitionName = ref<CountTransition>('like-count-fade');
let motionTimer: ReturnType<typeof setTimeout> | null = null;

const effectivelyDisabled = computed(() =>
  props.disabled || props.loading || props.pending,
);

const resolvedAriaPressed = computed<boolean | undefined>(() => {
  if (props.ariaPressed === null) {
    return undefined;
  }

  if (typeof props.ariaPressed === 'boolean') {
    return props.ariaPressed;
  }

  return props.liked;
});

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
  expectedLiked.value = null;
  expectedStateObserved.value = false;
  clearIntent();
  countTransitionName.value = 'like-count-fade';
};

const startMotion = (nextMotion: Exclude<LikeMotion, 'idle'>) => {
  clearMotionTimer();
  motion.value = nextMotion;
  motionTimer = setTimeout(() => {
    motion.value = 'idle';
    motionTimer = null;
    expectedLiked.value = null;
    expectedStateObserved.value = false;
    clearIntent();
  }, nextMotion === 'liking' ? 300 : 160);
};

const armIntentCountDirection = (intent: Exclude<CountIntent, null>) => {
  countIntent.value = intent;
  awaitingIntentCount.value = true;
  countTransitionName.value = 'like-count-fade';
};

const activate = () => {
  if (effectivelyDisabled.value) {
    return;
  }

  const nextLiked = !props.liked;
  expectedLiked.value = nextLiked;
  expectedStateObserved.value = false;

  startMotion(nextLiked ? 'liking' : 'unliking');
  armIntentCountDirection(nextLiked ? 'up' : 'down');
  emit('toggle');
};

watch(
  () => props.liked,
  nextLiked => {
    if (motion.value === 'idle' || expectedLiked.value === null) {
      return;
    }

    if (nextLiked === expectedLiked.value) {
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
        ? 'like-count-up'
        : 'like-count-down'
      : 'like-count-fade';
    clearIntent();
  },
);

onBeforeUnmount(() => {
  clearMotionTimer();
});
</script>

<style scoped>
.like-action {
  position: relative;
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
  transition: color 140ms ease, background-color 140ms ease, opacity 140ms ease;
}

.like-action--compact {
  margin: -8px 0;
}

.like-action--detail {
  min-height: 40px;
  padding-inline: var(--space-2);
}

.like-action--liked {
  color: var(--color-like);
}

.like-action:hover:not(:disabled),
.like-action:focus-visible {
  background: color-mix(in srgb, var(--color-like) 9%, transparent);
  color: var(--color-like);
}

.like-action:focus-visible {
  outline: 2px solid var(--color-like);
  outline-offset: 2px;
}

.like-action:disabled {
  cursor: default;
}

.like-action--disabled,
.like-action--loading {
  opacity: 0.64;
}

.like-action--pending {
  opacity: 1;
}

.like-action__visual {
  position: relative;
  display: inline-flex;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  align-items: center;
  justify-content: center;
  overflow: visible;
}

.like-action__heart {
  position: relative;
  z-index: 2;
  display: inline-flex;
  transform-origin: center;
}

.like-action__heart :deep(.app-icon) {
  display: block;
}

.like-action__halo {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 0;
  width: 8px;
  height: 8px;
  border: 1.25px solid var(--color-like);
  border-radius: 999px;
  opacity: 0;
  pointer-events: none;
  transform: translate(-50%, -50%) scale(0.35);
}

.like-action__particles {
  position: absolute;
  inset: 0;
  z-index: 1;
  pointer-events: none;
}

.like-action__particle {
  --particle-rotation: 0deg;
  --particle-x: 0px;
  --particle-y: -14px;
  position: absolute;
  top: 50%;
  left: 50%;
  width: 2.5px;
  height: 2.5px;
  border-radius: 50%;
  background: var(--color-like);
  opacity: 0;
  pointer-events: none;
  transform: translate(0, 0) rotate(var(--particle-rotation)) scale(0.4);
}

.like-action__particle--diamond {
  width: 3px;
  height: 3px;
  border-radius: 0.5px;
}

.like-action__particle:nth-child(1) {
  --particle-x: 0px;
  --particle-y: -14px;
}

.like-action__particle:nth-child(2) {
  --particle-x: 12px;
  --particle-y: -7px;
}

.like-action__particle:nth-child(3) {
  --particle-x: 14px;
  --particle-y: 5px;
}

.like-action__particle:nth-child(4) {
  --particle-x: 5px;
  --particle-y: 14px;
}

.like-action__particle:nth-child(5) {
  --particle-x: -8px;
  --particle-y: 13px;
  --particle-rotation: 45deg;
}

.like-action__particle:nth-child(6) {
  --particle-x: -15px;
  --particle-y: 4px;
  --particle-rotation: 45deg;
}

.like-action__particle:nth-child(7) {
  --particle-x: -12px;
  --particle-y: -8px;
  --particle-rotation: 45deg;
}

.like-action__particle:nth-child(8) {
  --particle-x: -4px;
  --particle-y: -15px;
  --particle-rotation: 45deg;
}

.like-action__particle:nth-child(n + 6) {
  background: var(--color-like-soft);
}

.like-action__count-window {
  display: inline-grid;
  min-width: 1.5ch;
  align-items: center;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.like-action__count {
  grid-area: 1 / 1;
  text-align: left;
}

@keyframes nexus-like-heart {
  0% {
    transform: scale(1);
  }

  18% {
    transform: scale(0.86);
  }

  46% {
    transform: scale(1.12);
  }

  70% {
    transform: scale(0.97);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes nexus-unlike-heart {
  0% {
    transform: scale(1);
  }

  42% {
    transform: scale(0.88);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes nexus-like-halo {
  0% {
    transform: translate(-50%, -50%) scale(0.35);
    opacity: 0;
  }

  22% {
    opacity: 0.3;
  }

  65% {
    opacity: 0.16;
  }

  100% {
    transform: translate(-50%, -50%) scale(1.3);
    opacity: 0;
  }
}

@keyframes nexus-like-particle {
  0% {
    transform: translate(0, 0) rotate(var(--particle-rotation)) scale(0.4);
    opacity: 0;
  }

  20% {
    opacity: 0.8;
  }

  100% {
    transform: translate(var(--particle-x), var(--particle-y))
      rotate(var(--particle-rotation)) scale(0.75);
    opacity: 0;
  }
}

.like-action--liking .like-action__heart {
  animation: nexus-like-heart 300ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.like-action--unliking .like-action__heart {
  animation: nexus-unlike-heart 160ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.like-action--liking .like-action__halo {
  animation: nexus-like-halo 240ms ease-out 60ms both;
}

.like-action--liking .like-action__particle {
  animation: nexus-like-particle 190ms cubic-bezier(0.22, 1, 0.36, 1) 75ms both;
}

.like-count-up-enter-active,
.like-count-up-leave-active {
  transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1), opacity 160ms ease;
}

.like-count-up-enter-from {
  opacity: 0;
  transform: translateY(4px);
}

.like-count-up-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.like-count-down-enter-active,
.like-count-down-leave-active {
  transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1), opacity 160ms ease;
}

.like-count-down-enter-from {
  opacity: 0;
  transform: translateY(-4px);
}

.like-count-down-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

.like-count-fade-enter-active,
.like-count-fade-leave-active {
  transition: opacity 120ms ease;
}

.like-count-fade-enter-from,
.like-count-fade-leave-to {
  opacity: 0.65;
}

@media (prefers-reduced-motion: reduce) {
  .like-action {
    transition: none;
  }

  .like-action--liking .like-action__heart,
  .like-action--unliking .like-action__heart,
  .like-action--liking .like-action__halo,
  .like-action--liking .like-action__particle {
    animation: none;
  }

  .like-action__halo,
  .like-action__particle {
    opacity: 0;
  }

  .like-count-up-enter-active,
  .like-count-up-leave-active,
  .like-count-down-enter-active,
  .like-count-down-leave-active,
  .like-count-fade-enter-active,
  .like-count-fade-leave-active {
    transition: none;
  }
}
</style>
