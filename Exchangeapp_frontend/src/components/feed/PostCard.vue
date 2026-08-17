<template>
  <article ref="postCardRef" class="post-card">
    <div class="post-card__header">
      <AuthorIdentity :author="post.author" :created-at="post.createdAt" />

      <div class="post-card__more">
        <button
          ref="moreButtonRef"
          class="post-card__metric post-card__more-button"
          type="button"
          aria-label="More actions"
          aria-haspopup="menu"
          :aria-expanded="moreOpen"
          @click.stop="toggleMore"
        >
          <AppIcon name="more" :size="20" />
        </button>
        <div
          v-if="moreOpen"
          ref="menuRef"
          class="post-card__menu"
          role="menu"
          @keydown="handleMenuKeydown"
        >
          <button
            :ref="setMenuItemRef"
            class="post-card__menu-item"
            type="button"
            role="menuitem"
            @click.stop="copyLink"
          >
            <AppIcon name="link" :size="18" />
            <span>{{ copyActionLabel }}</span>
          </button>
          <button
            v-if="showNotInterested"
            :ref="setMenuItemRef"
            class="post-card__menu-item"
            type="button"
            role="menuitem"
            @click.stop="handleNotInterested"
          >
            <AppIcon name="eye-off" :size="18" />
            <span>Not interested</span>
          </button>
          <button
            v-if="showDelete"
            :ref="setMenuItemRef"
            class="post-card__menu-item post-card__menu-item--danger"
            type="button"
            role="menuitem"
            :disabled="deletePending"
            :aria-busy="deletePending"
            @click.stop="handleDeletePost"
          >
            <AppIcon name="trash" :size="18" />
            <span>Delete post</span>
          </button>
        </div>
        <span
          v-if="copyState !== 'idle'"
          class="post-card__copy-status"
          aria-live="polite"
        >
          {{ copyActionLabel }}
        </span>
        <span
          v-if="deleteError"
          class="post-card__delete-status"
          role="status"
          aria-live="polite"
        >
          {{ deleteError }}
        </span>
      </div>
    </div>

    <RouterLink
      class="post-card__content"
      :to="{ name: 'NewsDetail', params: { id: String(post.id) } }"
      @click="emit('articleClick', post)"
    >
      <h2 v-if="post.title.trim()" class="post-card__title">{{ post.title }}</h2>
      <p
        v-if="post.excerpt"
        class="post-card__excerpt"
        :class="{ 'post-card__excerpt--standalone': !post.title.trim() }"
      >
        {{ post.excerpt }}
      </p>
      <figure v-if="showCover" class="post-card__cover">
        <img
          :src="post.coverImageUrl"
          :alt="post.title.trim() || 'Post image'"
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
        <AppIcon name="reply" :size="18" />
        <span>{{ post.commentCount }}</span>
      </RouterLink>
      <button
        class="post-card__metric post-card__like"
        :class="{
          'post-card__like--active': post.likeStatus === 'ready' && post.liked,
          'post-card__like--animating-like': likeAnimation === 'like',
          'post-card__like--animating-unlike': likeAnimation === 'unlike',
        }"
        type="button"
        :disabled="likeDisabled"
        :aria-pressed="post.likeStatus === 'ready' ? post.liked : undefined"
        :aria-label="likeLabel"
        @click.stop="handleLikeActivation"
      >
        <span class="post-card__like-icon" aria-hidden="true">
          <AppIcon
            name="heart"
            :size="18"
            :filled="post.likeStatus === 'ready' && post.liked"
          />
          <span class="post-card__like-burst" aria-hidden="true">
            <span
              v-for="particle in 6"
              :key="particle"
              class="post-card__like-particle"
            ></span>
          </span>
        </span>
        <span class="post-card__like-count">{{ post.likeCount }}</span>
      </button>
      <span
        class="post-card__metric post-card__views"
        :aria-label="viewLabel"
        :title="viewLabel"
      >
        <AppIcon name="analytics" :size="18" />
        <span>{{ compactViewCount }}</span>
        <span class="sr-only">{{ viewLabel }}</span>
      </span>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import type { FeedPost } from '../../types/Feed';
