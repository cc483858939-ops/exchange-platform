<template>
  <button
    type="button"
    class="repost-action"
    :class="{
      'repost-action--compact': variant === 'compact',
      'repost-action--detail': variant === 'detail',
      'repost-action--reposted': reposted,
      'repost-action--disabled': disabled,
      'repost-action--loading': loading,
      'repost-action--pending': pending,
    }"
    :disabled="effectivelyDisabled"
    :aria-busy="loading || pending ? 'true' : undefined"
    :aria-pressed="reposted"
    :aria-label="ariaLabel"
    @click="activate"
  >
    <AppIcon name="repost" :size="18" />
    <span aria-hidden="true">{{ count }}</span>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import AppIcon from '../icons/AppIcon.vue';

const props = withDefaults(defineProps<{
  reposted: boolean;
  count: number;
  loading?: boolean;
  pending?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  variant?: 'compact' | 'detail';
}>(), {
  loading: false,
  pending: false,
  disabled: false,
  variant: 'compact' as const,
});

const emit = defineEmits<{
  toggle: [];
}>();

const effectivelyDisabled = computed(() => props.disabled || props.loading || props.pending);

const activate = () => {
  if (!effectivelyDisabled.value) {
    emit('toggle');
  }
};
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
  transition: color 140ms ease, background-color 140ms ease, opacity 140ms ease;
}

.repost-action--compact {
  margin: -8px 0;
}

.repost-action--detail {
  padding-inline: var(--space-3);
}

.repost-action--reposted {
  color: var(--color-accent);
}

.repost-action:hover:not(:disabled),
.repost-action:focus-visible {
  background: color-mix(in srgb, var(--color-accent) 9%, transparent);
  color: var(--color-accent);
}

.repost-action:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.repost-action:disabled {
  cursor: default;
}

.repost-action--disabled,
.repost-action--loading {
  opacity: 0.64;
}

.repost-action--pending {
  opacity: 0.82;
}

@media (prefers-reduced-motion: reduce) {
  .repost-action {
    transition: none;
  }
}
</style>
