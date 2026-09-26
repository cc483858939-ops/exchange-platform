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
        <BookmarkAction
          class="reply-item__bookmark"
          :bookmarked="bookmarkStatus === 'ready' && bookmarkState.bookmarked"
          :disabled="bookmarkStatus === 'unavailable'"
          :loading="bookmarkStatus === 'unknown'"
          :pending="bookmarkPending"
          :ariaLabel="bookmarkLabel"
          variant="compact"
          @toggle="emit('toggleBookmark', reply.id)"
        />
      </div>
    </div>
    <div class="reply-item__content">
      <LinkifiedText :text="reply.content" :to="threadDestination" />
      <PostMediaGrid
        v-if="reply.media.length > 0"
        :media="reply.media"
        interactive
        @open="handleOpenMedia"
      />
    </div>
    <div class="reply-item__engagement" aria-label="Reply engagement">
      <RouterLink
        class="reply-item__reply-action"
        :to="replyDestination"
        :aria-label="replyActionLabel"
      >
        <AppIcon name="reply" :size="16" />
        <span>{{ replyActionText }}</span>
      </RouterLink>
    </div>
    <span v-if="deleting" class="reply-item__status">Deleting...</span>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Post } from '../../types/Post';
import type { FeedBookmarkStatus } from '../../types/Feed';
import BookmarkAction from '../engagement/BookmarkAction.vue';
import AuthorIdentity from '../AuthorIdentity.vue';
import LinkifiedText from '../content/LinkifiedText.vue';
import PostMediaGrid from '../content/PostMediaGrid.vue';
import AppIcon from '../icons/AppIcon.vue';

const props = defineProps<{
  reply: Post;
  canDelete: boolean;
  deleting: boolean;
  bookmarkState?: { bookmarked: boolean; status: FeedBookmarkStatus };
  bookmarkPending?: boolean;
}>();

const emit = defineEmits<{
  requestDelete: [replyID: number];
  toggleBookmark: [replyID: number];
  openMedia: [media: Post['media'], index: number];
}>();

const bookmarkState = computed(() => props.bookmarkState ?? {
  bookmarked: false,
  status: 'unknown' as const,
});
const bookmarkStatus = computed(() => bookmarkState.value.status);
const bookmarkPending = computed(() => props.bookmarkPending ?? false);
const bookmarkLabel = computed(() => (
  bookmarkStatus.value === 'unavailable'
    ? 'Bookmark unavailable'
    : bookmarkState.value.bookmarked ? 'Remove bookmark' : 'Bookmark reply'
));
const threadDestination = computed(() => ({
  name: 'PostDetail' as const,
  params: {
    id: String(props.reply.id),
  },
}));
const replyDestination = computed(() => ({
  name: 'PostDetail' as const,
  params: {
    id: String(props.reply.id),
  },
  query: {
    reply: '1',
  },
}));
const replyCount = computed(() => Math.max(0, props.reply.reply_count));
const replyActionText = computed(() => (
  replyCount.value > 0 ? String(replyCount.value) : 'Reply'
));
const replyActionLabel = computed(() => {
  if (replyCount.value === 0) {
    return 'Reply to this reply';
  }

  if (replyCount.value === 1) {
    return '1 reply. Continue discussion';
  }

  return `${replyCount.value} replies. Continue discussion`;
});

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

.reply-item__bookmark {
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

.reply-item__bookmark:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .reply-item__bookmark:hover:not(:disabled) {
    background: var(--color-surface-subtle);
    color: var(--color-accent);
  }
}

.reply-item__bookmark:disabled {
  cursor: default;
  opacity: 0.55;
}

.reply-item__delete:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-danger);
}

@media (hover: hover) and (pointer: fine) {
  .reply-item__delete:hover:not(:disabled) {
    background: var(--color-surface-subtle);
    color: var(--color-danger);
  }
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

.reply-item__engagement {
  display: flex;
  align-items: center;
  margin-top: var(--space-2);
}

.reply-item__reply-action {
  display: inline-flex;
  min-height: 36px;
  align-items: center;
  gap: var(--space-2);
  margin-left: 39px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 13px;
  text-decoration: none;
  transition: background-color 140ms ease, color 140ms ease;
}

.reply-item__reply-action:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .reply-item__reply-action:hover {
    background: var(--color-surface-subtle);
    color: var(--color-accent);
  }
}

.reply-item__reply-action .app-icon {
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
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