import AuthorIdentity from '../AuthorIdentity.vue';
import AppIcon from '../icons/AppIcon.vue';
import { getArticleViewTelemetry } from '../../services/articleViewTelemetry';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../../utils/engagementCount';

const props = withDefaults(defineProps<{
  post: FeedPost;
  likePending?: boolean;
  showNotInterested?: boolean;
  showDelete?: boolean;
  deletePending?: boolean;
  deleteError?: string;
}>(), {
  likePending: false,
  showNotInterested: false,
  showDelete: false,
  deletePending: false,
  deleteError: '',
});

const emit = defineEmits<{
  articleClick: [post: FeedPost];
  toggleLike: [articleId: number];
  notInterested: [articleId: number];
  deletePost: [articleId: number];
}>();

const router = useRouter();
const articleViewTelemetry = getArticleViewTelemetry();
const postCardRef = ref<HTMLElement | null>(null);
const showCover = ref(Boolean(props.post.coverImageUrl));
const moreButtonRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLDivElement | null>(null);
const menuItemRefs = ref<HTMLButtonElement[]>([]);
const moreOpen = ref(false);
type CopyState = 'idle' | 'success' | 'error';
const copyState = ref<CopyState>('idle');
let copyRequestVersion = 0;
type LikeAnimation = 'like' | 'unlike' | null;
const likeAnimation = ref<LikeAnimation>(null);
let likeAnimationTimer: ReturnType<typeof setTimeout> | null = null;

const likeDisabled = computed(() =>
  props.post.likeStatus !== 'ready' || props.likePending,
);

const clearLikeAnimationTimer = () => {
  if (likeAnimationTimer !== null) {
    clearTimeout(likeAnimationTimer);
    likeAnimationTimer = null;
  }
};

const startLikeAnimation = (animation: Exclude<LikeAnimation, null>) => {
  clearLikeAnimationTimer();
  likeAnimation.value = animation;
  likeAnimationTimer = setTimeout(() => {
    likeAnimation.value = null;
    likeAnimationTimer = null;
  }, animation === 'like' ? 320 : 200);
};

const handleLikeActivation = () => {
  if (likeDisabled.value) {
    return;
  }

  const wasLiked = props.post.liked;
  startLikeAnimation(wasLiked ? 'unlike' : 'like');
  emit('toggleLike', props.post.id);
};

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

const compactViewCount = computed(() => formatCompactEngagementCount(props.post.viewCount));
const viewLabel = computed(() => formatAccessibleEngagementCount(props.post.viewCount, 'views'));

const observeCurrentPost = () => {
  if (postCardRef.value) {
    articleViewTelemetry.observeFeedCard(postCardRef.value, props.post.id);
  }
};

const copyActionLabel = computed(() => {
  if (copyState.value === 'success') {
    return 'Copied';
  }
  if (copyState.value === 'error') {
    return 'Copy failed';
  }
  return 'Copy link';
});

const syncMenuItemRefs = () => {
  menuItemRefs.value = menuRef.value
    ? Array.from(menuRef.value.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
    : [];
};

const getMenuItems = () => {
  syncMenuItemRefs();
  return menuItemRefs.value;
};

const setMenuItemRef = () => {
  void nextTick(syncMenuItemRefs);
};

const focusMenuItem = (index: number) => {
  const items = getMenuItems();
  if (items.length === 0) {
    return;
  }
  items[(index + items.length) % items.length]?.focus();
};

const removeOutsideListener = () => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown);
};

const closeMore = (restoreFocus = false) => {
  const wasOpen = moreOpen.value;
  copyRequestVersion += 1;
  moreOpen.value = false;
  copyState.value = 'idle';
  menuItemRefs.value = [];
  removeOutsideListener();
  if (restoreFocus && wasOpen) {
    void nextTick(() => moreButtonRef.value?.focus());
  }
};

