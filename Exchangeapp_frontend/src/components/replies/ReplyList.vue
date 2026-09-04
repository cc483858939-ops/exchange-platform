<template>
  <section class="reply-list" aria-label="Replies">
    <p v-if="replies.length === 0" class="reply-list__empty">
      No replies yet. Be the first to reply.
    </p>

    <template v-else>
      <ReplyItem
        v-for="reply in replies"
        :key="reply.id"
        :reply="reply"
        :can-delete="Boolean(currentIdentity && currentIdentity.id === reply.author.id)"
        :deleting="deletingReplyId === reply.id"
        @delete="emit('delete', $event)"
        @open-media="handleOpenMedia"
      />
    </template>

    <div ref="sentinelRef" class="reply-list__sentinel" aria-live="polite">
      <span v-if="loadingMore" class="reply-list__status">Loading more replies...</span>
      <template v-else-if="loadMoreError">
        <span class="reply-list__error">{{ loadMoreError }}</span>
        <button class="reply-list__retry" type="button" @click="emit('retry')">Retry</button>
      </template>
      <button
        v-else-if="hasNext"
        class="reply-list__load-more"
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
import type { Post } from '../../types/Post';
import type { AuthIdentity } from '../../utils/authIdentity';
import ReplyItem from './ReplyItem.vue';

const props = defineProps<{
  replies: Post[];
  currentIdentity: AuthIdentity | null;
  deletingReplyId: number | null;
  hasNext: boolean;
  loadingMore: boolean;
  loadMoreError: string;
}>();

const emit = defineEmits<{
  loadMore: [];
  retry: [];
  delete: [replyID: number];
  openMedia: [media: Post['media'], index: number];
}>();

const handleOpenMedia = (media: Post['media'], index: number) => {
  emit('openMedia', media, index);
};

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
.reply-list {
  min-width: 0;
}

.reply-list__empty {
  margin: 0;
  padding: var(--space-8) 0;
  color: var(--color-text-secondary);
  text-align: center;
}

.reply-list__sentinel {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-3) 0 var(--space-5);
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.reply-list__error {
  color: var(--color-danger);
}

.reply-list__retry,
.reply-list__load-more {
  border: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font-size: 12px;
  font-weight: 750;
}

.reply-list__retry:hover,
.reply-list__load-more:hover {
  text-decoration: underline;
}

.reply-list__status {
  color: var(--color-text-tertiary);
}
</style>
