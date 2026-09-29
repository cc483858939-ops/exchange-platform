<template>
  <dialog
    ref="dialogRef"
    class="post-drafts-dialog"
    aria-modal="true"
    :aria-labelledby="titleID"
    :aria-describedby="descriptionID"
    @cancel="handleCancel"
  >
    <div class="post-drafts-dialog__panel">
      <header class="post-drafts-dialog__header">
        <div>
          <h2 :id="titleID" class="post-drafts-dialog__title">Drafts</h2>
          <p :id="descriptionID" class="post-drafts-dialog__description">
            Saved on this device
          </p>
        </div>
        <button
          ref="closeButtonRef"
          class="post-drafts-dialog__close"
          type="button"
          aria-label="Close drafts"
          :disabled="busy"
          @click="emit('close')"
        >
          <span aria-hidden="true">×</span>
        </button>
      </header>

      <p v-if="loading" class="post-drafts-dialog__status" role="status">
        Loading drafts…
      </p>
      <p v-else-if="error" class="post-drafts-dialog__error" role="alert">
        {{ error }}
      </p>
      <p v-else-if="orderedDrafts.length === 0" class="post-drafts-dialog__status">
        No saved drafts yet.
      </p>
      <ul v-else class="post-drafts-dialog__list">
        <li v-for="draft in orderedDrafts" :key="draft.id" class="post-drafts-dialog__item">
          <div class="post-drafts-dialog__copy">
            <p class="post-drafts-dialog__preview">{{ preview(draft) }}</p>
            <p class="post-drafts-dialog__meta">
              <span>{{ mediaDescription(draft) }}</span>
              <time :datetime="isoDateTime(draft.updatedAt)">
                {{ formatUpdatedAt(draft.updatedAt) }}
              </time>
            </p>
          </div>
          <div class="post-drafts-dialog__actions">
            <button
              class="post-drafts-dialog__button post-drafts-dialog__button--open"
              type="button"
              :disabled="busy"
              @click="emit('open-draft', draft.id)"
            >
              Open
            </button>
            <button
              class="post-drafts-dialog__button post-drafts-dialog__button--delete"
              type="button"
              :disabled="busy"
              @click="emit('delete-draft', draft.id)"
            >
              Delete
            </button>
          </div>
        </li>
      </ul>
    </div>
  </dialog>
</template>

<script lang="ts">
let dialogSequence = 0;
const nextDialogID = () => `post-drafts-${++dialogSequence}`;
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { PersistedPostDraft } from '../../storage/postDraftRepository';

const props = withDefaults(defineProps<{
  drafts: PersistedPostDraft[];
  loading?: boolean;
  busy?: boolean;
  error?: string;
}>(), {
  loading: false,
  busy: false,
  error: '',
});

const emit = defineEmits<{
  close: [];
  'open-draft': [draftID: string];
  'delete-draft': [draftID: string];
}>();

const dialogRef = ref<HTMLDialogElement | null>(null);
const closeButtonRef = ref<HTMLButtonElement | null>(null);
const instanceID = nextDialogID();
const titleID = `${instanceID}-title`;
const descriptionID = `${instanceID}-description`;
const orderedDrafts = computed(() => [...props.drafts]
  .sort((left, right) => right.updatedAt - left.updatedAt || left.id.localeCompare(right.id)));

const preview = (draft: PersistedPostDraft) => {
  const content = draft.content.trim().replace(/\s+/g, ' ');
  if (!content) return 'Media post';
  return content.length > 140 ? `${content.slice(0, 137)}…` : content;
};

const mediaDescription = (draft: PersistedPostDraft) => {
  const count = draft.media.length;
  return count === 0 ? 'Text post' : `${count} image${count === 1 ? '' : 's'}`;
};

const formatUpdatedAt = (timestamp: number) => {
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(timestamp);
  } catch {
    return 'Recently updated';
  }
};

const isoDateTime = (timestamp: number) => {
  try {
    return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : undefined;
  } catch {
    return undefined;
  }
};

const handleCancel = (event: Event) => {
  event.preventDefault();
  if (!props.busy) emit('close');
};

onMounted(() => {
  const dialog = dialogRef.value;
  if (!dialog) return;
  if (typeof dialog.showModal === 'function') {
    try {
      if (!dialog.open) dialog.showModal();
    } catch {
      dialog.setAttribute('open', '');
    }
  } else {
    dialog.setAttribute('open', '');
  }
  closeButtonRef.value?.focus();
});

onBeforeUnmount(() => {
  const dialog = dialogRef.value;
  if (!dialog) return;
  if (dialog.open && typeof dialog.close === 'function') {
    dialog.close();
  } else {
    dialog.removeAttribute('open');
  }
});
</script>

<style scoped>
.post-drafts-dialog {
  width: min(calc(100% - 32px), 560px);
  max-width: calc(100% - 32px);
  max-height: min(80vh, 720px);
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  background: var(--color-surface);
  color: var(--color-text);
}

.post-drafts-dialog::backdrop {
  background: rgb(0 0 0 / 48%);
}

.post-drafts-dialog__panel {
  display: grid;
  max-height: min(80vh, 720px);
  overflow-y: auto;
  padding: var(--space-5);
}

.post-drafts-dialog__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border);
  padding-bottom: var(--space-3);
}

.post-drafts-dialog__title,
.post-drafts-dialog__description,
.post-drafts-dialog__status,
.post-drafts-dialog__error,
.post-drafts-dialog__preview,
.post-drafts-dialog__meta {
  margin: 0;
}

.post-drafts-dialog__title {
  font-size: 20px;
  font-weight: 800;
}

.post-drafts-dialog__description,
.post-drafts-dialog__meta,
.post-drafts-dialog__status {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.post-drafts-dialog__close {
  display: grid;
  width: 40px;
  height: 40px;
  flex: 0 0 auto;
  place-items: center;
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 26px;
}

.post-drafts-dialog__close:disabled {
  cursor: wait;
  opacity: 0.55;
}

.post-drafts-dialog__close:focus-visible,
.post-drafts-dialog__button:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.post-drafts-dialog__status,
.post-drafts-dialog__error {
  padding: var(--space-5) 0;
}

.post-drafts-dialog__error {
  color: var(--color-danger);
}

.post-drafts-dialog__list {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}

.post-drafts-dialog__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  border-bottom: 1px solid var(--color-border);
  padding: var(--space-4) 0;
}

.post-drafts-dialog__copy {
  min-width: 0;
}

.post-drafts-dialog__preview {
  overflow: hidden;
  color: var(--color-text);
  font-size: 15px;
  text-overflow: ellipsis;
}

.post-drafts-dialog__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
}

.post-drafts-dialog__actions {
  display: flex;
  flex: 0 0 auto;
  gap: var(--space-2);
}

.post-drafts-dialog__button {
  min-height: 40px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-3);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 750;
}

.post-drafts-dialog__button--open {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.post-drafts-dialog__button:disabled {
  cursor: wait;
  opacity: 0.55;
}

@media (max-width: 520px) {
  .post-drafts-dialog__panel {
    padding: var(--space-4);
  }

  .post-drafts-dialog__item {
    align-items: flex-start;
    gap: var(--space-2);
  }

  .post-drafts-dialog__actions {
    flex-direction: column;
  }
}
</style>