const handleDocumentPointerDown = (event: PointerEvent) => {
  const target = event.target;
  if (!(target instanceof Node)) {
    return;
  }
  if (moreButtonRef.value?.contains(target) || menuRef.value?.contains(target)) {
    return;
  }
  closeMore();
};

const openMore = async () => {
  if (moreOpen.value) {
    return;
  }
  moreOpen.value = true;
  document.addEventListener('pointerdown', handleDocumentPointerDown);
  await nextTick();
  if (moreOpen.value) {
    focusMenuItem(0);
  }
};

const toggleMore = () => {
  if (moreOpen.value) {
    closeMore(true);
    return;
  }
  void openMore();
};

const handleMenuKeydown = (event: KeyboardEvent) => {
  const items = getMenuItems();
  if (items.length === 0) {
    return;
  }

  const currentIndex = items.indexOf(document.activeElement as HTMLButtonElement);
  if (event.key === 'ArrowDown') {
    event.preventDefault();
    focusMenuItem(currentIndex < 0 ? 0 : currentIndex + 1);
  } else if (event.key === 'ArrowUp') {
    event.preventDefault();
    focusMenuItem(currentIndex < 0 ? items.length - 1 : currentIndex - 1);
  } else if (event.key === 'Home') {
    event.preventDefault();
    focusMenuItem(0);
  } else if (event.key === 'End') {
    event.preventDefault();
    focusMenuItem(items.length - 1);
  } else if (event.key === 'Escape') {
    event.preventDefault();
    closeMore(true);
  } else if (event.key === 'Tab') {
    closeMore();
  }
};

const isCurrentCopyRequest = (requestVersion: number, articleId: number) =>
  requestVersion === copyRequestVersion
  && articleId === props.post.id
  && moreOpen.value;

const copyLink = async () => {
  const requestVersion = ++copyRequestVersion;
  const articleId = props.post.id;

  try {
    const resolved = router.resolve({
      name: 'NewsDetail',
      params: { id: String(articleId) },
    });
    const url = new URL(resolved.href, window.location.origin).toString();
    if (!navigator.clipboard) {
      throw new Error('Clipboard API unavailable');
    }
    await navigator.clipboard.writeText(url);

    if (!isCurrentCopyRequest(requestVersion, articleId)) {
      return;
    }

    copyState.value = 'success';
  } catch {
    if (!isCurrentCopyRequest(requestVersion, articleId)) {
      return;
    }

    copyState.value = 'error';
  }
};

const handleNotInterested = () => {
  closeMore();
  emit('notInterested', props.post.id);
};

const handleDeletePost = () => {
  if (props.deletePending) {
    return;
  }
  if (!window.confirm('Delete this post? This cannot be undone.')) {
    return;
  }

  closeMore();
  emit('deletePost', props.post.id);
};

watch(
  () => props.post.coverImageUrl,
  (coverImageUrl) => {
    showCover.value = Boolean(coverImageUrl);
  },
);

watch(
  () => props.post.id,
  (articleID, previousArticleID) => {
    if (articleID === previousArticleID) {
      return;
    }
    if (postCardRef.value) {
      articleViewTelemetry.unobserveFeedCard(postCardRef.value);
    }
    observeCurrentPost();
    closeMore();
    clearLikeAnimationTimer();
    likeAnimation.value = null;
  },
);

onMounted(observeCurrentPost);

const hideCover = () => {
  showCover.value = false;
};

onBeforeUnmount(() => {
  if (postCardRef.value) {
    articleViewTelemetry.unobserveFeedCard(postCardRef.value);
  }
  clearLikeAnimationTimer();
  closeMore();
});
</script>

<style scoped>
.post-card {
  --post-avatar-size: 40px;
  --post-column-gap: 12px;
  --post-like-color: #f91880;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
}

.post-card__header {
  display: flex;
  align-items: flex-start;
  min-width: 0;
}

.post-card__header :deep(.author-identity) {
  flex: 1 1 auto;
  width: auto;
  min-width: 0;
  gap: var(--post-column-gap);
}

