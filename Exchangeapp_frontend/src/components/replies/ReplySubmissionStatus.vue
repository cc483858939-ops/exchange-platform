<template>
  <section
    v-if="operation"
    class="reply-submission-status"
    aria-live="polite"
    :aria-busy="operation.phase === 'publishing' || busy ? 'true' : undefined"
    :data-phase="operation.phase"
  >
    <p v-if="operation.phase === 'publishing'" class="reply-submission-status__message" role="status">
      Sending reply…
    </p>
    <p v-else-if="operation.phase === 'succeeded'" class="reply-submission-status__message" role="status">
      {{ operation.cleanupPending ? 'Reply posted. Finishing draft cleanup…' : 'Reply posted.' }}
    </p>
    <template v-else>
      <p class="reply-submission-status__message" role="status">
        {{ operation.failureKind === 'idempotency_conflict'
          ? 'This reply can’t be retried safely.'
          : 'Reply failed. Retry safely.' }}
      </p>
      <div class="reply-submission-status__actions">
        <button
          v-if="operation.failureKind === 'retryable'"
          class="reply-submission-status__action"
          type="button"
          :disabled="busy"
          @click="emit('retry')"
        >Retry</button>
        <button
          class="reply-submission-status__action reply-submission-status__action--quiet"
          type="button"
          :disabled="busy"
          @click="emit('discard')"
        >Discard attempt</button>
      </div>
    </template>
    <p v-if="error" class="reply-submission-status__error" role="alert">{{ error }}</p>
  </section>
</template>

<script setup lang="ts">
defineProps<{
  operation: {
    phase: 'publishing' | 'failed' | 'succeeded';
    failureKind: 'retryable' | 'idempotency_conflict' | null;
    cleanupPending?: boolean;
  } | null;
  busy?: boolean;
  error?: string;
}>();

const emit = defineEmits<{
  retry: [];
  discard: [];
}>();
</script>

<style scoped>
.reply-submission-status {
  display: grid;
  gap: var(--space-2);
  margin: 0 0 var(--space-3);
  border-left: 3px solid var(--color-border-strong);
  padding: var(--space-2) var(--space-3);
  color: var(--color-text-secondary);
  font-size: 13px;
}
.reply-submission-status[data-phase='failed'] { border-left-color: var(--color-danger); }
.reply-submission-status__message,
.reply-submission-status__error { margin: 0; }
.reply-submission-status__error { color: var(--color-danger); }
.reply-submission-status__actions { display: flex; gap: var(--space-2); }
.reply-submission-status__action {
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-3);
  background: var(--color-accent);
  color: var(--color-on-accent, #fff);
  cursor: pointer;
  font: inherit;
  font-weight: 750;
}
.reply-submission-status__action--quiet { border: 1px solid var(--color-border-strong); background: transparent; color: var(--color-text); }
.reply-submission-status__action:disabled { cursor: wait; opacity: 0.5; }
</style>
