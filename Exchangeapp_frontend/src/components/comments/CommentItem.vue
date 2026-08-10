<template>
  <article class="comment-item">
    <div class="comment-item__header">
      <AuthorIdentity :author="comment.author" :created-at="comment.created_at" />
      <div class="comment-item__tools">
        <button
          v-if="canDelete"
          class="comment-item__delete"
          type="button"
          :disabled="deleting"
          aria-label="Delete reply"
          title="Delete reply"
          @click.stop="emit('delete', comment.id)"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M5 7h14M10 11v6M14 11v6M8 7l1-3h6l1 3m-9 0 .8 13h6.4L15 7" />
          </svg>
        </button>
      </div>
    </div>
    <p class="comment-item__content">{{ comment.content }}</p>
    <span v-if="deleting" class="comment-item__status">Deleting...</span>
  </article>
</template>

<script setup lang="ts">
import type { ArticleComment } from '../../types/Comment';
import AuthorIdentity from '../AuthorIdentity.vue';

const props = defineProps<{
  comment: ArticleComment;
  canDelete: boolean;
  deleting: boolean;
}>();

const emit = defineEmits<{
  delete: [commentID: number];
}>();
</script>

<style scoped>
.comment-item {
  position: relative;
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--color-border);
}

.comment-item__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.comment-item__tools {
  display: inline-flex;
  align-items: center;
  margin-left: auto;
}


.comment-item__delete {
  display: grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
}

.comment-item__delete:hover:not(:disabled),
.comment-item__delete:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-danger);
}

.comment-item__delete:disabled {
  cursor: wait;
  opacity: 0.45;
}

.comment-item__delete svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.comment-item__content {
  margin: var(--space-3) 0 0 39px;
  color: var(--color-text);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.comment-item__status {
  display: block;
  margin: var(--space-2) 0 0 39px;
  color: var(--color-text-tertiary);
  font-size: 12px;
}

@media (max-width: 420px) {
  .comment-item__header {
    align-items: flex-start;
  }


  .comment-item__content,
  .comment-item__status {
    margin-left: 0;
  }
}
</style>
