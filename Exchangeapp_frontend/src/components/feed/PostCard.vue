<template>
  <article class="post-card">
    <AuthorIdentity :author="post.author" :created-at="post.createdAt" />

    <RouterLink
      class="post-card__content"
      :to="{ name: 'NewsDetail', params: { id: String(post.id) } }"
      @click="emit('articleClick', post)"
    >
      <h2 class="post-card__title">{{ post.title }}</h2>
      <p v-if="post.excerpt" class="post-card__excerpt">{{ post.excerpt }}</p>
      <figure v-if="showCover" class="post-card__cover">
        <img
          :src="post.coverImageUrl"
          :alt="post.title"
          loading="lazy"
          @error="hideCover"
        />
      </figure>
    </RouterLink>

    <div class="post-card__engagement" aria-label="Engagement">
      <RouterLink
        class="post-card__metric post-card__reply"
        :to="{
          name: 'NewsDetail',
          params: { id: String(post.id) },
          query: { reply: '1' },
        }"
        :aria-label="replyLabel"
        @click="emit('articleClick', post)"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 11.5a7.5 7.5 0 0 1-7.5 7.5H8l-4 2v-5.2A7.5 7.5 0 1 1 20 11.5Z" />
        </svg>
        <span>{{ post.commentCount }}</span>
      </RouterLink>
      <button
        class="post-card__metric post-card__like"
        :class="{ 'post-card__like--active': post.likeStatus === 'ready' && post.liked }"
        type="button"
        :disabled="likeDisabled"
        :aria-pressed="post.likeStatus === 'ready' ? post.liked : undefined"
        :aria-label="likeLabel"
        @click.stop="emit('toggleLike', post.id)"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20.8 8.9c0 5.2-8.8 10.2-8.8 10.2S3.2 14.1 3.2 8.9A4.7 4.7 0 0 1 12 6.6a4.7 4.7 0 0 1 8.8 2.3Z" />
        </svg>
        <span>{{ post.likeCount }}</span>
      </button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { FeedPost } from '../../types/Feed';
import AuthorIdentity from '../AuthorIdentity.vue';

const props = withDefaults(defineProps<{
  post: FeedPost;
  likePending?: boolean;
}>(), {
  likePending: false,
});

const emit = defineEmits<{
  articleClick: [post: FeedPost];
  toggleLike: [articleId: number];
}>();

const showCover = ref(Boolean(props.post.coverImageUrl));

const likeDisabled = computed(() =>
  props.post.likeStatus !== 'ready' || props.likePending,
);

const likeLabel = computed(() => {
  const countLabel = String(props.post.likeCount)
    + (props.post.likeCount === 1 ? ' like' : ' likes');
  if (props.post.likeStatus === 'unavailable') {
    return 'Like unavailable, ' + countLabel;
  }
  if (props.post.likeStatus !== 'ready' || !props.post.liked) {
    return 'Like post, ' + countLabel;
  }
  return 'Unlike post, ' + countLabel;
});

const replyLabel = computed(() => {
  const countLabel = String(props.post.commentCount)
    + (props.post.commentCount === 1 ? ' reply' : ' replies');
  return 'Reply to post, ' + countLabel;
});

watch(
  () => props.post.coverImageUrl,
  (coverImageUrl) => {
    showCover.value = Boolean(coverImageUrl);
  },
);

const hideCover = () => {
  showCover.value = false;
};
</script>

<style scoped>
.post-card {
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
}

.post-card > .author-identity {
  margin-bottom: var(--space-3);
}

.post-card__content {
  display: block;
  color: inherit;
  text-decoration: none;
}

.post-card__title {
  margin: 0;
  color: var(--color-text);
  font-size: 20px;
  line-height: 1.22;
  letter-spacing: -0.025em;
}

.post-card__content:hover .post-card__title,
.post-card__content:focus-visible .post-card__title {
  color: var(--color-accent);
}

.post-card__excerpt {
  display: -webkit-box;
  margin: var(--space-2) 0 0;
  overflow: hidden;
  color: var(--color-text-secondary);
  font-size: 14px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.post-card__cover {
  aspect-ratio: 16 / 9;
  margin: var(--space-4) 0 0;
  overflow: hidden;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.post-card__cover img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.post-card__engagement {
  display: flex;
  align-items: center;
  gap: var(--space-5);
  margin-top: var(--space-3);
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.post-card__metric {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.post-card__like {
  min-width: 40px;
  min-height: 40px;
  margin: -8px 0;
  border: 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__reply {
  min-width: 40px;
  min-height: 40px;
  margin: -8px 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
  color: inherit;
  text-decoration: none;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__like:hover:not(:disabled),
.post-card__like:focus-visible,
.post-card__reply:hover,
.post-card__reply:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-card__like:active:not(:disabled),
.post-card__reply:active {
  transform: scale(0.97);
}

.post-card__like:disabled {
  cursor: default;
  opacity: 0.72;
}

.post-card__like--active {
  color: var(--color-accent);
}

.post-card__metric svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.post-card__like--active svg {
  fill: currentColor;
}

@media (max-width: 420px) {
  .post-card {
    padding: var(--space-4);
  }

  .post-card__title {
    font-size: 19px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .post-card__like {
    transition: none;
  }
}
</style>
