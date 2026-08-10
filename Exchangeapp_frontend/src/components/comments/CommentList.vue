<template>
  <section class="comment-list" aria-label="Replies">
    <p v-if="comments.length === 0" class="comment-list__empty">
      No replies yet. Be the first to reply.
    </p>

    <template v-else>
      <CommentItem
        v-for="comment in comments"
        :key="comment.id"
        :comment="comment"
        :can-delete="Boolean(currentIdentity && currentIdentity.id === comment.author.id)"
        :deleting="deletingCommentId === comment.id"
        @delete="emit('delete', $event)"
      />
    </template>

    <div ref="sentinelRef" class="comment-list__sentinel" aria-live="polite">
      <span v-if="loadingMore" class="comment-list__status">Loading more replies...</span>
      <template v-else-if="loadMoreError">
        <span class="comment-list__error">{{ loadMoreError }}</span>
        <button class="comment-list__retry" type="button" @click="emit('retry')">Retry</button>
      </template>
      <button
        v-else-if="hasNext"
        class="comment-list__load-more"
        type="button"
        @click="emit('loadMore')"
      >
        Load more replies
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { ArticleComment } from '../../types/Comment';
import type { AuthIdentity } from '../../utils/authIdentity';
import CommentItem from './CommentItem.vue';

const props = defineProps<{
  comments: ArticleComment[];
  currentIdentity: AuthIdentity | null;
  deletingCommentId: number | null;
  hasNext: boolean;
  loadingMore: boolean;
  loadMoreError: string;
}>();

const emit = defineEmits<{
  loadMore: [];
  retry: [];
  delete: [commentID: number];
}>();

const sentinelRef = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const connectObserver = () => {
  disconnectObserver();

  if (
    !props.hasNext ||
    props.loadingMore ||
    props.loadMoreError ||
    typeof IntersectionObserver === 'undefined' ||
    !sentinelRef.value
  ) {
    return;
  }

  const handleIntersections: IntersectionObserverCallback = entries => {
    if (entries.some(entry => entry.isIntersecting)) {
      emit('loadMore');
    }
  };

  observer = new IntersectionObserver(handleIntersections, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};

watch(
  [() => props.hasNext, () => props.loadingMore, () => props.loadMoreError],
  () => {
    void nextTick(connectObserver);
  },
  { flush: 'post' },
);

onMounted(() => {
  void nextTick(connectObserver);
});

onBeforeUnmount(disconnectObserver);
</script>

<style scoped>
.comment-list {
  min-width: 0;
}

.comment-list__empty {
  margin: 0;
  padding: var(--space-8) 0;
  color: var(--color-text-secondary);
  text-align: center;
}

.comment-list__sentinel {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-3) 0 var(--space-5);
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.comment-list__error {
  color: var(--color-danger);
}

.comment-list__retry,
.comment-list__load-more {
  border: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font-size: 12px;
  font-weight: 750;
}

.comment-list__retry:hover,
.comment-list__load-more:hover {
  text-decoration: underline;
}

.comment-list__status {
  color: var(--color-text-tertiary);
}
</style>