.post-card__header :deep(.author-avatar) {
  width: var(--post-avatar-size);
  height: var(--post-avatar-size);
  font-size: 15px;
}

.post-card__header :deep(.author-copy) {
  display: flex;
  align-items: baseline;
  min-width: 0;
  overflow: hidden;
  gap: var(--space-2);
  line-height: 1.2;
}

.post-card__header :deep(.author-name) {
  flex: 0 1 auto;
  min-width: 0;
  color: var(--color-text);
  font-size: 14px;
  font-weight: 700;
}

.post-card__header :deep(.author-meta) {
  flex: 1 1 0;
  min-width: 0;
  color: var(--color-text-tertiary);
  font-size: 13px;
  font-weight: 400;
}

.post-card__more {
  position: relative;
  flex: 0 0 auto;
  margin-left: var(--space-2);
}

.post-card__content,
.post-card__engagement {
  margin-left: calc(var(--post-avatar-size) + var(--post-column-gap));
}

.post-card__content {
  display: block;
  color: inherit;
  text-decoration: none;
}

.post-card__content:focus-visible {
  border-radius: var(--radius-sm);
  outline: 2px solid var(--color-accent);
  outline-offset: 3px;
}

.post-card__title {
  margin: var(--space-2) 0 0;
  color: var(--color-text);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.45;
}

.post-card__excerpt {
  display: -webkit-box;
  max-height: calc(1.5em * 5);
  margin: var(--space-2) 0 0;
  overflow: hidden;
  color: var(--color-text);
  font-size: 15px;
  font-weight: 400;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 5;
  line-clamp: 5;
}

.post-card__excerpt--standalone {
  margin-top: 0;
}

