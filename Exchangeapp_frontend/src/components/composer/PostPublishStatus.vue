<template>
  <aside
    v-if="operation || postPublishStore.recoveryError"
    class="post-publish-status"
    role="status"
    aria-live="polite"
  >
    <span class="post-publish-status__message">{{ message }}</span>
    <button
      v-if="operation?.phase === 'failed' && operation.failureKind === 'retryable'"
      class="post-publish-status__retry"
      :disabled="abandonBusy"
      type="button"
      @click="retryPublish"
    >
      Retry
    </button>
    <button
      v-if="operation?.phase === 'failed'"
      class="post-publish-status__discard"
      :disabled="abandonBusy"
      type="button"
      @click="requestAbandon"
    >
      Discard attempt
    </button>
  </aside>

  <ConfirmDialog
    v-if="abandonConfirmationOpen"
    title="Discard this post attempt?"
    description="This attempt may already have been posted. Discarding stops recovery on this device and does not delete anything already published. Check your profile before posting the same content again."
    confirm-label="Discard attempt"
    cancel-label="Cancel"
    danger
    :busy="abandonBusy"
    :error="abandonError"
    @confirm="confirmAbandon"
    @cancel="cancelAbandon"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { usePostPublishStore } from '../../store/postPublish';
import ConfirmDialog from '../dialogs/ConfirmDialog.vue';

const postPublishStore = usePostPublishStore();
const operation = computed(() => postPublishStore.latestOperation);
const abandonConfirmationOpen = ref(false);
const abandonOperationID = ref<string | null>(null);
const abandonBusy = ref(false);
const abandonError = ref('');
const message = computed(() => {
  switch (operation.value?.phase) {
    case 'uploading':
      return 'Uploading post...';
    case 'publishing':
      return 'Posting...';
    case 'failed':
      return operation.value?.error || 'Couldn’t confirm this post.';
    case 'succeeded':
      return 'Post sent.';
    default:
      return postPublishStore.recoveryError;
  }
});

const retryPublish = () => {
  const operationID = operation.value?.id;
  if (operationID) {
    postPublishStore.retry(operationID);
  }
};

const requestAbandon = () => {
  if (operation.value?.phase !== 'failed') return;
  abandonOperationID.value = operation.value.id;
  abandonError.value = '';
  abandonConfirmationOpen.value = true;
};

const cancelAbandon = () => {
  if (abandonBusy.value) return;
  abandonConfirmationOpen.value = false;
  abandonOperationID.value = null;
  abandonError.value = '';
};

const confirmAbandon = async () => {
  const operationID = abandonOperationID.value;
  if (!operationID || abandonBusy.value) return;

  abandonBusy.value = true;
  abandonError.value = '';
  try {
    if (await postPublishStore.abandonFailedOperation(operationID)) {
      abandonConfirmationOpen.value = false;
      abandonOperationID.value = null;
      return;
    }
    abandonError.value = 'Couldn’t discard this post attempt on this device. Try again.';
  } catch {
    abandonError.value = 'Couldn’t discard this post attempt on this device. Try again.';
  } finally {
    abandonBusy.value = false;
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

.post-publish-status__discard {
  flex: 0 0 auto;
  border: 0;
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-weight: 700;
  cursor: pointer;
}

.post-publish-status__retry:disabled,
.post-publish-status__discard:disabled {
  cursor: wait;
  opacity: 0.55;
}

.post-publish-status__retry:focus-visible,
.post-publish-status__discard:focus-visible {
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
