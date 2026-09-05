<template>
  <article class="reply-item">
    <div class="reply-item__header">
      <AuthorIdentity :author="reply.author" :created-at="reply.created_at" />
      <div class="reply-item__tools">
        <button
          v-if="canDelete"
          class="reply-item__delete"
          type="button"
          :disabled="deleting"
          aria-label="Delete reply"
          title="Delete reply"
          @click.stop="emit('requestDelete', reply.id)"
        >
          <AppIcon name="trash" :size="16" />
        </button>
      </div>
    </div>
    <div class="reply-item__content">
      <LinkifiedText :text="reply.content" />
      <PostMediaGrid
        v-if="reply.media.length > 0"
        :media="reply.media"
        interactive
        @open="handleOpenMedia"
      />
    </div>
    <span v-if="deleting" class="reply-item__status">Deleting...</span>
  </article>
</template>

<script setup lang="ts">
import type { Post } from '../../types/Post';
import AuthorIdentity from '../AuthorIdentity.vue';
import LinkifiedText from '../content/LinkifiedText.vue';
import PostMediaGrid from '../content/PostMediaGrid.vue';
import AppIcon from '../icons/AppIcon.vue';

const props = defineProps<{
  reply: Post;
  canDelete: boolean;
  deleting: boolean;
}>();

const emit = defineEmits<{
  requestDelete: [replyID: number];
  openMedia: [media: Post['media'], index: number];
}>();

const handleOpenMedia = (index: number) => {
  emit('openMedia', props.reply.media, index);
};
</script>

<style scoped>
.reply-item {
  position: relative;
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--color-border);
}

.reply-item__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.reply-item__tools {
  display: inline-flex;
  align-items: center;
  margin-left: auto;
}


.reply-item__delete {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
}

.reply-item__delete:hover:not(:disabled),
.reply-item__delete:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-danger);
}

.reply-item__delete:disabled {
  cursor: wait;
  opacity: 0.45;
}

.reply-item__delete .app-icon {
  width: 16px;
  height: 16px;
}

.reply-item__content {
  margin: var(--space-3) 0 0 39px;
  color: var(--color-text);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.reply-item__content :deep(.post-media-grid) {
  margin-top: var(--space-3);
}

.reply-item__status {
  display: block;
  margin: var(--space-2) 0 0 39px;
  color: var(--color-text-tertiary);
  font-size: 12px;
}

@media (max-width: 420px) {
  .reply-item__header {
    align-items: flex-start;
  }
}
</style>