.post-card__cover {
  box-sizing: border-box;
  aspect-ratio: 16 / 9;
  margin: var(--space-3) 0 0;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
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
  gap: var(--space-8);
  margin-top: var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.post-card__metric {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.post-card__like,
.post-card__reply {
  min-width: 40px;
  min-height: 40px;
  margin: -8px 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
}

.post-card__like {
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__reply {
  color: inherit;
  text-decoration: none;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__more-button {
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

.post-card__menu {
  position: absolute;
  top: calc(100% + var(--space-1));
  right: 0;
  z-index: 20;
  display: grid;
  min-width: 148px;
  gap: 2px;
  padding: var(--space-1);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
}

.post-card__menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 36px;
  border: 0;
  border-radius: calc(var(--radius-sm) - 2px);
  padding: 0 var(--space-3);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  text-align: left;
  white-space: nowrap;
}

.post-card__menu-item .app-icon {
  flex: 0 0 18px;
}

.post-card__menu-item:hover,
.post-card__menu-item:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-card__menu-item--danger {
  color: var(--color-danger);
}

.post-card__menu-item--danger:hover:not(:disabled),
.post-card__menu-item--danger:focus-visible {
  color: var(--color-danger);
}

.post-card__menu-item--danger:disabled {
  cursor: wait;
  opacity: 0.64;
}

.post-card__copy-status {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}

.post-card__delete-status {
  display: block;
  margin-top: var(--space-2);
  color: var(--color-danger);
  font-size: 12px;
}

.post-card__like:hover:not(:disabled),
.post-card__like:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--post-like-color);
}

.post-card__reply:hover,
.post-card__reply:focus-visible,
.post-card__more-button:hover,
.post-card__more-button:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-card__like:active:not(:disabled),
.post-card__reply:active,
.post-card__more-button:active {
  transform: scale(0.97);
}

.post-card__like:disabled {
  cursor: default;
  opacity: 0.72;
}

.post-card__like--animating-like:disabled,
.post-card__like--animating-unlike:disabled {
  opacity: 1;
}

.post-card__like--active {
  color: var(--post-like-color);
}

.post-card__reply .app-icon,
.post-card__like-icon .app-icon {
  width: 18px;
  height: 18px;
}

.post-card__more-button .app-icon {
  width: 20px;
  height: 20px;
}

.post-card__like-icon {
  position: relative;
  display: inline-flex;
  width: 16px;
  height: 16px;
  flex: 0 0 16px;
  align-items: center;
  justify-content: center;
  overflow: visible;
}

.post-card__like-burst {
  position: absolute;
  inset: 50% auto auto 50%;
  width: 0;
  height: 0;
  pointer-events: none;
}

.post-card__like-particle {
  position: absolute;
  top: -1.25px;
  left: -1.25px;
  width: 2.5px;
  height: 2.5px;
  border-radius: 50%;
  background: var(--post-like-color);
  opacity: 0;
  pointer-events: none;
}

.post-card__like-particle:nth-child(1) {
  --particle-x: 0px;
  --particle-y: -9px;
}

.post-card__like-particle:nth-child(2) {
  --particle-x: 8px;
  --particle-y: -5px;
}

.post-card__like-particle:nth-child(3) {
  --particle-x: 8px;
  --particle-y: 5px;
}

.post-card__like-particle:nth-child(4) {
  --particle-x: 0px;
  --particle-y: 9px;
}

.post-card__like-particle:nth-child(5) {
  --particle-x: -8px;
  --particle-y: 5px;
}

.post-card__like-particle:nth-child(6) {
  --particle-x: -8px;
  --particle-y: -5px;
}

@keyframes post-like-burst-pop {
  0% {
    transform: scale(1);
  }

  15% {
    transform: scale(0.92);
  }

  48% {
    transform: scale(1.4);
  }

  72% {
    transform: scale(0.98);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes post-unlike-release {
  0% {
    transform: scale(1);
  }

  42% {
    transform: scale(0.88);
  }

  100% {
    transform: scale(1);
  }
}

@keyframes post-like-count-in {
  0% {
    transform: translateY(2px);
    opacity: 0.7;
  }

  100% {
    transform: translateY(0);
    opacity: 1;
  }
}

@keyframes post-unlike-count-out {
  0% {
    transform: translateY(-1px);
    opacity: 0.7;
  }

  100% {
    transform: translateY(0);
    opacity: 1;
  }
}

@keyframes post-like-particle-burst {
  0% {
    transform: translate(0, 0) scale(0.55);
    opacity: 0;
  }

  18% {
    opacity: 0.85;
  }

  100% {
    transform: translate(var(--particle-x), var(--particle-y)) scale(0.7);
    opacity: 0;
  }
}

.post-card__like--animating-like .post-card__like-icon {
  animation: post-like-burst-pop 280ms ease-out both;
}

.post-card__like--animating-unlike .post-card__like-icon {
  animation: post-unlike-release 170ms ease-out both;
}

.post-card__like--animating-like .post-card__like-particle {
  animation: post-like-particle-burst 220ms ease-out 60ms both;
}

.post-card__like--animating-like .post-card__like-count {
  animation: post-like-count-in 160ms ease-out both;
}

.post-card__like--animating-unlike .post-card__like-count {
  animation: post-unlike-count-out 130ms ease-out both;
}

@media (max-width: 420px) {
  .post-card {
    --post-avatar-size: 36px;
    --post-column-gap: 10px;
    padding: var(--space-3);
  }

  .post-card__header :deep(.author-copy) {
    gap: var(--space-1);
  }

  .post-card__header :deep(.author-name) {
    font-size: 14px;
  }

  .post-card__header :deep(.author-meta) {
    font-size: 13px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .post-card__like,
  .post-card__reply,
  .post-card__more-button {
    transition: none;
  }

  .post-card__like--animating-like .post-card__like-icon,
  .post-card__like--animating-unlike .post-card__like-icon,
  .post-card__like--animating-like .post-card__like-count,
  .post-card__like--animating-unlike .post-card__like-count {
    animation: none;
  }

  .post-card__like--animating-like:disabled,
  .post-card__like--animating-unlike:disabled {
    opacity: 0.72;
  }

  .post-card__like-particle {
    display: none;
  }
}
</style>
