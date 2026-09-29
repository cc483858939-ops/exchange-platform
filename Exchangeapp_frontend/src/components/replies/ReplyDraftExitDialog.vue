<template>
  <dialog
    ref="dialogRef"
    class="reply-draft-exit-dialog"
    aria-modal="true"
    :aria-labelledby="titleID"
    :aria-describedby="descriptionID"
    :aria-busy="busy ? 'true' : undefined"
    @cancel="handleCancel"
  >
    <div class="reply-draft-exit-dialog__panel">
      <h2 :id="titleID" class="reply-draft-exit-dialog__title">
        {{ isSavedDraft ? 'Save changes?' : 'Save reply?' }}
      </h2>
      <p :id="descriptionID" class="reply-draft-exit-dialog__description">
        {{ isSavedDraft
          ? 'Save your changes to this reply draft before leaving?'
          : 'Save this reply as a draft before leaving?' }}
      </p>
      <p v-if="error" class="reply-draft-exit-dialog__error" role="alert" aria-live="polite">
        {{ error }}
      </p>
      <div class="reply-draft-exit-dialog__actions">
        <button
          ref="cancelButtonRef"
          class="reply-draft-exit-dialog__button reply-draft-exit-dialog__button--cancel"
          type="button"
          :disabled="busy"
          @click="emit('cancel')"
        >Cancel</button>
        <button
          class="reply-draft-exit-dialog__button reply-draft-exit-dialog__button--discard"
          type="button"
          :disabled="busy"
          @click="emit('discard')"
        >{{ isSavedDraft ? 'Discard changes' : 'Discard' }}</button>
        <button
          class="reply-draft-exit-dialog__button reply-draft-exit-dialog__button--save"
          type="button"
          :disabled="busy"
          :aria-busy="busy ? 'true' : undefined"
          @click="emit('save')"
        >{{ busy ? 'Saving…' : isSavedDraft ? 'Save' : 'Save draft' }}</button>
      </div>
    </div>
  </dialog>
</template>

<script lang="ts">
let replyExitDialogSequence = 0;
const nextDialogID = () => `reply-draft-exit-${++replyExitDialogSequence}`;
</script>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';

const props = withDefaults(defineProps<{ isSavedDraft: boolean; busy?: boolean; error?: string }>(), {
  busy: false,
  error: '',
});

const emit = defineEmits<{
  cancel: [];
  discard: [];
  save: [];
}>();

const dialogRef = ref<HTMLDialogElement | null>(null);
const cancelButtonRef = ref<HTMLButtonElement | null>(null);
const instanceID = nextDialogID();
const titleID = `${instanceID}-title`;
const descriptionID = `${instanceID}-description`;

const handleCancel = (event: Event) => {
  event.preventDefault();
  if (!props.busy) emit('cancel');
};

onMounted(() => {
  const dialog = dialogRef.value;
  if (!dialog) return;
  if (typeof dialog.showModal === 'function') {
    try { if (!dialog.open) dialog.showModal(); } catch { dialog.setAttribute('open', ''); }
  } else dialog.setAttribute('open', '');
  cancelButtonRef.value?.focus();
});

onBeforeUnmount(() => {
  const dialog = dialogRef.value;
  if (!dialog) return;
  if (dialog.open && typeof dialog.close === 'function') dialog.close();
  else dialog.removeAttribute('open');
});
</script>

<style scoped>
.reply-draft-exit-dialog {
  width: min(calc(100% - 32px), 440px);
  max-width: calc(100% - 32px);
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  background: var(--color-surface);
  color: var(--color-text);
}

.reply-draft-exit-dialog::backdrop { background: rgb(0 0 0 / 48%); }
.reply-draft-exit-dialog__panel { display: grid; gap: var(--space-3); padding: var(--space-5); }
.reply-draft-exit-dialog__title,
.reply-draft-exit-dialog__description,
.reply-draft-exit-dialog__error { margin: 0; }
.reply-draft-exit-dialog__title { font-size: 20px; font-weight: 800; }
.reply-draft-exit-dialog__description { color: var(--color-text-secondary); line-height: 1.5; }
.reply-draft-exit-dialog__error { color: var(--color-danger); font-size: 13px; line-height: 1.4; }
.reply-draft-exit-dialog__actions { display: flex; justify-content: flex-end; gap: var(--space-2); margin-top: var(--space-2); }
.reply-draft-exit-dialog__button {
  min-width: 82px;
  min-height: 44px;
  border: 1px solid transparent;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-3);
  font: inherit;
  font-size: 14px;
  font-weight: 750;
  cursor: pointer;
}
.reply-draft-exit-dialog__button:disabled { cursor: wait; opacity: 0.55; }
.reply-draft-exit-dialog__button--cancel,
.reply-draft-exit-dialog__button--discard { border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text); }
.reply-draft-exit-dialog__button--save { background: var(--color-accent); color: var(--color-on-accent, #fff); }
.reply-draft-exit-dialog__button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
@media (max-width: 460px) {
  .reply-draft-exit-dialog__actions { flex-wrap: wrap; }
  .reply-draft-exit-dialog__button { flex: 1 1 100px; }
}
</style>
