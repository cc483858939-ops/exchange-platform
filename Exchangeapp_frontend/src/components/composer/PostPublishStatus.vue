<template>
  <aside
    v-if="operation"
    class="post-publish-status"
    role="status"
    aria-live="polite"
  >
    <span class="post-publish-status__message">{{ message }}</span>
    <button
      v-if="operation.phase === 'failed'"
      class="post-publish-status__retry"
      type="button"
      @click="retryPublish"
    >
      Retry
    </button>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { usePostPublishStore } from '../../store/postPublish';

const postPublishStore = usePostPublishStore();
const operation = computed(() => postPublishStore.latestOperation);
const message = computed(() => {
  switch (operation.value?.phase) {
    case 'uploading':
      return 'Uploading post...';
    case 'publishing':
      return 'Posting...';
    case 'failed':
      return 'Couldn’t confirm this post.';
    case 'succeeded':
      return 'Post sent.';
    default:
      return '';
  }
});

const retryPublish = () => {
  const operationID = operation.value?.id;
  if (operationID) {
    postPublishStore.retry(operationID);
  }
};
</script>

<style scoped>
.post-publish-status {
  position: fixed;
  right: max(16px, env(safe-area-inset-right));
  bottom: calc(var(--mobile-bottom-nav-height) + var(--mobile-safe-bottom) + 16px);
  z-index: 30;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: min(420px, calc(100vw - 32px));
  padding: 12px 14px;
  border: 1px solid color-mix(in srgb, var(--color-accent) 24%, var(--color-border));
  border-radius: 14px;
  background: color-mix(in srgb, var(--color-surface) 96%, var(--color-accent));
  box-shadow: 0 12px 30px rgb(24 28 36 / 14%);
  color: var(--color-text);
  font-size: 13px;
}

.post-publish-status__message {
  min-width: 0;
}

.post-publish-status__retry {
  flex: 0 0 auto;
  border: 0;
  background: transparent;
  color: var(--color-accent);
  font: inherit;
  font-weight: 700;
  cursor: pointer;
}

.post-publish-status__retry:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 3px;
}

@media (min-width: 800px) {
  .post-publish-status {
    right: 24px;
    bottom: 24px;
  }
}
</style>
