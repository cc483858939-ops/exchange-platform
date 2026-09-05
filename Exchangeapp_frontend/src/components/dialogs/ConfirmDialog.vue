<template>
  <dialog
    ref="dialogRef"
    class="confirm-dialog"
    aria-modal="true"
    :aria-labelledby="titleID"
    :aria-describedby="descriptionID"
    @cancel="handleCancel"
  >
    <div class="confirm-dialog__panel">
      <h2 :id="titleID" class="confirm-dialog__title">{{ title }}</h2>
      <p :id="descriptionID" class="confirm-dialog__description">{{ description }}</p>
      <p
        v-if="error"
        :id="errorID"
        class="confirm-dialog__error"
        role="alert"
        aria-live="polite"
      >
        {{ error }}
      </p>
      <div class="confirm-dialog__actions">
        <button
          ref="cancelButtonRef"
          class="confirm-dialog__button confirm-dialog__button--cancel"
          type="button"
          :disabled="busy"
          @click="emit('cancel')"
        >
          {{ cancelLabel }}
        </button>
        <button
          class="confirm-dialog__button confirm-dialog__button--confirm"
          :class="{ 'confirm-dialog__button--danger': danger }"
          type="button"
          :disabled="busy"
          :aria-busy="busy ? 'true' : undefined"
          @click="emit('confirm')"
        >
          {{ busy ? 'Deleting…' : confirmLabel }}
        </button>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';

const props = withDefaults(defineProps<{
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  busy?: boolean;
  error?: string;
}>(), {
  confirmLabel: 'Confirm',
  cancelLabel: 'Cancel',
  danger: false,
  busy: false,
  error: '',
});

const emit = defineEmits<{
  confirm: [];
  cancel: [];
}>();

const dialogRef = ref<HTMLDialogElement | null>(null);
const cancelButtonRef = ref<HTMLButtonElement | null>(null);
const titleID = 'confirm-dialog-title';
const descriptionID = 'confirm-dialog-description';
const errorID = 'confirm-dialog-error';

const handleCancel = (event: Event) => {
  event.preventDefault();
  if (!props.busy) {
    emit('cancel');
  }
};

onMounted(() => {
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

  cancelButtonRef.value?.focus();
});

onBeforeUnmount(() => {
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
.confirm-dialog {
  width: min(calc(100% - 32px), 420px);
  max-width: calc(100% - 32px);
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  background: var(--color-surface);
  color: var(--color-text);
}

.confirm-dialog::backdrop {
  background: rgb(0 0 0 / 48%);
}

.confirm-dialog__panel {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-5);
}

.confirm-dialog__title,
.confirm-dialog__description,
.confirm-dialog__error {
  margin: 0;
}

.confirm-dialog__title {
  font-size: 20px;
  font-weight: 800;
}

.confirm-dialog__description {
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.confirm-dialog__error {
  color: var(--color-danger);
  font-size: 13px;
  line-height: 1.4;
}

.confirm-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.confirm-dialog__button {
  min-width: 88px;
  min-height: 44px;
  border: 1px solid transparent;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  font: inherit;
  font-size: 14px;
  font-weight: 750;
  cursor: pointer;
}

.confirm-dialog__button:disabled {
  cursor: wait;
  opacity: 0.55;
}

.confirm-dialog__button--cancel {
  border-color: var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-text);
}

.confirm-dialog__button--confirm {
  background: var(--color-accent);
  color: var(--color-on-accent, #fff);
}

.confirm-dialog__button--danger {
  background: var(--color-danger);
  color: #fff;
}

.confirm-dialog__button--cancel:hover:not(:disabled),
.confirm-dialog__button--cancel:focus-visible {
  background: var(--color-surface-subtle);
}

.confirm-dialog__button--confirm:hover:not(:disabled),
.confirm-dialog__button--confirm:focus-visible {
  filter: brightness(0.94);
}

.confirm-dialog__button:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

@media (max-width: 420px) {
  .confirm-dialog__actions {
    justify-content: stretch;
  }

  .confirm-dialog__button {
    flex: 1 1 0;
  }
}
</style>
